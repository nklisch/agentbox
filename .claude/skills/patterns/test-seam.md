# Pattern: Test Seam (package-level var + Set* restore)

Make a dependency swappable in tests without interfaces — one file, three lines, full reversibility.

## Rationale

Used when the real implementation reads global state (`os.Stdin`, `os.Getenv`) that can't be injected via a struct field, or when the function to swap is the package-level factory itself. The `Set*` function returns a restore closure so `defer restore()` is idiomatic.

## Examples

### Example 1: TTY detection
**File**: `internal/lifecycle/lifecycle.go:314`
```go
// The default uses os.Stdin; tests replace it with a func returning a constant.
var stdinIsTerminal = func() bool { return isTerminal(os.Stdin) }

// SetStdinIsTerminal replaces the TTY-detection function used by Run/Shell/Attach.
// Returns a restore function the caller should defer. Intended for tests.
func SetStdinIsTerminal(fn func() bool) func() {
    orig := stdinIsTerminal
    stdinIsTerminal = fn
    return func() { stdinIsTerminal = orig }
}
```

### Example 2: Lifecycle factory
**File**: `internal/cli/lifecycle.go:50` (definition), `internal/cli/export_test.go:9` (setter)
```go
// Definition — the real factory builds podman-backed Lifecycle.
var newLifecycle = func(cfg config.Config) (*lifecycle.Lifecycle, error) { ... }

// Setter in export_test.go (compiled only during tests):
func SetLifecycleFactory(fn func(config.Config) (*lifecycle.Lifecycle, error)) func() {
    orig := newLifecycle
    newLifecycle = fn
    return func() { newLifecycle = orig }
}
```

### Test usage
**File**: `internal/lifecycle/lifecycle_test.go:913`
```go
restore := lifecycle.SetStdinIsTerminal(func() bool { return true })
defer restore()
l.Stdin = strings.NewReader("y\n")
```

## When to Use
- When the behavior to swap reads global OS state (stdin, filesystem, system time) that can't be cleanly injected via a struct field
- When the function being swapped is a package-level factory whose output type is the struct that *holds* the injected dependencies (bootstrapping problem)

## When NOT to Use
- When you can inject via a struct field — prefer struct injection (see lifecycle-struct pattern)
- When more than one test file needs the seam — move it to export_test.go
- Don't add `Set*` functions for regular business logic; this pattern is specifically for seams

## Common Violations
- Forgetting `defer restore()` — the var stays swapped for subsequent tests in the same package, causing mysterious failures
- Putting `Set*` in a non-test file — only valid in `*_test.go` or `export_test.go` (which is test-only despite the name)
