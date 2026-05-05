package config

import "testing"

func TestLookupKind(t *testing.T) {
	cases := []struct {
		path []string
		want Kind
	}{
		// Top-level scalars
		{[]string{"runtime"}, KindString},
		{[]string{"default_agent"}, KindString},

		// Top-level list
		{[]string{"default_kits"}, KindStringList},

		// Nested scalars
		{[]string{"network", "mode"}, KindString},
		{[]string{"network", "safe", "upstream"}, KindString},
		{[]string{"network", "safe", "block_direct_ip"}, KindBool},
		{[]string{"network", "safe", "upstream_servers"}, KindStringList},
		{[]string{"network", "safe", "extra_block"}, KindStringList},
		{[]string{"network", "safe", "extra_allow"}, KindStringList},
		{[]string{"network", "allowlist", "allow"}, KindStringList},

		// Mounts
		{[]string{"mounts", "gitconfig"}, KindBool},
		{[]string{"mounts", "ssh_readonly"}, KindBool},
		{[]string{"mounts", "extra"}, KindStringList},

		// Free-form map key under mounts.agent_configs (map[string]string)
		{[]string{"mounts", "agent_configs", "claude"}, KindString},
		{[]string{"mounts", "agent_configs", "any_agent"}, KindString},

		// Secrets
		{[]string{"secrets", "passthrough"}, KindStringList},

		// Resources
		{[]string{"resources", "cpus"}, KindInt},
		{[]string{"resources", "memory"}, KindString},
		{[]string{"resources", "pids"}, KindInt},

		// Containers
		{[]string{"containers", "enable"}, KindBool},
		{[]string{"containers", "seccomp"}, KindString},
		{[]string{"containers", "extra_devices"}, KindStringList},
		{[]string{"containers", "extra_caps"}, KindStringList},

		// Shell
		{[]string{"shell", "shell"}, KindString},
		{[]string{"shell", "prompt"}, KindString},
		{[]string{"shell", "history"}, KindBool},

		// Agents: map[string]Agent — free-form agent name, typed Agent fields
		{[]string{"agents", "claude", "kits"}, KindStringList},
		{[]string{"agents", "claude", "cmd"}, KindStringList},
		{[]string{"agents", "codex", "kits"}, KindStringList},
		{[]string{"agents", "future-agent", "cmd"}, KindStringList},

		// Unknown / invalid paths
		{[]string{}, KindUnknown},
		{[]string{"nonexistent"}, KindUnknown},
		{[]string{"runtime", "too", "deep"}, KindUnknown},
	}

	for _, c := range cases {
		got := LookupKind(c.path)
		if got != c.want {
			t.Errorf("LookupKind(%v) = %v, want %v", c.path, got, c.want)
		}
	}
}
