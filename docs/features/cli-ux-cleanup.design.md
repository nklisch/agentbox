# Design: CLI UX cleanup — honor advertised flags, end silent commands

## Overview

agentbox's CLI surface advertises a contract — in `docs/CLI.md`, in `CLAUDE.md`'s
"Conventions" block, and in cobra help text — that the implementation only partially
keeps. This design pass closes the gap: every flag that's accepted does what its help
text claims, every mutating command honors `--dry-run`, every command that prints
non-essential chatter respects `--quiet`, and every doc claim matches behavior.

Concretely, this design addresses the verified findings from the CLI audit:

| # | Finding                                                                          | Severity |
| - | -------------------------------------------------------------------------------- | -------- |
| 1 | `agentbox run --detach-on-exit` accepted but discarded (`_ = detachOnExit`)      | HIGH     |
| 2 | Global `--quiet` (`-q`) registered but no command consults `global.Quiet`        | HIGH     |
| 3 | `agentbox rm` and `agentbox build` ignore `--dry-run` (CLAUDE.md says universal) | HIGH     |
| 4 | `agentbox config show --effective` accepted but discarded (`_ = effective`)      | MEDIUM   |
| 5 | `agentbox rm <id>` and `rm --all` print nothing on success                       | MEDIUM   |
| 6 | `agentbox shell --fresh` is real but undocumented                                | LOW      |
| 7 | `agentbox ls` prints nothing when no boxes match                                 | LOW      |

Already shipped in a prior commit (out of scope here, but listed for completeness):

- `agentbox rm --all` now actually prompts y/N when stdin is a TTY (`--force` skips).

## Design principles for this pass

1. **Match the contract or remove the flag.** Half-implemented flags are worse than
   missing flags — they mislead. Every kept flag does the documented thing; flags we
   don't intend to implement now are removed (cobra rejects them at parse time, which
   is a clearer failure than silent acceptance).
2. **Lifecycle owns side effects, CLI owns user-facing output.** Dry-run rendering and
   confirmation prompts live in `internal/lifecycle/` (where the operation is
   defined). Output formatting and `--quiet` gating happen at the `internal/cli/`
   layer (close to the user).
3. **Reuse `runspec.PodmanCreateArgs.ToShell` and the existing test seams.** Dry-run
   for `rm` should look textually similar to dry-run for `run`. Tests reuse the
   `runCmd`, `setupFakeLifecycle`, `lifecycle.SetStdinIsTerminal`, and `fakeRuntime`
   helpers — no new test infrastructure unless gaps are unavoidable.
4. **Failures are loud, successes are quiet (but not silent).** Every command that
   completes a mutation prints one line of confirmation by default; `--quiet`
   suppresses it. No mutation finishes with zero feedback.

---

## Implementation Units

### Unit 1: `cli` output helpers + `lifecycle.Quiet` field

**File:** `internal/cli/output.go` (new)

```go
package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// info prints an informational message to the command's stderr. Suppressed
// when --quiet is set. Use for status updates that are nice-to-have but not
// the command's primary output ("removed abc123", "no boxes found", etc.).
//
// info writes to stderr by convention: stdout is reserved for the command's
// structured output (table rows, JSON, dry-run shell). Tools piping
// `agentbox ls --json | jq` should not have to filter status chatter.
func info(cmd *cobra.Command, format string, args ...any) {
	if global.Quiet {
		return
	}
	fmt.Fprintf(cmd.ErrOrStderr(), format+"\n", args...)
}

// result prints the command's primary output to stdout. Always printed,
// regardless of --quiet. JSON, NDJSON, table rows, dry-run shell, and the
// output of `config show` all flow through result (or directly through
// cmd.OutOrStdout() — result is for one-line cases).
func result(cmd *cobra.Command, format string, args ...any) {
	fmt.Fprintf(cmd.OutOrStdout(), format+"\n", args...)
}

// fInfo is the io.Writer-style escape hatch for code paths that need a
// writer (e.g. passing to a builder). Returns either the command's stderr
// or io.Discard depending on --quiet. Caller is responsible for using
// stderr-appropriate framing.
func fInfo(cmd *cobra.Command) io.Writer {
	if global.Quiet {
		return io.Discard
	}
	return cmd.ErrOrStderr()
}
```

**File:** `internal/lifecycle/lifecycle.go` (modify)

