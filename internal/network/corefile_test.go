package network_test

import (
	"strings"
	"testing"

	"github.com/nklisch/agentbox/internal/config"
	"github.com/nklisch/agentbox/internal/network"
)

// makeSpec is a helper to build a Spec with the given mode and config.
func makeSpec(mode network.Mode, cfg config.Config) network.Spec {
	return network.Spec{
		ProjectID:   "abc123456789",
		Mode:        mode,
		NetworkName: "agentbox-net-abc123456789",
		SidecarName: "agentbox-coredns-abc123456789",
		SidecarIP:   "10.89.171.2",
		Subnet:      "10.89.171.0/24",
		Cfg:         cfg,
	}
}

func TestGenerateCorefile_SafeMode_Quad9(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Network.Safe.Upstream = "quad9"
	spec := makeSpec(network.ModeSafe, cfg)

	got := network.GenerateCorefile(spec)

	if !strings.Contains(got, "forward . 9.9.9.9 149.112.112.112") {
		t.Errorf("safe/quad9: expected quad9 forward line, got:\n%s", got)
	}
	if !strings.Contains(got, "policy random") {
		t.Errorf("safe/quad9: expected 'policy random', got:\n%s", got)
	}
	if !strings.Contains(got, "cache 300") {
		t.Errorf("safe/quad9: expected 'cache 300', got:\n%s", got)
	}
	if !strings.Contains(got, "log .") {
		t.Errorf("safe/quad9: expected 'log .' block, got:\n%s", got)
	}
	if !strings.Contains(got, "class all") {
		t.Errorf("safe/quad9: expected 'class all', got:\n%s", got)
	}
	if !strings.Contains(got, "errors") {
		t.Errorf("safe/quad9: expected 'errors', got:\n%s", got)
	}
	// Should start with the default zone block
	if !strings.Contains(got, ". {") {
		t.Errorf("safe/quad9: expected default zone '. {', got:\n%s", got)
	}
}

func TestGenerateCorefile_SafeMode_CloudflareSecurity(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Network.Safe.Upstream = "cloudflare-security"
	spec := makeSpec(network.ModeSafe, cfg)

	got := network.GenerateCorefile(spec)

	if !strings.Contains(got, "forward . 1.1.1.2 1.0.0.2") {
		t.Errorf("safe/cloudflare-security: expected cloudflare forward, got:\n%s", got)
	}
}

func TestGenerateCorefile_SafeMode_Custom(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Network.Safe.Upstream = "custom"
	cfg.Network.Safe.UpstreamServers = []string{"1.2.3.4", "5.6.7.8"}
	spec := makeSpec(network.ModeSafe, cfg)

	got := network.GenerateCorefile(spec)

	if !strings.Contains(got, "forward . 1.2.3.4 5.6.7.8") {
		t.Errorf("safe/custom: expected custom forward, got:\n%s", got)
	}
}

func TestGenerateCorefile_SafeMode_CustomNoServers(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Network.Safe.Upstream = "custom"
	cfg.Network.Safe.UpstreamServers = nil
	spec := makeSpec(network.ModeSafe, cfg)

	got := network.GenerateCorefile(spec)

	// Should still produce a corefile (with no forward line)
	if !strings.Contains(got, ". {") {
		t.Errorf("safe/custom-no-servers: expected default zone block, got:\n%s", got)
	}
	if strings.Contains(got, "forward") {
		t.Errorf("safe/custom-no-servers: expected no forward line, got:\n%s", got)
	}
}

func TestGenerateCorefile_SafeMode_NextDNS(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Network.Safe.Upstream = "nextdns"
	cfg.Network.Safe.NextDNSID = "abc123"
	spec := makeSpec(network.ModeSafe, cfg)

	got := network.GenerateCorefile(spec)

	if !strings.Contains(got, "forward .") {
		t.Errorf("safe/nextdns: expected forward line, got:\n%s", got)
	}
}

func TestGenerateCorefile_SafeMode_NextDNS_NoID(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Network.Safe.Upstream = "nextdns"
	cfg.Network.Safe.NextDNSID = "" // no ID → no forward line
	spec := makeSpec(network.ModeSafe, cfg)

	got := network.GenerateCorefile(spec)

	if !strings.Contains(got, ". {") {
		t.Errorf("safe/nextdns-no-id: expected default zone, got:\n%s", got)
	}
}

