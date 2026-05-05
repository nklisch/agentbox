# Design: Phase 7 — `containers` kit + nested rootless podman + `[runtime.containers]`

## Overview

Phase 7 lets agents spin up inner containers from inside an agentbox box: `docker run`,
`docker compose up`, the whole flow. No `--privileged`. No host docker socket mount.
The box gets enough re-granted capabilities + a custom seccomp profile to run rootless
podman *inside* the rootless agentbox container, and the inner containers share the box's
network namespace so Phase 6's CoreDNS + iptables filtering extends through to them.

This is the last feature phase before Phase 8's ship work. It's a content phase plus a
runtime-spec extension:

- **One new kit** (`containers`) that installs podman/buildah/skopeo/compose binaries
  and writes inner-podman storage + registries config.
- **Three runspec extensions** (`Devices`, `CapAdd`, plus a host-side seccomp profile
  bind mount) that get populated when `cfg.Containers.Enable` is true.
- **One new internal package** (`internal/seccomp`) that embeds the bundled containers
  seccomp JSON via `//go:embed` and writes it to a stable host path on first use.
- **One doctor warning** for the common mistake of having `containers` in `default_kits`
  but `runtime.containers.enable = false`.
- **One DefaultConfig nudge:** reinstate `"containers"` in `DefaultKits` (Phase 5 Part C
  trimmed it). `Containers.Enable` stays `false` by default — opt-in for the runtime
  privileges, doctor flags it, user reads + flips.

## Cross-cutting decisions

- **Runspec extensions** are additive — `Devices []string` + `CapAdd []string` fields
  on `PodmanCreateArgs` + `Mounts` extension. Empty slices when `!Containers.Enable`.
  ToShell emits `--device` / `--cap-add` lines only when the slices are non-empty.
- **Seccomp profile lives host-side, embedded in the binary.** `internal/seccomp` ships
  the JSON via `//go:embed`. On first use of containers mode, agentbox writes it to
  `<state-dir>/seccomp/containers.json` (idempotent — only writes if missing or
  outdated). The path is bind-mounted ro into the box at
  `/etc/agentbox/seccomp/containers.json`. SecOpt references the in-container path.
- **`docker → podman` translation via shim, not bash alias.** Bash aliases don't expand
  in non-interactive shells (which is what `agentbox exec . docker run ...` produces).
  The kit installs a tiny shim at `/usr/local/bin/docker` that `exec podman "$@"`. Aliases
  + env-var still set for muscle memory in interactive shells, but the shim is what
  makes scripted `docker` calls work.
- **`DOCKER_HOST` set in env.sh** to `unix:///run/user/0/podman/podman.sock` (the box
  runs as root; uid 0). The socket isn't auto-started in a non-systemd container; users
  need to run `podman system service` once if they want the API socket. For the test
  checkpoint, `docker compose up` works against the local podman binary (which doesn't
  need the API socket — `docker compose` is a Go binary that drives the docker CLI).
- **DefaultConfig.DefaultKits** goes back to `["polyglot", "containers", "claude"]`.
  `Containers.Enable` stays `false`. doctor's new check fires when those two are out
  of sync, with an actionable message.
