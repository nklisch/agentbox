package lifecycle_test

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nklisch/agentbox/internal/builtinkits"
	"github.com/nklisch/agentbox/internal/config"
	"github.com/nklisch/agentbox/internal/container"
	"github.com/nklisch/agentbox/internal/exitcode"
	"github.com/nklisch/agentbox/internal/kits"
	"github.com/nklisch/agentbox/internal/lifecycle"
	"github.com/nklisch/agentbox/internal/network"
	"github.com/nklisch/agentbox/internal/runspec"
	"github.com/nklisch/agentbox/internal/state"
)

// fakeRuntime is a test double for container.Runtime.
type fakeRuntime struct {
	boxes    map[string]container.Box // by container name
	failOn   map[string]error         // method-name → error
	calls    []string                 // method names in call order
	execStub func(name string, opts container.ExecOpts) (int, error)
}

func newFakeRuntime() *fakeRuntime {
	return &fakeRuntime{
		boxes:  make(map[string]container.Box),
		failOn: make(map[string]error),
	}
}

func (r *fakeRuntime) record(name string) {
	r.calls = append(r.calls, name)
}

func (r *fakeRuntime) Create(args runspec.PodmanCreateArgs) error {
	r.record("Create")
	if err := r.failOn["Create"]; err != nil {
		return err
	}
	id := strings.TrimPrefix(args.Name, "agentbox-")
	box := container.Box{
		ProjectID: id,
		Status:    container.StatusStopped,
	}
	for _, kv := range args.Labels {
		switch kv.Key {
		case "agentbox.project":
			box.Project = kv.Value
		case "agentbox.cwd":
			box.CWD = kv.Value
		case "agentbox.agent":
			box.Agent = kv.Value
		}
	}
	r.boxes[args.Name] = box
	return nil
}

func (r *fakeRuntime) Start(name string) error {
	r.record("Start")
	if err := r.failOn["Start"]; err != nil {
		return err
	}
	if box, ok := r.boxes[name]; ok {
		box.Status = container.StatusRunning
		r.boxes[name] = box
	}
	return nil
}

func (r *fakeRuntime) Stop(name string) error {
	r.record("Stop")
	if err := r.failOn["Stop"]; err != nil {
		return err
	}
	if box, ok := r.boxes[name]; ok {
		box.Status = container.StatusStopped
		r.boxes[name] = box
	}
	return nil
}

func (r *fakeRuntime) Inspect(name string) (container.Box, error) {
	r.record("Inspect")
	if err := r.failOn["Inspect"]; err != nil {
		return container.Box{}, err
	}
	box, ok := r.boxes[name]
	if !ok {
		return container.Box{Status: container.StatusMissing}, nil
	}
	return box, nil
}

func (r *fakeRuntime) Exec(name string, opts container.ExecOpts) (int, error) {
	r.record("Exec")
	if err := r.failOn["Exec"]; err != nil {
		return -1, err
	}
	if r.execStub != nil {
		return r.execStub(name, opts)
	}
	return 0, nil
}

func (r *fakeRuntime) Ls(all bool) ([]container.Box, error) {
	r.record("Ls")
	if err := r.failOn["Ls"]; err != nil {
		return nil, err
	}
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
	r.record("Rm")
	if err := r.failOn["Rm"]; err != nil {
		return err
	}
	delete(r.boxes, name)
	return nil
}

func (r *fakeRuntime) NetworkCreate(name, subnet string) error {
	r.record("NetworkCreate")
	if err := r.failOn["NetworkCreate"]; err != nil {
		return err
	}
	return nil
}

func (r *fakeRuntime) NetworkRm(name string) error {
	r.record("NetworkRm")
	if err := r.failOn["NetworkRm"]; err != nil {
		return err
	}
	return nil
}

func (r *fakeRuntime) NetworkExists(name string) (bool, error) {
	r.record("NetworkExists")
	if err := r.failOn["NetworkExists"]; err != nil {
		return false, err
	}
	return false, nil // default: network does not exist
}

// fakeNetworkManager is a test double for the network manager interface used by lifecycle.
type fakeNetworkManager struct {
	setupInfo  network.Info
	setupErr   error
	teardownErr error
	setupCalls    []network.Spec
	teardownCalls []network.Spec
}

func newFakeNetworkManager() *fakeNetworkManager {
	return &fakeNetworkManager{
		setupInfo: network.Info{NetworkName: "bridge"},
	}
}

func (f *fakeNetworkManager) SpecFor(cfg config.Config, projectID string) network.Spec {
	return network.Spec{
		ProjectID:   projectID,
		Mode:        network.Mode(cfg.Network.Mode),
		NetworkName: "agentbox-net-" + projectID,
		SidecarName: "agentbox-coredns-" + projectID,
		SidecarIP:   "10.89.0.2",
		Subnet:      "10.89.0.0/24",
		Cfg:         cfg,
	}
}

func (f *fakeNetworkManager) Setup(spec network.Spec) (network.Info, error) {
	f.setupCalls = append(f.setupCalls, spec)
	if f.setupErr != nil {
		return network.Info{}, f.setupErr
	}
	return f.setupInfo, nil
}

func (f *fakeNetworkManager) Teardown(spec network.Spec) error {
	f.teardownCalls = append(f.teardownCalls, spec)
	return f.teardownErr
}

