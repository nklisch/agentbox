# Autopilot Progress

**Status:** in-progress
**Started:** 2026-05-04
**Last updated:** 2026-05-05
**Phases since last refactor:** 1
**Total refactor passes:** 0

---

## Phases

| # | Phase | Status | Completed |
|---|-------|--------|-----------|
| 1 | CLI scaffold, config loading, dry-run | done | 2026-05-05 |
| 2 | Kit pipeline, base kit, `agentbox build` | active | — |
| 3 | Container lifecycle — run / shell / exec / attach / ls / rm | pending | — |
| 4 | Zellij-in-box, layout generation, `box` helpers | pending | — |
| 5 | Runtime kits + agent kits + agent integration | pending | — |
| 6 | Network policy — `safe` + `allowlist` + CoreDNS sidecar | pending | — |
| 7 | `containers` kit + nested rootless podman + `[runtime.containers]` | pending | — |
| 8 | macOS support, Docker fallback, completion, ship | pending | — |

---

## Refactor Log

(none yet — phases_since_refactor=1, default trigger is every 3 phases)

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
