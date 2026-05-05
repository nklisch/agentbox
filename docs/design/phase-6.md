# Design: Phase 6 — Network policy (`safe` + `allowlist` + CoreDNS sidecar)

## Overview

Phase 6 turns `network.mode = "safe"` from "warns and falls back to open" into a real
defense: each project box gets its own podman network with a CoreDNS sidecar that
forwards to a threat-feed-filtered upstream (Quad9 by default), an optional iptables +
ipset egress filter that drops traffic to IPs not resolved through CoreDNS, and a
`box net` helper that shows what's been queried and which queries got blocked.
Allowlist mode reuses the same plumbing but locks resolution to an explicit hostname
list — anything else returns NXDOMAIN.

**Two parts:**
- **Part A — DNS-level filtering.** New `internal/network` package with a Corefile
  generator, a sidecar lifecycle (CoreDNS container per box), and a Manager that
  orchestrates podman network creation + sidecar start. Lifecycle integration replaces
  Phase 3's `degradeNetworkMode` with a real setup. After Part A, `safe` mode blocks
  domains that the threat feed knows about; `allowlist` mode only resolves the explicit
  list. Direct-IP traffic is NOT yet blocked.
- **Part B — IP-level filtering.** A small per-box host process (`cmd/agentbox-netfilter`)
  tails CoreDNS's query log and populates a per-network `ipset` with resolved IPs.
  Host-side iptables rules drop traffic from the agentbox container's network interface
  to any IP not in that set. After Part B, `block_direct_ip = true` does what it says:
  `curl https://1.1.1.1` from inside the box returns connection-refused.

Two new long-running containers per box join the existing `agentbox-<project_id>`:
- `agentbox-coredns-<project_id>` — CoreDNS sidecar (always, when mode=safe|allowlist)
- `agentbox-netfilter-<project_id>` — netfilter daemon (Part B only; only when
  iptables-level filtering is enabled)

All three are labeled `agentbox=1` for discovery; an `agentbox.role` label distinguishes
`box` (the user-facing one), `coredns`, and `netfilter` so `agentbox ls` filters cleanly.

## Cross-cutting decisions

- **New package `internal/network`.** Holds types, Corefile generator, sidecar spec
  builder, and a Manager that ties them together. Domain-pure; mirrors Phase 2's
  `internal/kits` and Phase 3's `internal/lifecycle` shapes.
- **The container Runtime port grows three methods:** `NetworkCreate`, `NetworkRm`,
  `NetworkExists`. PodmanRuntime adds `os/exec` calls for `podman network create|rm|inspect`.
  Tests get fakeRuntime extensions.
- **runspec gets a `DNS []string` field.** The agentbox container needs `--dns
  <coredns-ip>` at create time; bind-mounting `/etc/resolv.conf` is messier (some tools
  rewrite it). `BuildInput` gains a `SidecarDNS []string` so lifecycle can pass the
  CoreDNS sidecar's IP through.
- **Sudo for iptables/ipset (Linux).** Manipulating host iptables requires root.
  Phase 6 documents that `safe` mode with `block_direct_ip = true` (the default) and
  `allowlist` mode require either running agentbox as root or pre-configuring
  passwordless sudo for `iptables`, `ipset`, and the `agentbox-netfilter` invocation.
  `agentbox doctor` checks for this and reports clearly.
- **macOS degrades to DNS-only.** The Linux-VM-behind-podman-machine model breaks
  iptables/ipset on the host. Phase 6 sets `block_direct_ip = false` automatically on
  macOS (Part A still works fully). Phase 8 may revisit.
- **CoreDNS image pinned.** `coredns/coredns:1.12.0` (or whatever's current at write time
  — verify against `hub.docker.com/r/coredns/coredns/tags`). Pulled lazily on first run;
  `agentbox doctor` warns if not present.
