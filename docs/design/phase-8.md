# Design: Phase 8 — macOS support, Docker fallback, completion, ship

## Overview

Phase 8 is the ship phase. It adds:

- **Docker as a fallback runtime** — `agentbox --runtime docker run` actually works
  against docker (not just podman with `--bin=docker`), via a thin `DockerRuntime` that
  embeds `PodmanRuntime` and only overrides the one method docker handles differently
  (`NetworkExists` — `docker network exists` doesn't exist; we use `docker network
  inspect` and treat exit-non-zero as "absent").
- **macOS support via `podman machine`** — a new doctor check verifies a machine is
  running (skip-OK on Linux); `agentbox doctor --fix` runs `podman machine init && start`
  if needed.
- **`agentbox doctor --fix`** — Phase 1 declared the flag and never wired it. Phase 8
  adds a `Fix func() error` field to `doctor.Check` so each check can ship its own safe
  remediation. Most fixes are no-ops; the meaningful ones are `podman-machine` and
  `coredns-image`.
- **Three more doctor checks**: `kit-cache-health` (compares cache JSON files vs. live
  podman images), `mount-source-existence` (per running box, verifies bind-mount
  sources still exist on the host).
- **Project README** at the repo root pointing at `docs/`.
- **Documentation alignment** — `/update-documentation` invocation reconciles
  SPEC.md / ARCHITECTURE.md / CLI.md / KITS.md with the implemented reality (every
  Phase 1-7 deviation logged in PROGRESS.md should propagate).
- **`v0.1.0` tag** — the symbolic completion of the roadmap.

`agentbox completion bash|zsh|fish` is already shipped (Phase 1); Phase 8 just verifies
it appears in the test checkpoint.

## Cross-cutting decisions

- **DockerRuntime via embedding, not duplication.** `PodmanRuntime` already takes a
  `Bin string` field — pass `"docker"` and most methods Just Work because the flag
  surfaces are nearly identical. The one method that differs (NetworkExists) gets
  overridden via Go composition. Single file, ~40 LOC.
- **No `DockerRunner` for kits.** `kits.PodmanRunner` is the same shape: pass `"docker"`
  and `docker build`, `docker images`, `docker rmi` work identically. Verify at
  implementation time. If anything diverges, add a thin embedded wrapper in
  `internal/kits/docker.go`. Default expectation: zero new code.
- **`Check.Fix func() error`** is the new field on `doctor.Check`. Exported but not
  JSON-serialized (the field is a closure). Most checks set it to `nil`; only
  `podman-machine` and `coredns-image` get real fixes for now.
- **macOS-vs-Linux skips return Status=OK** with a "skipped on <other-os>" message.
  Never FAIL on the wrong platform — that produces noisy `agentbox doctor` runs.
- **README at project root is short** — under 80 lines, points at `docs/VISION.md`.
  Don't duplicate doc content.
- **Final orchestration steps** (`/update-documentation`, run full test suite, write
  the completion summary, tag `v0.1.0`) are NOT implementation units — they're
  orchestration tasks the autopilot does after the implementation agent commits.

---

## Implementation Units

### Unit 1: `internal/container/docker.go` — DockerRuntime

**File**: `internal/container/docker.go` (new)

```go
package container

import (
    "errors"
    "io"
    "os/exec"
)

// DockerRuntime is a thin wrapper around PodmanRuntime that overrides the
// few methods where docker's CLI shape differs from podman's. Docker and
// podman share ~90% of the agentbox-relevant CLI surface; this file only
// contains the diffs.
type DockerRuntime struct {
    *PodmanRuntime
}

// NewDockerRuntime returns a Runtime backed by the `docker` binary.
func NewDockerRuntime() *DockerRuntime {
    return &DockerRuntime{PodmanRuntime: NewPodmanRuntime("docker")}
}

// NetworkExists overrides PodmanRuntime: docker has no `network exists`
// subcommand. Use `docker network inspect` and treat exit-non-zero as
// "absent".
func (r *DockerRuntime) NetworkExists(name string) (bool, error) {
    cmd := exec.Command(r.Bin, "network", "inspect", name)
    cmd.Stdout = io.Discard
    cmd.Stderr = io.Discard
    if err := cmd.Run(); err != nil {
        var ee *exec.ExitError
        if errors.As(err, &ee) {
            return false, nil
        }
        return false, err
    }
    return true, nil
}
```

**Implementation Notes**:
- `--ip` requires the docker network to use a non-default IPAM driver. If Phase 6's
  CoreDNS sidecar fails to assign its static IP under docker, the user will see a clear
  `docker create` error. v0.1 documents this as a known limitation: `--runtime docker`
  with `network=safe|allowlist` may not produce a working CoreDNS sidecar. Fix lands in
  a later phase OR via container-name-based DNS.
- All other methods (Create, Start, Stop, Inspect, Exec, Ls, Rm, NetworkCreate,
  NetworkRm) inherit from PodmanRuntime via embedding.

**Acceptance Criteria**:
- [ ] `NewDockerRuntime().Bin == "docker"`.
- [ ] `DockerRuntime` satisfies the `Runtime` interface (compile-time check).
- [ ] `NetworkExists("nonexistent-name")` returns `(false, nil)` against a real docker
      install (manual verification — automated tests use fake runtime).
- [ ] All embedded methods produce `docker <verb>` argv when invoked (verified by
      grepping the test trace if a real docker isn't available).

---

### Unit 2: `internal/cli/lifecycle.go` edit — runtime dispatch

**File**: `internal/cli/lifecycle.go` (edit)

The current factory:

```go
var newLifecycle = func(cfg config.Config) (*lifecycle.Lifecycle, error) {
    home, _ := os.UserHomeDir()
    rt := container.NewPodmanRuntime(cfg.Runtime)
    // ...
}
```

Replace `rt` construction:

```go
var rt container.Runtime
switch cfg.Runtime {
case "docker":
    rt = container.NewDockerRuntime()
default: // podman is the default fallback
    rt = container.NewPodmanRuntime(cfg.Runtime)
}
```

Same dispatch for the kits.Runner used by Builder. `kits.NewPodmanRunner(cfg.Runtime)`
takes the bin name; pass `"docker"` for docker. If diffs surface in `docker build`
behavior, layer a `kits/docker.go` wrapper analogous to `internal/container/docker.go`.

**Acceptance Criteria**:
- [ ] With `cfg.Runtime = "docker"`, `newLifecycle` returns a Lifecycle whose Runtime
      is `*container.DockerRuntime`.
- [ ] With `cfg.Runtime = "podman"`, returns `*container.PodmanRuntime`.
- [ ] Existing CLI tests still pass; new test case asserts the type-switch.

---

### Unit 3: `internal/doctor/doctor.go` edit — `Check.Fix` field + Fix dispatch

**File**: `internal/doctor/doctor.go` (edit)

Add the `Fix` field:

```go
// Check is a single doctor check.
type Check struct {
    Name    string         `json:"name"`
    Status  Status         `json:"status"`
    Message string         `json:"message"`
    Fix     func() error   `json:"-"` // optional remediation; called when --fix is set
}
```

Add a method on Result:

```go
// ApplyFixes calls Fix() on every check that has one and is currently FAIL or WARN.
// Returns the list of check names whose Fix was attempted, plus the first error
// encountered (continues after errors so all fixes get a chance to run).
func (r Result) ApplyFixes() (attempted []string, firstErr error) {
    for _, c := range r.Checks {
        if c.Fix == nil {
            continue
        }
        if c.Status != StatusFail && c.Status != StatusWarn {
            continue
        }
        attempted = append(attempted, c.Name)
        if err := c.Fix(); err != nil && firstErr == nil {
            firstErr = err
        }
    }
    return attempted, firstErr
}
```

**Acceptance Criteria**:
- [ ] `Check.Fix` is a `func() error` (nullable).
- [ ] `ApplyFixes` skips OK checks and skips checks with no Fix.
- [ ] When two checks both have Fix and both fail, `ApplyFixes` calls both and returns
      the first error.

---

### Unit 4: `internal/doctor/doctor.go` edit — three new checks

**File**: `internal/doctor/doctor.go` (edit)

Add three new check functions:

```go
// podmanMachineCheck — only meaningful on macOS. On Linux returns OK with a
// "skipped on Linux" message.
func podmanMachineCheck() Check {
    if runtime.GOOS != "darwin" {
        return Check{Name: "podman-machine", Status: StatusOK,
            Message: "skipped on Linux"}
    }
    cmd := exec.Command("podman", "machine", "list", "--format", "{{.Running}}")
    out, err := cmd.Output()
    if err != nil {
        return Check{Name: "podman-machine", Status: StatusFail,
            Message: "podman not installed or unable to list machines: " + err.Error(),
            Fix: func() error {
                // Try init + start.
                if err := exec.Command("podman", "machine", "init").Run(); err != nil {
                    return err
                }
                return exec.Command("podman", "machine", "start").Run()
            }}
    }
    // out contains lines of "true" / "false" per machine.
    for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
        if line == "true" {
            return Check{Name: "podman-machine", Status: StatusOK,
                Message: "podman machine running"}
        }
    }
    return Check{Name: "podman-machine", Status: StatusWarn,
        Message: "no running podman machine; agentbox containers will fail to start. Run `podman machine start` (or `agentbox doctor --fix`).",
        Fix: func() error {
            return exec.Command("podman", "machine", "start").Run()
        }}
}

