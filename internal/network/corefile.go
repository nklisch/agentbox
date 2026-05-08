package network

import (
	"fmt"
	"strings"

	"github.com/nklisch/agentbox/internal/config"
)

// GenerateCorefile returns the Corefile body for the given spec. Pure string
// output. Caller writes the bytes to the session state dir.
//
// Verified against coredns/coredns:1.14.3 on 2026-05-05:
//   - log plugin: writes to stdout only (no file path argument in 1.14.3).
//     Use "log . { class all }" for all queries; "box net" reads via "podman logs".
//   - template plugin: syntax is "template IN ANY { rcode NXDOMAIN }"
//     (not "template ANY ANY" — qclass and qtype are separate tokens).
//   - forward plugin: "policy random" is still valid in 1.14.3.
//   - The design's "log /var/log/coredns/queries.log { ... }" was incorrect;
//     adjusted to stdout-only logging + podman-logs approach.
func GenerateCorefile(spec Spec) string {
	switch spec.Mode {
	case ModeSafe:
		return safeCorefile(spec.Cfg.Network.Safe)
	case ModeAllowlist:
		return allowlistCorefile(spec.Cfg.Network.Allowlist)
	default:
		return ""
	}
}

func safeCorefile(s config.NetworkSafe) string {
	var b strings.Builder
	fmt.Fprintln(&b, "# agentbox safe-mode Corefile (regenerated each run)")

	// RPZ-style extra_block: respond NXDOMAIN before forwarding.
	// Each domain gets its own server block so CoreDNS intercepts it before
	// the default "." zone forwards it to the upstream.
	for _, dom := range s.ExtraBlock {
		fmt.Fprintf(&b, "%s {\n", dom)
		fmt.Fprintln(&b, "  template IN ANY {")
		fmt.Fprintln(&b, "    rcode NXDOMAIN")
		fmt.Fprintln(&b, "  }")
		fmt.Fprintln(&b, "}")
	}

	// Default zone: forward to upstream + log all queries to stdout.
	fmt.Fprintln(&b, ". {")
	if fwd, ok := upstreamForwardLine(s); ok {
		fmt.Fprintln(&b, fwd)
	}
	fmt.Fprintln(&b, "  cache 300")
	fmt.Fprintln(&b, "  errors")
	// Log all queries (success + NXDOMAIN + SERVFAIL) so "box net" can show history.
	// "class all" is required; "class denial" in CoreDNS covers SERVFAIL/REFUSED only,
	// not NXDOMAIN (verified against 1.14.3).
	fmt.Fprintln(&b, "  log . {")
	fmt.Fprintln(&b, "    class all")
	fmt.Fprintln(&b, "  }")
	fmt.Fprintln(&b, "}")
	return b.String()
}

// upstreamForwardLine returns the forward plugin stanza for the configured upstream.
func upstreamForwardLine(s config.NetworkSafe) (string, bool) {
	switch s.Upstream {
	case "quad9", "":
		// Default: Quad9 — threat-feed-filtered, GDPR-friendly.
		return "  forward . 9.9.9.9 149.112.112.112 {\n    policy random\n  }", true
	case "cloudflare-security":
		return "  forward . 1.1.1.2 1.0.0.2 {\n    policy random\n  }", true
	case "nextdns":
		// NextDNS uses plain DNS (53/UDP) per profile ID, not DoH.
		// Verified 2026-05-05: CoreDNS forward plugin doesn't support DoH URLs directly.
		// NextDNS plain-DNS endpoint: 45.90.28.X (profile-specific).
		// Use the anycast addresses; block_categories config is advisory only
		// when routing through NextDNS — the account profile controls actual blocking.
		if s.NextDNSID == "" {
			return "", false
		}
		// NextDNS recommended plain-DNS addresses for profile routing.
		return "  forward . 45.90.28.0 45.90.30.0 {\n    policy random\n  }", true
	case "custom":
		if len(s.UpstreamServers) == 0 {
			return "", false
		}
		return fmt.Sprintf("  forward . %s {\n    policy random\n  }", strings.Join(s.UpstreamServers, " ")), true
	}
	return "", false
}

func allowlistCorefile(a config.NetworkAllow) string {
	var b strings.Builder
	fmt.Fprintln(&b, "# agentbox allowlist-mode Corefile (regenerated each run)")

	// Each allowed domain (and its subdomains) gets its own server block that
	// forwards to a public resolver. CoreDNS matches the most-specific zone first,
	// so listed domains resolve; everything else falls through to the default "." block.
	if len(a.Allow) > 0 {
		fmt.Fprintf(&b, "%s {\n", strings.Join(a.Allow, " "))
		fmt.Fprintln(&b, "  forward . 1.1.1.1 8.8.8.8 {")
		fmt.Fprintln(&b, "    policy random")
		fmt.Fprintln(&b, "  }")
		fmt.Fprintln(&b, "  cache 300")
		fmt.Fprintln(&b, "  errors")
		fmt.Fprintln(&b, "  log . {")
		fmt.Fprintln(&b, "    class all")
		fmt.Fprintln(&b, "  }")
		fmt.Fprintln(&b, "}")
	}

	// Default zone: NXDOMAIN for everything not in the allow list.
	fmt.Fprintln(&b, ". {")
	fmt.Fprintln(&b, "  template IN ANY {")
	fmt.Fprintln(&b, "    rcode NXDOMAIN")
	fmt.Fprintln(&b, "  }")
	fmt.Fprintln(&b, "  log . {")
	fmt.Fprintln(&b, "    class all")
	fmt.Fprintln(&b, "  }")
	fmt.Fprintln(&b, "}")
	return b.String()
}
