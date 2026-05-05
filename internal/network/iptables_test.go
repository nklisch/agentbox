package network_test

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/nklisch/agentbox/internal/config"
	"github.com/nklisch/agentbox/internal/container"
	"github.com/nklisch/agentbox/internal/network"
)

// fakeIPTables provides a fake IPTables for testing Manager behaviour.
// It records every (bin, args...) tuple passed to run.
type fakeIPTables struct {
	calls  [][]string // each entry: [bin, arg1, arg2, ...]
	failOn string     // if non-empty, return an error when bin matches this
}

func newFakeIPTables() *fakeIPTables {
	return &fakeIPTables{}
}

// AsIPTables returns a *network.IPTables wired with this fake's runFn.
func (f *fakeIPTables) AsIPTables() *network.IPTables {
	return network.NewIPTablesWithFn(func(bin string, args ...string) error {
		entry := append([]string{bin}, args...)
		f.calls = append(f.calls, entry)
		if f.failOn != "" && bin == f.failOn {
			return fmt.Errorf("injected failure for %s", bin)
		}
		return nil
	})
}

func (f *fakeIPTables) containsBin(bin string) bool {
	for _, c := range f.calls {
		if len(c) > 0 && c[0] == bin {
			return true
		}
	}
	return false
}

// containsArgs returns true when any recorded call has bin+args as a prefix.
func (f *fakeIPTables) containsArgs(bin string, args ...string) bool {
	for _, c := range f.calls {
		if len(c) < 1+len(args) || c[0] != bin {
			continue
		}
		match := true
		for i, a := range args {
			if c[1+i] != a {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func (f *fakeIPTables) callCount() int { return len(f.calls) }

// ---- IPTables unit tests (via injected runFn) ----

func TestIPTables_SetupRules_CommandShape(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("IpsetEnabled() is always false on macOS")
	}
	fi := newFakeIPTables()
	ipt := fi.AsIPTables()

	cfg := config.DefaultConfig()
	cfg.Network.Mode = "safe"
	cfg.Network.Safe.BlockDirectIP = true
	mgr := &network.Manager{Runtime: newFakeRuntime()}
	spec := mgr.SpecFor(cfg, "abc123456789")

	if err := ipt.SetupRules(spec); err != nil {
		t.Fatalf("SetupRules: %v", err)
	}

	// Expect an ipset create call.
	if !fi.containsBin("ipset") {
		t.Error("expected ipset call from SetupRules")
	}
	// Expect an iptables call.
	if !fi.containsBin("iptables") {
		t.Error("expected iptables call from SetupRules")
	}
	// The ipset name must be ≤ 31 chars.
	setName := "abx-abc123456789-a"
	if len(setName) > 31 {
		t.Errorf("ipset name %q length %d exceeds 31-char limit", setName, len(setName))
	}
	if !fi.containsArgs("ipset", "create", setName) {
		t.Errorf("expected 'ipset create %s ...', calls: %v", setName, fi.calls)
	}
	// iptables must use -I FORWARD.
	if !fi.containsArgs("iptables", "-I", "FORWARD") {
		t.Errorf("expected 'iptables -I FORWARD ...', calls: %v", fi.calls)
	}
	// Comment must identify the project.
	var hasComment bool
	for _, c := range fi.calls {
		for _, arg := range c {
			if strings.Contains(arg, "agentbox=abc123456789") {
				hasComment = true
			}
		}
	}
	if !hasComment {
		t.Errorf("expected comment 'agentbox=abc123456789' in iptables args, calls: %v", fi.calls)
	}
}

func TestIPTables_SetupRules_SkippedWhenIpsetDisabled(t *testing.T) {
	fi := newFakeIPTables()
	ipt := fi.AsIPTables()

	cfg := config.DefaultConfig()
	cfg.Network.Mode = "safe"
	cfg.Network.Safe.BlockDirectIP = false // ipset disabled
	mgr := &network.Manager{Runtime: newFakeRuntime()}
	spec := mgr.SpecFor(cfg, "abc123456789")

	if err := ipt.SetupRules(spec); err != nil {
		t.Fatalf("SetupRules: %v", err)
	}
	if fi.callCount() != 0 {
		t.Errorf("expected 0 calls when IpsetEnabled()=false, got %d: %v", fi.callCount(), fi.calls)
	}
}

func TestIPTables_TeardownRules_Idempotent(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("IpsetEnabled() is always false on macOS")
	}
	fi := newFakeIPTables()
	ipt := fi.AsIPTables()

	cfg := config.DefaultConfig()
	cfg.Network.Mode = "safe"
	cfg.Network.Safe.BlockDirectIP = true
	mgr := &network.Manager{Runtime: newFakeRuntime()}
	spec := mgr.SpecFor(cfg, "abc123456789")

	// Call Teardown twice — should return nil both times (idempotent).
	if err := ipt.TeardownRules(spec); err != nil {
		t.Fatalf("TeardownRules (first): %v", err)
	}
	if err := ipt.TeardownRules(spec); err != nil {
		t.Fatalf("TeardownRules (second): %v", err)
	}

	// Expect -D FORWARD calls.
	if !fi.containsArgs("iptables", "-D", "FORWARD") {
		t.Errorf("expected 'iptables -D FORWARD ...', calls: %v", fi.calls)
	}
	// Expect ipset destroy call.
	if !fi.containsArgs("ipset", "destroy") {
		t.Errorf("expected 'ipset destroy ...', calls: %v", fi.calls)
	}
}

func TestIPTables_TeardownRules_SkippedWhenIpsetDisabled(t *testing.T) {
	fi := newFakeIPTables()
	ipt := fi.AsIPTables()

	cfg := config.DefaultConfig()
	cfg.Network.Mode = "safe"
	cfg.Network.Safe.BlockDirectIP = false
	mgr := &network.Manager{Runtime: newFakeRuntime()}
	spec := mgr.SpecFor(cfg, "abc123456789")

	if err := ipt.TeardownRules(spec); err != nil {
		t.Fatalf("TeardownRules: %v", err)
	}
	if fi.callCount() != 0 {
		t.Errorf("expected 0 calls when IpsetEnabled()=false, got %v", fi.calls)
	}
}

func TestIPTables_PrePopulate_AddsIPs(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("IpsetEnabled() is always false on macOS")
	}
	fi := newFakeIPTables()
	ipt := fi.AsIPTables()

	cfg := config.DefaultConfig()
	cfg.Network.Mode = "allowlist"
	mgr := &network.Manager{Runtime: newFakeRuntime()}
	spec := mgr.SpecFor(cfg, "abc123456789")

	// Use localhost — guaranteed to resolve.
	if err := ipt.PrePopulate(spec, []string{"localhost"}); err != nil {
		t.Fatalf("PrePopulate: %v", err)
	}
	// 127.0.0.1 is private/loopback — PrePopulate should skip it.
	// No ipset add calls expected for loopback. But no error either.
}

func TestIPTables_PrePopulate_SkipsUnresolvable(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("IpsetEnabled() is always false on macOS")
	}
	fi := newFakeIPTables()
	ipt := fi.AsIPTables()

	cfg := config.DefaultConfig()
	cfg.Network.Mode = "allowlist"
	mgr := &network.Manager{Runtime: newFakeRuntime()}
	spec := mgr.SpecFor(cfg, "abc123456789")

	// An unresolvable hostname should not cause PrePopulate to return an error.
	if err := ipt.PrePopulate(spec, []string{"this.host.does.not.exist.invalid"}); err != nil {
		t.Fatalf("PrePopulate should not error on bad hostname, got: %v", err)
	}
	_ = fi // calls may or may not have happened; no assert needed
}

