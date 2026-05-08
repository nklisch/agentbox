# Pattern: Lifecycle Struct (injected IO writers + named dependencies)

Orchestrator structs carry their own Stdin/Stdout/Stderr/Quiet alongside domain dependencies — never reach for os.Stdout directly.

## Rationale

Commands wire the correct writers at construction time; lifecycle methods use those writers exclusively. This makes output capturable by tests (`l.Stdout = &bytes.Buffer{}`), quietable (`l.Quiet = true`), and redirectable (cobra's `cmd.OutOrStdout()`). It also makes the code testable without spawning a real terminal.

## Examples

### Example 1: lifecycle.Lifecycle
**File**: `internal/lifecycle/lifecycle.go:41`
```go
type Lifecycle struct {
    Cfg     config.Config
    Runtime container.Runtime  // dependency (interface)
    Builder *kits.Builder      // dependency (concrete; owns its own I/O)
    Network NetworkManager     // dependency (interface)
    Home    string

    Stdin  io.Reader  // nil disables interactive prompts (e.g. rm --all)
    Stdout io.Writer
    Stderr io.Writer
    Quiet  bool       // suppress informational output

    pendingLayoutName string   // ephemeral, per-Run only
}
```

### Example 2: network.Manager
**File**: `internal/network/manager.go:19`
```go
type Manager struct {
    Runtime      container.Runtime
    IPTables     *IPTables
    NetfilterBin string
    Stderr       io.Writer  // nil → io.Discard in stderrOf()
}
```

### Example 3: info/warn helpers derived from the writers
**File**: `internal/lifecycle/output.go:11`
```go
// info writes to Stderr, suppressed by Quiet.
func (l *Lifecycle) info(format string, args ...any) {
    if l.Quiet { return }
    fmt.Fprintf(l.Stderr, format+"\n", args...)
}

// warn always writes to Stderr (never suppressed).
func (l *Lifecycle) warn(format string, args ...any) {
    fmt.Fprintf(l.Stderr, format+"\n", args...)
}
```

### Example 4: CLI wiring via initLifecycleCmd
**File**: `internal/cli/lifecycle.go:33`
```go
// Standard prelude: one call wires all IO and propagates --quiet.
func initLifecycleCmd(cmd *cobra.Command) (*lifecycle.Lifecycle, configResult, error) {
    res, err := loadConfig()
    if err != nil { return nil, configResult{}, err }
    l, err := newLifecycle(res.Config)
    if err != nil { return nil, configResult{}, exitcode.Wrap(exitcode.Generic, err) }
    l.Stdout = cmd.OutOrStdout()
    l.Stderr = cmd.ErrOrStderr()
    l.Quiet  = global.Quiet
    return l, res, nil
}
```

## When to Use
- Any long-lived orchestrator that drives I/O-producing operations
- Always use `cmd.OutOrStdout()` / `cmd.ErrOrStderr()` (not `os.Stdout`) when wiring from cobra

## When NOT to Use
- Short-lived helpers that receive writers as function parameters (e.g., `BuildContext{Stdout: ...}` in kits)
- Read-only helpers with no output (path helpers, validators)

## Common Violations
- Methods reaching for `os.Stdout` directly instead of `l.Stdout` — breaks test capture and `--quiet`
- Forgetting to set `l.Quiet = global.Quiet` after construction — the factory's default is already set, but test factories must also set it, or use `initLifecycleCmd` to ensure it
- Skipping `Stdin io.Reader` for commands that need interactive prompts — `l.Stdin` nil disables prompts (safe for non-interactive paths) but must be set for `confirmRmAll`-style flows
