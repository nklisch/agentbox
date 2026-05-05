# Autopilot Progress

**Status:** in-progress
**Started:** 2026-05-04
**Last updated:** 2026-05-05
**Phases since last refactor:** 4
**Total refactor passes:** 0

---

## Phases

| # | Phase | Status | Completed |
|---|-------|--------|-----------|
| 1 | CLI scaffold, config loading, dry-run | done | 2026-05-05 |
| 2 | Kit pipeline, base kit, `agentbox build` | done | 2026-05-05 |
| 3 | Container lifecycle — run / shell / exec / attach / ls / rm | done | 2026-05-05 |
| 4 | Zellij-in-box, layout generation, `box` helpers | done | 2026-05-05 |
| 5 | Runtime kits + agent kits + agent integration | active (Parts A+B done) | — |
| 6 | Network policy — `safe` + `allowlist` + CoreDNS sidecar | pending | — |
| 7 | `containers` kit + nested rootless podman + `[runtime.containers]` | pending | — |
| 8 | macOS support, Docker fallback, completion, ship | pending | — |

---

## Refactor Log

### After Phase 3: gate triggered, refactor skipped
- **Trigger:** phases_since_refactor=3 (default cadence).
- **Investigation:** Examined `internal/kits/podman.go` (89 LOC) and `internal/container/podman.go` (235 LOC) for duplication. Both use `errors.As(err, &ee)` for `*exec.ExitError` discrimination, but each call site interprets the non-zero exit differently (image-absent vs. container-missing vs. inner-exit-code-propagation vs. already-gone). Extracting a shared helper would either lose the per-call meaning or save fewer than 20 lines at the cost of an indirection.
- **Decision:** Skip. Bump default cadence to **every 4 phases** for the next round (phases 1-3 were independent subsystems with minimal overlap — see autopilot decision framework "Adjust later (4 phases)").
- **Counter NOT reset.** phases_since_refactor stays at 3; next gate fires at 4.