- **Inner-container network namespace sharing.** rootless podman inside the box uses
  pasta or slirp4netns by default. The default sharing model (host-network for the inner
  container = the box's network namespace) means inner containers go through the box's
  CoreDNS sidecar and iptables filter automatically. We don't have to do anything extra
  for this — it's how rootless nested containers naturally behave.

---

## Implementation Units

### Unit 1: `internal/runspec/runspec.go` edit — add `Devices` + `CapAdd`

**File**: `internal/runspec/runspec.go` (edit)

Add to `PodmanCreateArgs`:

```go
type PodmanCreateArgs struct {
    // ... existing ...
    CapDrop []string
    CapAdd  []string  // NEW: --cap-add <X>; populated when Containers.Enable
    SecOpt  []string
    Devices []string  // NEW: --device <X>; populated when Containers.Enable
    // ... existing ...
}
```

In `BuildPodmanCreateArgs`, replace the existing `if cfg.Containers.Enable` block with:

```go
if cfg.Containers.Enable {
    // Re-grant the small slice of caps + devices nested rootless podman needs.
    // CapDrop ALL stays; CapAdd layers specific caps back on top.
    args.CapAdd = append(args.CapAdd, cfg.Containers.ExtraCaps...)
    args.Devices = append(args.Devices, cfg.Containers.ExtraDevices...)
    args.SecOpt = append(args.SecOpt,
        "seccomp=/etc/agentbox/seccomp/containers.json",
        "unmask=/proc/sys/net/ipv4",
    )
}
```

(Note: Phase 1's defaults already populate `cfg.Containers.ExtraCaps = ["SETUID","SETGID"]`
and `cfg.Containers.ExtraDevices = ["/dev/fuse"]`, so this just plumbs them through.)

In `ToShell`, render the new fields. Insert after the `CapDrop` loop and before `SecOpt`:

```go
for _, c := range p.CapAdd {
    fmt.Fprintf(&b, "  --cap-add %s \\\n", c)
}
for _, d := range p.Devices {
    fmt.Fprintf(&b, "  --device %q \\\n", d)
}
```

Order matters per CLI semantics: `--cap-drop ALL --cap-add SETUID --cap-add SETGID` is
the canonical drop-then-add pattern.

**Acceptance Criteria**:
- [ ] `BuildPodmanCreateArgs(cfg with Containers.Enable=true)` returns args with
      `CapAdd = ["SETUID","SETGID"]` and `Devices = ["/dev/fuse"]`.
- [ ] With `Enable=false`, both slices are empty.
- [ ] `ToShell` output for an enabled config contains `--cap-add SETUID`,
      `--cap-add SETGID`, `--device "/dev/fuse"`.
- [ ] `ToShell` output preserves order: cap-drop lines first, cap-add lines next, then
      security-opt lines.

---

### Unit 2: `internal/container/podman.go` edit — render new fields

**File**: `internal/container/podman.go` (edit)

In `PodmanRuntime.Create`, after the existing `CapDrop` loop, add:

```go
for _, c := range args.CapAdd {
    argv = append(argv, "--cap-add", c)
}
for _, d := range args.Devices {
    argv = append(argv, "--device", d)
}
```

Order matches `ToShell` (cap-drop, cap-add, then security-opt loops).

**Acceptance Criteria**:
- [ ] PodmanRuntime.Create with Containers.Enable=true args appends
      `--cap-add SETUID --cap-add SETGID --device /dev/fuse` to argv.
- [ ] No new tests required; covered indirectly by runspec_test changes (Unit 1).

---

### Unit 3: `internal/seccomp/seccomp.go` (new package)

**File**: `internal/seccomp/seccomp.go`

```go
package seccomp

import (
    _ "embed"
    "fmt"
    "os"
    "path/filepath"

    "github.com/nklisch/agentbox/internal/state"
)

//go:embed containers.json
var containersJSON []byte

// ContainersJSONBytes returns the embedded seccomp profile as bytes.
// Tests use this directly to verify the profile is parseable JSON.
func ContainersJSONBytes() []byte {
    return containersJSON
}

// EnsureContainersProfile writes the embedded containers seccomp JSON to a
// stable host path and returns that path. Idempotent: writes only when the
// file is missing or its content differs from the embedded version.
//
// The host path is <state-dir>/seccomp/containers.json so it lives alongside
// other agentbox state and is removed by `agentbox doctor --fix prune` (a
// future addition; for now it persists across runs which is what we want).
func EnsureContainersProfile() (string, error) {
    base, err := state.Dir()
    if err != nil {
        return "", err
    }
    dir := filepath.Join(base, "seccomp")
    if err := state.EnsureDir(dir); err != nil {
        return "", fmt.Errorf("ensure seccomp dir: %w", err)
    }
    path := filepath.Join(dir, "containers.json")
    if needsWrite(path, containersJSON) {
        if err := os.WriteFile(path, containersJSON, 0o644); err != nil {
            return "", fmt.Errorf("write %s: %w", path, err)
        }
    }
    return path, nil
}

// needsWrite returns true when path is missing or differs from want.
func needsWrite(path string, want []byte) bool {
    have, err := os.ReadFile(path)
    if err != nil {
        return true
    }
    if len(have) != len(want) {
        return true
    }
    for i := range have {
        if have[i] != want[i] {
            return true
        }
    }
    return false
}
```

**Implementation Notes**:
- `containers.json` is the embedded file. Source: copy podman's default seccomp
  (`https://raw.githubusercontent.com/containers/common/main/pkg/seccomp/seccomp.json`)
  and add the following syscalls to the default-allow list (or remove from the
  default-deny rules): `clone3`, `mount`, `unshare`, `umount2`, `pivot_root`,
  `setdomainname`, `keyctl`, `pivot_root`. **Verify the canonical podman default
  at write time** — the URL may have moved, the JSON schema may have changed.
- The profile mode is `0o644` so the user inside the box (which is root) and the
  podman runtime can both read it.

**Acceptance Criteria**:
- [ ] `internal/seccomp/containers.json` exists and is valid JSON (parseable).
- [ ] `ContainersJSONBytes()` returns non-empty bytes that include the four added syscalls.
- [ ] `EnsureContainersProfile()` writes to `<state-dir>/seccomp/containers.json` on
      first call, returns the path; second call is a no-op if content matches.
- [ ] If the on-disk file is corrupted (truncated/different), the next call
      overwrites it.

---

### Unit 4: `internal/seccomp/seccomp_test.go`

**File**: `internal/seccomp/seccomp_test.go`

```go
package seccomp_test

import (
    "encoding/json"
    "os"
    "path/filepath"
    "strings"
    "testing"

    "github.com/nklisch/agentbox/internal/seccomp"
)

func TestContainersJSONBytes_ValidJSON(t *testing.T) {
    var v map[string]interface{}
    if err := json.Unmarshal(seccomp.ContainersJSONBytes(), &v); err != nil {
        t.Fatalf("containers.json is not valid JSON: %v", err)
    }
    if _, ok := v["defaultAction"]; !ok {
        t.Error("seccomp profile missing 'defaultAction' field")
    }
}

func TestContainersJSONBytes_AddsClone3(t *testing.T) {
    s := string(seccomp.ContainersJSONBytes())
    if !strings.Contains(s, "clone3") {
        t.Error("seccomp profile does not mention clone3 (expected in allow list)")
    }
}

func TestEnsureContainersProfile(t *testing.T) {
    t.Setenv("XDG_DATA_HOME", t.TempDir())
    path, err := seccomp.EnsureContainersProfile()
    if err != nil {
        t.Fatalf("EnsureContainersProfile: %v", err)
    }
    if !strings.HasSuffix(path, "/seccomp/containers.json") {
        t.Errorf("unexpected path: %s", path)
    }
    if _, err := os.Stat(path); err != nil {
        t.Errorf("file not created: %v", err)
    }
    // Second call should succeed too (idempotent).
    if _, err := seccomp.EnsureContainersProfile(); err != nil {
        t.Errorf("second EnsureContainersProfile: %v", err)
    }
}

func TestEnsureContainersProfile_OverwritesCorrupted(t *testing.T) {
    t.Setenv("XDG_DATA_HOME", t.TempDir())
    path, _ := seccomp.EnsureContainersProfile()
    // Corrupt.
    _ = os.WriteFile(path, []byte("garbage"), 0o644)
    // Re-call should overwrite.
    if _, err := seccomp.EnsureContainersProfile(); err != nil {
        t.Fatal(err)
    }
    body, _ := os.ReadFile(path)
    if string(body) == "garbage" {
        t.Error("EnsureContainersProfile did not overwrite corrupted file")
    }
}
```

**Acceptance Criteria**:
- [ ] All four tests pass.
- [ ] No external network or runtime needed.

---

### Unit 5: `internal/lifecycle/lifecycle.go` edit — wire seccomp + mount

**File**: `internal/lifecycle/lifecycle.go` (edit)

In `createBox`, after `state.EnsureSession` and before `runspec.BuildPodmanCreateArgs`,
ensure the seccomp profile exists on disk when containers mode is enabled:

```go
// If containers.enable, materialize the seccomp profile on disk so the
// runtime spec's bind-mount has a target.
if l.Cfg.Containers.Enable {
    seccompPath, err := seccomp.EnsureContainersProfile()
    if err != nil {
        return container.Box{}, exitcode.Wrap(exitcode.Generic, err)
    }
    in.SeccompPath = seccompPath
}
```

Add `SeccompPath string` to `runspec.BuildInput`:

```go
type BuildInput struct {
    // ... existing ...
    SeccompPath string // host path to bundled containers.json; populated by lifecycle when Containers.Enable
}
```

In `BuildPodmanCreateArgs`, add the bind mount when `cfg.Containers.Enable && in.SeccompPath != ""`:

```go
if cfg.Containers.Enable && in.SeccompPath != "" {
    args.Mounts = append(args.Mounts, Mount{
        Source: in.SeccompPath,
        Target: "/etc/agentbox/seccomp/containers.json",
        Mode:   "ro",
    })
}
```

(The `args.SecOpt` already includes `seccomp=/etc/agentbox/seccomp/containers.json` from
the existing Phase 1 block.)

Add the `seccomp` import to lifecycle.go.

**Acceptance Criteria**:
- [ ] With `Containers.Enable=true`, `BuildInput.SeccompPath` is populated by lifecycle.
- [ ] With `SeccompPath` set, args.Mounts contains a ro mount of the host JSON to the
      in-container path referenced by SecOpt.
- [ ] Without `Containers.Enable`, no seccomp mount appears.

---

### Unit 6: `internal/builtinkits/kits/containers/` — the kit content

**File**: `internal/builtinkits/kits/containers/`

Standard kit shape (manifest, packages, install, env). Verified against current
upstreams at write time.

#### `manifest.toml`

```toml
name        = "containers"
description = "Nested rootless podman + buildah + skopeo + docker-compose"
depends_on  = ["base"]
provides    = ["containers"]
```

#### `packages.txt`

```
podman
buildah
skopeo
fuse-overlayfs
slirp4netns
uidmap
crun
catatonit
```

(`crun` is the rootless OCI runtime; `catatonit` is the init process. Both common in
podman setups.)

#### `install.sh` structure

- Pinned versions: `PODMAN_COMPOSE_VERSION`, `DOCKER_COMPOSE_VERSION`. Verify upstream.
- `set -euo pipefail` + arch detection (copy from base).
- Sections:
  1. **podman-compose** via pip: `pip3 install --break-system-packages --no-cache-dir "podman-compose==${PODMAN_COMPOSE_VERSION}"`. (Some distros have it in apt; pip is more reproducible.) Verify package exists.
  2. **docker-compose** (the standalone Go binary, distinct from the `docker compose` plugin): download from `https://github.com/docker/compose/releases/download/v${DOCKER_COMPOSE_VERSION}/docker-compose-linux-${ARCH}` to `/usr/local/bin/docker-compose`, chmod +x.
  3. **`/usr/local/bin/docker` shim**: write a tiny script that `exec podman "$@"`. Marks the file +x. This is how `agentbox exec . docker run` actually translates to podman without relying on bash alias expansion.
  4. **`/etc/containers/storage.conf`**: write the rootless-fuse-overlayfs config. Sample (verify against current upstream — `podman info | grep -A5 graphRoot`):
     ```toml
     [storage]
     driver = "overlay"
     runroot = "/run/containers/storage"
     graphroot = "/var/lib/containers/storage"

     [storage.options]
     mount_program = "/usr/bin/fuse-overlayfs"
     ```
     The mount_program tells podman to use fuse-overlayfs, which is what makes nested
     rootless work without kernel overlay support.
  5. **`/etc/containers/registries.conf`**: standard upstream defaults — `unqualified-search-registries = ["docker.io", "quay.io", "ghcr.io"]`. Don't roll our own; copy verbatim from upstream.
  6. **zsh env.d bridge** — same idempotent block as Part A kits (already in base from
     Part A follow-up, but include defensively).

#### `env.sh`

```bash
# containers kit env. Exports + PATH only per KITS.md.

# Docker-compatible socket path (for the agentbox container running as root).
# The socket isn't auto-started; run `podman system service --time=0
# unix:///run/user/0/podman/podman.sock &` if you need the Docker API.
export DOCKER_HOST="unix:///run/user/0/podman/podman.sock"

# Friendly aliases for interactive shells. Non-interactive scripts use the
# /usr/local/bin/docker shim from install.sh — aliases aren't expanded there.
alias docker='podman'
alias docker-compose='podman-compose'
```

(The `alias` line in env.sh is a bit of a contract bend — KITS.md says "exports + PATH
only." Aliases are also valid in shell rc; document this exception.)

**Acceptance Criteria**:
- [ ] `agentbox build containers` exits 0.
- [ ] Built image has on PATH: `podman`, `buildah`, `skopeo`, `podman-compose`,
      `docker` (shim), `docker-compose`, `fuse-overlayfs`, `slirp4netns`, `newuidmap`,
      `newgidmap`, `crun`.
- [ ] `/etc/containers/storage.conf` exists and contains `mount_program = "/usr/bin/fuse-overlayfs"`.
- [ ] `/etc/containers/registries.conf` exists with the standard search-registries list.
- [ ] `chmod +x /usr/local/bin/docker` set; running `docker --version` invokes podman.

---

### Unit 7: `internal/config/config.go` edit — reinstate `containers` in DefaultKits

**File**: `internal/config/config.go` (edit)

Find:

```go
DefaultKits: []string{"polyglot", "claude"},  // Phase 5 trim
```

Restore Phase 1's full default:

```go
DefaultKits: []string{"polyglot", "containers", "claude"},
```

Update `internal/config/config_test.go`'s assertion accordingly.

**Note**: `Containers.Enable` stays `false`. The user has to opt into runtime privileges
by editing config (or future `agentbox doctor --fix` could prompt).

**Acceptance Criteria**:
- [ ] `DefaultConfig().DefaultKits == ["polyglot", "containers", "claude"]`.
- [ ] `DefaultConfig().Containers.Enable == false`.
- [ ] `internal/config/config_test.go` covers both.

---

### Unit 8: `internal/doctor/doctor.go` edit — containers-mismatch warning

**File**: `internal/doctor/doctor.go` (edit)

Add a check that fires when `containers` is in `DefaultKits` but `Containers.Enable` is
false:

```go
func containersConfigCheck(cfg config.Config) Check {
    hasKit := false
    for _, k := range cfg.DefaultKits {
        if k == "containers" {
            hasKit = true
            break
        }
    }
    if !hasKit {
        return Check{Name: "containers-config", Status: StatusOK,
            Message: "containers kit not in default_kits; nested-container support disabled by default"}
    }
    if cfg.Containers.Enable {
        return Check{Name: "containers-config", Status: StatusOK,
            Message: "containers kit + runtime.containers.enable=true; nested rootless ready"}
    }
    return Check{Name: "containers-config", Status: StatusWarn,
        Message: "containers kit is in default_kits but runtime.containers.enable=false; " +
            "nested docker/podman commands inside boxes will fail. Set [containers] enable=true " +
            "in ~/.config/agentbox/config.toml to grant the runtime privileges (requires understanding " +
            "the security trade-offs documented in SPEC.md)."}
}
```

Wire it into `Run` alongside the existing checks.

**Acceptance Criteria**:
- [ ] `Run({DefaultKits: ["containers"], Containers.Enable: false})` returns a Check
      with `Status == StatusWarn` and a message mentioning "enable=true".
- [ ] `Run({DefaultKits: ["containers"], Containers.Enable: true})` returns
      `Status == StatusOK`.
- [ ] `Run({DefaultKits: ["polyglot"], Containers.Enable: false})` returns
      `Status == StatusOK` (no kit → no mismatch to warn about).

---

### Unit 9: Tests — runspec extensions + lifecycle seccomp wiring

**Files**: `internal/runspec/runspec_test.go`, `internal/lifecycle/lifecycle_test.go`

For runspec:
- `TestBuildPodmanCreateArgs_ContainersEnable_AddsCapAndDevices` — assert
  `args.CapAdd == ["SETUID","SETGID"]` and `args.Devices == ["/dev/fuse"]` when
  `cfg.Containers.Enable = true`.
- `TestBuildPodmanCreateArgs_ContainersEnable_NoSeccompMountWithoutPath` — without
  `BuildInput.SeccompPath`, no seccomp mount appears (lifecycle is responsible for
  populating SeccompPath; runspec doesn't compute it).
- `TestBuildPodmanCreateArgs_ContainersEnable_WithSeccompPath_AddsMount` — with
  SeccompPath populated, args.Mounts contains the seccomp ro mount.
- `TestToShell_ContainersEnable_HasCapAddAndDevice` — output contains
  `--cap-add SETUID`, `--cap-add SETGID`, `--device "/dev/fuse"`.

For lifecycle:
- `TestEnsureBox_ContainersEnable_PopulatesSeccompPath` — fakeRuntime + fake seccomp
  call hook. Verifies that when `Cfg.Containers.Enable == true`, lifecycle calls
  `seccomp.EnsureContainersProfile()` and threads the result into the runspec.

**Acceptance Criteria**:
- [ ] `go test ./internal/runspec/... ./internal/lifecycle/... ./internal/seccomp/...` passes.

---

## Implementation Order

| # | Unit | Depends on |
|---:|------|-----------|
| 1 | runspec.go edit (CapAdd + Devices fields + ToShell rendering) | — |
| 2 | container/podman.go edit (--cap-add + --device argv) | Unit 1 |
| 3 | internal/seccomp/seccomp.go (new package + embed) | — |
| 4 | internal/seccomp/seccomp_test.go | Unit 3 |
| 5 | lifecycle.go edit (EnsureContainersProfile + SeccompPath wiring) | Units 1, 3 |
| 6 | kits/containers/* (manifest, packages, install.sh, env.sh) | — (parallel-safe with 1-5) |
| 7 | config.DefaultKits edit (re-add containers) + config_test.go | — |
| 8 | doctor.go edit (containers-config check) | Unit 7 |
| 9 | Tests for runspec + lifecycle | Units 1, 5 |

One Sonnet agent for all 9 units — patterns are familiar from Phase 5 (kit content) and
Phases 1/3 (runspec extensions). ~600-800 LOC total.

---

## Verification Checklist

```sh
cd /home/nathan/dev/agent-box
go vet ./...
go test ./...
go build ./...
make build

# Domain purity
grep -rn 'spf13/cobra\|internal/cli' \
  internal/seccomp/ internal/builtinkits/kits/containers/

# All 12 kits listed (existing 11 + containers).
./agentbox build --list | grep -q '^containers' && echo "containers listed"

# Build the kit (slow first time — apt + pip).
./agentbox build containers 2>&1 | tail -3

# Verify tools in image.
TAG=$(./agentbox build containers 2>&1 | grep -oP 'agentbox/[a-f0-9]{12}' | head -1)
podman run --rm "localhost/$TAG" zsh -lc '
  podman --version; buildah --version; skopeo --version
  podman-compose --version; docker --version; docker-compose --version
  which fuse-overlayfs slirp4netns newuidmap crun
'

# doctor's new check.
./agentbox doctor 2>&1 | grep containers-config

# ROADMAP Phase 7 test checkpoint — requires Containers.Enable=true.
mkdir -p /tmp/abx-net && cd /tmp/abx-net && touch x
cat > .agentbox.toml <<'EOF'
[agents.claude]
kits = ["polyglot", "containers"]
cmd = ["zsh"]

[containers]
enable = true
EOF
/home/nathan/dev/agent-box/agentbox run --kits polyglot,containers,claude --no-attach 2>&1 | tail -3
/home/nathan/dev/agent-box/agentbox exec . docker run --rm hello-world 2>&1 | head -10
/home/nathan/dev/agent-box/agentbox exec . sh -c 'docker run --rm alpine ip route'

cat > compose.yml <<'EOF'
services:
  redis:
    image: redis:7-alpine
EOF
/home/nathan/dev/agent-box/agentbox exec -w /tmp/abx-net . docker compose up -d 2>&1 | tail -5
/home/nathan/dev/agent-box/agentbox exec . docker ps
/home/nathan/dev/agent-box/agentbox exec . sh -c 'docker exec $(docker ps -q) redis-cli ping'
/home/nathan/dev/agent-box/agentbox exec -w /tmp/abx-net . docker compose down

/home/nathan/dev/agent-box/agentbox rm .
rm -rf /tmp/abx-net
```

**Common gotchas to debug if test checkpoint fails:**
- `docker run hello-world` fails with "permission denied on /dev/fuse": Containers.Enable
  not set, so `--device /dev/fuse` wasn't added.
- `docker run hello-world` fails with "newuidmap: Permission denied": uidmap package
  missing OR SETUID/SETGID caps not granted.
- `docker run hello-world` fails with "operation not permitted (clone3)": seccomp profile
  not mounted, OR the bundled profile doesn't have clone3 in the allow list.
- `docker compose up` fails to find `compose`: docker-compose binary not on PATH (the
  install.sh's GitHub-release download step failed silently).
- Inner container's `ip route` shows no default: outer agentbox network is `off` mode.
  Test checkpoint requires `safe` or `open` mode.

Phase 7 is "done" for autopilot purposes when:
- `agentbox build containers` succeeds and tools are on PATH.
- `agentbox doctor` shows the `containers-config` check.
- The ROADMAP test checkpoint passes end-to-end (hello-world + redis compose).
- `go test ./...` is green.
