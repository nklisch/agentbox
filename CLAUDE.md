# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Orientation

agentbox is a single-user Go CLI for running AI coding agents inside per-project Podman
containers. The repo is currently pre-code — the foundation docs are the source of truth.

Read in this order before changing anything:

1. `docs/VISION.md` — why this exists, what it isn't
2. `docs/SPEC.md` — every technical decision (config, runtime spec, labels, mounts, exit codes)
3. `docs/ARCHITECTURE.md` — how pieces fit (lifecycle, kit pipeline, network topology)
4. `docs/CLI.md` — command surface, flags, identifier resolution
5. `docs/KITS.md` — kit format and composition
6. `docs/ROADMAP.md` — phase order and per-phase test checkpoints

`docs/PROGRESS.md` (if present) tracks autopilot state — read it when resuming.

## Conventions

- **Stack:** Go (latest stable, no CGO), TOML config via `BurntSushi/toml`, single static binary.
- **No SDK for the container runtime.** Shell out to `podman`/`docker`/`zellij` and parse output.
- **No daemon.** Every command is one-shot. State lives in container labels and `~/.local/share/agentbox/`.
- **Same-path bind mount is non-negotiable:** `-v $PWD:$PWD -w $PWD`. Never rewrite project paths inside the box.
- **`project_id = sha1(realpath($PWD))[:12]`** — used for container name, network name, and state dir.
- **Labels are the source of truth** for `agentbox ls` — no external state file required. Schema in SPEC.md.
- **`--dry-run` is universal on mutating commands** and must print the exact shell invocation.
- **Secrets pass by name only.** `-e <NAME>` without value. Never read, log, or write secret values.
- **Exit codes are part of the contract** — see CLI.md "Exit codes" (notably 2/3/4/5/6/7/130).
- **`--json` output:** NDJSON for lists, single JSON object for single records. Errors to stderr.
- **Default network mode is `safe`, not `open`.** `safe` and `allowlist` share CoreDNS+iptables plumbing.
- **No editor inside the box.** Project is mounted at the host path; users edit on the host.
- **Built-in kits are read-only; user kits shadow built-ins by name.**

## Verification

Each phase in `docs/ROADMAP.md` ends with a `Test checkpoint:` block — a literal shell script
that is the acceptance criterion for that phase. Treat those as primary; Go unit tests cover
units, the checkpoint covers behavior end-to-end against real podman.

Once Phase 1 lands: `go build ./...`, `go test ./...`, `go vet ./...`. Health-check the system
with `agentbox doctor`. Inspect generated Dockerfiles with `agentbox build --print <kit_list>`.

## Watch out for stale training data

These move fast — verify against current sources, don't guess from memory:

- Podman / Docker CLI flags (network, security-opt, seccomp)
- CoreDNS plugins and Corefile syntax
- Zellij KDL layout schema
- Agent CLIs and their YOLO/permission flags (Claude Code, Codex, opencode)
- `@anthropic-ai/claude-code`, `@openai/codex` package names and install paths
