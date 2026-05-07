# Design: Kit-image distribution via GitHub Container Registry

## Overview

Implements the v0.3+ deferred item from `SPEC.md` ("Kit image registry distribution
(so first run is `pull` not `build`)"). Adds:

- **Producer side (CI):** a new `kit-images` GitHub Actions workflow that, on
  every `v*` tag, builds a fixed set of pre-published kit-list images for
  `linux/amd64` and `linux/arm64` and pushes them to GHCR as multi-arch
  manifests under `ghcr.io/nklisch/agentbox-kits`.
- **Consumer side (CLI):** `agentbox build` and `agentbox run` gain a
  pull-then-fall-back-to-build path. When the resolved kit list is composed
  entirely of built-in kits and `[registry] enabled = true`, the CLI tries
  `podman pull <remote>` against the version-pinned remote tag, retags the
  pulled image as the canonical local tag (`agentbox/<sha[:12]>`), and writes
  a cache entry so subsequent invocations short-circuit. Pull failures fall
  through to the existing local-build path.
- **Doctor check:** new `registry-reachable` check that does a HEAD against
  the registry's manifests endpoint for one published alias. Warn-only;
  doesn't block runs.
- **Plumbing primitive:** a new `agentbox build --emit-context <dir>` flag
  that writes the build context (Dockerfile + staged kit dirs) to disk so CI
  can hand it to `docker buildx build --push` without invoking the runner.

The local image tag (`agentbox/<sha[:12]>`) and every label (`agentbox.kit_image`)
are unchanged. The remote tag scheme (`<host>:<version>-<sha[:12]>`) is computed
in a new helper and used only by the registry path.

The four pre-published kit lists are:

| Nickname                         | Resolved kit list (after base auto-prepend) |
| -------------------------------- | ------------------------------------------- |
| `polyglot-containers-claude`     | `base, polyglot, containers, node, claude`  |
| `polyglot-containers-codex`      | `base, polyglot, containers, node, codex`   |
| `polyglot-claude`                | `base, polyglot, node, claude`              |
| `node-claude`                    | `base, node, claude`                        |

GHCR public packages are free (unlimited storage and bandwidth) for the public
agentbox repo, so this introduces no recurring cost.

## Decoupling

- **Group A: consumer side** (units 1–8). Adds the registry config, pull
  path, eligibility check, retag logic, error mapping, doctor check, CLI
  flags, and the cache-source marker. Ships standalone — without Group B
  the pull path simply 404s on every kit list and falls back to building.
- **Group B: producer side** (units 9–11). Adds `--emit-context`, the
  `kit-images.yml` workflow, and the published-kit-list manifest. Ships only
  after Group A is in users' hands; otherwise we'd publish images nobody
  pulls.

If Group B slips, Group A still passes acceptance because the fallback path
is the existing build behavior. Conversely, Group B can be exercised
manually (push images, verify with `podman pull` on a host) without Group A.

## Pre-design verification

Per CLAUDE.md's "fast-moving ecosystem" rule:

- **GHCR for OSS** (verified 2026-05-06): public packages free, unlimited
  storage and bandwidth. Authentication uses `GITHUB_TOKEN` with
  `packages: write` permission in workflows. `podman pull` against public
  GHCR images is unauthenticated.
- **`podman pull` exit codes** (re-verify at implementation start):
  - 0 — success
  - 125 — image not found (404), auth failure, network error, rate limit.
    Exit code alone is ambiguous; classify by parsing stderr.
- **`docker buildx build --platform linux/amd64,linux/arm64 --push`** is
  the canonical multi-arch flow. Requires `docker/setup-qemu-action` for
  arm64-on-amd64-host emulation. Build is slow under emulation (~30 min for
  the chunky polyglot+containers+claude list); cache via the `gha` cache
  backend. Re-verify the buildx and qemu action pin versions at
  implementation start.
- **GHCR API for HEAD checks**: `GET https://ghcr.io/v2/<owner>/<repo>/manifests/<tag>`
  with `Accept: application/vnd.oci.image.index.v1+json` returns 200 for
  multi-arch manifests, 404 for missing. No auth required for public images.
  Stable per OCI distribution spec; unlikely to drift.
- **`podman tag <src> <dst>`** is the canonical retag command; works on
  both podman and docker. Same syntax. Verified during cross-check.

If `podman pull`'s stderr classification turns out brittle at implementation
time, fall back to: parse exit codes only, surface the raw stderr to the
user, and always fall through to local build.

## Implementation Units

### Unit 1: `[registry]` config schema

**File:** `internal/config/config.go` (edit)

Add a `Registry` struct and a top-level field on `Config`. Place it after
`Zellij` for narrative flow.

```go
type Config struct {
    Runtime      string           `toml:"runtime" json:"runtime"`
    DefaultAgent string           `toml:"default_agent" json:"default_agent"`
    DefaultKits  []string         `toml:"default_kits" json:"default_kits"`
    Network      Network          `toml:"network" json:"network"`
    Mounts       Mounts           `toml:"mounts" json:"mounts"`
    Secrets      Secrets          `toml:"secrets" json:"secrets"`
    Resources    Resources        `toml:"resources" json:"resources"`
    Containers   Containers       `toml:"containers" json:"containers"`
    Shell        Shell            `toml:"shell" json:"shell"`
    Zellij       Zellij           `toml:"zellij" json:"zellij"`
    Registry     Registry         `toml:"registry" json:"registry"` // NEW
    Agents       map[string]Agent `toml:"agents" json:"agents"`
}

// Registry holds the kit-image registry configuration. When Enabled and the
// resolved kit list is composed entirely of built-in kits, agentbox tries to
// pull the pre-built image from Host before building locally.
type Registry struct {
    Enabled     bool   `toml:"enabled" json:"enabled"`
    Host        string `toml:"host" json:"host"`                 // e.g. "ghcr.io/nklisch/agentbox-kits"
    Verify      string `toml:"verify" json:"verify"`             // "none" (v0.3+) | "cosign" (deferred)
    PullTimeout string `toml:"pull_timeout" json:"pull_timeout"` // Go duration string, e.g. "5m"
}
```

In `DefaultConfig()` add to the returned struct:

```go
Registry: Registry{
    Enabled:     true,
    Host:        "ghcr.io/nklisch/agentbox-kits",
    Verify:      "none",
    PullTimeout: "5m",
},
```

In `Validate()` add:

```go
switch c.Registry.Verify {
case "", "none":
case "cosign":
    return fmt.Errorf("registry.verify: %q is reserved for a future release", c.Registry.Verify)
default:
    return fmt.Errorf("registry.verify: must be 'none', got %q", c.Registry.Verify)
}
if c.Registry.Enabled && c.Registry.Host == "" {
    return fmt.Errorf("registry.enabled=true requires registry.host to be set")
}
if c.Registry.PullTimeout != "" {
    if _, err := time.ParseDuration(c.Registry.PullTimeout); err != nil {
        return fmt.Errorf("registry.pull_timeout: %w", err)
    }
}
```

Add `"time"` to the imports.

**Implementation Notes:**

- `Verify == ""` is treated as `"none"` so the field is optional in old
  configs.
- `cosign` is reserved but rejected at parse time — a clear error if anyone
  tries to set it before the verification path is implemented.
- The existing `config-set` reflection-based machinery handles all four
  fields (string + bool scalars) without any hand-written wiring.

**Acceptance Criteria:**

- [ ] `agentbox config show --json` includes a `registry` object with the
      four fields.
- [ ] `agentbox config show` after `DefaultConfig()` reflects the defaults
      above.
- [ ] `agentbox config set registry.enabled false` toggles the field.
- [ ] `agentbox config set registry.verify cosign` rejects with exit code 2
      and a clear error.
- [ ] An `.agentbox.toml` with `[registry] pull_timeout = "junk"` fails
      validation with exit code 2.

---

### Unit 2: `RemoteImageRef` helper

**File:** `internal/runspec/runspec.go` (edit)

Add a function adjacent to `KitImageTag`:

```go
// RemoteImageRef returns the canonical GHCR reference for a resolved kit
// list at a given agentbox version. Format:
//
//   <host>:<version>-<sha1[:12]>
//
// where the version is normalized to drop a leading "v" so v0.3.0 and 0.3.0
// produce the same tag. Returns "" if host is empty.
func RemoteImageRef(host, version string, kits []string) string {
    if host == "" {
        return ""
    }
    v := strings.TrimPrefix(version, "v")
    sha := strings.TrimPrefix(KitImageTag(kits), "agentbox/")
    return fmt.Sprintf("%s:%s-%s", host, v, sha)
}

// RemoteAliasRef returns the human-readable alias variant. The nickname
// is the resolved kit list without "base", joined with "-". Returns "" if
// host or version is empty.
func RemoteAliasRef(host, version string, kits []string) string {
    if host == "" || version == "" {
        return ""
    }
    parts := make([]string, 0, len(kits))
    for _, k := range kits {
        if k == "base" {
            continue
        }
        parts = append(parts, k)
    }
    if len(parts) == 0 {
        parts = []string{"base"}
    }
    v := strings.TrimPrefix(version, "v")
    return fmt.Sprintf("%s:%s-%s", host, v, strings.Join(parts, "-"))
}
```

Add `"fmt"` to imports if not already present.

**Implementation Notes:**

- The CLI uses `RemoteImageRef` (sha-pinned) for pulls because it's
  deterministic and matches what CI publishes. The alias is a publishing
  convenience, not a CLI lookup target.
- Stripping `v` from version normalizes `Version = "v0.3.0"` from ldflags
  (when set with the leading v) and `"0.3.0"` to the same tag.
- Stripping `agentbox/` from the local tag is intentional — the local prefix
  is a vendor convention, not part of the content hash.
- `KitImageTag` already sorts kits before hashing, so `RemoteImageRef`
  inherits ordering invariance.

**Acceptance Criteria:**

- [ ] `RemoteImageRef("ghcr.io/n/agentbox-kits", "0.3.0", []string{"polyglot","claude","base"})`
      returns `"ghcr.io/n/agentbox-kits:0.3.0-<12-hex>"` and the hex matches
      the local tag's hex.
- [ ] `RemoteImageRef(..., "v0.3.0", ...)` and `RemoteImageRef(..., "0.3.0", ...)`
      return identical strings.
- [ ] `RemoteImageRef("", ...)` returns `""`.
- [ ] `RemoteAliasRef("ghcr.io/n/agentbox-kits", "0.3.0", []string{"base","polyglot","containers","claude"})`
      returns `"ghcr.io/n/agentbox-kits:0.3.0-polyglot-containers-claude"`.
- [ ] Reordering inputs does not change the result.

---

### Unit 3: Extend `Runner` with `Pull` and `Tag`

**File:** `internal/kits/builder.go` (edit interface), `internal/kits/podman.go`
(add methods)

Extend the `Runner` interface and implement on `PodmanRunner`:

```go
// Runner abstracts the container runtime (podman/docker).
type Runner interface {
    Build(ctx BuildContext) error
    HasImage(tag string) (bool, error)
    LiveImageRefs() ([]string, error)
    RemoveImage(tag string) error

    // Pull retrieves a remote image to the local store. Stdout/stderr are
    // streamed for live progress; the timeout (if non-zero) bounds the
    // operation. Returns a typed error on classifiable failure modes.
    Pull(ctx PullContext) error

    // Tag applies dst as an additional name for src. Used after a Pull to
    // give the pulled remote ref the canonical local tag.
    Tag(src, dst string) error
}

// PullContext is the input to Runner.Pull.
type PullContext struct {
    Ref     string         // the full remote reference, e.g. ghcr.io/n/agentbox-kits:0.3.0-abc123
    Timeout time.Duration  // 0 = no timeout
    Stdout  io.Writer
    Stderr  io.Writer
}

// PullErrorKind classifies pull failures so the consumer can decide whether
// to fall back silently (NotFound) or surface the failure (Auth, Network).
type PullErrorKind int

const (
    PullErrorUnknown  PullErrorKind = iota
    PullErrorNotFound               // 404 — image absent at that tag
    PullErrorAuth                   // 401/403 — registry auth failure
    PullErrorNetwork                // DNS, timeout, connection refused
    PullErrorRuntime                // podman binary missing, etc.
)

type PullError struct {
    Kind    PullErrorKind
    Ref     string
    Stderr  string
    Wrapped error
}

func (e *PullError) Error() string {
    return fmt.Sprintf("pull %s: %s", e.Ref, e.Wrapped.Error())
}
func (e *PullError) Unwrap() error { return e.Wrapped }
```

In `internal/kits/podman.go`:

```go
// Pull runs `<bin> pull <ref>`. Stderr is captured and parsed to classify
// the failure when the pull errors. Streams stdout/stderr for live progress.
func (r *PodmanRunner) Pull(ctx PullContext) error {
    if ctx.Ref == "" {
        return &PullError{Kind: PullErrorRuntime, Wrapped: errors.New("empty ref")}
    }

    cctx := context.Background()
    if ctx.Timeout > 0 {
        var cancel context.CancelFunc
        cctx, cancel = context.WithTimeout(cctx, ctx.Timeout)
        defer cancel()
    }

    cmd := exec.CommandContext(cctx, r.Bin, "pull", ctx.Ref)
    var stderr bytes.Buffer
    if ctx.Stdout != nil {
        cmd.Stdout = ctx.Stdout
    } else {
        cmd.Stdout = io.Discard
    }
    // Tee stderr both to the user (for visibility) and to a buffer (for
    // classification on error).
    if ctx.Stderr != nil {
        cmd.Stderr = io.MultiWriter(ctx.Stderr, &stderr)
    } else {
        cmd.Stderr = &stderr
    }

    err := cmd.Run()
    if err == nil {
        return nil
    }
    return &PullError{
        Kind:    classifyPullStderr(stderr.String(), cctx.Err()),
        Ref:     ctx.Ref,
        Stderr:  stderr.String(),
        Wrapped: err,
    }
}

// classifyPullStderr inspects podman/docker stderr to label the failure.
// Returns PullErrorRuntime when ctxErr is context.DeadlineExceeded so the
// timeout case is distinguishable from a generic network error.
func classifyPullStderr(stderr string, ctxErr error) PullErrorKind {
    if errors.Is(ctxErr, context.DeadlineExceeded) {
        return PullErrorNetwork
    }
    s := strings.ToLower(stderr)
    switch {
    case strings.Contains(s, "manifest unknown"),
         strings.Contains(s, "not found"),
         strings.Contains(s, "404"):
        return PullErrorNotFound
    case strings.Contains(s, "unauthorized"),
         strings.Contains(s, "authentication required"),
         strings.Contains(s, "denied"),
         strings.Contains(s, "401"),
         strings.Contains(s, "403"):
        return PullErrorAuth
    case strings.Contains(s, "no such host"),
         strings.Contains(s, "connection refused"),
         strings.Contains(s, "timeout"),
         strings.Contains(s, "i/o timeout"),
         strings.Contains(s, "dial tcp"):
        return PullErrorNetwork
    default:
        return PullErrorUnknown
    }
}

// Tag runs `<bin> tag <src> <dst>`. Used to apply the local agentbox/<sha>
// alias to a freshly pulled remote ref so the lifecycle layer can find it
// by the same tag it always uses.
func (r *PodmanRunner) Tag(src, dst string) error {
    cmd := exec.Command(r.Bin, "tag", src, dst)
    cmd.Stdout = io.Discard
    cmd.Stderr = io.Discard
    if err := cmd.Run(); err != nil {
        return fmt.Errorf("%s tag %s %s: %w", r.Bin, src, dst, err)
    }
    return nil
}
```

Add `"bytes"`, `"context"`, `"time"` to imports.

**Implementation Notes:**

- `Pull` streams progress to the user's TTY by default (stdout/stderr piped
  through). Mirrors the existing `Build` ergonomics — users see what's
  happening for a multi-GB pull.
