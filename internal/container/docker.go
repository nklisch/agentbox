package container

import (
	"errors"
	"io"
	"os/exec"
)

// Compile-time interface satisfaction.
var _ Runtime = (*DockerRuntime)(nil)

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
