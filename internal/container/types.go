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
	// Role is the agentbox.role label value: "box", "coredns", or "netfilter".
	// Empty string means unlabeled (pre-Phase-6 box, treated as "box").
	Role string `json:"role"`
}

// ContainerName returns the canonical podman container name for a project ID.
func ContainerName(projectID string) string { return "agentbox-" + projectID }

// NetworkName returns the canonical podman network name for a project ID.
func NetworkName(projectID string) string { return "agentbox-net-" + projectID }

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