// kitCacheHealthCheck — compares cache JSON files against actual podman/docker
// images. WARN if the cache references images that no longer exist (user pruned
// externally) — `agentbox build` will rebuild on next run.
func kitCacheHealthCheck(runtimeBin string) Check {
    cache, err := kits.NewCache()
    if err != nil {
        return Check{Name: "kit-cache", Status: StatusFail,
            Message: "kit cache unreachable: " + err.Error()}
    }
    entries, err := cache.ListEntries()
    if err != nil {
        return Check{Name: "kit-cache", Status: StatusWarn,
            Message: "kit cache list error: " + err.Error()}
    }
    if len(entries) == 0 {
        return Check{Name: "kit-cache", Status: StatusOK,
            Message: "kit cache empty (no kits built yet)"}
    }
    var stale []string
    for _, e := range entries {
        cmd := exec.Command(runtimeBin, "image", "inspect", e.Tag)
        cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
        if cmd.Run() != nil {
            stale = append(stale, e.Tag)
        }
    }
    if len(stale) == 0 {
        return Check{Name: "kit-cache", Status: StatusOK,
            Message: fmt.Sprintf("%d cached kits, all images present", len(entries))}
    }
    return Check{Name: "kit-cache", Status: StatusWarn,
        Message: fmt.Sprintf("%d cached kits reference %d missing images: %s. Run `agentbox build --no-cache <kit>` to rebuild.",
            len(entries), len(stale), strings.Join(stale, ", "))}
}

