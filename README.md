# agentbox

Single-user CLI for running AI coding agents inside per-project Podman containers.

`agentbox run` ensures a per-project Podman container exists for the current
directory, bind-mounts the project at the same path inside the box, and drops
you into a zellij session where the configured agent (Claude Code, Codex,
opencode) is already running with dangerous permissions disabled. Detach, walk
away, come back hours later, reattach. The project, agent config, and shell
history persist on the host. The container itself is disposable.

This is calibrated for one user — me, the author. Decisions favor my workflow
over generality.

## Quickstart

```sh
# 1. Build and install (Go 1.23+, podman in PATH).
make install

# 2. Verify your environment.
agentbox doctor --fix    # auto-remediates podman machine on macOS.

# 3. From any project directory.
cd ~/dev/myproject
agentbox run             # creates the box; drops you into zellij with Claude.

# 4. When you're done.
agentbox rm .
```

## Documentation

Read in this order:

1. [docs/VISION.md](docs/VISION.md) — why this exists, what it isn't.
2. [docs/SPEC.md](docs/SPEC.md) — every technical decision (config, runtime
   spec, labels, mounts, exit codes).
3. [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — how the pieces fit
   (lifecycle, kit pipeline, network topology).
4. [docs/CLI.md](docs/CLI.md) — command surface, flags, identifier resolution.
5. [docs/KITS.md](docs/KITS.md) — kit format and composition.
6. [docs/ROADMAP.md](docs/ROADMAP.md) — phase order and per-phase test
   checkpoints.

## Status

v0.1.0 — Linux primary. macOS via `podman machine` supported. Docker as a
runtime fallback (`--runtime docker`). Tested against rootless podman 4.x.
See `docs/PROGRESS.md` for the build history.

## License

MIT (or Apache-2.0 — set at tag time).
