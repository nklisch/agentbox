package lifecycle_test

import (
	"errors"
	"testing"

	"github.com/nklisch/agentbox/internal/container"
	"github.com/nklisch/agentbox/internal/exitcode"
	"github.com/nklisch/agentbox/internal/lifecycle"
)

func newResolveLifecycle(t *testing.T, rt *fakeRuntime) *lifecycle.Lifecycle {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cfg := defaultTestCfg()
	return &lifecycle.Lifecycle{
		Cfg:     cfg,
		Runtime: rt,
	}
}

func TestResolveID_Dot(t *testing.T) {
	rt := newFakeRuntime()
	projID, _ := setupProject(t)

	l := newResolveLifecycle(t, rt)
	got, err := l.ResolveID(".")
	if err != nil {
		t.Fatalf("ResolveID('.'): %v", err)
	}
	if got != projID {
		t.Errorf("ResolveID('.') = %q, want %q", got, projID)
	}
}

func TestResolveID_AgentboxPrefix(t *testing.T) {
	rt := newFakeRuntime()
	l := newResolveLifecycle(t, rt)

	got, err := l.ResolveID("agentbox-abc123def456")
	if err != nil {
		t.Fatalf("ResolveID with agentbox- prefix: %v", err)
	}
	if got != "abc123def456" {
		t.Errorf("ResolveID('agentbox-abc123def456') = %q, want %q", got, "abc123def456")
	}
}

func TestResolveID_Exact12Char(t *testing.T) {
	rt := newFakeRuntime()
	l := newResolveLifecycle(t, rt)

	// Exact 12-char hex should return as-is without Runtime call
	before := len(rt.calls)
	got, err := l.ResolveID("abc123def456")
	if err != nil {
		t.Fatalf("ResolveID exact: %v", err)
	}
	if got != "abc123def456" {
		t.Errorf("ResolveID('abc123def456') = %q, want same", got)
	}
	// No Ls call should have been made
	if len(rt.calls) != before {
		t.Error("expected no Runtime calls for exact 12-char hex ID")
	}
}

func TestResolveID_PrefixUniqueMatch(t *testing.T) {
	rt := newFakeRuntime()
	rt.boxes["agentbox-abc123def456"] = container.Box{
		ProjectID: "abc123def456",
		Status:    container.StatusRunning,
	}
	rt.boxes["agentbox-xyz789uvw012"] = container.Box{
		ProjectID: "xyz789uvw012",
		Status:    container.StatusRunning,
	}

	l := newResolveLifecycle(t, rt)
	got, err := l.ResolveID("abc")
	if err != nil {
		t.Fatalf("ResolveID prefix: %v", err)
	}
	if got != "abc123def456" {
		t.Errorf("ResolveID('abc') = %q, want %q", got, "abc123def456")
	}
}

func TestResolveID_PrefixAmbiguous(t *testing.T) {
	rt := newFakeRuntime()
	rt.boxes["agentbox-abc111111111"] = container.Box{ProjectID: "abc111111111", Status: container.StatusRunning}
	rt.boxes["agentbox-abc222222222"] = container.Box{ProjectID: "abc222222222", Status: container.StatusRunning}

	l := newResolveLifecycle(t, rt)
	_, err := l.ResolveID("abc")
	if err == nil {
		t.Fatal("expected error for ambiguous prefix")
	}
	var ee *exitcode.Err
	if !errors.As(err, &ee) {
		t.Fatalf("expected *exitcode.Err, got %T", err)
	}
	if ee.Code != exitcode.InvalidArgs {
		t.Errorf("expected InvalidArgs (%d), got %d", exitcode.InvalidArgs, ee.Code)
	}
	if !containsStr(ee.Error(), "abc111111111") || !containsStr(ee.Error(), "abc222222222") {
		t.Errorf("ambiguous error should list matches, got: %q", ee.Error())
	}
}

func TestResolveID_NoMatch(t *testing.T) {
	rt := newFakeRuntime()
	rt.boxes["agentbox-xyz789uvw012"] = container.Box{ProjectID: "xyz789uvw012", Status: container.StatusRunning}

	l := newResolveLifecycle(t, rt)
	_, err := l.ResolveID("nope")
	if err == nil {
		t.Fatal("expected error for non-matching prefix")
	}
	var ee *exitcode.Err
	if !errors.As(err, &ee) {
		t.Fatalf("expected *exitcode.Err, got %T", err)
	}
	if ee.Code != exitcode.NotFound {
		t.Errorf("expected NotFound (%d), got %d", exitcode.NotFound, ee.Code)
	}
}

func TestResolveID_Empty(t *testing.T) {
	rt := newFakeRuntime()
	l := newResolveLifecycle(t, rt)

	_, err := l.ResolveID("")
	if err == nil {
		t.Fatal("expected error for empty input")
	}
	var ee *exitcode.Err
	if !errors.As(err, &ee) {
		t.Fatalf("expected *exitcode.Err, got %T", err)
	}
	if ee.Code != exitcode.InvalidArgs {
		t.Errorf("expected InvalidArgs (%d), got %d", exitcode.InvalidArgs, ee.Code)
	}
}

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}())
}
