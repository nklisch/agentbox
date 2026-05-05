# Design: Phase 2 — Kit pipeline, base kit, `agentbox build`

## Overview

Phase 2 stands up the kit composition pipeline and ships the first real kit. After this phase, `agentbox build base` produces a working Debian image with the in-box DX bundle (zsh + starship + modern CLI replacements + zellij + the `box` helpers). The kit pipeline is composable for future phases: P5 will drop in `polyglot`, `node`, etc., as additional kit dirs without touching the resolver or generator.

The phase splits into two parts:

- **Part A — Kit infrastructure (no podman calls).** Types, parser, registry, resolver, content hasher, Dockerfile generator, cache, and the orchestrator with a `Runner` port. All unit-tested with fake filesystems and fake runners. Part A can land and be verified with `go test ./...` before any kit content exists.
- **Part B — Base kit content + adapter + CLI wireup.** The actual `kits/base/` files (manifest, packages, install, env, `box` helpers), the `PodmanRunner` adapter for the port, the embed accessor, and `internal/cli/build.go` replacing the Phase 1 stub. The Phase 2 ROADMAP test checkpoint runs after Part B.

The architectural rules from Phase 1 carry forward: domain packages stay cobra-free; same-path bind mounts are non-negotiable elsewhere; secrets pass by name only.

## Cross-cutting decisions

- **Where built-in kits live in source.** Built-in kits live at `internal/builtinkits/kits/<name>/` — *inside* the embedding package, not at the project root. This is required by Go's `//go:embed`: patterns can't contain `..` or `/` prefixes, so kits can't sit two directories up from their embedder. Putting them in `internal/builtinkits/kits/` keeps `//go:embed all:kits` clean and the import graph honest. (See Decision Log entry that I'll add to PROGRESS.md.)
- **Reuse `runspec.KitImageTag`.** The canonical tag formula already lives in Phase 1's `internal/runspec/runspec.go`. The resolver imports it; Phase 2 doesn't redefine it.
- **Package layout.** One new domain package — `internal/kits` — contains the entire pipeline: types, parsing, registry, resolution, hashing, Dockerfile generation, cache, and orchestration. The `Runner` interface is also defined there; the `PodmanRunner` adapter is a separate file in the same package, but uses only `os/exec` (no kit-specific logic). The `internal/builtinkits` package contains only the embedded FS accessor — kept tiny so it's hard to accidentally introduce bidirectional dependencies.
- **`Runner` port stays domain-side.** The interface is in `internal/kits` (the domain). `PodmanRunner` implements it. Tests substitute a fake. This is consistent with Phase 1's `runspec` (structured args + `ToShell`; future podman calls will plug into the runspec package the same way).
- **Empty `packages.txt` handling.** The generated Dockerfile pipes `packages.txt` through `grep -v` to strip comments and blank lines, then `xargs -r` (GNU `--no-run-if-empty`) so apt isn't invoked when the file has only comments. Debian uses GNU xargs by default — this is safe.
- **Build context staging.** `Builder.Build` writes the resolved kit list to a temp dir as `<ctxdir>/kits/<name>/<files…>` so `podman build` sees a normal context. The temp dir is cleaned on success; preserved on failure (with a stderr note) so the user can debug.
- **`box info` env-var contract.** The `box info` script reads `AGENTBOX_*` environment variables that agentbox will set at container-create time in Phase 3. For Phase 2's test checkpoint (plain `podman run`, no agentbox), those vars are unset and `box info` prints `n/a` for them — exit 0. This deliberately defers the "real labels visible to box info" decision to Phase 3 where it belongs (with the rest of the container lifecycle).

---

## Part A — Kit infrastructure (units 1–9)

### Unit 1: `internal/kits/types.go`

**File**: `internal/kits/types.go`

```go
package kits

import "io/fs"

// Manifest mirrors manifest.toml.
type Manifest struct {
	Name            string   `toml:"name"`
	Description     string   `toml:"description"`
	DependsOn       []string `toml:"depends_on"`        // nil → defaulted to ["base"] for non-"base" kits
	AgentboxVersion string   `toml:"agentbox_version"`  // optional semver constraint; checked at resolve time
	Provides        []string `toml:"provides"`
	ConflictsWith   []string `toml:"conflicts_with"`
}

// Kit is a loaded kit: manifest + access to the kit's directory.
type Kit struct {
	Manifest Manifest
	FS       fs.FS  // rooted at the kit's own dir; reading "manifest.toml" works
	Source   string // "builtin" or "user:<abspath>"
}

// KitInfo is the lightweight description used by `agentbox build --list`.
type KitInfo struct {
	Name        string
	Description string
	Source      string // "builtin" | "user"
}

// Resolved is the output of dependency resolution: a topologically-sorted,
// deduped list of Kits, plus the canonical image tag.
type Resolved struct {
	Kits []Kit  // base first, then in topological order
	Tag  string // from runspec.KitImageTag(Names())
}

// Names returns the kit names of a Resolved in resolution order.
func (r Resolved) Names() []string {
	out := make([]string, len(r.Kits))
	for i, k := range r.Kits {
		out[i] = k.Manifest.Name
	}
	return out
}
```

**Acceptance criteria**:
- [ ] `Resolved.Names()` returns names in `Kits` order.
- [ ] No file I/O in this file.

---

### Unit 2: `internal/kits/parse.go`

**File**: `internal/kits/parse.go`

```go
package kits

import (
	"errors"
	"fmt"
	"io/fs"
	"regexp"

	"github.com/BurntSushi/toml"
)

// Required files in every kit dir.
var requiredFiles = []string{"manifest.toml", "packages.txt", "install.sh", "env.sh"}

// nameRE matches valid kit names ([a-z0-9_-]+).
var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// Load loads a kit from an fs.FS rooted at the kit's directory.
// Verifies the four required files exist; parses + validates manifest.toml;
// applies the depends_on default (["base"] for non-"base" kits).
func Load(name string, kitFS fs.FS, source string) (Kit, error) {
	if !nameRE.MatchString(name) {
		return Kit{}, fmt.Errorf("invalid kit name %q (must match [a-z0-9_-]+)", name)
	}

	for _, f := range requiredFiles {
		if _, err := fs.Stat(kitFS, f); err != nil {
			return Kit{}, fmt.Errorf("kit %q: missing required file %s", name, f)
		}
	}

	mf, err := readManifest(kitFS)
	if err != nil {
		return Kit{}, fmt.Errorf("kit %q: %w", name, err)
	}

	if mf.Name == "" {
		mf.Name = name
	}
	if mf.Name != name {
		return Kit{}, fmt.Errorf("kit %q: manifest name %q does not match directory name", name, mf.Name)
	}
	if mf.Description == "" {
		return Kit{}, fmt.Errorf("kit %q: manifest is missing description", name)
	}

	// depends_on default: ["base"] except for "base" itself.
	if mf.DependsOn == nil && name != "base" {
		mf.DependsOn = []string{"base"}
	}
	if name == "base" && len(mf.DependsOn) > 0 {
		return Kit{}, errors.New(`kit "base": depends_on must be empty`)
	}

	return Kit{Manifest: mf, FS: kitFS, Source: source}, nil
}

func readManifest(kitFS fs.FS) (Manifest, error) {
	b, err := fs.ReadFile(kitFS, "manifest.toml")
	if err != nil {
		return Manifest{}, fmt.Errorf("read manifest: %w", err)
	}
	var mf Manifest
	if _, err := toml.Decode(string(b), &mf); err != nil {
		return Manifest{}, fmt.Errorf("parse manifest: %w", err)
	}
	return mf, nil
}
```

**Implementation notes**:
- `fs.Stat` (not `fs.ReadFile`) for required-file existence — cheaper and clearer error.
- `toml.Decode(string, &v)` is a stable BurntSushi/toml entrypoint. If the implementer prefers `toml.NewDecoder(bytes.NewReader(b)).Decode(&v)`, both work.
- The `manifest.toml`'s `name` field is optional in our reader — if absent we use the directory name, but if present it must agree. This catches "kit dir was renamed but manifest wasn't updated" early.

