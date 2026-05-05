package cli_test

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nklisch/agentbox/internal/cli"
	"github.com/nklisch/agentbox/internal/config"
	"github.com/nklisch/agentbox/internal/container"
	"github.com/nklisch/agentbox/internal/exitcode"
	"github.com/nklisch/agentbox/internal/lifecycle"
	"github.com/nklisch/agentbox/internal/runspec"
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

// ---- Phase 3 integration tests using fakeLifecycle seam ----

// fakeRuntime is the same test double used here for CLI-level tests.
type fakeRuntime struct {
	boxes    map[string]container.Box
	calls    []string
	execStub func(name string, opts container.ExecOpts) (int, error)
}

func newFakeRuntime() *fakeRuntime {
	return &fakeRuntime{boxes: make(map[string]container.Box)}
}

func (r *fakeRuntime) Create(args runspec.PodmanCreateArgs) error {
	r.calls = append(r.calls, "Create")
	id := strings.TrimPrefix(args.Name, "agentbox-")
	r.boxes[args.Name] = container.Box{ProjectID: id, Status: container.StatusStopped}
	return nil
}
func (r *fakeRuntime) Start(name string) error {
	r.calls = append(r.calls, "Start")
	if b, ok := r.boxes[name]; ok {
		b.Status = container.StatusRunning
		r.boxes[name] = b
	}
	return nil
}
func (r *fakeRuntime) Stop(name string) error { return nil }
func (r *fakeRuntime) Inspect(name string) (container.Box, error) {
	b, ok := r.boxes[name]
	if !ok {
		return container.Box{Status: container.StatusMissing}, nil
	}
	return b, nil
}
func (r *fakeRuntime) Exec(name string, opts container.ExecOpts) (int, error) {
	r.calls = append(r.calls, "Exec")
	if r.execStub != nil {
		return r.execStub(name, opts)
	}
	return 0, nil
}
func (r *fakeRuntime) Ls(all bool) ([]container.Box, error) {
	var out []container.Box
	for _, b := range r.boxes {
		if !all && b.Status != container.StatusRunning {
			continue
		}
		out = append(out, b)
	}
	return out, nil
}
func (r *fakeRuntime) Rm(name string, force bool) error {
	delete(r.boxes, name)
	return nil
}
func (r *fakeRuntime) NetworkCreate(name, subnet string) error { return nil }
func (r *fakeRuntime) NetworkRm(name string) error             { return nil }
func (r *fakeRuntime) NetworkExists(name string) (bool, error) { return false, nil }

// projectID computes the 12-char project ID for a path (matches project.IDFromPath).
func projectID(path string) string {
	h := sha1.Sum([]byte(path))
	return hex.EncodeToString(h[:])[:12]
}

// setupFakeLifecycle installs a lifecycle factory that uses a fakeRuntime +
// pre-populated boxes. Returns the fake runtime and a restore function.
func setupFakeLifecycle(t *testing.T, rt *fakeRuntime) func() {
	t.Helper()
	restore := cli.SetLifecycleFactory(func(cfg config.Config) (*lifecycle.Lifecycle, error) {
		return &lifecycle.Lifecycle{
			Cfg:     cfg,
			Runtime: rt,
			// Builder is nil — tests don't exercise kit build paths.
			Stdout: os.Stdout,
			Stderr: os.Stderr,
		}, nil
	})
	return restore
}

func TestRun_NoAttach_FakeRuntime(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("XDG_DATA_HOME", tmp)
	orig, _ := os.Getwd()
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	// Pre-populate the box as already running so EnsureBox returns immediately
	// without needing a real Builder.
	id := projectID(tmp)
	rt := newFakeRuntime()
	rt.boxes["agentbox-"+id] = container.Box{
		ProjectID: id,
		CWD:       tmp,
		Status:    container.StatusRunning,
	}

	restore := setupFakeLifecycle(t, rt)
	defer restore()

	out, _, err := runCmd(t, "run", "--no-attach")
	if err != nil {
		t.Fatalf("run --no-attach: %v", err)
	}
	if !strings.Contains(out, "(running)") {
		t.Errorf("expected '(running)' in output, got: %q", out)
	}
}

func TestLs_JSON_NDJSON(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("XDG_DATA_HOME", tmp)

	rt := newFakeRuntime()
	rt.boxes["agentbox-abc123456789"] = container.Box{
		ProjectID: "abc123456789",
		Project:   "testproj",
		Agent:     "claude",
		Kits:      []string{"base"},
		Status:    container.StatusRunning,
		Created:   time.Now(),
	}

	restore := setupFakeLifecycle(t, rt)
	defer restore()

	out, _, err := runCmd(t, "ls", "--json", "--all")
	if err != nil {
		t.Fatalf("ls --json: %v", err)
	}
	// NDJSON: one JSON object per line.
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 NDJSON line, got %d: %q", len(lines), out)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &v); err != nil {
		t.Fatalf("NDJSON line is not valid JSON: %v\n%s", err, lines[0])
	}
	if v["project_id"] != "abc123456789" {
		t.Errorf("project_id = %v, want abc123456789", v["project_id"])
	}
}