- **No external daemon process for the netfilter daemon.** It runs as a regular
  background process (forked from `agentbox run`'s setup, PID tracked in session dir).
  Stopped on `agentbox rm`. This is a per-box daemon (lifetime tied to the box), not an
  agentbox-wide daemon — consistent with the CoreDNS sidecar.
- **Three sequential containers per box.** Sidecar starts first; netfilter daemon next
  (if Part B); main agentbox container last. Teardown reverses. `agentbox ls` filters by
  `agentbox.role=box` so users only see their boxes.

---

## Part A — DNS-level filtering (Units 1–11)

### Unit 1: `internal/network/types.go`

**File**: `internal/network/types.go` (new)

```go
package network

import (
    "github.com/nklisch/agentbox/internal/config"
)

// Mode is one of the four network modes from SPEC.md.
type Mode string

const (
    ModeOff       Mode = "off"
    ModeSafe      Mode = "safe"
    ModeAllowlist Mode = "allowlist"
    ModeOpen      Mode = "open"
)

// Filtered reports whether mode requires CoreDNS sidecar + custom network.
func (m Mode) Filtered() bool {
    return m == ModeSafe || m == ModeAllowlist
}

// Spec is the resolved network spec for one project box.
type Spec struct {
    ProjectID    string
    Mode         Mode
    NetworkName  string  // "agentbox-net-<id>"
    SidecarName  string  // "agentbox-coredns-<id>"
    NetfilterName string // "agentbox-netfilter-<id>" (Part B; empty when not used)
    SidecarIP    string  // assigned to CoreDNS container, e.g., "10.89.0.2"
    BoxIP        string  // hint for box; podman picks from the network's range
    Subnet       string  // "10.89.X.0/24" — derived from project_id for stable subnets
    Cfg          config.Config
}

// Info is what Manager.Setup returns to lifecycle: the runtime context the
// agentbox container needs to know about.
type Info struct {
    NetworkName string   // pass to runspec.PodmanCreateArgs.Network
    SidecarDNS  []string // pass to runspec.PodmanCreateArgs.DNS (one entry; CoreDNS IP)
}

// IpsetEnabled reports whether IP-level filtering should run (Part B).
// Linux + (safe with block_direct_ip || allowlist) → true.
// macOS → false (always).
func (s Spec) IpsetEnabled() bool { ... }
```

**Implementation Notes**:
- Subnet derivation: hash the project_id and pick from a designated range
  `10.89.0.0/16`, treating the project_id's first byte as the third octet
  (`10.89.<byte>.0/24`). Collision unlikely for personal use; document.
- `IpsetEnabled` returns false on macOS (`runtime.GOOS == "darwin"`).

**Acceptance Criteria**:
- [ ] `Mode("safe").Filtered() == true`; `Mode("off").Filtered() == false`.
- [ ] `Spec{ProjectID: "abc123", Mode: ModeSafe}.NetworkName == "agentbox-net-abc123"`.
- [ ] On macOS, `IpsetEnabled()` returns false even when block_direct_ip=true.

---

### Unit 2: `internal/network/corefile.go`

**File**: `internal/network/corefile.go` (new)

```go
package network

import (
    "fmt"
    "strings"
)

// GenerateCorefile returns the Corefile body for the given spec. Pure
// string output. Caller writes the bytes to the session state dir.
//
// VERIFY against `coredns -plugins` and `coredns -conf-dump default` for the
// installed coredns image at write time. Plugin syntax has rotated across
// versions (esp. forward + tls + log).
func GenerateCorefile(spec Spec) string {
    switch spec.Mode {
    case ModeSafe:
        return safeCorefile(spec.Cfg.Network.Safe)
    case ModeAllowlist:
        return allowlistCorefile(spec.Cfg.Network.Allowlist)
    default:
        return ""
    }
}

func safeCorefile(s config.NetworkSafe) string {
    var b strings.Builder
    fmt.Fprintln(&b, "# agentbox safe-mode Corefile (regenerated each run)")

    // RPZ-style extra_block: respond NXDOMAIN before forwarding.
    for _, dom := range s.ExtraBlock {
        fmt.Fprintf(&b, "%s {\n", dom)
        fmt.Fprintln(&b, "  template ANY ANY {")
        fmt.Fprintln(&b, "    rcode NXDOMAIN")
        fmt.Fprintln(&b, "  }")
        fmt.Fprintln(&b, "}")
    }
    // Default zone: forward to upstream.
    fmt.Fprintln(&b, ". {")
    forwardLine, ok := upstreamForwardLine(s)
    if ok {
        fmt.Fprintln(&b, forwardLine)
    }
    fmt.Fprintln(&b, "  cache 300")
    fmt.Fprintln(&b, "  errors")
    fmt.Fprintln(&b, "  log /var/log/coredns/queries.log {")
    fmt.Fprintln(&b, "    class denial success")
    fmt.Fprintln(&b, "  }")
    fmt.Fprintln(&b, "}")
    return b.String()
}

func upstreamForwardLine(s config.NetworkSafe) (string, bool) {
    switch s.Upstream {
    case "quad9":
        return "  forward . 9.9.9.9 149.112.112.112 {\n    policy random\n  }", true
    case "cloudflare-security":
        return "  forward . 1.1.1.2 1.0.0.2 {\n    policy random\n  }", true
    case "nextdns":
        // VERIFY: NextDNS DoH URL format. As of design time:
        //   https://dns.nextdns.io/<id>
        // with category-block params appended via query string.
        // Use forward plugin's tls support.
        url := fmt.Sprintf("https://dns.nextdns.io/%s", s.NextDNSID)
        if len(s.BlockCategories) > 0 {
            url += "?block=" + strings.Join(s.BlockCategories, ",")
        }
        return fmt.Sprintf("  forward . %s {\n    tls_servername dns.nextdns.io\n  }", url), s.NextDNSID != ""
    case "custom":
        if len(s.UpstreamServers) == 0 {
            return "", false
        }
        return fmt.Sprintf("  forward . %s {\n    policy random\n  }", strings.Join(s.UpstreamServers, " ")), true
    }
    return "", false
}

func allowlistCorefile(a config.NetworkAllow) string {
    var b strings.Builder
    fmt.Fprintln(&b, "# agentbox allowlist-mode Corefile (regenerated each run)")
    // Forward only the listed zones to a public resolver.
    if len(a.Allow) > 0 {
        fmt.Fprintf(&b, "%s {\n", strings.Join(a.Allow, " "))
        fmt.Fprintln(&b, "  forward . 1.1.1.1 8.8.8.8 {")
        fmt.Fprintln(&b, "    policy random")
        fmt.Fprintln(&b, "  }")
        fmt.Fprintln(&b, "  cache 300")
        fmt.Fprintln(&b, "  errors")
        fmt.Fprintln(&b, "  log /var/log/coredns/queries.log {")
        fmt.Fprintln(&b, "    class denial success")
        fmt.Fprintln(&b, "  }")
        fmt.Fprintln(&b, "}")
    }
    // Default zone: NXDOMAIN for everything not listed.
    fmt.Fprintln(&b, ". {")
    fmt.Fprintln(&b, "  template ANY ANY {")
    fmt.Fprintln(&b, "    rcode NXDOMAIN")
    fmt.Fprintln(&b, "  }")
    fmt.Fprintln(&b, "  log /var/log/coredns/queries.log {")
    fmt.Fprintln(&b, "    class denial success")
    fmt.Fprintln(&b, "  }")
    fmt.Fprintln(&b, "}")
    return b.String()
}
```

**Implementation Notes**:
- The CoreDNS `template` plugin returns synthesized responses. Verify it ships in the
  default image (`coredns/coredns:1.12.0`); standard plugin set as of 2024+.
- `extra_allow` is harder to express in Corefile syntax — it's not a clean RPZ override
  in CoreDNS. For Phase 6 ship: implement `extra_block` (per design above) and document
  `extra_allow` as a known gap (the implementer can use the `hosts` plugin to map
  domain → static IP, but that doesn't fit a "skip the threat feed" semantics).
- Log format `class denial success` captures both successful and NXDOMAIN responses, so
  `box net` shows BLOCKED entries.

**Acceptance Criteria**:
- [ ] `GenerateCorefile(safeSpec)` contains `forward . 9.9.9.9` for quad9.
- [ ] Output contains `log /var/log/coredns/queries.log` block.
- [ ] Allowlist output contains `forward` for the listed domains and a default zone with
      `template ANY ANY { rcode NXDOMAIN }`.
- [ ] `extra_block` entries produce per-domain blocks BEFORE the default zone.
- [ ] Output is deterministic (same input → byte-identical output).

---

### Unit 3: `internal/network/corefile_test.go`

**File**: `internal/network/corefile_test.go` (new)

Golden tests covering each upstream + each mode + extra_block + nextdns category params.

**Acceptance Criteria**:
- [ ] `go test ./internal/network/...` passes.

---

### Unit 4: `internal/container/runtime.go` edit — add network ops

**File**: `internal/container/runtime.go` (edit existing)

Add three methods to the `Runtime` interface:

```go
type Runtime interface {
    Create(args runspec.PodmanCreateArgs) error
    Start(name string) error
    Stop(name string) error
    Inspect(name string) (Box, error)
    Exec(name string, opts ExecOpts) (int, error)
    Ls(all bool) ([]Box, error)
    Rm(name string, force bool) error

    // NEW (Phase 6):
    NetworkCreate(name, subnet string) error
    NetworkRm(name string) error
    NetworkExists(name string) (bool, error)
}
```

`fakeRuntime` in `internal/lifecycle/lifecycle_test.go` and tests there get matching
stub implementations (record calls, return canned results).

**Acceptance Criteria**:
- [ ] Compiles after the edit; existing tests still pass once fake updates land.

---

### Unit 5: `internal/container/podman.go` edit — implement network ops

**File**: `internal/container/podman.go` (edit existing)

```go
// NetworkCreate runs `<bin> network create --driver bridge --subnet <subnet> <name>`.
func (r *PodmanRuntime) NetworkCreate(name, subnet string) error {
    cmd := exec.Command(r.Bin, "network", "create",
        "--driver", "bridge", "--subnet", subnet, name)
    cmd.Stdout = io.Discard
    cmd.Stderr = io.Discard
    if err := cmd.Run(); err != nil {
        var ee *exec.ExitError
        if errors.As(err, &ee) {
            // Likely "already exists" — caller handles via NetworkExists check first.
            return fmt.Errorf("%s network create: %w", r.Bin, err)
        }
        return err
    }
    return nil
}

// NetworkRm runs `<bin> network rm <name>`. Idempotent: missing network returns nil.
func (r *PodmanRuntime) NetworkRm(name string) error {
    cmd := exec.Command(r.Bin, "network", "rm", name)
    cmd.Stdout = io.Discard
    cmd.Stderr = io.Discard
    if err := cmd.Run(); err != nil {
        var ee *exec.ExitError
        if errors.As(err, &ee) {
            return nil // missing = ok
        }
        return err
    }
    return nil
}

// NetworkExists runs `<bin> network exists <name>`. Exit 0 = present.
func (r *PodmanRuntime) NetworkExists(name string) (bool, error) {
    cmd := exec.Command(r.Bin, "network", "exists", name)
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
- `podman network exists` returns 0 if present, 1 if not. Verify against current podman.
- For docker, the command is `docker network inspect <name>` — `network exists` is
  podman-specific. Phase 8's docker fallback handles this; for Phase 6, target podman.

**Acceptance Criteria**:
- [ ] All three methods compile and follow the existing PodmanRuntime style.

---

### Unit 6: `internal/network/sidecar.go`

**File**: `internal/network/sidecar.go` (new)

Builds a `runspec.PodmanCreateArgs` for the CoreDNS sidecar. Reusing
`PodmanCreateArgs` for the sidecar means dry-run shows it too.

```go
package network

import (
    "github.com/nklisch/agentbox/internal/runspec"
)

// CoreDNSImage is the pinned CoreDNS image used as the sidecar.
// VERIFY: latest stable tag at write time.
const CoreDNSImage = "docker.io/coredns/coredns:1.12.0"

// SidecarInput collects the data BuildSidecar needs.
type SidecarInput struct {
    Spec       Spec
    StateDir   string // <state-dir>/sessions/<project_id>
}

// BuildSidecar returns runspec.PodmanCreateArgs for the CoreDNS sidecar.
// The agentbox=1 + agentbox.role=coredns labels are set so `agentbox ls`
// can filter to user-facing boxes only.
func BuildSidecar(in SidecarInput) runspec.PodmanCreateArgs {
    return runspec.PodmanCreateArgs{
        Name:    in.Spec.SidecarName,
        Labels: []runspec.KV{
            {Key: "agentbox", Value: "1"},
            {Key: "agentbox.role", Value: "coredns"},
            {Key: "agentbox.project_id", Value: in.Spec.ProjectID},
        },
        Mounts: []runspec.Mount{
            {Source: in.StateDir + "/Corefile",       Target: "/etc/coredns/Corefile",   Mode: "ro"},
            {Source: in.StateDir + "/coredns-queries.log", Target: "/var/log/coredns/queries.log", Mode: "rw"},
        },
        Network: in.Spec.NetworkName,
        Image:   CoreDNSImage,
        Argv:    []string{"-conf", "/etc/coredns/Corefile"},
    }
}
```

**Implementation Notes**:
- The CoreDNS image entrypoint is `/coredns`; `Argv: ["-conf", ...]` becomes args to it.
  Verify by `podman inspect docker.io/coredns/coredns:1.12.0 --format {{.Config.Entrypoint}}`.
- The queries log path inside the container — adjust if the chosen CoreDNS image expects
  a different path.

**Acceptance Criteria**:
- [ ] `BuildSidecar({Spec{ProjectID: "abc", ...}})` returns args with
      `Name: "agentbox-coredns-abc"`, `Network: "agentbox-net-abc"`, and the two mounts.
- [ ] `Labels` includes `agentbox.role=coredns`.

---

### Unit 7: `internal/network/manager.go` — orchestrator

**File**: `internal/network/manager.go` (new)

```go
package network

import (
    "fmt"
    "os"
    "path/filepath"

    "github.com/nklisch/agentbox/internal/config"
    "github.com/nklisch/agentbox/internal/container"
    "github.com/nklisch/agentbox/internal/runspec"
    "github.com/nklisch/agentbox/internal/state"
)

// Manager orchestrates per-project network setup: create the podman network,
// write Corefile, start CoreDNS sidecar, optionally start netfilter daemon.
type Manager struct {
    Runtime container.Runtime
}

// SpecFor builds a Spec for the given project + config.
func (m *Manager) SpecFor(cfg config.Config, projectID string) Spec {
    s := Spec{
        ProjectID:   projectID,
        Mode:        Mode(cfg.Network.Mode),
        NetworkName: "agentbox-net-" + projectID,
        SidecarName: "agentbox-coredns-" + projectID,
        Cfg:         cfg,
    }
    s.Subnet = subnetFromProjectID(projectID)
    s.SidecarIP = sidecarIPFromSubnet(s.Subnet) // first usable host = .2
    if s.IpsetEnabled() {
        s.NetfilterName = "agentbox-netfilter-" + projectID
    }
    return s
}

// Setup brings up the network and sidecar for spec. Idempotent — repeated
// Setup with the same spec is a no-op past whatever's already up.
//
// For mode=off: returns Info{NetworkName: "none"}, no sidecar.
// For mode=open: returns Info{NetworkName: "bridge"}, no sidecar.
// For mode=safe|allowlist: creates the network if absent, writes Corefile,
// starts the sidecar.
func (m *Manager) Setup(spec Spec) (Info, error) {
    if !spec.Mode.Filtered() {
        return infoForUnfiltered(spec.Mode), nil
    }

    // Network.
    exists, err := m.Runtime.NetworkExists(spec.NetworkName)
    if err != nil {
        return Info{}, err
    }
    if !exists {
        if err := m.Runtime.NetworkCreate(spec.NetworkName, spec.Subnet); err != nil {
            return Info{}, fmt.Errorf("network create: %w", err)
        }
    }

    // Corefile.
    if err := writeCorefile(spec); err != nil {
        return Info{}, err
    }

    // Sidecar.
    sb, err := m.Runtime.Inspect(spec.SidecarName)
    if err != nil {
        return Info{}, err
    }
    if sb.Status == container.StatusMissing {
        stateDir, err := state.SessionDir(spec.ProjectID)
        if err != nil {
            return Info{}, err
        }
        args := BuildSidecar(SidecarInput{Spec: spec, StateDir: stateDir})
        if err := m.Runtime.Create(args); err != nil {
            return Info{}, fmt.Errorf("sidecar create: %w", err)
        }
    }
    if err := m.Runtime.Start(spec.SidecarName); err != nil {
        return Info{}, fmt.Errorf("sidecar start: %w", err)
    }

    // Resolve sidecar IP via Inspect (post-start; podman assigns from the network range).
    sb, err = m.Runtime.Inspect(spec.SidecarName)
    if err != nil {
        return Info{}, err
    }
    // Box record doesn't currently expose the IP. Phase 6 needs to extend it OR
    // resolve via a separate `podman inspect ... --format {{.NetworkSettings.IPAddress}}`.
    // For Phase 6: extend Inspect's parseInspect to read the network-specific IP.

    return Info{
        NetworkName: spec.NetworkName,
        SidecarDNS:  []string{spec.SidecarIP},
    }, nil
}

// Teardown stops + removes the sidecar and the podman network.
// Idempotent: missing pieces are not errors.
func (m *Manager) Teardown(spec Spec) error {
    if !spec.Mode.Filtered() {
        return nil
    }
    // Order: sidecar first, then network.
    if err := m.Runtime.Rm(spec.SidecarName, true); err != nil {
        return err
    }
    if err := m.Runtime.NetworkRm(spec.NetworkName); err != nil {
        return err
    }
    return nil
}

func writeCorefile(spec Spec) error {
    dir, err := state.SessionDir(spec.ProjectID)
    if err != nil {
        return err
    }
    if err := state.EnsureDir(dir); err != nil {
        return err
    }
    body := GenerateCorefile(spec)
    if err := os.WriteFile(filepath.Join(dir, "Corefile"), []byte(body), 0o600); err != nil {
        return err
    }
    // Also touch the queries log so the sidecar's bind mount has a target file.
    qlog := filepath.Join(dir, "coredns-queries.log")
    if _, err := os.Stat(qlog); os.IsNotExist(err) {
        f, err := os.Create(qlog)
        if err != nil {
            return err
        }
        _ = f.Close()
    }
    return nil
}

// Helpers for unfiltered modes.
func infoForUnfiltered(mode Mode) Info {
    switch mode {
    case ModeOff:
        return Info{NetworkName: "none"}
    default: // ModeOpen
        return Info{NetworkName: "bridge"}
    }
}
```

Plus helpers `subnetFromProjectID(string) string` and `sidecarIPFromSubnet(string) string`
in this file (not shown — straightforward arithmetic on the project_id hex).

**Implementation Notes**:
- `Inspect`'s current `Box` struct doesn't expose container IP. Phase 6 needs to either
  (a) extend `Box` with an `IPs map[string]string` field (network → IP), or (b) add a
  dedicated `Runtime.GetIP(name, networkName) (string, error)` method. Recommend (b) —
  scoped, doesn't disturb existing Box callers.
- `SpecFor` is called by lifecycle BEFORE `Setup` so the sidecar's IP can be threaded
  into runspec.BuildInput.SidecarDNS. Subnet is deterministic from project_id; sidecar IP
  is the first usable host in that subnet (always `<subnet-prefix>.2`).

**Acceptance Criteria**:
- [ ] `Setup(spec=ModeOff)` returns `Info{NetworkName: "none"}` and makes zero Runtime calls.
- [ ] `Setup(spec=ModeSafe)` creates network if absent, writes Corefile, creates+starts
      sidecar (verified via fakeRuntime call recording).
- [ ] `Setup(spec=ModeSafe)` with already-running sidecar is a no-op (no new Create/Start).
- [ ] `Teardown(spec=ModeSafe)` calls `Rm(sidecar, force=true)` then `NetworkRm`.
- [ ] `writeCorefile` creates `<state-dir>/Corefile` and touches `coredns-queries.log`.

---

### Unit 8: `internal/runspec/runspec.go` edit — add `DNS` field

**File**: `internal/runspec/runspec.go` (edit existing)

Add to `PodmanCreateArgs`:

```go
type PodmanCreateArgs struct {
    // ... existing fields ...
    DNS []string // --dns <ip> entries; one per IP. Phase 6 sets to [coredns sidecar IP] for safe/allowlist.
}
```

Add to `BuildInput`:

```go
type BuildInput struct {
    // ... existing fields ...
    SidecarDNS []string // pass-through to PodmanCreateArgs.DNS
}
```

In `BuildPodmanCreateArgs`, populate it:

```go
args.DNS = in.SidecarDNS
```

In `ToShell`, render after Network:

```go
for _, d := range p.DNS {
    fmt.Fprintf(&b, "  --dns %q \\\n", d)
}
```

In PodmanRuntime.Create (`internal/container/podman.go`), append to argv:

```go
for _, d := range args.DNS {
    argv = append(argv, "--dns", d)
}
```

**Acceptance Criteria**:
- [ ] runspec_test.go: when `BuildInput.SidecarDNS = []string{"10.89.7.2"}`, output args
      have `DNS = []string{"10.89.7.2"}`.
- [ ] `ToShell` output contains a `--dns "10.89.7.2"` line.

---

### Unit 9: `internal/lifecycle/lifecycle.go` edit — replace degradeNetworkMode

**File**: `internal/lifecycle/lifecycle.go` (edit existing)

Replace the `degradeNetworkMode()` method with `setupNetwork()`:

```go
type Lifecycle struct {
    Cfg     config.Config
    Runtime container.Runtime
    Builder *kits.Builder
    Network *network.Manager // NEW
    Home    string
    Stdout  io.Writer
    Stderr  io.Writer
}

// setupNetwork brings up the per-project network + sidecar, returning the
// Info that runspec.BuildInput needs (network name + DNS overrides).
// Replaces Phase 3's degradeNetworkMode warning + fallback.
func (l *Lifecycle) setupNetwork(projectID string) (network.Info, error) {
    spec := l.Network.SpecFor(l.Cfg, projectID)
    return l.Network.Setup(spec)
}
```

Update `EnsureBox` and `createBox`:
- After `project.Resolve()` returns projID, call `setupNetwork(projID)` to get
  `network.Info`.
- Pass `info.SidecarDNS` to `runspec.BuildInput.SidecarDNS`.
- The `args.Network` field will already be set correctly by `runspec.NetworkArg` (which
  returns `agentbox-net-<id>` for safe/allowlist).

Update `Rm`:
- After removing the agentbox container, call `l.Network.Teardown(spec)` to remove the
  sidecar + network.

Update CLI's `newLifecycle` factory (in `internal/cli/lifecycle.go`):
```go
return &lifecycle.Lifecycle{
    // ... existing ...
    Network: &network.Manager{Runtime: rt},
}, nil
```

`degradeNetworkMode` is fully removed. Tests update accordingly.

**Implementation Notes**:
- `Lifecycle.Ls` should filter by `agentbox.role=box` so sidecar/netfilter containers
  don't appear. Phase 6 adds this filter to `lifecycle.Lifecycle.Ls` (just inside the
  client-side loop). Tests assert it.
- `Lifecycle.Rm({All: true})` walks all `agentbox=1` containers but should restrict to
  `role=box` for the user-facing list, then teardown each box's network/sidecar
  separately. Document.

**Acceptance Criteria**:
- [ ] `degradeNetworkMode` is gone (`grep degradeNetworkMode` returns 0).
- [ ] `Lifecycle.EnsureBox` calls `setupNetwork` before `createBox`.
- [ ] With `Cfg.Network.Mode == "safe"` and a fake Network manager, `EnsureBox`
      passes `SidecarDNS` through to `runspec.BuildInput`.
- [ ] `Lifecycle.Rm` calls `Network.Teardown` after removing the agentbox container.
- [ ] `Lifecycle.Ls` filters out coredns + netfilter containers.

---

### Unit 10: `internal/builtinkits/kits/base/box-net` rewrite

**File**: `internal/builtinkits/kits/base/box-net` (new — Phase 2's box dispatcher already references it but the script doesn't exist; Phase 6 ships it)

```bash
#!/usr/bin/env bash
# box net — show the network policy and recent DNS queries.
# Reads $AGENTBOX_NETWORK to pick a presentation; reads /var/log/coredns/queries.log
# for the query history. When mode is off/open, prints a one-line note.

set -u

mode="${AGENTBOX_NETWORK:-n/a}"

case "$mode" in
  off)
    printf "mode         off (no network)\n"
    exit 0
    ;;
  open)
    printf "mode         open (default bridge — no filtering active)\n"
    exit 0
    ;;
  safe|allowlist)
    printf "mode         %s\n" "$mode"
    ;;
  *)
    printf "mode         %s (unknown)\n" "$mode"
    exit 0
    ;;