- The classification only ever uses substring checks. Brittle, but
  recoverable: an unknown classification falls through to the
  "fall back to local build" path with stderr surfaced. Worst case is a
  needless rebuild, never a wrong answer.
- The `Tag` operation is idempotent on both podman and docker.

**Acceptance Criteria:**

- [ ] `Pull` against a public, existing tag succeeds and the image appears
      under that ref in `podman images`.
- [ ] `Pull` against a non-existent tag returns `*PullError` with
      `Kind == PullErrorNotFound`.
- [ ] `Pull` against an unreachable host (e.g. `ghcr.invalid`) returns
      `Kind == PullErrorNetwork`.
- [ ] `Pull` honors `Timeout`: setting `100ms` against a real registry
      yields `Kind == PullErrorNetwork` (deadline exceeded).
- [ ] `Tag` after a successful pull produces a second name visible in
      `podman images <dst>`.
- [ ] Existing `Build`, `HasImage`, `LiveImageRefs`, `RemoveImage` paths are
      unchanged and pass their pre-existing tests.

---

### Unit 4: Eligibility check

**File:** `internal/kits/registry.go` (add method) — placed here to keep the
"is this kit list pull-eligible" logic adjacent to the kit metadata.

```go
// AllBuiltin reports whether every kit in the resolved list comes from the
// built-in registry (i.e., none are user-authored or shadowed). Pull-from-
// registry eligibility hinges on this: a user kit's content cannot match a
// pre-published image, so the pull path skips them and falls through to
// local build.
func AllBuiltin(res Resolved) bool {
    for _, k := range res.Kits {
        if k.Source != "builtin" {
            return false
        }
    }
    return true
}
```

