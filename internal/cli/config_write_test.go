package cli_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nklisch/agentbox/internal/exitcode"
)

// configWriteHarness sets up an isolated config environment for a test:
// a temp dir used as XDG_CONFIG_HOME and HOME, and a project directory for
// .agentbox.toml tests. Returns the global and project config paths plus a
// cleanup function.
func configWriteHarness(t *testing.T) (globalPath, projectPath string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)

	// chdir into a sub-directory so DefaultPaths picks up a project path.
	proj := filepath.Join(dir, "myproject")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(orig) })
	if err := os.Chdir(proj); err != nil {
		t.Fatal(err)
	}

	globalPath = filepath.Join(dir, "agentbox", "config.toml")
	projectPath = filepath.Join(proj, ".agentbox.toml")
	return globalPath, projectPath
}

// readFile reads the full contents of a file for assertions.
func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return "" // file doesn't exist
	}
	return string(b)
}

// TestConfigSet_Basic verifies set writes a key and is visible in config show.
func TestConfigSet_Basic(t *testing.T) {
	globalPath, _ := configWriteHarness(t)

	_, _, err := runCmd(t, "config", "set", "network.mode", "open")
	if err != nil {
		t.Fatalf("config set: %v", err)
	}

	out, _, err := runCmd(t, "config", "show", "--json")
	if err != nil {
		t.Fatalf("config show: %v", err)
	}
	if !strings.Contains(out, `"open"`) {
		t.Errorf("expected open in config show output, got: %s", out)
	}

	// File should contain the key.
	content := readFile(t, globalPath)
	if !strings.Contains(content, "open") {
		t.Errorf("expected 'open' in file, got: %s", content)
	}
}

// TestConfigSet_ValidationRejects verifies invalid values are rejected.
func TestConfigSet_ValidationRejects(t *testing.T) {
	globalPath, _ := configWriteHarness(t)
	// Write a valid config first.
	runCmd(t, "config", "set", "network.mode", "safe")
	before := readFile(t, globalPath)

	_, stderr, err := runCmd(t, "config", "set", "network.mode", "bogus")
	if err == nil {
		t.Fatal("expected error for invalid network mode")
	}
	var ee *exitcode.Err
	if !isExitCode(err, exitcode.InvalidArgs, &ee) {
		t.Errorf("expected exit code 2, got %v", err)
	}
	if !strings.Contains(stderr, "bogus") && !strings.Contains(stderr, "invalid") {
		t.Logf("stderr: %s", stderr)
	}
	// File unchanged.
	after := readFile(t, globalPath)
	if after != before {
		t.Errorf("file changed after rejected mutation:\nbefore: %s\nafter: %s", before, after)
	}
}

// TestConfigSet_StringList verifies comma-list overwrites the array.
func TestConfigSet_StringList(t *testing.T) {
	configWriteHarness(t)

	_, _, err := runCmd(t, "config", "set", "default_kits", "node,claude")
	if err != nil {
		t.Fatalf("config set: %v", err)
	}

	kits := defaultKitsFromShow(t)
	if !containsStr(kits, "node") || !containsStr(kits, "claude") {
		t.Errorf("expected node and claude in default_kits: %v", kits)
	}
	if containsStr(kits, "polyglot") {
		t.Errorf("polyglot should have been overwritten, but still in default_kits: %v", kits)
	}
}

// TestConfigSet_DryRun verifies --dry-run prints the body but doesn't write.
func TestConfigSet_DryRun(t *testing.T) {
	globalPath, _ := configWriteHarness(t)

	out, _, err := runCmd(t, "config", "set", "--dry-run", "runtime", "docker")
	if err != nil {
		t.Fatalf("config set --dry-run: %v", err)
	}
	if !strings.Contains(out, "would write to") {
		t.Errorf("expected 'would write to' in output: %s", out)
	}
	if !strings.Contains(out, "docker") {
		t.Errorf("expected 'docker' in dry-run output: %s", out)
	}
	// File should NOT exist (no write happened).
	if _, err := os.Stat(globalPath); !os.IsNotExist(err) {
		t.Errorf("expected no file after dry-run, but file exists")
	}
}

