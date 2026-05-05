package container

import (
	"github.com/nklisch/agentbox/internal/runspec"
)

// Runtime is the port over a container engine (podman or docker). The
// PodmanRuntime adapter implements it via os/exec; tests use a fake.
type Runtime interface {
	// Create creates a container per args. Container is in "stopped" state
	// (per SPEC.md's `sleep infinity` model). Returns nil if a container
	// with the same name already exists; caller checks via Inspect first.
	Create(args runspec.PodmanCreateArgs) error

	// Start moves a stopped container to running. No-op if already running.
	Start(name string) error

	// Stop stops a running container. No-op if already stopped.
	Stop(name string) error

	// Inspect returns the Box for `name`. Status=Missing if no such container.
	Inspect(name string) (Box, error)

	// Exec runs a command inside a running container, streaming stdio per
	// ExecOpts. Returns the command's exit code (or an error if exec itself
	// failed to start).
	Exec(name string, opts ExecOpts) (int, error)

	// Ls returns all agentbox-labeled boxes. all=true includes stopped ones;
	// all=false returns running only.
	Ls(all bool) ([]Box, error)

	// Rm removes a container. force=true sends SIGKILL first if running.
	// Idempotent: missing container returns nil.
	Rm(name string, force bool) error

	// NetworkCreate creates a bridge network with the given name and CIDR subnet.
	// Idempotent in the sense that callers check NetworkExists first.
	NetworkCreate(name, subnet string) error

	// NetworkRm removes a named network. Idempotent: missing network returns nil.
	NetworkRm(name string) error

	// NetworkExists reports whether a network with the given name exists.
	NetworkExists(name string) (bool, error)
}