esac

cfg=/etc/agentbox/config.toml
if [ -f "$cfg" ]; then
    upstream=$(awk -F'=' '/^upstream[[:space:]]*=/ { gsub(/[" ]/,"",$2); print $2; exit }' "$cfg" 2>/dev/null)
    [ -n "$upstream" ] && printf "upstream     %s\n" "$upstream"
fi
printf "block_direct_ip: %s\n" "${AGENTBOX_BLOCK_DIRECT_IP:-true}"

log=/var/log/coredns/queries.log
if [ ! -f "$log" ]; then
    printf "\nrecent queries: (no log available — sidecar may not be running)\n"
    exit 0
fi

printf "\nrecent queries (last 50):\n"
# CoreDNS log line format (default with `log` plugin, format `combined`):
#   <timestamp> <remote-ip> - - "<qclass> <qtype> <name>." <protocol> <rcode> ...
tail -n 50 "$log" | awk '
{
    ts=$1
    name=$0; sub(/.*"[A-Z]+ [A-Z]+ /, "", name); sub(/\." .*/, "", name)
    rcode = $0; sub(/.* /, "", rcode)
    status = "ALLOWED"
    if (rcode ~ /NOERROR/) status = "ALLOWED"
    if (rcode ~ /NXDOMAIN/) status = "BLOCKED"
    printf "  %s  %-40s  %s\n", ts, name, status
}'
```

**Implementation Notes**:
- This script's awk parsing of CoreDNS log lines is best-effort. The log format depends
  on the `log` plugin's `format` directive — verify against actual output before
  finalizing. If the format differs, simplify to "show the last 50 lines verbatim".
- `chmod +x box-net` in source tree.
- Add the file to `kits/base/install.sh` so it's copied to `/usr/local/bin/` (the
  dispatcher in `kits/base/box` already calls `box-net`).

**Acceptance Criteria**:
- [ ] `box net` inside an agentbox container with mode=safe shows mode + upstream + recent queries.
- [ ] `box net` with mode=off prints `mode off (no network)` and exits 0.
- [ ] `box net` with mode=open prints the open-mode note and exits 0.

---

### Unit 11: `internal/lifecycle/lifecycle_test.go` extensions + `internal/network/network_test.go`

Tests:

For `internal/network`:
- `TestSpecFor_Modes` — table-driven across off/safe/allowlist/open.
- `TestSetup_OffOpen_NoRuntimeCalls` — fakeRuntime records 0 calls.
- `TestSetup_Safe_CreatesNetworkAndSidecar` — verifies the call sequence.
- `TestSetup_Safe_IdempotentWhenSidecarRunning` — second Setup is a no-op past inspect.
- `TestTeardown_Safe_StopsAndRemoves` — verifies Rm + NetworkRm calls.
- `TestWriteCorefile_ProducesFile` — uses `t.Setenv("XDG_DATA_HOME", t.TempDir())`.

For `internal/lifecycle` (extensions):
- `TestEnsureBox_Safe_PassesDNSThrough` — fakeRuntime + fakeNetwork; assert
  `runspec.BuildInput.SidecarDNS` reaches `runspec.BuildPodmanCreateArgs`.
- `TestRm_Safe_TeardownNetwork` — assert `Network.Teardown` called after `Runtime.Rm`.
- `TestLs_FiltersByRoleBox` — fakeRuntime returns three containers (box, coredns, netfilter);
  Ls returns only the box.

**Acceptance Criteria**:
- [ ] `go test ./internal/network/... ./internal/lifecycle/... ./internal/runspec/...` passes.

---

## Part B — IP-level filtering (Units 12–17)

### Unit 12: `cmd/agentbox-netfilter/main.go`

**File**: `cmd/agentbox-netfilter/main.go` (new — small standalone binary)

A tiny daemon that:
1. Reads its args: `--queries-log <path> --ipset <name>`.
2. Tails the queries log (re-opens on rotation).
3. For each line, parses out the resolved IP (the answer).
4. Calls `ipset add <name> <ip> -exist` to add it.
5. On SIGTERM, exits cleanly.

Rough shape:

```go
package main

