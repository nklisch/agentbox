# LAYOUTS

The zellij layout system for agentbox. Select a layout with `agentbox run --layout <name>`,
or set it permanently in config with `[zellij] layout = "<name>"`.

## Overview

Every `agentbox run` renders a zellij layout file at
`~/.local/share/agentbox/sessions/<id>/layout.kdl` and instructs zellij to load it. The
layout determines what panes and tabs you land in when you attach.

Layouts are baked at box-create time. To switch layouts, use `agentbox run --fresh --layout
<name>`. The existing session's layout is not changed on plain re-attach.

**Resolution order:**

1. `--layout <name>` CLI flag (highest priority).
2. `[zellij] layout = "<name>"` in the project's `.agentbox.toml`.
3. `[zellij] layout = "<name>"` in `~/.config/agentbox/config.toml`.
4. `"focus"` (built-in default).

If the name is not a built-in and no file exists at
`~/.config/agentbox/layouts/<name>.kdl`, agentbox exits with code 2 and prints an error
listing the built-in names and the custom path it tried.

---

## Built-in layouts

### `focus` (default)

The original agentbox layout. Three panes on a single work tab plus a shell tab.

```
┌──────────────────────────────────────────────────┐
│  tab: agentbox (focused)         tab: shell       │  ← tab bar
├──────────────────────────────────────────────────┤
│                                                  │
│            agent pane (70%)                      │
│        claude --dangerously-skip-permissions     │
│                                                  │
├─────────────────────┬────────────────────────────┤
│  git (watch)  30%   │  btm (stats)  30%          │
├──────────────────────────────────────────────────┤
│  status bar (mode, keybinds)                     │  ← status bar
└──────────────────────────────────────────────────┘
```

- **agent pane** — runs `box-agent <cmd>` so the pane drops to `zsh -l` on agent exit.
- **git pane** — `watch --color -n 2 box-git-watch` showing branch, dirty files, recent commits.
- **stats pane** — `btm` (bottom) for CPU/memory/process stats.
- **shell tab** — bare `zsh` for poking around without an agent.

### `reviewer`

Pre-commit posture: split pane for code review + live diff + test runner.

```
┌──────────────────────────────────────────────────┐
│  tab: reviewer (focused)         tab: shell       │  ← tab bar
├───────────────────────┬──────────────────────────┤
│                       │                          │
│   agent pane (50%)    │   diff pane (50%)        │
│   claude ...          │   watch box-diff-watch   │
│                       │   delta syntax highlight │
│                       │                          │
├──────────────────────────────────────────────────┤
│         tests pane (30%)                         │
│         box-tests-watch (watchexec)              │
├──────────────────────────────────────────────────┤
│  status bar                                      │
└──────────────────────────────────────────────────┘
```

- **agent pane** (50% width, 70% rows) — same `box-agent` wrapping as `focus`.
- **diff pane** (50% width, 70% rows) — `watch --color -n 2 box-diff-watch`. Detects base
  branch automatically; renders with delta (syntax highlighting, line numbers, side-by-side
  at ≥160 cols).
- **tests pane** (full width, 30% rows) — `box-tests-watch`. Detects your project's test
  command and re-runs it on file changes via watchexec.
- **shell tab** — same bare `zsh` as `focus`.

### `auditor`

Agent observability: agent pane (60% rows) + live tool-call trail pane (40% rows).

```
┌──────────────────────────────────────────────────┐
│  tab: auditor (focused)          tab: shell       │  ← tab bar
├──────────────────────────────────────────────────┤
│                                                  │
│          agent pane (60%)                        │
│      claude --dangerously-skip-permissions       │
│                                                  │
├──────────────────────────────────────────────────┤
│          trail pane (40%)                        │
│  box-trail tails trail.jsonl, renders events     │
│  14:22:01  PostToolUse   Bash   git status  294ms │
│  14:22:08  PostToolUse   Edit   runspec.go        │
├──────────────────────────────────────────────────┤
│  status bar                                      │
└──────────────────────────────────────────────────┘
```

- **agent pane** (full width, 60% rows) — same `box-agent` wrapping as `focus`.
- **trail pane** (full width, 40% rows) — `box-trail` tails the JSONL trail file and
  renders one color-coded line per Claude Code hook event.
- **shell tab** — same bare `zsh` as other built-in layouts.

