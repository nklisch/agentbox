# Pattern: Dependency-Injection Fake (interface + call recording + error injection)

Implement the full interface, record every call in a slice, and inject errors by name.

## Rationale

`container.Runtime` is the central adapter for all container operations. Tests can't run real podman, so they inject a `fakeRuntime` that records what was called (for assertion) and can be configured to return errors on specific methods (for error-path testing). The same structure exists in both `lifecycle_test.go` (richer, with `failOn`) and `cli_test.go` (simpler). New fakes should follow this template.

## Examples

### Example 1: Full fakeRuntime with error injection
**File**: `internal/lifecycle/lifecycle_test.go:26`
```go
type fakeRuntime struct {
    boxes    map[string]container.Box
    failOn   map[string]error         // method-name → return this error
    calls    []string                 // every method call in order
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
    // ... real state-mutation logic
    return nil
}
```

### Example 2: Asserting on calls
**File**: `internal/lifecycle/lifecycle_test.go:307`
```go
func containsCall(calls []string, name string) bool {
    for _, c := range calls {
        if c == name { return true }
    }
    return false
}

// In a test:
if !containsCall(rt.calls, "Start") {
    t.Error("expected Start to be called")
}
if containsCall(rt.calls, "Create") {
    t.Error("Create should not be called for existing box")
}
```

### Example 3: Error injection
**File**: `internal/lifecycle/lifecycle_test.go` (around TestRm_ tests)
```go
rt := newFakeRuntime()
rt.failOn["Stop"] = errors.New("stop failed: container busy")
// Run the code under test; assert it handles the error gracefully.
```

## When to Use
- Whenever a test needs to exercise code that calls `container.Runtime`
- When you need to verify a method was NOT called (dry-run tests, idempotency tests)
- When you need to test error handling paths

## When NOT to Use
- Don't duplicate this struct in multiple test files — consider a shared test package if more than 2 packages need the same fake. Currently `lifecycle_test` and `cli_test` both have one; that's acceptable because CLI tests are higher-level.

## Common Violations
- Only implementing the happy-path methods — implement ALL interface methods (even stubs that return nil/false/"") or the compiler will catch missing methods
- Not initializing the `failOn` map — `nil` map panics on read; always use `make(map[string]error)`
- Mutating `calls` without `record()` — always go through `record(name)` so the slice builds correctly