import (
    "bufio"
    "context"
    "flag"
    "fmt"
    "os"
    "os/exec"
    "os/signal"
    "regexp"
    "syscall"
    "time"
)

// Match resolved IPv4/IPv6 in CoreDNS log lines. The exact regex depends on
// the log format used by GenerateCorefile — verify at write time.
var ipRE = regexp.MustCompile(`\b\d+\.\d+\.\d+\.\d+\b`)

func main() {
    queriesLog := flag.String("queries-log", "", "path to CoreDNS queries log")
    ipsetName := flag.String("ipset", "", "name of ipset to populate")
    flag.Parse()

    if *queriesLog == "" || *ipsetName == "" {
        fmt.Fprintln(os.Stderr, "usage: agentbox-netfilter --queries-log <path> --ipset <name>")
        os.Exit(2)
    }

    ctx, cancel := context.WithCancel(context.Background())
    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, syscall.SIGTERM, os.Interrupt)
    go func() { <-sigCh; cancel() }()

    if err := tailAndApply(ctx, *queriesLog, *ipsetName); err != nil {
        fmt.Fprintln(os.Stderr, "netfilter:", err)
        os.Exit(1)
    }
}

func tailAndApply(ctx context.Context, path, ipset string) error {
    // Open the file. If it doesn't exist yet, retry until it does (CoreDNS
    // creates it on first query).
    f, err := openWithRetry(ctx, path, 30*time.Second)
    if err != nil {
        return err
    }
    defer f.Close()
    // Seek to end (we only care about new entries).
    _, _ = f.Seek(0, os.SEEK_END)

    sc := bufio.NewScanner(f)
    sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
    for {
        for sc.Scan() {
            ip := ipRE.FindString(sc.Text())
            if ip == "" {
                continue
            }
            // ipset add <name> <ip> -exist (no error on duplicate).
            _ = exec.Command("ipset", "add", ipset, ip, "-exist").Run()
        }
        if err := sc.Err(); err != nil {
            return err
        }
        select {
        case <-ctx.Done():
            return nil
        case <-time.After(500 * time.Millisecond):
        }
    }
}

