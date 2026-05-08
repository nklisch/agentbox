package lifecycle

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// CollectExternalSymlinkTargets walks claudeDir looking for symlinks whose
// resolved target lives OUTSIDE claudeDir. Returns the deduped, sorted-ish
// (insertion-ordered) list of resolved target paths.
//
// Why this exists: users routinely symlink global skills/plugins into the
// agent config dir (e.g. ~/.claude/skills/<name> → ~/.agents/skills/<name>).
// agentbox bind-mounts ~/.claude into the box rw, which preserves the
// symlinks but doesn't make their targets reachable inside the container.
// The caller bind-mounts each returned target at its same host path inside
// the box (the project's "same-path mount" philosophy) so the preserved
// symlinks resolve to a real path, with host↔box state still live-synced.
//
// Symlinks pointing INSIDE claudeDir are ignored — the parent bind-mount
// already covers them. Dangling symlinks are silently skipped (best-effort
// scan; the agent will see a dangling link, same as before this fix).
//
// If claudeDir doesn't exist, returns (nil, nil).
func CollectExternalSymlinkTargets(claudeDir string) ([]string, error) {
	if claudeDir == "" {
		return nil, nil
	}
	rootInfo, err := os.Stat(claudeDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !rootInfo.IsDir() {
		return nil, nil
	}
	rootResolved, err := filepath.EvalSymlinks(claudeDir)
	if err != nil {
		rootResolved = claudeDir
	}

	seen := map[string]bool{}
	var targets []string

	walkErr := filepath.WalkDir(claudeDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type()&os.ModeSymlink == 0 {
			return nil
		}
		resolved, rerr := filepath.EvalSymlinks(path)
		if rerr != nil {
			return nil
		}
		if isInside(resolved, rootResolved) {
			return nil
		}
		if !seen[resolved] {
			seen[resolved] = true
			targets = append(targets, resolved)
		}
		return nil
	})
	return targets, walkErr
}

// isInside reports whether child is the same path as parent or nested
// underneath it. Both are expected to be cleaned, absolute paths (callers
// pass the output of EvalSymlinks).
func isInside(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return !strings.HasPrefix(rel, "..")
}