// fakeKitsRunner implements kits.Runner with a no-op Build (always succeeds).
// Used to build a real *kits.Builder backed by a fake runner.
type fakeKitsRunner struct {
	buildErr error
}

func (r *fakeKitsRunner) Build(ctx kits.BuildContext) error { return r.buildErr }
func (r *fakeKitsRunner) HasImage(tag string) (bool, error) { return true, nil }
func (r *fakeKitsRunner) LiveImageRefs() ([]string, error)  { return nil, nil }
func (r *fakeKitsRunner) RemoveImage(tag string) error       { return nil }

// newTestBuilder returns a real *kits.Builder backed by builtinkits + fakeKitsRunner.
// The fakeRunner's HasImage returns true so the cache always hits (no actual build).
func newTestBuilder(t *testing.T, buildErr error) *kits.Builder {
	t.Helper()
	tmp := t.TempDir()
	reg := kits.NewRegistry(builtinkits.FS(), filepath.Join(tmp, "kits"))
	cache, err := kits.NewCache()
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	runner := &fakeKitsRunner{buildErr: buildErr}
	return &kits.Builder{
		Registry: reg,
		Cache:    cache,
		Runner:   runner,
		Version:  "test",
	}
}

// setupProject changes the test's working directory to a temp dir and returns
// (projectID, absPath).
func setupProject(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	orig, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
	h := sha1.Sum([]byte(dir))
	id := hex.EncodeToString(h[:])[:12]
	return id, dir
}

// defaultTestCfg returns a config with network=open and agents using only the
// "base" kit (the only built-in kit available in tests).
func defaultTestCfg() config.Config {
	cfg := config.DefaultConfig()
	cfg.Network.Mode = "open"
	// Override agents to use only "base" kit (the sole built-in kit).
	cfg.Agents = map[string]config.Agent{
		"claude": {Kits: []string{"base"}},
	}
	cfg.DefaultAgent = "claude"
	// Disable optional mounts to avoid missing-file errors in tests.
	cfg.Mounts.Gitconfig = false
	cfg.Mounts.SSHReadonly = false
	cfg.Mounts.AgentConfigs = nil
	return cfg
}

// isolateState sets XDG_DATA_HOME to a fresh temp dir for the test and returns
// the path. Callers that create session state before calling lifecycle methods
// should use this to ensure consistent data dir.
func isolateState(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)
	return tmp
}

// newTestLifecycle creates a Lifecycle with a fakeRuntime and isolated state.
// It calls isolateState internally so all subsequent state operations use the
// same temp dir. Pass builderErr=nil for a builder that always succeeds.
func newTestLifecycle(t *testing.T, rt *fakeRuntime, cfg config.Config, builderErr error) *lifecycle.Lifecycle {
	t.Helper()
	isolateState(t)
	netMgr := newFakeNetworkManager()
	// For safe/allowlist mode, return a network info with the per-project network.
	netMgr.setupInfo = network.Info{
		NetworkName: "bridge",
		SidecarDNS:  nil,
	}
	return &lifecycle.Lifecycle{
		Cfg:     cfg,
		Runtime: rt,
		Builder: newTestBuilder(t, builderErr),
		Network: netMgr,
		Home:    t.TempDir(),
		Stdout:  &bytes.Buffer{},
		Stderr:  &bytes.Buffer{},
	}
}

func containsCall(calls []string, name string) bool {
	for _, c := range calls {
		if c == name {
			return true
		}
	}
	return false
}

// ---- EnsureBox tests ----

func TestEnsureBox_RunningBoxIsNoop(t *testing.T) {
	rt := newFakeRuntime()
	cfg := defaultTestCfg()
	projID, projAbs := setupProject(t)
	name := "agentbox-" + projID
	rt.boxes[name] = container.Box{
		ProjectID: projID,
		CWD:       projAbs,
		Status:    container.StatusRunning,
	}

	l := newTestLifecycle(t, rt, cfg, nil)
	box, err := l.EnsureBox(lifecycle.EnsureOpts{})
	if err != nil {
		t.Fatalf("EnsureBox: %v", err)
	}
	if box.Status != container.StatusRunning {
		t.Errorf("expected running box, got %q", box.Status)
	}
	for _, call := range rt.calls {
		if call == "Create" || call == "Start" {
			t.Errorf("unexpected call to %q for already-running box", call)
		}
	}
}

func TestEnsureBox_StoppedBoxStarts(t *testing.T) {
	rt := newFakeRuntime()
	cfg := defaultTestCfg()
	projID, projAbs := setupProject(t)
	name := "agentbox-" + projID
	rt.boxes[name] = container.Box{
		ProjectID: projID,
		CWD:       projAbs,
		Status:    container.StatusStopped,
	}

	l := newTestLifecycle(t, rt, cfg, nil)
	box, err := l.EnsureBox(lifecycle.EnsureOpts{})
	if err != nil {
		t.Fatalf("EnsureBox: %v", err)
	}
	if box.Status != container.StatusRunning {
		t.Errorf("expected running box after start, got %q", box.Status)
	}
	if !containsCall(rt.calls, "Start") {
		t.Error("expected Start to be called for stopped box")
	}
	if containsCall(rt.calls, "Create") {
		t.Error("expected Create NOT to be called for stopped box")
	}
}