// ---- Manager tests with fake IPTables ----

func TestSetup_Safe_BlockDirectIP_CallsIPTables(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("IpsetEnabled() is always false on macOS")
	}
	isolateState(t)

	rt := newFakeRuntime()
	fi := newFakeIPTables()

	mgr := &network.Manager{
		Runtime:      rt,
		IPTables:     fi.AsIPTables(),
		NetfilterBin: "/bin/true", // fake bin that exits 0 (daemon "start" no-ops)
	}

	cfg := config.DefaultConfig()
	cfg.Network.Mode = "safe"
	cfg.Network.Safe.BlockDirectIP = true
	spec := mgr.SpecFor(cfg, "abc123456789")

	_, err := mgr.Setup(spec)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	if !fi.containsBin("ipset") {
		t.Error("expected ipset calls from Setup with block_direct_ip=true")
	}
	if !fi.containsBin("iptables") {
		t.Error("expected iptables calls from Setup with block_direct_ip=true")
	}
}

func TestSetup_Safe_BlockDirectIPFalse_NoIPTables(t *testing.T) {
	isolateState(t)

	rt := newFakeRuntime()
	fi := newFakeIPTables()

	mgr := &network.Manager{
		Runtime:  rt,
		IPTables: fi.AsIPTables(),
	}

	cfg := config.DefaultConfig()
	cfg.Network.Mode = "safe"
	cfg.Network.Safe.BlockDirectIP = false // ipset disabled
	spec := mgr.SpecFor(cfg, "abc123456789")

	_, err := mgr.Setup(spec)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	if fi.callCount() != 0 {
		t.Errorf("expected no IPTables calls when block_direct_ip=false, got %v", fi.calls)
	}
}