**Acceptance criteria**:
- [ ] `Load("base", validBaseFS, "builtin")` returns a Kit with `DependsOn` empty and no error.
- [ ] `Load("foo", fsWithoutInstall, "...")` errors with "missing required file install.sh".
- [ ] `Load("foo", fsWithoutDescription, "...")` errors mentioning description.
- [ ] `Load("FOO", validFS, "...")` errors on the name regex.
- [ ] `Load("polyglot", validFSWithNilDependsOn, "...")` returns DependsOn = `["base"]`.
- [ ] `Load("base", baseFSWithDependsOnBase, "...")` errors (base may not depend on anything).

---

### Unit 3: `internal/kits/registry.go`

**File**: `internal/kits/registry.go`

```go
package kits

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
)

// Registry resolves kit names to Kits, sourcing from a built-in fs.FS first
// and a user kit directory second. User kits with the same name as a
// built-in shadow the built-in.
type Registry struct {
	builtin fs.FS  // rooted at the dir containing kit subdirs (e.g., the embed FS subbed at "kits")
	userDir string // absolute path to ~/.config/agentbox/kits, or "" if missing
}

// NewRegistry constructs a Registry. `userDir` may be "" — the registry
// gracefully omits it. If userDir is set but doesn't exist on disk, that's
// not an error (the user just doesn't have any user-authored kits yet).
func NewRegistry(builtin fs.FS, userDir string) *Registry {
	return &Registry{builtin: builtin, userDir: userDir}
}

// List returns every kit name known to the registry, alphabetically sorted.
// User-shadowed names appear once.
func (r *Registry) List() ([]string, error) {
	seen := make(map[string]struct{})

	if r.builtin != nil {
		entries, err := fs.ReadDir(r.builtin, ".")
		if err != nil {
			return nil, fmt.Errorf("list builtin kits: %w", err)
		}
		for _, e := range entries {
			if e.IsDir() {
				seen[e.Name()] = struct{}{}
			}
		}
	}

	if r.userDir != "" {
		entries, err := os.ReadDir(r.userDir)
		if err == nil {
			for _, e := range entries {
				if e.IsDir() {
					seen[e.Name()] = struct{}{}
				}
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("list user kits: %w", err)
		}
	}

	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out, nil
}

// Get loads the named kit. User kits shadow built-ins. Returns an error
// (wrapping fs.ErrNotExist) if the kit is unknown.
func (r *Registry) Get(name string) (Kit, error) {
	if r.userDir != "" {
		dir := r.userDir + string(os.PathSeparator) + name
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return Load(name, os.DirFS(dir), "user:"+dir)
		}
	}
	if r.builtin != nil {
		sub, err := fs.Sub(r.builtin, name)
		if err == nil {
			if _, statErr := fs.Stat(sub, "manifest.toml"); statErr == nil {
				return Load(name, sub, "builtin")
			}
		}
	}
	return Kit{}, fmt.Errorf("kit %q: %w", name, fs.ErrNotExist)
}

// Describe returns name + description + source for every known kit, in
// alphabetical order. Used by `agentbox build --list`.
func (r *Registry) Describe() ([]KitInfo, error) {
	names, err := r.List()
	if err != nil {
		return nil, err
	}
	out := make([]KitInfo, 0, len(names))
	for _, n := range names {
		k, err := r.Get(n)
		if err != nil {
			// Skip kits that fail to load — tolerate broken user kits without
			// failing the whole list. Print to stderr (caller decides; the
			// CLI will surface this).
			out = append(out, KitInfo{Name: n, Description: "(load error: " + err.Error() + ")", Source: "?"})
			continue
		}
		src := "builtin"
		if k.Source != "builtin" {
			src = "user"
		}
		out = append(out, KitInfo{Name: n, Description: k.Manifest.Description, Source: src})
	}
	return out, nil
}
```

**Implementation notes**:
- Source label discrimination: the canonical strings are `"builtin"` and `"user:<abspath>"`. `Describe` simplifies to `"builtin"` / `"user"` for display.
- Alphabetical sort makes `--list` output stable and grep-able.

**Acceptance criteria**:
- [ ] `List` returns names from both sources, deduped, sorted.
- [ ] User kit named `base` shadows built-in `base` (verified via `Get("base").Source` starting with `"user:"`).
- [ ] `Get("nonexistent")` returns an error wrapping `fs.ErrNotExist`.
- [ ] When `userDir` doesn't exist on disk, `List` succeeds with built-ins only.

---

### Unit 4: `internal/kits/resolve.go`

**File**: `internal/kits/resolve.go`

```go
package kits

import (
	"errors"
	"fmt"

	"github.com/nklisch/agentbox/internal/runspec"
)

// Resolve walks depends_on starting from `requested`, topo-sorts, dedupes,
// and conflict-checks. Returns the resolved kit list and image tag.
func Resolve(reg *Registry, requested []string) (Resolved, error) {
	if len(requested) == 0 {
		return Resolved{}, errors.New("at least one kit must be requested")
	}

	loaded := make(map[string]Kit) // by name
	visiting := make(map[string]bool)
	order := make([]string, 0, 8)

	var visit func(string, []string) error
	visit = func(name string, path []string) error {
		if _, ok := loaded[name]; ok {
			return nil // already resolved
		}
		if visiting[name] {
			cycle := append(append([]string{}, path...), name)
			return fmt.Errorf("dependency cycle: %v", cycle)
		}
		visiting[name] = true

		k, err := reg.Get(name)
		if err != nil {
			return err
		}
		// "base" never has dependencies; everything else has at least ["base"]
		// (Load applied that default).
		for _, dep := range k.Manifest.DependsOn {
			if err := visit(dep, append(path, name)); err != nil {
				return err
			}
		}

		visiting[name] = false
		loaded[name] = k
		order = append(order, name)
		return nil
	}

	for _, name := range requested {
		if err := visit(name, nil); err != nil {
			return Resolved{}, err
		}
	}

	// Conflict check: for each kit, verify nothing in conflicts_with appears
	// in the resolved set.
	for _, n := range order {
		k := loaded[n]
		for _, c := range k.Manifest.ConflictsWith {
			if _, present := loaded[c]; present {
				return Resolved{}, fmt.Errorf("kits %q and %q conflict (per %q.conflicts_with)", n, c, n)
			}
		}
	}

	kitsList := make([]Kit, len(order))
	for i, n := range order {
		kitsList[i] = loaded[n]
	}
	return Resolved{
		Kits: kitsList,
		Tag:  runspec.KitImageTag(order),
	}, nil
}
```

**Implementation notes**:
- DFS post-order produces a topological ordering with `base` naturally first (because it has no deps; visit returns it before its dependents).
- The order respects `runspec.KitImageTag`'s requirement that the input list be deterministic — we pass post-order names, but `KitImageTag` itself sorts internally so the output is stable regardless. This is correct: tag identity is defined by *which* kits, not their order; Dockerfile order is defined by topological sort.
- Cycle path reporting helps users debug their custom kits.

**Acceptance criteria**:
- [ ] `Resolve(reg, ["base"])` → 1 kit, tag = `runspec.KitImageTag(["base"])`.
- [ ] `Resolve(reg, ["claude"])` (where claude depends on base+node) → 3 kits in order [base, node, claude].
- [ ] `Resolve(reg, ["a", "b"])` where both depend on base → [base, a, b] OR [base, b, a] (both valid topological orders) but no duplicate base.
- [ ] Cycle (a→b, b→a) returns error containing both names.
- [ ] Missing dep (a→missing) returns an error wrapping `fs.ErrNotExist`.
- [ ] Conflicts: kit `a` has `conflicts_with = ["b"]`; resolving `[a, b]` errors with both names mentioned.
- [ ] Empty input returns an error.

---

### Unit 5: `internal/kits/hash.go`

**File**: `internal/kits/hash.go`

```go
package kits

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"sort"
)

// HashKit computes a stable content hash for a kit's directory. The hash
// covers every regular file in the kit FS: relative path + a separator +
// file bytes + a separator. Files are processed in sorted-path order so
// the result is deterministic.
//
// Used for cache invalidation: change any file in a kit, the hash changes,
// the cache misses, the image rebuilds.
func HashKit(kit Kit) (string, error) {
	h := sha1.New()
	var paths []string
	err := fs.WalkDir(kit.FS, ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("walk kit %q: %w", kit.Manifest.Name, err)
	}
	sort.Strings(paths)
	for _, p := range paths {
		f, err := kit.FS.Open(p)
		if err != nil {
			return "", fmt.Errorf("open %s: %w", p, err)
		}
		fmt.Fprintf(h, "%s\x00", p)
		if _, err := io.Copy(h, f); err != nil {
			_ = f.Close()
			return "", fmt.Errorf("read %s: %w", p, err)
		}
		_ = f.Close()
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
```