func TestLs_HumanTable_HasHeader(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("XDG_DATA_HOME", tmp)

	rt := newFakeRuntime()
	rt.boxes["agentbox-abc123456789"] = container.Box{
		ProjectID: "abc123456789",
		Project:   "testproj",
		Agent:     "claude",
		Kits:      []string{"base"},
		Status:    container.StatusRunning,
		Created:   time.Now(),
	}

	restore := setupFakeLifecycle(t, rt)
	defer restore()

	out, _, err := runCmd(t, "ls", "--all")
	if err != nil {
		t.Fatalf("ls: %v", err)
	}
	if !strings.Contains(out, "PROJECT_ID") {
		t.Errorf("ls table missing PROJECT_ID header:\n%s", out)
	}
	if !strings.Contains(out, "abc123456789") {
		t.Errorf("ls table missing project_id abc123456789:\n%s", out)
	}
}

func TestRm_NoArgsExits2(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("XDG_DATA_HOME", tmp)

	rt := newFakeRuntime()
	restore := setupFakeLifecycle(t, rt)
	defer restore()

	_, _, err := runCmd(t, "rm")
	if err == nil {
		t.Fatal("expected error for rm with no args")
	}
	var ee *exitcode.Err
	if !errors.As(err, &ee) {
		t.Fatalf("expected *exitcode.Err, got %T", err)
	}
	if ee.Code != exitcode.InvalidArgs {
		t.Errorf("expected InvalidArgs (%d), got %d", exitcode.InvalidArgs, ee.Code)
	}
}

func TestRm_AllRequiresForce(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("XDG_DATA_HOME", tmp)

	rt := newFakeRuntime()
	rt.boxes["agentbox-aaa000000000"] = container.Box{
		ProjectID: "aaa000000000",
		Status:    container.StatusRunning,
	}
	restore := setupFakeLifecycle(t, rt)
	defer restore()

	_, _, err := runCmd(t, "rm", "--all")
	if err == nil {
		t.Fatal("expected error for rm --all without --force")
	}
	var ee *exitcode.Err
	if !errors.As(err, &ee) {
		t.Fatalf("expected *exitcode.Err, got %T", err)
	}
	if ee.Code != exitcode.InvalidArgs {
		t.Errorf("expected InvalidArgs (%d), got %d", exitcode.InvalidArgs, ee.Code)
	}
}

func TestAttach_NotRunningExits4(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("XDG_DATA_HOME", tmp)

	rt := newFakeRuntime()
	rt.boxes["agentbox-abc123456789"] = container.Box{
		ProjectID: "abc123456789",
		Status:    container.StatusStopped,
	}
	restore := setupFakeLifecycle(t, rt)
	defer restore()

	_, _, err := runCmd(t, "attach", "abc123456789")
	if err == nil {
		t.Fatal("expected error for attach on stopped box")
	}
	var ee *exitcode.Err
	if !errors.As(err, &ee) {
		t.Fatalf("expected *exitcode.Err, got %T", err)
	}
	if ee.Code != exitcode.NotFound {
		t.Errorf("expected NotFound (%d), got %d", exitcode.NotFound, ee.Code)
	}
}

func TestExec_RoutesArgsToLifecycle(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("XDG_DATA_HOME", tmp)

	rt := newFakeRuntime()
	rt.boxes["agentbox-abc123456789"] = container.Box{
		ProjectID: "abc123456789",
		CWD:       tmp,
		Status:    container.StatusRunning,
	}

	var gotArgv []string
	rt.execStub = func(name string, opts container.ExecOpts) (int, error) {
		gotArgv = opts.Argv
		return 0, nil
	}
	restore := setupFakeLifecycle(t, rt)
	defer restore()

	_, _, err := runCmd(t, "exec", "abc123456789", "echo", "hello")
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if len(gotArgv) != 2 || gotArgv[0] != "echo" || gotArgv[1] != "hello" {
		t.Errorf("expected exec argv [echo hello], got %v", gotArgv)
	}
}

// TestRunDryRun_ContainsEnvVars verifies that the new AGENTBOX_* env vars
// appear in dry-run output (backward-compatible extension of Phase 1 test).
func TestRunDryRun_ContainsEnvVars(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("XDG_DATA_HOME", tmp)
	orig, _ := os.Getwd()
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	out, _, err := runCmd(t, "run", "--dry-run")
	if err != nil {
		t.Fatalf("run --dry-run: %v", err)
	}
	if !strings.Contains(out, "AGENTBOX_PROJECT_ID=") {
		t.Errorf("dry-run output missing AGENTBOX_PROJECT_ID=:\n%s", out)
	}
}

// ── Phase 4: zellij routing tests ────────────────────────────────────────────

