# Pattern: Exit Code Error Propagation

Every public error returned to the CLI layer carries a typed exit code via `exitcode.Wrap` or `exitcode.New`.

## Rationale

CLAUDE.md: "Exit codes are part of the contract." `main.go` unwraps `*exitcode.Err` to set the process exit code. All other errors become exit code 1 (generic). The codes are: 1=generic, 2=invalid args/config, 3=runtime unavailable, 4=box not found, 5=kit build failed, 6=network setup failed, 7=mount source missing, 130=SIGINT.

## Examples

### Example 1: Wrapping an underlying error with a specific code
**File**: `internal/lifecycle/lifecycle.go:107`
```go
// When the underlying error already has a message, wrap it.
if err := l.Runtime.Start(name); err != nil {
    return container.Box{}, exitcode.Wrap(exitcode.Generic, err)
}
```

### Example 2: Creating a new error with a specific code
**File**: `internal/lifecycle/lifecycle.go:134`
```go
// When you own the message (no underlying error to preserve).
if !ok {
    return container.Box{}, exitcode.New(exitcode.InvalidArgs,
        "agent %q not defined in [agents.*]", agent)
}
```

### Example 3: Domain-specific codes
**File**: `internal/lifecycle/lifecycle.go:156`
```go
if err != nil {
    return container.Box{}, exitcode.Wrap(exitcode.KitBuild, err)
}
```

**File**: `internal/lifecycle/lifecycle.go:233`
```go
return container.Box{}, exitcode.New(exitcode.MountMissing,
    "mount source missing on host: %s", m.Source)
```

### Example 4: Unwrapping in CLI (main.go handles this automatically)
**File**: `internal/exitcode/exitcode.go`
```go
type Err struct {
    Code int
    msg  string
    Wrap error
}
func (e *Err) Error() string { ... }
func Code(err error) int { ... }  // returns Code if *Err, else 1
```

## When to Use
- Any error returning up to a cobra `RunE` function should carry a code
- Use the most specific code that applies (`KitBuild` for kit errors, `NotFound` for missing box, etc.)
- `exitcode.Generic` is the catch-all for unexpected internal errors

## When NOT to Use
- Don't use exitcode inside packages that lifecycle/cli calls (e.g., `internal/kits`, `internal/state`) — these return plain errors; the lifecycle layer adds codes when it re-wraps
- Don't double-wrap: `exitcode.Wrap(exitcode.Generic, exitcode.New(…))` loses the inner code; check if the error already has a code before wrapping

## Common Violations
- Using `exitcode.Generic` for errors that have a specific code (e.g. returning generic when the box wasn't found — should be `exitcode.NotFound`)
- Returning raw `errors.New(...)` from a RunE function — main.go translates un-wrapped errors to exit code 1, which may be technically correct, but the intent isn't explicit