**Implementation notes**:
- NUL-separators between path and content (and after content) prevent path/content boundary ambiguity.
- Symlinks should not appear in built-in (embed) FS. For user kits, `fs.WalkDir` follows directory entries but `fs.DirEntry.IsDir()` checks the entry, not the target — symlinks to files appear as regular files. This is acceptable for v0.1.

**Acceptance criteria**:
- [ ] Same kit twice → same hash.
- [ ] Modifying any byte of any file → different hash (verified by changing one char in install.sh).
- [ ] Renaming a file → different hash.
- [ ] Output is 40 hex chars (full SHA-1).

---

### Unit 6: `internal/kits/dockerfile.go`

**File**: `internal/kits/dockerfile.go`

```go
package kits

import (
	"fmt"
	"strings"
)

// GenerateDockerfile renders the Dockerfile for a resolved kit list. The
// shape matches docs/KITS.md "Generated Dockerfile" verbatim — start at
// debian:bookworm-slim, install ca-certificates/curl/gnupg, run each kit's
// packages.txt + install.sh + env.sh in topological order, then wire env.d
// into shell rc and clean apt caches.
func GenerateDockerfile(res Resolved, agentboxVersion string) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# Generated by agentbox %s. Do not edit; regenerate with `agentbox build --print`.\n", agentboxVersion)
	fmt.Fprintln(&b, "FROM debian:bookworm-slim")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "ENV DEBIAN_FRONTEND=noninteractive")
	fmt.Fprintln(&b, "RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl gnupg && rm -rf /var/lib/apt/lists/*")
	fmt.Fprintln(&b, "RUN mkdir -p /etc/agentbox/env.d")
	fmt.Fprintln(&b)

	for i, k := range res.Kits {
		name := k.Manifest.Name
		prefix := fmt.Sprintf("%02d", i*10) // 00, 10, 20, ...; matches KITS.md ordering hint
		fmt.Fprintf(&b, "# === kit: %s ===\n", name)
		fmt.Fprintf(&b, "COPY kits/%s/ /tmp/kit-%s/\n", name, name)
		// Strip comments and blanks from packages.txt, then xargs -r so apt isn't
		// invoked when the file is effectively empty.
		fmt.Fprintf(&b,
			"RUN apt-get update && grep -v '^[[:space:]]*#' /tmp/kit-%s/packages.txt | grep -v '^[[:space:]]*$' | xargs -r apt-get install -y --no-install-recommends && rm -rf /var/lib/apt/lists/*\n",
			name)
		fmt.Fprintf(&b,
			"RUN chmod +x /tmp/kit-%s/install.sh && KIT_NAME=%s KIT_DIR=/tmp/kit-%s AGENTBOX_VERSION=%s bash -e /tmp/kit-%s/install.sh\n",
			name, name, name, agentboxVersion, name)
		fmt.Fprintf(&b, "RUN cp /tmp/kit-%s/env.sh /etc/agentbox/env.d/%s-%s.sh\n", name, prefix, name)
		fmt.Fprintln(&b)
	}

	fmt.Fprintln(&b, "# Final wireup: source all env.d/*.sh from shell rc")
	fmt.Fprintln(&b, "RUN echo 'for f in /etc/agentbox/env.d/*.sh; do . \"$f\"; done' > /etc/profile.d/agentbox.sh")
	fmt.Fprintln(&b, "RUN apt-get clean && rm -rf /var/lib/apt/lists/* /tmp/kit-*")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "# Container entrypoint is managed by agentbox at create time (sleep infinity).")

	return b.String()
}
```

**Implementation notes**:
- `apt-get update` is repeated per kit because we run `rm -rf /var/lib/apt/lists/*` after each install to keep layers small. The trade-off: more network traffic during build, smaller image. Acceptable for v0.1.
- `chmod +x install.sh` is defensive; embed.FS may strip the executable bit depending on how the file was added to git, and we always `bash -e` the file explicitly anyway.
- Single `RUN` per kit keeps Dockerfile readable; podman's layer cache still re-uses earlier layers when only a later kit changes.
- The `00`, `10`, `20` env.d prefix ordering matches KITS.md's hint that kits are sourced in resolution order.

**Acceptance criteria**:
- [ ] Output begins with `# Generated by agentbox`.
- [ ] Output contains `FROM debian:bookworm-slim` exactly once.
- [ ] For a `[base]` resolution, output contains `COPY kits/base/ /tmp/kit-base/` and `cp /tmp/kit-base/env.sh /etc/agentbox/env.d/00-base.sh`.
- [ ] For `[base, polyglot]`, polyglot's env file is `10-polyglot.sh`.
- [ ] Output contains the final `for f in /etc/agentbox/env.d/*.sh; do . "$f"; done` line.
- [ ] Output contains the apt-clean / tmp cleanup at the end.
- [ ] Calling twice with the same input produces byte-identical output.

---

### Unit 7: `internal/kits/cache.go`

**File**: `internal/kits/cache.go`

```go
package kits

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nklisch/agentbox/internal/state"
)

// CacheEntry is the JSON written to ~/.local/share/agentbox/cache/kits/<tag>.json.
type CacheEntry struct {
	Tag             string            `json:"tag"`
	Kits            []string          `json:"kits"`
	KitHashes       map[string]string `json:"kit_hashes"`
	BuiltAt         time.Time         `json:"built_at"`
	AgentboxVersion string            `json:"agentbox_version"`
}

// Cache is the kit-image cache.
type Cache struct {
	Dir string // ~/.local/share/agentbox/cache/kits
}

// NewCache returns a Cache rooted at <state-dir>/cache/kits and ensures
// the directory exists.
func NewCache() (*Cache, error) {
	base, err := state.Dir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(base, "cache", "kits")
	if err := state.EnsureDir(dir); err != nil {
		return nil, fmt.Errorf("ensure cache dir %s: %w", dir, err)
	}
	return &Cache{Dir: dir}, nil
}

// HashesFromResolved computes per-kit content hashes for a resolved kit list.
func HashesFromResolved(res Resolved) (map[string]string, error) {
	out := make(map[string]string, len(res.Kits))
	for _, k := range res.Kits {
		h, err := HashKit(k)
		if err != nil {
			return nil, err
		}
		out[k.Manifest.Name] = h
	}
	return out, nil
}

// Lookup reads <tag>.json. Returns (CacheEntry{}, fs.ErrNotExist) if absent.
func (c *Cache) Lookup(tag string) (CacheEntry, error) {
	path := c.path(tag, ".json")
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return CacheEntry{}, fs.ErrNotExist
	}
	if err != nil {
		return CacheEntry{}, fmt.Errorf("read cache %s: %w", path, err)
	}
	var entry CacheEntry
	if err := json.Unmarshal(b, &entry); err != nil {
		return CacheEntry{}, fmt.Errorf("parse cache %s: %w", path, err)
	}
	return entry, nil
}

// Save writes both <tag>.json and <tag>.Dockerfile atomically.
func (c *Cache) Save(entry CacheEntry, dockerfile string) error {
	if err := state.EnsureDir(c.Dir); err != nil {
		return err
	}
	jsonBytes, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return err
	}
	if err := writeAtomic(c.path(entry.Tag, ".json"), jsonBytes, 0o644); err != nil {
		return err
	}
	return writeAtomic(c.path(entry.Tag, ".Dockerfile"), []byte(dockerfile), 0o644)
}

// HasMatch returns true if a cache entry exists for res.Tag and every
// kit's content hash matches the on-disk record.
func (c *Cache) HasMatch(res Resolved) (bool, error) {
	entry, err := c.Lookup(res.Tag)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	current, err := HashesFromResolved(res)
	if err != nil {
		return false, err
	}
	if len(current) != len(entry.KitHashes) {
		return false, nil
	}
	for k, h := range current {
		if entry.KitHashes[k] != h {
			return false, nil
		}
	}
	return true, nil
}

// ListEntries returns every <tag>.json entry the cache knows about.
func (c *Cache) ListEntries() ([]CacheEntry, error) {
	entries, err := os.ReadDir(c.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []CacheEntry
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		tag := strings.TrimSuffix(e.Name(), ".json")
		entry, err := c.Lookup(tag)
		if err != nil {
			continue // skip unreadable; don't fail the whole list
		}
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Tag < out[j].Tag })
	return out, nil
}

// Remove deletes <tag>.json and <tag>.Dockerfile from the cache. The
// associated podman image is not touched (Builder.Prune handles that).
func (c *Cache) Remove(tag string) error {
	for _, ext := range []string{".json", ".Dockerfile"} {
		path := c.path(tag, ext)
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// path returns the cache path for (tag, ext). The tag may contain "/"
// (e.g., "agentbox/abc123") so we replace it for filesystem safety.
func (c *Cache) path(tag, ext string) string {
	safe := strings.ReplaceAll(tag, "/", "_")
	return filepath.Join(c.Dir, safe+ext)
}

// writeAtomic writes data to a temp file and renames it onto path. mode
// applies to the final file.
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	dir, base := filepath.Split(path)
	tmp, err := os.CreateTemp(dir, base+".tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
```

