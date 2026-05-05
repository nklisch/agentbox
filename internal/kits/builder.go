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
// The concrete PodmanRunner adapter lives in podman.go (same package) and is
// wired up at the CLI layer.
type Runner interface {
	Build(ctx BuildContext) error
	HasImage(tag string) (bool, error)
	LiveImageRefs() ([]string, error)
	RemoveImage(tag string) error
}

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
// without staging a build context or calling the runner.
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

	// Cache check (unless NoCache bypasses it).
	if !opts.NoCache {
		match, err := b.Cache.HasMatch(res)
		if err != nil {
			return result, fmt.Errorf("check cache: %w", err)
		}
		if match {
			// Cache says we built this exact kit set. But the image might have
			// been pruned externally (e.g., `podman rmi`) — verify it still exists.
			has, err := b.Runner.HasImage(res.Tag)
			if err != nil {
				return result, fmt.Errorf("check image: %w", err)
			}
			if has {
				result.CacheHit = true
				return result, nil
			}
			// Cache is stale (image gone); fall through to rebuild.
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

	// Save the cache entry so subsequent builds can detect hits.
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
		// Image may already be absent; non-fatal for prune.
		_ = b.Runner.RemoveImage(e.Tag)
		_ = b.Cache.Remove(e.Tag)
		removed = append(removed, e.Tag)
	}
	sort.Strings(removed)
	sort.Strings(kept)
	return PruneResult{Removed: removed, Kept: kept}, nil
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
