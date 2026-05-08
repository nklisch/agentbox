# Feature: claude-mode integration (`agentbox run --mode`)

## Summary

The `claude` kit now installs `claude-mode` alongside Claude Code. `claude-mode`
is a thin wrapper that prepends a behaviorally-tuned system prompt to every
Claude session — no API key tricks, no forking, just a curated prompt injected
before the conversation starts.

`agentbox run --mode <preset>` wires this in: instead of launching
`claude --dangerously-skip-permissions`, the agent pane runs
`claude-mode <preset> --dangerously-skip-permissions`. Empty `--mode` is a
byte-identical no-op; the flag is opt-in.

## Prerequisites

The `claude` kit image must have been built (or pulled) after this change was
merged. If you see `claude-mode: command not found` inside the box, rebuild:

```sh
agentbox build polyglot,claude --no-cache
```

## Usage

```sh
agentbox run --mode create        # task-creation posture
agentbox run --mode safe          # surgical-precision, minimal blast radius
agentbox run --mode refactor      # refactor-focused prompting
agentbox run --mode explore       # research and exploration posture
agentbox run --mode debug         # debugging posture
agentbox run --mode methodical    # step-by-step verification
agentbox run --mode director      # high-level direction, delegates details
agentbox run --mode partner       # collaborative pair-programming style
agentbox run --mode none          # claude-mode binary, no prompt injection
```

The `--mode` flag composes with all other `agentbox run` flags:

```sh
agentbox run --mode safe --layout reviewer
agentbox run --mode create --dry-run   # shows '# mode = create' in header
```

## Preset semantics

Preset definitions live in `claude-mode` itself — agentbox forwards the preset
name verbatim and does not validate it against a fixed list. This means:

- **Built-in presets** (`create`, `extend`, `safe`, `refactor`, `explore`,
  `debug`, `methodical`, `director`, `partner`, `none`) work out of the box.
- **User-defined presets** from a `.claude-mode.json` file inside the box are
  also accepted. agentbox cannot see these from the host; `claude-mode` rejects
  unknown names at run time with a clear error.

For the full prompt text and per-preset behavior details, see the upstream repo:
<https://github.com/nklisch/claude-code-modes>

## Constraints

- **Claude agent only.** `--mode` is rejected with exit code 2 when the resolved
  agent command is not `claude` (e.g. `agentbox run codex --mode create` exits 2).
- **No new config key.** `--mode` is a per-run flag only; there is no
  `[agents.claude] mode = "..."` config equivalent. Use a shell alias or
  project-local script if you always want the same mode.
- **No preset validation on the host.** agentbox never reads `.claude-mode.json`
  from inside the box; that file is only visible at agent-start time inside
  the container.

## Dry-run

`agentbox run --mode create --dry-run` shows a `# mode = create` comment header
between `# network = ...` and `# layout = ...` so you can confirm the flag is
wired before spinning up the box.

## Kit rebuild

`claude-mode` is installed at build time into `/usr/local/bin/claude-mode`. The
binary tracks whatever `main` branch release was current when the image was
built — there is no version pinning yet (matching how `claude` itself is
pinned). To get a newer `claude-mode` release, rebuild the kit image:

```sh
agentbox build polyglot,claude --no-cache
```

The `CLAUDE_MODE_VERSION` env var is read at build time for forward-compat
traceability but does not yet alter which release is fetched (upstream does not
expose a version-select mechanism).