### After Phase 4: gate triggered, refactor skipped again
- **Trigger:** phases_since_refactor=4 (raised cadence).
- **Investigation:** Phase 4 added one new package (`internal/zellij`) — pure string output, no duplication with existing code. Lifecycle grew Run/Shell/Attach into zellij paths but they share `zellijAttach` already. `box-info` script bash growth (~80 LOC) is a single-file concern, not library duplication. `runspec` env-vars list is now 8 entries; could be tabularized but currently readable.
- **Decision:** Skip. Wait for Phase 5 to re-evaluate; Phase 5 ships ~10 kits and may surface real patterns worth extracting (kit-install-helpers, version-pinning conventions, etc.).
- **Counter NOT reset.** phases_since_refactor stays at 4. Next gate fires at 5 (Phase 5's own gate, as designed).

---

## Phase 5 Part A Notes

What's now possible that wasn't before:
- `agentbox build node|python|go|rust|systems|cloud` — six new built-in kits
  build to working images. Each ships a focused language environment with
  modern tooling.
- All tool versions verified against upstream release APIs at write time
  (per CLAUDE.md "stale data" rule). Pinned versions are env-var overridable
  in each install.sh so users can bump independently.

Pinned versions (verified 2026-05-05):
- node: Node LTS major 24, current 25 (via `n`); bun 1.3.13, deno 2.7.14;
  pnpm + yarn via corepack
- python: uv 0.11.9, ruff 0.15.12, pyenv 2.6.28
- go: Go 1.26.2; gopls @latest; golangci-lint 2.12.1; delve 1.26.3
- rust: rustup bootstrapper; toolchains stable + nightly (channel names
  intentional, not point-pinned); cargo-watch 8.5.3, sccache 0.15.0
- systems: all from apt (Bookworm)
- cloud: aws-cli 2.34.42, gcloud 566.0.0 (apt), az 2.86.0 (apt),
  terraform 1.15.1, kubectl 1.36.0, helm 4.1.4

Image sizes after build (rough):
- python 814MB, go 1.39GB, node 1.27GB, systems 1.54GB, rust 2.22GB,
  cloud 2.31GB.

Structural fix landed alongside Part A:
- **base kit now bridges env.d into zsh.** Phase 2's Dockerfile generator
  wires env.d sourcing only into `/etc/profile.d/agentbox.sh` (bash login
  shells), but zsh (the agentbox default) doesn't read /etc/profile.d.
  Each Phase 5 Part A kit added the bridge to `/etc/zsh/zshenv` per-kit;
  base now provides it once for everyone. Idempotent guards prevent
  double-append. Commit `18473b1`.

Implementation stats:
- 1 Sonnet agent for all 6 kits (sequential within agent: systems first,
  then go, python, node, rust, cloud).
- 24 kit content files + 1 small structural fix to base.
- Plus `libatomic1` added to node/packages.txt (Node 25 runtime dep).
- Commits: design (`0da7a15`), Part A impl (`870f6a0`), zshenv fix
  (`18473b1`).

Part C of Phase 5 remains (agent kits: claude, codex, opencode + config defaults).

---

## Phase 5 Part B Notes

What's now possible that wasn't before:
- `agentbox build polyglot` — the headline kit. One image instead of seven.
  Installs Node + Python + Go + Rust + Ruby (rbenv) + Java/Kotlin (sdkman)
  + C/C++ toolchain + DB clients.
- `agentbox build polyglot,node` composes cleanly (node is NOT in conflicts_with).
- `agentbox build polyglot,python|go|rust|systems` all error at resolve time
  (conflicts fire on the full walked set, as designed).

Resolver behavior clarified:
- Conflicts fire on the **full resolved set** (after depends_on expansion), not
  just the user-requested set. This means `polyglot,claude` works only because
  "node" is omitted from polyglot's conflicts_with. If "node" were listed, the
  resolver would see [base, node, polyglot, claude] and error on node+polyglot.
  The design anticipated this (see design/phase-5.md cross-cutting decisions).

New pinned versions (verified 2026-05-05):
- rbenv: 1.3.2 (rbenv/rbenv tag v1.3.2 — GitHub releases API confirmed)
- sdkman: no version pin — get.sdkman.io installer URL is the stable distribution
  point; intentionally unpinned per design.

Smoke test results (image: agentbox/1c19bac4056b):
- node v25.9.0, pnpm 10.33.3, bun 1.3.13, deno 2.7.14
- python3 3.11.2, uv 0.11.9, ruff 0.15.12, pyenv 2.6.28
- go 1.26.2, golangci-lint 2.12.1, delve (Delve Debugger), gopls via go install
- rustc 1.95.0, cargo 1.95.0, cargo-watch 8.5.3, sccache 0.15.0
- rbenv 1.3.2, sdkman init script at /opt/sdkman/bin/sdkman-init.sh
- psql 15.16, mysql 10.11.14-MariaDB, sqlite3 3.40.1, redis-cli 7.0.15
- clang 14.0.6, cmake 3.25.1, ninja 1.11.1

Image size: 5.16 GB (expected; design said ~3-4GB but Ruby+sdkman+DB clients add up).

Implementation stats:
- 1 Sonnet agent. 4 files, 389 LOC total (install.sh is 296 lines).
- Inlined from 4 Part A kits + 2 new sections (Ruby, sdkman).
- Commit: `4175516`.

---

## Phase 4 Notes

What's now possible that wasn't before:
- `agentbox run` opens a **zellij** session inside the box: agent in 70% top
  pane, git ticker + `btm` stats sharing the bottom 30%, separate "shell" tab
  with zsh. Detach with Ctrl+p d, walk away, `agentbox attach .` reconnects to
  the same session.
- `agentbox shell` (default) opens a single-pane zellij — gives a familiar
  "agentbox" prompt for scripts using `expect`. `agentbox shell --no-zellij`
  drops to bare zsh for non-interactive scripting.
- `box info` inside the box prints the full CLI.md format: project_id,
  project, cwd, agent, kits, kit_image, network, mounts table (rw/ro per
  bind mount via findmnt), resources (cpu.max + memory.max + pids.max from
  cgroup v2), created. Degrades to `n/a` outside agentbox; exits 0.
- `box save <file>` actually persists across `agentbox rm`. The host's
  `~/.local/share/agentbox/sessions/<id>/saved/` directory is bind-mounted at
  `/root/.local/share/agentbox-saved` inside the box; AGENTBOX_SAVED_DIR points
  at the in-container path so the script writes to the mount.
- The interactive ROADMAP test checkpoint (expect script exercising attach +
  detach + reattach) is left for the user's manual verification — zellij needs
  a real TTY which the orchestrator's bash environment can't supply.

Implementation stats:
- 1 design pass, 1 implementation orchestration (single Sonnet agent), 1 hot-fix
  commit (AGENTBOX_SAVED_DIR was pointing at the host path).
- New package: `internal/zellij` (KDL generator, 8 golden tests).
- Total files touched: ~12 (3 new, 9 edited).
- Image size: 417 MB (unchanged from Phase 2; box-info polish added <1 KB).
- Commits: design (`49f1891`), implementation (`edb86eb`), saved-dir fix (`e5e9bb0`).

---

## Phase 3 Notes

What's now possible that wasn't before:
- `agentbox run` actually creates a working container, drops you into zsh.
- `agentbox shell`, `agentbox exec`, `agentbox attach`, `agentbox ls`, `agentbox rm`
  are all real — five Phase 1 stubs replaced with full implementations.
- `agentbox ls --json` gives NDJSON pipeable into `jq`.
- Identifier resolution accepts `.`, `<id-prefix>`, full `<id>`, or `agentbox-<id>`.
  Ambiguous prefixes return a helpful list of matches.
- The kit image is built on demand by `EnsureBox` (cache hit makes it instant
  after first build). Phase 2's Builder gets exercised for real.
- Mount-source-missing failures map to exit 7 with a useful message.

Implementation stats:
- 1 design pass, 1 implementation orchestration (single Sonnet agent, 18 files).
- Plus 1 hot-fix commit (TTY detection for attach when stdin is /dev/null).
- 2 new domain packages: `internal/container` (Runtime port + PodmanRuntime),
  `internal/lifecycle` (orchestrator + ResolveID).
- `internal/cli/stub.go` deleted entirely — no commands stubbed anymore.
- Total commits: design (`c3d9b37`), implementation (`bbab4cf`), TTY fix (`90157a5`).

Known gaps closing naturally in later phases:
- ROADMAP Phase 3 checkpoint requires `.agentbox.toml` override of agent kits
  to `["base"]`. Phase 5 ships polyglot/claude/etc.; this gap closes then.
- Network mode safe/allowlist degrades to open with stderr warning. Phase 6
  ships CoreDNS+iptables; warning goes away.

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

### Phase 3: Network mode degradation, not Fail Fast
- **Context:** Default config has `network.mode = "safe"`, but Phase 6 hasn't shipped CoreDNS+iptables. The original design had `guardNetworkMode` exit 6 on safe/allowlist; that would break the ROADMAP test checkpoint (which runs plain `agentbox run --no-attach`).
- **Chose:** `degradeNetworkMode` prints a stderr warning and falls back to `"open"`.
- **Alternative:** Default config change to mode="open" (would change SPEC user-facing default), or require flag override on every run.
- **Reasoning:** Defaults still work end-to-end during the gap between phases. Phase 6 replaces the warning with real safe mode without touching the runtime spec or the default config.

### Phase 3: TTY detection via golang.org/x/term, not stdlib `os.ModeCharDevice`
- **Context:** First implementation of `attach` hung when stdin was `/dev/null` (test/script invocations). `os.ModeCharDevice` is true for /dev/null too, so the stdlib-only check incorrectly identified /dev/null as a TTY and allocated a pty.
- **Chose:** Add `golang.org/x/term` (small, Go-team-maintained module). `term.IsTerminal(int(f.Fd()))` correctly distinguishes via TIOCGWINSZ ioctl.
- **Alternative:** Reimplement TIOCGWINSZ via raw syscall (fragile, OS-specific).
- **Reasoning:** Standard, tiny, canonical. Bumped go.mod's `go` directive from 1.23 → 1.25.0 (x/term v0.42.0's minimum). Still well within "latest stable".

