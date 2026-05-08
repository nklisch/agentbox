# Design: claude-mode integration (`agentbox run --mode`)

## Overview

Bake [claude-mode](https://github.com/nklisch/claude-code-modes) into the
existing `claude` kit and add an opt-in `--mode <preset>` flag to `agentbox
run` so the user can launch Claude Code with a behaviorally-tuned system
prompt without leaving the box.

The change is small and surgical:

- One installer line appended to `internal/builtinkits/kits/claude/install.sh`
  that runs claude-mode's official `curl … | sh` one-liner with
  `CLAUDE_MODE_INSTALL=/usr/local/bin` so the binary lands on the system
  PATH inside the box.
- One new flag — `--mode <preset>` — on `agentbox run` (RunOpts.Mode →
  CLI flag → lifecycle.Run → dry-run path).
- One new pure helper — `lifecycle.BuildAgentCmd(base []string, mode
  string) ([]string, error)` — that rewrites the resolved
  `Agents[<name>].Cmd` slice when `--mode` is set. With `--mode` empty
  the helper is a no-op and the agent invocation stays
  byte-identical to today's `["claude", "--dangerously-skip-permissions"]`.
- Validation: `--mode` is only legal when the resolved agent's `Cmd[0] ==
  "claude"`. Pairing it with `codex`, `opencode`, or any other agent
  exits with `exitcode.InvalidArgs` and a message naming the resolved
  command.

No new kit, no new config field, no schema change, no impact on
`agentbox build` cache tags beyond the natural rebuild that follows
editing `kits/claude/install.sh`. Layout system, trail wiring, network
modes, and registry pull paths are untouched.

The user's original wording mentioned an `--model` flag; clarification
in the design conversation confirmed the intent is a single `--mode`
flag for picking a claude-mode preset (`create`, `safe`, `refactor`,
…). No model-selection flag is in scope for this design.

## Pre-design verification

Per CLAUDE.md's "watch out for stale training data" rule, the following
surfaces were verified at design time against the upstream sources
checked into `../claude-code-modes`:

- **claude-code-modes installer**
  (`../claude-code-modes/install.sh`, read 2026-05-07): POSIX `sh`
  script. Detects `linux|darwin` × `x64|arm64`, fetches the latest
  release tag from `api.github.com/repos/nklisch/claude-code-modes/releases/latest`,
  downloads `claude-mode-<os>-<arch>` plus `checksums.txt`, verifies
  SHA-256 (`sha256sum` or `shasum`), `chmod +x`, drops macOS quarantine
  xattr (no-op on linux), prints a PATH-amend hint when `INSTALL_DIR`
  isn't on PATH. Honors `CLAUDE_MODE_INSTALL=<dir>` to override the
  default `$HOME/.local/bin` install prefix. Requires `curl` *or*
  `wget`. **No version-pinning hook** — the script always pulls the
  latest release. (Same trade-off the current `claude` kit accepts via
  `CLAUDE_VERSION="${CLAUDE_VERSION:-latest}"`.)
- **claude-mode CLI surface** (`../claude-code-modes/README.md`,
  `../claude-code-modes/SPEC.md`, read 2026-05-07): `claude-mode
  <preset> [flags] [-- claude-flags]`. Built-in presets: `create`,
  `extend`, `safe`, `refactor`, `explore`, `debug`, `methodical`,
  `director`, `partner`, `none`. Flags not recognized by claude-mode
  are forwarded verbatim to `claude`, so `claude-mode create
  --dangerously-skip-permissions` runs `claude
  --dangerously-skip-permissions` under a tuned system prompt.
  `--system-prompt` and `--system-prompt-file` are intercepted and
  rejected (irrelevant for our wiring; we never pass them).
- **`base` kit packages** (`internal/builtinkits/kits/base/packages.txt`,
  spot-checked 2026-05-07): `curl` is preinstalled by the dockerfile
  preamble (`internal/kits/dockerfile.go:22`), so the installer's
  HTTPS fetches work without the `claude` kit pulling extra apt
  packages. `sha256sum` ships in debian's `coreutils` (preinstalled in
  `debian:bookworm-slim`).
- **Existing `claude` kit installer pattern**
  (`internal/builtinkits/kits/claude/install.sh`): pinning convention
  is `FOO_VERSION="${FOO_VERSION:-latest}"`, `set -euo pipefail`,
  `command -v <bin>` post-install verification. We mirror this
  exactly for the claude-mode block.

If claude-code-modes' install.sh schema changes between design and
implementation (a new flag, a renamed env var, a different download
host), the implementer fixes the kit installer accordingly and notes
the bump in the PR.

## Implementation Units

### Unit 1: claude-mode install step in the `claude` kit

**File**: `internal/builtinkits/kits/claude/install.sh` (edit)

Append a self-contained block after the existing `command -v claude`
verification. The block:

1. Honors a `CLAUDE_MODE_VERSION` env var for forward-compat (the
   upstream installer doesn't support pinning today, so the variable
   is read but only logged for now — keeps the symmetry with
   `CLAUDE_VERSION` and gives us a clean place to wire pinning if
   upstream adds it).
2. Sets `CLAUDE_MODE_INSTALL=/usr/local/bin` so the binary lands on a
   PATH directory accessible to all in-box users (root) and is shadowed
   by no per-user install.
3. Pipes the upstream installer through `sh`. `set -euo pipefail` from
   the parent script means the build aborts if the install or
   checksum fails.
4. Verifies `claude-mode` is on PATH after install.

```bash
#!/usr/bin/env bash
# claude kit installer: @anthropic-ai/claude-code via npm + claude-mode binary.
# Verified: package name @anthropic-ai/claude-code, binary `claude`.
# Latest version at write time: 2.1.128 (npm view @anthropic-ai/claude-code, 2026-05-05).
# YOLO flag: --dangerously-skip-permissions (confirmed present in this release).
# claude-mode: https://github.com/nklisch/claude-code-modes — installer verified 2026-05-07.
set -euo pipefail

CLAUDE_VERSION="${CLAUDE_VERSION:-latest}"
CLAUDE_MODE_VERSION="${CLAUDE_MODE_VERSION:-latest}"

echo "Installing @anthropic-ai/claude-code@${CLAUDE_VERSION}..."
npm install -g "@anthropic-ai/claude-code@${CLAUDE_VERSION}"

# Verify the binary is reachable on PATH.
# npm globals install to /usr/local/bin when node is installed via NodeSource (node kit).
command -v claude >/dev/null || { echo "claude binary not on PATH after install" >&2; exit 1; }

echo "claude kit install complete"

# --- claude-mode (system-prompt wrapper for claude) ---
# Upstream installer downloads a single static binary into CLAUDE_MODE_INSTALL,
# verifies SHA-256 against the release's checksums.txt, and exits non-zero on
# any failure. Upstream does not yet honor a version env var; we read
# CLAUDE_MODE_VERSION for forward-compat and log it for traceability.
echo "Installing claude-mode (CLAUDE_MODE_VERSION=${CLAUDE_MODE_VERSION})..."
CLAUDE_MODE_INSTALL=/usr/local/bin \
  curl -fsSL https://raw.githubusercontent.com/nklisch/claude-code-modes/main/install.sh | sh

command -v claude-mode >/dev/null || { echo "claude-mode binary not on PATH after install" >&2; exit 1; }

echo "claude-mode install complete"
```

**Implementation Notes**:

- Do **not** change `manifest.toml`, `packages.txt`, or `env.sh` —
  the binary is on PATH already and needs no env vars.
- The build succeeds even if the `agentbox-net` is in a `safe` /
  `allowlist` mode at *runtime*, because kit installers run at
  *build* time when the box doesn't exist yet — full network access
  is fine. The two hosts touched (`api.github.com`,
  `github.com`/`objects.githubusercontent.com`) are reachable from
  default debian DNS during build.
- The post-install `command -v` check is the contract. If the
  upstream installer ever changes its install path or the binary
  name, this check fails the build — exactly what we want.

**Acceptance Criteria**:

- [ ] `agentbox build polyglot,claude --no-cache` completes
  successfully (kit hash differs from the prior built image — that's
  the expected cache-bust).
- [ ] `agentbox run --no-attach && agentbox exec . -- which claude-mode`
  prints `/usr/local/bin/claude-mode`.
- [ ] `agentbox exec . -- claude-mode --version` prints a version line
  (release builds: single `claude-mode <semver>` line).
- [ ] `agentbox exec . -- claude-mode explore --print | head -5`
  outputs the assembled system prompt without requiring an Anthropic
  API key (claude-mode's `--print` short-circuits before spawning
  claude).
- [ ] `agentbox build --print polyglot,claude` shows the claude kit's
  install command unchanged in shape (one `RUN bash -e
  /tmp/kit-claude/install.sh` line per the existing dockerfile
  generator); no new `COPY` or `RUN` directives are emitted by
  `internal/kits/dockerfile.go`.

---

### Unit 2: `lifecycle.RunOpts.Mode`

**File**: `internal/lifecycle/lifecycle.go` (edit, lines 325–336)

Add a single new field. Place it adjacent to `Layout` since both are
"how the agent runs" knobs.

```go
// RunOpts controls the Run command.
type RunOpts struct {
    Agent        string
    Kits         []string
    Fresh        bool
    Attach       bool
    Network      string // override Cfg.Network.Mode for this run
    NoZellij     bool   // skip zellij and use bare-shell exec (only meaningful for Shell)
    Layout       string // --layout flag value; empty falls back to cfg.Zellij.Layout
    Mode         string // --mode flag: claude-mode preset (create|safe|...). Empty = no claude-mode wrapping. Claude-only.
    NoPull       bool   // skip the registry pull attempt; build locally
    DetachOnExit bool   // stop the container after the user's session ends
}
```

**Implementation Notes**:

- New field is a plain `string`. Empty string is the
  no-claude-mode default and matches today's behavior byte-for-byte.
- Documented as "claude-only" in the field comment so future
  RunOpts callers don't gain the wrong mental model. Validation
  happens in `BuildAgentCmd` (Unit 3).

**Acceptance Criteria**:

- [ ] Existing `RunOpts{}` zero values still produce the current
  `["claude", "--dangerously-skip-permissions"]` invocation in
  every existing lifecycle test.

---

### Unit 3: `lifecycle.BuildAgentCmd` helper

**File**: `internal/lifecycle/agentcmd.go` (new)

Extract the agent-command transformation into a pure helper. Pure
functions are easy to test, easy to reuse from `runDryRun`, and
keep `Run`/`Shell` slim.

```go
package lifecycle

import (
    "github.com/nklisch/agentbox/internal/exitcode"
)

// BuildAgentCmd returns the final argv that should be launched in the
// agent pane, applying the --mode flag transformation when set.
//
// When mode is empty, the input is returned unchanged (a fresh copy —
// the caller never mutates the agent config slice).
//
// When mode is non-empty, the function rewrites:
//
//     ["claude", arg1, arg2, ...]   →   ["claude-mode", mode, arg1, arg2, ...]
//
// `--mode` is only legal when base[0] == "claude". Pairing it with any
// other binary returns exitcode.InvalidArgs naming the resolved
// command, so the user sees a clear failure at the host CLI rather
// than a "claude-mode: command not found" deep inside the box (we
// only install claude-mode in the claude kit).
//
// The mode value itself is NOT validated against the built-in preset
// list — claude-mode supports user-defined presets via .claude-mode.json
// inside the box, so any non-empty string is forwarded as-is and
// claude-mode rejects unknown names with its own clear error.
func BuildAgentCmd(base []string, mode string) ([]string, error) {
    out := append([]string(nil), base...) // defensive copy
    if mode == "" {
        return out, nil
    }
    if len(base) == 0 || base[0] != "claude" {
        var resolved string
        if len(base) == 0 {
            resolved = "<empty>"
        } else {
            resolved = base[0]
        }
        return nil, exitcode.New(exitcode.InvalidArgs,
            "--mode is only supported for the claude agent (resolved command: %q)",
            resolved)
    }
    // Replace base[0] ("claude") with two tokens: "claude-mode" and the preset.
    // Keep the remaining args (typically --dangerously-skip-permissions).
    out = append([]string{"claude-mode", mode}, base[1:]...)
    return out, nil
}
```

**Implementation Notes**:

- Lives in the `lifecycle` package so it can return
  `*exitcode.Err` directly without an extra wrap layer at the
  caller. (Per `.claude/skills/patterns/exitcode.md`: lifecycle
  layer adds codes, leaf packages don't.)
- Always returns a fresh slice — never aliases `base` — so the
  caller can mutate the result (e.g., append `--model` later if
  that flag is added) without surprising the agent config map.
- Does **not** validate the preset name. Two reasons:
  (1) claude-mode supports user-defined presets via project- or
  global-level `.claude-mode.json`, which agentbox can't see from
  the host; (2) keeping the host CLI dumb keeps the source of
  truth on preset semantics in claude-mode itself.
- The error message includes the *resolved binary name* so users
  with a custom `[agents.foo]` Cmd quickly understand why it
  failed.

**Acceptance Criteria**:

- [ ] `BuildAgentCmd([]string{"claude","--dangerously-skip-permissions"}, "")` returns
  `[]string{"claude","--dangerously-skip-permissions"}`, no error, slice is a fresh allocation.
- [ ] `BuildAgentCmd([]string{"claude","--dangerously-skip-permissions"}, "create")`
  returns `[]string{"claude-mode","create","--dangerously-skip-permissions"}`, no error.
- [ ] `BuildAgentCmd([]string{"claude"}, "safe")` returns
  `[]string{"claude-mode","safe"}`, no error.
- [ ] `BuildAgentCmd([]string{"codex","--dangerously-bypass-..."}, "create")`
  returns `nil` slice and an `*exitcode.Err` with `Code ==
  exitcode.InvalidArgs` whose message contains `"claude"` and `"codex"`.
- [ ] `BuildAgentCmd(nil, "create")` returns the same error class
  with message containing `<empty>`.
- [ ] Mutating the returned slice does not mutate the input
  `base` slice (defensive-copy contract).

---

### Unit 4: Apply `BuildAgentCmd` in `Lifecycle.Run`

**File**: `internal/lifecycle/lifecycle.go` (edit, around lines 363–410)

Two call-site edits in `Run`. Both `writeLayoutFor` calls (the
pre-EnsureBox write and the post-EnsureBox refresh) already pass
`a.Cmd`; replace with the transformed slice.

```go
func (l *Lifecycle) Run(opts RunOpts) error {
    if opts.Network != "" {
        l.Cfg.Network.Mode = opts.Network
    }

    home, _ := os.UserHomeDir()
    layoutName := opts.Layout
    if layoutName == "" {
        layoutName = l.Cfg.Zellij.Layout
    }
    spec, err := zellij.Resolve(layoutName, home)
    if err != nil {
        return exitcode.Wrap(exitcode.InvalidArgs, err)
    }

    // Resolve agent config to get the base command for the layout.
    agent := l.Cfg.DefaultAgent
    if opts.Agent != "" {
        agent = opts.Agent
    }
    a, ok := l.Cfg.Agents[agent]
    if !ok {
        return exitcode.New(exitcode.InvalidArgs, "agent %q not defined", agent)
    }

    // Apply --mode rewrite. BuildAgentCmd validates the agent is "claude"
    // when mode is non-empty and returns *exitcode.Err on failure.
    agentCmd, err := BuildAgentCmd(a.Cmd, opts.Mode)
    if err != nil {
        return err
    }

    // Pre-EnsureBox layout write (macOS Podman virtiofs cache fix; see existing comment).
    projID, projAbs, perr := project.Resolve()
    if perr != nil {
        return exitcode.Wrap(exitcode.Generic, perr)
    }
    if err := writeLayoutFor(projID, projAbs, spec, zellij.ModeRun, agentCmd, l.Cfg.Shell.Shell, ""); err != nil {
        return exitcode.Wrap(exitcode.Generic, err)
    }

    l.pendingLayoutName = spec.Name
    box, err := l.EnsureBox(EnsureOpts{
        Agent: opts.Agent, Kits: opts.Kits, Fresh: opts.Fresh, NoPull: opts.NoPull,
    })
    l.pendingLayoutName = ""
    if err != nil {
        return err
    }

    // Refresh write (existing behavior — see existing comment about box.CWD canonicality).
    if err := writeLayoutFor(box.ProjectID, box.CWD, spec, zellij.ModeRun, agentCmd, l.Cfg.Shell.Shell, ""); err != nil {
        return exitcode.Wrap(exitcode.Generic, err)
    }

    // ... unchanged attach / DetachOnExit branches below ...
}
```

**Implementation Notes**:

- `BuildAgentCmd` runs *after* layout resolution and agent
  resolution but *before* the first `writeLayoutFor`. This means
  an invalid `--mode codex` invocation fails before any state
  directory is touched — clean rollback.
- The variable shadows are intentional. `agentCmd` is the
  transformed slice; we never hand `a.Cmd` directly to
  `writeLayoutFor` after this point.
- `Shell` (the bare-shell command) does not call `BuildAgentCmd`.
  `agentbox shell` doesn't run an agent, so `--mode` would be
  meaningless there; the flag is not exposed on the shell command.

**Acceptance Criteria**:

- [ ] Calling `Lifecycle.Run(RunOpts{Mode:"create"})` with the
  default claude agent produces a `<state>/layout.kdl` whose agent
  pane contains `command "box-agent"` followed by `args
  "claude-mode" "create" "--dangerously-skip-permissions"`. (Hint:
  the existing `box-agent` wrapper at
  `internal/zellij/kdl.go:92` prepends `box-agent` regardless of
  what the agent command starts with — the prepend behavior is
  preserved.)
- [ ] `Lifecycle.Run(RunOpts{Mode:"create", Agent:"codex"})` (when
  `cfg.Agents["codex"]` exists) returns `*exitcode.Err{Code:
  exitcode.InvalidArgs}` with no `<state>/layout.kdl` written —
  error path runs before the first `writeLayoutFor`.
- [ ] `Lifecycle.Run(RunOpts{})` (mode unset) produces a
  byte-identical `layout.kdl` to today's output — confirmed by
  `TestRun_FocusClaude_NoTrailMount`-style golden assertion.

---

### Unit 5: Apply `BuildAgentCmd` in dry-run

**File**: `internal/cli/run.go` (edit, function `runDryRun`, lines 85–184)

The dry-run output today does not surface the agent command — it
prints `podman create …` only. With `--mode`, that's a regression
in clarity (the user can't preview what the agent pane will run).
We add two things:

1. A `# mode = <preset>` header line, printed after `# kits = …`
   when `--mode` is set. Mirrors the existing `# layout = …`
   header style.
2. A validation pass that runs `BuildAgentCmd` and propagates its
   error before the create-args render. This makes `--dry-run`
   reject invalid `--mode codex` exactly the way the live path
   does — same error, same exit code, no extra state.

```go
func runDryRun(cmd *cobra.Command, cfg configResult, args []string,
    kitsFlag, networkFlag, layoutFlag, modeFlag string) error {
    c := cfg.Config
    // ... existing network override + Validate block ...

    agent := c.DefaultAgent
    if len(args) == 1 {
        agent = args[0]
    }
    a, ok := c.Agents[agent]
    if !ok {
        return exitcode.New(exitcode.InvalidArgs, "agent %q not defined in [agents.*]", agent)
    }

    // Validate --mode before any other work so dry-run errors mirror live-run.
    if _, err := lifecycle.BuildAgentCmd(a.Cmd, modeFlag); err != nil {
        return err
    }

    // ... existing kit resolution + project resolution + layout resolution ...

    fmt.Fprintf(cmd.OutOrStdout(), "# project_id = %s\n", id)
    fmt.Fprintf(cmd.OutOrStdout(), "# kits = %s\n", strings.Join(kitList, ","))
    if !sameSlice(kitList, resolved.Names()) {
        fmt.Fprintf(cmd.OutOrStdout(), "# resolved = %s\n", strings.Join(resolved.Names(), ","))
    }
    fmt.Fprintf(cmd.OutOrStdout(), "# network = %s\n", c.Network.Mode)
    if modeFlag != "" {
        fmt.Fprintf(cmd.OutOrStdout(), "# mode = %s\n", modeFlag)
    }
    if spec.Kind == zellij.LayoutCustom {
        fmt.Fprintf(cmd.OutOrStdout(), "# layout = %s (%s, %s)\n", spec.Name, spec.Kind, spec.Path)
    } else {
        fmt.Fprintf(cmd.OutOrStdout(), "# layout = %s (%s)\n", spec.Name, spec.Kind)
    }
    fmt.Fprint(cmd.OutOrStdout(), rs.ToShell(c.Runtime))
    return nil
}
```

**Implementation Notes**:

- The `# mode = …` header is omitted when `--mode` is empty,
  matching the convention used for `# resolved = …` (only
  printed when meaningful).
- The runspec.PodmanCreateArgs output is unchanged. `--mode`
  doesn't affect the container create call — it only affects the
  agent pane command, which lives in `<state>/layout.kdl` and
  isn't part of dry-run today. Rather than start dumping layout
  KDL into dry-run (a much bigger UX change), the `# mode = …`
  header is the minimal viable signal.
- The validation call uses `lifecycle.BuildAgentCmd` — same
  helper, same error — so dry-run can never "succeed" on an
  input that the live path would reject.

**Acceptance Criteria**:

- [ ] `agentbox run --mode create --dry-run` exits 0 and stdout
  contains a `# mode = create` line between `# network = …` and
  `# layout = …`.
- [ ] `agentbox run codex --mode create --dry-run` (with codex
  defined in config) exits 2 and stderr contains
  `--mode is only supported for the claude agent` and
  `"codex"`.
- [ ] `agentbox run --dry-run` (mode unset) produces output
  byte-identical to today's `TestRunDryRun` expectations. The
  existing `TestRunDryRun` continues to pass without
  modification.

---

### Unit 6: `--mode` flag on `agentbox run`

**File**: `internal/cli/run.go` (edit, function `newRunCmd`, lines 23–82)

Add the flag, propagate to `RunOpts`, thread through to dry-run.

```go
func newRunCmd() *cobra.Command {
    var (
        fresh        bool
        noPull       bool
        kitsFlag     string
        networkFlag  string
        layoutFlag   string
        modeFlag     string
        noAttach     bool
        detachOnExit bool
    )
    cmd := &cobra.Command{
        Use:   "run [agent]",
        Short: "Create or attach to the per-project box, launch the configured agent",
        Args:  cobra.MaximumNArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            if global.DryRun {
                res, err := loadConfig()
                if err != nil {
                    return err
                }
                return runDryRun(cmd, res, args, kitsFlag, networkFlag, layoutFlag, modeFlag)
            }

            l, _, err := initLifecycleCmd(cmd)
            if err != nil {
                return err
            }

            if noAttach && detachOnExit {
                return exitcode.New(exitcode.InvalidArgs,
                    "--no-attach and --detach-on-exit are mutually exclusive: "+
                        "--detach-on-exit requires an interactive session to detach from")
            }
            opts := lifecycle.RunOpts{
                Fresh:        fresh,
                NoPull:       noPull,
                Attach:       !noAttach,
                Network:      networkFlag,
                Layout:       layoutFlag,
                Mode:         modeFlag,
                DetachOnExit: detachOnExit,
            }
            if len(args) == 1 {
                opts.Agent = args[0]
            }
            if kitsFlag != "" {
                opts.Kits = strings.Split(kitsFlag, ",")
            }
            return l.Run(opts)
        },
    }
    cmd.Flags().BoolVar(&fresh, "fresh", false, "remove any existing box for this project before creating")
    cmd.Flags().BoolVar(&noPull, "no-pull", false, "skip the registry pull attempt; build locally")
    cmd.Flags().StringVar(&kitsFlag, "kits", "", "override the kit list (comma-separated)")
    cmd.Flags().StringVar(&networkFlag, "network", "", "override network.mode for this run")
    cmd.Flags().StringVar(&layoutFlag, "layout", "",
        "zellij layout name (focus|reviewer|auditor|<custom>); overrides [zellij].layout config")
    cmd.Flags().StringVar(&modeFlag, "mode", "",
        "claude-mode preset (create|extend|safe|refactor|explore|debug|methodical|director|partner|none|<config-defined>); claude agent only")
    cmd.Flags().BoolVar(&noAttach, "no-attach", false, "create/start the box but don't attach")
    cmd.Flags().BoolVar(&detachOnExit, "detach-on-exit", false, "stop the container when this session ends (default: keep running for `agentbox attach`)")
    return cmd
}
```

**Implementation Notes**:

- The flag help string lists the built-in claude-mode presets so
  `agentbox run --help` is a useful reference. The
  `<config-defined>` hint nods at user-defined presets without
  asking the user to install claude-mode just to read its docs.
- The flag is positioned alphabetically (`--mode` after
  `--layout`) in the help output, matching the existing flag
  registration order.
- No mutual-exclusivity checks beyond the existing `--no-attach
  && --detach-on-exit` pair. `--mode` composes cleanly with
  every other flag.

**Acceptance Criteria**:

- [ ] `agentbox run --help` lists `--mode` with the documented
  description.
- [ ] `agentbox run --mode safe --no-attach` propagates
  `Mode:"safe"` into `lifecycle.RunOpts` (verified in CLI test
  via fake lifecycle).
- [ ] `agentbox run --mode "" --no-attach` is treated as
  no-mode (empty string is the documented zero value).
- [ ] `agentbox run --mode create --dry-run` and `agentbox run
  --dry-run --mode create` both produce identical output (cobra
  flag parse order doesn't matter).

---

### Unit 7: Doc updates

**Files**: `docs/CLI.md`, `docs/KITS.md`, `CLAUDE.md`, `docs/features/claude-mode.md` (new short user-facing note)

Three small edits + one new short doc. Skip if the implementer
considers them out of scope for the implementation PR — but the
"watch out for stale training data" rule asks us to surface
agent-CLI knobs explicitly.

1. **`docs/CLI.md`** — add a row to the `agentbox run` flags
   table:

   | Flag | Behavior |
   | ---- | -------- |
   | `--mode <preset>` | Wrap `claude` in `claude-mode <preset>` so the agent runs under a behaviorally-tuned system prompt. Built-in presets: `create`, `extend`, `safe`, `refactor`, `explore`, `debug`, `methodical`, `director`, `partner`, `none`. User-defined presets in `.claude-mode.json` are also accepted. Claude agent only — pairing with another agent exits 2. |

   Plus a short example after the existing `--layout auditor` line:
   `agentbox run --mode safe         # surgical-precision system prompt`.

2. **`docs/KITS.md`** — update the `claude` row in the kit
   catalog:

   | `claude` | `@anthropic-ai/claude-code` (npm) + `claude-mode` static binary (system-prompt wrapper). Implicitly `depends_on = ["base", "node"]`. |

3. **`CLAUDE.md`** — append `claude-mode` to the
   "watch out for stale training data" agent-CLI bullet:

   `Agent CLIs and their YOLO/permission flags (Claude Code,
   Codex, opencode), and the claude-mode wrapper's preset names
   and flag passthrough rules`

4. **`docs/features/claude-mode.md`** (new, ~50 lines) — short
   user-facing companion to this design doc, mirroring the shape
   of `docs/features/zellij-layouts.md`. Lists what `--mode` does
   in one paragraph, points to the upstream repo for preset
   semantics, and notes the kit-rebuild requirement after a
   claude-mode upstream release.

**Implementation Notes**:

- Doc updates are not load-bearing for the feature working —
  they're the difference between "shipped" and "discoverable."
  Implementer can split them into a follow-up PR if the code PR
  gets large.

**Acceptance Criteria**:

- [ ] `agentbox run --help` text agrees with the row in
  `docs/CLI.md`.
- [ ] Reading `docs/KITS.md` straight through, a new user
  understands that the `claude` kit installs both `claude` and
  `claude-mode`.

---

## Implementation Order

1. **Unit 1** — claude kit installer edit. Build the kit locally
   (`agentbox build polyglot,claude --no-cache --no-pull`) and
   verify `claude-mode --version` works in the resulting box. No
   Go code yet; this is the riskiest unit (depends on a network
   resource we don't control), so prove it works first.
2. **Unit 3** — `BuildAgentCmd` helper + unit tests. Pure
   function, no dependencies, easiest to TDD.
3. **Unit 2** — `RunOpts.Mode` field. Single-line struct edit;
   gated by Unit 3 so the code that *uses* the field exists.
4. **Unit 4** — Wire `BuildAgentCmd` into `Lifecycle.Run`. Pull
   in lifecycle-level tests that prove the layout KDL contains
   the expected agent pane command.
5. **Unit 5** — Wire into `runDryRun`. Adds the `# mode = …`
   header and the early-validation path.
6. **Unit 6** — `--mode` flag on the cobra command. CLI plumbing
   only — by this point the lifecycle layer already does the
   work.
7. **Unit 7** — Docs. Last because the help-string text is
   easier to land once the flag is registered and we've felt the
   ergonomics in passing.

Each unit is independently shippable. Stopping after Unit 6
delivers a working feature; Unit 1 stopping point delivers a box
with claude-mode pre-installed but no host-side `--mode` flag
(users can still invoke `agentbox exec . -- claude-mode create`
manually).

## Testing

### Unit Tests: `internal/lifecycle/agentcmd_test.go` (new)

Pure-function tests for `BuildAgentCmd`. Lives next to the helper.

```go
func TestBuildAgentCmd_EmptyMode_ReturnsCopy(t *testing.T)
func TestBuildAgentCmd_ClaudePlusMode_RewritesPrefix(t *testing.T)
func TestBuildAgentCmd_ClaudeOnly_RewritesToTwoTokens(t *testing.T)
func TestBuildAgentCmd_NonClaude_RejectsWithExitcode(t *testing.T)
func TestBuildAgentCmd_EmptyBase_RejectsWithExitcode(t *testing.T)
func TestBuildAgentCmd_DefensiveCopy_DoesNotMutateInput(t *testing.T)
```

Key assertions follow the patterns in
`/Users/nathanklisch/workspace/agentbox/internal/lifecycle/lifecycle_test.go`
(error type via `errors.As(err, &ee)` against
`*exitcode.Err`; check `ee.Code == exitcode.InvalidArgs`).

### Lifecycle integration tests: `internal/lifecycle/lifecycle_test.go` (additions)

Add tests next to the existing `TestRun_FocusClaude_NoTrailMount`
group. These read `<state>/layout.kdl` after `Run` to verify the
agent command rendered into the KDL.

```go
func TestRun_ModeRewritesAgentCmdInLayout(t *testing.T) {
    cr := newCaptureRuntime()
    cfg := defaultTestCfg()
    cfg.Agents["claude"] = config.Agent{
        Kits: []string{"base"},
        Cmd:  []string{"claude", "--dangerously-skip-permissions"},
    }
    projID, _ := setupProject(t)
    isolateState(t)

    l := newCaptureLifecycle(t, cr, cfg)
    err := l.Run(lifecycle.RunOpts{Attach: false, Agent: "claude", Mode: "create"})
    if err != nil { t.Fatalf("Run: %v", err) }

    dir, _ := state.SessionDir(projID)
    kdl, _ := os.ReadFile(state.LayoutPath(dir))
    // box-agent wrapper is preserved; the wrapped command is now claude-mode.
    want := `args "box-agent" "claude-mode" "create" "--dangerously-skip-permissions"`
    if !strings.Contains(string(kdl), want) {
        t.Errorf("layout.kdl missing %q\n--- got ---\n%s", want, string(kdl))
    }
}

func TestRun_ModeWithCodex_Errors(t *testing.T) {
    cr := newCaptureRuntime()
    cfg := defaultTestCfg()
    cfg.Agents["codex"] = config.Agent{
        Kits: []string{"base"},
        Cmd:  []string{"codex", "--dangerously-bypass-approvals-and-sandbox"},
    }
    setupProject(t)
    isolateState(t)

    l := newCaptureLifecycle(t, cr, cfg)
    err := l.Run(lifecycle.RunOpts{Attach: false, Agent: "codex", Mode: "create"})
    var ee *exitcode.Err
    if !errors.As(err, &ee) || ee.Code != exitcode.InvalidArgs {
        t.Fatalf("expected *exitcode.Err{Code:InvalidArgs}, got %T %v", err, err)
    }
    if !strings.Contains(ee.Error(), "claude") || !strings.Contains(ee.Error(), "codex") {
        t.Errorf("error message should reference both 'claude' and 'codex': %s", ee.Error())
    }
    // No layout.kdl should have been written for the failed run.
    // (state/dir.go's EnsureSession is idempotent and harmless if it ran;
    //  the assertion is on Run returning before writeLayoutFor.)
}

func TestRun_NoMode_LayoutUnchanged(t *testing.T) {
    // Regression guard: with Mode:"" the agent pane command is byte-identical
    // to today's output. Catches accidental always-on rewrites.
    cr := newCaptureRuntime()
    cfg := defaultTestCfg()
    cfg.Agents["claude"] = config.Agent{
        Kits: []string{"base"},
        Cmd:  []string{"claude", "--dangerously-skip-permissions"},
    }
    projID, _ := setupProject(t)
    isolateState(t)

    l := newCaptureLifecycle(t, cr, cfg)
    err := l.Run(lifecycle.RunOpts{Attach: false, Agent: "claude"})
    if err != nil { t.Fatalf("Run: %v", err) }

    dir, _ := state.SessionDir(projID)
    kdl, _ := os.ReadFile(state.LayoutPath(dir))
    want := `args "box-agent" "claude" "--dangerously-skip-permissions"`
    if !strings.Contains(string(kdl), want) {
        t.Errorf("layout.kdl missing baseline %q\n--- got ---\n%s", want, string(kdl))
    }
    // Negative: claude-mode tokens must NOT appear.
    if strings.Contains(string(kdl), "claude-mode") {
        t.Errorf("layout.kdl unexpectedly contains 'claude-mode' with Mode unset")
    }
}
```

### CLI tests: `internal/cli/cli_test.go` (additions)

Two new dry-run tests next to `TestRunDryRun`. Use the existing
`runCmd` helper.

```go
func TestRunDryRun_Mode_AddsHeader(t *testing.T) {
    tmp := t.TempDir()
    t.Setenv("XDG_CONFIG_HOME", tmp)
    t.Setenv("XDG_DATA_HOME", tmp)
    orig, _ := os.Getwd()
    if err := os.Chdir(tmp); err != nil { t.Fatalf(...) }
    t.Cleanup(func() { _ = os.Chdir(orig) })

    out, _, err := runCmd(t, "run", "--mode", "create", "--dry-run")
    if err != nil { t.Fatalf("run --mode create --dry-run: %v", err) }
    if !strings.Contains(out, "# mode = create\n") {
        t.Errorf("dry-run output missing '# mode = create' header:\n%s", out)
    }
}

func TestRunDryRun_ModeWithCodex_Errors(t *testing.T) {
    // Configure a codex agent in config, invoke --mode with it, expect exit code 2.
    // (Exact setup mirrors TestRunUnknownAgent at internal/cli/cli_test.go:161.)
    tmp := t.TempDir()
    t.Setenv("XDG_CONFIG_HOME", tmp)
    t.Setenv("XDG_DATA_HOME", tmp)
    // Write a project-local config with a codex agent.
    os.WriteFile(filepath.Join(tmp, ".agentbox.toml"), []byte(`
[agents.codex]
kits = ["polyglot", "codex"]
cmd  = ["codex", "--dangerously-bypass-approvals-and-sandbox"]
`), 0o600)
    orig, _ := os.Getwd()
    if err := os.Chdir(tmp); err != nil { t.Fatalf(...) }
    t.Cleanup(func() { _ = os.Chdir(orig) })

    _, _, err := runCmd(t, "run", "codex", "--mode", "create", "--dry-run")
    var ee *exitcode.Err
    if !errors.As(err, &ee) || ee.Code != exitcode.InvalidArgs {
        t.Fatalf("expected *exitcode.Err{Code:InvalidArgs}, got %T %v", err, err)
    }
}

func TestRunDryRun_NoMode_NoHeader(t *testing.T) {
    // Regression: without --mode, the '# mode = …' line must NOT appear.
    // Existing TestRunDryRun already asserts the positive shape; this one
    // adds a negative assertion that's cheap and catches accidental
    // always-on header emission.
    tmp := t.TempDir()
    t.Setenv("XDG_CONFIG_HOME", tmp)
    t.Setenv("XDG_DATA_HOME", tmp)
    orig, _ := os.Getwd()
    if err := os.Chdir(tmp); err != nil { t.Fatalf(...) }
    t.Cleanup(func() { _ = os.Chdir(orig) })

    out, _, err := runCmd(t, "run", "--dry-run")
    if err != nil { t.Fatalf("run --dry-run: %v", err) }
    if strings.Contains(out, "# mode") {
        t.Errorf("dry-run output unexpectedly contains '# mode' header:\n%s", out)
    }
}
```

### Kit installer test: smoke checkpoint (manual / CI)

The kit installer is shell + network — not unit-testable in Go.
Verification is by the Phase test-checkpoint pattern from
`docs/ROADMAP.md`: a one-liner shell smoke run after the kit
rebuilds.

```sh
agentbox build polyglot,claude --no-cache --no-pull
agentbox run --no-attach
agentbox exec . -- which claude-mode      # expect: /usr/local/bin/claude-mode
agentbox exec . -- claude-mode --version  # expect: claude-mode <semver>
agentbox exec . -- claude-mode explore --print | head -3
agentbox rm .
```

Documented in `docs/PROGRESS.md` as the acceptance line for this
feature.

## Verification Checklist

- [ ] `go build ./...` succeeds.
- [ ] `go vet ./...` is clean.
- [ ] `go test ./internal/lifecycle/... ./internal/cli/...` passes
  with the new tests + all existing tests still green.
- [ ] `agentbox build --print polyglot,claude` shows no new
  Dockerfile directives — only the `kits/claude/install.sh`
  body changed.
- [ ] `agentbox build polyglot,claude --no-cache --no-pull`
  succeeds end-to-end on Linux and (via podman-machine) macOS.
- [ ] In a freshly built box: `agentbox exec . -- claude-mode
  --version` prints a version line.
- [ ] `agentbox run --mode safe --no-attach && agentbox exec .
  --shell "cat ~/.local/share/agentbox/sessions/<id>/layout.kdl"
  | grep -F 'args "box-agent" "claude-mode" "safe"'` succeeds.
  (When run on the host: `cat
  ~/.local/share/agentbox/sessions/<id>/layout.kdl | grep -F
  'args "box-agent" "claude-mode" "safe"'`.)
- [ ] `agentbox run codex --mode create --dry-run` exits 2 with a
  message containing `--mode is only supported for the claude
  agent`.
- [ ] `agentbox run --dry-run` (no `--mode`) produces the same
  byte sequence as before this change. (Diff against a saved
  pre-change `--dry-run` capture.)
- [ ] `agentbox doctor` is unaffected (no new check, no warning).
- [ ] `agentbox build --print-tag polyglot,claude` returns a new
  SHA (kit-content hash differs because `install.sh` changed).
  This is expected; users on first run after upgrade will do one
  local build (or, once `.github/published-kits.yml` triggers a
  new GHCR publish on the next `v*` tag, one registry pull).
