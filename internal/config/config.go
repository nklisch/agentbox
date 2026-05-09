package config

import (
	"fmt"
	"time"
)

// Config is the merged agentbox configuration. Fields with zero values are
// treated as "unset" by toml.Decode and left at their default if the file
// doesn't mention them.
type Config struct {
	Runtime      string           `toml:"runtime" json:"runtime"`
	DefaultAgent string           `toml:"default_agent" json:"default_agent"`
	DefaultKits  []string         `toml:"default_kits" json:"default_kits"`
	Network      Network          `toml:"network" json:"network"`
	Mounts       Mounts           `toml:"mounts" json:"mounts"`
	Secrets      Secrets          `toml:"secrets" json:"secrets"`
	Resources    Resources        `toml:"resources" json:"resources"`
	Containers   Containers       `toml:"containers" json:"containers"`
	Shell        Shell            `toml:"shell" json:"shell"`
	Zellij       Zellij           `toml:"zellij" json:"zellij"`
	Registry     Registry         `toml:"registry" json:"registry"`
	Agents       map[string]Agent `toml:"agents" json:"agents"`
}

// Registry holds the kit-image registry configuration. When Enabled and the
// resolved kit list is composed entirely of built-in kits, agentbox tries to
// pull the pre-built image from Host before building locally.
type Registry struct {
	Enabled     bool   `toml:"enabled" json:"enabled"`
	Host        string `toml:"host" json:"host"`                 // e.g. "ghcr.io/nklisch/agentbox-kits"
	Verify      string `toml:"verify" json:"verify"`             // "none" (v0.3+) | "cosign" (deferred)
	PullTimeout string `toml:"pull_timeout" json:"pull_timeout"` // Go duration string, e.g. "5m"
}

type Network struct {
	Mode      string       `toml:"mode" json:"mode"`
	Safe      NetworkSafe  `toml:"safe" json:"safe"`
	Allowlist NetworkAllow `toml:"allowlist" json:"allowlist"`
}

type NetworkSafe struct {
	Upstream        string   `toml:"upstream" json:"upstream"`
	UpstreamServers []string `toml:"upstream_servers" json:"upstream_servers"`
	NextDNSID       string   `toml:"nextdns_id" json:"nextdns_id"`
	BlockCategories []string `toml:"block_categories" json:"block_categories"`
	BlockDirectIP   bool     `toml:"block_direct_ip" json:"block_direct_ip"`
	ExtraBlock      []string `toml:"extra_block" json:"extra_block"`
	ExtraAllow      []string `toml:"extra_allow" json:"extra_allow"`
}

type NetworkAllow struct {
	Allow []string `toml:"allow" json:"allow"`
}

type Mounts struct {
	Gitconfig    bool              `toml:"gitconfig" json:"gitconfig"`
	SSHReadonly  bool              `toml:"ssh_readonly" json:"ssh_readonly"`
	Extra        []string          `toml:"extra" json:"extra"`
	AgentConfigs map[string]string `toml:"agent_configs" json:"agent_configs"`
}

type Secrets struct {
	Passthrough []string `toml:"passthrough" json:"passthrough"`
}

type Resources struct {
	CPUs   int    `toml:"cpus" json:"cpus"`
	Memory string `toml:"memory" json:"memory"`
	PIDs   int    `toml:"pids" json:"pids"`
}

// Containers replaces SPEC.md's [runtime.containers] (invalid TOML — key
// collision with top-level `runtime`).
type Containers struct {
	Enable       bool     `toml:"enable" json:"enable"`
	ExtraDevices []string `toml:"extra_devices" json:"extra_devices"`
	ExtraCaps    []string `toml:"extra_caps" json:"extra_caps"`
	Seccomp      string   `toml:"seccomp" json:"seccomp"`
}

type Shell struct {
	Shell   string            `toml:"shell" json:"shell"`
	Prompt  string            `toml:"prompt" json:"prompt"`
	History bool              `toml:"history" json:"history"`
	Aliases map[string]string `toml:"aliases" json:"aliases"`
}

// Zellij holds the zellij-related run-time options.
type Zellij struct {
	// Layout names a built-in ("focus", "reviewer", "auditor") or a
	// user-defined layout file at ~/.config/agentbox/layouts/<name>.kdl.
	// Empty falls back to "focus" at resolution time.
	Layout string `toml:"layout" json:"layout"`
}

