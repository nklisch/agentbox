package network

import (
	"runtime"

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
	ProjectID     string
	Mode          Mode
	NetworkName   string // "agentbox-net-<id>"
	SidecarName   string // "agentbox-coredns-<id>"
	NetfilterName string // "agentbox-netfilter-<id>" (Part B; empty when not used)
	SidecarIP     string // assigned to CoreDNS container, e.g., "10.89.239.2"
	Subnet        string // "10.89.X.0/24" — derived from project_id for stable subnets
	Cfg           config.Config
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
func (s Spec) IpsetEnabled() bool {
	if runtime.GOOS == "darwin" {
		return false
	}
	switch s.Mode {
	case ModeSafe:
		return s.Cfg.Network.Safe.BlockDirectIP
	case ModeAllowlist:
		return true
	}
	return false
}
