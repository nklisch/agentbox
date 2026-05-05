# Autopilot Progress

**Status:** in-progress
**Started:** 2026-05-04
**Last updated:** 2026-05-05
**Phases since last refactor:** 2
**Total refactor passes:** 0

---

## Phases

| # | Phase | Status | Completed |
|---|-------|--------|-----------|
| 1 | CLI scaffold, config loading, dry-run | done | 2026-05-05 |
| 2 | Kit pipeline, base kit, `agentbox build` | done | 2026-05-05 |
| 3 | Container lifecycle — run / shell / exec / attach / ls / rm | active | — |
| 4 | Zellij-in-box, layout generation, `box` helpers | pending | — |
| 5 | Runtime kits + agent kits + agent integration | pending | — |
| 6 | Network policy — `safe` + `allowlist` + CoreDNS sidecar | pending | — |
| 7 | `containers` kit + nested rootless podman + `[runtime.containers]` | pending | — |
| 8 | macOS support, Docker fallback, completion, ship | pending | — |

---

## Refactor Log

(none yet — phases_since_refactor=1, default trigger is every 3 phases)

---

## Phase 2 Notes

What's now possible that wasn't before:
- `agentbox build base` actually produces a working 417MB Debian image with the
  in-box DX bundle (zsh + starship + zellij + 12 modern CLI tools + the box helpers).
