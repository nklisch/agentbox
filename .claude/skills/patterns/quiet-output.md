# Pattern: Quiet-Aware Output Helpers

Use `info()`/`warn()` for status chatter; `result()` (or direct stdout writes) for primary output. Never suppress errors or primary output.

## Rationale

CLAUDE.md: `--quiet` suppresses non-error output. The project distinguishes two output streams: primary output (table rows, JSON, dry-run shell, structured data) always goes to stdout and is never suppressed; informational chatter ("removed abc123", "no boxes", build status) goes to stderr and is suppressed by `--quiet`. This keeps `agentbox ls --json | jq` pipe-clean.

## Examples

### Example 1: CLI-layer helpers
**File**: `internal/cli/output.go:17`
```go
// info → stderr, suppressed by --quiet
func info(cmd *cobra.Command, format string, args ...any) {
    if global.Quiet { return }
    fmt.Fprintf(cmd.ErrOrStderr(), format+"\n", args...)
}

// result → stdout, never suppressed (it IS the output)
func result(cmd *cobra.Command, format string, args ...any) {
    fmt.Fprintf(cmd.OutOrStdout(), format+"\n", args...)
}

// fInfo → stderr writer or io.Discard; for passing to builders
func fInfo(cmd *cobra.Command) io.Writer {
    if global.Quiet { return io.Discard }
    return cmd.ErrOrStderr()
}
```

### Example 2: Lifecycle-layer helpers
**File**: `internal/lifecycle/output.go:11`
```go
// Same semantics, but keyed on l.Quiet instead of global.Quiet.
func (l *Lifecycle) info(format string, args ...any) {
    if l.Quiet { return }
    fmt.Fprintf(l.Stderr, format+"\n", args...)
}

func (l *Lifecycle) warn(format string, args ...any) {
    fmt.Fprintf(l.Stderr, format+"\n", args...)  // never suppressed
}
```

### Example 3: Correct usage of info for success messages
**File**: `internal/lifecycle/lifecycle.go` (rmOne)
```go
// Confirmation that the mutation happened — informational, goes via l.info.
l.info("removed %s", projID)
```

### Example 4: Correct usage for empty-state hint
**File**: `internal/cli/ls.go`
```go
if len(boxes) == 0 {
    info(cmd, "no boxes")  // hint to stderr, suppressed by --quiet
    return nil
}
return printLsTable(cmd.OutOrStdout(), boxes)  // result stays on stdout always
```

## When to Use
- Status confirmations ("removed X", "no boxes") → `info` / `l.info`
- Warnings about non-fatal failures → `warn` / `l.warn`
- Build/pull progress → `fInfo(cmd)` writer passed to Builder
- Primary output: table rows, JSON, Dockerfile content, dry-run shell → direct stdout, never via `info`

## When NOT to Use
- Don't route primary structured output through `info` — it goes to stderr and is suppressable, which would make `agentbox ls | wc -l` always return 0
- Don't gate error messages on `--quiet` — errors always print regardless

## Common Violations
- Writing informational messages directly to `l.Stdout` without the `l.Quiet` guard — add `l.info(...)` instead
- Using `fmt.Fprintf(os.Stderr, ...)` inside a lifecycle or network package — use the injected `l.Stderr` / `l.warn()` / `m.Stderr` so the output is capturable in tests
