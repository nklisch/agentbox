# agentbox pattern index

Load individual pattern files from `.claude/skills/patterns/` for full details + code examples.

- **exitcode**: Every error returned to cobra RunE carries `exitcode.Wrap(code, err)` or `exitcode.New(code, format, ...)` → [exitcode.md]
- **lifecycle-struct**: Orchestrator structs inject Stdin/Stdout/Stderr/Quiet + domain deps; methods use `l.info()`/`l.warn()`; never `os.Stdout` directly → [lifecycle-struct.md]
- **cli-command**: Box commands use `initLifecycleCmd(cmd)`; build/doctor/config call `loadConfig()` directly (no lifecycle) → [cli-command.md]
- **quiet-output**: `info()`/`l.info()` for chatter → stderr, suppressed by --quiet; `result()`/direct stdout for primary output, never suppressed → [quiet-output.md]
- **dry-run-output**: `# key = value` comment headers then shell commands to stdout; bash-safe; mutually exclusive with `--print`/`--list` → [dry-run-output.md]
- **generator-function**: `Generate*` funcs take a struct, return a string, no side effects; caller writes to disk → [generator-function.md]
- **test-seam**: Package-level var + `Set*(fn) func()` restore closure for swappable behavior in tests → [test-seam.md]
- **runtime-fake**: Implements `container.Runtime`; records calls in `calls []string`; injects errors via `failOn map[string]error` → [runtime-fake.md]
- **test-isolation**: `setupProject(t)` + `isolateState(t)` + `defaultTestCfg()` + `newTestLifecycle`/`runCmd` for hermetic tests → [test-isolation.md]