```go
type Lifecycle struct {
	// ...existing fields...
	Quiet  bool       // suppress non-error stdout/stderr writes
}
```

Lifecycle methods that currently print informational messages
(`Run` lines 418/423, `Shell` line 456, `Attach` line 596, `rmAll` "aborted"
line, network-teardown warning) gate on `!l.Quiet`. Errors and the
confirmation prompt are NOT gated — they're not "non-error output."

**File:** `internal/cli/lifecycle.go` (modify)

```go
return &lifecycle.Lifecycle{
	// ...existing fields...
	Quiet: global.Quiet,
	Stdin: os.Stdin,
}, nil
```

**Implementation Notes:**

- `info()` writes to **stderr**, not stdout, so `agentbox ls --json --quiet` is
  pipe-clean. CLAUDE.md says "Errors to stderr" — informational messages are
  closer to errors than to results in this regard.
- `lifecycle.Lifecycle.Quiet` is a single bool field, not a callback or logger
  abstraction. Lifecycle has 6 informational print sites total; a struct field is
  the right size.
- The CLI factory threads `global.Quiet` once at construction time — no
  re-reading the global from inside lifecycle.

**Acceptance Criteria:**

- [ ] `info(cmd, ...)` prints to `cmd.ErrOrStderr()` when `global.Quiet == false`
- [ ] `info(cmd, ...)` is a no-op when `global.Quiet == true`
- [ ] `result(cmd, ...)` prints to `cmd.OutOrStdout()` regardless of `global.Quiet`
- [ ] `lifecycle.Lifecycle.Quiet` controls the informational prints in
  `Run`/`Shell`/`Attach`/`rmAll`/`rmOne`
