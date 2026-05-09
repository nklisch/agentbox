package kits

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nklisch/agentbox/internal/config"
	"github.com/nklisch/agentbox/internal/runspec"
)

// Runner abstracts the container runtime (podman/docker). The kits package
// uses it through this interface so the domain stays adapter-free.
// The concrete PodmanRunner adapter lives in podman.go (same package) and is
// wired up at the CLI layer.
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

	// Bin returns the runtime binary name ("podman" or "docker"); used for
	// user-facing error messages.
	Bin() string
}

// PullContext is the input to Runner.Pull.
type PullContext struct {
	Ref     string        // the full remote reference, e.g. ghcr.io/n/agentbox-kits:0.3.0-abc123
	Timeout time.Duration // 0 = no timeout
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

// PullError is a typed error returned by Runner.Pull.
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

// BuildContext is the input to Runner.Build.
type BuildContext struct {
	Tag        string    // the image tag to apply
	Dockerfile string    // absolute path to the Dockerfile
	ContextDir string    // absolute path to the build context dir (parent of kits/)
	NoCache    bool      // pass through `--no-cache` to the underlying runtime
	Stdout     io.Writer // build progress
	Stderr     io.Writer
}

// BuildOpts controls Builder.Build behavior.
type BuildOpts struct {
	NoCache bool
	NoPull  bool      // skip the registry pull attempt unconditionally
	Stdout  io.Writer // for podman build progress
	Stderr  io.Writer
}

// BuildResult describes the outcome of Builder.Build.
type BuildResult struct {
	Tag        string
	CacheHit   bool
	PullHit    bool   // image came from the registry
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

	// Registry-pull plumbing. When RegistryHost is empty, the pull path is
	// skipped (preserving pre-registry behavior for tests/legacy callers).
	RegistryHost    string                // e.g. "ghcr.io/nklisch/agentbox-kits"; "" disables pull
	RegistryEnabled bool                  // mirrors cfg.Registry.Enabled
	RegistryVerify  string                // "none" today
	PullTimeout     time.Duration         // 0 = no timeout
	RegistryRefresh config.RefreshPolicy  // pre-parsed cfg.Registry.Refresh; zero value = off
	Now             func() time.Time      // time source for refresh age check; nil → time.Now
}

// Resolve resolves the requested kit list using the builder's registry and
// returns the resolved kit set with its canonical image tag. This is the
// same resolution step that Build performs internally. Provided as a
// convenience so callers (e.g. dry-run) can inspect the resolved set without
// triggering a build.
func (b *Builder) Resolve(requested []string) (Resolved, error) {
	return Resolve(b.Registry, requested)
}

// PrintDockerfile resolves the kit list and returns the Dockerfile string
// without staging a build context or calling the runner.
func (b *Builder) PrintDockerfile(requested []string) (string, error) {
	res, err := Resolve(b.Registry, requested)
	if err != nil {
		return "", err
	}
	return GenerateDockerfile(res, b.Version), nil
}

// ResolveForTag resolves the kit list and returns the canonical local image
// tag (agentbox/<sha12>) without building or staging anything. Used by
// --print-tag so CI can compute the version-pinned GHCR tag without grepping
// through Dockerfile output.
func (b *Builder) ResolveForTag(requested []string) (string, error) {
	res, err := Resolve(b.Registry, requested)
	if err != nil {
		return "", err
	}
	return res.Tag, nil
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

	// Cache check (unless NoCache bypasses it).
	if !opts.NoCache {
		match, err := b.Cache.HasMatch(res)
		if err != nil {
			return result, fmt.Errorf("check cache: %w", err)
		}
		if match {
			// Cache says we built this exact kit set. But two things can
			// invalidate that:
			//   1. The image got pruned externally (e.g. `podman rmi`).
			//   2. registry.refresh is enabled and the cache entry is older
			//      than the configured threshold — the user wants the
			//      rolling tag re-pulled.
			has, err := b.Runner.HasImage(res.Tag)
			if err != nil {
				return result, fmt.Errorf("check image: %w", err)
			}
			if has && !b.cacheStaleByRefresh(res) {
				result.CacheHit = true
				return result, nil
			}
			// Cache is stale (image gone, or refresh threshold elapsed);
			// fall through to the pull/build path.
		}
	}

	// Try the registry path before doing a local build. Eligibility:
	// - registry enabled
	// - pull not explicitly suppressed for this call
	// - NoCache not set (--no-cache forces a fresh local build)
	// - all kits in the resolved list are built-in (not shadowed by user kits)
	// - host is configured
	if !opts.NoCache && b.RegistryEnabled && !opts.NoPull && b.RegistryHost != "" && AllBuiltin(res) {
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
				BuiltAt:         b.now().UTC(),
				AgentboxVersion: b.Version,
				Source:          "registry",
			}
			if err := b.Cache.Save(entry, dockerfile); err != nil {
				return result, fmt.Errorf("save cache after pull: %w", err)
			}
			return result, nil
		}
		// perr != nil: visibly logged inside tryPullAndTag; continue to local build.
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

	// Save the cache entry so subsequent builds can detect hits.
	hashes, err := HashesFromResolved(res)
	if err != nil {
		return result, fmt.Errorf("hash kits: %w", err)
	}
	entry := CacheEntry{
		Tag:             res.Tag,
		Kits:            res.Names(),
		KitHashes:       hashes,
		BuiltAt:         b.now().UTC(),
		AgentboxVersion: b.Version,
		Source:          "local",
	}
	if err := b.Cache.Save(entry, dockerfile); err != nil {
		return result, fmt.Errorf("save cache: %w", err)
	}
	return result, nil
}