// mountSourcesCheck — for every running agentbox-labeled container, verify
// each bind-mount source still exists on the host.
func mountSourcesCheck(runtimeBin string) Check {
    cmd := exec.Command(runtimeBin, "ps", "-a",
        "--filter", "label=agentbox.role=box",
        "--format", "{{.Names}}")
    out, err := cmd.Output()
    if err != nil {
        return Check{Name: "mount-sources", Status: StatusOK,
            Message: "no running boxes (or runtime unreachable)"}
    }
    names := strings.Fields(strings.TrimSpace(string(out)))
    if len(names) == 0 {
        return Check{Name: "mount-sources", Status: StatusOK,
            Message: "no running boxes"}
    }

    var missing []string
    for _, name := range names {
        // Inspect each container's mounts via `runtime inspect --format ...`.
        cmd := exec.Command(runtimeBin, "inspect", name,
            "--format", "{{range .Mounts}}{{.Source}}\n{{end}}")
        out, err := cmd.Output()
        if err != nil {
            continue
        }
        for _, src := range strings.Split(strings.TrimSpace(string(out)), "\n") {
            if src == "" {
                continue
            }
            if _, err := os.Stat(src); errors.Is(err, fs.ErrNotExist) {
                missing = append(missing, fmt.Sprintf("%s: %s", name, src))
            }
        }
    }
    if len(missing) == 0 {
        return Check{Name: "mount-sources", Status: StatusOK,
            Message: fmt.Sprintf("all bind-mount sources present (%d boxes)", len(names))}
    }
    return Check{Name: "mount-sources", Status: StatusWarn,
        Message: fmt.Sprintf("%d missing mount sources: %s", len(missing), strings.Join(missing, "; "))}
}
```

Wire them into `Run`:

```go
func Run(cfg config.Config) Result {
    return Result{
        Checks: []Check{
            runtimeCheck(cfg.Runtime),
            stateDirCheck(),
            iptablesCheck(),
            ipsetCheck(),
            sudoCheck(),
            corednsImageCheck(cfg.Runtime),
            containersConfigCheck(cfg),
            podmanMachineCheck(),         // NEW
            kitCacheHealthCheck(cfg.Runtime),  // NEW
            mountSourcesCheck(cfg.Runtime),    // NEW
        },
    }
}
```

Update `corednsImageCheck` to attach a Fix func:

```go
// In corednsImageCheck — when image is missing, set Fix to pull it.
return Check{Name: "coredns-image", Status: StatusWarn,
    Message: "...",
    Fix: func() error {
        return exec.Command(runtimeBin, "pull", network.CoreDNSImage).Run()
    }}