func TestGenerateCorefile_SafeMode_ExtraBlock(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Network.Safe.Upstream = "quad9"
	cfg.Network.Safe.ExtraBlock = []string{"evil.example.com", "bad.net"}
	spec := makeSpec(network.ModeSafe, cfg)

	got := network.GenerateCorefile(spec)

	// Both extra_block domains should appear before the default zone
	if !strings.Contains(got, "evil.example.com {") {
		t.Errorf("safe/extra-block: expected 'evil.example.com {' block, got:\n%s", got)
	}
	if !strings.Contains(got, "bad.net {") {
		t.Errorf("safe/extra-block: expected 'bad.net {' block, got:\n%s", got)
	}

	// Template NXDOMAIN in each block
	evilIdx := strings.Index(got, "evil.example.com {")
	dotIdx := strings.Index(got, ". {")
	if evilIdx < 0 || dotIdx < 0 {
		t.Fatal("safe/extra-block: missing required blocks")
	}
	if evilIdx > dotIdx {
		t.Errorf("safe/extra-block: extra_block zone should appear BEFORE default '.' zone")
	}

	// Each block should have template NXDOMAIN
	if count := strings.Count(got, "rcode NXDOMAIN"); count < 2 {
		t.Errorf("safe/extra-block: expected at least 2 'rcode NXDOMAIN' entries, got %d in:\n%s", count, got)
	}
}

func TestGenerateCorefile_Allowlist_Basic(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Network.Allowlist.Allow = []string{"github.com", "api.anthropic.com"}
	spec := makeSpec(network.ModeAllowlist, cfg)

	got := network.GenerateCorefile(spec)

	// Should have the allowlist zone with forward
	if !strings.Contains(got, "github.com") {
		t.Errorf("allowlist: expected 'github.com' in output, got:\n%s", got)
	}
	if !strings.Contains(got, "api.anthropic.com") {
		t.Errorf("allowlist: expected 'api.anthropic.com' in output, got:\n%s", got)
	}
	if !strings.Contains(got, "forward . 1.1.1.1 8.8.8.8") {
		t.Errorf("allowlist: expected public forward, got:\n%s", got)
	}

	// Default zone should be NXDOMAIN
	if !strings.Contains(got, "template IN ANY") {
		t.Errorf("allowlist: expected 'template IN ANY' in default zone, got:\n%s", got)
	}
	if !strings.Contains(got, "rcode NXDOMAIN") {
		t.Errorf("allowlist: expected 'rcode NXDOMAIN' in default zone, got:\n%s", got)
	}
}

func TestGenerateCorefile_Allowlist_Empty(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Network.Allowlist.Allow = nil
	spec := makeSpec(network.ModeAllowlist, cfg)

	got := network.GenerateCorefile(spec)

	// Should still have default deny zone
	if !strings.Contains(got, ". {") {
		t.Errorf("allowlist/empty: expected default zone, got:\n%s", got)
	}
	if !strings.Contains(got, "rcode NXDOMAIN") {
		t.Errorf("allowlist/empty: expected NXDOMAIN in default zone, got:\n%s", got)
	}
}

func TestGenerateCorefile_Off_ReturnsEmpty(t *testing.T) {
	cfg := config.DefaultConfig()
	spec := makeSpec(network.ModeOff, cfg)

	got := network.GenerateCorefile(spec)
	if got != "" {
		t.Errorf("mode=off: expected empty string, got %q", got)
	}
}

func TestGenerateCorefile_Open_ReturnsEmpty(t *testing.T) {
	cfg := config.DefaultConfig()
	spec := makeSpec(network.ModeOpen, cfg)

	got := network.GenerateCorefile(spec)
	if got != "" {
		t.Errorf("mode=open: expected empty string, got %q", got)
	}
}

func TestGenerateCorefile_Deterministic(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Network.Safe.Upstream = "quad9"
	cfg.Network.Safe.ExtraBlock = []string{"evil.com", "bad.net"}
	spec := makeSpec(network.ModeSafe, cfg)

	got1 := network.GenerateCorefile(spec)
	got2 := network.GenerateCorefile(spec)
	if got1 != got2 {
		t.Errorf("GenerateCorefile not deterministic:\n--- first ---\n%s\n--- second ---\n%s", got1, got2)
	}
}

func TestGenerateCorefile_Allowlist_LogAll(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Network.Allowlist.Allow = []string{"github.com"}
	spec := makeSpec(network.ModeAllowlist, cfg)

	got := network.GenerateCorefile(spec)

	// Both zones (allowlist and default) should log all queries
	if count := strings.Count(got, "class all"); count < 2 {
		t.Errorf("allowlist: expected 'class all' in both zones (got %d), in:\n%s", count, got)
	}
}
