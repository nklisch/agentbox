package network_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nklisch/agentbox/internal/config"
	"github.com/nklisch/agentbox/internal/container"
	"github.com/nklisch/agentbox/internal/network"
	"github.com/nklisch/agentbox/internal/runspec"
)

// fakeRuntime is a test double for container.Runtime used in network tests.
type fakeRuntime struct {
	networks map[string]bool // name → exists
	boxes    map[string]container.Box
	calls    []string
	failOn   map[string]error
}

func newFakeRuntime() *fakeRuntime {
	return &fakeRuntime{
		networks: make(map[string]bool),
		boxes:    make(map[string]container.Box),
		failOn:   make(map[string]error),
	}
}

func (r *fakeRuntime) record(name string) {
	r.calls = append(r.calls, name)
}

func (r *fakeRuntime) containsCall(name string) bool {
	for _, c := range r.calls {
		if c == name {
			return true
		}
	}
	return false
}

func (r *fakeRuntime) Create(args runspec.PodmanCreateArgs) error {
	r.record("Create")
	if err := r.failOn["Create"]; err != nil {
		return err
	}
	r.boxes[args.Name] = container.Box{
		ProjectID: args.Name,
		Status:    container.StatusStopped,
	}
	return nil
}

func (r *fakeRuntime) Start(name string) error {
	r.record("Start")
	if err := r.failOn["Start"]; err != nil {
		return err
	}
	if b, ok := r.boxes[name]; ok {
		b.Status = container.StatusRunning
		r.boxes[name] = b
	}
	return nil
}

func (r *fakeRuntime) Stop(name string) error { return nil }

func (r *fakeRuntime) Inspect(name string) (container.Box, error) {
	r.record("Inspect")
	if err := r.failOn["Inspect"]; err != nil {
		return container.Box{}, err
	}
	b, ok := r.boxes[name]
	if !ok {
		return container.Box{Status: container.StatusMissing}, nil
	}
	return b, nil
}

func (r *fakeRuntime) Exec(name string, opts container.ExecOpts) (int, error) { return 0, nil }

func (r *fakeRuntime) Ls(all bool) ([]container.Box, error) {
	var out []container.Box
	for _, b := range r.boxes {
		out = append(out, b)
	}
	return out, nil
}

func (r *fakeRuntime) Rm(name string, force bool) error {
	r.record("Rm")
	if err := r.failOn["Rm"]; err != nil {
		return err
	}
	delete(r.boxes, name)
	return nil
}

func (r *fakeRuntime) NetworkCreate(name, subnet string) error {
	r.record("NetworkCreate")
	if err := r.failOn["NetworkCreate"]; err != nil {
		return err
	}
	r.networks[name] = true
	return nil
}

func (r *fakeRuntime) NetworkRm(name string) error {
	r.record("NetworkRm")
	if err := r.failOn["NetworkRm"]; err != nil {
		return err
	}
	delete(r.networks, name)
	return nil
}

func (r *fakeRuntime) NetworkExists(name string) (bool, error) {
	r.record("NetworkExists")
	if err := r.failOn["NetworkExists"]; err != nil {
		return false, err
	}
	return r.networks[name], nil
}

// isolateState sets XDG_DATA_HOME to a fresh temp dir.
func isolateState(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
}

// ---- SpecFor tests ----