```

**Implementation Notes**:
- `runtime.GOOS` is the standard way to gate-by-platform.
- The `mountSourcesCheck` filters by `label=agentbox.role=box` — Phase 6 added that
  label; CoreDNS sidecars and netfilter daemons have different roles and aren't
  user-facing boxes.
- `kitCacheHealthCheck` doesn't have a Fix because the right action is `agentbox
  build --no-cache <kit>`, which the user should choose per-kit.

**Acceptance Criteria**:
- [ ] Linux: `podman-machine` returns OK with "skipped on Linux"; `kit-cache` returns
      OK or WARN; `mount-sources` returns OK when no boxes.
- [ ] `agentbox doctor` shows all 10 checks now.
- [ ] When the CoreDNS image is absent, `corednsImageCheck` returns WARN with a
      non-nil Fix that pulls the image.

---

### Unit 5: `internal/cli/doctor.go` edit — `--fix` flag wiring

**File**: `internal/cli/doctor.go` (edit)

The current RunE:

```go
RunE: func(cmd *cobra.Command, args []string) error {
    res, err := loadConfig()
    if err != nil { return err }
    result := doctor.Run(res.Config)
    if global.JSON { ... }
    // print human output, return error if AnyFail.
}
```

Add `--fix` handling. After running checks once, if `--fix` is set, apply fixes and
re-run:

```go
RunE: func(cmd *cobra.Command, args []string) error {
    res, err := loadConfig()
    if err != nil { return err }

    result := doctor.Run(res.Config)

    if fix && (result.AnyFail() || hasWarnWithFix(result)) {
        attempted, fixErr := result.ApplyFixes()
        fmt.Fprintf(cmd.OutOrStdout(), "applied fixes: %s\n", strings.Join(attempted, ", "))
        if fixErr != nil {
            fmt.Fprintf(cmd.ErrOrStderr(), "fix error: %v\n", fixErr)
        }
        // Re-run checks to show the new state.
        result = doctor.Run(res.Config)
    }

    // (existing output logic — print human or JSON)
}

func hasWarnWithFix(r doctor.Result) bool {
    for _, c := range r.Checks {
        if c.Status == doctor.StatusWarn && c.Fix != nil {
            return true
        }
    }
    return false
}
```

**Acceptance Criteria**:
- [ ] `agentbox doctor --fix` calls Fix on each WARN/FAIL check that has one.
- [ ] After a fix attempt, doctor re-runs checks and reports the new state.
- [ ] Without `--fix`, behavior is unchanged (just reports).

---

### Unit 6: `internal/doctor/doctor_test.go` extensions

**File**: `internal/doctor/doctor_test.go` (edit)

Tests:
- `TestApplyFixes_SkipsOKChecks` — Result with OK + WARN+Fix + FAIL+Fix; verify only
  the latter two run.
- `TestApplyFixes_ReturnsFirstError` — two failing fixes; verify first error returned,
  both attempted.
- `TestPodmanMachineCheck_LinuxSkipsOK` — using `runtime.GOOS == "linux"`, returns OK
  with "skipped" message.
- `TestKitCacheHealthCheck_EmptyCache` — `t.Setenv("XDG_DATA_HOME", t.TempDir())`;
  no entries; returns OK.
- `TestMountSourcesCheck_NoRunningBoxes` — runtime command returns empty; OK.

**Acceptance Criteria**:
- [ ] `go test ./internal/doctor/...` passes.

---

### Unit 7: `README.md` — project root README

**File**: `README.md` (new, at project root)

```markdown
# agentbox

Single-user CLI for running AI coding agents inside per-project Podman containers.

`agentbox run` ensures a per-project Podman container exists for the current directory,
bind-mounts the project at the same path inside the box, and drops you into a zellij
session where the configured agent (Claude Code, Codex, opencode) is already running
with permissions disabled. Detach, walk away, come back hours later, reattach. The
project, agent config, and shell history persist on the host. The container itself is
disposable.

This is calibrated for one user — me, the author. Decisions favor my workflow over
generality.

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
2. [docs/SPEC.md](docs/SPEC.md) — every technical decision (config, runtime spec,
   labels, mounts, exit codes).
