# Pattern: CLI Command RunE Prelude

Box-interacting commands use `initLifecycleCmd`; domain-specific commands have a different prelude.

## Rationale

Six commands (run, shell, attach, exec, ls, rm) all interact with container boxes and follow an identical setup: load config, build lifecycle, wire stdio, propagate --quiet. `initLifecycleCmd` encapsulates this. Other commands (build, doctor, config) have their own domain-specific setups and don't use the lifecycle layer at all.

## Examples

### Example 1: Standard lifecycle-using command (run)
**File**: `internal/cli/run.go:38`
```go
RunE: func(cmd *cobra.Command, args []string) error {
    l, res, err := initLifecycleCmd(cmd)
    if err != nil {
        return err
    }
    if global.DryRun {
        return runDryRun(cmd, res, args, kitsFlag, networkFlag, layoutFlag)
    }
    opts := lifecycle.RunOpts{
        Fresh:  fresh,
        Attach: !noAttach,
        // ...
    }
    return l.Run(opts)
},
```

### Example 2: Simple lifecycle command (ls, rm)
**File**: `internal/cli/ls.go:27`
```go
RunE: func(cmd *cobra.Command, args []string) error {
    l, _, err := initLifecycleCmd(cmd)
    if err != nil {
        return err
    }
    boxes, err := l.Ls(lifecycle.LsFilter{All: all, ...})
    // ...
},
```

### Example 3: Domain command — no lifecycle (build)
**File**: `internal/cli/build.go:34`
```go
RunE: func(cmd *cobra.Command, args []string) error {
    res, err := loadConfig()
    if err != nil {
        return err
    }
    b, err := newBuilder(res.Config)
    if err != nil {
        return err
    }
    switch {
    case listOnly:  return runBuildList(cmd, b)
    case printOnly: return runBuildPrint(cmd, b, kitList(args, res.Config.DefaultKits))
    default:        return runBuildBuild(cmd, b, kitList(...), noCache, noPull)
    }
},
```

### Example 4: Domain command — no lifecycle (doctor)
**File**: `internal/cli/doctor.go:19`
```go
RunE: func(cmd *cobra.Command, args []string) error {
    res, err := loadConfig()
    if err != nil { return err }
    result := doctor.Run(res.Config)
    // output directly without Lifecycle
},
```

## When to Use
- Command interacts with container boxes → `initLifecycleCmd`
- Command does NOT touch boxes (build, doctor, config) → call `loadConfig()` directly and construct the domain-specific helper

## When NOT to Use
- Don't use `initLifecycleCmd` for build/doctor/config subcommands — they need `newBuilder(res.Config)` or direct `doctor.Run(res.Config)`, not a Lifecycle
- Don't add lifecycle interactions to build/doctor — they're intentionally separate

## Common Violations
- Calling `newLifecycle(res.Config)` directly in a command body — always go through `initLifecycleCmd` so the factory seam and `--quiet` wiring are consistent
- Not setting `l.Stdin` for commands that need interactive prompts — `initLifecycleCmd` doesn't set Stdin (it's nil by default); set it explicitly for prompt-using flows