// cacheStaleByRefresh reports whether registry.refresh policy says we should
// bypass an otherwise-valid cache entry to re-pull the rolling tag. Returns
// false when refresh is disabled, when no cache entry exists, when the
// entry's BuiltAt is missing or in the future (clock skew), or when the
// entry is younger than the configured MaxAge. Returns true when the policy
// is "always" or when the entry is older than MaxAge.
func (b *Builder) cacheStaleByRefresh(res Resolved) bool {
	if !b.RegistryRefresh.Enabled {
		return false
	}
	if b.RegistryRefresh.Always {
		return true
	}
	entry, err := b.Cache.Lookup(res.Tag)
	if err != nil {
		// No entry → caller will treat the cache as a miss anyway. Returning
		// false here keeps the existing fallthrough semantics intact.
		return false
	}
	if entry.BuiltAt.IsZero() {
		return false
	}
	age := b.now().Sub(entry.BuiltAt)
	if age < 0 {
		// Clock went backwards (or BuiltAt is in the future). Don't force a
		// rebuild on the user — they didn't ask for arbitrary churn.
		return false
	}
	return age > b.RegistryRefresh.MaxAge
}

// now returns the configured time source or time.Now. Tests inject a fake
// clock via Builder.Now to exercise age-based refresh logic deterministically.
func (b *Builder) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

// tryPullAndTag attempts to pull the remote image and retag it with the
// canonical local tag. Returns (true, nil) on success. Returns (false, nil)
// when the pull failed in a way the consumer should silently fall back from
// (NotFound). Returns (false, err) on classified failures the user should
// see (Auth, Network) — the caller still falls back to local build, but
// surfaces the wrapped error.
//
// Ref selection: when RegistryRefresh is enabled, the rolling
// `latest-<nickname>` tag is used so the user picks up upstream package
// updates from the daily cron rebuild. Otherwise the immutable
// `<version>-<sha12>` tag is used (default; preserves reproducibility).
func (b *Builder) tryPullAndTag(res Resolved, opts BuildOpts) (bool, error) {
	var ref string
	if b.RegistryRefresh.Enabled {
		ref = runspec.RemoteRollingRef(b.RegistryHost, res.Names())
	} else {
		ref = runspec.RemoteImageRef(b.RegistryHost, b.Version, res.Names())
	}
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
		// Image may already be absent; non-fatal for prune.
		_ = b.Runner.RemoveImage(e.Tag)
		_ = b.Cache.Remove(e.Tag)
		removed = append(removed, e.Tag)
	}
	sort.Strings(removed)
	sort.Strings(kept)
	return PruneResult{Removed: removed, Kept: kept}, nil
}

// stageInto copies the resolved kit dirs into <dst>/kits/<name>/. Used by
// both stageContext (live builds) and EmitContext (CI build contexts).
// dst must already exist; individual kit subdirs are created as needed.
func stageInto(dst string, res Resolved) error {
	for _, k := range res.Kits {
		kitDst := filepath.Join(dst, "kits", k.Manifest.Name)
		if err := copyFS(k.FS, kitDst); err != nil {
			return fmt.Errorf("stage kit %q: %w", k.Manifest.Name, err)
		}
	}
	return nil
}

// stageContext writes res's kits to a fresh temp dir as kits/<name>/<files>.
// Returns the dir path and a cleanup function (which removes the dir on success
// or failure — callers defer it).
func stageContext(res Resolved) (string, func(), error) {
	dir, err := os.MkdirTemp("", "agentbox-build-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	if err := stageInto(dir, res); err != nil {
		cleanup()
		return "", nil, err
	}
	return dir, cleanup, nil
}

// EmitContext stages the resolved build context to dst (created if absent).
// Writes:
//
//	<dst>/Dockerfile
//	<dst>/kits/<name>/...    (one subdir per resolved kit)
//
// dst must not exist or must be empty — refuses to overwrite existing files
// to avoid surprise. Does not invoke the container runner; it is a pure
// staging primitive for CI (docker buildx build --push).
func (b *Builder) EmitContext(requested []string, dst string) error {
	if dst == "" {
		return fmt.Errorf("emit-context: dst is empty")
	}
	// Check whether dst exists and is non-empty.
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

	// Stage kit dirs. Reuses the same copyFS the temp-dir build path uses,
	// so the on-disk layout matches what the runner sees (DRY).
	if err := stageInto(dst, res); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dst, "Dockerfile"), []byte(dockerfile), 0o644)
}

// copyFS recursively copies srcFS rooted at "." into dst on disk. Preserves
// regular files only; sets executable bit on shell scripts (.sh) and on
// any file with no extension that lives in the kit's top-level dir (the
// `box` dispatcher and box-* helpers).
//
// embed.FS does not preserve file modes, so we reconstruct executability
// from naming convention here. The Dockerfile also runs `chmod +x install.sh`
// defensively.
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