func openWithRetry(ctx context.Context, path string, timeout time.Duration) (*os.File, error) {
    deadline := time.Now().Add(timeout)
    for {
        f, err := os.Open(path)
        if err == nil {
            return f, nil
        }
        if !os.IsNotExist(err) {
            return nil, err
        }
        if time.Now().After(deadline) {
            return nil, fmt.Errorf("queries log %s did not appear within %s", path, timeout)
        }
        select {
        case <-ctx.Done():
            return nil, ctx.Err()
        case <-time.After(200 * time.Millisecond):
        }
    }
}
```

**Implementation Notes**:
- The binary lives at `cmd/agentbox-netfilter/`. The Makefile's `build` target should
  also produce this binary (or a separate `make netfilter` target).
- It's invoked by Manager via `exec.Command("sudo", "agentbox-netfilter", ...)` — sudo
  is needed because `ipset add` requires CAP_NET_ADMIN.
- Verify `ipset add ... -exist` syntax is current. (Older ipset versions used `--exist`.)
- The IP regex is intentionally simple. CoreDNS query logs include both query lines
  (no IP yet) and reply lines (IP in the answer field). For Phase 6, the implementer
  should verify the actual log format and tighten the regex; for the smoke test,
  matching ANY IPv4 in log lines is acceptable since false-positives just result in
  benign ipset additions.

**Acceptance Criteria**:
- [ ] `go build ./cmd/agentbox-netfilter` succeeds.
- [ ] `agentbox-netfilter --help` (cobra-less; uses stdlib `flag`) prints usage and exits 2.
- [ ] Given a log file containing `... 10.0.0.5 ...`, the daemon calls `ipset add <name> 10.0.0.5 -exist`.
      (Test via a fake `ipset` binary on PATH that records its args.)

---

### Unit 13: `internal/network/iptables.go`

**File**: `internal/network/iptables.go` (new)

Sets up host-side iptables + ipset rules for a per-network egress filter.

```go
package network

