package state_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nklisch/agentbox/internal/state"
)

func TestDir_XDGOverride(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)

	got, err := state.Dir()
	if err != nil {
		t.Fatalf("Dir() error: %v", err)
	}
	want := filepath.Join(tmp, "agentbox")
	if got != want {
		t.Errorf("Dir() = %q, want %q", got, want)
	}
}

func TestDir_FallbackToHome(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")

	got, err := state.Dir()
	if err != nil {
		t.Fatalf("Dir() error: %v", err)
	}
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".local", "share", "agentbox")
	if got != want {
		t.Errorf("Dir() = %q, want %q", got, want)
	}
}

func TestSessionDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)

	got, err := state.SessionDir("abc123")
	if err != nil {
		t.Fatalf("SessionDir() error: %v", err)
	}
	want := filepath.Join(tmp, "agentbox", "sessions", "abc123")
	if got != want {
		t.Errorf("SessionDir() = %q, want %q", got, want)
	}
}

func TestEnsureDir_Idempotent(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "a", "b", "c")

	if err := state.EnsureDir(dir); err != nil {
		t.Fatalf("first EnsureDir: %v", err)
	}
	if err := state.EnsureDir(dir); err != nil {
		t.Fatalf("second EnsureDir (idempotent): %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Stat after EnsureDir: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory to exist")
	}
}

func TestIsWritable_Directory(t *testing.T) {
	tmp := t.TempDir()
	if !state.IsWritable(tmp) {
		t.Errorf("expected %q to be writable", tmp)
	}
}

func TestIsWritable_NonDir(t *testing.T) {
	// /proc/self/cmdline is a file, not a directory — CreateTemp should fail.
	if state.IsWritable("/proc/self/cmdline") {
		t.Error("expected /proc/self/cmdline to not be writable (not a directory)")
	}
}

func TestIsWritable_Nonexistent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist")
	if state.IsWritable(path) {
		t.Error("expected non-existent path to not be writable")
	}
}

func TestDir_ContainsAgentbox(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)

	got, err := state.Dir()
	if err != nil {
		t.Fatalf("Dir() error: %v", err)
	}
	if !strings.HasSuffix(got, "agentbox") {
		t.Errorf("Dir() = %q, expected to end with 'agentbox'", got)
	}
}
