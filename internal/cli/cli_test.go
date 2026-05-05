package cli_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/nklisch/agentbox/internal/cli"
	"github.com/nklisch/agentbox/internal/exitcode"
)

// runCmd executes the root command with the given args and returns captured
// stdout, stderr, and the error returned by cobra's Execute.
func runCmd(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := cli.NewRootCmd()
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

func TestVersion(t *testing.T) {
	out, _, err := runCmd(t, "--version")
	if err != nil {
		t.Fatalf("--version returned error: %v", err)
	}
	if out == "" {
		t.Fatal("--version produced no output")
	}
	if !strings.Contains(out, "agentbox") {
		t.Errorf("version output %q does not contain 'agentbox'", out)
	}
}

func TestHelp(t *testing.T) {
	out, _, err := runCmd(t, "--help")
	if err != nil {
		t.Fatalf("--help returned error: %v", err)
	}
	if !strings.Contains(out, "Usage:") {
		t.Errorf("help output %q does not contain 'Usage:'", out)
	}
}

func TestConfigPath_Global(t *testing.T) {
	// Isolate XDG so the path is deterministic.
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	out, _, err := runCmd(t, "config", "path")
	if err != nil {
		t.Fatalf("config path returned error: %v", err)
	}
	out = strings.TrimSpace(out)
	if !strings.Contains(out, "agentbox") || !strings.Contains(out, "config.toml") {
		t.Errorf("config path output %q does not match expected pattern", out)
	}
}

func TestConfigPath_Project(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	// Change working directory so the project path is rooted in tmp.
	orig, _ := os.Getwd()
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	out, _, err := runCmd(t, "config", "path", "--project")
	if err != nil {
		t.Fatalf("config path --project returned error: %v", err)
	}
	out = strings.TrimSpace(out)
	if !strings.HasSuffix(out, ".agentbox.toml") {
		t.Errorf("config path --project output %q should end in .agentbox.toml", out)
	}
}

func TestConfigShow_JSON(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("XDG_DATA_HOME", tmp)

	out, _, err := runCmd(t, "config", "show", "--json")
	if err != nil {
		t.Fatalf("config show --json returned error: %v", err)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("config show --json output is not valid JSON: %v\noutput: %s", err, out)
	}
	if runtime, ok := v["runtime"].(string); !ok || runtime != "podman" {
		t.Errorf("expected runtime=podman in JSON output, got: %v", v["runtime"])
	}
}

func TestConfigShow_TOML(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("XDG_DATA_HOME", tmp)

	out, _, err := runCmd(t, "config", "show")
	if err != nil {
		t.Fatalf("config show returned error: %v", err)
	}
	if !strings.Contains(out, "runtime") {
		t.Errorf("config show (TOML) output %q does not contain 'runtime'", out)
	}
	if !strings.Contains(out, "podman") {
		t.Errorf("config show (TOML) output %q does not contain 'podman'", out)
	}
}

func TestRunDryRun(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("XDG_DATA_HOME", tmp)
	// Run from a known directory so we can verify the mount.
	orig, _ := os.Getwd()
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	out, _, err := runCmd(t, "run", "--dry-run")
	if err != nil {
		t.Fatalf("run --dry-run returned error: %v", err)
	}
	if !strings.Contains(out, "agentbox-") {
		t.Errorf("dry-run output missing 'agentbox-' container name prefix:\n%s", out)
	}
	if !strings.Contains(out, tmp) {
		t.Errorf("dry-run output missing project abs path %q:\n%s", tmp, out)
	}
	if !strings.Contains(out, "--cap-drop") {
		t.Errorf("dry-run output missing '--cap-drop':\n%s", out)
	}
	if !strings.Contains(out, "ALL") {
		t.Errorf("dry-run output missing 'ALL' (cap-drop ALL):\n%s", out)
	}
	if !strings.Contains(out, "no-new-privileges") {
		t.Errorf("dry-run output missing 'no-new-privileges':\n%s", out)
	}
}

func TestRunUnknownAgent(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("XDG_DATA_HOME", tmp)
	orig, _ := os.Getwd()
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	_, _, err := runCmd(t, "run", "nonexistent", "--dry-run")
	if err == nil {
		t.Fatal("expected error for unknown agent, got nil")
	}
	var ee *exitcode.Err
	if !errors.As(err, &ee) {
		t.Fatalf("expected *exitcode.Err, got %T: %v", err, err)
	}
	if ee.Code != exitcode.InvalidArgs {
		t.Errorf("expected exit code %d (InvalidArgs), got %d", exitcode.InvalidArgs, ee.Code)
	}
}

func TestStubReturnsUnimplemented(t *testing.T) {
	_, _, err := runCmd(t, "shell")
	if err == nil {
		t.Fatal("expected error for stub command, got nil")
	}
	var ee *exitcode.Err
	if !errors.As(err, &ee) {
		t.Fatalf("expected *exitcode.Err, got %T: %v", err, err)
	}
	if ee.Code != exitcode.Generic {
		t.Errorf("expected exit code %d (Generic/1), got %d", exitcode.Generic, ee.Code)
	}
	if !strings.Contains(ee.Error(), "not yet implemented") {
		t.Errorf("error message %q should contain 'not yet implemented'", ee.Error())
	}
}

func TestCompletionZsh(t *testing.T) {
	out, _, err := runCmd(t, "completion", "zsh")
	if err != nil {
		t.Fatalf("completion zsh returned error: %v", err)
	}
	if !strings.Contains(out, "_agentbox") {
		t.Errorf("zsh completion output does not contain '_agentbox':\n%s", out[:min(len(out), 200)])
	}
}

func TestUnknownFlag(t *testing.T) {
	_, _, err := runCmd(t, "--bogus")
	if err == nil {
		t.Fatal("expected error for unknown flag, got nil")
	}
	// cobra returns an unwrapped error for unknown flags; main.go maps to exit 2.
	// Here we just verify error is non-nil (the exit code mapping is in main.go).
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