**Trail wiring** is automatic when `agent = "claude"` (the only supported agent in v1).
agentbox writes a merged `claude-settings.json` (user settings + agentbox trail hooks) and
shadow-mounts it read-only on top of `$HOME/.claude/settings.json` inside the box (HOME
mirrors the host's HOME path — see SPEC mount semantics). The host's `~/.claude/settings.json`
is never modified.

For non-claude agents, the trail pane shows a placeholder message and stays alive.

**Env var set by agentbox for trail-wired sessions:**

| Variable | Value |
| -------- | ----- |
| `BOX_TRAIL_FILE` | `/etc/agentbox/trail.jsonl` (in-container path) |

See [docs/TRAIL.md](TRAIL.md) for the full JSONL event schema and extension guide.

---

## New base-kit helpers

Three new helpers ship in the base kit alongside the existing `box-*` family:

### `box-diff-watch`

Renders a delta-formatted diff of HEAD against the detected base branch. Designed for
`watch --color -n 2 box-diff-watch` in the reviewer diff pane.

**Base branch detection (priority order):**

1. `git symbolic-ref refs/remotes/origin/HEAD` (strips `refs/remotes/origin/` prefix).
2. Local `main` branch.
3. Local `master` branch.
4. Fallback: `HEAD~5..HEAD` (last 5 commits).

**Visual:**
- `--line-numbers` always on.
- `--side-by-side` enabled when terminal width ≥ 160 cols; unified otherwise.
- delta 24-bit color (auto-detected; agentbox boxes support it).

**Usage:** runs via `watch --color -n 2 box-diff-watch` from the reviewer diff pane. Can
also be run manually from any box shell.

### `box-tests-watch`

Detects the project's canonical test command and re-runs it on file change via watchexec.
Used in the reviewer tests pane.

**Detection order (first match wins):**

1. `AGENTBOX_REVIEWER_TEST_CMD` env var — explicit override, used as-is.
2. `Makefile` with a `test:` target → `make test`.
3. `Cargo.toml` → `cargo test`.
4. `go.mod` → `go test ./...`.
5. `package.json` with a `"test"` script → `bun test` / `pnpm test` / `yarn test` /
   `npm test` (chosen by lockfile: `bun.lock`/`bun.lockb`, `pnpm-lock.yaml`, `yarn.lock`).
6. `pyproject.toml` or `pytest.ini` → `pytest`.
7. None matched → prints a message and sleeps (pane stays alive).

**watchexec behavior:** runs the test command once at startup (immediate feedback), then
re-runs on every file change. Honors `.gitignore`. `--clear` between runs; `--restart`
kills the previous run if a change fires before it finishes.

**Env var:**

| Variable | Effect |
| -------- | ------ |
| `AGENTBOX_REVIEWER_TEST_CMD` | Force a specific test command (bypasses detection). |

### `box-trail`

Tails `$BOX_TRAIL_FILE` (the agent-activity JSONL trail) and renders one color-coded line
per Claude Code hook event. Used in the `auditor` layout's trail pane.

When `BOX_TRAIL_FILE` is unset (non-claude agent or non-auditor layout), `box-trail` prints
a placeholder message and sleeps so the pane stays visible.

**Color scheme:**

| Event | Color |
| ----- | ----- |
| `PreToolUse` | dim (high volume; quieted) |
| `PostToolUse` | cyan |
| `PostToolUseFailure` | red |
| `Stop` | magenta |
| `StopFailure` | bold red |

**Env var:**

| Variable | Effect |
| -------- | ------ |
| `BOX_TRAIL_FILE` | Path to the JSONL trail file. Set by agentbox when auditor + claude. |

---

## Custom layouts

Drop a KDL file at `~/.config/agentbox/layouts/<name>.kdl`. Name it anything (no built-in
name conflicts). Select it with `--layout <name>`.

### Template variables

agentbox performs Go `text/template` substitution on the file before writing it. Use these
in your KDL:

| Variable | Value |
| -------- | ----- |
| `{{.ProjectAbs}}` | Absolute path of the project on the host (use as `cwd` in panes). |
| `{{.Shell}}` | Configured shell name (`zsh`, `bash`, `fish`). |
| `{{.AgentCommand}}` | First element of the agent command (e.g. `claude`). |
| `{{.AgentArgs}}` | Remaining args, KDL-quoted and space-joined (e.g. `"--dangerously-skip-permissions"`). |
| `{{.AgentCmdFull}}` | Ready-to-paste KDL block: `command "claude"\nargs "--dangerously-skip-permissions"`. |
| `{{.TrailFile}}` | In-container trail file path; empty unless trail is wired (auditor + claude). |

KDL doesn't use `{{` syntactically, so collisions are unlikely. If you need a literal `{{`
in your layout, escape it with `{{"{{"}}` (standard Go template idiom).

**Custom layouts are not validated by agentbox** — zellij parses the KDL and its errors are
your signal. The `default_tab_template` wrapping (tab bar + status bar) is your
responsibility in custom layouts.

### Worked example

```
~/.config/agentbox/layouts/myworkflow.kdl
```

```kdl
// my custom agentbox layout
layout {
    default_tab_template {
        pane size=1 borderless=true {
            plugin location="tab-bar"
        }
        children
        pane size=2 borderless=true {
            plugin location="status-bar"
        }
    }

    tab name="work" focus=true {
        pane split_direction="horizontal" {
            pane size="60%" name="agent" {
                {{.AgentCmdFull}}
                cwd "{{.ProjectAbs}}"
            }
            pane size="40%" name="shell" {
                command "{{.Shell}}"
                cwd "{{.ProjectAbs}}"
            }
        }
    }
}
```

Select it with:

```sh
agentbox run --layout myworkflow
# or permanently:
agentbox config set zellij.layout myworkflow
```

Inspect the rendered output (with substitutions applied) via dry-run:

```sh
agentbox run --layout myworkflow --dry-run
# prints: # layout = myworkflow (custom, /home/u/.config/agentbox/layouts/myworkflow.kdl)
```