**Implementation Notes:**

- The check is over the *resolved* list, not the requested list — so a
  user kit pulled in transitively via `depends_on` also disables the pull
  path. Correct: the transitive content must match too.
- `Kit.Source` is already populated by `Registry.Get` ("builtin" or
  "user:<path>") — no schema change.

**Acceptance Criteria:**

- [ ] Returns true for the four pre-published kit lists when no user kits
      shadow built-ins.
- [ ] Returns false when `~/.config/agentbox/kits/claude/` exists and
      shadows the built-in `claude` kit.
- [ ] Returns false for an empty resolved list (defensive — eligibility
      requires evidence, not absence).

---

### Unit 5: Builder pull-or-build path

**File:** `internal/kits/builder.go` (edit `Build` + add helper)

Extend `BuildOpts` and `BuildResult` and weave a pull attempt into `Build`:

```go
type BuildOpts struct {
    NoCache bool
    NoPull  bool          // skip the registry pull attempt unconditionally
    Stdout  io.Writer
    Stderr  io.Writer
}

type BuildResult struct {
    Tag        string
    CacheHit   bool
    PullHit    bool      // image came from the registry
    Dockerfile string
    Kits       []string
}

// Builder gains two fields populated by the CLI wiring layer (Unit 7):
type Builder struct {
    Registry        *Registry
    Cache           *Cache
    Runner          Runner
    Version         string

    // Registry-pull plumbing. When RegistryHost is empty, the pull path is
    // skipped (preserving Phase-2 behavior for tests/legacy callers).
    RegistryHost    string         // e.g. "ghcr.io/nklisch/agentbox-kits"; "" disables pull
    RegistryEnabled bool           // mirrors cfg.Registry.Enabled
    RegistryVerify  string         // "none" today
    PullTimeout     time.Duration  // 0 = no timeout
}
```

In `Build`, after the cache miss but before `stageContext`:

```go
// (existing): cache miss path begins here.

// Try the registry path before doing a local build. Eligibility:
// - registry enabled
// - pull not explicitly suppressed for this call
// - all kits in the resolved list are built-in (not shadowed by user kits)
// - host is configured
if b.RegistryEnabled && !opts.NoPull && b.RegistryHost != "" && AllBuiltin(res) {
    pulled, perr := b.tryPullAndTag(res, opts)
    if perr == nil && pulled {
        result.PullHit = true
        // Save cache entry so subsequent runs short-circuit on the cache.
        hashes, herr := HashesFromResolved(res)
        if herr != nil {
            return result, fmt.Errorf("hash kits after pull: %w", herr)
        }
        entry := CacheEntry{
            Tag:             res.Tag,
            Kits:            res.Names(),
            KitHashes:       hashes,
            BuiltAt:         time.Now().UTC(),
            AgentboxVersion: b.Version,
            Source:          "registry", // NEW field; see Unit 6
        }
        if err := b.Cache.Save(entry, dockerfile); err != nil {
            return result, fmt.Errorf("save cache after pull: %w", err)
        }
        return result, nil
    }
    // perr != nil: visibly logged inside tryPullAndTag; continue to local build.
}

// (existing): stageContext + Build + cache write.
```

Add the helper:

```go
// tryPullAndTag attempts to pull the remote image and retag it with the
// canonical local tag. Returns (true, nil) on success. Returns (false, nil)
// when the pull failed in a way the consumer should silently fall back from
// (NotFound). Returns (false, err) on classified failures the user should
// see (Auth, Network) — the caller still falls back to local build, but
// surfaces the wrapped error.
func (b *Builder) tryPullAndTag(res Resolved, opts BuildOpts) (bool, error) {
    ref := runspec.RemoteImageRef(b.RegistryHost, b.Version, res.Names())
    if ref == "" {
        return false, nil
    }

    fmt.Fprintf(opts.Stderr, "[registry] pulling %s\n", ref)
    perr := b.Runner.Pull(PullContext{
        Ref:     ref,
        Timeout: b.PullTimeout,
        Stdout:  opts.Stdout,
        Stderr:  opts.Stderr,
    })
    if perr != nil {
        var pe *PullError
        if errors.As(perr, &pe) {
            switch pe.Kind {
            case PullErrorNotFound:
                fmt.Fprintf(opts.Stderr, "[registry] no image at %s; building locally\n", ref)
                return false, nil
            case PullErrorAuth:
                fmt.Fprintf(opts.Stderr,
                    "[registry] auth required for %s — try `%s login %s`; "+
                    "falling back to local build\n",
                    ref, b.Runner.Bin(), strings.SplitN(b.RegistryHost, "/", 2)[0])
                return false, perr
            case PullErrorNetwork:
                fmt.Fprintf(opts.Stderr,
                    "[registry] network error pulling %s; falling back to local build\n", ref)
                return false, perr
            default:
                fmt.Fprintf(opts.Stderr,
                    "[registry] pull failed (%s); falling back to local build\n", pe.Stderr)
                return false, perr
            }
        }
        return false, perr
    }

    if err := b.Runner.Tag(ref, res.Tag); err != nil {
        // Tagging failed after a successful pull — odd. Don't claim a hit;
        // fall back to building (which will succeed and overwrite the tag).
        fmt.Fprintf(opts.Stderr, "[registry] retag %s -> %s failed: %v\n", ref, res.Tag, err)
        return false, err
    }
    fmt.Fprintf(opts.Stderr, "[registry] pulled %s as %s\n", ref, res.Tag)
    return true, nil
}
```

Also expose `Bin()` on `Runner`:

```go
type Runner interface {
    // ... existing methods ...
    Bin() string // "podman" or "docker"; for user-facing error messages
}

// In PodmanRunner:
func (r *PodmanRunner) Bin() string { return r.Bin }
```

Wait — `Bin` would shadow the field. Rename the field to `bin` (unexported) and
add a `Bin()` accessor, or use a different method name. Pick:

```go
type PodmanRunner struct {
    bin string
}

func NewPodmanRunner(bin string) *PodmanRunner { return &PodmanRunner{bin: bin} }
func (r *PodmanRunner) Bin() string             { return r.bin }
```

Update existing call sites that reference `r.Bin` directly (the four current
methods in `podman.go`) to use `r.bin`.

Add `"github.com/nklisch/agentbox/internal/runspec"` to `builder.go` imports
(not currently present).

**Implementation Notes:**

- Pull happens *after* cache check so a previously-pulled image short-circuits
  on subsequent runs without contacting the registry.
