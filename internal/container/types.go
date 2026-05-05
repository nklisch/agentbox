package container

import (
	"io"
	"time"
)

// Status represents a container's current state.
type Status string

const (
	StatusRunning Status = "running"
	StatusStopped Status = "stopped"
	StatusMissing Status = "missing"
)

// Box describes an agentbox container as projected from podman labels.
type Box struct {
	ProjectID string    `json:"project_id"`
	Project   string    `json:"project"`
	CWD       string    `json:"cwd"`
	Agent     string    `json:"agent"`
	Kits      []string  `json:"kits"`
	KitImage  string    `json:"kit_image"`
	Status    Status    `json:"status"`
	Created   time.Time `json:"created"`
}

// ContainerName returns the canonical podman container name for a box.
// Mirrors project.ContainerName ("agentbox-<id>"). Defined here so the
// container package is self-contained.
func ContainerName(projectID string) string {
	return "agentbox-" + projectID
}

// ExecOpts controls Runtime.Exec.
type ExecOpts struct {
	Argv        []string
	Workdir     string
	Interactive bool
	TTY         bool
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
}