**Implementation notes**:
- Tag → filename mapping: `agentbox/abc123` becomes `agentbox_abc123.json`. Unambiguous since `_` isn't in our hash chars.
- `writeAtomic` survives crash-mid-write — readers always see either the old file or a complete new one.

**Acceptance criteria**:
- [ ] `Save` then `Lookup` round-trips an entry losslessly.
- [ ] `HasMatch` returns false when any kit hash differs.
- [ ] `HasMatch` returns true when all hashes match exactly.
- [ ] `ListEntries` skips non-JSON files in the dir.
- [ ] Test isolates filesystem via `t.TempDir()` and `t.Setenv("XDG_DATA_HOME", ...)`.

---

### Unit 8: `internal/kits/builder.go`

**File**: `internal/kits/builder.go`

```go
package kits

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Runner abstracts the container runtime (podman/docker). The kits package
// uses it through this interface so the domain stays adapter-free.
type Runner interface {
	Build(ctx BuildContext) error
	HasImage(tag string) (bool, error)
	LiveImageRefs() ([]string, error)
	RemoveImage(tag string) error
}

// BuildContext is the input to Runner.Build.
type BuildContext struct {
	Tag        string // the image tag to apply
	Dockerfile string // absolute path to the Dockerfile
	ContextDir string // absolute path to the build context dir (parent of kits/)
	NoCache    bool   // pass through `--no-cache` to the underlying runtime
	Stdout     io.Writer
	Stderr     io.Writer
}

// BuildOpts controls Builder.Build behavior.
type BuildOpts struct {
	NoCache bool
	Stdout  io.Writer // for podman build progress
	Stderr  io.Writer
}

// BuildResult describes the outcome of Builder.Build.
type BuildResult struct {
	Tag        string
	CacheHit   bool
	Dockerfile string
	Kits       []string
}

// PruneResult describes the outcome of Builder.Prune.
type PruneResult struct {
	Removed []string
	Kept    []string
}

// Builder ties registry + resolver + dockerfile + cache + runner together.
type Builder struct {
	Registry *Registry
	Cache    *Cache
	Runner   Runner
	Version  string // agentbox version (used in Dockerfile header + cache entry)
}

// PrintDockerfile resolves the kit list and returns the Dockerfile string
// without staging or building.
func (b *Builder) PrintDockerfile(requested []string) (string, error) {
	res, err := Resolve(b.Registry, requested)
	if err != nil {
		return "", err
	}
	return GenerateDockerfile(res, b.Version), nil
}

// Build resolves the requested kit list, checks the cache, generates a
// Dockerfile, stages a build context, and invokes the Runner. Returns a
// BuildResult describing what happened.
func (b *Builder) Build(requested []string, opts BuildOpts) (BuildResult, error) {
	if opts.Stdout == nil {
		opts.Stdout = io.Discard
	}
	if opts.Stderr == nil {
		opts.Stderr = io.Discard
	}

	res, err := Resolve(b.Registry, requested)
	if err != nil {
		return BuildResult{}, err
	}
	dockerfile := GenerateDockerfile(res, b.Version)
	result := BuildResult{Tag: res.Tag, Dockerfile: dockerfile, Kits: res.Names()}

	// Cache check (unless NoCache).
	if !opts.NoCache {
		match, err := b.Cache.HasMatch(res)
		if err != nil {
			return result, fmt.Errorf("check cache: %w", err)
		}
		if match {
			// Cache says we built this exact thing. But the image might have
			// been pruned externally — verify it's still in the runtime.
			has, err := b.Runner.HasImage(res.Tag)
			if err != nil {
				return result, fmt.Errorf("check image: %w", err)
			}
			if has {
				result.CacheHit = true
				return result, nil
			}
			// Cache stale; fall through to rebuild.
		}
	}

	ctxDir, cleanup, err := stageContext(res)
	if err != nil {
		return result, fmt.Errorf("stage build context: %w", err)
	}
	defer cleanup()

	dfPath := filepath.Join(ctxDir, "Dockerfile")
	if err := os.WriteFile(dfPath, []byte(dockerfile), 0o644); err != nil {
		return result, err
	}

	if err := b.Runner.Build(BuildContext{
		Tag:        res.Tag,
		Dockerfile: dfPath,
		ContextDir: ctxDir,
		NoCache:    opts.NoCache,
		Stdout:     opts.Stdout,
		Stderr:     opts.Stderr,
	}); err != nil {
		return result, fmt.Errorf("podman build: %w", err)
	}

	// Save cache entry.
	hashes, err := HashesFromResolved(res)
	if err != nil {
		return result, fmt.Errorf("hash kits: %w", err)
	}
	entry := CacheEntry{
		Tag:             res.Tag,
		Kits:            res.Names(),
		KitHashes:       hashes,
		BuiltAt:         time.Now().UTC(),
		AgentboxVersion: b.Version,
	}
	if err := b.Cache.Save(entry, dockerfile); err != nil {
		return result, fmt.Errorf("save cache: %w", err)
	}
	return result, nil
}

// Prune removes any agentbox/<tag> image not currently referenced by an
// agentbox-labeled container. Cache entries for removed images are also
// dropped.
func (b *Builder) Prune() (PruneResult, error) {
	live, err := b.Runner.LiveImageRefs()
	if err != nil {
		return PruneResult{}, err
	}
	liveSet := make(map[string]struct{}, len(live))
	for _, l := range live {
		liveSet[l] = struct{}{}
	}

	entries, err := b.Cache.ListEntries()
	if err != nil {
		return PruneResult{}, err
	}

	var removed, kept []string
	for _, e := range entries {
		if _, ref := liveSet[e.Tag]; ref {
			kept = append(kept, e.Tag)
			continue
		}
		if err := b.Runner.RemoveImage(e.Tag); err != nil {
			// Image may already be absent; continue but record as removed
			// since cache should not point at non-existent images.
		}
		_ = b.Cache.Remove(e.Tag)
		removed = append(removed, e.Tag)
	}
	sort.Strings(removed)
	sort.Strings(kept)
	return PruneResult{Removed: removed, Kept: kept}, nil
}

// stageContext writes res's kits to a fresh temp dir as kits/<name>/<files>.
// Returns the dir path and a cleanup function (which removes the dir).
func stageContext(res Resolved) (string, func(), error) {
	dir, err := os.MkdirTemp("", "agentbox-build-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	for _, k := range res.Kits {
		dst := filepath.Join(dir, "kits", k.Manifest.Name)
		if err := copyFS(k.FS, dst); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("stage kit %q: %w", k.Manifest.Name, err)
		}
	}
	return dir, cleanup, nil
}

// copyFS recursively copies srcFS rooted at "." into dst on disk. Preserves
// regular files only; sets executable bit on shell scripts (.sh) and on
// any file with no extension that lives in the kit's top-level dir (the
// `box` dispatcher and box-* helpers).
func copyFS(srcFS fs.FS, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return fs.WalkDir(srcFS, ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == "." {
			return nil
		}
		target := filepath.Join(dst, path)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		src, err := srcFS.Open(path)
		if err != nil {
			return err
		}
		defer src.Close()
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, fileMode(path))
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, src)
		return err
	})
}

// fileMode returns 0755 for shell scripts and box helpers, 0644 otherwise.
func fileMode(path string) os.FileMode {
	base := filepath.Base(path)
	if filepath.Ext(base) == ".sh" {
		return 0o755
	}
	if base == "box" || (len(base) > 4 && base[:4] == "box-") {
		return 0o755
	}
	return 0o644
}
```

