package cli_test

import (
	"strings"
	"testing"
)

func TestInfo_NonQuietWritesToStderr(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("XDG_DATA_HOME", tmp)

	rt := newFakeRuntime()
	restore := setupFakeLifecycle(t, rt)
	defer restore()

	// ls with no boxes: info() writes "no boxes" to stderr (Unit 7, but we
	// test the output helper here too by checking the non-quiet path).
	_, stderr, err := runCmd(t, "ls")
	if err != nil {
		t.Fatalf("ls returned error: %v", err)
	}
	if !strings.Contains(stderr, "no boxes") {
		t.Errorf("expected 'no boxes' on stderr when not quiet, got: %q", stderr)
	}
}

func TestInfo_QuietSuppresses(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("XDG_DATA_HOME", tmp)

	rt := newFakeRuntime()
	restore := setupFakeLifecycle(t, rt)
	defer restore()

	// With --quiet, info() writes should be suppressed.
	_, stderr, err := runCmd(t, "ls", "-q")
	if err != nil {
		t.Fatalf("ls -q returned error: %v", err)
	}
	if strings.Contains(stderr, "no boxes") {
		t.Errorf("expected 'no boxes' suppressed with --quiet, got stderr: %q", stderr)
	}
}

func TestResult_AlwaysWrites(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("XDG_DATA_HOME", tmp)

	// config show uses direct writes to OutOrStdout — same principle as result().
	// Verify --quiet does NOT suppress the primary output.
	out, _, err := runCmd(t, "config", "show", "-q")
	if err != nil {
		t.Fatalf("config show -q returned error: %v", err)
	}
	if !strings.Contains(out, "runtime") {
		t.Errorf("config show -q should still output config (result, not chatter), got: %q", out)
	}
}
