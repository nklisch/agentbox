package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nklisch/agentbox/internal/config"
)

// --- Unit 6: DefaultConfig + Validate ---

func TestDefaultConfig_Runtime(t *testing.T) {
	cfg := config.DefaultConfig()
	if cfg.Runtime != "podman" {
		t.Errorf("DefaultConfig().Runtime = %q, want %q", cfg.Runtime, "podman")
	}
}

func TestDefaultConfig_NetworkMode(t *testing.T) {
	cfg := config.DefaultConfig()
	if cfg.Network.Mode != "safe" {
		t.Errorf("DefaultConfig().Network.Mode = %q, want %q", cfg.Network.Mode, "safe")
	}
}

func TestDefaultConfig_ContainersDisabled(t *testing.T) {
	cfg := config.DefaultConfig()
	if cfg.Containers.Enable != false {
		t.Error("DefaultConfig().Containers.Enable should be false")
	}
}

func TestDefaultConfig_ValidatesClean(t *testing.T) {
	cfg := config.DefaultConfig()
	if err := cfg.Validate(); err != nil {
		t.Errorf("DefaultConfig().Validate() = %v, want nil", err)
	}
}

func TestValidate_InvalidRuntime(t *testing.T) {
	cfg := config.Config{Runtime: "rkt", Network: config.Network{Mode: "safe"}}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for invalid runtime, got nil")
	}
	if !strings.Contains(err.Error(), "runtime") {
		t.Errorf("error %q should mention 'runtime'", err.Error())
	}
}

func TestValidate_InvalidNetworkMode(t *testing.T) {
	cfg := config.Config{Runtime: "podman", Network: config.Network{Mode: "wrong"}}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for invalid network mode, got nil")
	}
	if !strings.Contains(err.Error(), "network.mode") {
		t.Errorf("error %q should mention 'network.mode'", err.Error())
	}
}

func TestValidate_AllValidRuntimes(t *testing.T) {
	for _, rt := range []string{"podman", "docker"} {
		cfg := config.Config{Runtime: rt, Network: config.Network{Mode: "safe"}}
		if err := cfg.Validate(); err != nil {
			t.Errorf("Validate() with runtime=%q: %v", rt, err)
		}
	}
}

func TestValidate_AllValidNetworkModes(t *testing.T) {
	for _, mode := range []string{"off", "safe", "allowlist", "open"} {
		cfg := config.Config{Runtime: "podman", Network: config.Network{Mode: mode}}
		if err := cfg.Validate(); err != nil {
			t.Errorf("Validate() with network.mode=%q: %v", mode, err)
		}
	}
}

// --- Unit 7: DefaultPaths + Load ---

func TestDefaultPaths_GlobalContainsAgentbox(t *testing.T) {
	paths, err := config.DefaultPaths()
	if err != nil {
		t.Fatalf("DefaultPaths() error: %v", err)
	}
	if !strings.Contains(paths.Global, "agentbox") || !strings.HasSuffix(paths.Global, "config.toml") {
		t.Errorf("Global path %q should contain agentbox/config.toml", paths.Global)
	}
}

func TestDefaultPaths_ProjectEndsWithDotAgentbox(t *testing.T) {
	paths, err := config.DefaultPaths()
	if err != nil {
		t.Fatalf("DefaultPaths() error: %v", err)
	}
	if !strings.HasSuffix(paths.Project, ".agentbox.toml") {
		t.Errorf("Project path %q should end with .agentbox.toml", paths.Project)
	}
}

func TestLoad_NonexistentFiles(t *testing.T) {
	tmp := t.TempDir()
	paths := config.Paths{
		Global:  filepath.Join(tmp, "does-not-exist.toml"),
		Project: filepath.Join(tmp, "also-does-not-exist.toml"),
	}
	cfg, err := config.Load(paths)
	if err != nil {
		t.Fatalf("Load() with nonexistent files: %v", err)
	}
	// Should return defaults unchanged.
	def := config.DefaultConfig()
	if cfg.Runtime != def.Runtime {
		t.Errorf("runtime = %q, want %q", cfg.Runtime, def.Runtime)
	}
	if cfg.Network.Mode != def.Network.Mode {
		t.Errorf("network.mode = %q, want %q", cfg.Network.Mode, def.Network.Mode)
	}
}

func TestLoad_GlobalOverridesDefault(t *testing.T) {
	tmp := t.TempDir()
	globalPath := filepath.Join(tmp, "config.toml")
	if err := os.WriteFile(globalPath, []byte(`runtime = "docker"`), 0o600); err != nil {
		t.Fatal(err)
	}
	paths := config.Paths{
		Global:  globalPath,
		Project: filepath.Join(tmp, "nonexistent.toml"),
	}
	cfg, err := config.Load(paths)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Runtime != "docker" {
		t.Errorf("runtime = %q, want %q", cfg.Runtime, "docker")
	}
	// Other defaults should be preserved.
	if cfg.Network.Mode != "safe" {
		t.Errorf("network.mode = %q, want %q (default preserved)", cfg.Network.Mode, "safe")
	}
}

func TestLoad_ProjectOverridesGlobal(t *testing.T) {
	tmp := t.TempDir()
	globalPath := filepath.Join(tmp, "config.toml")
	projectPath := filepath.Join(tmp, ".agentbox.toml")

	if err := os.WriteFile(globalPath, []byte(`runtime = "docker"`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectPath, []byte(`runtime = "podman"`), 0o600); err != nil {
		t.Fatal(err)
	}
	paths := config.Paths{
		Global:  globalPath,
		Project: projectPath,
	}
	cfg, err := config.Load(paths)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Runtime != "podman" {
		t.Errorf("runtime = %q, want project value %q", cfg.Runtime, "podman")
	}
}

func TestLoad_MalformedFile(t *testing.T) {
	tmp := t.TempDir()
	globalPath := filepath.Join(tmp, "bad.toml")
	if err := os.WriteFile(globalPath, []byte(`this is not valid toml %%%`), 0o600); err != nil {
		t.Fatal(err)
	}
	paths := config.Paths{
		Global:  globalPath,
		Project: "",
	}
	_, err := config.Load(paths)
	if err == nil {
		t.Fatal("expected error for malformed TOML, got nil")
	}
	// Error should reference the path and parse context.
	if !strings.Contains(err.Error(), "global config") {
		t.Errorf("error %q should mention 'global config'", err.Error())
	}
}

func TestLoad_ProjectMalformedFile(t *testing.T) {
	tmp := t.TempDir()
	projectPath := filepath.Join(tmp, ".agentbox.toml")
	if err := os.WriteFile(projectPath, []byte(`[invalid`), 0o600); err != nil {
		t.Fatal(err)
	}
	paths := config.Paths{
		Global:  filepath.Join(tmp, "nonexistent.toml"),
		Project: projectPath,
	}
	_, err := config.Load(paths)
	if err == nil {
		t.Fatal("expected error for malformed project TOML, got nil")
	}
	if !strings.Contains(err.Error(), "project config") {
		t.Errorf("error %q should mention 'project config'", err.Error())
	}
}

func TestLoad_EmptyProjectPath(t *testing.T) {
	tmp := t.TempDir()
	paths := config.Paths{
		Global:  filepath.Join(tmp, "nonexistent.toml"),
		Project: "", // empty means skip
	}
	cfg, err := config.Load(paths)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Runtime != "podman" {
		t.Errorf("runtime = %q, want default %q", cfg.Runtime, "podman")
	}
}
