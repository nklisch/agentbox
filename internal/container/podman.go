package container

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/nklisch/agentbox/internal/runspec"
)

// runQuiet runs cmd capturing stderr; on non-zero exit, the returned error
// includes the trimmed stderr so callers (and users) see why podman failed.
// stdout is discarded — these commands don't return data on stdout for
// success paths the lifecycle uses.
func runQuiet(label string, cmd *exec.Cmd) error {
	var stderr bytes.Buffer
	cmd.Stdout = io.Discard
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return fmt.Errorf("%s: %w: %s", label, err, msg)
		}
		return fmt.Errorf("%s: %w", label, err)
	}
	return nil
}

// PodmanRuntime shells out to `podman` (or `docker`).
type PodmanRuntime struct {
	Bin string // "podman" or "docker"
}

// NewPodmanRuntime returns a Runtime backed by the named binary.
func NewPodmanRuntime(bin string) *PodmanRuntime {
	return &PodmanRuntime{Bin: bin}
}

// Create runs `<bin> create [...args from PodmanCreateArgs]`.
func (r *PodmanRuntime) Create(args runspec.PodmanCreateArgs) error {
	argv := []string{"create", "--name", args.Name}
	for _, kv := range args.Labels {
		argv = append(argv, "--label", kv.Key+"="+kv.Value)
	}
	for _, m := range args.Mounts {
		argv = append(argv, "-v", m.Source+":"+m.Target+":"+m.Mode)
	}
	if args.Workdir != "" {
		argv = append(argv, "-w", args.Workdir)
	}
	if args.CPUs > 0 {
		argv = append(argv, "--cpus", fmt.Sprintf("%d", args.CPUs))
	}
	if args.Memory != "" {
		argv = append(argv, "--memory", args.Memory)
	}
	if args.PIDs > 0 {
		argv = append(argv, "--pids-limit", fmt.Sprintf("%d", args.PIDs))
	}
	for _, c := range args.CapDrop {
		argv = append(argv, "--cap-drop", c)
	}
	for _, c := range args.CapAdd {
		argv = append(argv, "--cap-add", c)
	}
	for _, d := range args.Devices {
		argv = append(argv, "--device", d)
	}
	for _, s := range args.SecOpt {
		argv = append(argv, "--security-opt", s)
	}
	if args.Network != "" {
		argv = append(argv, "--network", args.Network)
	}
	if args.IP != "" {
		argv = append(argv, "--ip", args.IP)
	}
	for _, d := range args.DNS {
		argv = append(argv, "--dns", d)
	}
	for _, s := range args.Sysctls {
		argv = append(argv, "--sysctl", s.Key+"="+s.Value)
	}
	for _, e := range args.EnvNames {
		argv = append(argv, "-e", e)
	}
	for _, e := range args.EnvVars {
		argv = append(argv, "-e", e.Key+"="+e.Value)
	}
	argv = append(argv, args.Image)
	argv = append(argv, args.Argv...)

	return runQuiet(r.Bin+" create", exec.Command(r.Bin, argv...))
}

// Start runs `<bin> start <name>`.
func (r *PodmanRuntime) Start(name string) error {
	return runQuiet(r.Bin+" start "+name, exec.Command(r.Bin, "start", name))
}

// Stop runs `<bin> stop <name>`.
func (r *PodmanRuntime) Stop(name string) error {
	return runQuiet(r.Bin+" stop "+name, exec.Command(r.Bin, "stop", name))
}

// Inspect returns the Box for name. Status=Missing if no such container.
func (r *PodmanRuntime) Inspect(name string) (Box, error) {
	cmd := exec.Command(r.Bin, "inspect", "--type", "container",
		"--format", "{{json .}}", name)
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			// Non-zero from inspect = container not found.
			return Box{Status: StatusMissing}, nil
		}
		return Box{}, err
	}
	return parseInspect(out)
}

// inspectFormat is the subset we need from `podman inspect`.
type inspectFormat struct {
	Name  string `json:"Name"`
	State struct {
		Running bool   `json:"Running"`
		Status  string `json:"Status"`
	} `json:"State"`
	Config struct {
		Labels map[string]string `json:"Labels"`
		Image  string            `json:"Image"`
	} `json:"Config"`
	Created string `json:"Created"`
}

