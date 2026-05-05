# Feature: Zellij layout system (`--layout <name>`)

## Summary

Today, `agentbox run` always renders the same hand-coded zellij layout — agent
pane on top + git status pane + btm pane + shell tab. It's a fine default but
there's no way to swap it for a different shape. This feature introduces a
named-layout system with three built-in layouts and user-defined custom
layouts:

- **`focus`** — today's layout, renamed (no behavior change).
- **`reviewer`** — pre-commit posture: agent + live diff vs base branch +
  file-watching test runner.
- **`auditor`** — agent observability: agent + live tool-call activity tail
  rendered from Claude Code hook events.

Users select via `agentbox run --layout <name>` (one-off) or sticky via
`[zellij] layout = "..."` in config. Custom layouts live at
`~/.config/agentbox/layouts/<name>.kdl` with template variables for project
path, shell, and agent command.

The `auditor` layout depends on a new minimal **agent-activity trail**: a
JSONL event stream populated by Claude Code hooks that agentbox configures
inside the box, plus a `box-trail` helper that tails the JSONL and renders
one color-coded line per tool call. v1 ships a passive tail (no scrub, no
pause, Claude-only). Codex and opencode adapters, interactive trail UX
(pause/scrub/click-to-expand), and mid-session layout switching are
explicitly out of scope.

## Requirements

### `--layout <name>` CLI flag

- New flag on `agentbox run` and `agentbox shell`. Accepts a single layout
  name. Default unset.
- **Acceptance:** `agentbox run --layout reviewer` produces a session whose
  zellij layout matches the `reviewer` shape (agent pane, live diff pane, test
  pane).
- **Acceptance:** `agentbox run --layout doesnotexist` exits with exit code 2
  (`InvalidArgs`) and prints a message naming the resolution paths it tried
  (built-ins; `~/.config/agentbox/layouts/doesnotexist.kdl`).
- `--layout` works with `--dry-run`: the dry-run output references the
  resolved layout file path so users can inspect where the KDL came from.
- `--layout` honored by `agentbox run` only when creating a new box. If a box
  already exists and the user runs `agentbox run` (no `--fresh`), the
  existing session's layout is preserved (per existing semantics — layouts
  are baked at create time, not re-rendered on attach).

### `[zellij] layout` config

- New config section `[zellij]` with one key: `layout = "<name>"`. Defaults to
  `"focus"` when unset. Lives in the global `~/.config/agentbox/config.toml`
  and in per-project `.agentbox.toml`; project overrides global; CLI flag
  overrides both.
- **Acceptance:** `agentbox config set zellij.layout reviewer` writes the
  value, and a subsequent `agentbox run` (no `--layout`) uses the reviewer
  layout.
- **Acceptance:** `agentbox config show --json | jq -r .zellij.layout`
  reflects the current value.
- The existing `agentbox config set` machinery handles the new path
  automatically (it's a string scalar via reflection per `config-set`'s
  design); no special parsing required.

### Built-in layout: `focus` (renamed default)

- The current layout is renamed `focus`. Behavior is unchanged: agent pane
  (70%), bottom row with git dashboard + btm, second tab with shell.
- **Acceptance:** running `agentbox run` on a fresh project with no layout
  config produces exactly the same KDL as v0.2.5.
- **Acceptance:** `agentbox run --layout focus` is equivalent to no flag.
- The existing `internal/zellij` Layout type uses `Mode` to differentiate
  Run vs Shell. Layout selection layers on top of that — Mode stays for
  shell-vs-run distinction, layout name picks the run-mode shape.

### Built-in layout: `reviewer`

- Three panes total, on a single tab plus the existing shell tab:
  - **Top-left, 50% width × 100% of available rows in the top row:** agent
    pane, same shape as `focus`'s agent pane (wrapped in `box-agent` so the
    pane drops into `zsh -l` on agent exit).
  - **Top-right, 50% width × 100% of available rows in the top row:** live
    diff against the base branch, refreshed every 2 seconds, rendered with
    delta for syntax-highlighted and side-by-side-or-unified output.
  - **Bottom, full width × 30% rows:** test runner, re-runs on file change
    via `watchexec`, output preserved in the pane.