import (
    "fmt"
    "io"
    "os/exec"
)

// IPTables manages the host-side ipset + iptables rules for a project's network.
// All commands are invoked via sudo (Phase 6 documents the requirement).
type IPTables struct {
    Sudo bool // true when not running as root; prepends "sudo" to commands
}

// SetupRules creates the per-network ipset and adds an iptables rule that
// drops outbound traffic from the agentbox network's interface to any
// destination not in the ipset.
//
// For allowlist mode, callers should pre-populate the ipset by resolving
// each Cfg.Network.Allowlist.Allow entry once (CoreDNS will also add them
// on first lookup, but pre-population avoids a race).
func (t *IPTables) SetupRules(spec Spec) error {
    if !spec.IpsetEnabled() {
        return nil
    }
    setName := spec.NetworkName + "-allowed"

    // 1. Create ipset.
    if err := t.run("ipset", "create", setName, "hash:ip", "family", "inet", "-exist"); err != nil {
        return fmt.Errorf("ipset create: %w", err)
    }
    // 2. iptables rule on FORWARD chain: drop traffic from the agentbox
    //    network's source IPs (the subnet) to any destination NOT in the set.
    //    Use --comment "agentbox=<id>" to make rules easy to delete.
    rule := []string{
        "-I", "FORWARD",
        "-s", spec.Subnet,
        "-m", "set", "!", "--match-set", setName, "dst",
        "-j", "DROP",
        "-m", "comment", "--comment", "agentbox=" + spec.ProjectID,
    }
    return t.run("iptables", rule...)
}

