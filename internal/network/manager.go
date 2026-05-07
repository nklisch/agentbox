package network

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/nklisch/agentbox/internal/config"
	"github.com/nklisch/agentbox/internal/container"
	"github.com/nklisch/agentbox/internal/state"
)

// Manager orchestrates per-project network setup: creates the podman network,
// writes the Corefile, starts the CoreDNS sidecar.
// It follows the same orchestrator pattern as internal/lifecycle.
type Manager struct {
	Runtime      container.Runtime
	IPTables     *IPTables // nil means iptables enforcement is disabled
	NetfilterBin string    // path/name of agentbox-netfilter binary; defaults to "agentbox-netfilter"
	Stderr       io.Writer // nil → io.Discard; populated by Lifecycle factory
}

// stderrOf returns m.Stderr or io.Discard if unset.
func (m *Manager) stderrOf() io.Writer {
	if m.Stderr == nil {
		return io.Discard
	}
	return m.Stderr
}

// SpecFor builds a Spec for the given project + config.
// Subnet derivation: 10.89.<X>.0/24 where X is the first byte of the
// project_id hex string (first 2 hex chars) parsed as decimal.
//
// Example: project_id "ef38f6..." → first 2 chars "ef" → 0xef = 239
//
//	→ subnet 10.89.239.0/24, sidecar IP 10.89.239.2
//
// Collision analysis: 256 possible subnets (one per first-byte value),
// sha1-derived, so collisions are unlikely for personal use (a few dozen
// projects). When two projects happen to collide, the second Setup will
// fail on network create — the caller will see a clear error. This is an
// acceptable trade-off for stable, predictable addressing.
func (m *Manager) SpecFor(cfg config.Config, projectID string) Spec {
	s := Spec{
		ProjectID:   projectID,
		Mode:        Mode(cfg.Network.Mode),
		NetworkName: container.NetworkName(projectID),
		SidecarName: "agentbox-coredns-" + projectID,
		Cfg:         cfg,
	}
	s.Subnet = subnetFromProjectID(projectID)
	s.SidecarIP = sidecarIPFromSubnet(s.Subnet)
	if s.IpsetEnabled() {
		s.NetfilterName = "agentbox-netfilter-" + projectID
	}
	return s
}

// Setup brings up the network and sidecar for spec. Idempotent — repeated
// Setup with the same spec is a no-op for already-running pieces.
//
// For mode=off: returns Info{NetworkName: "none"}, no sidecar.
// For mode=open: returns Info{NetworkName: "bridge"}, no sidecar.
// For mode=safe|allowlist: creates the network if absent, writes Corefile,
// creates+starts the sidecar.
func (m *Manager) Setup(spec Spec) (Info, error) {
	if !spec.Mode.Filtered() {
		return infoForUnfiltered(spec.Mode), nil
	}

	// Create the per-project bridge network if it doesn't exist yet.
	exists, err := m.Runtime.NetworkExists(spec.NetworkName)
	if err != nil {
		return Info{}, fmt.Errorf("network exists check: %w", err)
	}
	if !exists {
		if err := m.Runtime.NetworkCreate(spec.NetworkName, spec.Subnet); err != nil {
			return Info{}, fmt.Errorf("network create: %w", err)
		}
	}

	// Write the Corefile to the session state dir.
	if err := writeCorefile(spec); err != nil {
		return Info{}, fmt.Errorf("write corefile: %w", err)
	}

	// Create the sidecar container if it doesn't exist.
	sb, err := m.Runtime.Inspect(spec.SidecarName)
	if err != nil {
		return Info{}, fmt.Errorf("sidecar inspect: %w", err)
	}
	if sb.Status == container.StatusMissing {
		stateDir, err := state.SessionDir(spec.ProjectID)
		if err != nil {
			return Info{}, fmt.Errorf("session dir: %w", err)
		}
		args := BuildSidecar(SidecarInput{Spec: spec, StateDir: stateDir})
		if err := m.Runtime.Create(args); err != nil {
			return Info{}, fmt.Errorf("sidecar create: %w", err)
		}
	}

	// Start the sidecar (no-op if already running).
	if err := m.Runtime.Start(spec.SidecarName); err != nil {
		return Info{}, fmt.Errorf("sidecar start: %w", err)
	}

	// Part B: set up iptables/ipset and start the netfilter daemon when enabled.
	if spec.IpsetEnabled() && m.IPTables != nil {
		if err := m.IPTables.SetupRules(spec); err != nil {
			return Info{}, fmt.Errorf("iptables setup: %w", err)
		}
		if spec.Mode == ModeAllowlist {
			if err := m.IPTables.PrePopulate(spec, spec.Cfg.Network.Allowlist.Allow); err != nil {
				// Best-effort: log but don't abort setup.
				fmt.Fprintf(m.stderrOf(), "warning: allowlist prepopulate: %v\n", err)
			}
		}
		if err := m.startNetfilterDaemon(spec); err != nil {
			return Info{}, fmt.Errorf("netfilter daemon start: %w", err)
		}
	}

	return Info{
		NetworkName: spec.NetworkName,
		// SidecarIP is deterministic from the project_id — no need to inspect
		// the running container to discover it. This is the key advantage of
		// the stable subnet derivation: the agentbox container's --dns flag
		// can be set before the sidecar even starts.
		SidecarDNS: []string{spec.SidecarIP},
	}, nil
}