**Implementation notes**:
- The `HasImage` cache-stale-but-image-gone check is important: a user might `podman rmi` an image but the cache JSON would still claim it exists. We re-check before claiming a hit.
- `copyFS` sets the executable bit because `embed.FS` doesn't preserve it — `fs.FS` doesn't carry mode information. We set executability based on file naming convention: `*.sh`, `box`, `box-*`. The Dockerfile also runs `chmod +x` on `install.sh` defensively.
- `stageContext`'s temp dir is removed on success; `Build` callers can preserve it on failure by setting a debug flag in a future phase. For v0.1 we just clean up always to keep the user's `/tmp` tidy.

**Acceptance criteria**:
- [ ] With a fake Runner that records calls, `Build([base])` calls `Runner.Build` once with the right tag and a Dockerfile path under a temp dir.
- [ ] Second `Build([base])` with no kit changes: `Runner.Build` is NOT called; result has `CacheHit: true`.
- [ ] Cache hit but `Runner.HasImage` returns false → `Runner.Build` IS called (cache stale).
- [ ] `Build` with `NoCache: true` always invokes `Runner.Build`.
- [ ] `PrintDockerfile` doesn't stage a context or call the runner.
- [ ] `Prune` calls `Runner.RemoveImage` for tags absent from `LiveImageRefs`, keeps live ones.
- [ ] Staged context contains files at `kits/<name>/manifest.toml` etc., readable from disk.

---

### Unit 9: tests for units 1–8

**File**: `internal/kits/kits_test.go` (one file is fine; can be split if it grows past ~600 LOC)

Key test setups:

```go
// fakeBaseFS returns a testing/fstest.MapFS rooted such that "manifest.toml"
// reads the base manifest. Use for parser/registry/resolver/hash tests.
func fakeBaseFS() fs.FS { ... }

// fakeKitFS(name, manifestTOML, packages, install, env) builds an in-memory FS for testing.
func fakeKitFS(name, manifestTOML, packages, install, env string) fs.FS { ... }

// fakeRunner records calls and returns canned results.
type fakeRunner struct {
    builds       []BuildContext
    hasImage     map[string]bool
    liveRefs     []string
    removed      []string
    failBuild    bool
}
func (r *fakeRunner) Build(ctx BuildContext) error { ... }
// (etc.)
```

Cases to cover:
- `TestLoad_*` (per acceptance criteria above).
- `TestRegistry_BuiltinAndUser_Shadow`.
- `TestResolve_BaseOnly`, `TestResolve_DependencyChain`, `TestResolve_Cycle`, `TestResolve_MissingDep`, `TestResolve_Conflicts`.
- `TestHashKit_Deterministic`, `TestHashKit_DiffersOnContentChange`.
- `TestGenerateDockerfile_Stable`, `TestGenerateDockerfile_HasFromAndCopyAndEnvD` (`base` only).
- `TestCache_RoundTrip`, `TestCache_HasMatchTrueWhenAllHashesMatch`, `TestCache_HasMatchFalseWhenAnyHashChanges`, `TestCache_ListEntries_SkipsNonJSON`.
- `TestBuilder_FreshBuild`, `TestBuilder_CacheHitWhenImagePresent`, `TestBuilder_CacheStaleWhenImageMissing`, `TestBuilder_NoCache`, `TestBuilder_Prune`.

Tests must isolate filesystem state via `t.TempDir()` + `t.Setenv("XDG_DATA_HOME", ...)`.

**Acceptance criteria**:
- [ ] `go test ./internal/kits/...` passes.
- [ ] No test invokes `podman` or `docker` (use `fakeRunner` exclusively).

---

## Part B — Base kit content + adapter + CLI wireup (units 10–15)

### Unit 10: `internal/kits/podman.go` — Runner adapter

**File**: `internal/kits/podman.go`

```go
package kits

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// PodmanRunner shells out to `podman` (or `docker`). It implements Runner.
type PodmanRunner struct {
	Bin string // "podman" or "docker", from cfg.Runtime
}

// NewPodmanRunner returns a runner using the named binary.
func NewPodmanRunner(bin string) *PodmanRunner {
	return &PodmanRunner{Bin: bin}
}

// Build runs `<bin> build [--no-cache] -t <tag> -f <dockerfile> <ctxdir>`.
func (r *PodmanRunner) Build(ctx BuildContext) error {
	args := []string{"build", "-t", ctx.Tag, "-f", ctx.Dockerfile}
	if ctx.NoCache {
		args = append(args, "--no-cache")
	}
	args = append(args, ctx.ContextDir)
	cmd := exec.Command(r.Bin, args...)
	cmd.Stdout = ctx.Stdout
	cmd.Stderr = ctx.Stderr
	return cmd.Run()
}

// HasImage runs `<bin> image inspect <tag>`. Exit 0 → present.
func (r *PodmanRunner) HasImage(tag string) (bool, error) {
	cmd := exec.Command(r.Bin, "image", "inspect", tag)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return false, nil // exit non-zero = absent (or bin error; treat as absent)
		}
		return false, err // command not found, etc.
	}
	return true, nil
}

// LiveImageRefs returns the image refs of containers currently labeled
// agentbox=1 (running OR stopped). Used by Prune.
func (r *PodmanRunner) LiveImageRefs() ([]string, error) {
	cmd := exec.Command(r.Bin, "ps", "-a", "--filter", "label=agentbox=1", "--format", "{{.Image}}")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s ps: %w", r.Bin, err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var refs []string
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		refs = append(refs, l)
	}
	return refs, nil
}

// RemoveImage runs `<bin> rmi <tag>`. Returns nil if the image is already absent.
func (r *PodmanRunner) RemoveImage(tag string) error {
	cmd := exec.Command(r.Bin, "rmi", tag)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil // image already gone, etc. — non-fatal for prune
		}
		return err
	}
	return nil
}
```

**Acceptance criteria**:
- [ ] Compiles. (Behavior is verified end-to-end via the ROADMAP test checkpoint.)
- [ ] No fields beyond `Bin` — keeps the adapter trivial.

---

### Unit 11: `internal/builtinkits/builtinkits.go` — embed

**File**: `internal/builtinkits/builtinkits.go`

```go
package builtinkits

import (
	"embed"
	"io/fs"
)

//go:embed all:kits
var content embed.FS

// FS returns an fs.FS rooted at the embedded kits/ directory. Subdirectories
// of the returned FS are kit names (e.g., "base").
func FS() fs.FS {
	sub, err := fs.Sub(content, "kits")
	if err != nil {
		panic(err) // unreachable: build-time embed failure would have been caught earlier
	}
	return sub
}
```

**Implementation notes**:
- `all:kits` keeps files starting with `_` or `.` if any sneak in. Defense in depth.
- Kits live at `internal/builtinkits/kits/<name>/`. The directory must exist at build time or `//go:embed` errors at compile.

**Acceptance criteria**:
- [ ] `FS()` returns an `fs.FS` from which `fs.ReadFile(fs, "base/manifest.toml")` succeeds.

---

### Unit 12: `kits/base/manifest.toml` and friends — base kit content

**Directory**: `internal/builtinkits/kits/base/`

#### `manifest.toml`

```toml
name        = "base"
description = "Shell, modern CLI replacements, zellij, and the box helpers"
depends_on  = []
```

#### `packages.txt`

Apt-installable tools only. The rest go in install.sh because they're not in Debian repos or are out of date there.

```
zsh
git
git-delta
unzip
xz-utils
ca-certificates
gnupg
fzf
bat
fd-find
ripgrep
jq
```

(Note: `git-delta` is in Debian Bookworm; `bat` is `batcat`, `fd-find` is `fdfind` — install.sh creates the canonical aliases.)

#### `env.sh`

KITS.md is strict: env.sh is exports + PATH only — no commands. So this file stays minimal. The interactive shell setup (starship/zoxide/fzf init) goes into `/etc/zsh/zshrc` from install.sh.

```bash
# Agentbox base kit env. Sourced from /etc/profile.d/agentbox.sh at login.
# Per KITS.md, this file is exports/PATH only — interactive-shell init lives
# in /etc/zsh/zshrc (written by install.sh).

export PATH="/usr/local/bin:${PATH}"
export EDITOR="${EDITOR:-vi}"
export PAGER="${PAGER:-less}"
export LESS="-R --mouse"
export ZELLIJ_DEFAULT_LAYOUT="agentbox"
```

