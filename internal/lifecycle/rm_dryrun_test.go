package lifecycle_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nklisch/agentbox/internal/container"
	"github.com/nklisch/agentbox/internal/lifecycle"
)

func TestRm_DryRun_RendersStopAndRm(t *testing.T) {
	rt := newFakeRuntime()
	cfg := defaultTestCfg()
	projID, projAbs := setupProject(t)
	name := "agentbox-" + projID
	rt.boxes[name] = container.Box{
		ProjectID: projID,
		CWD:       projAbs,
		Status:    container.StatusRunning,
		Role:      "box",
	}

	var stdout bytes.Buffer
	l := newTestLifecycle(t, rt, cfg, nil)
	l.Stdout = &stdout

	if err := l.Rm(lifecycle.RmOpts{Input: ".", DryRun: true}); err != nil {
		t.Fatalf("Rm dry-run: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "podman stop") {
		t.Errorf("expected 'podman stop' for running box, got:\n%s", out)
	}
	if !strings.Contains(out, "podman rm") {
		t.Errorf("expected 'podman rm' in output, got:\n%s", out)
	}
	if !strings.Contains(out, "# project_id = "+projID) {
		t.Errorf("expected project_id comment, got:\n%s", out)
	}
}

func TestRm_DryRun_DoesNotCallRuntime(t *testing.T) {
	rt := newFakeRuntime()
	cfg := defaultTestCfg()
	projID, projAbs := setupProject(t)
	name := "agentbox-" + projID
	rt.boxes[name] = container.Box{
		ProjectID: projID,
		CWD:       projAbs,
		Status:    container.StatusRunning,
		Role:      "box",
	}

	l := newTestLifecycle(t, rt, cfg, nil)
	l.Stdout = &bytes.Buffer{}

	if err := l.Rm(lifecycle.RmOpts{Input: ".", DryRun: true}); err != nil {
		t.Fatalf("Rm dry-run: %v", err)
	}

	if containsCall(rt.calls, "Rm") {
		t.Error("Rm should NOT be called during dry-run")
	}
	if containsCall(rt.calls, "Stop") {
		t.Error("Stop should NOT be called during dry-run")
	}
	// Box should still be there.
	if _, ok := rt.boxes[name]; !ok {
		t.Error("box should still exist after dry-run")
	}
}

func TestRm_DryRun_DoesNotPrompt(t *testing.T) {
	rt := newFakeRuntime()
	cfg := defaultTestCfg()
	isolateState(t)

	rt.boxes["agentbox-aaa000000000"] = container.Box{
		ProjectID: "aaa000000000",
		Status:    container.StatusRunning,
		Role:      "box",
	}

	l := newTestLifecycle(t, rt, cfg, nil)
	l.Stdout = &bytes.Buffer{}
	// l.Stdin is nil — if confirmation prompt fires, it would return an error.
	// Dry-run must skip the prompt entirely.
	if err := l.Rm(lifecycle.RmOpts{All: true, DryRun: true}); err != nil {
		t.Fatalf("Rm --all dry-run should not prompt and should not error: %v", err)
	}
}

func TestRm_DryRun_KeepStateOmitsRmRf(t *testing.T) {
	rt := newFakeRuntime()
	cfg := defaultTestCfg()
	projID, projAbs := setupProject(t)
	name := "agentbox-" + projID
	rt.boxes[name] = container.Box{
		ProjectID: projID,
		CWD:       projAbs,
		Status:    container.StatusStopped,
		Role:      "box",
	}

	var stdout bytes.Buffer
	l := newTestLifecycle(t, rt, cfg, nil)
	l.Stdout = &stdout

	if err := l.Rm(lifecycle.RmOpts{Input: ".", DryRun: true, KeepState: true}); err != nil {
		t.Fatalf("Rm dry-run --keep-state: %v", err)
	}

	out := stdout.String()
	if strings.Contains(out, "rm -rf") {
		t.Errorf("--keep-state should omit 'rm -rf' line, got:\n%s", out)
	}
	if !strings.Contains(out, "podman rm") {
		t.Errorf("expected 'podman rm' even with --keep-state, got:\n%s", out)
	}
}

func TestRm_DryRun_AllEnumeratesAllBoxes(t *testing.T) {
	rt := newFakeRuntime()
	cfg := defaultTestCfg()
	isolateState(t)

	rt.boxes["agentbox-aaa000000000"] = container.Box{
		ProjectID: "aaa000000000",
		Status:    container.StatusRunning,
		Role:      "box",
	}
	rt.boxes["agentbox-bbb000000000"] = container.Box{
		ProjectID: "bbb000000000",
		Status:    container.StatusStopped,
		Role:      "box",
	}

	var stdout bytes.Buffer
	l := newTestLifecycle(t, rt, cfg, nil)
	l.Stdout = &stdout

	if err := l.Rm(lifecycle.RmOpts{All: true, DryRun: true}); err != nil {
		t.Fatalf("Rm --all dry-run: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "aaa000000000") {
		t.Errorf("expected aaa000000000 in dry-run output, got:\n%s", out)
	}
	if !strings.Contains(out, "bbb000000000") {
		t.Errorf("expected bbb000000000 in dry-run output, got:\n%s", out)
	}
	// Running box should have a stop command; stopped box should not.
	lines := strings.Split(out, "\n")
	var aaaSection, bbbSection []string
	var current *[]string
	for _, line := range lines {
		if strings.Contains(line, "aaa000000000") && strings.HasPrefix(line, "# project_id") {
			current = &aaaSection
		} else if strings.Contains(line, "bbb000000000") && strings.HasPrefix(line, "# project_id") {
			current = &bbbSection
		}
		if current != nil {
			*current = append(*current, line)
		}
	}
	aaaStr := strings.Join(aaaSection, "\n")
	bbbStr := strings.Join(bbbSection, "\n")
	if !strings.Contains(aaaStr, "stop") {
		t.Errorf("expected 'stop' for running box aaa, section:\n%s", aaaStr)
	}
	if strings.Contains(bbbStr, "stop") {
		t.Errorf("stopped box bbb should not have 'stop' command, section:\n%s", bbbStr)
	}
}

func TestRm_DryRun_DockerRuntime(t *testing.T) {
	rt := newFakeRuntime()
	cfg := defaultTestCfg()
	cfg.Runtime = "docker"
	projID, projAbs := setupProject(t)
	name := "agentbox-" + projID
	rt.boxes[name] = container.Box{
		ProjectID: projID,
		CWD:       projAbs,
		Status:    container.StatusRunning,
		Role:      "box",
	}

	var stdout bytes.Buffer
	l := newTestLifecycle(t, rt, cfg, nil)
	l.Stdout = &stdout

	if err := l.Rm(lifecycle.RmOpts{Input: ".", DryRun: true}); err != nil {
		t.Fatalf("Rm dry-run (docker): %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "docker stop") {
		t.Errorf("expected 'docker stop' for docker runtime, got:\n%s", out)
	}
	if strings.Contains(out, "podman") {
		t.Errorf("docker runtime dry-run should not contain 'podman', got:\n%s", out)
	}
}
