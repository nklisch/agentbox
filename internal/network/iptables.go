package network

import (
	"fmt"
	"net"
	"os/exec"
)

// IPTables manages the host-side ipset + iptables rules for a project's network.
//
// All commands are invoked via `sudo -n` when Sudo=true (default on Linux when
// not already running as root). This requires passwordless sudo for iptables and
// ipset — run `agentbox doctor` to verify the system is configured correctly.
//
// Callers may inject runFn via NewIPTablesWithFn to replace exec.Command for
// testing without invoking real iptables/ipset.
type IPTables struct {
	// Sudo true prepends "sudo -n" to every iptables/ipset invocation.
	// Default: true on Linux when uid != 0; false when running as root or on macOS.
	Sudo bool

	// runFn is the low-level executor. Nil means use exec.Command.Run().
	// Tests inject a fake here so no actual iptables/ipset calls happen.
	runFn func(string, ...string) error
}

// NewIPTablesWithFn returns an IPTables that calls fn instead of exec.Command.
// Used in tests to record iptables/ipset invocations without side effects.
func NewIPTablesWithFn(fn func(string, ...string) error) *IPTables {
	return &IPTables{runFn: fn}
}

// ipsetName returns the per-project ipset name for the allowed-destinations set.
//
// Name budget: ipset names are limited to 31 characters.
//   abx-<12hex>-a = 4 + 12 + 2 = 18 chars — well within the limit.
//   "abx" = agentbox abbreviation, "a" = allowed.
//
// This name lives only inside the runtime; it is not user-facing.
func ipsetName(spec Spec) string {
	return "abx-" + spec.ProjectID + "-a"
}

// SetupRules creates the per-network ipset and inserts an iptables FORWARD rule
// that drops traffic from the agentbox network's subnet to any destination IP
// not in the ipset.
//
// Idempotent: the `-exist` flag on `ipset create` means a pre-existing set is
// not an error. The iptables rule is inserted with `-I` (prepend); calling Setup
// twice will insert a duplicate rule, so callers should guard with IpsetEnabled()
// checks and avoid double-setup. Teardown removes by exact spec match.
//
// The iptables rule uses `-m comment --comment "agentbox=<project_id>"` so it is
// identifiable in `iptables -S` output and TeardownRules can delete it cleanly.
func (t *IPTables) SetupRules(spec Spec) error {
	if !spec.IpsetEnabled() {
		return nil
	}
	set := ipsetName(spec)

	// Step 1: create the ipset (hash:ip, inet family, no error on duplicate).
	if err := t.run("ipset", "create", set, "hash:ip", "family", "inet", "-exist"); err != nil {
		return fmt.Errorf("ipset create %s: %w", set, err)
	}

	// Step 2: insert FORWARD DROP rule for the subnet → not-in-ipset traffic.
	// Rule order: -I inserts at position 1 (top of FORWARD chain), so it takes
	// effect before any ACCEPT rules further down the chain.
	rule := t.forwardRule("-I", spec, set)
	if err := t.run("iptables", rule...); err != nil {
		return fmt.Errorf("iptables -I FORWARD: %w", err)
	}
	return nil
}

// TeardownRules removes the iptables FORWARD rule and destroys the ipset.
// Idempotent: missing rule or set is not an error (exit codes are ignored on
// these deletions).
func (t *IPTables) TeardownRules(spec Spec) error {
	if !spec.IpsetEnabled() {
		return nil
	}
	set := ipsetName(spec)

	// Delete the iptables rule by exact spec match. `-D` with the full rule
	// spec removes the first matching rule; if there is none, iptables exits 1
	// (ignored here for idempotency).
	rule := t.forwardRule("-D", spec, set)
	_ = t.run("iptables", rule...)

	// Destroy the ipset. Best-effort; a missing set is fine.
	_ = t.run("ipset", "destroy", set)
	return nil
}

// PrePopulate resolves each hostname in allow via stdlib DNS and adds the
// resulting IPs to the ipset. Used by allowlist mode at setup time so the
// first curl/fetch is not blocked before CoreDNS populates the set dynamically.
//
// Failures are logged to stderr and skipped — PrePopulate is best-effort.
// CoreDNS will populate the set dynamically as the box makes real queries.
func (t *IPTables) PrePopulate(spec Spec, allow []string) error {
	set := ipsetName(spec)
	for _, host := range allow {
		ips, err := net.LookupHost(host)
		if err != nil {
			// Best-effort: log and continue.
			fmt.Printf("agentbox-netfilter: PrePopulate %s: %v\n", host, err)
			continue
		}
		for _, ip := range ips {
			// Skip IPv6 for now; the ipset is inet (IPv4) only.
			if net.ParseIP(ip).To4() == nil {
				continue
			}
			_ = t.run("ipset", "add", set, ip, "-exist")
		}
	}
	return nil
}

// forwardRule returns the iptables FORWARD rule args for the given verb (-I or -D).
// The rule matches traffic from spec.Subnet to any destination NOT in the ipset
// and JUMPs to DROP.
func (t *IPTables) forwardRule(verb string, spec Spec, set string) []string {
	return []string{
		verb, "FORWARD",
		"-s", spec.Subnet,
		"-m", "set", "!", "--match-set", set, "dst",
		"-j", "DROP",
		"-m", "comment", "--comment", "agentbox=" + spec.ProjectID,
	}
}

// run executes bin with args, optionally prefixed with "sudo -n".
// If t.runFn is set (test injection), it delegates there instead.
func (t *IPTables) run(bin string, args ...string) error {
	if t.runFn != nil {
		return t.runFn(bin, args...)
	}
	var cmd *exec.Cmd
	if t.Sudo {
		sudoArgs := append([]string{"-n", bin}, args...)
		cmd = exec.Command("sudo", sudoArgs...)
	} else {
		cmd = exec.Command(bin, args...)
	}
	// Discard stdout/stderr — these tools are chatty on success and we don't
	// want their output mixed into agentbox's structured output.
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s %v: %w (output: %s)", bin, args, err, string(out))
	}
	return nil
}