func parseInspect(b []byte) (Box, error) {
	var raw inspectFormat
	if err := json.Unmarshal(b, &raw); err != nil {
		return Box{}, fmt.Errorf("parse inspect: %w", err)
	}
	return boxFromLabels(raw.Config.Labels, raw.State.Running, raw.Created), nil
}

func boxFromLabels(labels map[string]string, running bool, created string) Box {
	box := Box{
		ProjectID: labels["agentbox.project_id"],
		Project:   labels["agentbox.project"],
		CWD:       labels["agentbox.cwd"],
		Agent:     labels["agentbox.agent"],
		KitImage:  labels["agentbox.kit_image"],
		Role:      labels["agentbox.role"],
	}
	if k := labels["agentbox.kits"]; k != "" {
		box.Kits = strings.Split(k, ",")
	}
	if running {
		box.Status = StatusRunning
	} else {
		box.Status = StatusStopped
	}
	// Created may be the container's creation time or the agentbox label;
	// prefer the label when present (deterministic).
	if c := labels["agentbox.created"]; c != "" {
		if t, err := time.Parse(time.RFC3339, c); err == nil {
			box.Created = t
		}
	} else if created != "" {
		if t, err := time.Parse(time.RFC3339Nano, created); err == nil {
			box.Created = t
		}
	}
	return box
}

// Exec runs a command in a running container, streaming stdio per opts.
func (r *PodmanRuntime) Exec(name string, opts ExecOpts) (int, error) {
	argv := []string{"exec"}
	if opts.Interactive {
		argv = append(argv, "-i")
	}
	if opts.TTY {
		argv = append(argv, "-t")
	}
	if opts.Workdir != "" {
		argv = append(argv, "-w", opts.Workdir)
	}
	argv = append(argv, name)
	argv = append(argv, opts.Argv...)

	cmd := exec.Command(r.Bin, argv...)
	cmd.Stdin = opts.Stdin
	cmd.Stdout = opts.Stdout
	cmd.Stderr = opts.Stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode(), nil
		}
		return -1, fmt.Errorf("%s exec: %w", r.Bin, err)
	}
	return 0, nil
}

// Ls returns all agentbox-labeled boxes.
func (r *PodmanRuntime) Ls(all bool) ([]Box, error) {
	argv := []string{"ps", "--filter", "label=agentbox=1", "--format", "{{.Names}}"}
	if all {
		// Insert -a after "ps" subcommand.
		argv = append([]string{argv[0], "-a"}, argv[1:]...)
	}
	cmd := exec.Command(r.Bin, argv...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s ps: %w", r.Bin, err)
	}
	var boxes []Box
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		box, err := r.Inspect(line)
		if err != nil {
			continue // skip unreadable
		}
		if box.Status == StatusMissing {
			continue
		}
		boxes = append(boxes, box)
	}
	return boxes, nil
}

// NetworkCreate creates a bridge network with the given CIDR subnet.
// Runs `<bin> network create --driver bridge --subnet <subnet> <name>`.
func (r *PodmanRuntime) NetworkCreate(name, subnet string) error {
	cmd := exec.Command(r.Bin, "network", "create",
		"--driver", "bridge", "--subnet", subnet, name)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return fmt.Errorf("%s network create: %w", r.Bin, err)
		}
		return err
	}
	return nil
}

// NetworkRm removes a named network. Idempotent: missing network returns nil.
// Runs `<bin> network rm <name>`.
func (r *PodmanRuntime) NetworkRm(name string) error {
	cmd := exec.Command(r.Bin, "network", "rm", name)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			// Exit code 1 typically means network not found — treat as no-op.
			return nil
		}
		return err
	}
	return nil
}

// NetworkExists reports whether a network with the given name exists.
// Runs `<bin> network exists <name>`; exit 0 = present, non-zero = absent.
// Note: `network exists` is podman-specific; docker uses `network inspect`.
// Phase 8's docker fallback will add a docker-compatible implementation.
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

// Rm removes a container. force=true uses --force.
func (r *PodmanRuntime) Rm(name string, force bool) error {
	argv := []string{"rm"}
	if force {
		argv = append(argv, "--force")
	}
	argv = append(argv, name)
	cmd := exec.Command(r.Bin, argv...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			// Already absent — non-fatal.
			return nil
		}
		return err
	}
	return nil
}
