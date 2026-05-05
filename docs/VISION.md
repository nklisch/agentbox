# VISION

A note to future-me about why agentbox exists.

## Why this exists

I want to run AI coding agents — Claude Code, Codex, opencode — with their permission
guardrails turned off, without that being scary. On the host, "YOLO mode" means an agent
can `rm -rf` something I care about, leak a credential it stumbles across, or `npm install`
some compromised package directly into my home directory. The friction of confirming every
action is real, but the alternative shouldn't be "yolo and hope."

The existing options either overshoot (microVMs, devcontainers built per-project,
cloud sandboxes) or undershoot (just running on the host, or a vanilla `docker run` with
no DX). I want something in the middle: a container that's isolated enough that I'm
comfortable letting an agent run unattended, but ergonomic enough that I actually use it.

## What it is

A personal CLI that, when I run `agentbox run` in a project directory:

1. Ensures a per-project Podman container exists for that project (creates one if not).
2. Bind-mounts the project at the same absolute path inside the box.
3. Drops me into a zellij session inside the box where the configured agent is already
   running with its YOLO flag enabled.
4. Lets me detach, walk away, come back later, and reattach to the same session with
   state intact.

The box has a real shell environment — zsh + starship, modern CLI replacements, language
runtimes — so when I open a pane to poke around, it feels like a place I want to be.

When I'm done with the box, `agentbox rm` throws it away. The project, my agent's config,
and my shell history persist on the host. The container itself, and anything installed
inside it, doesn't.

## Who it's for

Me. Maybe you, if you're reading this and you also run agents locally and want a sane
isolation story. But the design is calibrated for one user — decisions favor my workflow
over generality, and the docs are written for future-me, not for onboarding strangers.

## What success looks like

- `agentbox run` in any of my projects gets me to a working agent session in seconds.
- I run agents with permissions disabled inside the box and don't think twice about it.
- Detach → walk away for hours → reattach → agent state intact, no surprises.
- Adding a new language runtime or tool to the box is a one-file change to a kit, not a
  Dockerfile rewrite.
- Network allowlist mode actually blocks the things I'd want it to block when I enable it.
- I prefer running agents through agentbox to running them on the host. If I find myself
  bypassing it, something's wrong with the design.

## What this is not

- **Not microVM-grade isolation.** Containers + dropped capabilities + no-new-privileges is
  the bar. A determined container escape ends the game and that's accepted.
- **Not cloud or remote.** Local boxes only. No daemon, no central service.
- **Not multi-user.** No team features, no permission models, no shared state between users.
- **Not a web UI.** Terminal-native via zellij.
- **Not a Docker Desktop product.** Podman is the primary runtime. Docker works as a
  fallback. Docker Desktop on macOS specifically is not the recommended path.
- **Not Windows.** Linux and macOS only. Maybe Windows later if I need it.
- **Not a devcontainer replacement.** The kit is the *agent's* environment. The project's
  own build environment is the project's concern.
- **Not for distribution.** No marketing, no onboarding, no Homebrew tap (yet). If it
  spreads, fine. That's not why I'm building it.

## The shape of the thing

A single Go binary that shells out to `podman` (or `docker`) and `zellij`. No daemon, no
SDK dependencies, no long-running services on the host. Configuration is TOML, two-tier
(global + per-project). State lives in container labels and a small directory under
`~/.local/share/agentbox/`. Kits — the layered tooling images — are composable: pick a list
like `polyglot + cloud + claude` and the build system generates a Dockerfile that stacks
them.

Everything beyond that is in `SPEC.md` and `ARCHITECTURE.md`.