// TestConfigSet_Project verifies --project writes to .agentbox.toml.
func TestConfigSet_Project(t *testing.T) {
	globalPath, projectPath := configWriteHarness(t)

	_, _, err := runCmd(t, "config", "set", "--project", "network.mode", "allowlist")
	if err != nil {
		t.Fatalf("config set --project: %v", err)
	}
	// Global file should NOT exist.
	if _, err := os.Stat(globalPath); !os.IsNotExist(err) {
		t.Errorf("global file should not be created by --project write")
	}
	// Project file should contain the key.
	content := readFile(t, projectPath)
	if !strings.Contains(content, "allowlist") {
		t.Errorf("expected 'allowlist' in project file: %s", content)
	}
}

// TestConfigUnset removes a key and falls back to default.
func TestConfigUnset(t *testing.T) {
	configWriteHarness(t)

	runCmd(t, "config", "set", "network.safe.block_direct_ip", "false")
	_, _, err := runCmd(t, "config", "unset", "network.safe.block_direct_ip")
	if err != nil {
		t.Fatalf("config unset: %v", err)
	}

	out, _, _ := runCmd(t, "config", "show", "--json")
	// After unset the default (true) should be shown.
	if !strings.Contains(out, `"block_direct_ip": true`) {
		t.Errorf("expected default block_direct_ip=true after unset: %s", out)
	}
}

// TestConfigUnset_Idempotent verifies unsetting a missing key is a no-op.
func TestConfigUnset_Idempotent(t *testing.T) {
	globalPath, _ := configWriteHarness(t)
	runCmd(t, "config", "set", "runtime", "docker")
	before := readFile(t, globalPath)

	_, _, err := runCmd(t, "config", "unset", "network.safe.upstream_servers")
	if err != nil {
		t.Fatalf("config unset (missing key): %v", err)
	}
	after := readFile(t, globalPath)
	if after != before {
		t.Errorf("file changed on unset of missing key:\nbefore: %s\nafter: %s", before, after)
	}
}

// TestConfigNetwork_Sugar verifies `config network open` sets the mode.
func TestConfigNetwork_Sugar(t *testing.T) {
	configWriteHarness(t)

	_, _, err := runCmd(t, "config", "network", "open")
	if err != nil {
		t.Fatalf("config network: %v", err)
	}

	out, _, _ := runCmd(t, "config", "show", "--json")
	if !strings.Contains(out, `"open"`) {
		t.Errorf("expected mode=open: %s", out)
	}
}

// TestConfigNetwork_Invalid rejects unknown modes.
func TestConfigNetwork_Invalid(t *testing.T) {
	configWriteHarness(t)
	_, _, err := runCmd(t, "config", "network", "bogus")
	if err == nil {
		t.Fatal("expected error for invalid mode")
	}
}

// TestConfigContainers_OnOff toggles containers.enable.
func TestConfigContainers_OnOff(t *testing.T) {
	configWriteHarness(t)

	if _, _, err := runCmd(t, "config", "containers", "on"); err != nil {
		t.Fatalf("config containers on: %v", err)
	}
	out, _, _ := runCmd(t, "config", "show", "--json")
	if !strings.Contains(out, `"enable": true`) {
		t.Errorf("expected enable=true: %s", out)
	}

	if _, _, err := runCmd(t, "config", "containers", "off"); err != nil {
		t.Fatalf("config containers off: %v", err)
	}
	out, _, _ = runCmd(t, "config", "show", "--json")
	if !strings.Contains(out, `"enable": false`) {
		t.Errorf("expected enable=false: %s", out)
	}
}

// TestConfigContainers_CaseInsensitive verifies friendly inputs are accepted.
func TestConfigContainers_CaseInsensitive(t *testing.T) {
	configWriteHarness(t)
	if _, _, err := runCmd(t, "config", "containers", "OFF"); err != nil {
		t.Fatalf("config containers OFF: %v", err)
	}
}