// TeardownRules removes the iptables rule and the ipset.
// Idempotent: missing pieces are not errors.
func (t *IPTables) TeardownRules(spec Spec) error {
    if !spec.IpsetEnabled() {
        return nil
    }
    setName := spec.NetworkName + "-allowed"

    // Find and delete iptables rules with the matching comment.
    // Use `iptables -S | grep agentbox=<id>` to list, then `-D`.
    // Simplest: run iptables -D with the same args as -I (-D removes by spec match).
    rule := []string{
        "-D", "FORWARD",
        "-s", spec.Subnet,
        "-m", "set", "!", "--match-set", setName, "dst",
        "-j", "DROP",
        "-m", "comment", "--comment", "agentbox=" + spec.ProjectID,
    }
    _ = t.run("iptables", rule...) // ignore errors — rule may not exist

    // Then destroy the ipset.
    _ = t.run("ipset", "destroy", setName) // ignore errors — set may not exist
    return nil
}

// PrePopulate resolves each domain in `allow` and adds its IPs to the ipset.
// Used by allowlist mode at network setup time.
func (t *IPTables) PrePopulate(spec Spec, allow []string) error {
    setName := spec.NetworkName + "-allowed"
    for _, dom := range allow {
        ips, err := lookupIPs(dom)
        if err != nil {
            continue // best-effort
        }
        for _, ip := range ips {
            _ = t.run("ipset", "add", setName, ip, "-exist")
        }
    }
    return nil
}

func (t *IPTables) run(bin string, args ...string) error {
    var cmd *exec.Cmd
    if t.Sudo {
        cmd = exec.Command("sudo", append([]string{"-n", bin}, args...)...)
    } else {
        cmd = exec.Command(bin, args...)
    }
    cmd.Stdout = io.Discard
    cmd.Stderr = io.Discard
    return cmd.Run()
}

func lookupIPs(host string) ([]string, error) {
    // net.LookupHost(host) — stdlib.
}
```

**Implementation Notes**:
- `sudo -n` (non-interactive) fails fast if there's no passwordless sudo for the user.
  Document this requirement in CLI.md and PROGRESS.md. The `agentbox doctor` check in
  Unit 17 confirms the setup.
- Tests for IPTables shouldn't actually run iptables/ipset — verify command-construction
  with a fake `run` injection point.

**Acceptance Criteria**:
- [ ] `SetupRules(spec)` constructs the right ipset + iptables commands.
- [ ] `TeardownRules` is idempotent (returns nil even if rules/set don't exist).
- [ ] `PrePopulate` resolves DNS names via stdlib and feeds them to `ipset add`.

---

### Unit 14: `internal/network/manager.go` edit — wire IPTables + netfilter daemon

**File**: `internal/network/manager.go` (edit)

```go
type Manager struct {
    Runtime container.Runtime
    IPTables *IPTables  // NEW
    NetfilterBin string // NEW; path to the agentbox-netfilter binary (default: "agentbox-netfilter")
}

// In Setup, after sidecar starts (when spec.IpsetEnabled()):
if spec.IpsetEnabled() {
    if err := m.IPTables.SetupRules(spec); err != nil {
        return Info{}, fmt.Errorf("iptables setup: %w", err)
    }
    if spec.Mode == ModeAllowlist {
        if err := m.IPTables.PrePopulate(spec, spec.Cfg.Network.Allowlist.Allow); err != nil {
            return Info{}, fmt.Errorf("allowlist prepopulate: %w", err)
        }
    }
    if err := m.startNetfilterDaemon(spec); err != nil {
        return Info{}, fmt.Errorf("netfilter daemon start: %w", err)
    }
}

// startNetfilterDaemon forks the agentbox-netfilter binary as a background
// process. Tracks PID in <state-dir>/netfilter.pid for later cleanup.
func (m *Manager) startNetfilterDaemon(spec Spec) error {
    // sudo -n agentbox-netfilter --queries-log <path> --ipset <name> &
    // Disown by setting SysProcAttr.Setsid = true.
    // Write PID to <state-dir>/netfilter.pid.
}

