package doctor_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
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

// ---- Phase 8: ApplyFixes + new check tests ----

// TestApplyFixes_SkipsOKChecks verifies that ApplyFixes only calls Fix on
// FAIL/WARN checks, never on OK ones.
func TestApplyFixes_SkipsOKChecks(t *testing.T) {
	okCalled := false
	warnCalled := false
	failCalled := false

	result := doctor.Result{
		Checks: []doctor.Check{
			{Name: "ok-check", Status: doctor.StatusOK, Fix: func() error {
				okCalled = true
				return nil
			}},
			{Name: "warn-check", Status: doctor.StatusWarn, Fix: func() error {
				warnCalled = true
				return nil
			}},
			{Name: "fail-check", Status: doctor.StatusFail, Fix: func() error {
				failCalled = true
				return nil
			}},
		},
	}

	attempted, err := result.ApplyFixes()
	if err != nil {
		t.Errorf("ApplyFixes() unexpected error: %v", err)
	}
	if okCalled {
		t.Error("Fix was called on an OK check; should be skipped")
	}
	if !warnCalled {
		t.Error("Fix was not called on WARN check; should be called")
	}
	if !failCalled {
		t.Error("Fix was not called on FAIL check; should be called")
	}
	if len(attempted) != 2 {
		t.Errorf("attempted = %v, want 2 entries", attempted)
	}
}

// TestApplyFixes_ReturnsFirstError verifies that ApplyFixes calls all Fixes
// even when one errors, and returns only the first error.
func TestApplyFixes_ReturnsFirstError(t *testing.T) {
	err1 := errors.New("first fix error")
	err2 := errors.New("second fix error")
	second := false

	result := doctor.Result{
		Checks: []doctor.Check{
			{Name: "check-a", Status: doctor.StatusFail, Fix: func() error { return err1 }},
			{Name: "check-b", Status: doctor.StatusFail, Fix: func() error {
				second = true
				return err2
			}},
		},
	}

	attempted, firstErr := result.ApplyFixes()
	if firstErr != err1 {
		t.Errorf("firstErr = %v, want %v", firstErr, err1)
	}
	if !second {
		t.Error("second Fix was not called; ApplyFixes should continue after errors")
	}
	if len(attempted) != 2 {
		t.Errorf("attempted = %v, want 2 entries", attempted)
	}
}

// TestPodmanMachineCheck_LinuxSkipsOK verifies that on Linux the podman-machine
// check immediately returns OK with a "skipped" message.
func TestPodmanMachineCheck_LinuxSkipsOK(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("test is only meaningful on Linux")
	}
	cfg := config.DefaultConfig()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	result := doctor.Run(cfg)

	c := findCheck(result, "podman-machine")
	if c == nil {
		t.Fatal("podman-machine check not found in result")
	}
	if c.Status != doctor.StatusOK {
		t.Errorf("podman-machine status = %q, want OK on Linux", c.Status)
	}
	if !strings.Contains(strings.ToLower(c.Message), "skip") {
		t.Errorf("podman-machine message %q should mention 'skip' on Linux", c.Message)
	}
}

// TestKitCacheHealthCheck_EmptyCache verifies that an empty kit cache returns
// OK (no kits built yet).
func TestKitCacheHealthCheck_EmptyCache(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cfg := config.DefaultConfig()
	result := doctor.Run(cfg)

	c := findCheck(result, "kit-cache")
	if c == nil {
		t.Fatal("kit-cache check not found in result")
	}
	if c.Status != doctor.StatusOK {
		t.Errorf("kit-cache status = %q, want OK for empty cache", c.Status)
	}
	if !strings.Contains(c.Message, "empty") {
		t.Errorf("kit-cache message %q should mention 'empty'", c.Message)
	}
}