- `Source: "registry"` in the cache entry distinguishes pull-derived images
  from locally built ones — useful for `agentbox build --list` future work
  and harmless to old readers (Go JSON ignores unknown fields when reading
  too, since it's a struct field; absent in old entries reads as "").
- The pull path writes the same cache schema, so the existing
  `HasMatch` (kit_hashes comparison) still correctly invalidates when kit
  content changes locally — even if the user pulls a remote, then modifies
  a built-in kit on disk, the next build sees a hash mismatch and rebuilds
  locally.
- Failures are *visibly* surfaced to stderr but never abort the build. The
  user sees "[registry] ..." lines they can grep / silence with `--quiet`.

**Acceptance Criteria:**

- [ ] With `RegistryEnabled=true`, host set, and a published kit list, the
      first `Build` returns `PullHit=true`, the local tag exists, and
      `<tag>.json` cache entry has `source = "registry"`.
- [ ] Second `Build` with the same kit list returns `CacheHit=true` and
      makes zero network calls (verify by setting host to an unreachable
      address and confirming success).
- [ ] `Build` with `NoPull=true` skips the registry path entirely and
      builds locally even when eligible.
- [ ] `Build` against a kit list with a user-shadowed kit skips the
      registry path (no pull attempt logged) and builds locally.
- [ ] `Build` after `RegistryEnabled=false` skips the registry path.
- [ ] `Build` with `NoCache=true` ignores the registry path entirely
      (forces local rebuild even if remote exists). This matches the
      existing `--no-cache` semantics.
- [ ] Auth failure surfaces a visible "[registry] auth required ..." line
      and the build still completes via local fallback.

---

### Unit 6: Cache entry source marker

**File:** `internal/kits/cache.go` (edit struct)

Add a `Source` field to `CacheEntry`. Old readers tolerate it (Go JSON
ignores unknown fields when decoding); old entries decode with `Source = ""`,
which the writer can interpret as "local build" when needed.

```go
type CacheEntry struct {
    Tag             string            `json:"tag"`
    Kits            []string          `json:"kits"`
    KitHashes       map[string]string `json:"kit_hashes"`
    BuiltAt         time.Time         `json:"built_at"`
    AgentboxVersion string            `json:"agentbox_version"`
    Source          string            `json:"source,omitempty"` // "registry" | "" (local)
}
```

In the existing local-build path inside `Builder.Build`, set
`entry.Source = "local"` for new entries (for clarity in the JSON; old
entries without the field still read as the empty string and work fine).

**Implementation Notes:**

- `omitempty` keeps existing JSON fixtures (and any test golden files) from
  drifting on entries that don't set the field.
- No existing reader breaks: cache `HasMatch` only consults `KitHashes`.

**Acceptance Criteria:**

- [ ] A pull-derived cache entry serializes with `"source": "registry"`.
- [ ] A local-built cache entry serializes with `"source": "local"`.
- [ ] An old entry without `source` decodes cleanly and `HasMatch` still
      returns true on a kit-hash match.

---

### Unit 7: CLI wiring — `newBuilder` populates registry fields

**File:** `internal/cli/build.go` (edit), `internal/cli/run.go` (edit
where `newBuilder` is called — same helper).

Update `newBuilder` to take the full config and propagate the registry
fields:

```go
func newBuilder(cfg config.Config) (*kits.Builder, error) {
    userKitsDir, _ := userKitsDirPath()
    reg := kits.NewRegistry(builtinkits.FS(), userKitsDir)
    cache, err := kits.NewCache()
    if err != nil {
        return nil, exitcode.Wrap(exitcode.Generic, err)
    }
    timeout, _ := time.ParseDuration(cfg.Registry.PullTimeout) // Validate already accepted it
    return &kits.Builder{
        Registry:        reg,
        Cache:           cache,
        Runner:          kits.NewPodmanRunner(cfg.Runtime),
        Version:         version.Version,
        RegistryEnabled: cfg.Registry.Enabled,
        RegistryHost:    cfg.Registry.Host,
        RegistryVerify:  cfg.Registry.Verify,
        PullTimeout:     timeout,
    }, nil
}
```

Update every caller — currently `internal/cli/build.go` passes
`res.Config.Runtime`; switch to `res.Config`. Likewise the lifecycle wiring
(`internal/lifecycle/lifecycle.go`'s `Lifecycle.Builder` field is set by
the CLI in `internal/cli/run.go`'s setup — pass the full config there too).

Add `--no-pull` flag to both `agentbox build` and `agentbox run`:

In `build.go`:

```go
var noPull bool
// ... inside newBuildCmd ...
cmd.Flags().BoolVar(&noPull, "no-pull", false, "skip the registry pull attempt; build locally")
// ... when calling Builder.Build, pass kits.BuildOpts{NoCache: noCache, NoPull: noPull, ...}
```

In `run.go`'s `RunOpts` and flag wiring (analog to existing `Fresh`):

```go
type RunOpts struct {
    Agent    string
    Kits     []string
    Fresh    bool
    Attach   bool
    Network  string
    NoZellij bool
    Layout   string
    NoPull   bool // NEW
}
```

`Lifecycle.ensureBox` (or wherever `Builder.Build` is called from
lifecycle) must thread `opts.NoPull` into `kits.BuildOpts.NoPull`.

**Implementation Notes:**

- The eligibility check (Unit 4) lives in the builder; the CLI just toggles
  whether to *try* the pull. So `--no-pull` is a hard skip even when the
  kit list is otherwise eligible.
- `time.ParseDuration` returning an error is impossible here because
  `Validate()` (Unit 1) already rejected the config — but ignore the error
  defensively and treat it as zero timeout.

**Acceptance Criteria:**

- [ ] `agentbox build polyglot,claude --no-pull` builds locally even when
      the registry is reachable (verify with no `[registry] pull...` log
      lines on stderr).
- [ ] `agentbox run --no-pull` propagates the flag through to the builder.
- [ ] `agentbox build` with no flags pulls from the registry on a fresh
      host.
- [ ] `agentbox config show` reflects the registry block.

---

### Unit 8: `registry-reachable` doctor check

**File:** `internal/doctor/doctor.go` (edit — add check to the slice)

```go
// registryReachableCheck does a HEAD against the registry's manifests
// endpoint for one well-known alias to confirm the registry is reachable
// and the publisher is publishing. Warn-only on any failure — boxes still
// build locally if the registry is offline.
func registryReachableCheck(cfg config.Config) Check {
    const name = "registry-reachable"
    if !cfg.Registry.Enabled || cfg.Registry.Host == "" {
        return Check{Name: name, Status: StatusOK,
            Message: "registry pull disabled; skipping reachability check"}
    }
    // Probe the rolling alias for one published kit list. The CLI knows
    // none of the published nicknames natively (consumer ignorance is the
    // design), but for *doctor* the well-known anchor is acceptable.
    probeRef := cfg.Registry.Host + ":latest-polyglot-containers-claude"
    url, err := manifestProbeURL(probeRef)
    if err != nil {
        return Check{Name: name, Status: StatusWarn,
            Message: fmt.Sprintf("could not derive probe URL: %v", err)}
    }

    client := &http.Client{Timeout: 5 * time.Second}
    req, err := http.NewRequest(http.MethodHead, url, nil)
    if err != nil {
        return Check{Name: name, Status: StatusWarn, Message: err.Error()}
    }
    req.Header.Set("Accept", "application/vnd.oci.image.index.v1+json")
    resp, err := client.Do(req)
    if err != nil {
        return Check{Name: name, Status: StatusWarn,
            Message: fmt.Sprintf("registry %s unreachable: %v (will fall back to local build)", cfg.Registry.Host, err)}
    }
    defer resp.Body.Close()

    switch {
    case resp.StatusCode == 200:
        return Check{Name: name, Status: StatusOK,
            Message: fmt.Sprintf("%s reachable; published images present", cfg.Registry.Host)}
    case resp.StatusCode == 404:
        return Check{Name: name, Status: StatusWarn,
            Message: fmt.Sprintf("%s reachable but %s not published yet (will fall back to local build)",
                cfg.Registry.Host, probeRef)}
    case resp.StatusCode == 401 || resp.StatusCode == 403:
        return Check{Name: name, Status: StatusWarn,
            Message: fmt.Sprintf("%s requires auth (HTTP %d); try `podman login %s`",
                cfg.Registry.Host, resp.StatusCode, registryDomain(cfg.Registry.Host))}
    default:
        return Check{Name: name, Status: StatusWarn,
            Message: fmt.Sprintf("%s probe returned HTTP %d", cfg.Registry.Host, resp.StatusCode)}
    }
}

// manifestProbeURL converts an OCI ref like
// "ghcr.io/nklisch/agentbox-kits:latest-polyglot-claude" into the v2
// registry manifest URL: https://ghcr.io/v2/nklisch/agentbox-kits/manifests/latest-polyglot-claude
func manifestProbeURL(ref string) (string, error) {
    host, repo, tag, ok := splitOCIRef(ref)
    if !ok {
        return "", fmt.Errorf("malformed ref %q", ref)
    }
    return fmt.Sprintf("https://%s/v2/%s/manifests/%s", host, repo, tag), nil
}

// splitOCIRef parses "ghcr.io/owner/repo:tag" into its three parts.
func splitOCIRef(ref string) (host, repo, tag string, ok bool) {
    colon := strings.LastIndex(ref, ":")
    slash := strings.Index(ref, "/")
    if colon <= 0 || slash < 0 || slash > colon {
        return "", "", "", false
    }
    host = ref[:slash]
    repo = ref[slash+1 : colon]
    tag = ref[colon+1:]
    return host, repo, tag, repo != "" && tag != ""
}

// registryDomain returns the domain portion of a host like
// "ghcr.io/nklisch/agentbox-kits" -> "ghcr.io".
func registryDomain(host string) string {
    if i := strings.Index(host, "/"); i > 0 {
        return host[:i]
    }
    return host
}
```

Register the check in `Run()`:

```go
checks := []Check{
    runtimeCheck(cfg.Runtime),
    stateDirCheck(),
    iptablesCheck(),
    ipsetCheck(),
    sudoIptablesCheck(),
    corednsImageCheck(cfg.Runtime),
    containersConfigCheck(cfg),
    registryReachableCheck(cfg), // NEW — placed after coredns/containers, before podman-machine
    podmanMachineCheck(),
    kitCacheCheck(),
    mountSourcesCheck(cfg),
}
```

Add `"net/http"`, `"time"`, `"strings"` to imports if not already present.

**Implementation Notes:**

- The probe uses a *known-published* alias rather than reproducing the
  CLI's eligibility logic — doctor is allowed to know about CI's publishing
  catalog. If we change the published-list nicknames, this check goes stale
  and surfaces a warning, which is fine.
- 5-second timeout is short enough not to slow `doctor`. The check is
  warn-only.
- No `Fix` closure: the failure modes are all "outside agentbox's control"
  (network, auth, publisher hasn't shipped). Surfacing the corrective
  action is the most we can do.

**Acceptance Criteria:**

- [ ] `agentbox doctor` includes a `registry-reachable` row.
- [ ] Status is OK when GHCR is reachable and the alias is published.
- [ ] Status is WARN with a clear message when the registry is offline (use
      `--config` pointing at a config with `host = "ghcr.invalid/x/y"` to
      reproduce).
- [ ] Status is OK with the "skipping" message when `[registry] enabled = false`.

---

### Unit 9: `agentbox build --emit-context <dir>` flag

**File:** `internal/cli/build.go` (edit) + `internal/kits/builder.go` (add
method)

The CI workflow needs the build context (Dockerfile + staged kit dirs) on
disk so `docker buildx build --push` can consume it. Today, `stageContext`
is private and runs only inside `Build`. Lift it behind a public method.

```go
// EmitContext stages the resolved build context to dst (created if absent).
// Writes:
//   <dst>/Dockerfile
//   <dst>/kits/<name>/...    (one subdir per resolved kit)
// dst must not exist or must be empty — refuses to overwrite existing files
// to avoid surprise.
func (b *Builder) EmitContext(requested []string, dst string) error {
    if dst == "" {
        return fmt.Errorf("emit-context: dst is empty")
    }
    if entries, err := os.ReadDir(dst); err == nil && len(entries) > 0 {
        return fmt.Errorf("emit-context: %s is not empty", dst)
    } else if err != nil && !errors.Is(err, fs.ErrNotExist) {
        return fmt.Errorf("emit-context: stat %s: %w", dst, err)
    }
    if err := os.MkdirAll(dst, 0o755); err != nil {
        return err
    }

    res, err := Resolve(b.Registry, requested)
    if err != nil {
        return err
    }
    dockerfile := GenerateDockerfile(res, b.Version)

    // Stage kits + Dockerfile. Reuses the same copyFS the temp-dir path
    // uses, so the on-disk layout matches what the runner sees.
    for _, k := range res.Kits {
        kitDst := filepath.Join(dst, "kits", k.Manifest.Name)
        if err := copyFS(k.FS, kitDst); err != nil {
            return fmt.Errorf("stage kit %q: %w", k.Manifest.Name, err)
        }
    }
    return os.WriteFile(filepath.Join(dst, "Dockerfile"), []byte(dockerfile), 0o644)
}
```

In `internal/cli/build.go`:

```go
var emitContext string
// ... inside newBuildCmd ...
cmd.Flags().StringVar(&emitContext, "emit-context", "",
    "stage the build context (Dockerfile + kit dirs) to <dir> instead of building")
cmd.MarkFlagsMutuallyExclusive("print", "list", "prune", "emit-context")

// ... in the RunE switch ...
case emitContext != "":
    return runBuildEmitContext(cmd, b, kitList(args, res.Config.DefaultKits), emitContext)
```

Add `runBuildEmitContext`:

```go
func runBuildEmitContext(cmd *cobra.Command, b *kits.Builder, requested []string, dst string) error {
    if len(requested) == 0 {
        return exitcode.New(exitcode.InvalidArgs, "no kits requested and default_kits is empty")
    }
    if err := b.EmitContext(requested, dst); err != nil {
        return exitcode.Wrap(exitcode.KitBuild, err)
    }
    fmt.Fprintf(cmd.OutOrStdout(), "context emitted to %s\n", dst)
    return nil
}
```

Add `"errors"`, `"io/fs"` to `builder.go` imports if needed.

**Implementation Notes:**

- `EmitContext` does not invoke the runner — it's a pure-Go primitive that
  the CI workflow can use without `podman build` semantics interfering.
- Refusing to write into a non-empty dir prevents footguns where someone
  emits over their project dir or a worktree.
- Refactor opportunity: `stageContext` now duplicates the inner copy-loop.
  Inline `stageContext` to call `EmitContext` against a temp dir, or extract
  a private `stageInto(dst, res)` helper. Pick the latter — clearer
  function names, single source of truth for the staging logic.

**Acceptance Criteria:**

- [ ] `agentbox build --emit-context /tmp/ctx polyglot,claude` writes
      `/tmp/ctx/Dockerfile` and `/tmp/ctx/kits/{base,polyglot,node,claude}/`
      with each kit's files.
- [ ] Re-running into a non-empty dir errors with exit code 5 and a clear
      message.
- [ ] The emitted Dockerfile is byte-identical to
      `agentbox build --print polyglot,claude`.
- [ ] `--emit-context` is mutually exclusive with `--print`, `--list`,
      `--prune` (cobra rejects with exit code 2).

---

### Unit 10: Published-kit-list catalog

**File:** `.github/published-kits.yml` (NEW)

A separate file (rather than inlining the list in the workflow YAML) so a
future "is this kit list pre-published?" check from any tool can read one
canonical source.

```yaml
# Source of truth for which kit-list compositions are pre-built and
# published to ghcr.io/nklisch/agentbox-kits on release.
#
# Each entry describes a tuple of:
#   nickname  — human-readable suffix on the alias tags
#   kits      — comma-separated kit list (matching the `agentbox build` arg)
#
# The version-pinned, sha-pinned tag (consumed by the CLI) is computed at
# publish time. The aliases (nickname-suffixed) are also published for
# human-driven `podman pull`.
images:
  - nickname: polyglot-containers-claude
    kits: polyglot,containers,claude
  - nickname: polyglot-containers-codex
    kits: polyglot,containers,codex
  - nickname: polyglot-claude
    kits: polyglot,claude
  - nickname: node-claude
    kits: node,claude
```

**Implementation Notes:**

- One YAML file, two consumers: the `kit-images.yml` workflow (Unit 11)
  and a future `agentbox doctor` published-list-aware check (out of scope).
- Adding a new published kit-list is a one-PR change here. No code change
  required.

**Acceptance Criteria:**

- [ ] File parses as YAML with the four entries above.
- [ ] CI workflow reads it and iterates without hardcoding the list inline.

---

### Unit 11: `kit-images.yml` workflow

**File:** `.github/workflows/kit-images.yml` (NEW)

```yaml
name: kit-images

on:
  push:
    tags: ['v*']
  workflow_dispatch:
    inputs:
      version:
        description: 'Version tag to build under (e.g. v0.3.0). Required for manual runs.'
        required: true

permissions:
  contents: read
  packages: write

jobs:
  publish:
    runs-on: ubuntu-latest
    strategy:
      fail-fast: false
      matrix:
        # Read images from .github/published-kits.yml. Hand-mirrored here
        # because matrix can't read external files; the matrix and the YAML
        # MUST stay in sync — guarded by the `verify-matrix` step below.
        include:
          - nickname: polyglot-containers-claude
            kits: polyglot,containers,claude
          - nickname: polyglot-containers-codex
            kits: polyglot,containers,codex
          - nickname: polyglot-claude
            kits: polyglot,claude
          - nickname: node-claude
            kits: node,claude
    steps:
      - name: Checkout
        uses: actions/checkout@v6
        with:
          fetch-depth: 0

      - name: Resolve version
        id: version
        run: |
          if [ "${{ github.event_name }}" = "workflow_dispatch" ]; then
            echo "version=${{ inputs.version }}" >> "$GITHUB_OUTPUT"
          else
            echo "version=${GITHUB_REF#refs/tags/}" >> "$GITHUB_OUTPUT"
          fi

      - name: Verify matrix matches published-kits.yml
        run: |
          # Fail loudly if .github/published-kits.yml drifts from the matrix
          # above. yq is preinstalled on ubuntu-latest runners.
          declare -A from_yaml
          while IFS=$'\t' read -r nick kits; do
            from_yaml[$nick]=$kits
          done < <(yq -r '.images[] | [.nickname, .kits] | @tsv' .github/published-kits.yml)
          if [ "${from_yaml[${{ matrix.nickname }}]}" != "${{ matrix.kits }}" ]; then
            echo "::error::matrix entry ${{ matrix.nickname }} ('${{ matrix.kits }}') does not match published-kits.yml entry ('${from_yaml[${{ matrix.nickname }}]}')" >&2
            exit 1
          fi

      - name: Set up Go
        uses: actions/setup-go@v6
        with:
          go-version-file: go.mod
          cache: true

      - name: Build agentbox CLI
        run: |
          # Build with the same ldflags the release uses so --version and
          # the registry tag (which embeds the version) match.
          go build \
            -ldflags "-s -w \
              -X github.com/nklisch/agentbox/internal/version.Version=${{ steps.version.outputs.version }} \
              -X github.com/nklisch/agentbox/internal/version.Commit=$(git rev-parse --short HEAD) \
              -X github.com/nklisch/agentbox/internal/version.Date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
            -o ./agentbox \
            ./cmd/agentbox

      - name: Emit build context
        id: emit
        run: |
          ./agentbox build --emit-context ./build-ctx ${{ matrix.kits }}
          # Compute the version-pinned, sha-pinned tag the CLI will look for.
          # `agentbox build --print` happens to print the tag in a comment
          # header line; cleaner: add a `--print-tag` later. For v0.3, parse
          # the cache name out of `agentbox build` against the same kit list.
          # Workaround: re-derive the sha here using `agentbox` itself.
          echo "tag=$(./agentbox build --print ${{ matrix.kits }} | head -1 | grep -oE '[0-9a-f]{12}')" >> "$GITHUB_OUTPUT"

      - name: Set up QEMU
        uses: docker/setup-qemu-action@v3

      - name: Set up Buildx
        uses: docker/setup-buildx-action@v3

      - name: Log in to ghcr.io
        uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}

      - name: Build and push (multi-arch)
        uses: docker/build-push-action@v6
        with:
          context: ./build-ctx
          file: ./build-ctx/Dockerfile
          platforms: linux/amd64,linux/arm64
          push: true
          # Three tags published per image:
          #   <version>-<sha>                (CLI's deterministic lookup; immutable)
          #   <version>-<nickname>           (human-readable; immutable)
          #   latest-<nickname>              (rolling; reassigned each release)
          tags: |
            ghcr.io/${{ github.repository_owner }}/agentbox-kits:${{ steps.version.outputs.version }}-${{ steps.emit.outputs.tag }}
            ghcr.io/${{ github.repository_owner }}/agentbox-kits:${{ steps.version.outputs.version }}-${{ matrix.nickname }}
            ghcr.io/${{ github.repository_owner }}/agentbox-kits:latest-${{ matrix.nickname }}
          cache-from: type=gha,scope=${{ matrix.nickname }}
          cache-to:   type=gha,scope=${{ matrix.nickname }},mode=max
```

**Implementation Notes:**

- Triggers: `push` to a `v*` tag (in lockstep with the existing `release.yml`)
  and `workflow_dispatch` for re-publishing without a new tag.
- `verify-matrix` step is a guard against the YAML and matrix drifting —
  cheaper than refactoring the matrix to consume the YAML at runtime
  (Actions doesn't support that natively without an extra `setup-matrix`
  job).
- The `$tag` derivation parses the sha out of `agentbox build --print`'s
  generated Dockerfile header. **Better:** add a `agentbox build --print-tag`
  flag in a follow-up unit so the workflow doesn't grep — flagged but
  deferred to keep this design tightly scoped.
- `cache-from`/`cache-to` per-nickname so cross-list builds don't pollute
  each other's layer caches.
- `linux/amd64,linux/arm64` produces a multi-arch manifest. arm64 build is
  emulated via QEMU (slow — minutes-to-tens-of-minutes for chunky lists).
  Acceptable for release workflows.

**Acceptance Criteria:**

- [ ] On `git push tag v0.3.x`, the workflow runs and publishes four
      multi-arch manifests to `ghcr.io/nklisch/agentbox-kits`.
- [ ] Each image carries three tags: `<version>-<sha>`,
      `<version>-<nickname>`, `latest-<nickname>`.
- [ ] `podman pull --platform linux/arm64 ghcr.io/nklisch/agentbox-kits:latest-node-claude`
      succeeds on an arm64 host and the resulting image runs `box info`
      without error.
- [ ] `verify-matrix` step fails the job when `.github/published-kits.yml`
      and the matrix disagree.

---

## Implementation Order

1. **Unit 1** — config schema + defaults + validation.
2. **Unit 2** — `RemoteImageRef` and `RemoteAliasRef` helpers (pure;
   testable in isolation).
3. **Unit 3** — `Runner.Pull`/`Tag` interface + `PodmanRunner`
   implementation + `PullError` taxonomy.
4. **Unit 4** — `AllBuiltin` eligibility helper.
5. **Unit 6** — cache `Source` field. (Sequenced before Unit 5 because
   Unit 5's pull path writes the field.)
6. **Unit 5** — Builder pull-or-build path, the load-bearing change.
7. **Unit 7** — CLI wiring: `newBuilder` consumes config, `--no-pull` flag
   on `build` and `run`.
8. **Unit 8** — doctor `registry-reachable` check.
9. **Unit 9** — `--emit-context` flag (decoupled from Group A; can land in
   either order).
10. **Unit 10** — `published-kits.yml` catalog file.
11. **Unit 11** — `kit-images.yml` workflow.

Group A (Units 1–8) ships independently. Group B (Units 9–11) depends only
on Unit 9 from Group A and is otherwise standalone.

## Testing

### Unit Tests

#### `internal/runspec/runspec_test.go`
- `TestRemoteImageRef`: round-trip kit lists, version normalization
  (v-prefix vs no-prefix), empty host returns "", reordering invariance.
- `TestRemoteAliasRef`: base stripped, kit ordering preserved, empty
  version returns "".

#### `internal/kits/builder_test.go`
Add a fake `Runner` (`fakeRunner`) that records `Pull`/`Tag` calls and
returns scripted `*PullError` values:

- `TestBuild_PullHit_WhenAllBuiltinAndEnabled`: scripted pull succeeds;
  result has `PullHit=true`, `CacheHit=false`, cache entry has
  `Source=="registry"`.
- `TestBuild_NoPullFlag_SkipsRegistry`: pull never called when
  `BuildOpts.NoPull=true`; falls through to local build.
- `TestBuild_RegistryDisabled_SkipsRegistry`: same as above when
  `Builder.RegistryEnabled=false`.
- `TestBuild_UserKitDisablesPull`: registry is enabled, but the resolved
  list contains a kit with `Source=="user:..."` — pull is not called.
- `TestBuild_PullNotFound_FallsBackToLocal`: pull returns
  `PullError{Kind: PullErrorNotFound}`; build proceeds via local path,
  result `PullHit=false`.
- `TestBuild_PullAuth_FallsBackWithError`: pull returns Auth; local build
  succeeds; the Auth error is logged to stderr (verify via captured
  writer).
- `TestBuild_NoCache_SkipsRegistry`: `BuildOpts.NoCache=true` skips both
  cache and pull paths; local build runs.
- `TestBuild_CacheHit_SkipsPull`: prepopulate the cache + pre-existing
  image; verify pull is never called.

#### `internal/kits/podman_test.go`
- `TestPodmanRunner_Pull_NotFound`: integration test against a known-bad
  ref (`ghcr.io/nklisch/agentbox-kits:never-published-tag-test`); skip if
  podman absent.
- `TestPodmanRunner_Pull_NetworkTimeout`: short timeout against a real
  host; verify `PullError.Kind == PullErrorNetwork`.
- `TestClassifyPullStderr`: table-driven against fixture stderr strings
  copied from real `podman pull` failures.

#### `internal/kits/registry_test.go`
- `TestAllBuiltin_AllBuiltin`: returns true for a built-in-only resolved
  list.
- `TestAllBuiltin_UserShadow`: returns false when one kit's `Source` is
  `"user:..."`.
- `TestAllBuiltin_EmptyList`: returns false.

#### `internal/doctor/doctor_test.go`
- `TestRegistryReachableCheck_Disabled`: returns OK with skip message.
- `TestRegistryReachableCheck_Reachable`: spin up `httptest.Server`
  returning 200 for `/v2/.../manifests/...`; verify OK.
- `TestRegistryReachableCheck_NotPublished`: server returns 404; verify
  WARN with "not published yet" message.
- `TestRegistryReachableCheck_AuthRequired`: server returns 401; WARN
  with `podman login` suggestion.
- `TestRegistryReachableCheck_Unreachable`: point at an unroutable host;
  WARN with the network-error message.

### Integration / End-to-end

#### `internal/kits/builder_e2e_test.go` (build-tag, gated by `agentbox` runner availability)
- `TestE2E_BuildPullsFromRegistry`: with a real podman, run `Build` with
  the registry enabled and the host pointing at a real published image.
  Verify the local tag exists and `cat <cache>.json | jq .source` is
  `"registry"`.

#### CI smoke (manual / one-off)
- After `kit-images.yml` publishes, `podman pull` each of the four
  versioned tags + their aliases on both linux/amd64 and linux/arm64
  hosts and verify `podman run --rm <tag> box info` succeeds.

## Verification Checklist

```sh
# Group A — consumer side
go build ./...
go test ./internal/runspec/... ./internal/kits/... ./internal/doctor/...
go vet ./...

# 1. Config: registry block round-trips.
agentbox config show --json | jq '.registry'

# 2. Eligibility: pull happens for built-in-only list.
rm -rf ~/.local/share/agentbox/cache/kits
podman rmi -f $(podman images -q 'agentbox/*') 2>/dev/null
agentbox build polyglot,claude 2>&1 | grep -E '\[registry\] (pulling|pulled)'

# 3. Cache hit on second build.
agentbox build polyglot,claude 2>&1 | grep -q 'cache hit'

# 4. --no-pull skips registry.
podman rmi -f $(podman images -q 'agentbox/*') 2>/dev/null
rm -rf ~/.local/share/agentbox/cache/kits
agentbox build polyglot,claude --no-pull 2>&1 | grep -v '\[registry\]' && \
  agentbox build polyglot,claude --no-pull 2>&1 | grep -q 'built:'

# 5. User kit disables pull.
mkdir -p ~/.config/agentbox/kits/claude && \
  cp -r /tmp/builtin-claude-snapshot/* ~/.config/agentbox/kits/claude/
agentbox build polyglot,claude 2>&1 | grep -v '\[registry\] pulling'
rm -rf ~/.config/agentbox/kits/claude

# 6. Auth failure visible but recoverable.
AGENTBOX_REGISTRY_HOST="ghcr.io/private-org/private-kits" \
  agentbox build polyglot,claude 2>&1 | grep -E 'auth required|local build'

# 7. Doctor surfaces the new check.
agentbox doctor | grep -q 'registry-reachable'

# Group B — producer side (after CI runs)
podman pull ghcr.io/nklisch/agentbox-kits:latest-polyglot-containers-claude
podman manifest inspect ghcr.io/nklisch/agentbox-kits:latest-polyglot-containers-claude | \
  jq '.manifests[].platform.architecture' | sort -u  # → ["amd64","arm64"]
podman run --rm ghcr.io/nklisch/agentbox-kits:latest-polyglot-containers-claude \
  zsh -lc 'box info; rg --version; claude --version'

# 8. Emit-context produces a buildable directory.
mkdir /tmp/abx-ctx
agentbox build --emit-context /tmp/abx-ctx polyglot,claude
test -f /tmp/abx-ctx/Dockerfile && test -d /tmp/abx-ctx/kits/base
docker buildx build --platform linux/amd64 -t test:emit /tmp/abx-ctx  # builds OK
```

## Out of scope / explicitly deferred

- **Cosign signing and verification.** `[registry] verify = "cosign"` is
  reserved at the config layer (rejected at parse time) but not
  implemented. Add when there's a credible threat model that requires it.
- **Pulling on a private registry.** The CLI doesn't do `podman login`
  for the user; if `[registry] host` points at a private registry, the
  user must `podman login` themselves. Auth failures surface clearly.
- **Pull resume / partial-pull handling.** Pulls are atomic from the
  CLI's perspective: success or full retry on the local-build path.
- **A `agentbox pull` subcommand.** The pull-then-fall-back path inside
  `build` covers the two real use cases (`agentbox build` to ensure local
  image; `agentbox run` to launch). A dedicated `pull` adds surface for
  marginal value — revisit if asked.
- **Progress UI for pulls.** Stream `podman pull`'s native output. Don't
  re-render or summarize; users have enough mental model for "downloading
  layers".
- **Image layer dedup across kit lists.** GHCR + buildx GHA cache do their
  own dedup; agentbox doesn't reach into that.
- **`--print-tag` flag.** Workflow currently grep-extracts the sha from
  `agentbox build --print`. A clean `--print-tag` flag is a follow-up
  one-liner; intentionally not included to keep this design scoped.

## Doc updates required (post-merge)

- `docs/SPEC.md` — flip the "Open / deferred" entry for kit image registry
  distribution; add the `[registry]` block to the config example with
  defaults; document the doctor check.
- `docs/CLI.md` — document `--no-pull` on `agentbox build` and `agentbox
  run`; document `--emit-context`; add `registry-reachable` row to the
  doctor checks table.
- `docs/KITS.md` — note that built-in kit lists with no user shadowing
  pull from GHCR by default; user kits force local builds.
- `docs/ARCHITECTURE.md` — add a brief section in "Kit build pipeline"
  describing the pull path and the eligibility check.
- `docs/ROADMAP.md` — mark the v0.3+ "Kit image registry distribution"
  item complete; update PROGRESS.md.

These doc changes are explicitly **not** part of the implementation units
above — they should land in a single follow-up PR after the code merges,
per the project's existing pattern (see `824f488 docs: prepare v0.3.0` and
`2a3b364 docs: align foundation/reference docs to v0.3.0`).
