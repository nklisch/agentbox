# Pattern: Test Isolation Recipe

Combine `setupProject` + `isolateState` + `defaultTestCfg` + `runCmd`/`newTestLifecycle` for a self-contained, parallelism-safe test.

## Rationale

Lifecycle tests change `$PWD` (project ID derivation) and state directories (`$XDG_DATA_HOME`). CLI tests spawn the full cobra command tree. Without isolation, parallel tests stomp on each other's project IDs and state dirs. The four helpers set up a hermetic environment for each test; `t.Cleanup` automatically restores it.

## Examples

### Example 1: Full lifecycle test setup
**File**: `internal/lifecycle/lifecycle_test.go:807`
```go
func TestRm_RemovesContainerAndState(t *testing.T) {
    rt := newFakeRuntime()
    cfg := defaultTestCfg()

    projID, projAbs := setupProject(t)   // chdir to temp dir; returns hash ID
    isolateState(t)                       // sets XDG_DATA_HOME to a temp dir

    // Pre-populate the fake runtime as if the box already exists.
    rt.boxes["agentbox-"+projID] = container.Box{
        ProjectID: projID, CWD: projAbs, Status: container.StatusRunning,
    }

    l := newTestLifecycle(t, rt, cfg, nil)
    if err := l.Rm(lifecycle.RmOpts{Input: "."}); err != nil {
        t.Fatalf("Rm: %v", err)
    }
    // Assert state dir was removed.
    dir, _ := state.SessionDir(projID)
    if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
        t.Errorf("expected state dir to be removed, but it still exists")
    }
}
```

### Example 2: `defaultTestCfg` — baseline with minimal deps
**File**: `internal/lifecycle/lifecycle_test.go:259`
```go
func defaultTestCfg() config.Config {
    cfg := config.DefaultConfig()
    cfg.Network.Mode = "open"               // no DNS/iptables needed
    cfg.Agents = map[string]config.Agent{
        "claude": {Kits: []string{"base"}}, // only the built-in "base" kit
    }
    cfg.DefaultAgent = "claude"
    cfg.Mounts.Gitconfig   = false          // avoid ~/.gitconfig dependency
    cfg.Mounts.SSHReadonly = false          // avoid ~/.ssh dependency
    cfg.Mounts.AgentConfigs = nil           // avoid ~/.claude dependency
    return cfg
}
```

### Example 3: CLI end-to-end test with runCmd
**File**: `internal/cli/cli_test.go:271`
```go
func TestRun_NoAttach_FakeRuntime(t *testing.T) {
    tmp := t.TempDir()
    t.Setenv("XDG_CONFIG_HOME", tmp)
    t.Setenv("XDG_DATA_HOME", tmp)
    orig, _ := os.Getwd()
    if err := os.Chdir(tmp); err != nil { t.Fatalf("chdir: %v", err) }
    t.Cleanup(func() { _ = os.Chdir(orig) })

    rt := newFakeRuntime()
    restore := setupFakeLifecycle(t, rt)
    defer restore()

    stdout, stderr, err := runCmd(t, "run", "--no-attach")
    if err != nil { t.Fatalf("run: %v", err) }
    if !strings.Contains(stdout+stderr, "running") {
        t.Errorf("expected running status, got stdout=%q stderr=%q", stdout, stderr)
    }
}
```

### Example 4: Capture lifecycle constructor for inspecting Create args
**File**: `internal/lifecycle/lifecycle_test.go:1238`
```go
func TestRun_AuditorClaude_WiresTrailMount(t *testing.T) {
    cr := newCaptureRuntime()  // wraps fakeRuntime, also records lastCreate
    cfg := defaultTestCfg()
    setupProject(t)

    l := newCaptureLifecycle(t, cr, cfg)
    err := l.Run(lifecycle.RunOpts{Attach: false, Layout: "auditor", Agent: "claude"})
    if err != nil { t.Fatalf("Run: %v", err) }

    mounts := cr.lastCreate.Mounts
    if !hasMount(mounts, "/etc/agentbox/trail.jsonl", "rw") {
        t.Errorf("expected trail.jsonl rw mount")
    }
}
```

## When to Use
- All lifecycle tests that call methods that derive projectID from `$PWD` — must use `setupProject`
- All lifecycle tests that create session state — must use `isolateState`
- CLI end-to-end tests — use `runCmd` + `setupFakeLifecycle` + explicit XDG overrides
- Tests that need to inspect `podman create` arguments — use `newCaptureLifecycle`

## When NOT to Use
- Tests that don't interact with project IDs or state (unit tests for pure helpers like `ExpandHome`, `MergeTrailHooks`) — don't need the setup recipe

## Common Violations
- Using `isolateState` without `setupProject` (or vice versa) — state dir is keyed by projectID; the project must be set up first
- Hardcoding a project ID instead of deriving it with `setupProject` — project ID is `sha1(realpath)[:12]`; `/tmp/foo` on macOS resolves to `/private/tmp/foo`, giving a different hash
- Forgetting to `defer restore()` for `setupFakeLifecycle` — leaves the global factory overridden, breaking subsequent tests