- `agentbox build --print <kits>` emits the full Dockerfile to stdout for inspection.
- `agentbox build --list` enumerates known kits (built-in + user; user shadows by name).
- `agentbox build --no-cache` forces a full rebuild; `agentbox build --prune` cleans
  unreferenced agentbox/* images.
- The kit cache at `~/.local/share/agentbox/cache/kits/` writes both `<tag>.json`
  and `<tag>.Dockerfile` for inspection.
- Phase 5+'s `polyglot`, `node`, `python`, `claude`, etc., are now a content-only
  add: drop a kit dir into `internal/builtinkits/kits/<name>/`, no Go code changes
  required. The Runner port + Builder + Resolver handle it.
- The domain layer stays clean: `internal/kits` and `internal/builtinkits` import
  no cobra. PodmanRunner is the only adapter using `os/exec`.

Implementation stats:
- 1 design pass, 2 implementation orchestrations (Part A + Part B), 3 commits.
- 49 unit tests in internal/kits; full test suite (Phase 1 + Phase 2) green.
- Image build is ~5min cold, ~20s warm (podman layer cache); cache hit instant.

---

## Phase 1 Notes

What's now possible that wasn't before:
- `agentbox` is a real binary. It boots, parses cobra args, loads merged TOML config.
- `agentbox config show/edit/path` works end-to-end with `--json`, `--global`, `--project`.
- `agentbox run --dry-run` produces a fully-formed `podman create` invocation with the
  same-path bind mount, full label schema, secrets passed by name only, and `--cap-drop
  ALL --security-opt no-new-privileges`. Other phases will plug in real podman calls.
- `agentbox doctor` reports runtime + state-dir health.
- All exit codes from CLI.md are wired (0/1/2/3 today, 4/5/6/7 reserved as constants).
- Domain layer (config, project, runspec, state, doctor, exitcode, version) is
  cobra-free and fully unit-tested. Future agents can build on this without leaking
  the CLI library into the domain.

Implementation stats:
- 2 Sonnet agents, sequential (foundation → CLI/main).
- 17 Go source files + 5 test files + Makefile + go.mod/go.sum + .gitignore.
- 3 commits on `main`: `7c29c3f` foundation, `ecb2a75` CLI+main.

---

## Decision Log

### Phase 1: CLI library — cobra
- **Context:** ROADMAP.md leaves the choice between cobra and stdlib `flag` + a sub-router open ("pick at design time").
- **Chose:** `github.com/spf13/cobra`.
- **Alternative:** stdlib `flag` + a hand-rolled subcommand router.
- **Reasoning:** Cobra is the boring, well-understood standard for multi-subcommand Go CLIs. It handles per-subcommand flags, help text, and `completion bash|zsh|fish` (which Phase 8 explicitly needs) without us writing a router. Stdlib `flag` would mean re-inventing all of that.

### Phase 1: Go module path — `github.com/nklisch/agentbox`
- **Context:** Greenfield project; no remote configured yet.
- **Chose:** `github.com/nklisch/agentbox`.
- **Alternative:** plain `agentbox` (no domain prefix).
- **Reasoning:** The user's email is `nklisch@gmail.com` and personal GitHub is the most likely future remote per VISION.md framing ("Not for distribution… If it spreads, fine"). Domain-style paths are also less awkward when introducing internal sub-packages later.

### Phase 1: cobra version — pinned via `@latest` at install time, resolved to v1.10.2
- **Context:** Design didn't pin a specific cobra version; agent had to pick at `go get` time.
- **Chose:** Whatever `@latest` resolved to (v1.10.2 at time of install).
- **Alternative:** Pin to a known-good older release.
- **Reasoning:** Cobra's API has been stable for years and the design's API usage (`MarkFlagsMutuallyExclusive`, `MatchAll`, completion generators) all matched current API exactly. Pinning later via `go.sum` is automatic.

### Phase 1: `.gitignore` pattern shape — root-anchored
- **Context:** Initial `.gitignore` from Agent 1 had `agentbox` (unanchored), which would also match `cmd/agentbox/` source directory and prevent it from being staged.
- **Chose:** `/agentbox` (root-anchored).
- **Alternative:** `agentbox` (would have blocked the source dir).
- **Reasoning:** Convention for Go binaries — only the root-level binary should be ignored, not anything else with the same name. Agent 2 caught this and fixed it before committing.

### Phase 2: Built-in kits live at `internal/builtinkits/kits/`, not project root
- **Context:** Original brief recommended `kits/` at project root. Go's `//go:embed` forbids `..` and `/`-prefixed patterns — kits two levels above the embedder can't be embedded.
- **Chose:** `internal/builtinkits/kits/<name>/` — embedder + content live together.
- **Alternative:** Place a `package <name>` Go file at project root just for embedding (unusual layout) or rebuild the binary with separate kit shipping (against VISION's "single static binary").
- **Reasoning:** Go's embed restriction is structural; the design honors it cleanly with no functional cost.

### Phase 2: SPEC's `[runtime.containers]` was renamed to `[containers]` in Phase 1; Phase 2's design re-confirmed this
- See Phase 1 deviation log entry; Phase 2 cache JSON, build flags, and runspec all assume the renamed shape.

### Phase 2: `git-delta` moved from packages.txt to install.sh
- **Context:** Design listed `git-delta` in `packages.txt`, but it's not in Debian Bookworm's apt repos.
- **Chose:** Install via GitHub releases (dandavison/delta v0.19.2) inside install.sh, with `git config --system core.pager` wired to it.
- **Reasoning:** Debian's late inclusion of `delta`. Apt-only would silently drop the tool.

### Phase 2: `box info` env-var contract — read AGENTBOX_* env vars, fall back to "n/a"
- **Context:** The Phase 2 ROADMAP test checkpoint runs `podman run --rm $TAG box info` directly (no agentbox wrapper, no labels visible). Earlier alternative was to read container labels via `/run/.containerenv`, but podman doesn't expose user-defined labels there.
- **Chose:** `box info` reads `AGENTBOX_PROJECT_ID`, `AGENTBOX_PROJECT`, etc. — Phase 3 will set these via `podman create -e <NAME>=<value>` at container create time.
- **Alternative:** Custom labels file at a known path inside the container (e.g., `/etc/agentbox/labels`).
- **Reasoning:** Env vars compose with Phase 1's secrets-by-name pattern (just `-e <NAME>=<value>` instead of `-e <NAME>`); no extra mount needed; simpler to debug.

### Phase 2: Pinned in-box tool versions (Part B install.sh)
- All versions verified against current GitHub releases at write time:
  zellij 0.44.1 / starship 1.25.1 / eza 0.23.4 / dust 1.2.4 /
  duf 0.9.1 / bottom 0.12.3 / procs 0.14.11 / hyperfine 1.20.0 /
  watchexec 2.5.1 / yq 4.53.2 / zoxide 0.9.9 / git-delta 0.19.2 /
  httpie 3.2.4 (pip) / tldr (pip, unpinned)
- The implementer should bump these as upstream releases stabilize. Build is reproducible against any pin that's still resolvable on GitHub.

---

## Deviations

### Phase 1: SPEC.md `[runtime.containers]` is invalid TOML
- **Expected:** SPEC.md shows `runtime = "podman"` (top-level key) alongside `[runtime.containers]` (sub-table). TOML forbids a key from being both a value and a table-parent.
- **Actual:** Renamed `[runtime.containers]` → `[containers]` in the config schema. Top-level `runtime = "podman"` is unchanged.
- **Impact:** Minor user-facing rename. SPEC.md should be updated to match (queued under "Suggested Additions" — covered by `/update-documentation` after Phase 1 implementation).

---

## Suggested Additions

- Update SPEC.md to reflect `[containers]` rename (deviation from Phase 1 design).

---

## Testing Passes

(none yet)

---

## Completion Summary

(pending)
