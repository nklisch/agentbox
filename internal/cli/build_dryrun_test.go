package cli_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/nklisch/agentbox/internal/exitcode"
)

// TestBuild_DryRun_PullEligiblePrintsPullAndTag verifies that when the registry
// is enabled (default config), dry-run prints the pull + tag commands.
func TestBuild_DryRun_PullEligiblePrintsPullAndTag(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("XDG_DATA_HOME", tmp)

	// Default config has registry.enabled=true and registry.host set.
	out, _, err := runCmd(t, "build", "base", "--dry-run")
	if err != nil {
		t.Fatalf("build base --dry-run: %v", err)
	}
	if !strings.Contains(out, "podman pull") {
		t.Errorf("expected 'podman pull' when registry is enabled, got:\n%s", out)
	}
	if !strings.Contains(out, "podman tag") {
		t.Errorf("expected 'podman tag' when registry is enabled, got:\n%s", out)
	}
	// Every non-comment line must be a valid shell command.
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "podman ") {
			t.Errorf("non-comment line is not a shell command: %q", line)
		}
	}
}

// TestBuild_DryRun_NoPullPrintsBuildOnly verifies that --no-pull disables the
// pull path and shows the build command instead.
func TestBuild_DryRun_NoPullPrintsBuildOnly(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("XDG_DATA_HOME", tmp)

	out, _, err := runCmd(t, "build", "base", "--dry-run", "--no-pull")
	if err != nil {
		t.Fatalf("build base --dry-run --no-pull: %v", err)
	}
	if !strings.Contains(out, "podman build") {
		t.Errorf("expected 'podman build' line with --no-pull, got:\n%s", out)
	}
	if strings.Contains(out, "podman pull") {
		t.Errorf("expected no 'podman pull' when --no-pull is set, got:\n%s", out)
	}
}

func TestBuild_DryRun_DoesNotCallRunner(t *testing.T) {
	// The dry-run must not invoke podman build/pull. We verify indirectly:
	// the command succeeds without an actual container runtime being present
	// (same as --print-tag, which also doesn't invoke the runner).
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("XDG_DATA_HOME", tmp)

	out, _, err := runCmd(t, "build", "base", "--dry-run")
	if err != nil {
		t.Fatalf("build base --dry-run: %v", err)
	}
	if out == "" {
		t.Error("expected non-empty dry-run output")
	}
}

func TestBuild_DryRun_ContainsTagAndKits(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("XDG_DATA_HOME", tmp)

	out, _, err := runCmd(t, "build", "base", "--dry-run")
	if err != nil {
		t.Fatalf("build base --dry-run: %v", err)
	}
	if !strings.Contains(out, "# tag = agentbox/") {
		t.Errorf("expected '# tag = agentbox/' in dry-run output, got:\n%s", out)
	}
	if !strings.Contains(out, "# kits = base") {
		t.Errorf("expected '# kits = base' in dry-run output, got:\n%s", out)
	}
}

func TestBuild_DryRun_MutuallyExclusiveWithPrint(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("XDG_DATA_HOME", tmp)

	_, _, err := runCmd(t, "build", "base", "--dry-run", "--print")
	if err == nil {
		t.Fatal("expected error for --dry-run combined with --print")
	}
	var ee *exitcode.Err
	if !errors.As(err, &ee) {
		t.Fatalf("expected *exitcode.Err, got %T: %v", err, err)
	}
	if ee.Code != exitcode.InvalidArgs {
		t.Errorf("expected InvalidArgs (%d), got %d", exitcode.InvalidArgs, ee.Code)
	}
}

func TestBuild_DryRun_MutuallyExclusiveWithPrintTag(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("XDG_DATA_HOME", tmp)

	_, _, err := runCmd(t, "build", "base", "--dry-run", "--print-tag")
	if err == nil {
		t.Fatal("expected error for --dry-run combined with --print-tag")
	}
	var ee *exitcode.Err
	if !errors.As(err, &ee) {
		t.Fatalf("expected *exitcode.Err, got %T: %v", err, err)
	}
	if ee.Code != exitcode.InvalidArgs {
		t.Errorf("expected InvalidArgs (%d), got %d", exitcode.InvalidArgs, ee.Code)
	}
}

func TestBuild_DryRun_MutuallyExclusiveWithList(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("XDG_DATA_HOME", tmp)

	_, _, err := runCmd(t, "build", "--dry-run", "--list")
	if err == nil {
		t.Fatal("expected error for --dry-run combined with --list")
	}
	var ee *exitcode.Err
	if !errors.As(err, &ee) {
		t.Fatalf("expected *exitcode.Err, got %T: %v", err, err)
	}
	if ee.Code != exitcode.InvalidArgs {
		t.Errorf("expected InvalidArgs (%d), got %d", exitcode.InvalidArgs, ee.Code)
	}
}

func TestBuild_DryRun_OutputIsBashSafe(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("XDG_DATA_HOME", tmp)

	out, _, err := runCmd(t, "build", "base", "--dry-run")
	if err != nil {
		t.Fatalf("build base --dry-run: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "podman ") {
			t.Errorf("line is neither a comment nor a podman command (bash-unsafe): %q", line)
		}
	}
}

func TestBuild_DryRun_EmptyKitsExits2(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("XDG_DATA_HOME", tmp)

	// Write a config with empty default_kits so we can trigger the empty-kits path.
	cfgDir := tmp + "/agentbox"
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(cfgDir+"/config.toml", []byte("default_kits = []\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, _, err := runCmd(t, "build", "--dry-run")
	if err == nil {
		t.Fatal("expected error for empty kits with --dry-run")
	}
	var ee *exitcode.Err
	if !errors.As(err, &ee) {
		t.Fatalf("expected *exitcode.Err, got %T: %v", err, err)
	}
	if ee.Code != exitcode.InvalidArgs {
		t.Errorf("expected InvalidArgs (%d), got %d", exitcode.InvalidArgs, ee.Code)
	}
}