type Agent struct {
	Kits []string `toml:"kits" json:"kits"`
	Cmd  []string `toml:"cmd" json:"cmd"`
}

// DefaultConfig returns the config with all SPEC.md defaults applied.
func DefaultConfig() Config {
	return Config{
		Runtime:      "podman",
		DefaultAgent: "claude",
		DefaultKits: []string{"polyglot", "containers", "claude"},
		Network: Network{
			Mode: "safe",
			Safe: NetworkSafe{
				Upstream:      "quad9",
				BlockDirectIP: true,
			},
			Allowlist: NetworkAllow{
				// Baseline that lets the three default agents (claude, codex,
				// opencode) actually function: provider APIs + the package
				// registries / GitHub endpoints that agent tooling reaches for.
				// Users can trim or extend via `agentbox config set`.
				Allow: []string{
					// Anthropic / Claude Code
					"api.anthropic.com",
					"mcp-proxy.anthropic.com",
					// OpenAI / Codex
					"api.openai.com",
					// Package registries
					"registry.npmjs.org",
					"pypi.org",
					"files.pythonhosted.org",
					"proxy.golang.org",
					"sum.golang.org",
					// GitHub (plugin sources, clones, release artifacts)
					"github.com",
					"api.github.com",
					"raw.githubusercontent.com",
					"objects.githubusercontent.com",
					"codeload.github.com",
				},
			},
		},
		Mounts: Mounts{
			Gitconfig:   true,
			SSHReadonly: true,
			AgentConfigs: map[string]string{
				"claude":   "~/.claude",
				"codex":    "~/.codex",
				"opencode": "~/.opencode",
			},
		},
		Secrets: Secrets{
			Passthrough: []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY"},
		},
		Resources: Resources{
			CPUs:   4,
			Memory: "8g",
			PIDs:   512,
		},
		Containers: Containers{
			Enable:       true,
			ExtraDevices: []string{"/dev/fuse"},
			ExtraCaps:    []string{"SETUID", "SETGID"},
			Seccomp:      "containers",
		},
		Shell: Shell{
			Shell:   "zsh",
			Prompt:  "starship",
			History: true,
		},
		Zellij: Zellij{
			Layout: "focus",
		},
		Registry: Registry{
			Enabled:     true,
			Host:        "ghcr.io/nklisch/agentbox-kits",
			Verify:      "none",
			PullTimeout: "5m",
		},
		Agents: map[string]Agent{
			"claude": {
				Kits: []string{"polyglot", "claude"},
				// YOLO flag verified 2026-05-05 against @anthropic-ai/claude-code@2.1.128.
				Cmd: []string{"claude", "--dangerously-skip-permissions"},
			},
			"codex": {
				Kits: []string{"polyglot", "codex"},
				// YOLO flag verified 2026-05-05 against @openai/codex@0.128.0.
				Cmd: []string{"codex", "--dangerously-bypass-approvals-and-sandbox"},
			},
			"opencode": {
				Kits: []string{"polyglot", "opencode"},
				// opencode v1.14.37: --dangerously-skip-permissions is on the `run` subcommand
				// only; the TUI (bare `opencode`) has no equivalent flag. Use bare command for
				// interactive in-box use. Non-interactive automation can use `opencode run --dangerously-skip-permissions`.
				Cmd: []string{"opencode"},
			},
		},
	}
}

// Validate checks that string-enum fields hold permitted values. Returns
// an error formatted for end users.
func (c Config) Validate() error {
	switch c.Runtime {
	case "podman", "docker":
	default:
		return fmt.Errorf("runtime: must be 'podman' or 'docker', got %q", c.Runtime)
	}
	switch c.Network.Mode {
	case "off", "safe", "allowlist", "open":
	default:
		return fmt.Errorf("network.mode: must be off|safe|allowlist|open, got %q", c.Network.Mode)
	}
	switch c.Registry.Verify {
	case "", "none":
	case "cosign":
		return fmt.Errorf("registry.verify: %q is reserved for a future release", c.Registry.Verify)
	default:
		return fmt.Errorf("registry.verify: must be 'none', got %q", c.Registry.Verify)
	}
	if c.Registry.Enabled && c.Registry.Host == "" {
		return fmt.Errorf("registry.enabled=true requires registry.host to be set")
	}
	if c.Registry.PullTimeout != "" {
		if _, err := time.ParseDuration(c.Registry.PullTimeout); err != nil {
			return fmt.Errorf("registry.pull_timeout: %w", err)
		}
	}
	return nil
}