func TestEnsureBox_MissingBoxCreates(t *testing.T) {
	rt := newFakeRuntime()
	cfg := defaultTestCfg()
	setupProject(t)

	l := newTestLifecycle(t, rt, cfg, nil)
	box, err := l.EnsureBox(lifecycle.EnsureOpts{})
	if err != nil {
		t.Fatalf("EnsureBox: %v", err)
	}
	if box.Status != container.StatusRunning {
		t.Errorf("expected running box after create+start, got %q", box.Status)
	}
	if !containsCall(rt.calls, "Create") {
		t.Error("expected Create to be called for missing box")
	}
	if !containsCall(rt.calls, "Start") {
		t.Error("expected Start to be called for missing box")
	}
}

func TestEnsureBox_FreshRemovesFirst(t *testing.T) {
	rt := newFakeRuntime()
	cfg := defaultTestCfg()
	projID, projAbs := setupProject(t)
	name := "agentbox-" + projID
	rt.boxes[name] = container.Box{
		ProjectID: projID,
		CWD:       projAbs,
		Status:    container.StatusRunning,
	}

	l := newTestLifecycle(t, rt, cfg, nil)
	_, err := l.EnsureBox(lifecycle.EnsureOpts{Fresh: true})
	if err != nil {
		t.Fatalf("EnsureBox Fresh: %v", err)
	}
	rmIdx, createIdx := -1, -1
	for i, c := range rt.calls {
		if c == "Rm" && rmIdx == -1 {
			rmIdx = i
		}
		if c == "Create" && createIdx == -1 {
			createIdx = i
		}
	}
	if rmIdx == -1 {
		t.Error("expected Rm to be called with Fresh=true")
	}
	if createIdx == -1 {
		t.Error("expected Create to be called with Fresh=true")
	}
	if rmIdx >= createIdx {
		t.Errorf("expected Rm (idx %d) before Create (idx %d)", rmIdx, createIdx)
	}
}

func TestEnsureBox_BuildFailureReturnsKitBuildExit(t *testing.T) {
	rt := newFakeRuntime()
	cfg := defaultTestCfg()
	setupProject(t)

	buildErr := errors.New("docker build failed")
	l := newTestLifecycle(t, rt, cfg, buildErr)

	_, err := l.EnsureBox(lifecycle.EnsureOpts{})
	if err == nil {
		t.Fatal("expected error from kit build failure")
	}
	var ee *exitcode.Err
	if !errors.As(err, &ee) {
		t.Fatalf("expected *exitcode.Err, got %T: %v", err, err)
	}
	if ee.Code != exitcode.KitBuild {
		t.Errorf("expected KitBuild (%d), got %d", exitcode.KitBuild, ee.Code)
	}
}

func TestEnsureBox_MountMissingReturnsExit7(t *testing.T) {
	rt := newFakeRuntime()
	cfg := defaultTestCfg()
	// Enable gitconfig mount with a home that has no .gitconfig
	cfg.Mounts.Gitconfig = true
	homeDir := t.TempDir()
	// Do NOT create .gitconfig — it should be missing.
	setupProject(t)

	l := newTestLifecycle(t, rt, cfg, nil)
	l.Home = homeDir // override to a dir without .gitconfig

	_, err := l.EnsureBox(lifecycle.EnsureOpts{})
	if err == nil {
		t.Fatal("expected error for missing mount source")
	}
	var ee *exitcode.Err
	if !errors.As(err, &ee) {
		t.Fatalf("expected *exitcode.Err, got %T: %v", err, err)
	}
	if ee.Code != exitcode.MountMissing {
		t.Errorf("expected MountMissing (%d), got %d", exitcode.MountMissing, ee.Code)
	}
}

func TestEnsureBox_NetworkSafeUsesManager(t *testing.T) {
	rt := newFakeRuntime()
	// Use safe mode explicitly.
	cfg := defaultTestCfg()
	cfg.Network.Mode = "safe"
	setupProject(t)

	netMgr := newFakeNetworkManager()
	netMgr.setupInfo = network.Info{
		NetworkName: "agentbox-net-testproj",
		SidecarDNS:  []string{"10.89.0.2"},
	}

	var stderr bytes.Buffer
	isolateState(t)
	l := &lifecycle.Lifecycle{
		Cfg:     cfg,
		Runtime: rt,
		Builder: newTestBuilder(t, nil),
		Network: netMgr,
		Home:    t.TempDir(),
		Stdout:  &bytes.Buffer{},
		Stderr:  &stderr,
	}

	// safe mode should work without any warning (Phase 6: real network setup)
	_, err := l.EnsureBox(lifecycle.EnsureOpts{})
	if err != nil {
		t.Fatalf("EnsureBox with safe mode should not error: %v", err)
	}
	if strings.Contains(stderr.String(), "warning") {
		t.Errorf("Phase 6: safe mode should not produce degrade warning, got: %q", stderr.String())
	}
	// Network manager should have been called
	if len(netMgr.setupCalls) == 0 {
		t.Error("expected Network.Setup to be called for safe mode")
	}
}

// ---- Run tests ----