func TestSpecFor_Modes(t *testing.T) {
	tests := []struct {
		mode            string
		wantNetName     string
		wantSidecarName string
		wantFiltered    bool
	}{
		{"safe", "agentbox-net-abc123456789", "agentbox-coredns-abc123456789", true},
		{"allowlist", "agentbox-net-abc123456789", "agentbox-coredns-abc123456789", true},
		{"open", "", "", false},
		{"off", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			rt := newFakeRuntime()
			mgr := &network.Manager{Runtime: rt}
			cfg := config.DefaultConfig()
			cfg.Network.Mode = tt.mode

			spec := mgr.SpecFor(cfg, "abc123456789")

			if spec.Mode.Filtered() != tt.wantFiltered {
				t.Errorf("Filtered() = %v, want %v", spec.Mode.Filtered(), tt.wantFiltered)
			}
			if tt.wantFiltered {
				if spec.NetworkName != tt.wantNetName {
					t.Errorf("NetworkName = %q, want %q", spec.NetworkName, tt.wantNetName)
				}
				if spec.SidecarName != tt.wantSidecarName {
					t.Errorf("SidecarName = %q, want %q", spec.SidecarName, tt.wantSidecarName)
				}
				if spec.Subnet == "" {
					t.Error("Subnet should not be empty for filtered mode")
				}
				if spec.SidecarIP == "" {
					t.Error("SidecarIP should not be empty for filtered mode")
				}
			}
		})
	}
}

func TestSpecFor_SubnetDerivation(t *testing.T) {
	rt := newFakeRuntime()
	mgr := &network.Manager{Runtime: rt}
	cfg := config.DefaultConfig()
	cfg.Network.Mode = "safe"

	// project_id starting with "ef" → 0xef = 239 → 10.89.239.0/24
	spec := mgr.SpecFor(cfg, "ef38f6abcdef")
	if spec.Subnet != "10.89.239.0/24" {
		t.Errorf("Subnet = %q, want \"10.89.239.0/24\"", spec.Subnet)
	}
	if spec.SidecarIP != "10.89.239.2" {
		t.Errorf("SidecarIP = %q, want \"10.89.239.2\"", spec.SidecarIP)
	}
}

func TestSpecFor_SubnetDerivation_Zero(t *testing.T) {
	rt := newFakeRuntime()
	mgr := &network.Manager{Runtime: rt}
	cfg := config.DefaultConfig()
	cfg.Network.Mode = "safe"

	// project_id starting with "00" → 0 → 10.89.0.0/24
	spec := mgr.SpecFor(cfg, "00abcdef1234")
	if spec.Subnet != "10.89.0.0/24" {
		t.Errorf("Subnet = %q, want \"10.89.0.0/24\"", spec.Subnet)
	}
	if spec.SidecarIP != "10.89.0.2" {
		t.Errorf("SidecarIP = %q, want \"10.89.0.2\"", spec.SidecarIP)
	}
}

// ---- Setup tests ----

func TestSetup_Off_NoRuntimeCalls(t *testing.T) {
	rt := newFakeRuntime()
	mgr := &network.Manager{Runtime: rt}
	cfg := config.DefaultConfig()
	cfg.Network.Mode = "off"
	spec := mgr.SpecFor(cfg, "abc123456789")

	info, err := mgr.Setup(spec)
	if err != nil {
		t.Fatalf("Setup(off): %v", err)
	}
	if info.NetworkName != "none" {
		t.Errorf("Setup(off) NetworkName = %q, want \"none\"", info.NetworkName)
	}
	if len(rt.calls) != 0 {
		t.Errorf("Setup(off) should make zero runtime calls, got %v", rt.calls)
	}
}

func TestSetup_Open_NoRuntimeCalls(t *testing.T) {
	rt := newFakeRuntime()
	mgr := &network.Manager{Runtime: rt}
	cfg := config.DefaultConfig()
	cfg.Network.Mode = "open"
	spec := mgr.SpecFor(cfg, "abc123456789")

	info, err := mgr.Setup(spec)
	if err != nil {
		t.Fatalf("Setup(open): %v", err)
	}
	if info.NetworkName != "bridge" {
		t.Errorf("Setup(open) NetworkName = %q, want \"bridge\"", info.NetworkName)
	}
	if len(rt.calls) != 0 {
		t.Errorf("Setup(open) should make zero runtime calls, got %v", rt.calls)
	}
}