// In Teardown, before NetworkRm:
if spec.IpsetEnabled() {
    _ = m.stopNetfilterDaemon(spec)  // best-effort
    _ = m.IPTables.TeardownRules(spec)
}
```

**Acceptance Criteria**:
- [ ] `Setup` with mode=safe + block_direct_ip=true forks netfilter daemon.
- [ ] `Setup` with mode=allowlist pre-populates ipset before forking daemon.
- [ ] `Teardown` stops daemon, tears down iptables/ipset, then removes network.

---

### Unit 15: `Makefile` — build `agentbox-netfilter` binary

**File**: `Makefile` (edit existing)

Add a target so `make build` produces both binaries:

```make
build:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o agentbox ./cmd/agentbox
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o agentbox-netfilter ./cmd/agentbox-netfilter
```

Plus an `install` rule that copies both binaries.

**Acceptance Criteria**:
- [ ] `make build` produces both `agentbox` and `agentbox-netfilter`.
- [ ] Both binaries are static (CGO_ENABLED=0).

---

### Unit 16: `agentbox doctor` extensions

**File**: `internal/doctor/doctor.go` (edit existing)

Add three new checks (only when running on Linux):

```go
func iptablesCheck() Check { ... }   // `which iptables`; OK or FAIL
func ipsetCheck() Check { ... }      // `which ipset`; OK or FAIL
func sudoCheck() Check { ... }       // `sudo -n iptables -L`; OK if exit 0 (passwordless)
                                      //                       WARN if not (interactive prompt would succeed)
                                      //                       FAIL if iptables binary missing
func corednsImageCheck() Check { ... } // `podman image exists docker.io/coredns/coredns:1.12.0`
                                       //                       OK if present
                                       //                       WARN with "agentbox doctor --fix or run safe-mode once to pull"
```

These checks run unconditionally on Linux; they're informational on macOS (Phase 6 only
needs DNS-level filtering there).

**Acceptance Criteria**:
- [ ] `agentbox doctor` shows the new checks alongside runtime + state-dir.
- [ ] On a fresh Linux box without iptables installed, `iptables` check is FAIL.
- [ ] On Linux with passwordless sudo for iptables: `sudo` check is OK.

---

### Unit 17: `Lifecycle.Rm` and `Lifecycle.Ls` polish

**File**: `internal/lifecycle/lifecycle.go` (edit)

- `Ls` adds a `role` filter so only `agentbox.role=box` (or unlabeled — backward-compat
  for boxes from before Phase 6) shows up.
- `Rm` for `--all` walks `agentbox.role=box` containers; for each, calls
  `Network.Teardown` to clean up the per-box sidecar + netfilter + network. Sidecar
  containers get cleaned up by Teardown, not by direct Ls iteration.

**Acceptance Criteria**:
- [ ] `agentbox ls` shows the box only, even when sidecar/netfilter containers exist.
- [ ] `agentbox rm --all --force` cleans up everything related to each box (box +
      sidecar + netfilter + network + ipset + iptables).

---

## Implementation Order

| Layer | Units | Notes |
|-------|-------|-------|
| **Part A** | 1–11 | DNS-level filtering. Standalone deliverable; safe/allowlist work via NXDOMAIN even without Part B. |
| **Part B** | 12–17 | IP-level filtering. Requires Part A to be in place. Documents sudo requirement and ships the netfilter daemon. |

Within Part A:
1. Unit 1 (types.go) — no deps.
2. Unit 2 (corefile.go) + Unit 3 (tests) — depends on Unit 1.
3. Unit 4 (Runtime port) + Unit 5 (PodmanRuntime impl) — independent of Units 1-3.
4. Unit 6 (sidecar.go) — depends on Unit 1 + runspec.
5. Unit 7 (manager.go) — depends on Units 1, 2, 4, 5, 6.
6. Unit 8 (runspec edit) — independent.
7. Unit 9 (lifecycle edit) — depends on Units 7, 8.
8. Unit 10 (box-net) — independent.
9. Unit 11 (tests) — depends on everything in Part A.

Within Part B:
12. Unit 12 (netfilter binary) — independent of the rest of Part B.
13. Unit 13 (iptables.go) — independent.
14. Unit 14 (manager.go iptables wire) — depends on Units 12, 13 + Unit 7.
15. Unit 15 (Makefile) — independent.
16. Unit 16 (doctor) — independent.
17. Unit 17 (lifecycle Ls/Rm polish) — depends on Unit 14.

**Recommended orchestration:** ONE Sonnet agent for Part A (units 1–11) — patterns are
familiar from Phase 2/3. THEN a second Sonnet agent for Part B once Part A is verified.

---

## Verification Checklist

```sh
cd /home/nathan/dev/agent-box
go vet ./...
go test ./...
make build

# Domain purity
grep -rn 'spf13/cobra\|internal/cli' \
  internal/version/ internal/exitcode/ internal/state/ internal/project/ \
  internal/config/ internal/runspec/ internal/doctor/ internal/kits/ \
  internal/builtinkits/ internal/container/ internal/lifecycle/ internal/zellij/ \
  internal/network/
# Only kits_test.go comment line should match.

# Doctor passes (or reports actionable gaps).
./agentbox doctor

# Pull the CoreDNS image once.
podman pull docker.io/coredns/coredns:1.12.0

# ROADMAP Phase 6 test checkpoint — safe mode.
mkdir -p /tmp/abx-proj && cd /tmp/abx-proj && touch x
./agentbox run --network safe --no-attach
./agentbox exec . dig +short api.anthropic.com   # resolves
./agentbox exec . dig +short malware.testing.example  # NXDOMAIN if in feed
./agentbox exec . sh -c 'curl -fsS --max-time 3 https://1.1.1.1 || echo BLOCKED'  # BLOCKED with Part B
./agentbox exec . box net  # shows queries with ALLOWED/BLOCKED labels
./agentbox rm .

# Allowlist mode.
cat > .agentbox.toml <<EOF
[network]
mode = "allowlist"
[network.allowlist]
allow = ["github.com"]
EOF
./agentbox run --no-attach
./agentbox exec . sh -c 'curl -fsS --max-time 3 https://github.com >/dev/null && echo OK'  # OK
./agentbox exec . sh -c 'curl -fsS --max-time 3 https://npmjs.org >/dev/null || echo BLOCKED'  # BLOCKED
./agentbox rm .
rm -rf /tmp/abx-proj
```

Phase 6 is "done" for autopilot purposes when:
- Part A: safe mode resolves api.anthropic.com (allowed) but returns NXDOMAIN for a
  domain in the threat feed; allowlist mode resolves github.com but NXDOMAIN for npmjs.org.
- Part B (Linux only): direct-IP curl from the box is BLOCKED in safe mode with default
  block_direct_ip; allowlist mode allows curl github.com but blocks curl npmjs.org.
- `box net` shows recent queries with their status.
- `agentbox rm` cleans up boxes + sidecars + networks + iptables/ipset state.
- `agentbox ls` shows only user-facing boxes (one row per box, not three).
