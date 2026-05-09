package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestDefaultConfig_ContainersEnabled(t *testing.T) {
	cfg := config.DefaultConfig()
	if cfg.Containers.Enable != true {
		t.Error("DefaultConfig().Containers.Enable should be true")
	}
}

func TestDefaultConfig_AgentsHasOpencode(t *testing.T) {
	cfg := config.DefaultConfig()
	agent, ok := cfg.Agents["opencode"]
	if !ok {
		t.Fatal("DefaultConfig().Agents missing 'opencode' entry")
	}
	if len(agent.Kits) == 0 {
		t.Error("DefaultConfig().Agents['opencode'].Kits should not be empty")
	}
	if len(agent.Cmd) == 0 {
		t.Error("DefaultConfig().Agents['opencode'].Cmd should not be empty")
	}
	if agent.Cmd[0] != "opencode" {
		t.Errorf("DefaultConfig().Agents['opencode'].Cmd[0] = %q, want %q", agent.Cmd[0], "opencode")
	}
}

func TestDefaultConfig_AgentsHasClaude(t *testing.T) {
	cfg := config.DefaultConfig()
	agent, ok := cfg.Agents["claude"]
	if !ok {
		t.Fatal("DefaultConfig().Agents missing 'claude' entry")
	}
	if len(agent.Cmd) < 2 || agent.Cmd[1] != "--dangerously-skip-permissions" {
		t.Errorf("DefaultConfig().Agents['claude'].Cmd = %v, want second element --dangerously-skip-permissions", agent.Cmd)
	}
}

func TestDefaultConfig_AgentsHasCodex(t *testing.T) {
	cfg := config.DefaultConfig()
	agent, ok := cfg.Agents["codex"]
	if !ok {
		t.Fatal("DefaultConfig().Agents missing 'codex' entry")
	}
	if len(agent.Cmd) < 2 || agent.Cmd[1] != "--dangerously-bypass-approvals-and-sandbox" {
		t.Errorf("DefaultConfig().Agents['codex'].Cmd = %v, want second element --dangerously-bypass-approvals-and-sandbox", agent.Cmd)
	}
}

func TestDefaultConfig_DefaultKitsIncludesContainers(t *testing.T) {
	cfg := config.DefaultConfig()
	found := false
	for _, k := range cfg.DefaultKits {
		if k == "containers" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("DefaultConfig().DefaultKits = %v, want 'containers' to be present (Phase 7)", cfg.DefaultKits)
	}
}

func TestDefaultConfig_DefaultKitsContainsClaude(t *testing.T) {
	cfg := config.DefaultConfig()
	found := false
	for _, k := range cfg.DefaultKits {
		if k == "claude" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("DefaultConfig().DefaultKits = %v, want 'claude' to be present", cfg.DefaultKits)
	}
}