- [ ] Existing prompt in `confirmRmAll` is NOT suppressed by `--quiet` (a prompt
  the user can't see is a hang)
- [ ] Existing error messages and warnings are NOT suppressed by `--quiet`

---

### Unit 2: `--dry-run` for `agentbox rm`

**File:** `internal/lifecycle/lifecycle.go` (modify)

```go
type RmOpts struct {
	Input     string
	All       bool
	Force     bool
	KeepState bool
	DryRun    bool // print equivalent shell commands; do not execute
}
```

```go
// Rm orchestrates rmOne/rmAll dispatch and the dry-run branch.
//
// In dry-run mode, no container is touched, no network is removed, no state
// dir is deleted, and the confirmation prompt is skipped (you can't act on
// what you're not doing). The output is the verbatim shell sequence the
// non-dry-run path would invoke, written to l.Stdout.
func (l *Lifecycle) Rm(opts RmOpts) error
```

**File:** `internal/lifecycle/rm_dryrun.go` (new)

```go
package lifecycle

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/nklisch/agentbox/internal/config"
	"github.com/nklisch/agentbox/internal/container"
	"github.com/nklisch/agentbox/internal/network"
	"github.com/nklisch/agentbox/internal/state"
)

// renderRmShell writes to w the shell commands that rmOne would execute for
// projID. Output is one command per line, prefixed with the runtime
// (`podman` or `docker`) from cfg. State-dir removal is rendered as a
// literal `rm -rf` so the user can copy-paste.
//
// Order matches the live path:
//   1. podman stop <name>           (only if box is running)
//   2. podman rm <name>
//   3. <network teardown>           (per-project network + sidecar)
//   4. rm -rf <state dir>           (unless keepState)
//
// netSpecFor must match what NetworkManager.SpecFor returns at run time.
// Pass nil if no network manager is configured (matches lifecycle.go's
// rmOne early-out).
func renderRmShell(
	w io.Writer,
	cfg config.Config,
	projID string,
	box container.Box,
	keepState bool,
	netSpec *network.Spec,
) error
```

**File:** `internal/cli/rm.go` (modify)

```go
opts := lifecycle.RmOpts{
	All:       all,
	Force:     force,
	KeepState: keepState,
	DryRun:    global.DryRun,
}
```

**Implementation Notes:**

- The dry-run path runs through `Lifecycle.Ls` to enumerate target boxes (so
  `rm --all --dry-run` prints the same set the live path would remove). It
  does NOT call `runtime.Rm` or `network.Teardown`.
- Confirmation prompt is skipped under `--dry-run`: there's nothing destructive
  to confirm, and a prompt mid-pipe (`agentbox rm --all --dry-run | tee plan.sh`)
  would be hostile.
- `rm <id> --dry-run --keep-state` reflects `--keep-state` by omitting the
  state-dir line, matching the live path's behavior.
- The state-dir line uses `rm -rf $(...)` form so it's safe under `set -e` if
  the user pastes the output into a shell script. Resolves the state-dir path
  via `state.SessionDir(projID)`.
- Existing global `--dry-run` is wired in `run.go` as
  `if global.DryRun { return runDryRun(...) }`. We follow the same pattern in
  `rm.go`'s RunE: pass through to lifecycle, which branches internally. The
  CLI doesn't know about lifecycle internals.

**Sample output (`agentbox rm --all --dry-run`):**

```sh
# project_id = a3f2c1d4e5b6 (myapp, running)
podman stop agentbox-a3f2c1d4e5b6
podman rm agentbox-a3f2c1d4e5b6
podman network rm agentbox-net-a3f2c1d4e5b6
rm -rf /home/nathan/.local/share/agentbox/sessions/a3f2c1d4e5b6
# project_id = b8d4e7a1c2f3 (api-svc, exited)
podman rm agentbox-b8d4e7a1c2f3
rm -rf /home/nathan/.local/share/agentbox/sessions/b8d4e7a1c2f3
```

**Acceptance Criteria:**

- [ ] `agentbox rm <id> --dry-run` prints exactly the commands a live `rm` would run, in
  order, to stdout
- [ ] `agentbox rm --all --dry-run` enumerates every matching box and prints commands for
  each, separated by `# project_id = ...` comments
- [ ] Dry-run does NOT call `runtime.Rm`, `runtime.Stop`, or `Network.Teardown`
- [ ] Dry-run does NOT delete `<state>/sessions/<projID>/`
- [ ] Dry-run does NOT prompt (even without `--force`)
- [ ] `--keep-state` causes the `rm -rf <state>` line to be omitted
- [ ] Stopped boxes omit the `podman stop` line (matching live behavior)
- [ ] When the runtime is `docker`, the rendered commands say `docker` not `podman`
- [ ] Exit code is 0 even if no boxes match (matching `agentbox ls` semantics)

---

### Unit 3: `--dry-run` for `agentbox build`

**File:** `internal/cli/build.go` (modify)

```go
// runBuildBuild is the existing happy-path build invocation. We branch
// before calling Builder.Build when global.DryRun is set.
func runBuildBuild(cmd *cobra.Command, b *kits.Builder, requested []string, noCache, noPull bool) error {
	if len(requested) == 0 {
		return exitcode.New(exitcode.InvalidArgs, "no kits requested and default_kits is empty")
	}
	if global.DryRun {
		return runBuildDryRun(cmd, b, requested, noCache, noPull)
	}
	// ... existing live path unchanged ...
}
```

**File:** `internal/cli/build_dryrun.go` (new)

```go
package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/nklisch/agentbox/internal/exitcode"
	"github.com/nklisch/agentbox/internal/kits"
	"github.com/nklisch/agentbox/internal/runspec"
	"github.com/nklisch/agentbox/internal/version"
)

// runBuildDryRun resolves the kit list and prints the commands the live
// `runBuildBuild` would invoke, without touching the cache, the registry,
// or the build context on disk.
//
// Output structure:
//   # kits = polyglot,claude
//   # resolved = base,polyglot,claude
//   # tag = agentbox/<sha[:12]>
//   # remote = ghcr.io/.../agentbox-kits:<version>-<sha[:12]> (when pull-eligible)
//   podman pull ghcr.io/.../agentbox-kits:<version>-<sha[:12]>
//   podman tag <remote> agentbox/<sha[:12]>
//   # OR (when pull skipped or ineligible):
//   podman build -t agentbox/<sha[:12]> -f - <context-dir>
//
// Prefer printing the pull commands when registry+kit list is pull-eligible
// (matching the live ordering: pull first, fall back to build). When pull is
// disabled (--no-pull, registry.enabled=false, or any user kit in the list),
// print only the build command.
func runBuildDryRun(cmd *cobra.Command, b *kits.Builder, requested []string, noCache, noPull bool) error
```

**Implementation Notes:**

- This unit only adds dry-run for the bare `agentbox build` path. The
  `--print`, `--print-tag`, `--list`, `--prune`, `--emit-context` paths
  already serve as introspection commands; combining them with `--dry-run`
  would be ambiguous. Mark `--dry-run` as mutually exclusive with those five
  flags via `cmd.MarkFlagsMutuallyExclusive`.
- The pull-eligibility logic (`registry.enabled && all-builtin && !no-pull`)
  is duplicated from `kits.Builder.Build`. Extract a small predicate
  (`kits.PullEligible(reg, requested, opts) bool`) so both paths agree.
- The remote ref is computed via `runspec.RemoteImageRef(host, version, kits)`
  which already exists and handles the `v` prefix normalization.
- The build context isn't materialized in dry-run; we print
  `<context-dir>` literally as a placeholder unless `--emit-context` is
  set (which it can't be — mutually exclusive).

**Acceptance Criteria:**

- [ ] `agentbox build polyglot,claude --dry-run` prints the resolved kit list, the local
  tag, and either the `podman pull` + `podman tag` pair (pull-eligible) or the
  `podman build` line (not eligible)
- [ ] No `podman build`, `podman pull`, or `podman tag` is actually invoked
- [ ] No cache entry is written
- [ ] The Dockerfile is NOT rendered to disk
- [ ] `--dry-run` combined with `--print`, `--print-tag`, `--list`, `--prune`, or
  `--emit-context` exits 2 with a clear error
- [ ] Output is consumable by `bash -e` (every line is either `#` comment or a
  command)

---

### Unit 4: per-command `--quiet` wiring

**File:** `internal/cli/run.go`, `internal/cli/build.go`, `internal/cli/doctor.go`,
`internal/cli/ls.go`, `internal/lifecycle/lifecycle.go` (modify)

Wrap informational prints in `info(cmd, ...)` (CLI side) or gate on `!l.Quiet`
(lifecycle side). Concrete sites:

| Site                                   | Action                                                       |
| -------------------------------------- | ------------------------------------------------------------ |
| `lifecycle.Run` lines 418, 423          | Gate `Fprintf(l.Stdout, "%s (%s)\n", ...)` on `!l.Quiet`     |
| `lifecycle.Shell` line 456              | Gate liveness print on `!l.Quiet`                            |
| `lifecycle.Attach` line 596             | Gate liveness print on `!l.Quiet`                            |
| `lifecycle.rmAll` "aborted" line       | Gate on `!l.Quiet`                                           |
| `lifecycle.rmOne` network warning      | Keep visible (it's a warning, not chatter)                   |
| `cli/build.go` "built/pulled/cache hit" lines | Replace `fmt.Fprintf(stdout, ...)` with `info(cmd, ...)` (these are status, not output) |
| `cli/doctor.go` per-check print path   | When `--quiet`, only print FAIL lines; final summary still prints exit-code-relevant info |

**Implementation Notes:**

- `agentbox ls` table output is the **result**, not chatter. `--quiet` does NOT
  suppress it. Same for `--json` output.
- `agentbox config show` output is the result — never suppressed.
- `agentbox doctor`'s WARN/OK lines are the result of the diagnostic; under
  `--quiet` we collapse them so only FAIL lines print, but the exit code is
  unchanged. This matches `make`'s `--quiet` semantics.
- Build output lines move from stdout to stderr because they're status
  messages, not parseable output. This is a small behavior change; existing
  CI scripts that capture build's stdout to grep for "built:" will need to
  capture stderr instead. Acceptable: `--print-tag` is the documented
  scriptable-output path for build.

**Acceptance Criteria:**

- [ ] `agentbox run --no-attach -q` prints nothing on success
- [ ] `agentbox run --no-attach` (no `-q`) still prints `<id> (running)`
- [ ] `agentbox build polyglot,claude -q` prints nothing on cache-hit success
- [ ] `agentbox build polyglot,claude -q` still prints podman build progress on failure
  (Builder.Build's stderr is unchanged; only the wrapper line is suppressed)
- [ ] `agentbox doctor -q` prints only FAIL lines; exit code unchanged
- [ ] `agentbox ls -q` still prints the table (result, not chatter)
- [ ] `agentbox ls --json -q` still prints NDJSON
- [ ] Errors are NEVER suppressed by `--quiet`

---

### Unit 5: implement `--detach-on-exit`

**File:** `internal/lifecycle/lifecycle.go` (modify)

```go
type RunOpts struct {
	// ...existing fields...
	DetachOnExit bool // stop the container after the user's session ends
}
```

```go
// Run is unchanged in shape, but the post-zellij path branches on DetachOnExit.
//
// "Detach" here means the user's interactive session ends — they exited
// zellij, the agent process exited and zellij closed (no surviving panes),
// or the connection was severed. In all cases, the box's `sleep infinity`
// main process is still running. With DetachOnExit, we Stop the container
// after the zellij exec returns. The container can be restarted with
// `agentbox run` later (state, layouts, history all persist).
//
// We do NOT remove the container — that's `agentbox rm`. We do NOT track
// the agent process specifically — zellij's exit is the proxy for "user is
// done." This is approximate but matches the user-visible meaning of
// "stop the container when I'm done." The help text reflects that.
func (l *Lifecycle) Run(opts RunOpts) error {
	// ... existing pre-zellij logic ...
	err := l.zellijAttach(box)
	if opts.DetachOnExit {
		if stopErr := l.Runtime.Stop(container.ContainerName(box.ProjectID)); stopErr != nil {
			fmt.Fprintf(l.Stderr, "warning: --detach-on-exit: stop container: %v\n", stopErr)
		}
	}
	return err
}
```

**File:** `internal/cli/run.go` (modify)

```go
opts := lifecycle.RunOpts{
	// ...existing fields...
	DetachOnExit: detachOnExit,
}
// remove: _ = detachOnExit
```

**File:** `internal/cli/run.go` flag declaration update:

```go
cmd.Flags().BoolVar(&detachOnExit, "detach-on-exit", false,
	"stop the container when this session ends (default: keep running for `agentbox attach`)")
```

**Implementation Notes:**

- We stop on `Run`'s zellij path only. `Shell` could grow the same flag later
  but isn't currently advertised, so we don't add it.
- `--no-attach` + `--detach-on-exit` is a contradiction (no session to detach
  from); reject with exit 2 in CLI validation, matching the existing pattern.
- Stop failure is a warning, not a fatal: the user's session has already
  ended cleanly; we don't want to mask the run's exit code with a podman
  hiccup.
- This semantic is "stop on session end" not "stop on agent exit." The help
  text and CLI.md update reflect that.

**Acceptance Criteria:**

- [ ] `agentbox run --detach-on-exit` then exiting zellij causes `runtime.Stop` to be
  called (verified via fake runtime call recording)
- [ ] `agentbox run` (no flag) does NOT call `runtime.Stop`
- [ ] `agentbox run --detach-on-exit --no-attach` exits 2 with a clear error
- [ ] `runtime.Stop` failure prints a warning to stderr but doesn't change the
  command's exit code
- [ ] `docs/CLI.md` updated to describe the actual semantics
- [ ] The flag's discarding line (`_ = detachOnExit`) is removed

---

### Unit 6: drop `config show --effective`

**Decision:** remove the flag.

The flag was reserved for future per-run overrides (`--network`, `--kits`, etc.)
that would be applied transiently. None of those exist on `config show` today,
and the doc claim ("includes CLI-flag overrides as if a `run` were happening
now") doesn't reflect reality. Removing the flag is honest; if we later need
the feature, we can add it back with real implementation.

**File:** `internal/cli/config.go` (modify)

```go
func newConfigShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Print the merged configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := loadConfig()
			if err != nil {
				return err
			}
			cfg := res.Config
			if global.JSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(cfg)
			}
			return toml.NewEncoder(cmd.OutOrStdout()).Encode(cfg)
		},
	}
	return cmd
}
```

(Remove the `effective` var, the flag declaration, and the `_ = effective` line.)

**Implementation Notes:**

- `loadConfig()` already applies `--config`, `--no-project-config`, and
  `--runtime` to the returned config. So `config show` already reflects
  global flag overrides — no functionality is lost by removing `--effective`.
- Users who pass `--effective` after this change will get cobra's "unknown
  flag" error (exit 2). This is the desired failure mode: clearer than silent
  acceptance.

**Acceptance Criteria:**

- [ ] `agentbox config show --effective` exits 2 with "unknown flag: --effective"
- [ ] `agentbox config show` continues to work and reflects `--runtime`/`--config`
  overrides via `loadConfig`
- [ ] `docs/CLI.md` no longer documents `--effective`

---

### Unit 7: `agentbox ls` empty-state hint

**File:** `internal/cli/ls.go` (modify)

```go
boxes, err := l.Ls(filter)
if err != nil {
	return err
}

if global.JSON {
	enc := json.NewEncoder(cmd.OutOrStdout())
	for _, b := range boxes {
		_ = enc.Encode(b) // NDJSON: one record per line, even when zero
	}
	return nil
}

if len(boxes) == 0 {
	info(cmd, "no boxes")
	return nil
}
return printLsTable(cmd.OutOrStdout(), boxes)
```

**Implementation Notes:**

- `--json` with zero boxes emits zero lines (not `[]`) — matching CLAUDE.md's
  NDJSON contract. The empty-state hint is human-only.
- The hint goes to stderr via `info()` so `agentbox ls | wc -l` still returns
  0 (no lines) when there are no boxes.

**Acceptance Criteria:**

- [ ] `agentbox ls` with no boxes prints `no boxes` to stderr
- [ ] `agentbox ls -q` with no boxes prints nothing
- [ ] `agentbox ls --json` with no boxes prints zero lines to stdout
- [ ] `agentbox ls` with boxes prints the table to stdout (unchanged)

---

### Unit 8: `agentbox rm` success messages

**File:** `internal/lifecycle/lifecycle.go` (modify)

```go
func (l *Lifecycle) rmOne(projID string, keepState bool) error {
	if err := l.Runtime.Rm(container.ContainerName(projID), true); err != nil {
		return exitcode.Wrap(exitcode.Generic, err)
	}
	if l.Network != nil {
		spec := l.Network.SpecFor(l.Cfg, projID)
		if err := l.Network.Teardown(spec); err != nil {
			fmt.Fprintf(l.Stderr, "warning: network teardown for %s: %v\n", projID, err)
		}
	}
	if !keepState {
		if err := state.RemoveSession(projID); err != nil {
			return exitcode.Wrap(exitcode.Generic, err)
		}
	}
	if !l.Quiet {
		fmt.Fprintf(l.Stderr, "removed %s\n", projID)
	}
	return nil
}

func (l *Lifecycle) rmAll(force, keepState bool) error {
	// ...existing logic, capturing successful removals...
	var firstErr error
	removed := 0
	for _, b := range userBoxes {
		if err := l.rmOne(b.ProjectID, keepState); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		removed++
	}
	if !l.Quiet && removed > 0 {
		fmt.Fprintf(l.Stderr, "removed %d box(es)\n", removed)
	}
	return firstErr
}
```

**Implementation Notes:**

- `rmOne` already prints `removed <id>` per box; `rmAll` adds a summary line
  AFTER the per-box lines so a `rm --all` of three boxes shows:
  ```
  removed a3f2c1d4e5b6
  removed b8d4e7a1c2f3
  removed c1a9f30b4d5e
  removed 3 box(es)
  ```
- These go to stderr (CLAUDE.md pattern: stdout reserved for parseable output;
  stderr for status). `agentbox rm --all > /dev/null 2>&1` still works as a
  silencer regardless.
- `--quiet` suppresses both the per-box lines and the summary.
- The summary counts SUCCESSFUL removals only; failures are reported through
  the returned error.

**Acceptance Criteria:**

- [ ] `agentbox rm <id>` prints `removed <id>` to stderr on success
- [ ] `agentbox rm --all` prints one `removed <id>` per box plus a final
  `removed N box(es)` summary
- [ ] `agentbox rm <id> -q` prints nothing on success
- [ ] `agentbox rm <id>` failure does NOT print "removed"
- [ ] `agentbox rm --all` partial failure (some boxes removed, one errors) prints
  the successes and the summary count of successes; returns the first error

---

### Unit 9: documentation reconciliation

**File:** `docs/CLI.md` (modify)

Specific changes (search-and-replace):

1. **`agentbox shell` flags table** (line ~130) — add `--fresh`:
   ```
   | `--fresh`     | Remove any existing box for this project before creating. |
   ```

2. **`agentbox run --detach-on-exit`** (line ~85) — update help to match
   "session end" semantics:
   ```
   | `--detach-on-exit`  | Stop the container when this session ends (default: keep running for `agentbox attach`). |
   ```

3. **`agentbox rm` flags table** (line ~224) — clarify `--force`:
   ```
   | `--force`   | Skip the y/N confirmation prompt when using `--all`. Required when stdin is not a TTY. |
   ```

4. **`agentbox config show`** (line ~363) — drop `--effective`:
   ```
   | `show`     | Print the merged configuration. |
   ```

5. **Add `--dry-run` examples** under `agentbox rm`:
   ```sh
   agentbox rm --all --dry-run         # print the podman commands without running them
   ```

   Under `agentbox build`:
   ```sh
   agentbox build polyglot,claude --dry-run   # print podman pull/build commands without running them
   ```

6. **Global flags table** (line ~36) — clarify `--quiet`:
   ```
   | `--quiet, -q` | false | Suppress informational messages. Errors and primary output (table/JSON/dry-run shell) are not affected. |
   ```

**Implementation Notes:**

- This unit is doc-only. It runs LAST in implementation order so all other
  units' behavior is settled before docs are aligned.
- No `docs/SPEC.md` changes — SPEC describes the runtime semantics; CLI.md
  is the user-facing surface.

**Acceptance Criteria:**

- [ ] Each item above appears verbatim (or close enough) in `docs/CLI.md`
- [ ] No section of `docs/CLI.md` describes a flag that doesn't exist in code
- [ ] No CLI flag exists that isn't documented in `docs/CLI.md`

---

## Testing

### Test layout

| Unit | Test file                                  | New helpers? |
| ---- | ------------------------------------------ | ------------ |
| 1    | `internal/cli/output_test.go` (new)        | none         |
| 2    | `internal/lifecycle/rm_dryrun_test.go` (new), `internal/cli/cli_test.go` (extend) | none |
| 3    | `internal/cli/build_dryrun_test.go` (new)  | one helper to check no `Builder.Build` call |
| 4    | spread across `cli_test.go`, `lifecycle_test.go` | none         |
| 5    | `internal/lifecycle/lifecycle_test.go` (extend)  | none         |
| 6    | `internal/cli/cli_test.go` (extend)        | none         |
| 7    | `internal/cli/cli_test.go` (extend)        | none         |
| 8    | `internal/lifecycle/lifecycle_test.go` (extend)  | none         |

All tests reuse the existing test seams: `runCmd(t, args...)`,
`setupFakeLifecycle(t, rt)`, `lifecycle.SetStdinIsTerminal(...)`, the
`fakeRuntime.calls` recording, and `fakeRuntime.boxes` pre-population.

### Key test cases

**Unit 1 (output helpers):**

```go
func TestInfo_QuietSuppresses(t *testing.T)        // global.Quiet=true → no output
func TestInfo_NonQuietWritesToStderr(t *testing.T) // global.Quiet=false → cmd.ErrOrStderr()
func TestResult_AlwaysWrites(t *testing.T)         // global.Quiet=true → still prints
func TestLifecycleQuiet_GatesRunLivenessPrint(t *testing.T) // l.Quiet=true → silent
```

**Unit 2 (rm dry-run):**

```go
func TestRm_DryRun_RendersStopAndRm(t *testing.T)            // captures stdout, asserts substrings
func TestRm_DryRun_DoesNotCallRuntime(t *testing.T)          // asserts !containsCall(rt.calls, "Rm")
func TestRm_DryRun_DoesNotPrompt(t *testing.T)               // l.Stdin not consumed even without --force
func TestRm_DryRun_KeepStateOmitsRmRf(t *testing.T)          // --keep-state → no `rm -rf` line
func TestRm_DryRun_AllEnumeratesAllBoxes(t *testing.T)       // multi-box --all coverage
func TestRm_DryRun_DockerRuntime(t *testing.T)               // `docker rm` not `podman rm`
```

**Unit 3 (build dry-run):**

```go
func TestBuild_DryRun_PullEligiblePrintsPullAndTag(t *testing.T)
func TestBuild_DryRun_NoPullPrintsBuildOnly(t *testing.T)
func TestBuild_DryRun_DoesNotCallRunner(t *testing.T)
func TestBuild_DryRun_MutuallyExclusiveWithPrint(t *testing.T)  // exit 2
func TestBuild_DryRun_OutputIsBashSafe(t *testing.T)            // every line `#`-prefixed or a command
```

**Unit 4 (--quiet):**

```go
func TestRun_NoAttach_QuietSuppressesLivenessPrint(t *testing.T)
func TestRun_NoAttach_NonQuietPrintsLiveness(t *testing.T)
func TestBuild_QuietSuppressesBuiltLine(t *testing.T)
func TestDoctor_QuietPrintsOnlyFails(t *testing.T)
func TestLs_QuietStillPrintsTable(t *testing.T)        // result, not chatter
func TestErrors_NotSuppressedByQuiet(t *testing.T)     // error to stderr regardless
```

**Unit 5 (--detach-on-exit):**

```go
func TestRun_DetachOnExit_StopsContainer(t *testing.T)        // rt.calls contains "Stop"
func TestRun_NoDetachOnExit_DoesNotStop(t *testing.T)
func TestRun_DetachOnExit_StopFailureLogsWarning(t *testing.T) // exit code unchanged
func TestRun_DetachOnExit_NoAttachIsRejected(t *testing.T)    // exit 2
```

**Unit 6 (drop --effective):**

```go
func TestConfigShow_EffectiveFlagRejected(t *testing.T)       // exit 2 with "unknown flag"
func TestConfigShow_StillRespectsGlobalFlags(t *testing.T)    // --runtime override visible
```

**Unit 7 (ls empty-state):**

```go
func TestLs_EmptyPrintsHintToStderr(t *testing.T)
func TestLs_EmptyJSONPrintsZeroLines(t *testing.T)
func TestLs_EmptyQuietPrintsNothing(t *testing.T)
```

**Unit 8 (rm success messages):**

```go
func TestRm_OnePrintsRemoved(t *testing.T)
func TestRm_AllPrintsPerBoxAndSummary(t *testing.T)
func TestRm_QuietSuppressesSuccessMessages(t *testing.T)
func TestRm_FailureDoesNotPrintRemoved(t *testing.T)
```

### Assertion patterns

Reuse existing patterns documented in the test inventory:

- `strings.Contains(out, "...")` for substring checks on shell rendering
- `json.Unmarshal([]byte(line), &v)` for NDJSON line checks
- `containsCall(rt.calls, "Stop")` for fake-runtime call assertions
- `lifecycle.SetStdinIsTerminal(func() bool { return true })` + `l.Stdin = strings.NewReader("y\n")` for prompt tests

No new test helpers required.

---

## Implementation Order

Each step is independently committable. Steps build on each other in the order
listed; later units assume earlier units' types and helpers are present.

1. **Unit 1** — `cli.info`/`cli.result` + `lifecycle.Lifecycle.Quiet` field +
   factory wiring. Foundation for Unit 4, 7, 8.
2. **Unit 2** — `agentbox rm --dry-run`. Self-contained.
3. **Unit 3** — `agentbox build --dry-run`. Self-contained.
4. **Unit 5** — `--detach-on-exit` implementation. Self-contained, independent
   of Quiet wiring.
5. **Unit 6** — drop `config show --effective`. Self-contained, two-line change.
6. **Unit 4** — Per-command `--quiet` wiring. Depends on Unit 1.
7. **Unit 7** — `agentbox ls` empty-state hint. Depends on Unit 1.
8. **Unit 8** — `agentbox rm` success messages. Depends on Unit 1.
9. **Unit 9** — `docs/CLI.md` reconciliation. Depends on all behavior changes
   above being final.

This ordering keeps Unit 1's diff small (just helpers + a struct field +
factory wiring), then sequences mutating-command dry-run before
output-shaping. Doc updates last so they reflect the shipped behavior.

## Verification Checklist

After each unit lands, the project's standard verification:

```sh
go build ./...
go vet ./...
go test ./...
```

After Unit 9 (everything done), manual smoke:

```sh
# Unit 2 — rm dry-run
agentbox run --no-attach
agentbox rm --all --dry-run    # should print podman commands, no boxes removed
agentbox ls                    # boxes still present

# Unit 3 — build dry-run
agentbox build polyglot,claude --dry-run    # podman commands, no build

# Unit 4 — quiet
agentbox run --no-attach -q                  # silent on success
agentbox run --no-attach -q 2>&1 | wc -c     # 0 bytes
agentbox ls -q                                # still prints table (result, not chatter)

# Unit 5 — detach on exit
agentbox run --detach-on-exit                # exit zellij, container should be Stopped
agentbox ls --all                             # box shows Exited

# Unit 6 — effective flag dropped
agentbox config show --effective              # exits 2, "unknown flag"

# Unit 7 — ls empty
agentbox rm --all --force
agentbox ls                                   # prints "no boxes" to stderr
agentbox ls --json                            # zero lines on stdout

# Unit 8 — rm success
agentbox run --no-attach
agentbox rm .                                  # prints "removed <id>"
```

Update `docs/CLI.md` (Unit 9) and confirm:

```sh
grep -n "detach-on-exit\|--effective\|--fresh" docs/CLI.md   # expectations match implementation
```
