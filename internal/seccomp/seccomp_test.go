package seccomp_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/nklisch/agentbox/internal/seccomp"
)

func TestContainersJSONBytes_ValidJSON(t *testing.T) {
	var v map[string]interface{}
	if err := json.Unmarshal(seccomp.ContainersJSONBytes(), &v); err != nil {
		t.Fatalf("containers.json is not valid JSON: %v", err)
	}
	if _, ok := v["defaultAction"]; !ok {
		t.Error("seccomp profile missing 'defaultAction' field")
	}
}

func TestContainersJSONBytes_AddsClone3(t *testing.T) {
	s := string(seccomp.ContainersJSONBytes())
	if !strings.Contains(s, "clone3") {
		t.Error("seccomp profile does not mention clone3 (expected in allow list)")
	}
}

func TestEnsureContainersProfile(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	path, err := seccomp.EnsureContainersProfile()
	if err != nil {
		t.Fatalf("EnsureContainersProfile: %v", err)
	}
	if !strings.HasSuffix(path, "/seccomp/containers.json") {
		t.Errorf("unexpected path: %s", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file not created: %v", err)
	}
	// Second call should succeed too (idempotent).
	if _, err := seccomp.EnsureContainersProfile(); err != nil {
		t.Errorf("second EnsureContainersProfile: %v", err)
	}
}

func TestEnsureContainersProfile_OverwritesCorrupted(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	path, _ := seccomp.EnsureContainersProfile()
	// Corrupt.
	_ = os.WriteFile(path, []byte("garbage"), 0o644)
	// Re-call should overwrite.
	if _, err := seccomp.EnsureContainersProfile(); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	if string(body) == "garbage" {
		t.Error("EnsureContainersProfile did not overwrite corrupted file")
	}
}