#### `install.sh`

The heavy file. Structure:

```bash
#!/usr/bin/env bash
# Base kit installer: shell + modern CLI + zellij + box helpers.
# All tool versions are pinned via env vars at the top so reproducible
# rebuilds are possible. Override at build time via `--build-arg` (future)
# or by editing here.

set -euo pipefail

# --- Pinned versions (verify each at write time; do not guess from memory) ---
# Each tool is pinned to a specific recent release. The implementer should
# verify each version exists at the noted source before committing — these
# may have moved on by the time anyone is reading this.
ZELLIJ_VERSION="${ZELLIJ_VERSION:-0.40.1}"        # github.com/zellij-org/zellij/releases
STARSHIP_VERSION="${STARSHIP_VERSION:-1.20.0}"    # github.com/starship/starship/releases
EZA_VERSION="${EZA_VERSION:-0.18.0}"              # github.com/eza-community/eza/releases
DUST_VERSION="${DUST_VERSION:-1.0.0}"             # github.com/bootandy/dust/releases
DUF_VERSION="${DUF_VERSION:-0.8.1}"               # github.com/muesli/duf/releases
BTM_VERSION="${BTM_VERSION:-0.9.6}"               # github.com/ClementTsang/bottom/releases
PROCS_VERSION="${PROCS_VERSION:-0.14.5}"          # github.com/dalance/procs/releases
HYPERFINE_VERSION="${HYPERFINE_VERSION:-1.18.0}"  # github.com/sharkdp/hyperfine/releases
WATCHEXEC_VERSION="${WATCHEXEC_VERSION:-2.1.2}"   # github.com/watchexec/watchexec/releases
YQ_VERSION="${YQ_VERSION:-4.42.0}"                # github.com/mikefarah/yq/releases
ZOXIDE_VERSION="${ZOXIDE_VERSION:-0.9.4}"         # github.com/ajeetdsouza/zoxide/releases

ARCH="$(uname -m)"
case "$ARCH" in
  x86_64)  RUST_TRIPLE="x86_64-unknown-linux-gnu";  GO_ARCH="amd64" ;;
  aarch64) RUST_TRIPLE="aarch64-unknown-linux-gnu"; GO_ARCH="arm64" ;;
  *) echo "unsupported arch: $ARCH" >&2; exit 1 ;;
esac

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# --- Rename Debian-mangled tools to canonical names ---
# Debian ships `bat` as `batcat`, `fd-find` as `fdfind`. Make them
# available under the names the rest of the world uses.
[ -x /usr/bin/batcat ] && ln -sf /usr/bin/batcat /usr/local/bin/bat
[ -x /usr/bin/fdfind ] && ln -sf /usr/bin/fdfind /usr/local/bin/fd

# --- Per-tool sections ---
# zellij — multiplexer
curl -fsSL "https://github.com/zellij-org/zellij/releases/download/v${ZELLIJ_VERSION}/zellij-${RUST_TRIPLE}.tar.gz" \
  | tar -xz -C /usr/local/bin

# starship — prompt
curl -fsSL "https://github.com/starship/starship/releases/download/v${STARSHIP_VERSION}/starship-${RUST_TRIPLE}.tar.gz" \
  | tar -xz -C /usr/local/bin

# eza — ls replacement
curl -fsSL "https://github.com/eza-community/eza/releases/download/v${EZA_VERSION}/eza_${RUST_TRIPLE}.tar.gz" \
  -o "$TMP/eza.tar.gz"
tar -xzf "$TMP/eza.tar.gz" -C /usr/local/bin

# dust — du replacement
curl -fsSL "https://github.com/bootandy/dust/releases/download/v${DUST_VERSION}/dust-v${DUST_VERSION}-${RUST_TRIPLE}.tar.gz" \
  -o "$TMP/dust.tar.gz"
tar -xzf "$TMP/dust.tar.gz" -C "$TMP"
install -m 0755 "$TMP/dust-v${DUST_VERSION}-${RUST_TRIPLE}/dust" /usr/local/bin/dust

# duf — df replacement
curl -fsSL "https://github.com/muesli/duf/releases/download/v${DUF_VERSION}/duf_${DUF_VERSION}_linux_${GO_ARCH}.tar.gz" \
  -o "$TMP/duf.tar.gz"
tar -xzf "$TMP/duf.tar.gz" -C "$TMP"
install -m 0755 "$TMP/duf" /usr/local/bin/duf

# btm — bottom (top replacement)
curl -fsSL "https://github.com/ClementTsang/bottom/releases/download/${BTM_VERSION}/bottom_${RUST_TRIPLE}.tar.gz" \
  -o "$TMP/btm.tar.gz"
tar -xzf "$TMP/btm.tar.gz" -C "$TMP"
install -m 0755 "$TMP/btm" /usr/local/bin/btm

# procs — ps replacement
curl -fsSL "https://github.com/dalance/procs/releases/download/v${PROCS_VERSION}/procs-v${PROCS_VERSION}-${ARCH}-linux.zip" \
  -o "$TMP/procs.zip"
unzip -q "$TMP/procs.zip" -d "$TMP"
install -m 0755 "$TMP/procs" /usr/local/bin/procs

# hyperfine — benchmark tool
curl -fsSL "https://github.com/sharkdp/hyperfine/releases/download/v${HYPERFINE_VERSION}/hyperfine-v${HYPERFINE_VERSION}-${RUST_TRIPLE}.tar.gz" \
  -o "$TMP/hyperfine.tar.gz"
tar -xzf "$TMP/hyperfine.tar.gz" -C "$TMP"
install -m 0755 "$TMP/hyperfine-v${HYPERFINE_VERSION}-${RUST_TRIPLE}/hyperfine" /usr/local/bin/hyperfine

# watchexec — file-watch runner
curl -fsSL "https://github.com/watchexec/watchexec/releases/download/v${WATCHEXEC_VERSION}/watchexec-${WATCHEXEC_VERSION}-${RUST_TRIPLE}.tar.xz" \
  -o "$TMP/watchexec.tar.xz"
tar -xJf "$TMP/watchexec.tar.xz" -C "$TMP"
install -m 0755 "$TMP/watchexec-${WATCHEXEC_VERSION}-${RUST_TRIPLE}/watchexec" /usr/local/bin/watchexec

# yq — yaml query
curl -fsSL "https://github.com/mikefarah/yq/releases/download/v${YQ_VERSION}/yq_linux_${GO_ARCH}" \
  -o /usr/local/bin/yq
chmod +x /usr/local/bin/yq

# zoxide — frecency cd
curl -fsSL "https://github.com/ajeetdsouza/zoxide/releases/download/v${ZOXIDE_VERSION}/zoxide-${ZOXIDE_VERSION}-${RUST_TRIPLE}.tar.gz" \
  -o "$TMP/zoxide.tar.gz"
tar -xzf "$TMP/zoxide.tar.gz" -C "$TMP"
install -m 0755 "$TMP/zoxide" /usr/local/bin/zoxide

# httpie — pretty curl
# (uses pip; small enough that the dep on python3 is acceptable here)
apt-get update
apt-get install -y --no-install-recommends python3-pip
pip3 install --break-system-packages --no-cache-dir "httpie==3.2.2"
rm -rf /var/lib/apt/lists/*

# tldr — community man pages
pip3 install --break-system-packages --no-cache-dir "tldr"

# --- box helpers (copied from this kit's dir) ---
install -m 0755 "${KIT_DIR}/box"        /usr/local/bin/box
install -m 0755 "${KIT_DIR}/box-info"   /usr/local/bin/box-info
install -m 0755 "${KIT_DIR}/box-scratch" /usr/local/bin/box-scratch
install -m 0755 "${KIT_DIR}/box-save"   /usr/local/bin/box-save
install -m 0755 "${KIT_DIR}/box-help"   /usr/local/bin/box-help

# --- /etc/zsh/zshrc setup (interactive-shell init lives here, NOT in env.sh) ---
mkdir -p /etc/zsh
cat >> /etc/zsh/zshrc <<'ZSHRC'

# --- agentbox base kit additions ---
# fzf key bindings
[ -f /usr/share/doc/fzf/examples/key-bindings.zsh ] && . /usr/share/doc/fzf/examples/key-bindings.zsh
[ -f /usr/share/doc/fzf/examples/completion.zsh ]   && . /usr/share/doc/fzf/examples/completion.zsh

# zoxide
command -v zoxide >/dev/null && eval "$(zoxide init zsh)"

# starship prompt
command -v starship >/dev/null && eval "$(starship init zsh)"

# Aliases
alias ll='eza -la'
alias l='eza -l'
alias tree='eza --tree'
ZSHRC

# Default shell to zsh for root (the agentbox container runs as root).
chsh -s /usr/bin/zsh root || true

echo "base kit install complete"
```