### Phase 3: `attach` short-circuits to liveness check when stdin is not a TTY
- **Context:** `agentbox attach` semantics are "drop into a shell"; with no TTY, dropping into an interactive zsh against /dev/null hangs even with `-i` only (podman exec keeps the stream open).
- **Chose:** When `!isTerminal(os.Stdin)`, print `<project_id> (running)` and exit 0.
- **Alternative:** Run `[zsh, -c, exit]` (works but is surprising — user might expect their stdin to feed into zsh).
- **Reasoning:** Phase 4 replaces this whole path with zellij. The non-TTY path is for scripted use ("is this box up?") where the liveness signal is enough. Real interactive use is unaffected.

### Phase 4: AGENTBOX_SAVED_DIR points to in-container mount target, not host path
- **Context:** Phase 3's runspec set `AGENTBOX_SAVED_DIR = in.StateDir + "/saved"` (the host path). Phase 4's box-save script reads that env var and `cp $src $dst`. Inside the container, the host path doesn't resolve — `mkdir -p` made the container-side host-shaped directory and silently dropped the file there. `box save` reported success but nothing reached the host's `saved/`.
- **Chose:** Set `AGENTBOX_SAVED_DIR` to the literal in-container mount target `/root/.local/share/agentbox-saved`. The mount Target in the same runspec block matches.
- **Alternative:** Make box-save smarter (translate host paths to container paths). Worse — duplicates path knowledge in two places.
- **Reasoning:** Single Source of Truth. The env var is the path the script will write to; it must be the in-container path. Comment in runspec.go now says so explicitly.

