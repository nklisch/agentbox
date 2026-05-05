# Autopilot Progress

**Status:** in-progress
**Started:** 2026-05-04
**Last updated:** 2026-05-04
**Phases since last refactor:** 0
**Total refactor passes:** 0

---

## Phases

| # | Phase | Status | Completed |
|---|-------|--------|-----------|
| 1 | CLI scaffold, config loading, dry-run | active | — |
| 2 | Kit pipeline, base kit, `agentbox build` | pending | — |
| 3 | Container lifecycle — run / shell / exec / attach / ls / rm | pending | — |
| 4 | Zellij-in-box, layout generation, `box` helpers | pending | — |
| 5 | Runtime kits + agent kits + agent integration | pending | — |
| 6 | Network policy — `safe` + `allowlist` + CoreDNS sidecar | pending | — |
| 7 | `containers` kit + nested rootless podman + `[runtime.containers]` | pending | — |
| 8 | macOS support, Docker fallback, completion, ship | pending | — |

---

## Refactor Log

(none yet)

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