- Plus the existing `shell` second tab (unchanged).
- Layout has the standard `default_tab_template` wrapping (tab-bar + status-
  bar) added in v0.2.3.
- **Acceptance:** `agentbox run --layout reviewer` lands in a zellij session
  with three named panes (`agent`, `diff`, `tests`) on a single tab plus the
  shell tab.

### Diff pane (in `reviewer`) — visual formatting

- **Base branch detection (in priority order):**
  1. Read `git symbolic-ref refs/remotes/origin/HEAD` and strip the
     `refs/remotes/origin/` prefix. Use the result if non-empty.
  2. Else, check for a local branch named `main` (`git rev-parse --verify main`).
  3. Else, check for `master`.
  4. Else, fall back to `HEAD~5` (the last five commits' worth of diff) and
     print a one-line note in the pane: "no base branch detected — showing
     last 5 commits".
- **Renderer: `delta`.** Already installed by base kit (line 144 of
  `internal/builtinkits/kits/base/install.sh`).
- **Visual requirements (the user explicitly called this out):**
  - Syntax highlighting on (delta default) — code in the diff is highlighted
    by language.
  - Line numbers visible (`--line-numbers`).
  - Side-by-side mode when terminal is wide enough (≥160 cols), unified
    mode otherwise. delta's `--side-by-side` is on a config-time flag; the
    helper script picks based on `tput cols` at refresh time.
  - File-header decoration on (delta default) — clear separators between
    files.
  - Use 24-bit color (`true-color`) — already on by default in delta when
    the terminal supports it; agentbox boxes do.
- **Refresh:** every 2 seconds via `watch --color -n 2 box-diff-watch`. Same
  pattern as `box-git-watch`.
- New base-kit helper `box-diff-watch` ships in this feature.
- **Acceptance:** with the project on `main`, branched to a feature branch
  with two file changes, `box-diff-watch` (run manually) prints a delta-
  rendered diff with syntax highlighting, line numbers, and file headers.

### Built-in layout: `auditor`

- Three panes total, on a single tab plus the existing shell tab:
  - **Top, full width × 60% rows:** agent pane, same shape as `focus`'s
    agent pane (wrapped in `box-agent`).
  - **Bottom, full width × 40% rows:** trail pane running `box-trail`,
    which tails `<state>/trail.jsonl` and renders one line per tool call.
- Plus the existing `shell` second tab (unchanged).
- Layout has the standard `default_tab_template` wrapping (tab-bar + status-
  bar) added in v0.2.3.
- **Acceptance:** `agentbox run --layout auditor` lands in a zellij session
  with two named panes (`agent`, `trail`) on a single tab plus the shell
  tab.
- **Acceptance:** when the agent runs `bash`, `edit`, `read`, `fetch`, etc.
  tool calls, each appears as a one-line entry in the trail pane within
  ~1s of the call completing.

### Trail mechanism (`box-trail`, Claude hooks, `<state>/trail.jsonl`)

- **Event source: Claude Code hooks.** agentbox configures hooks for the
  claude agent so that PreToolUse, PostToolUse, and Stop events append a
  JSONL record to `<state>/trail.jsonl`. The hooks file is owned by
  agentbox and lives at a path Claude Code reads (current candidate:
  `~/.claude/settings.json` inside the box, but design must verify the
  current hooks API at implementation time — Anthropic moves this surface).
  agentbox writes hooks via a templated settings file injected at session
  setup; the host's `~/.claude/settings.json` is unaffected.
- **Hook-to-JSONL contract:** each hook is a `jq`-friendly one-liner that
  echoes the relevant event payload as a single JSON object on its own
  line. Schema:
  ```json
  {"ts":"2026-05-05T19:22:01Z","kind":"tool_use","tool":"bash","input":"git status","exit":0,"dur_ms":420}
  {"ts":"2026-05-05T19:22:08Z","kind":"tool_use","tool":"edit","input":"runspec.go","exit":0}
  {"ts":"2026-05-05T19:22:15Z","kind":"tool_use","tool":"fetch","input":"goreleaser.com","bytes":1234}
  {"ts":"2026-05-05T19:22:30Z","kind":"stop","reason":"end_turn"}
  ```
  Field names are stable v1 contract; design picks exact set based on what
  Claude Code's current hook payload exposes.
- **`box-trail` helper:** new base-kit script that tails
  `<state>/trail.jsonl` (default; overridable via env var `BOX_TRAIL_FILE`)
  and renders each event as a one-line color-coded entry:
  ```
  14:22:01  [bash]   git status                   ok 0.4s
  14:22:08  [edit]   runspec.go (+12 -3)
  14:22:15  [fetch]  goreleaser.com (1.2 KB)
  14:22:30  [stop]   end_turn
  ```
  Color scheme: `[bash]` cyan, `[edit]` green, `[read]` dim, `[fetch]`
  yellow, errors red, `[stop]` magenta. Field truncation rules: input
  truncated to 60 cols, summary stats right-aligned.
- **`box-trail` runs `tail -F` semantics** so the pane survives
  `<state>/trail.jsonl` being absent at start (creates on first event),
  truncated, or rotated.
- **State path mount:** `<state>/trail.jsonl` is on the host (under
  `~/.local/share/agentbox/sessions/<id>/`) and bind-mounted RW into the
  box (where Claude's hooks write to it and `box-trail` tails it). Same
  path inside and outside per the project's same-path-mount invariant.
- **Acceptance:** with the auditor layout active, manually running an
  arbitrary `claude` tool call (e.g. via the agent pane prompt) appears in
  the trail pane within ~1s.
- **Acceptance:** restarting `box-trail` mid-session resumes from the file
  start without dropping events; pane shows full session history.
- **Acceptance:** running `agentbox run --layout auditor` against a
  non-claude agent (codex / opencode) emits the auditor layout but the
  trail pane prints a one-line message: `trail not configured for agent
  <name> (claude only in v1)`. Pane stays alive (shows the message and
  exits gracefully — no infinite spinner).

### Test runner pane (in `reviewer`)

- New base-kit helper `box-tests-watch` runs the project's canonical test
  command and re-runs it on file changes via `watchexec`.
- **Project type detection (in priority order):**
  1. `[zellij.reviewer] test_cmd = "..."` in agentbox config — explicit
     override; use as-is.
  2. `Makefile` with a `test` target (heuristic: `grep -E '^test:' Makefile`)
     → `make test`.
  3. `Cargo.toml` → `cargo test`.
  4. `go.mod` → `go test ./...`.
  5. `package.json` with a `"test"` script (heuristic: presence of
     `"test":` in scripts via `jq`) → choose the runner from the present
     lockfile (`bun.lock` → `bun test`, `pnpm-lock.yaml` → `pnpm test`,
     `yarn.lock` → `yarn test`, else `npm test`).
  6. `pyproject.toml` or `pytest.ini` → `pytest`.
  7. None matched → print a message: `no test command detected — set
     [zellij.reviewer] test_cmd in your config`. Pane stays useful (doesn't
     exit).
- **File-watch behavior:** `watchexec --watch . -- <cmd>`. watchexec is
  already in base kit. Honors `.gitignore` by default.
- **Initial state:** runs once at pane start (so the user sees something on
  first launch), then waits for changes.
- **Acceptance:** in a Go project, `box-tests-watch` runs `go test ./...`
  once and again on every `.go` file save.
- **Acceptance:** in an empty directory, `box-tests-watch` prints the "no
  test command detected" message and waits.

### Custom layouts at `~/.config/agentbox/layouts/<name>.kdl`

- Layout name resolution order:
  1. If `<name>` is a built-in (`focus`, `reviewer`), use that.
  2. Else, look for `~/.config/agentbox/layouts/<name>.kdl`. If present, use
     it.
  3. Else, error (exit 2).
- Custom KDL files support template substitution. The following placeholders
  are replaced before the file is written to the session state dir:
  - `{{project_abs}}` → host absolute path of the project (used for `cwd`
    in panes).
  - `{{shell}}` → user's configured shell name (`zsh`/`bash`/`fish`).
  - `{{agent_command}}` → the agent's primary command (e.g. `claude`).
  - `{{agent_args}}` → JSON-array of the agent's args (e.g.
    `["--dangerously-skip-permissions"]`), formatted KDL-style:
    `"--dangerously-skip-permissions"`.
  - `{{agent_cmd_full}}` → ready-to-paste KDL `command "..." \n args "..."
    "..."` block for users who don't want to template the two parts
    separately.
- **Acceptance:** a file at `~/.config/agentbox/layouts/myown.kdl`
  containing `{{project_abs}}` is rendered to `<state>/layout.kdl` with the
  placeholder replaced by the actual project path before zellij is
  launched.
- **Acceptance:** `agentbox run --layout myown --dry-run` prints the
  resolved file path of the user's layout (so they can inspect the
  templated output).
