package kits

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// PodmanRunner shells out to `podman` (or `docker`). It implements Runner.
type PodmanRunner struct {
	bin string // "podman" or "docker", from cfg.Runtime
}

// NewPodmanRunner returns a runner using the named binary.
func NewPodmanRunner(bin string) *PodmanRunner {
	return &PodmanRunner{bin: bin}
}

// Bin returns the runtime binary name. Implements Runner.Bin().
func (r *PodmanRunner) Bin() string { return r.bin }

// Build runs `<bin> build -t <tag> -f <dockerfile> [--no-cache] <ctxdir>`.
// stdout and stderr from the build are streamed through ctx.Stdout/ctx.Stderr
// so the user sees live progress.
func (r *PodmanRunner) Build(ctx BuildContext) error {
	args := []string{"build", "-t", ctx.Tag, "-f", ctx.Dockerfile}
	if ctx.NoCache {
		args = append(args, "--no-cache")
	}
	args = append(args, ctx.ContextDir)
	cmd := exec.Command(r.bin, args...)
	cmd.Stdout = ctx.Stdout
	cmd.Stderr = ctx.Stderr
	return cmd.Run()
}

// HasImage runs `<bin> image inspect <tag>`. Exit 0 means the image is present
// locally; any non-zero exit means absent (we treat all ExitErrors as absent
// rather than surfacing the podman error message).
func (r *PodmanRunner) HasImage(tag string) (bool, error) {
	cmd := exec.Command(r.bin, "image", "inspect", tag)
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
	cmd := exec.Command(r.bin, "ps", "-a",
		"--filter", "label=agentbox=1",
		"--format", "{{.Image}}")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s ps: %w", r.bin, err)
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
	cmd := exec.Command(r.bin, "rmi", tag)
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

// Pull runs `<bin> pull <ref>`. Stderr is captured and parsed to classify
// the failure when the pull errors. Streams stdout/stderr for live progress.
func (r *PodmanRunner) Pull(ctx PullContext) error {
	if ctx.Ref == "" {
		return &PullError{Kind: PullErrorRuntime, Wrapped: errors.New("empty ref")}
	}

	cctx := context.Background()
	if ctx.Timeout > 0 {
		var cancel context.CancelFunc
		cctx, cancel = context.WithTimeout(cctx, ctx.Timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(cctx, r.bin, "pull", ctx.Ref)
	var stderr bytes.Buffer
	if ctx.Stdout != nil {
		cmd.Stdout = ctx.Stdout
	} else {
		cmd.Stdout = io.Discard
	}
	// Tee stderr both to the user (for visibility) and to a buffer (for
	// classification on error).
	if ctx.Stderr != nil {
		cmd.Stderr = io.MultiWriter(ctx.Stderr, &stderr)
	} else {
		cmd.Stderr = &stderr
	}

	err := cmd.Run()
	if err == nil {
		return nil
	}
	return &PullError{
		Kind:    classifyPullStderr(stderr.String(), cctx.Err()),
		Ref:     ctx.Ref,
		Stderr:  stderr.String(),
		Wrapped: err,
	}
}

// classifyPullStderr inspects podman/docker stderr to label the failure.
// Returns PullErrorNetwork when ctxErr is context.DeadlineExceeded so the
// timeout case is distinguishable from a generic network error.
func classifyPullStderr(stderr string, ctxErr error) PullErrorKind {
	if errors.Is(ctxErr, context.DeadlineExceeded) {
		return PullErrorNetwork
	}
	s := strings.ToLower(stderr)
	switch {
	case strings.Contains(s, "manifest unknown"),
		strings.Contains(s, "not found"),
		strings.Contains(s, "404"):
		return PullErrorNotFound
	case strings.Contains(s, "unauthorized"),
		strings.Contains(s, "authentication required"),
		strings.Contains(s, "denied"),
		strings.Contains(s, "401"),
		strings.Contains(s, "403"):
		return PullErrorAuth
	case strings.Contains(s, "no such host"),
		strings.Contains(s, "connection refused"),
		strings.Contains(s, "timeout"),
		strings.Contains(s, "i/o timeout"),
		strings.Contains(s, "dial tcp"):
		return PullErrorNetwork
	default:
		return PullErrorUnknown
	}
}

// Tag runs `<bin> tag <src> <dst>`. Used to apply the local agentbox/<sha>
// alias to a freshly pulled remote ref so the lifecycle layer can find it
// by the same tag it always uses.
func (r *PodmanRunner) Tag(src, dst string) error {
	cmd := exec.Command(r.bin, "tag", src, dst)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s tag %s %s: %w", r.bin, src, dst, err)
	}
	return nil
}