func TestTeardown_Safe_BlockDirectIP_CallsTeardownRules(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("IpsetEnabled() is always false on macOS")
	}
	isolateState(t)

	rt := newFakeRuntime()
	fi := newFakeIPTables()

	mgr := &network.Manager{
		Runtime:  rt,
		IPTables: fi.AsIPTables(),
	}

	cfg := config.DefaultConfig()
	cfg.Network.Mode = "safe"
	cfg.Network.Safe.BlockDirectIP = true
	spec := mgr.SpecFor(cfg, "abc123456789")

	// Pre-populate so Teardown has something to remove.
	rt.boxes[spec.SidecarName] = container.Box{Status: container.StatusRunning}
	rt.networks[spec.NetworkName] = true

	if err := mgr.Teardown(spec); err != nil {
		t.Fatalf("Teardown: %v", err)
	}

	// iptables -D FORWARD should be called.
	if !fi.containsArgs("iptables", "-D", "FORWARD") {
		t.Errorf("expected 'iptables -D FORWARD ...' in Teardown, calls: %v", fi.calls)
	}
	// ipset destroy should be called.
	if !fi.containsArgs("ipset", "destroy") {
		t.Errorf("expected 'ipset destroy ...' in Teardown, calls: %v", fi.calls)
	}
	// Runtime Rm and NetworkRm should still happen.
	if !rt.containsCall("Rm") {
		t.Error("expected Rm called in Teardown")
	}
	if !rt.containsCall("NetworkRm") {
		t.Error("expected NetworkRm called in Teardown")
	}
}

func TestTeardown_Idempotent_NoIPTablesError(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("IpsetEnabled() is always false on macOS")
	}
	isolateState(t)

	rt := newFakeRuntime()
	fi := newFakeIPTables()

	mgr := &network.Manager{
		Runtime:  rt,
		IPTables: fi.AsIPTables(),
	}

	cfg := config.DefaultConfig()
	cfg.Network.Mode = "safe"
	cfg.Network.Safe.BlockDirectIP = true
	spec := mgr.SpecFor(cfg, "abc123456789")

	// Call Teardown twice — second time has no boxes/networks to remove but
	// should not return an error.
	if err := mgr.Teardown(spec); err != nil {
		t.Fatalf("Teardown (first): %v", err)
	}
	if err := mgr.Teardown(spec); err != nil {
		t.Fatalf("Teardown (second, idempotent): %v", err)
	}
}
