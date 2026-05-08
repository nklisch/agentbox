package lifecycle_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nklisch/agentbox/internal/lifecycle"
)

func TestCollectExternalSymlinkTargets_MissingDir(t *testing.T) {
	got, err := lifecycle.CollectExternalSymlinkTargets(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("missing dir should not error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("missing dir should return empty, got %v", got)
	}
}

func TestCollectExternalSymlinkTargets_EmptyArg(t *testing.T) {
	got, err := lifecycle.CollectExternalSymlinkTargets("")
	if err != nil {
		t.Fatalf("empty arg should not error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("empty arg should return empty, got %v", got)
	}
}

// The headline behaviour: a symlink under ~/.claude pointing to an
// external path is reported as a target the caller should bind-mount.
func TestCollectExternalSymlinkTargets_ReportsExternalSymlinks(t *testing.T) {
	homeDir := t.TempDir()
	claudeDir := filepath.Join(homeDir, ".claude")
	if err := os.MkdirAll(filepath.Join(claudeDir, "skills"), 0o700); err != nil {
		t.Fatal(err)
	}
	externalRoot := t.TempDir()
	external := filepath.Join(externalRoot, "skilltap")
	if err := os.MkdirAll(external, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(claudeDir, "skills", "skilltap")); err != nil {
		t.Fatal(err)
	}

	got, err := lifecycle.CollectExternalSymlinkTargets(claudeDir)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	wantResolved, _ := filepath.EvalSymlinks(external)
	if wantResolved == "" {
		wantResolved = external
	}
	if len(got) != 1 || got[0] != wantResolved {
		t.Errorf("got %v, want [%s]", got, wantResolved)
	}
}

// Symlinks pointing INSIDE ~/.claude don't need additional mounts — they
// resolve via the parent bind-mount. Skip them.
func TestCollectExternalSymlinkTargets_IgnoresInternalSymlinks(t *testing.T) {
	homeDir := t.TempDir()
	claudeDir := filepath.Join(homeDir, ".claude")
	if err := os.MkdirAll(filepath.Join(claudeDir, "real"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(claudeDir, "skills"), 0o700); err != nil {
		t.Fatal(err)
	}
	// ~/.claude/skills/local → ~/.claude/real (internal — already covered)
	if err := os.Symlink(filepath.Join(claudeDir, "real"), filepath.Join(claudeDir, "skills", "local")); err != nil {
		t.Fatal(err)
	}

	got, err := lifecycle.CollectExternalSymlinkTargets(claudeDir)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("internal symlinks should be ignored, got %v", got)
	}
}

// Multiple symlinks to the same external target are reported once.
func TestCollectExternalSymlinkTargets_DedupesSameTarget(t *testing.T) {
	homeDir := t.TempDir()
	claudeDir := filepath.Join(homeDir, ".claude")
	if err := os.MkdirAll(filepath.Join(claudeDir, "skills"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(claudeDir, "plugins"), 0o700); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "shared")
	if err := os.MkdirAll(external, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(claudeDir, "skills", "a")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(claudeDir, "plugins", "b")); err != nil {
		t.Fatal(err)
	}

	got, err := lifecycle.CollectExternalSymlinkTargets(claudeDir)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("expected 1 deduped target, got %v", got)
	}
}

// Dangling symlinks are silently skipped — the agent will see a dangling
// link inside the box, same as before this fix. Best-effort scan.
func TestCollectExternalSymlinkTargets_SkipsDangling(t *testing.T) {
	homeDir := t.TempDir()
	claudeDir := filepath.Join(homeDir, ".claude")
	if err := os.MkdirAll(filepath.Join(claudeDir, "skills"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/nonexistent/path", filepath.Join(claudeDir, "skills", "broken")); err != nil {
		t.Fatal(err)
	}

	got, err := lifecycle.CollectExternalSymlinkTargets(claudeDir)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("dangling symlinks should be skipped, got %v", got)
	}
}

// Regular files and directories don't pollute the list.
func TestCollectExternalSymlinkTargets_IgnoresRegularEntries(t *testing.T) {
	homeDir := t.TempDir()
	claudeDir := filepath.Join(homeDir, ".claude")
	if err := os.MkdirAll(filepath.Join(claudeDir, "cache"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := lifecycle.CollectExternalSymlinkTargets(claudeDir)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no targets for symlink-free tree, got %v", got)
	}
}