// Teardown stops + removes the sidecar and the podman network.
// Idempotent: missing pieces are not errors.
//
// Teardown order (reverses Setup order):
//  1. Stop the netfilter daemon (SIGTERM, best-effort).
//  2. Teardown iptables/ipset rules (best-effort).
//  3. Remove sidecar container (force).
//  4. Remove the podman network.
func (m *Manager) Teardown(spec Spec) error {
	if !spec.Mode.Filtered() {
		return nil
	}

	// Part B: stop daemon + teardown iptables before touching the network.
	if spec.IpsetEnabled() && m.IPTables != nil {
		_ = m.stopNetfilterDaemon(spec)    // best-effort: missing PID or dead process is OK
		_ = m.IPTables.TeardownRules(spec) // best-effort: missing rules/set is OK
	}

	// Remove sidecar first (it's attached to the network), then the network.
	if err := m.Runtime.Rm(spec.SidecarName, true); err != nil {
		return fmt.Errorf("sidecar rm: %w", err)
	}
	if err := m.Runtime.NetworkRm(spec.NetworkName); err != nil {
		return fmt.Errorf("network rm: %w", err)
	}
	return nil
}

// netfilterBin returns the effective binary path/name for agentbox-netfilter.
// Falls back to "agentbox-netfilter" (expected on PATH after `make install`).
func (m *Manager) netfilterBin() string {
	if m.NetfilterBin != "" {
		return m.NetfilterBin
	}
	return "agentbox-netfilter"
}

// startNetfilterDaemon forks agentbox-netfilter as a detached background process.
//
// The daemon is spawned with Setsid=true so it survives the parent process
// exiting. The PID is written to <state-dir>/netfilter.pid for later cleanup
// by stopNetfilterDaemon.
//
// The daemon needs CAP_NET_ADMIN to call ipset — it runs under sudo when
// IPTables.Sudo is true (which it is when agentbox runs as a normal user).
func (m *Manager) startNetfilterDaemon(spec Spec) error {
	stateDir, err := state.SessionDir(spec.ProjectID)
	if err != nil {
		return fmt.Errorf("session dir: %w", err)
	}

	bin := m.netfilterBin()
	args := []string{
		bin,
		"--coredns-container", spec.SidecarName,
		"--ipset", ipsetName(spec),
	}

	var cmd *exec.Cmd
	if m.IPTables != nil && m.IPTables.Sudo {
		sudoArgs := append([]string{"-n"}, args...)
		cmd = exec.Command("sudo", sudoArgs...)
	} else {
		cmd = exec.Command(args[0], args[1:]...)
	}

	// Detach: new session so the daemon outlives the parent.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdout = nil
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start netfilter daemon: %w", err)
	}
	pid := cmd.Process.Pid

	// Write PID file for later cleanup.
	pidFile := filepath.Join(stateDir, "netfilter.pid")
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d", pid)), 0o644); err != nil {
		// Non-fatal: daemon is running; we just can't signal it cleanly later.
		fmt.Fprintf(m.stderrOf(), "warning: write netfilter.pid: %v\n", err)
	}

	// Release the process handle — we don't want to reap it.
	_ = cmd.Process.Release()
	return nil
}