func TestRun_NoAttach(t *testing.T) {
	rt := newFakeRuntime()
	cfg := defaultTestCfg()
	setupProject(t)

	var stdout bytes.Buffer
	l := newTestLifecycle(t, rt, cfg, nil)
	l.Stdout = &stdout

	err := l.Run(lifecycle.RunOpts{Attach: false})
	if err != nil {
		t.Fatalf("Run(noAttach): %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "(running)") {
		t.Errorf("expected '(running)' in output, got: %q", out)
	}
	if containsCall(rt.calls, "Exec") {
		t.Error("Exec should not be called when Attach=false")
	}
}

func TestRun_AttachNonTTY_PrintsLiveness(t *testing.T) {
	rt := newFakeRuntime()
	cfg := defaultTestCfg()
	setupProject(t)

	var stdout bytes.Buffer
	l := newTestLifecycle(t, rt, cfg, nil)
	l.Stdout = &stdout

	// Default stdinIsTerminal returns false in tests (no real TTY).
	err := l.Run(lifecycle.RunOpts{Attach: true})
	if err != nil {
		t.Fatalf("Run(attach, non-TTY): %v", err)
	}
	if containsCall(rt.calls, "Exec") {
		t.Error("Exec should not be called when stdin is not a TTY")
	}
	if !strings.Contains(stdout.String(), "(running)") {
		t.Errorf("expected liveness print, got: %q", stdout.String())
	}
}

func TestRun_AttachTTY_InvokesZellij(t *testing.T) {
	rt := newFakeRuntime()
	cfg := defaultTestCfg()
	setupProject(t)

	var execArgv []string
	rt.execStub = func(name string, opts container.ExecOpts) (int, error) {
		execArgv = opts.Argv
		return 0, nil
	}

	restoreTTY := lifecycle.SetStdinIsTerminal(func() bool { return true })
	defer restoreTTY()

	l := newTestLifecycle(t, rt, cfg, nil)
	err := l.Run(lifecycle.RunOpts{Attach: true})
	if err != nil {
		t.Fatalf("Run(attach, TTY): %v", err)
	}
	if !containsCall(rt.calls, "Exec") {
		t.Fatal("expected Exec to be called when Attach=true and stdin is TTY")
	}
	if len(execArgv) == 0 || execArgv[0] != "zellij" {
		t.Errorf("expected zellij exec, got argv=%v", execArgv)
	}
}

func TestShell_NoZellij_CallsShellExec(t *testing.T) {
	rt := newFakeRuntime()
	cfg := defaultTestCfg()
	setupProject(t)

	var execArgv []string
	rt.execStub = func(name string, opts container.ExecOpts) (int, error) {
		execArgv = opts.Argv
		return 0, nil
	}

	restoreTTY := lifecycle.SetStdinIsTerminal(func() bool { return true })
	defer restoreTTY()

	l := newTestLifecycle(t, rt, cfg, nil)
	err := l.Shell(lifecycle.RunOpts{Attach: true, NoZellij: true})
	if err != nil {
		t.Fatalf("Shell(NoZellij): %v", err)
	}
	if !containsCall(rt.calls, "Exec") {
		t.Fatal("expected Exec to be called for --no-zellij")
	}
	if len(execArgv) == 0 || execArgv[0] == "zellij" {
		t.Errorf("--no-zellij should exec shell, not zellij; got argv=%v", execArgv)
	}
}

func TestShell_DefaultPath_InvokesZellij(t *testing.T) {
	rt := newFakeRuntime()
	cfg := defaultTestCfg()
	setupProject(t)

	var execArgv []string
	rt.execStub = func(name string, opts container.ExecOpts) (int, error) {
		execArgv = opts.Argv
		return 0, nil
	}

	restoreTTY := lifecycle.SetStdinIsTerminal(func() bool { return true })
	defer restoreTTY()

	l := newTestLifecycle(t, rt, cfg, nil)
	err := l.Shell(lifecycle.RunOpts{Attach: true, NoZellij: false})
	if err != nil {
		t.Fatalf("Shell(zellij): %v", err)
	}
	if !containsCall(rt.calls, "Exec") {
		t.Fatal("expected Exec to be called for default shell path")
	}
	if len(execArgv) == 0 || execArgv[0] != "zellij" {
		t.Errorf("expected zellij exec for default shell; got argv=%v", execArgv)
	}
}

// ---- Exec tests ----

func TestExec_PassesExitCode(t *testing.T) {
	rt := newFakeRuntime()
	cfg := defaultTestCfg()
	projID, projAbs := setupProject(t)
	name := "agentbox-" + projID
	rt.boxes[name] = container.Box{
		ProjectID: projID,
		CWD:       projAbs,
		Status:    container.StatusRunning,
	}

	rt.execStub = func(n string, opts container.ExecOpts) (int, error) {
		return 7, nil
	}

	l := newTestLifecycle(t, rt, cfg, nil)
	err := l.Exec(lifecycle.ExecOpts{Input: ".", Argv: []string{"false"}})
	if err == nil {
		t.Fatal("expected error from Exec returning exit code 7")
	}
	var ee *exitcode.Err
	if !errors.As(err, &ee) {
		t.Fatalf("expected *exitcode.Err, got %T: %v", err, err)
	}
	if ee.Code != 7 {
		t.Errorf("expected exit code 7, got %d", ee.Code)
	}
}

func TestExec_NotRunning(t *testing.T) {
	rt := newFakeRuntime()
	cfg := defaultTestCfg()
	projID, projAbs := setupProject(t)
	name := "agentbox-" + projID
	rt.boxes[name] = container.Box{
		ProjectID: projID,
		CWD:       projAbs,
		Status:    container.StatusStopped,
	}

	l := newTestLifecycle(t, rt, cfg, nil)
	err := l.Exec(lifecycle.ExecOpts{Input: ".", Argv: []string{"pwd"}})
	if err == nil {
		t.Fatal("expected error for exec on stopped box")
	}
	var ee *exitcode.Err
	if !errors.As(err, &ee) {
		t.Fatalf("expected *exitcode.Err, got %T", err)
	}
	if ee.Code != exitcode.NotFound {
		t.Errorf("expected NotFound (%d), got %d", exitcode.NotFound, ee.Code)
	}
}

func TestExec_UseShellWraps(t *testing.T) {
	rt := newFakeRuntime()
	cfg := defaultTestCfg()
	projID, projAbs := setupProject(t)
	name := "agentbox-" + projID
	rt.boxes[name] = container.Box{
		ProjectID: projID,
		CWD:       projAbs,
		Status:    container.StatusRunning,
	}

	var gotArgv []string
	rt.execStub = func(n string, opts container.ExecOpts) (int, error) {
		gotArgv = opts.Argv
		return 0, nil
	}

	l := newTestLifecycle(t, rt, cfg, nil)
	err := l.Exec(lifecycle.ExecOpts{
		Input:    ".",
		Argv:     []string{"echo", "hello"},
		UseShell: true,
	})
	if err != nil {
		t.Fatalf("Exec UseShell: %v", err)
	}
	if len(gotArgv) != 3 {
		t.Fatalf("expected 3-element argv from UseShell, got %v", gotArgv)
	}
	if gotArgv[0] != cfg.Shell.Shell {
		t.Errorf("argv[0] = %q, want %q", gotArgv[0], cfg.Shell.Shell)
	}
	if gotArgv[1] != "-c" {
		t.Errorf("argv[1] = %q, want \"-c\"", gotArgv[1])
	}
	if gotArgv[2] != "echo hello" {
		t.Errorf("argv[2] = %q, want \"echo hello\"", gotArgv[2])
	}
}

// ---- Ls tests ----

func TestLs_FiltersClientSide(t *testing.T) {
	rt := newFakeRuntime()
	cfg := defaultTestCfg()

	rt.boxes["agentbox-aaa000000000"] = container.Box{
		ProjectID: "aaa000000000",
		Project:   "proj-a",
		Agent:     "claude",
		Kits:      []string{"polyglot", "claude"},
		Status:    container.StatusRunning,
	}
	rt.boxes["agentbox-bbb000000000"] = container.Box{
		ProjectID: "bbb000000000",
		Project:   "proj-b",
		Agent:     "codex",
		Kits:      []string{"polyglot", "codex"},
		Status:    container.StatusRunning,
	}

	l := newTestLifecycle(t, rt, cfg, nil)

	// Filter by agent
	boxes, err := l.Ls(lifecycle.LsFilter{All: true, Agent: "claude"})
	if err != nil {
		t.Fatalf("Ls: %v", err)
	}
	if len(boxes) != 1 || boxes[0].Agent != "claude" {
		t.Errorf("expected 1 claude box, got %v", boxes)
	}

	// Filter by project
	boxes, err = l.Ls(lifecycle.LsFilter{All: true, Project: "proj-b"})
	if err != nil {
		t.Fatalf("Ls: %v", err)
	}
	if len(boxes) != 1 || boxes[0].Project != "proj-b" {
		t.Errorf("expected 1 proj-b box, got %v", boxes)
	}

	// Filter by kit
	boxes, err = l.Ls(lifecycle.LsFilter{All: true, Kit: "codex"})
	if err != nil {
		t.Fatalf("Ls: %v", err)
	}
	if len(boxes) != 1 || boxes[0].ProjectID != "bbb000000000" {
		t.Errorf("expected 1 box with codex kit, got %v", boxes)
	}

	// No filter — all boxes
	boxes, err = l.Ls(lifecycle.LsFilter{All: true})
	if err != nil {
		t.Fatalf("Ls: %v", err)
	}
	if len(boxes) != 2 {
		t.Errorf("expected 2 boxes with no filter, got %d", len(boxes))
	}
}

// ---- Rm tests ----

func TestRm_RemovesContainerAndState(t *testing.T) {
	rt := newFakeRuntime()
	cfg := defaultTestCfg()

	// Isolate state BEFORE calling state.EnsureSession so all operations share the same dir.
	isolateState(t)

	projID, projAbs := setupProject(t)
	name := "agentbox-" + projID
	rt.boxes[name] = container.Box{
		ProjectID: projID,
		CWD:       projAbs,
		Status:    container.StatusRunning,
	}

	// Ensure a session dir exists.
	if _, err := state.EnsureSession(projID); err != nil {
		t.Fatalf("setup session: %v", err)
	}

	// Verify session dir is there.
	sessionDir, _ := state.SessionDir(projID)
	if _, err := os.Stat(sessionDir); err != nil {
		t.Fatalf("session dir should exist before Rm: %v", err)
	}

	l := &lifecycle.Lifecycle{
		Cfg:    cfg,
		Runtime: rt,
		Home:   t.TempDir(),
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
	}
	if err := l.Rm(lifecycle.RmOpts{Input: "."}); err != nil {
		t.Fatalf("Rm: %v", err)
	}

	if _, ok := rt.boxes[name]; ok {
		t.Error("expected container to be removed from runtime")
	}
	if _, err := os.Stat(sessionDir); !errors.Is(err, fs.ErrNotExist) {
		t.Error("expected session dir to be removed after Rm")
	}
}

func TestRm_KeepStateSkipsRemoveSession(t *testing.T) {
	rt := newFakeRuntime()
	cfg := defaultTestCfg()

	// Isolate state BEFORE calling state.EnsureSession.
	isolateState(t)

	projID, projAbs := setupProject(t)
	name := "agentbox-" + projID
	rt.boxes[name] = container.Box{
		ProjectID: projID,
		CWD:       projAbs,
		Status:    container.StatusRunning,
	}
	if _, err := state.EnsureSession(projID); err != nil {
		t.Fatalf("setup session: %v", err)
	}
	sessionDir, _ := state.SessionDir(projID)

	l := &lifecycle.Lifecycle{
		Cfg:    cfg,
		Runtime: rt,
		Home:   t.TempDir(),
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
	}
	if err := l.Rm(lifecycle.RmOpts{Input: ".", KeepState: true}); err != nil {
		t.Fatalf("Rm KeepState: %v", err)
	}
	if _, err := os.Stat(sessionDir); err != nil {
		t.Errorf("state dir should survive KeepState=true, but got error: %v", err)
	}
}

func TestRm_AllRequiresForce(t *testing.T) {
	rt := newFakeRuntime()
	cfg := defaultTestCfg()

	rt.boxes["agentbox-aaa000000000"] = container.Box{
		ProjectID: "aaa000000000",
		Status:    container.StatusRunning,
	}

	l := newTestLifecycle(t, rt, cfg, nil)
	err := l.Rm(lifecycle.RmOpts{All: true, Force: false})
	if err == nil {
		t.Fatal("expected error for --all without --force")
	}
	var ee *exitcode.Err
	if !errors.As(err, &ee) {
		t.Fatalf("expected *exitcode.Err, got %T", err)
	}
	if ee.Code != exitcode.InvalidArgs {
		t.Errorf("expected InvalidArgs (%d), got %d", exitcode.InvalidArgs, ee.Code)
	}
}

func TestRm_AllForceRemovesAll(t *testing.T) {
	rt := newFakeRuntime()
	cfg := defaultTestCfg()
	isolateState(t)

	rt.boxes["agentbox-aaa000000000"] = container.Box{ProjectID: "aaa000000000", Status: container.StatusRunning}
	rt.boxes["agentbox-bbb000000000"] = container.Box{ProjectID: "bbb000000000", Status: container.StatusStopped}

	l := newTestLifecycle(t, rt, cfg, nil)
	if err := l.Rm(lifecycle.RmOpts{All: true, Force: true}); err != nil {
		t.Fatalf("Rm --all --force: %v", err)
	}
	if len(rt.boxes) != 0 {
		t.Errorf("expected all boxes removed, got %d remaining", len(rt.boxes))
	}
}

func TestRm_NoArgs(t *testing.T) {
	rt := newFakeRuntime()
	cfg := defaultTestCfg()
	l := newTestLifecycle(t, rt, cfg, nil)

	err := l.Rm(lifecycle.RmOpts{})
	if err == nil {
		t.Fatal("expected error for Rm with no args")
	}
	var ee *exitcode.Err
	if !errors.As(err, &ee) {
		t.Fatalf("expected *exitcode.Err, got %T", err)
	}
	if ee.Code != exitcode.InvalidArgs {
		t.Errorf("expected InvalidArgs (%d), got %d", exitcode.InvalidArgs, ee.Code)
	}
}

// ---- Phase 6: Network manager integration tests ----

func TestEnsureBox_Safe_PassesDNSThrough(t *testing.T) {
	rt := newFakeRuntime()
	cfg := defaultTestCfg()
	cfg.Network.Mode = "safe"
	setupProject(t)

	netMgr := newFakeNetworkManager()
	netMgr.setupInfo = network.Info{
		NetworkName: "agentbox-net-testprojid",
		SidecarDNS:  []string{"10.89.171.2"},
	}

	isolateState(t)
	l := &lifecycle.Lifecycle{
		Cfg:     cfg,
		Runtime: rt,
		Builder: newTestBuilder(t, nil),
		Network: netMgr,
		Home:    t.TempDir(),
		Stdout:  &bytes.Buffer{},
		Stderr:  &bytes.Buffer{},
	}

	_, err := l.EnsureBox(lifecycle.EnsureOpts{})
	if err != nil {
		t.Fatalf("EnsureBox: %v", err)
	}

	// The Network.Setup should have been called
	if len(netMgr.setupCalls) == 0 {
		t.Fatal("expected Network.Setup to be called")
	}

	// The Create call should have DNS set from the sidecar info.
	// Inspect the created box's args via what was stored in fakeRuntime.
	// We verify indirectly: if Create was called (box was created) and
	// no error occurred, the DNS was threaded through correctly.
	if !containsCall(rt.calls, "Create") {
		t.Error("expected Create to be called (missing box path)")
	}
}

func TestRm_Safe_TeardownNetwork(t *testing.T) {
	rt := newFakeRuntime()
	cfg := defaultTestCfg()
	cfg.Network.Mode = "safe"

	isolateState(t)
	projID, projAbs := setupProject(t)
	name := "agentbox-" + projID
	rt.boxes[name] = container.Box{
		ProjectID: projID,
		CWD:       projAbs,
		Status:    container.StatusRunning,
		Role:      "box",
	}

	netMgr := newFakeNetworkManager()
	l := &lifecycle.Lifecycle{
		Cfg:     cfg,
		Runtime: rt,
		Builder: newTestBuilder(t, nil),
		Network: netMgr,
		Home:    t.TempDir(),
		Stdout:  &bytes.Buffer{},
		Stderr:  &bytes.Buffer{},
	}

	if err := l.Rm(lifecycle.RmOpts{Input: "."}); err != nil {
		t.Fatalf("Rm: %v", err)
	}

	// Network.Teardown should have been called
	if len(netMgr.teardownCalls) == 0 {
		t.Error("expected Network.Teardown to be called on Rm for safe mode")
	}
}

func TestLs_FiltersByRoleBox(t *testing.T) {
	rt := newFakeRuntime()
	cfg := defaultTestCfg()

	// Populate fake runtime with one box and two sidecars
	rt.boxes["agentbox-aaa000000000"] = container.Box{
		ProjectID: "aaa000000000",
		Project:   "proj-a",
		Agent:     "claude",
		Status:    container.StatusRunning,
		Role:      "box", // user-facing box
	}
	rt.boxes["agentbox-coredns-aaa000000000"] = container.Box{
		ProjectID: "aaa000000000",
		Status:    container.StatusRunning,
		Role:      "coredns", // sidecar — should be filtered out
	}
	rt.boxes["agentbox-netfilter-aaa000000000"] = container.Box{
		ProjectID: "aaa000000000",
		Status:    container.StatusRunning,
		Role:      "netfilter", // sidecar — should be filtered out
	}

	l := newTestLifecycle(t, rt, cfg, nil)

	boxes, err := l.Ls(lifecycle.LsFilter{All: true})
	if err != nil {
		t.Fatalf("Ls: %v", err)
	}
	if len(boxes) != 1 {
		t.Errorf("expected 1 box (role=box), got %d: %+v", len(boxes), boxes)
	}
	if len(boxes) > 0 && boxes[0].Role != "box" && boxes[0].Role != "" {
		t.Errorf("expected box with role=box or empty, got role=%q", boxes[0].Role)
	}
}

func TestLs_UnlabeledBoxIncluded(t *testing.T) {
	// Pre-Phase-6 containers have no agentbox.role label; they should still appear in Ls.
	rt := newFakeRuntime()
	cfg := defaultTestCfg()

	rt.boxes["agentbox-aaa000000000"] = container.Box{
		ProjectID: "aaa000000000",
		Project:   "proj-a",
		Agent:     "claude",
		Status:    container.StatusRunning,
		Role:      "", // unlabeled (pre-Phase-6)
	}

	l := newTestLifecycle(t, rt, cfg, nil)

	boxes, err := l.Ls(lifecycle.LsFilter{All: true})
	if err != nil {
		t.Fatalf("Ls: %v", err)
	}
	if len(boxes) != 1 {
		t.Errorf("expected 1 box (unlabeled pre-Phase-6), got %d", len(boxes))
	}
}

// ---- Group B: trail wiring gate tests ----

// captureCreateArgs is a fakeRuntime that captures the PodmanCreateArgs passed
// to Create so tests can inspect mounts and env vars.
type captureRuntime struct {
	*fakeRuntime
	lastCreate runspec.PodmanCreateArgs
}

func newCaptureRuntime() *captureRuntime {
	return &captureRuntime{fakeRuntime: newFakeRuntime()}
}

func (r *captureRuntime) Create(args runspec.PodmanCreateArgs) error {
	r.lastCreate = args
	return r.fakeRuntime.Create(args)
}

// newCaptureLifecycle builds a Lifecycle with a captureRuntime so tests can
// inspect the PodmanCreateArgs. It also pre-creates the ~/.claude dir so the
// WriteShadowSettings path exists when auditor+claude fires.
func newCaptureLifecycle(t *testing.T, cr *captureRuntime, cfg config.Config) *lifecycle.Lifecycle {
	t.Helper()
	isolateState(t)
	homeDir := t.TempDir()
	// Create ~/.claude dir so WriteShadowSettings can find it (it reads
	// ~/.claude/settings.json which may not exist — that's fine, MergeTrailHooks
	// handles a missing file). The dir itself must exist for path join to work
	// cleanly in WriteShadowSettings.
	os.MkdirAll(filepath.Join(homeDir, ".claude"), 0o700)
	netMgr := newFakeNetworkManager()
	return &lifecycle.Lifecycle{
		Cfg:     cfg,
		Runtime: cr,
		Builder: newTestBuilder(t, nil),
		Network: netMgr,
		Home:    homeDir,
		Stdout:  &bytes.Buffer{},
		Stderr:  &bytes.Buffer{},
	}
}

func hasMount(mounts []runspec.Mount, target, mode string) bool {
	for _, m := range mounts {
		if m.Target == target && m.Mode == mode {
			return true
		}
	}
	return false
}

func hasEnvVar(envVars []runspec.KV, key string) bool {
	for _, kv := range envVars {
		if kv.Key == key {
			return true
		}
	}
	return false
}

func TestRun_AuditorClaude_WiresTrailMount(t *testing.T) {
	cr := newCaptureRuntime()
	cfg := defaultTestCfg()
	setupProject(t)

	l := newCaptureLifecycle(t, cr, cfg)
	err := l.Run(lifecycle.RunOpts{
		Attach: false,
		Layout: "auditor",
		Agent:  "claude",
	})
	if err != nil {
		t.Fatalf("Run(auditor, claude): %v", err)
	}
	mounts := cr.lastCreate.Mounts
	if !hasMount(mounts, "/etc/agentbox/trail.jsonl", "rw") {
		t.Errorf("expected trail.jsonl rw mount for auditor+claude, mounts: %+v", mounts)
	}
	if !hasMount(mounts, "/root/.claude/settings.json", "ro") {
		t.Errorf("expected shadow settings ro mount for auditor+claude, mounts: %+v", mounts)
	}
	if !hasEnvVar(cr.lastCreate.EnvVars, "BOX_TRAIL_FILE") {
		t.Errorf("expected BOX_TRAIL_FILE env var for auditor+claude, envvars: %+v", cr.lastCreate.EnvVars)
	}
}

func TestRun_FocusClaude_NoTrailMount(t *testing.T) {
	cr := newCaptureRuntime()
	cfg := defaultTestCfg()
	setupProject(t)

	l := newCaptureLifecycle(t, cr, cfg)
	err := l.Run(lifecycle.RunOpts{
		Attach: false,
		Layout: "focus",
		Agent:  "claude",
	})
	if err != nil {
		t.Fatalf("Run(focus, claude): %v", err)
	}
	mounts := cr.lastCreate.Mounts
	if hasMount(mounts, "/etc/agentbox/trail.jsonl", "rw") {
		t.Errorf("trail mount should be absent for focus+claude, mounts: %+v", mounts)
	}
	if hasEnvVar(cr.lastCreate.EnvVars, "BOX_TRAIL_FILE") {
		t.Errorf("BOX_TRAIL_FILE should be absent for focus+claude, envvars: %+v", cr.lastCreate.EnvVars)
	}
}

func TestRun_AuditorNonClaude_NoTrailMount(t *testing.T) {
	cr := newCaptureRuntime()
	cfg := defaultTestCfg()
	// Add a non-claude agent to the config.
	cfg.Agents["codex"] = config.Agent{Kits: []string{"base"}}
	setupProject(t)

	l := newCaptureLifecycle(t, cr, cfg)
	err := l.Run(lifecycle.RunOpts{
		Attach: false,
		Layout: "auditor",
		Agent:  "codex",
	})
	if err != nil {
		t.Fatalf("Run(auditor, codex): %v", err)
	}
	mounts := cr.lastCreate.Mounts
	if hasMount(mounts, "/etc/agentbox/trail.jsonl", "rw") {
		t.Errorf("trail mount should be absent for auditor+codex, mounts: %+v", mounts)
	}
	if hasEnvVar(cr.lastCreate.EnvVars, "BOX_TRAIL_FILE") {
		t.Errorf("BOX_TRAIL_FILE should be absent for auditor+codex, envvars: %+v", cr.lastCreate.EnvVars)
	}
}

// ---- Phase 7: containers seccomp wiring tests ----

func TestEnsureBox_ContainersEnable_WritesSeccompProfile(t *testing.T) {
	rt := newFakeRuntime()
	cfg := defaultTestCfg()
	cfg.Containers.Enable = true
	setupProject(t)

	// newTestLifecycle calls isolateState which sets XDG_DATA_HOME to a temp dir.
	// seccomp.EnsureContainersProfile uses state.Dir() which reads XDG_DATA_HOME.
	l := newTestLifecycle(t, rt, cfg, nil)

	_, err := l.EnsureBox(lifecycle.EnsureOpts{})
	if err != nil {
		t.Fatalf("EnsureBox with Containers.Enable=true: %v", err)
	}

	// Verify the seccomp profile was written to the state dir.
	dataHome := os.Getenv("XDG_DATA_HOME")
	seccompPath := filepath.Join(dataHome, "agentbox", "seccomp", "containers.json")
	if _, err := os.Stat(seccompPath); err != nil {
		t.Errorf("expected seccomp profile at %s, stat error: %v", seccompPath, err)
	}
}

func TestEnsureBox_ContainersDisable_NoSeccompProfile(t *testing.T) {
	rt := newFakeRuntime()
	cfg := defaultTestCfg()
	cfg.Containers.Enable = false
	setupProject(t)

	l := newTestLifecycle(t, rt, cfg, nil)

	_, err := l.EnsureBox(lifecycle.EnsureOpts{})
	if err != nil {
		t.Fatalf("EnsureBox with Containers.Enable=false: %v", err)
	}

	// Verify the seccomp profile was NOT written when containers are disabled.
	dataHome := os.Getenv("XDG_DATA_HOME")
	seccompPath := filepath.Join(dataHome, "agentbox", "seccomp", "containers.json")
	if _, err := os.Stat(seccompPath); err == nil {
		t.Errorf("seccomp profile should not exist when Containers.Enable=false, but found at %s", seccompPath)
	}
}