3. [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — how the pieces fit (lifecycle, kit
   pipeline, network topology).
4. [docs/CLI.md](docs/CLI.md) — command surface, flags, identifier resolution.
5. [docs/KITS.md](docs/KITS.md) — kit format and composition.
6. [docs/ROADMAP.md](docs/ROADMAP.md) — phase order and per-phase test checkpoints.

## Status

v0.1.0 — Linux primary. macOS via `podman machine` supported. Docker as a runtime
fallback. Tested against rootless podman 4.x. See `docs/PROGRESS.md` for the build
history.

## License

(set at tag time — recommend MIT or Apache-2.0 for a personal-use tool.)
```

**Acceptance Criteria**:
- [ ] `README.md` exists at the project root.
- [ ] Under 80 lines.
- [ ] Links to all 6 docs files in docs/.
- [ ] Quickstart section has 4 steps that work as written.

---

### Unit 8: `internal/cli/cli_test.go` extensions

**File**: `internal/cli/cli_test.go` (extend)

- `TestNewLifecycle_PodmanRuntime` — `cfg.Runtime = "podman"` produces lifecycle with
  `*container.PodmanRuntime`.
- `TestNewLifecycle_DockerRuntime` — `cfg.Runtime = "docker"` produces lifecycle with
  `*container.DockerRuntime`.
- `TestDoctor_Fix_NoFlag_NoFixCalls` — without `--fix`, no Fix funcs invoked even on
  WARN/FAIL.
- `TestDoctor_Fix_Flag_CallsFixes` — with `--fix`, Fix funcs invoked.

**Acceptance Criteria**:
- [ ] All new tests pass.
- [ ] Existing cli_test.go tests still pass.

---

### Unit 9 (orchestration, not code): `/update-documentation` pass

After units 1-8 are committed and verified, run `/update-documentation` to align
SPEC.md / ARCHITECTURE.md / CLI.md / KITS.md / VISION.md with implemented reality.

Specific known divergences from PROGRESS.md decision logs:
- **SPEC.md `[runtime.containers]` → `[containers]`** (Phase 1 design rename; SPEC still
  shows the old shape).
- **SPEC.md "Network modes" Corefile examples** — Phase 6 used:
  - `log . { class all }` (NOT `log /var/log/coredns/queries.log { class denial success }`)
  - `template IN ANY` (NOT `template ANY ANY`)
  - SPEC.md's example Corefile snippets in the "safe" section need to match what the
    code actually generates.
- **ARCHITECTURE.md "Network architecture"** — Phase 6 ships THREE containers per box
  in safe/allowlist mode (box + coredns sidecar + netfilter daemon). The diagrams
  should reflect this. The "ipset and iptables on the host" section can be expanded.
- **CLI.md `box info` example** — verify it matches Phase 4's actual output (it should;
  Phase 4 used CLI.md as the source).
- **KITS.md "Default kit catalog"** — verify each kit's description matches its
  manifest.toml. Phase 5 finalized those.
- **SPEC.md "Open / deferred"** — the "agentbox init for first-run UX" item is
  effectively done via `agentbox doctor --fix`. The "ttyd in the kit for browser-based
  attach" stays out of scope. The "Per-agent YOLO flags" item is fully resolved (Phase 5
  Part C verified them and pinned values in DefaultConfig).
- **VISION.md** doesn't need updates — it's intentionally philosophical and ages well.

This is invoked as a Skill, not implemented as a unit.

**Acceptance Criteria**:
- [ ] All four core docs (SPEC, ARCH, CLI, KITS) reviewed and any Phase 1-7 deviations
      reflected.
- [ ] PROGRESS.md "Suggested Additions" section either resolved or moved to ROADMAP.md
      as future work.

---

### Unit 10 (orchestration): final verification + completion summary + tag

After units 1-9, the orchestration finishes with:

1. `go vet ./... && go test ./... && make build` — final green run.
2. Run the Phase 8 ROADMAP test checkpoint:
   ```sh
   ./agentbox doctor --json | jq '[.checks[] | select(.status != "ok")] | length'
   ./agentbox completion zsh > /tmp/comp && grep -q '_agentbox' /tmp/comp
   ./agentbox --runtime docker ls
   ./agentbox --runtime docker run --no-attach && ./agentbox --runtime docker rm .
   ```
   The `--runtime docker` checks require docker installed; document as
   manual-verification if it isn't on the dev machine.
3. Update `docs/PROGRESS.md`:
   - Status: `complete`.
   - Mark Phase 8 done.
   - Add a "Completion Summary" section at the top of the file:
     ```
     ## Completion Summary

     **Total phases completed:** 8/8
     **Total refactor passes:** 0 (gate evaluated 5 times; skipped each time —
       documented reasoning in Refactor Log)
     **Total commits:** ~35 across 9 design docs + 8 phase implementations + multiple
       hot-fixes + progress updates.

     ### Deviations from the original ROADMAP
     - SPEC's `[runtime.containers]` was renamed to `[containers]` (Phase 1 — invalid TOML key collision).
     - Default config's `default_kits` was temporarily trimmed in Phase 5 Part C (no
       containers kit yet) and restored in Phase 7.
     - Phase 6 used CoreDNS 1.14.3's stdout-only log plugin, requiring `box net` and the
       netfilter daemon to read via `podman logs --follow` instead of a file.
     - Phase 8's `--runtime docker` keeps `--ip` in network create but documents that
       docker may reject it on default IPAM (Phase 9+ would fix).

     ### Known issues
     - macOS smoke test deferred to user verification (no Apple Silicon CI).
     - `--runtime docker` smoke test deferred (docker not on dev machine).
     - Port forwarding from box to host (out of scope for v0.1; tracked in ROADMAP).
     - Shared rootless image cache across boxes (out of scope for v0.1).

     ### What ships
     - 12 built-in kits (base + 6 single-language + polyglot + 3 agents + containers).
     - DNS-level (Phase 6 Part A) + IP-level (Phase 6 Part B) network filtering for
       safe and allowlist modes.
     - Nested rootless podman (`docker run` from inside the box) with a bundled
       seccomp profile.
     - Two binaries: `agentbox` and `agentbox-netfilter`.
     ```
4. `git tag v0.1.0` — the symbolic completion of the roadmap.

**Acceptance Criteria**:
- [ ] `go test ./...` green.
- [ ] PROGRESS.md "Status: complete" + Completion Summary present.
- [ ] `git tag` shows `v0.1.0`.

---

## Implementation Order

Layer 1 (parallel-safe Go code):
1. Unit 1 — `internal/container/docker.go` (small, embedded wrapper).
2. Unit 2 — `internal/cli/lifecycle.go` runtime dispatch.
3. Unit 3 — `Check.Fix` field + `Result.ApplyFixes`.

Layer 2 (depends on Layer 1):
4. Unit 4 — three new doctor checks.
5. Unit 5 — `--fix` CLI wiring.
6. Unit 6 — doctor tests.
7. Unit 7 — README.md.
8. Unit 8 — cli_test extensions.

Layer 3 (orchestration, after agent commits):
9. Unit 9 — `/update-documentation` pass.
10. Unit 10 — final verification, completion summary, tag.

One Sonnet agent for units 1-8 (all code changes). The orchestrator runs units 9-10
itself.

---

## Verification Checklist

```sh
cd /home/nathan/dev/agent-box
go vet ./...
go test ./...
make build