func TestSetup_Safe_CreatesNetworkAndSidecar(t *testing.T) {
	isolateState(t)
	rt := newFakeRuntime()
	mgr := &network.Manager{Runtime: rt}
	cfg := config.DefaultConfig()
	cfg.Network.Mode = "safe"
	cfg.Network.Safe.Upstream = "quad9"
	spec := mgr.SpecFor(cfg, "abc123456789")

	info, err := mgr.Setup(spec)
	if err != nil {
		t.Fatalf("Setup(safe): %v", err)
	}

	if info.NetworkName != "agentbox-net-abc123456789" {
		t.Errorf("NetworkName = %q", info.NetworkName)
	}
	if len(info.SidecarDNS) != 1 {
		t.Fatalf("SidecarDNS = %v, want 1 entry", info.SidecarDNS)
	}
	if info.SidecarDNS[0] != spec.SidecarIP {
		t.Errorf("SidecarDNS[0] = %q, want %q", info.SidecarDNS[0], spec.SidecarIP)
	}

	if !rt.containsCall("NetworkCreate") {
		t.Error("expected NetworkCreate to be called")
	}
	if !rt.containsCall("Create") {
		t.Error("expected Create (sidecar) to be called")
	}
	if !rt.containsCall("Start") {
		t.Error("expected Start (sidecar) to be called")
	}
}

func TestSetup_Safe_NetworkAlreadyExists_NoCreateCall(t *testing.T) {
	isolateState(t)
	rt := newFakeRuntime()
	mgr := &network.Manager{Runtime: rt}
	cfg := config.DefaultConfig()
	cfg.Network.Mode = "safe"
	spec := mgr.SpecFor(cfg, "abc123456789")

	// Pre-create the network so NetworkExists returns true
	rt.networks[spec.NetworkName] = true

	_, err := mgr.Setup(spec)
	if err != nil {
		t.Fatalf("Setup(safe, network exists): %v", err)
	}

	if rt.containsCall("NetworkCreate") {
		t.Error("NetworkCreate should NOT be called when network already exists")
	}
}

func TestSetup_Safe_SidecarAlreadyRunning_NoCreateCall(t *testing.T) {
	isolateState(t)
	rt := newFakeRuntime()
	mgr := &network.Manager{Runtime: rt}
	cfg := config.DefaultConfig()
	cfg.Network.Mode = "safe"
	spec := mgr.SpecFor(cfg, "abc123456789")

	// Pre-create the sidecar as already running
	rt.boxes[spec.SidecarName] = container.Box{
		Status: container.StatusRunning,
	}

	_, err := mgr.Setup(spec)
	if err != nil {
		t.Fatalf("Setup(safe, sidecar running): %v", err)
	}

	if rt.containsCall("Create") {
		t.Error("Create should NOT be called when sidecar already exists")
	}
	// Start should still be called (idempotent)
	if !rt.containsCall("Start") {
		t.Error("Start should still be called (idempotent)")
	}
}

func TestTeardown_Safe_StopsAndRemovesAll(t *testing.T) {
	isolateState(t)
	rt := newFakeRuntime()
	mgr := &network.Manager{Runtime: rt}
	cfg := config.DefaultConfig()
	cfg.Network.Mode = "safe"
	spec := mgr.SpecFor(cfg, "abc123456789")

	// Pre-populate so Rm/NetworkRm have something to remove
	rt.boxes[spec.SidecarName] = container.Box{Status: container.StatusRunning}
	rt.networks[spec.NetworkName] = true

	if err := mgr.Teardown(spec); err != nil {
		t.Fatalf("Teardown(safe): %v", err)
	}

	if !rt.containsCall("Rm") {
		t.Error("expected Rm (sidecar) to be called on Teardown")
	}
	if !rt.containsCall("NetworkRm") {
		t.Error("expected NetworkRm to be called on Teardown")
	}
}

