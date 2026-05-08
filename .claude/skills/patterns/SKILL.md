---
name: patterns
description: "agentbox code patterns with concrete examples. Auto-loads when implementing,
  designing, testing, or reviewing code in this repo. Provides detailed pattern definitions
  with file:line references so agents can follow established conventions."
user-invocable: false
allowed-tools: Read, Glob, Grep
---

# agentbox Pattern Reference

Detailed pattern files with concrete code examples for the agentbox codebase.
See `.claude/rules/patterns.md` for the one-line index.

## Available patterns

- [exitcode.md](exitcode.md) — Exit code error propagation (`exitcode.Wrap`/`New`)
- [lifecycle-struct.md](lifecycle-struct.md) — Orchestrator struct with injected IO writers
- [cli-command.md](cli-command.md) — Cobra RunE prelude (`initLifecycleCmd` vs domain commands)
- [quiet-output.md](quiet-output.md) — `info()`/`warn()` quiet-aware output helpers
- [dry-run-output.md](dry-run-output.md) — `--dry-run` comment-header + shell-command format
- [generator-function.md](generator-function.md) — Pure string generators (`Generate*`)
- [test-seam.md](test-seam.md) — Package-level var + `Set*` restore for test swapping
- [runtime-fake.md](runtime-fake.md) — DI fake: interface impl + call recording + error injection
- [test-isolation.md](test-isolation.md) — Test setup recipe: `setupProject` + `isolateState` + `defaultTestCfg`
