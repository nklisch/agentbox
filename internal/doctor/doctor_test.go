package doctor_test

import (
	"strings"
	"testing"

	"github.com/nklisch/agentbox/internal/config"
	"github.com/nklisch/agentbox/internal/doctor"
)

func TestRun_ReturnsAtLeastTwoChecks(t *testing.T) {
	cfg := config.DefaultConfig()
	result := doctor.Run(cfg)
	if len(result.Checks) < 2 {
		t.Errorf("Run() returned %d checks, want at least 2", len(result.Checks))
	}
}

func TestRun_RuntimeCheckNames(t *testing.T) {
	cfg := config.DefaultConfig()
	result := doctor.Run(cfg)

	var names []string
	for _, c := range result.Checks {
		names = append(names, c.Name)
	}

	found := func(name string) bool {
		for _, n := range names {
			if n == name {
				return true
			}
		}
		return false
	}

	if !found("runtime") {
		t.Errorf("expected 'runtime' check, got %v", names)
	}
	if !found("state-dir") {
		t.Errorf("expected 'state-dir' check, got %v", names)
	}
}

func TestRun_MissingBinary_Fails(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Runtime = "definitely-missing-bin-xyz"
	result := doctor.Run(cfg)

	var runtimeCheck *doctor.Check
	for i := range result.Checks {
		if result.Checks[i].Name == "runtime" {
			runtimeCheck = &result.Checks[i]
			break
		}
	}
	if runtimeCheck == nil {
		t.Fatal("runtime check not found")
	}
	if runtimeCheck.Status != doctor.StatusFail {
		t.Errorf("runtime check status = %q, want %q", runtimeCheck.Status, doctor.StatusFail)
	}
}

func TestAnyFail_True(t *testing.T) {
	result := doctor.Result{
		Checks: []doctor.Check{
			{Name: "a", Status: doctor.StatusOK, Message: "fine"},
			{Name: "b", Status: doctor.StatusFail, Message: "broken"},
		},
	}
	if !result.AnyFail() {
		t.Error("AnyFail() = false, want true when a check has FAIL status")
	}
}

func TestAnyFail_False(t *testing.T) {
	result := doctor.Result{
		Checks: []doctor.Check{
			{Name: "a", Status: doctor.StatusOK, Message: "fine"},
			{Name: "b", Status: doctor.StatusWarn, Message: "warning"},
		},
	}
	if result.AnyFail() {
		t.Error("AnyFail() = true, want false when no check has FAIL status")
	}
}

func TestAnyFail_Empty(t *testing.T) {
	result := doctor.Result{}
	if result.AnyFail() {
		t.Error("AnyFail() = true on empty checks, want false")
	}
}

func TestRun_StateDirCheck_CreatesDir(t *testing.T) {
	// Use XDG_DATA_HOME pointing to a temp dir to isolate from real state.
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)

	cfg := config.DefaultConfig()
	// Use a runtime that doesn't exist so we don't depend on the environment.
	cfg.Runtime = "definitely-missing-bin-xyz"
	result := doctor.Run(cfg)

	var stateDirCheck *doctor.Check
	for i := range result.Checks {
		if result.Checks[i].Name == "state-dir" {
			stateDirCheck = &result.Checks[i]
			break
		}
	}
	if stateDirCheck == nil {
		t.Fatal("state-dir check not found")
	}
	// State dir check should succeed (idempotent directory creation).
	if stateDirCheck.Status != doctor.StatusOK {
		t.Errorf("state-dir check status = %q (%s), want OK", stateDirCheck.Status, stateDirCheck.Message)
	}
}

func TestRun_AnyFail_WithMissingRuntime(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)

	cfg := config.DefaultConfig()
	cfg.Runtime = "definitely-missing-bin-xyz"
	result := doctor.Run(cfg)

	if !result.AnyFail() {
		t.Error("AnyFail() = false, want true when runtime binary is missing")
	}
}

// ---- Phase 7: containersConfigCheck tests ----

func findCheck(result doctor.Result, name string) *doctor.Check {
	for i := range result.Checks {
		if result.Checks[i].Name == name {
			return &result.Checks[i]
		}
	}
	return nil
}

func TestContainersConfigCheck_NoKit_IsOK(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cfg := config.DefaultConfig()
	cfg.DefaultKits = []string{"polyglot"}
	cfg.Containers.Enable = false
	result := doctor.Run(cfg)

	c := findCheck(result, "containers-config")
	if c == nil {
		t.Fatal("containers-config check not found")
	}
	if c.Status != doctor.StatusOK {
		t.Errorf("status = %q, want OK when containers kit absent", c.Status)
	}
}

func TestContainersConfigCheck_KitAndEnableTrue_IsOK(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cfg := config.DefaultConfig()
	cfg.DefaultKits = []string{"containers"}
	cfg.Containers.Enable = true
	result := doctor.Run(cfg)

	c := findCheck(result, "containers-config")
	if c == nil {
		t.Fatal("containers-config check not found")
	}
	if c.Status != doctor.StatusOK {
		t.Errorf("status = %q, want OK when containers kit present and Enable=true", c.Status)
	}
}

func TestContainersConfigCheck_KitButEnableFalse_IsWarn(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cfg := config.DefaultConfig()
	cfg.DefaultKits = []string{"containers"}
	cfg.Containers.Enable = false
	result := doctor.Run(cfg)

	c := findCheck(result, "containers-config")
	if c == nil {
		t.Fatal("containers-config check not found")
	}
	if c.Status != doctor.StatusWarn {
		t.Errorf("status = %q, want WARN when containers kit present but Enable=false", c.Status)
	}
	if !strings.Contains(c.Message, "enable=true") {
		t.Errorf("message should mention 'enable=true', got: %q", c.Message)
	}
}