// fakeTerminal overrides the lifecycle.StdinIsTerminal seam so tests can
// simulate an interactive terminal without requiring a real TTY.
func withFakeTerminal(t *testing.T, isTTY bool) func() {
	t.Helper()
	return lifecycle.SetStdinIsTerminal(func() bool { return isTTY })
}

func TestShell_NoZellij_FallsBackToShellExec(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("XDG_DATA_HOME", tmp)
	orig, _ := os.Getwd()
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	id := projectID(tmp)
	rt := newFakeRuntime()
	rt.boxes["agentbox-"+id] = container.Box{
		ProjectID: id,
		CWD:       tmp,
		Status:    container.StatusRunning,
	}

	var gotArgv []string
	rt.execStub = func(name string, opts container.ExecOpts) (int, error) {
		gotArgv = opts.Argv
		return 0, nil
	}
	restore := setupFakeLifecycle(t, rt)
	defer restore()

	// Simulate interactive TTY so --no-zellij actually execs (rather than
	// falling back to liveness print).
	restoreTTY := withFakeTerminal(t, true)
	defer restoreTTY()

	_, _, err := runCmd(t, "shell", "--no-zellij")
	if err != nil {
		t.Fatalf("shell --no-zellij: %v", err)
	}
	// Should exec the configured shell directly, not zellij.
	if len(gotArgv) == 0 {
		t.Fatal("expected an exec call, got none")
	}
	if gotArgv[0] == "zellij" {
		t.Errorf("--no-zellij should not exec zellij; got argv=%v", gotArgv)
	}
}

func TestShell_DefaultPath_InvokesZellij(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("XDG_DATA_HOME", tmp)
	orig, _ := os.Getwd()
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	id := projectID(tmp)
	rt := newFakeRuntime()
	rt.boxes["agentbox-"+id] = container.Box{
		ProjectID: id,
		CWD:       tmp,
		Status:    container.StatusRunning,
	}

	var gotArgv []string
	rt.execStub = func(name string, opts container.ExecOpts) (int, error) {
		gotArgv = opts.Argv
		return 0, nil
	}
	restore := setupFakeLifecycle(t, rt)
	defer restore()

	// Simulate interactive TTY so zellij path is taken.
	restoreTTY := withFakeTerminal(t, true)
	defer restoreTTY()

	_, _, err := runCmd(t, "shell")
	if err != nil {
		t.Fatalf("shell: %v", err)
	}
	if len(gotArgv) == 0 {
		t.Fatal("expected an exec call, got none")
	}
	if gotArgv[0] != "zellij" {
		t.Errorf("expected zellij as first arg; got argv=%v", gotArgv)
	}
	// Should contain --layout and attach -c agentbox.
	shellStr := strings.Join(gotArgv, " ")
	if !strings.Contains(shellStr, "--layout") {
		t.Errorf("expected --layout in argv: %v", gotArgv)
	}
	if !strings.Contains(shellStr, "attach") || !strings.Contains(shellStr, "agentbox") {
		t.Errorf("expected 'attach ... agentbox' in argv: %v", gotArgv)
	}
}

func TestRun_AttachInteractive_InvokesZellij(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("XDG_DATA_HOME", tmp)
	orig, _ := os.Getwd()
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	id := projectID(tmp)
	rt := newFakeRuntime()
	rt.boxes["agentbox-"+id] = container.Box{
		ProjectID: id,
		CWD:       tmp,
		Agent:     "claude",
		Status:    container.StatusRunning,
	}

	var gotArgv []string
	rt.execStub = func(name string, opts container.ExecOpts) (int, error) {
		gotArgv = opts.Argv
		return 0, nil
	}
	restore := setupFakeLifecycle(t, rt)
	defer restore()

	// Simulate interactive TTY.
	restoreTTY := withFakeTerminal(t, true)
	defer restoreTTY()

	_, _, err := runCmd(t, "run")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(gotArgv) == 0 {
		t.Fatal("expected an exec call, got none")
	}
	if gotArgv[0] != "zellij" {
		t.Errorf("expected zellij as first arg for interactive run; got argv=%v", gotArgv)
	}
}

func TestRun_AttachNonTTY_PrintsLiveness(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("XDG_DATA_HOME", tmp)
	orig, _ := os.Getwd()
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	id := projectID(tmp)
	rt := newFakeRuntime()
	rt.boxes["agentbox-"+id] = container.Box{
		ProjectID: id,
		CWD:       tmp,
		Agent:     "claude",
		Status:    container.StatusRunning,
	}
	restore := setupFakeLifecycle(t, rt)
	defer restore()

	// Non-TTY stdin (default in tests).
	restoreTTY := withFakeTerminal(t, false)
	defer restoreTTY()

	out, _, err := runCmd(t, "run")
	if err != nil {
		t.Fatalf("run (non-TTY): %v", err)
	}
	if !strings.Contains(out, "(running)") {
		t.Errorf("expected liveness print with '(running)', got: %q", out)
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
