package kits

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// PodmanRunner shells out to `podman` (or `docker`). It implements Runner.
type PodmanRunner struct {
	Bin string // "podman" or "docker", from cfg.Runtime
}

// NewPodmanRunner returns a runner using the named binary.
func NewPodmanRunner(bin string) *PodmanRunner {
	return &PodmanRunner{Bin: bin}
}

// Build runs `<bin> build -t <tag> -f <dockerfile> [--no-cache] <ctxdir>`.
// stdout and stderr from the build are streamed through ctx.Stdout/ctx.Stderr
// so the user sees live progress.
func (r *PodmanRunner) Build(ctx BuildContext) error {
	args := []string{"build", "-t", ctx.Tag, "-f", ctx.Dockerfile}
	if ctx.NoCache {
		args = append(args, "--no-cache")
	}
	args = append(args, ctx.ContextDir)
	cmd := exec.Command(r.Bin, args...)
	cmd.Stdout = ctx.Stdout
	cmd.Stderr = ctx.Stderr
	return cmd.Run()
}

// HasImage runs `<bin> image inspect <tag>`. Exit 0 means the image is present
// locally; any non-zero exit means absent (we treat all ExitErrors as absent
// rather than surfacing the podman error message).
func (r *PodmanRunner) HasImage(tag string) (bool, error) {
	cmd := exec.Command(r.Bin, "image", "inspect", tag)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return false, nil // non-zero exit = image absent (or unknown tag)
		}
		return false, err // binary not found, permission denied, etc.
	}
	return true, nil
}

// LiveImageRefs returns the .Image field of every container currently labeled
// agentbox=1 (running or stopped). Used by Prune to determine which images
// are still in use and must not be removed.
func (r *PodmanRunner) LiveImageRefs() ([]string, error) {
	cmd := exec.Command(r.Bin, "ps", "-a",
		"--filter", "label=agentbox=1",
		"--format", "{{.Image}}")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s ps: %w", r.Bin, err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var refs []string
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" {
			refs = append(refs, l)
		}
	}
	return refs, nil
}

// RemoveImage runs `<bin> rmi <tag>`. Returns nil if the image is already
// absent — non-fatal for prune workflows.
func (r *PodmanRunner) RemoveImage(tag string) error {
	cmd := exec.Command(r.Bin, "rmi", tag)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil // image already gone or unknown — treat as success for prune
		}
		return err
	}
	return nil
}
