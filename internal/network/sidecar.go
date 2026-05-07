package network

import (
	"github.com/nklisch/agentbox/internal/runspec"
	"github.com/nklisch/agentbox/internal/state"
)

// CoreDNSImage is the pinned CoreDNS image used as the sidecar.
// Verified 2026-05-05 against hub.docker.com/r/coredns/coredns/tags:
// latest stable tag is 1.14.3 (released 2026-04-22).
const CoreDNSImage = "docker.io/coredns/coredns:1.14.3"

// SidecarInput collects the data BuildSidecar needs.
type SidecarInput struct {
	Spec     Spec
	StateDir string // <state-dir>/sessions/<project_id>
}

// BuildSidecar returns runspec.PodmanCreateArgs for the CoreDNS sidecar.
//
// The sidecar:
//   - Gets a static IP (spec.SidecarIP) on the per-project network so the
//     agentbox container can always reach it at a known address via --dns.
//   - Mounts the generated Corefile from the session state dir at /Corefile
//     (CoreDNS's default config path when working dir is /).
//   - Writes query logs to stdout; callers read via `podman logs agentbox-coredns-<id>`.
//   - Is labeled agentbox.role=coredns so `agentbox ls` filters it from the
//     user-facing box list.
//
// Note: the CoreDNS 1.14.3 log plugin does NOT support file-based output —
// it writes to stdout only. The design's "log /var/log/coredns/queries.log"
// is not supported in this version; box-net uses "podman logs" instead.
func BuildSidecar(in SidecarInput) runspec.PodmanCreateArgs {
	return runspec.PodmanCreateArgs{
		Name: in.Spec.SidecarName,
		Labels: []runspec.KV{
			{Key: "agentbox", Value: "1"},
			{Key: "agentbox.role", Value: "coredns"},
			{Key: "agentbox.project_id", Value: in.Spec.ProjectID},
		},
		Mounts: []runspec.Mount{
			// Corefile at /Corefile — CoreDNS default config path (workdir is /).
			{Source: state.CorefilePath(in.StateDir), Target: "/Corefile", Mode: "ro"},
		},
		Network: in.Spec.NetworkName,
		// Static IP so the agentbox container's --dns flag can always find the
		// sidecar at a predictable address. Verified: podman bridge networks with
		// --subnet support --ip assignment (tested 2026-05-05).
		IP:    in.Spec.SidecarIP,
		Image: CoreDNSImage,
		// No argv needed: CoreDNS entrypoint is /coredns, default config is /Corefile.
		Argv: []string{},
	}
}
