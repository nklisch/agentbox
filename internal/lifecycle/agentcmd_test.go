package lifecycle_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/nklisch/agentbox/internal/exitcode"
	"github.com/nklisch/agentbox/internal/lifecycle"
)

func TestBuildAgentCmd_EmptyMode_ReturnsCopy(t *testing.T) {
	base := []string{"claude", "--dangerously-skip-permissions"}
	got, err := lifecycle.BuildAgentCmd(base, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != len(base) {
		t.Fatalf("expected len %d, got %d: %v", len(base), len(got), got)
	}
	for i := range base {
		if got[i] != base[i] {
			t.Errorf("element %d: want %q, got %q", i, base[i], got[i])
		}
	}
}

func TestBuildAgentCmd_ClaudePlusMode_RewritesPrefix(t *testing.T) {
	base := []string{"claude", "--dangerously-skip-permissions"}
	got, err := lifecycle.BuildAgentCmd(base, "create")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"claude-mode", "create", "--dangerously-skip-permissions"}
	if len(got) != len(want) {
		t.Fatalf("expected len %d, got %d: %v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("element %d: want %q, got %q", i, want[i], got[i])
		}
	}
}

func TestBuildAgentCmd_ClaudeOnly_RewritesToTwoTokens(t *testing.T) {
	base := []string{"claude"}
	got, err := lifecycle.BuildAgentCmd(base, "safe")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"claude-mode", "safe"}
	if len(got) != len(want) {
		t.Fatalf("expected len %d, got %d: %v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("element %d: want %q, got %q", i, want[i], got[i])
		}
	}
}

func TestBuildAgentCmd_NonClaude_RejectsWithExitcode(t *testing.T) {
	base := []string{"codex", "--dangerously-bypass-approvals-and-sandbox"}
	got, err := lifecycle.BuildAgentCmd(base, "create")
	if got != nil {
		t.Errorf("expected nil slice, got %v", got)
	}
	var ee *exitcode.Err
	if !errors.As(err, &ee) {
		t.Fatalf("expected *exitcode.Err, got %T: %v", err, err)
	}
	if ee.Code != exitcode.InvalidArgs {
		t.Errorf("expected Code=%d (InvalidArgs), got %d", exitcode.InvalidArgs, ee.Code)
	}
	msg := ee.Error()
	if !strings.Contains(msg, "claude") {
		t.Errorf("error message should reference 'claude': %s", msg)
	}
	if !strings.Contains(msg, "codex") {
		t.Errorf("error message should reference 'codex': %s", msg)
	}
}

func TestBuildAgentCmd_EmptyBase_RejectsWithExitcode(t *testing.T) {
	got, err := lifecycle.BuildAgentCmd(nil, "create")
	if got != nil {
		t.Errorf("expected nil slice, got %v", got)
	}
	var ee *exitcode.Err
	if !errors.As(err, &ee) {
		t.Fatalf("expected *exitcode.Err, got %T: %v", err, err)
	}
	if ee.Code != exitcode.InvalidArgs {
		t.Errorf("expected Code=%d (InvalidArgs), got %d", exitcode.InvalidArgs, ee.Code)
	}
	if !strings.Contains(ee.Error(), "<empty>") {
		t.Errorf("error message should contain '<empty>': %s", ee.Error())
	}
}

func TestBuildAgentCmd_DefensiveCopy_DoesNotMutateInput(t *testing.T) {
	base := []string{"claude", "--dangerously-skip-permissions"}
	origLen := len(base)
	origFirst := base[0]

	// Empty mode path.
	got, _ := lifecycle.BuildAgentCmd(base, "")
	got[0] = "mutated"
	if base[0] != origFirst {
		t.Errorf("empty-mode: mutation of returned slice altered input base[0]: got %q", base[0])
	}

	// Non-empty mode path.
	base2 := []string{"claude", "--dangerously-skip-permissions"}
	got2, _ := lifecycle.BuildAgentCmd(base2, "create")
	got2[0] = "mutated"
	if base2[0] != "claude" {
		t.Errorf("mode path: mutation of returned slice altered input base2[0]: got %q", base2[0])
	}

	if len(base) != origLen {
		t.Errorf("input slice length changed: want %d, got %d", origLen, len(base))
	}
}