func TestTeardown_Off_NoRuntimeCalls(t *testing.T) {
	rt := newFakeRuntime()
	mgr := &network.Manager{Runtime: rt}
	cfg := config.DefaultConfig()
	cfg.Network.Mode = "off"
	spec := mgr.SpecFor(cfg, "abc123456789")

	if err := mgr.Teardown(spec); err != nil {
		t.Fatalf("Teardown(off): %v", err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("Teardown(off) should make zero runtime calls, got %v", rt.calls)
	}
}

// ---- WriteCorefile test ----

func TestSetup_Safe_WritesCorefile(t *testing.T) {
	isolateState(t)
	rt := newFakeRuntime()
	mgr := &network.Manager{Runtime: rt}
	cfg := config.DefaultConfig()
	cfg.Network.Mode = "safe"
	cfg.Network.Safe.Upstream = "quad9"
	spec := mgr.SpecFor(cfg, "abc123456789")

	_, err := mgr.Setup(spec)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	// Find the state dir and verify Corefile exists
	xdgData := os.Getenv("XDG_DATA_HOME")
	corefilePath := filepath.Join(xdgData, "agentbox", "sessions", "abc123456789", "Corefile")
	data, err := os.ReadFile(corefilePath)
	if err != nil {
		t.Fatalf("Corefile not found at %s: %v", corefilePath, err)
	}
	if len(data) == 0 {
		t.Error("Corefile is empty")
	}
	if !containsStr(string(data), "9.9.9.9") {
		t.Errorf("Corefile missing quad9 upstream:\n%s", data)
	}
}

// ---- BuildSidecar tests ----

func TestBuildSidecar_FieldsCorrect(t *testing.T) {
	spec := network.Spec{
		ProjectID:   "abc123456789",
		NetworkName: "agentbox-net-abc123456789",
		SidecarName: "agentbox-coredns-abc123456789",
		SidecarIP:   "10.89.171.2",
		Subnet:      "10.89.171.0/24",
	}
	in := network.SidecarInput{
		Spec:     spec,
		StateDir: "/tmp/state/sessions/abc123456789",
	}

	args := network.BuildSidecar(in)

	if args.Name != "agentbox-coredns-abc123456789" {
		t.Errorf("Name = %q", args.Name)
	}
	if args.Network != "agentbox-net-abc123456789" {
		t.Errorf("Network = %q", args.Network)
	}
	if args.IP != "10.89.171.2" {
		t.Errorf("IP = %q, want \"10.89.171.2\"", args.IP)
	}
	if args.Image != network.CoreDNSImage {
		t.Errorf("Image = %q, want %q", args.Image, network.CoreDNSImage)
	}

	// Check labels
	var hasAgentbox, hasRole, hasProjID bool
	for _, kv := range args.Labels {
		switch {
		case kv.Key == "agentbox" && kv.Value == "1":
			hasAgentbox = true
		case kv.Key == "agentbox.role" && kv.Value == "coredns":
			hasRole = true
		case kv.Key == "agentbox.project_id" && kv.Value == "abc123456789":
			hasProjID = true
		}
	}
	if !hasAgentbox {
		t.Error("missing label agentbox=1")
	}
	if !hasRole {
		t.Error("missing label agentbox.role=coredns")
	}
	if !hasProjID {
		t.Error("missing label agentbox.project_id")
	}

	// Check Corefile mount
	var hasCorefile bool
	for _, m := range args.Mounts {
		if m.Target == "/Corefile" && m.Mode == "ro" {
			hasCorefile = true
		}
	}
	if !hasCorefile {
		t.Errorf("missing Corefile mount at /Corefile:ro, mounts: %+v", args.Mounts)
	}
}

// ---- Mode helpers tests ----

func TestMode_Filtered(t *testing.T) {
	if !network.ModeSafe.Filtered() {
		t.Error("safe.Filtered() should be true")
	}
	if !network.ModeAllowlist.Filtered() {
		t.Error("allowlist.Filtered() should be true")
	}
	if network.ModeOff.Filtered() {
		t.Error("off.Filtered() should be false")
	}
	if network.ModeOpen.Filtered() {
		t.Error("open.Filtered() should be false")
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStrHelper(s, sub))
}

func containsStrHelper(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