- Custom layouts are NOT validated for KDL correctness by agentbox — zellij
  errors at parse time become the user's signal. (Same forgiveness as
  `mounts.extra` per existing project precedent.)
- The wrapping `default_tab_template` (tab-bar + status-bar) is the user's
  responsibility in custom layouts. agentbox does not inject it.

### Documentation

- README's Quickstart section gains a one-paragraph note pointing at
  `docs/CLI.md` for the `--layout` flag and a `## Layouts` subsection
  listing the built-ins.
- `docs/CLI.md` documents `--layout` for `run` and `shell`, plus the
  resolution order and template variables for custom layouts.
- `docs/SPEC.md` adds a `[zellij]` section under `### Top-level keys` with
  the `layout` key.
- A new `docs/LAYOUTS.md` (similar in shape to `docs/KITS.md`) documents
  each built-in layout with a small ASCII diagram, lists the new
  base-kit helpers (`box-diff-watch`, `box-tests-watch`, `box-trail`),
  and shows a worked example of writing a custom layout with the
  template variables.
- A new `docs/TRAIL.md` documents the trail event schema (each JSONL
  field's meaning), how Claude Code hooks are wired by agentbox, where
  the trail file lives, and how a future contributor would add a codex
  or opencode adapter (v2 work).

## Scope

**In scope:**
- Three built-in layouts: `focus` (rename of current default), `reviewer`
  (new), `auditor` (new).
- `--layout` CLI flag on `run` and `shell`.
- `[zellij] layout` config key.
- Custom layouts at `~/.config/agentbox/layouts/<name>.kdl` with template
  substitution.
- **Minimal agent-activity trail** for the auditor layout: Claude Code
  hooks setup, JSONL event file at `<state>/trail.jsonl`, and a `box-trail`
  helper that tails and renders.
- Three new base-kit helpers: `box-diff-watch`, `box-tests-watch`,
  `box-trail`.
- Doc updates: README, `docs/CLI.md`, `docs/SPEC.md`, new `docs/LAYOUTS.md`,
  new `docs/TRAIL.md` documenting the trail event schema and how to extend
  it.

**Out of scope:**
- **`dashboard` layout.** Deferred — needs a DNS-stream pane and a
  doctor-watch helper that depend on more design.
- **Trail adapters for codex / opencode.** v1 is Claude-only. Codex and
  opencode have different observability surfaces and need separate
  research; auditor with those agents shows a placeholder message.
- **Interactive trail UX:** pause / scrub / click-to-expand / filter by
  tool kind. v1 is a passive tail.
- **Trail persistence across sessions.** `<state>/trail.jsonl` lives in the
  session state dir and dies with `--fresh`. Cross-session replay is a v2
  topic.
- **Trail token-spend / cost meter.** Separate feature; not in v1.
- **Mid-session layout switching** (`box layout <name>`, `agentbox layout
  set <name>`). Layouts chosen at create time only; switching means
  `agentbox run --fresh --layout <name>`.
- **Layout previews** (e.g. `agentbox layout list` showing a thumbnail).
- **Sharing layouts** (publishing a layout to a registry, importing one from
  a URL). Custom layouts live on the user's filesystem only.
- **Layout validation beyond name resolution.** agentbox doesn't parse the
  KDL; zellij does, and zellij's error becomes the user's signal.
- **Shell-mode layouts.** `agentbox shell` keeps the existing single-pane
  layout. `--layout` is run-only.
- **Adding a `[zellij.reviewer]` config table beyond `test_cmd`.** Per-pane
  config knobs would proliferate; defer.

## Technical Context

- **Existing code:**
  - `internal/zellij/kdl.go` — current layout generator with `runLayout`
    and `shellLayout`. Will fan out: a layout-resolution layer picks which
    generator to call.
  - `internal/zellij/types.go` — `Layout` struct (Mode, AgentCmd,
    ProjectAbs, Shell). Will gain a `LayoutName string` field. `Mode`
    stays as the run-vs-shell distinction; layout name is the run-mode
    shape selector.
  - `internal/config/config.go` — adds a `Zellij` struct + field.
  - `internal/config/typeinfo.go` — handles new `[zellij]` section
    automatically via reflection (same as existing `[shell]` etc.).
  - `internal/cli/run.go` and `internal/cli/shell.go` — add `--layout`
    flag wiring.
  - `internal/lifecycle/lifecycle.go` — passes resolved layout name into
    `BuildInput` and on to zellij rendering.
  - `internal/builtinkits/kits/base/install.sh` — installs the two new
    helpers alongside the existing `box-*` family.
  - Layout file precedent: `<state>/layout.kdl` is already the canonical
    path agentbox writes the rendered KDL to. That path stays.

- **New code:**
  - `internal/zellij/reviewer.go` and `internal/zellij/auditor.go` (or
    sibling functions in `kdl.go`) — generate the reviewer / auditor KDL.
  - `internal/zellij/custom.go` — loads a user-defined KDL file and
    performs template substitution. Uses `text/template` for safety.
  - `internal/lifecycle/` (probably a small new file or extension to
    existing) — when the resolved layout is `auditor` AND the resolved
    agent is `claude`, write a Claude hooks settings file into the
    session state dir and bind-mount it so Claude reads it. This is the
    "trail wiring" step.
  - `internal/builtinkits/kits/base/box-diff-watch` — shell helper.
  - `internal/builtinkits/kits/base/box-tests-watch` — shell helper.
  - `internal/builtinkits/kits/base/box-trail` — shell helper that tails
    `<state>/trail.jsonl` and renders.

- **Dependencies (already in base kit):**
  - `delta` — for diff rendering. Installed at v0.19.2.
  - `watchexec` — for file-change-triggered runs. Installed at v2.5.1.
  - `git`, `jq` (for `package.json` heuristic + JSONL parsing in
    `box-trail`) — already in base kit (`packages.txt` includes both).

- **Constraints:**
  - **Same-path bind mount stays.** The `cwd` for every pane is the host
    project path (per CLAUDE.md's invariants).
  - **No editor inside the box.** `reviewer` doesn't open a file editor —
    the diff pane is read-only via watch.
  - **Status-bar / tab-bar wrap.** Built-in layouts must include the
    `default_tab_template` block from v0.2.3 so keybinds stay visible.
    Custom layouts are the user's responsibility.
  - **Box-agent wrapper applies to the agent pane** in built-in layouts so
    pane-stays-useful-on-exit behavior from v0.2.5 carries over.
  - **No new external deps.** Everything reviewer needs is already
    installed.

## Open Questions

- **Template substitution mechanism for custom layouts:** Go `text/template`
  is the safe default but introduces `{{` `}}` collisions if a user's KDL
  legitimately uses double-brace tokens. Likely fine — KDL doesn't use them
  syntactically — but worth confirming during design.
- **`{{agent_cmd_full}}` shape:** does it include indentation? KDL is
  whitespace-sensitive only inside strings, so any indentation is cosmetic.
  Design's call.
- **Test-runner detection on monorepos / multi-module projects:** the
  heuristic walks the project root only. Subprojects with their own
  `Cargo.toml` etc. aren't handled. v1 fallback is the explicit
  `[zellij.reviewer] test_cmd` override.
- **Side-by-side threshold:** I picked 160 cols. Real-world tradeoff is
  unclear without trying it. Design can tune.
- **`box-tests-watch` initial run vs wait-for-change:** running once on
  start gives instant feedback but may surprise users who didn't expect a
  long-running test command to fire. Acceptable default per spec; can be
  made configurable later.
- **Shell tab in `reviewer`:** keep it, drop it, or make it optional? v1
  spec keeps it (parity with `focus`); design can revisit if it's
  unnecessary clutter.
- **Conflict between `box-tests-watch` and watchexec respecting
  `.gitignore`:** by default watchexec watches everything; with
  `.gitignore` honored it skips `node_modules` etc. Design picks the
  default (probably honor `.gitignore`, can revisit).
- **What to do when `git symbolic-ref refs/remotes/origin/HEAD` fails on a
  repo with no upstream:** the heuristic falls back to `main`/`master`/last-
  5-commits. Acceptable, but we should make sure the pane shows the actual
  comparison ref so the user knows what they're looking at. Design's
  responsibility to surface that.
- **Claude Code hooks API verification.** [CLAUDE.md flags this surface as
  fast-moving.] Design's first task is to verify the current hooks config
  format against `@anthropic-ai/claude-code@2.x` (Anthropic's docs and the
  installed binary's `--help` / `claude config` output). Confirm the exact
  hook event names (PreToolUse, PostToolUse, Stop, etc.), how each emits
  payload to a hook command, and whether settings are read from
  `~/.claude/settings.json` or some other path inside the box. If hooks
  turn out to be impractical (e.g. payload missing fields the trail needs,
  or settings model has changed), fall back: trail and `auditor` defer to
  v2; layouts ship with `focus` + `reviewer` only. Layout system itself
  should NOT be blocked on trail.
- **Hook settings injection mechanism.** Three candidates: (a) write a
  per-session settings file under session state and set `CLAUDE_CONFIG`
  env var to point at it; (b) write into `/etc/claude/settings.json`
  inside the box and rely on Claude reading global settings; (c) write
  into `~/.claude/settings.local.json` inside the box (the bind-mounted
  ~/.claude is read-only from agentbox's perspective; this option may
  conflict with the user's host file). Design picks based on what the
  current Claude Code version supports.
- **Trail JSONL schema stability.** v1 schema is design's contract. If
  Claude Code's hook payloads don't expose enough to populate the schema
  cleanly, design narrows the schema to what's available. Future agent
  adapters (codex / opencode in v2) must produce compatible JSONL, so v1
  schema decisions are load-bearing.
- **box-trail handling of malformed JSONL.** What if a hook misfires and
  writes a non-JSON line, or a partial line during high-throughput
  events? Default: skip the line silently and continue. Design picks
  whether to log the skip somewhere visible.
- **Concurrent writes from multiple hooks.** Append-only writes from
  separate hook invocations should be safe on Linux ext4/btrfs (atomic
  for writes < PIPE_BUF), but verify against the actual hook execution
  model. If hooks run sequentially per turn, no concern.
