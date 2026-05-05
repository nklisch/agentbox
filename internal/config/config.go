package config

import "fmt"

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
	Agents       map[string]Agent `toml:"agents" json:"agents"`
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

type Agent struct {
	Kits []string `toml:"kits" json:"kits"`
	Cmd  []string `toml:"cmd" json:"cmd"`
}

// DefaultConfig returns the config with all SPEC.md defaults applied.
func DefaultConfig() Config {
	return Config{
		Runtime:      "podman",
		DefaultAgent: "claude",
		DefaultKits:  []string{"polyglot", "containers", "claude"},
		Network: Network{
			Mode: "safe",
			Safe: NetworkSafe{
				Upstream:      "quad9",
				BlockDirectIP: true,
			},
			Allowlist: NetworkAllow{
				Allow: []string{
					"registry.npmjs.org",
					"pypi.org",
					"github.com",
					"api.anthropic.com",
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
			Enable:       false,
			ExtraDevices: []string{"/dev/fuse"},
			ExtraCaps:    []string{"SETUID", "SETGID"},
			Seccomp:      "containers",
		},
		Shell: Shell{
			Shell:   "zsh",
			Prompt:  "starship",
			History: true,
		},
		Agents: map[string]Agent{
			"claude": {
				Kits: []string{"polyglot", "claude"},
				Cmd:  []string{"claude", "--dangerously-skip-permissions"},
			},
			"codex": {
				Kits: []string{"polyglot", "codex"},
				Cmd:  []string{"codex"},
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
	return nil
}