# Domain purity (no new violations).
grep -rn 'spf13/cobra\|internal/cli' \
  internal/container/ internal/doctor/ internal/network/ internal/runspec/ \
  internal/lifecycle/ internal/kits/ internal/builtinkits/

# All 10 doctor checks listed.
./agentbox doctor 2>&1 | head -15

# --fix flag wired.
./agentbox doctor --fix 2>&1 | head -15  # safe even when nothing to fix

# DockerRuntime is a Runtime (compile-time check).
go build ./internal/container/

# README links work.
ls README.md
grep -q 'docs/VISION.md' README.md

# Phase 8 test checkpoint (Linux).
./agentbox doctor --json | jq '[.checks[] | select(.status != "OK")] | length'  # → 0 ideally; some WARNs are expected (sudo, podman-machine on Linux)
./agentbox completion zsh > /tmp/comp && grep -q '_agentbox' /tmp/comp && echo "completion ok"
# --runtime docker requires docker installed; defer if absent.
```

The macOS smoke test + the `--runtime docker` end-to-end are **manual user
verification** — the dev machine may not have either. The orchestration's verification
focuses on:
- Code compiles + tests pass.
- Doctor enumerates 10 checks.
- README is in place.
- DockerRuntime satisfies the Runtime interface.

Phase 8 is "done" when:
- All units committed.
- `/update-documentation` ran and propagated the deviations.
- `docs/PROGRESS.md` status is `complete` with the Completion Summary block.
- `git tag v0.1.0` is applied.