func TestDefaultConfig_AllowlistCoversDefaultAgents(t *testing.T) {
	cfg := config.DefaultConfig()
	// Endpoints the three default agents (claude/codex/opencode) cannot
	// function without when network.mode = "allowlist". If the list silently
	// shrinks below this floor, agentbox's primary use case breaks.
	required := []string{
		"api.anthropic.com",        // Claude API — Claude Code can't issue completions without it
		"mcp-proxy.anthropic.com",  // managed MCP servers
		"api.openai.com",           // Codex API
		"registry.npmjs.org",       // npm-installed plugins
		"github.com",               // plugin sources, clones
		"api.github.com",           // plugin marketplace metadata
		"raw.githubusercontent.com", // plugin file fetches
	}
	have := make(map[string]bool, len(cfg.Network.Allowlist.Allow))
	for _, h := range cfg.Network.Allowlist.Allow {
		have[h] = true
	}
	for _, h := range required {
		if !have[h] {
			t.Errorf("DefaultConfig().Network.Allowlist.Allow missing %q (required for default agents)", h)
		}
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

// --- registry.refresh: ParseRefresh + Validate ---

func TestParseRefresh_Off(t *testing.T) {
	for _, s := range []string{"", "off"} {
		got, err := config.ParseRefresh(s)
		if err != nil {
			t.Errorf("ParseRefresh(%q) unexpected err: %v", s, err)
		}
		if got.Enabled {
			t.Errorf("ParseRefresh(%q): expected Enabled=false, got %+v", s, got)
		}
	}
}

func TestParseRefresh_Always(t *testing.T) {
	got, err := config.ParseRefresh("always")
	if err != nil {
		t.Fatalf("ParseRefresh(always) err: %v", err)
	}
	if !got.Enabled || !got.Always {
		t.Errorf("ParseRefresh(always): want Enabled+Always, got %+v", got)
	}
}

func TestParseRefresh_Duration(t *testing.T) {
	got, err := config.ParseRefresh("24h")
	if err != nil {
		t.Fatalf("ParseRefresh(24h) err: %v", err)
	}
	if !got.Enabled || got.Always || got.MaxAge != 24*time.Hour {
		t.Errorf("ParseRefresh(24h): want Enabled+24h, got %+v", got)
	}
}

func TestParseRefresh_Invalid(t *testing.T) {
	for _, s := range []string{"banana", "1xyz", "24"} { // "24" alone fails ParseDuration too
		if _, err := config.ParseRefresh(s); err == nil {
			t.Errorf("ParseRefresh(%q): expected error, got nil", s)
		}
	}
}

func TestParseRefresh_NegativeRejected(t *testing.T) {
	if _, err := config.ParseRefresh("-1h"); err == nil {
		t.Errorf("ParseRefresh(-1h): expected error, got nil")
	}
}

func TestValidate_RegistryRefresh(t *testing.T) {
	good := []string{"", "off", "always", "1h", "24h", "168h"}
	for _, s := range good {
		cfg := config.DefaultConfig()
		cfg.Registry.Refresh = s
		if err := cfg.Validate(); err != nil {
			t.Errorf("Validate() with registry.refresh=%q: %v", s, err)
		}
	}
	bad := []string{"banana", "-1h"}
	for _, s := range bad {
		cfg := config.DefaultConfig()
		cfg.Registry.Refresh = s
		if err := cfg.Validate(); err == nil {
			t.Errorf("Validate() with registry.refresh=%q: expected error, got nil", s)
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

// --- Unit 1: [zellij] config schema ---

func TestDefaultConfig_ZellijLayoutIsFocus(t *testing.T) {
	cfg := config.DefaultConfig()
	if cfg.Zellij.Layout != "focus" {
		t.Errorf("DefaultConfig().Zellij.Layout = %q, want %q", cfg.Zellij.Layout, "focus")
	}
}

func TestLoad_ZellijLayoutFromTOML(t *testing.T) {
	tmp := t.TempDir()
	globalPath := filepath.Join(tmp, "config.toml")
	if err := os.WriteFile(globalPath, []byte("[zellij]\nlayout = \"reviewer\""), 0o600); err != nil {
		t.Fatal(err)
	}
	paths := config.Paths{Global: globalPath, Project: ""}
	cfg, err := config.Load(paths)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Zellij.Layout != "reviewer" {
		t.Errorf("Zellij.Layout = %q, want %q", cfg.Zellij.Layout, "reviewer")
	}
}

func TestLoad_ZellijProjectOverridesGlobal(t *testing.T) {
	tmp := t.TempDir()
	globalPath := filepath.Join(tmp, "config.toml")
	projectPath := filepath.Join(tmp, ".agentbox.toml")
	if err := os.WriteFile(globalPath, []byte("[zellij]\nlayout = \"reviewer\""), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectPath, []byte("[zellij]\nlayout = \"auditor\""), 0o600); err != nil {
		t.Fatal(err)
	}
	paths := config.Paths{Global: globalPath, Project: projectPath}
	cfg, err := config.Load(paths)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Zellij.Layout != "auditor" {
		t.Errorf("Zellij.Layout = %q, want project value %q", cfg.Zellij.Layout, "auditor")
	}
}