#### `box` (dispatcher), `box-info`, `box-scratch`, `box-save`, `box-help`

All under `internal/builtinkits/kits/base/`. Each is a small bash script. They're copied to `/usr/local/bin/` by install.sh (chmod 0755 set there).

**`box`** — dispatcher:

```bash
#!/usr/bin/env bash
# box — dispatcher for the in-box helper commands.
# Subcommands live as /usr/local/bin/box-<name>.

set -u

case "${1:-help}" in
  info)    shift; exec box-info "$@" ;;
  scratch) shift; exec box-scratch "$@" ;;
  save)    shift; exec box-save "$@" ;;
  net)     echo "box net: not yet available (lands in agentbox Phase 6)" >&2; exit 1 ;;
  help|-h|--help) exec box-help ;;
  *)
    echo "box: unknown subcommand: $1" >&2
    box-help
    exit 1
    ;;
esac
```

**`box-info`** — print whatever the in-box environment knows:

```bash
#!/usr/bin/env bash
# box info — print this box's project, agent, kits, mounts, network, resources.
# Sources: AGENTBOX_* env vars (set by agentbox at create time, lands in Phase 3),
# /etc/agentbox/config.toml (mounted by agentbox at create time, lands in Phase 4).
# When run outside an agentbox container (e.g., `podman run --rm <tag> box info`),
# unset values print as "n/a" and the command still exits 0.

set -u

cfg=/etc/agentbox/config.toml

printf "project_id   %s\n" "${AGENTBOX_PROJECT_ID:-n/a}"
printf "project      %s\n" "${AGENTBOX_PROJECT:-n/a}"
printf "agent        %s\n" "${AGENTBOX_AGENT:-n/a}"
printf "kits         %s\n" "${AGENTBOX_KITS:-n/a}"
printf "kit_image    %s\n" "${AGENTBOX_KIT_IMAGE:-n/a}"
printf "network      %s\n" "${AGENTBOX_NETWORK:-n/a}"
printf "created      %s\n" "${AGENTBOX_CREATED:-n/a}"
printf "\n"

if [ -f "$cfg" ]; then
  printf "config       %s (mounted by agentbox)\n" "$cfg"
else
  printf "config       (not mounted; run via 'agentbox run' to mount)\n"
fi

# Resource limits — read from cgroups when available.
if [ -f /sys/fs/cgroup/memory.max ]; then
  printf "memory.max   %s\n" "$(cat /sys/fs/cgroup/memory.max)"
elif [ -f /sys/fs/cgroup/memory/memory.limit_in_bytes ]; then
  printf "memory.max   %s\n" "$(cat /sys/fs/cgroup/memory/memory.limit_in_bytes)"
fi
if [ -f /sys/fs/cgroup/cpu.max ]; then
  printf "cpu.max      %s\n" "$(cat /sys/fs/cgroup/cpu.max)"
fi
```

**`box-scratch`** — cd into a tmpfs scratch dir:

```bash
#!/usr/bin/env bash
# box scratch — cd into /tmp/box-scratch (tmpfs; ephemeral).
# Usage: source <(box scratch) — or use `cd "$(box scratch)"`.

set -u
mkdir -p /tmp/box-scratch
echo /tmp/box-scratch
```