// TestMountSourcesCheck_NoRunningBoxes verifies that when no agentbox-labeled
// containers exist the check returns OK.
func TestMountSourcesCheck_NoRunningBoxes(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	// Use a runtime bin that won't find any containers (or isn't installed).
	// Either way, mount-sources should return OK — it degrades gracefully.
	cfg := config.DefaultConfig()
	result := doctor.Run(cfg)

	c := findCheck(result, "mount-sources")
	if c == nil {
		t.Fatal("mount-sources check not found in result")
	}
	// On a clean dev machine with no agentbox boxes, should be OK.
	// The check treats runtime-error as "no boxes" (graceful degradation).
	if c.Status == doctor.StatusFail {
		t.Errorf("mount-sources returned FAIL; want OK or WARN on clean machine: %s", c.Message)
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

// ---- Unit 8: registryReachableCheck tests ----

// TestRegistryReachableCheck_Disabled verifies check returns OK when disabled.
func TestRegistryReachableCheck_Disabled(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cfg := config.DefaultConfig()
	cfg.Registry.Enabled = false
	result := doctor.Run(cfg)

	c := findCheck(result, "registry-reachable")
	if c == nil {
		t.Fatal("registry-reachable check not found")
	}
	if c.Status != doctor.StatusOK {
		t.Errorf("status = %q, want OK when registry disabled", c.Status)
	}
	if !strings.Contains(c.Message, "skipping") {
		t.Errorf("message should mention 'skipping', got: %q", c.Message)
	}
}

// TestRegistryReachableCheck_Reachable verifies OK when server returns 200.
func TestRegistryReachableCheck_Reachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Use the test server URL as the probe URL directly.
	c := doctor.DoRegistryProbeForTest("registry-reachable", "ghcr.io/test/kits",
		"ghcr.io/test/kits:latest-polyglot-containers-claude", srv.URL+"/v2/test/kits/manifests/latest-polyglot-containers-claude")
	if c.Status != doctor.StatusOK {
		t.Errorf("status = %q, want OK for 200 response; msg: %s", c.Status, c.Message)
	}
	if !strings.Contains(c.Message, "reachable") {
		t.Errorf("message should mention 'reachable', got: %q", c.Message)
	}
}

// TestRegistryReachableCheck_NotPublished verifies WARN when server returns 404.
func TestRegistryReachableCheck_NotPublished(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := doctor.DoRegistryProbeForTest("registry-reachable", "ghcr.io/test/kits",
		"ghcr.io/test/kits:latest-polyglot-containers-claude", srv.URL+"/v2/test/kits/manifests/latest-polyglot-containers-claude")
	if c.Status != doctor.StatusWarn {
		t.Errorf("status = %q, want WARN for 404 response", c.Status)
	}
	if !strings.Contains(c.Message, "not published yet") {
		t.Errorf("message should mention 'not published yet', got: %q", c.Message)
	}
}

// TestRegistryReachableCheck_AuthRequired verifies WARN + login suggestion for 401.
func TestRegistryReachableCheck_AuthRequired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := doctor.DoRegistryProbeForTest("registry-reachable", "ghcr.io/test/kits",
		"ghcr.io/test/kits:latest-polyglot-containers-claude", srv.URL+"/v2/test/kits/manifests/latest-polyglot-containers-claude")
	if c.Status != doctor.StatusWarn {
		t.Errorf("status = %q, want WARN for 401 response", c.Status)
	}
	if !strings.Contains(c.Message, "podman login") {
		t.Errorf("message should mention 'podman login', got: %q", c.Message)
	}
}

// TestRegistryReachableCheck_Unreachable verifies WARN when host is unreachable.
func TestRegistryReachableCheck_Unreachable(t *testing.T) {
	// Start a server and then close it immediately, so the connection is refused fast.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	addr := srv.URL
	srv.Close() // closed — any connection attempt gets "connection refused"

	c := doctor.DoRegistryProbeForTest("registry-reachable", "ghcr.invalid/test/kits",
		"ghcr.invalid/test/kits:latest-polyglot-containers-claude",
		addr+"/v2/test/kits/manifests/latest-polyglot-containers-claude")
	if c.Status != doctor.StatusWarn {
		t.Errorf("status = %q, want WARN for unreachable host", c.Status)
	}
	if !strings.Contains(strings.ToLower(c.Message), "unreachable") {
		t.Errorf("message should mention 'unreachable', got: %q", c.Message)
	}
}

// TestRegistryReachableCheck_InRun verifies the check appears in doctor.Run results.
func TestRegistryReachableCheck_InRun(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cfg := config.DefaultConfig()
	// Point at an unreachable address to keep the test fast.
	cfg.Registry.Host = "ghcr.io/nklisch/agentbox-kits" // default; check should be present
	cfg.Registry.Enabled = false                          // skip actual network call
	result := doctor.Run(cfg)

	c := findCheck(result, "registry-reachable")
	if c == nil {
		t.Fatal("registry-reachable check not found in doctor.Run output")
	}
}