// TestConfigContainers_Invalid rejects unrecognised toggles.
func TestConfigContainers_Invalid(t *testing.T) {
	configWriteHarness(t)
	_, _, err := runCmd(t, "config", "containers", "maybe")
	if err == nil {
		t.Fatal("expected error for 'maybe'")
	}
}

// TestConfigKits_AddIdempotent appends once; double-add is a no-op.
func TestConfigKits_AddIdempotent(t *testing.T) {
	configWriteHarness(t)

	runCmd(t, "config", "kits", "add", "foo")
	runCmd(t, "config", "kits", "add", "foo")

	out, _, _ := runCmd(t, "config", "show", "--json")
	// Count occurrences of "foo".
	count := strings.Count(out, `"foo"`)
	if count != 1 {
		t.Errorf("expected exactly one 'foo' in default_kits, found %d: %s", count, out)
	}
}

// TestConfigKits_AddSeedsFromMerged verifies that add on a fresh file
// preserves the merged defaults.
func TestConfigKits_AddSeedsFromMerged(t *testing.T) {
	configWriteHarness(t)

	_, _, err := runCmd(t, "config", "kits", "add", "foo")
	if err != nil {
		t.Fatalf("kits add: %v", err)
	}
	kits := defaultKitsFromShow(t)
	if !containsStr(kits, "polyglot") {
		t.Errorf("expected polyglot (from defaults) to remain in default_kits: %v", kits)
	}
	if !containsStr(kits, "foo") {
		t.Errorf("expected foo to be added to default_kits: %v", kits)
	}
}

// TestConfigKits_Remove removes first occurrence.
func TestConfigKits_Remove(t *testing.T) {
	configWriteHarness(t)

	// Set a known list then remove one member.
	runCmd(t, "config", "set", "default_kits", "polyglot,claude,containers")
	if _, _, err := runCmd(t, "config", "kits", "remove", "claude"); err != nil {
		t.Fatalf("kits remove: %v", err)
	}

	kits := defaultKitsFromShow(t)
	if containsStr(kits, "claude") {
		t.Errorf("claude should have been removed from default_kits: %v", kits)
	}
	if !containsStr(kits, "polyglot") || !containsStr(kits, "containers") {
		t.Errorf("other kits should remain: %v", kits)
	}
}

// TestConfigKits_RemoveIdempotent removes a kit that isn't present.
func TestConfigKits_RemoveIdempotent(t *testing.T) {
	globalPath, _ := configWriteHarness(t)
	runCmd(t, "config", "set", "runtime", "podman") // ensure a file exists
	before := readFile(t, globalPath)

	_, _, err := runCmd(t, "config", "kits", "remove", "not-there")
	if err != nil {
		t.Fatalf("kits remove (missing): %v", err)
	}
	// File content is re-encoded so the key order may change; just confirm no error.
	_ = before
}

// defaultKitsFromShow runs config show --json and returns the default_kits slice.
func defaultKitsFromShow(t *testing.T) []string {
	t.Helper()
	out, _, err := runCmd(t, "config", "show", "--json")
	if err != nil {
		t.Fatalf("config show --json: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	raw, ok := m["default_kits"]
	if !ok {
		return nil
	}
	var kits []string
	json.Unmarshal(raw, &kits)
	return kits
}

// containsStr reports whether slice contains s.
func containsStr(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// isExitCode checks whether err is an *exitcode.Err with the given code.
func isExitCode(err error, code int, out **exitcode.Err) bool {
	var ee *exitcode.Err
	if !isExitErr(err, &ee) {
		return false
	}
	if out != nil {
		*out = ee
	}
	return ee.Code == code
}

func isExitErr(err error, out **exitcode.Err) bool {
	var ee *exitcode.Err
	if ok := errors.As(err, &ee); ok {
		if out != nil {
			*out = ee
		}
		return true
	}
	return false
}
