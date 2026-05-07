package lifecycle

import (
	"fmt"
	"io"

	"github.com/nklisch/agentbox/internal/config"
	"github.com/nklisch/agentbox/internal/container"
	"github.com/nklisch/agentbox/internal/network"
	"github.com/nklisch/agentbox/internal/state"
)

// renderRmShell writes to w the shell commands that rmOne would execute for
// projID. Output is one command per line, prefixed with the runtime
// (`podman` or `docker`) from cfg. State-dir removal is rendered as a
// literal `rm -rf` so the user can copy-paste.
//
// Order matches the live path:
//  1. podman stop <name>           (only if box is running)
//  2. podman rm <name>
//  3. <network teardown>           (per-project network + sidecar)
//  4. rm -rf <state dir>           (unless keepState)
//
// netSpec must match what NetworkManager.SpecFor returns at run time.
// Pass nil if no network manager is configured (matches lifecycle.go's
// rmOne early-out).
func renderRmShell(
	w io.Writer,
	cfg config.Config,
	projID string,
	box container.Box,
	keepState bool,
	netSpec *network.Spec,
) error {
	rt := cfg.Runtime
	if rt == "" {
		rt = "podman"
	}
	name := container.ContainerName(projID)

	fmt.Fprintf(w, "# project_id = %s\n", projID)

	if box.Status == container.StatusRunning {
		fmt.Fprintf(w, "%s stop %s\n", rt, name)
	}
	fmt.Fprintf(w, "%s rm %s\n", rt, name)

	if netSpec != nil && netSpec.NetworkName != "" &&
		netSpec.NetworkName != "none" && netSpec.NetworkName != "bridge" {
		fmt.Fprintf(w, "%s network rm %s\n", rt, netSpec.NetworkName)
		if netSpec.SidecarName != "" {
			fmt.Fprintf(w, "%s rm -f %s\n", rt, netSpec.SidecarName)
		}
	}

	if !keepState {
		stateDir, err := state.SessionDir(projID)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "rm -rf %s\n", stateDir)
	}
	return nil
}