### Phase 4: zellij KDL syntax verified at write time — design's syntax was correct
- **Context:** CLAUDE.md flags zellij KDL as "fast-moving — verify, don't guess." The design specified syntax based on zellij docs but warned that field names rotate.
- **Chose:** Implementer dumped `zellij setup --dump-layout default` from the base kit's zellij 0.44.1 and confirmed every field name in the design matched. No KDL adjustments needed.
- **Reasoning:** The design's spelling (`split_direction`, `pane size="..." name="..."`, `command`/`args`/`cwd`, `tab name="..." focus=true`) is correct for zellij 0.44.1. Future zellij upgrades may change this — re-verify if bumping.

### Phase 4: Lifecycle test seam exposed as `SetStdinIsTerminal`
- **Context:** Tests for Run/Shell/Attach need to control whether stdin appears as a TTY without actually allocating one.
- **Chose:** Package-level var `stdinIsTerminal = func() bool { return isTerminal(os.Stdin) }` plus an exported `SetStdinIsTerminal(fn) restoreFn` helper. Tests call `defer lifecycle.SetStdinIsTerminal(func() bool { return true })()`.
- **Alternative:** Inject as a Lifecycle field — would require touching every existing test that constructs Lifecycle.
- **Reasoning:** Boring, additive. Existing tests untouched.

### Phase 4: Lifecycle.Run writes layout even with --no-attach
- **Context:** Design said only write layout when actually attaching. But that means a later `agentbox attach .` finds an empty layout.kdl (touched by EnsureSession to satisfy the mount). Tests asserted layout.kdl is non-empty after `run --no-attach`.
- **Chose:** Always write the ModeRun layout in Lifecycle.Run, regardless of Attach.
- **Alternative:** Have Attach always regenerate (it already does, defensively). Both work; current choice keeps the layout file authoritative immediately after creation.

### Phase 3: Default agent kits don't exist until Phase 5 — known gap
- **Context:** `cfg.Agents["claude"].Kits = ["polyglot", "claude"]` per SPEC defaults, but only `base` exists in builtinkits/. Calling `agentbox run` in a fresh dir without override fails at kit resolution.
- **Chose:** Document; require `.agentbox.toml` overriding `[agents.claude] kits = ["base"]` for Phase 3 testing. The ROADMAP Phase 3 checkpoint script needs this override.
- **Alternative (deferred):** Fall back to base if requested kits missing, with a warning. Or change default kits temporarily.
- **Reasoning:** Phase 5 ships polyglot/claude/etc.; this gap closes naturally then. Logged as a "Suggested addition" under Phase 5 rather than carrying a temporary fallback.

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
- After Phase 5, ROADMAP Phase 3 test checkpoint should work without an `.agentbox.toml` override (polyglot/claude kits will exist).
- After Phase 6, the network-mode degradation in `Lifecycle.degradeNetworkMode` should be replaced with real safe/allowlist plumbing. The warning string + fallback are temporary scaffolding.

---

## Testing Passes

(none yet)

---

## Completion Summary

(pending)