// stopNetfilterDaemon reads <state-dir>/netfilter.pid and sends SIGTERM.
// Best-effort: missing PID file or already-dead process is not an error.
func (m *Manager) stopNetfilterDaemon(spec Spec) error {
	stateDir, err := state.SessionDir(spec.ProjectID)
	if err != nil {
		return nil // can't locate PID file — give up quietly
	}
	pidFile := filepath.Join(stateDir, "netfilter.pid")
	data, err := os.ReadFile(pidFile)
	if os.IsNotExist(err) {
		return nil // no daemon was started
	}
	if err != nil {
		return nil // unreadable — best-effort
	}

	var pid int
	if _, err := fmt.Sscan(string(data), &pid); err != nil || pid <= 0 {
		return nil
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil
	}
	// SIGTERM is polite; the daemon catches it and exits cleanly.
	_ = proc.Signal(syscall.SIGTERM)
	_ = os.Remove(pidFile)
	return nil
}

// writeCorefile generates and writes the Corefile to the session state dir.
// Also ensures the session dir exists.
func writeCorefile(spec Spec) error {
	dir, err := state.SessionDir(spec.ProjectID)
	if err != nil {
		return err
	}
	if err := state.EnsureDir(dir); err != nil {
		return err
	}
	body := GenerateCorefile(spec)
	// Use 0644 (world-readable) so the CoreDNS container can read the Corefile.
	// CoreDNS runs as nonroot:nonroot; in rootless podman, the container user
	// won't map to the host file owner, so the file must be group/world-readable.
	// The Corefile contains no secrets — only DNS forwarding configuration.
	return os.WriteFile(state.CorefilePath(dir), []byte(body), 0o644)
}

// subnetFromProjectID returns "10.89.<X>.0/24" where X is the first byte of
// the project_id hex string (first 2 chars) parsed as a decimal uint8.
//
// Subnet range: 10.89.0.0/24 – 10.89.255.0/24 (256 buckets).
// Collision is possible in principle but unlikely for personal use.
func subnetFromProjectID(projectID string) string {
	if len(projectID) < 2 {
		return "10.89.0.0/24"
	}
	b, err := strconv.ParseUint(projectID[:2], 16, 8)
	if err != nil {
		return "10.89.0.0/24"
	}
	return fmt.Sprintf("10.89.%d.0/24", b)
}

// sidecarIPFromSubnet returns the first usable host address in the subnet.
// For "10.89.X.0/24", that is "10.89.X.2" (the .1 is the gateway).
func sidecarIPFromSubnet(subnet string) string {
	// subnet is "10.89.X.0/24"; replace trailing ".0/24" with ".2"
	// Simple string manipulation: find last "." before "/", replace octet+mask.
	for i := len(subnet) - 1; i >= 0; i-- {
		if subnet[i] == '.' {
			return subnet[:i+1] + "2"
		}
	}
	return "10.89.0.2" // fallback
}

// infoForUnfiltered returns the appropriate Info for non-filtered modes.
func infoForUnfiltered(mode Mode) Info {
	switch mode {
	case ModeOff:
		return Info{NetworkName: "none"}
	default: // ModeOpen
		return Info{NetworkName: "bridge"}
	}
}