(The script prints the path; in the zellij integration we'll alias `box-scratch` to also `cd` into it via a shell function, but for v0.1 the script is cd-friendly via subshell.)

**`box-save`** — copy a file to the host state dir:

```bash
#!/usr/bin/env bash
# box save <file> — copy <file> from the box to the host state dir.
# Host state is mounted at /root/.local/share/agentbox-history (Phase 1)
# and the saved/ dir is created here at first save.

set -eu

if [ $# -lt 1 ]; then
  echo "usage: box save <file>" >&2
  exit 2
fi

src="$1"
if [ ! -f "$src" ]; then
  echo "box save: $src: not a regular file" >&2
  exit 1
fi

# Host-side path (Phase 4 will mount sessions/<id>/saved/ explicitly; for now
# write into a sibling of the history mount that agentbox already mounts).
dst_root="${AGENTBOX_SAVED_DIR:-/root/.local/share/agentbox-saved}"
mkdir -p "$dst_root"
dst="$dst_root/$(basename "$src")"
cp -p "$src" "$dst"
echo "saved → $dst"
```

(Note: Phase 4 will mount the saved dir from the host's `~/.local/share/agentbox/sessions/<id>/saved/`. For Phase 2 the script writes to a path inside the container; the host won't see it without that mount. Document this.)

**`box-help`** — list subcommands:

```bash
#!/usr/bin/env bash
cat <<'EOF'
box — agentbox in-box helpers

  box info       Show project, agent, kits, mounts, network, resources
  box scratch    Print path to a tmpfs scratch dir (ephemeral)
  box save <f>   Copy a file to the host state dir so it survives `agentbox rm`
  box net        (Phase 6) Show network policy + recent DNS queries
  box help       This message
EOF
```

**Acceptance criteria** (collectively for unit 12):
- [ ] `internal/builtinkits/kits/base/manifest.toml` parses cleanly via `kits.Load("base", ...)`.
- [ ] `packages.txt` apt-installs cleanly during a build (verified by the integration checkpoint).
- [ ] `install.sh` exits 0 during a build (verified by the integration checkpoint).
- [ ] After build, the resulting image has `zsh`, `rg`, `eza`, `zellij`, `jq` on PATH (the ROADMAP test checkpoint runs these).
- [ ] `box info` exits 0 when run via plain `podman run --rm <tag> box info` (no AGENTBOX_* vars, no mounted config) — prints `n/a` for missing fields.
- [ ] `box help` exits 0.

---

### Unit 13: `internal/cli/build.go` — CLI command

**File**: `internal/cli/build.go`

```go
package cli

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nklisch/agentbox/internal/builtinkits"
	"github.com/nklisch/agentbox/internal/exitcode"
	"github.com/nklisch/agentbox/internal/kits"
	"github.com/nklisch/agentbox/internal/version"
)

func newBuildCmd() *cobra.Command {
	var (
		noCache   bool
		printOnly bool
		listOnly  bool
		prune     bool
	)
	cmd := &cobra.Command{
		Use:   "build [kit_list]",
		Short: "Build (or rebuild) a composed kit image",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := loadConfig()
			if err != nil {
				return err
			}

			b, err := newBuilder(res.Config.Runtime)
			if err != nil {
				return err
			}

			switch {
			case listOnly:
				return runBuildList(cmd, b)
			case prune:
				return runBuildPrune(cmd, b)
			case printOnly:
				return runBuildPrint(cmd, b, kitList(args, res.Config.DefaultKits))
			default:
				return runBuildBuild(cmd, b, kitList(args, res.Config.DefaultKits), noCache)
			}
		},
	}
	cmd.Flags().BoolVar(&noCache, "no-cache", false, "force a full rebuild (skips agentbox + podman caches)")
	cmd.Flags().BoolVar(&printOnly, "print", false, "print the generated Dockerfile to stdout; do not build")
	cmd.Flags().BoolVar(&listOnly, "list", false, "list all known kits and exit")
	cmd.Flags().BoolVar(&prune, "prune", false, "remove kit images not referenced by any current box")
	cmd.MarkFlagsMutuallyExclusive("print", "list", "prune")
	return cmd
}

func newBuilder(runtimeBin string) (*kits.Builder, error) {
	userKitsDir, _ := userKitsDirPath() // empty string if unresolvable; non-fatal
	reg := kits.NewRegistry(builtinkits.FS(), userKitsDir)
	cache, err := kits.NewCache()
	if err != nil {
		return nil, exitcode.Wrap(exitcode.Generic, err)
	}
	return &kits.Builder{
		Registry: reg,
		Cache:    cache,
		Runner:   kits.NewPodmanRunner(runtimeBin),
		Version:  version.Version,
	}, nil
}

// userKitsDirPath returns ~/.config/agentbox/kits (or "" if HOME unresolvable).
func userKitsDirPath() (string, error) {
	cfgDir, err := osUserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfgDir, "agentbox", "kits"), nil
}

// kitList returns the resolved list: arg if non-empty (comma-split), else default.
func kitList(args []string, defaultKits []string) []string {
	if len(args) == 1 {
		return strings.Split(args[0], ",")
	}
	return defaultKits
}

func runBuildList(cmd *cobra.Command, b *kits.Builder) error {
	infos, err := b.Registry.Describe()
	if err != nil {
		return exitcode.Wrap(exitcode.KitBuild, err)
	}
	for _, ki := range infos {
		fmt.Fprintf(cmd.OutOrStdout(), "%-12s %s  (%s)\n", ki.Name, ki.Description, ki.Source)
	}
	return nil
}

func runBuildPrint(cmd *cobra.Command, b *kits.Builder, requested []string) error {
	df, err := b.PrintDockerfile(requested)
	if err != nil {
		return exitcode.Wrap(exitcode.KitBuild, err)
	}
	fmt.Fprint(cmd.OutOrStdout(), df)
	return nil
}

func runBuildPrune(cmd *cobra.Command, b *kits.Builder) error {
	res, err := b.Prune()
	if err != nil {
		return exitcode.Wrap(exitcode.KitBuild, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "removed %d image(s); kept %d\n", len(res.Removed), len(res.Kept))
	for _, t := range res.Removed {
		fmt.Fprintf(cmd.OutOrStdout(), "  removed: %s\n", t)
	}
	return nil
}

func runBuildBuild(cmd *cobra.Command, b *kits.Builder, requested []string, noCache bool) error {
	if len(requested) == 0 {
		return exitcode.New(exitcode.InvalidArgs, "no kits requested and default_kits is empty")
	}
	res, err := b.Build(requested, kits.BuildOpts{
		NoCache: noCache,
		Stdout:  cmd.OutOrStdout(),
		Stderr:  cmd.ErrOrStderr(),
	})
	if err != nil {
		return exitcode.Wrap(exitcode.KitBuild, err)
	}
	if res.CacheHit {
		fmt.Fprintf(cmd.OutOrStdout(), "cache hit: %s (kits: %s)\n", res.Tag, strings.Join(res.Kits, ","))
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "built: %s (kits: %s)\n", res.Tag, strings.Join(res.Kits, ","))
	return nil
}

// osUserConfigDir is a tiny indirection so tests can override later if needed.
// In the implementation, just call os.UserConfigDir directly via this wrapper.
var osUserConfigDir = func() (string, error) {
	// Implementation: return os.UserConfigDir()
	panic("set in build.go: osUserConfigDir = os.UserConfigDir")
}
```

**Implementation notes**:
- The `osUserConfigDir` indirection is one line you can drop entirely if it feels like overkill — replace with a direct `os.UserConfigDir()` call. The wrapper-with-panic in the design is just to flag that the implementer needs to set it; clean it up however reads best.
- `--list`, `--print`, `--prune` are mutually exclusive (cobra handles that).
- Default-kits fallback: if no arg is given, use `default_kits` from config. If default_kits is also empty, error.

**Acceptance criteria**:
- [ ] `agentbox build --list` prints lines starting with `base` (and any user kits).
- [ ] `agentbox build --print base` prints a Dockerfile starting with `# Generated by agentbox`.
- [ ] `agentbox build --print base | grep -q FROM debian:bookworm-slim` succeeds.
- [ ] `agentbox build base` builds the image; second invocation prints `cache hit:`.
- [ ] `agentbox build --no-cache base` rebuilds (no cache hit message).
- [ ] `agentbox build --prune` exits 0; output starts with `removed N image(s);`.
- [ ] `agentbox build` (no args) uses `default_kits`; if those are unresolvable returns exit 5.

---

### Unit 14: edit `internal/cli/stub.go` — drop `newBuildCmd`

**File**: `internal/cli/stub.go`

Remove the `newBuildCmd` function. The new real one lives in `build.go` and `root.go`'s `cmd.AddCommand(newBuildCmd(), ...)` call is unchanged (Go resolves the name to whichever file defines it within the package).

**Acceptance criteria**:
- [ ] `grep -n 'newBuildCmd' internal/cli/stub.go` prints nothing.
- [ ] `grep -n 'newBuildCmd' internal/cli/build.go` prints exactly one definition.
- [ ] `agentbox build --help` shows the real flag set (`--no-cache`, `--print`, `--list`, `--prune`), not just the stub message.

---

### Unit 15: integration verification

No new file. The phase's acceptance is the ROADMAP test checkpoint, run manually after Part B lands. Recorded in PROGRESS.md.

```sh
# Build the binary.
cd /home/nathan/dev/agent-box
go vet ./...
go test ./...
make build

# 1. ROADMAP Phase 2 test checkpoint.
./agentbox build --list | grep -q '^base'

./agentbox build --print base > /tmp/Dockerfile
grep -q 'FROM debian:bookworm-slim' /tmp/Dockerfile

./agentbox build base                          # builds (slow first time)
TAG=$(./agentbox build --print base | grep -oP 'agentbox/\w+' | head -1)
podman run --rm "$TAG" zsh -lc 'box info; rg --version; eza --version; zellij --version; jq --version'

./agentbox build base                          # no-op, cache hit, exits fast
./agentbox build --no-cache base               # forces rebuild

# 2. Sanity beyond the checkpoint.
./agentbox build --prune                       # removes nothing if image is referenced
ls ~/.local/share/agentbox/cache/kits/         # contains <tag>.json + <tag>.Dockerfile
```

The image build is genuinely slow the first time (5–15 min on a typical laptop, dominated by curl downloads of pinned binaries). Cache hits are instant.

---

## Implementation Order

**Part A** (units 1–9) — build and test in isolation, no podman calls:

| Order | Unit | Depends on |
|------:|------|------------|
| 1 | Unit 1 — types.go | — |
| 2 | Unit 2 — parse.go | Unit 1 |
| 3 | Unit 3 — registry.go | Units 1, 2 |
| 4 | Unit 4 — resolve.go | Units 1–3, runspec.KitImageTag (Phase 1) |
| 5 | Unit 5 — hash.go | Unit 1 |
| 6 | Unit 6 — dockerfile.go | Unit 1 |
| 7 | Unit 7 — cache.go | Units 1, 5, internal/state (Phase 1) |
| 8 | Unit 8 — builder.go | Units 1, 4, 6, 7 |
| 9 | Unit 9 — tests | All of 1–8 |

After Part A: `go vet ./... && go test ./...` must be green.

**Part B** (units 10–15):

| Order | Unit | Depends on |
|------:|------|------------|
| 10 | Unit 10 — podman.go | Unit 8's Runner interface |
| 11 | Unit 11 — builtinkits/builtinkits.go | Unit 12 (the kits dir must exist before embed compiles) |
| 12 | Unit 12 — base kit content | — |
| 13 | Unit 13 — cli/build.go | Units 8, 10, 11 |
| 14 | Unit 14 — stub.go edit | Unit 13 |
| 15 | Unit 15 — manual verification | All of Part A + B |

Within Part B, units 11 and 12 must land together: `//go:embed all:kits` requires the directory to exist at compile time.

---

## Verification Checklist

```sh
# After Part A
cd /home/nathan/dev/agent-box
go vet ./...
go test ./...                                    # all kits tests pass; no podman invoked

# After Part B
go vet ./...
go test ./...
make build

./agentbox build --list | grep -q '^base'        # checkpoint 1
./agentbox build --print base > /tmp/Dockerfile  # checkpoint 2
grep -q 'FROM debian:bookworm-slim' /tmp/Dockerfile

./agentbox build base                            # checkpoint 3 — slow first time
TAG=$(./agentbox build --print base | grep -oP 'agentbox/\w+' | head -1)
podman run --rm "$TAG" zsh -lc 'box info; rg --version; eza --version; zellij --version; jq --version'   # checkpoint 4

./agentbox build base                            # checkpoint 5 — cache hit
./agentbox build --no-cache base                 # checkpoint 6 — forces rebuild

# Domain-layer purity preserved
grep -rn 'spf13/cobra' internal/version/ internal/exitcode/ internal/state/ internal/project/ internal/config/ internal/runspec/ internal/doctor/ internal/kits/   # prints nothing
```

Phase 2 is "done" for autopilot purposes when all of the above succeed.
