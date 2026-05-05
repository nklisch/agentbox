package runspec_test

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/nklisch/agentbox/internal/config"
	"github.com/nklisch/agentbox/internal/runspec"
)

func defaultInput() runspec.BuildInput {
	return runspec.BuildInput{
		ProjectID:   "abc123456789",
		ProjectAbs:  "/home/user/myproject",
		ProjectName: "myproject",
		Agent:       "claude",
		Kits:        []string{"polyglot", "claude"},
		HomeDir:     "/home/user",
		StateDir:    "/home/user/.local/share/agentbox/sessions/abc123456789",
		Created:     time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestKitImageTag_OrderIndependent(t *testing.T) {
	tag1 := runspec.KitImageTag([]string{"claude", "polyglot"})
	tag2 := runspec.KitImageTag([]string{"polyglot", "claude"})
	if tag1 != tag2 {
		t.Errorf("KitImageTag order-dependent: %q != %q", tag1, tag2)
	}
}

func TestKitImageTag_Format(t *testing.T) {
	tag := runspec.KitImageTag([]string{"claude", "polyglot"})
	re := regexp.MustCompile(`^agentbox/[a-f0-9]{12}$`)
	if !re.MatchString(tag) {
		t.Errorf("KitImageTag() = %q, does not match ^agentbox/[a-f0-9]{12}$", tag)
	}
}

func TestKitImageTag_Deterministic(t *testing.T) {
	kits := []string{"polyglot", "claude", "containers"}
	tag1 := runspec.KitImageTag(kits)
	tag2 := runspec.KitImageTag(kits)
	if tag1 != tag2 {
		t.Errorf("KitImageTag not deterministic: %q != %q", tag1, tag2)
	}
}

func TestNetworkArg_Off(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Network.Mode = "off"
	got := runspec.NetworkArg(cfg, "abc123")
	if got != "none" {
		t.Errorf("NetworkArg(off) = %q, want %q", got, "none")
	}
}

func TestNetworkArg_Open(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Network.Mode = "open"
	got := runspec.NetworkArg(cfg, "abc123")
	if got != "bridge" {
		t.Errorf("NetworkArg(open) = %q, want %q", got, "bridge")
	}
}

func TestNetworkArg_Safe(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Network.Mode = "safe"
	got := runspec.NetworkArg(cfg, "abc123")
	if got != "agentbox-net-abc123" {
		t.Errorf("NetworkArg(safe) = %q, want %q", got, "agentbox-net-abc123")
	}
}

func TestNetworkArg_Allowlist(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Network.Mode = "allowlist"
	got := runspec.NetworkArg(cfg, "abc123")
	if got != "agentbox-net-abc123" {
		t.Errorf("NetworkArg(allowlist) = %q, want %q", got, "agentbox-net-abc123")
	}
}

func TestBuildPodmanCreateArgs_ContainerName(t *testing.T) {
	cfg := config.DefaultConfig()
	in := defaultInput()

	args, err := runspec.BuildPodmanCreateArgs(cfg, in)
	if err != nil {
		t.Fatalf("BuildPodmanCreateArgs() error: %v", err)
	}
	if args.Name != "agentbox-abc123456789" {
		t.Errorf("Name = %q, want %q", args.Name, "agentbox-abc123456789")
	}
}

func TestBuildPodmanCreateArgs_SamePathMount(t *testing.T) {
	cfg := config.DefaultConfig()
	in := defaultInput()

	args, err := runspec.BuildPodmanCreateArgs(cfg, in)
	if err != nil {
		t.Fatalf("BuildPodmanCreateArgs() error: %v", err)
	}

	var found bool
	for _, m := range args.Mounts {
		if m.Source == in.ProjectAbs && m.Target == in.ProjectAbs && m.Mode == "rw" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected same-path mount {%s:%s:rw}, not found in %+v", in.ProjectAbs, in.ProjectAbs, args.Mounts)
	}
}

func TestBuildPodmanCreateArgs_CapDropAll(t *testing.T) {
	cfg := config.DefaultConfig()
	in := defaultInput()

	args, err := runspec.BuildPodmanCreateArgs(cfg, in)
	if err != nil {
		t.Fatalf("BuildPodmanCreateArgs() error: %v", err)
	}
	found := false
	for _, c := range args.CapDrop {
		if c == "ALL" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected --cap-drop ALL in CapDrop %v", args.CapDrop)
	}
}

func TestBuildPodmanCreateArgs_SecOpt(t *testing.T) {
	cfg := config.DefaultConfig()
	in := defaultInput()

	args, err := runspec.BuildPodmanCreateArgs(cfg, in)
	if err != nil {
		t.Fatalf("BuildPodmanCreateArgs() error: %v", err)
	}
	found := false
	for _, s := range args.SecOpt {
		if s == "no-new-privileges" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected no-new-privileges in SecOpt %v", args.SecOpt)
	}
}

func TestBuildPodmanCreateArgs_ContainersEnable(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Containers.Enable = true
	in := defaultInput()

	args, err := runspec.BuildPodmanCreateArgs(cfg, in)
	if err != nil {
		t.Fatalf("BuildPodmanCreateArgs() error: %v", err)
	}

	hasSeccomp := false
	hasUnmask := false
	for _, s := range args.SecOpt {
		if strings.HasPrefix(s, "seccomp=") {
			hasSeccomp = true
		}
		if strings.HasPrefix(s, "unmask=") {
			hasUnmask = true
		}
	}
	if !hasSeccomp {
		t.Error("expected seccomp= in SecOpt when Containers.Enable=true")
	}
	if !hasUnmask {
		t.Error("expected unmask= in SecOpt when Containers.Enable=true")
	}
}

func TestBuildPodmanCreateArgs_NetworkMode(t *testing.T) {
	tests := []struct {
		mode string
		want string
	}{
		{"off", "none"},
		{"open", "bridge"},
		{"safe", "agentbox-net-abc123456789"},
		{"allowlist", "agentbox-net-abc123456789"},
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.Network.Mode = tt.mode
			in := defaultInput()
			args, err := runspec.BuildPodmanCreateArgs(cfg, in)
			if err != nil {
				t.Fatalf("BuildPodmanCreateArgs() error: %v", err)
			}
			if args.Network != tt.want {
				t.Errorf("Network = %q, want %q", args.Network, tt.want)
			}
		})
	}
}

func TestBuildPodmanCreateArgs_LabelsOrdered(t *testing.T) {
	cfg := config.DefaultConfig()
	in := defaultInput()

	args1, err := runspec.BuildPodmanCreateArgs(cfg, in)
	if err != nil {
		t.Fatalf("BuildPodmanCreateArgs() error: %v", err)
	}
	args2, err := runspec.BuildPodmanCreateArgs(cfg, in)
	if err != nil {
		t.Fatalf("BuildPodmanCreateArgs() second call error: %v", err)
	}
	if len(args1.Labels) != len(args2.Labels) {
		t.Fatalf("label count mismatch: %d vs %d", len(args1.Labels), len(args2.Labels))
	}
	for i := range args1.Labels {
		if args1.Labels[i] != args2.Labels[i] {
			t.Errorf("label[%d]: %+v != %+v (non-deterministic)", i, args1.Labels[i], args2.Labels[i])
		}
	}
}

func TestToShell_ContainsContainerName(t *testing.T) {
	cfg := config.DefaultConfig()
	in := defaultInput()

	args, err := runspec.BuildPodmanCreateArgs(cfg, in)
	if err != nil {
		t.Fatalf("BuildPodmanCreateArgs() error: %v", err)
	}
	shell := args.ToShell("podman")
	if !strings.Contains(shell, "agentbox-") {
		t.Errorf("ToShell output does not contain 'agentbox-':\n%s", shell)
	}
}

func TestToShell_ContainsProjectPath(t *testing.T) {
	cfg := config.DefaultConfig()
	in := defaultInput()

	args, err := runspec.BuildPodmanCreateArgs(cfg, in)
	if err != nil {
		t.Fatalf("BuildPodmanCreateArgs() error: %v", err)
	}
	shell := args.ToShell("podman")
	if !strings.Contains(shell, in.ProjectAbs) {
		t.Errorf("ToShell output does not contain project path %q:\n%s", in.ProjectAbs, shell)
	}
}

func TestToShell_MultilineWithContinuations(t *testing.T) {
	cfg := config.DefaultConfig()
	in := defaultInput()

	args, err := runspec.BuildPodmanCreateArgs(cfg, in)
	if err != nil {
		t.Fatalf("BuildPodmanCreateArgs() error: %v", err)
	}
	shell := args.ToShell("podman")
	lines := strings.Split(shell, "\n")
	if len(lines) < 5 {
		t.Errorf("expected multi-line output, got %d lines", len(lines))
	}
	// Most lines (except the last two) should end with " \" (continuation).
	continuationCount := 0
	for _, line := range lines {
		if strings.HasSuffix(line, ` \`) {
			continuationCount++
		}
	}
	if continuationCount < 3 {
		t.Errorf("expected at least 3 continuation lines, got %d:\n%s", continuationCount, shell)
	}
}

func TestToShell_EndsWithNewline(t *testing.T) {
	cfg := config.DefaultConfig()
	in := defaultInput()

	args, err := runspec.BuildPodmanCreateArgs(cfg, in)
	if err != nil {
		t.Fatalf("BuildPodmanCreateArgs() error: %v", err)
	}
	shell := args.ToShell("podman")
	if !strings.HasSuffix(shell, "\n") {
		t.Error("ToShell output does not end with newline")
	}
}

func TestToShell_CapDropAndSecOpt(t *testing.T) {
	cfg := config.DefaultConfig()
	in := defaultInput()

	args, err := runspec.BuildPodmanCreateArgs(cfg, in)
	if err != nil {
		t.Fatalf("BuildPodmanCreateArgs() error: %v", err)
	}
	shell := args.ToShell("podman")
	if !strings.Contains(shell, "--cap-drop ALL") {
		t.Errorf("ToShell missing --cap-drop ALL:\n%s", shell)
	}
	if !strings.Contains(shell, "--security-opt no-new-privileges") {
		t.Errorf("ToShell missing --security-opt no-new-privileges:\n%s", shell)
	}
}

func TestBuildPodmanCreateArgs_EnvVars(t *testing.T) {
	cfg := config.DefaultConfig()
	in := defaultInput()

	args, err := runspec.BuildPodmanCreateArgs(cfg, in)
	if err != nil {
		t.Fatalf("BuildPodmanCreateArgs() error: %v", err)
	}

	kvMap := make(map[string]string, len(args.EnvVars))
	for _, kv := range args.EnvVars {
		kvMap[kv.Key] = kv.Value
	}

	checks := map[string]string{
		"AGENTBOX_PROJECT_ID": in.ProjectID,
		"AGENTBOX_PROJECT":    in.ProjectName,
		"AGENTBOX_AGENT":      in.Agent,
		"AGENTBOX_KITS":       strings.Join(in.Kits, ","),
		"AGENTBOX_NETWORK":    cfg.Network.Mode,
	}
	for key, want := range checks {
		if got, ok := kvMap[key]; !ok {
			t.Errorf("EnvVars missing key %q", key)
		} else if got != want {
			t.Errorf("EnvVars[%q] = %q, want %q", key, got, want)
		}
	}
	// AGENTBOX_SAVED_DIR is the in-container mount target (not the host path),
	// so box-save inside the container can resolve it to the bind-mounted dir.
	if v, want := kvMap["AGENTBOX_SAVED_DIR"], "/root/.local/share/agentbox-saved"; v != want {
		t.Errorf("AGENTBOX_SAVED_DIR = %q, want %q", v, want)
	}
	// AGENTBOX_CREATED should be RFC3339
	if v := kvMap["AGENTBOX_CREATED"]; v == "" {
		t.Errorf("AGENTBOX_CREATED is empty")
	}
	// 8 base + IS_SANDBOX (claude-only workaround for the root refusal of
	// --dangerously-skip-permissions).
	if len(args.EnvVars) != 9 {
		t.Errorf("expected 9 EnvVars (8 base + IS_SANDBOX for claude), got %d", len(args.EnvVars))
	}
}

func TestToShell_ContainsEnvVars(t *testing.T) {
	cfg := config.DefaultConfig()
	in := defaultInput()

	args, err := runspec.BuildPodmanCreateArgs(cfg, in)
	if err != nil {
		t.Fatalf("BuildPodmanCreateArgs() error: %v", err)
	}
	shell := args.ToShell("podman")
	if !strings.Contains(shell, "AGENTBOX_PROJECT_ID=") {
		t.Errorf("ToShell missing AGENTBOX_PROJECT_ID= line:\n%s", shell)
	}
	if !strings.Contains(shell, in.ProjectID) {
		t.Errorf("ToShell missing project ID %q:\n%s", in.ProjectID, shell)
	}
}

func TestBuildPodmanCreateArgs_SavedMount(t *testing.T) {
	cfg := config.DefaultConfig()
	in := defaultInput()

	args, err := runspec.BuildPodmanCreateArgs(cfg, in)
	if err != nil {
		t.Fatalf("BuildPodmanCreateArgs() error: %v", err)
	}

	var found bool
	for _, m := range args.Mounts {
		if m.Source == in.StateDir+"/saved" &&
			m.Target == "/root/.local/share/agentbox-saved" &&
			m.Mode == "rw" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected saved/ mount {%s/saved:/root/.local/share/agentbox-saved:rw}, not found in %+v",
			in.StateDir, args.Mounts)
	}
}

func TestBuildPodmanCreateArgs_ExtraMount_Invalid(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Mounts.Extra = []string{"not-valid-format"}
	in := defaultInput()

	_, err := runspec.BuildPodmanCreateArgs(cfg, in)
	if err == nil {
		t.Fatal("expected error for invalid extra mount format")
	}
	if !strings.Contains(err.Error(), "mounts.extra") {
		t.Errorf("error %q should mention 'mounts.extra'", err.Error())
	}
}

func TestBuildPodmanCreateArgs_ExtraMount_BadMode(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Mounts.Extra = []string{"/src:/dst:badmode"}
	in := defaultInput()

	_, err := runspec.BuildPodmanCreateArgs(cfg, in)
	if err == nil {
		t.Fatal("expected error for invalid mount mode")
	}
}

func TestBuildPodmanCreateArgs_ExtraMount_Valid(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Mounts.Extra = []string{"/data:/data:ro"}
	in := defaultInput()

	args, err := runspec.BuildPodmanCreateArgs(cfg, in)
	if err != nil {
		t.Fatalf("BuildPodmanCreateArgs() error: %v", err)
	}
	found := false
	for _, m := range args.Mounts {
		if m.Source == "/data" && m.Target == "/data" && m.Mode == "ro" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("extra mount /data:/data:ro not found in %+v", args.Mounts)
	}
}

// ---- Phase 6: DNS / SidecarDNS tests ----

func TestBuildPodmanCreateArgs_SidecarDNS_Populated(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Network.Mode = "safe"
	in := defaultInput()
	in.SidecarDNS = []string{"10.89.171.2"}

	args, err := runspec.BuildPodmanCreateArgs(cfg, in)
	if err != nil {
		t.Fatalf("BuildPodmanCreateArgs() error: %v", err)
	}
	if len(args.DNS) != 1 || args.DNS[0] != "10.89.171.2" {
		t.Errorf("DNS = %v, want [10.89.171.2]", args.DNS)
	}
}

func TestBuildPodmanCreateArgs_SidecarDNS_Empty(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Network.Mode = "open"
	in := defaultInput()
	// No SidecarDNS set

	args, err := runspec.BuildPodmanCreateArgs(cfg, in)
	if err != nil {
		t.Fatalf("BuildPodmanCreateArgs() error: %v", err)
	}
	if len(args.DNS) != 0 {
		t.Errorf("DNS should be empty for open mode, got %v", args.DNS)
	}
}

func TestToShell_ContainsDNSLine(t *testing.T) {
	cfg := config.DefaultConfig()
	in := defaultInput()
	in.SidecarDNS = []string{"10.89.7.2"}

	args, err := runspec.BuildPodmanCreateArgs(cfg, in)
	if err != nil {
		t.Fatalf("BuildPodmanCreateArgs() error: %v", err)
	}
	shell := args.ToShell("podman")
	if !strings.Contains(shell, `--dns "10.89.7.2"`) {
		t.Errorf("ToShell output missing --dns line:\n%s", shell)
	}
}

func TestBuildPodmanCreateArgs_RoleLabel(t *testing.T) {
	cfg := config.DefaultConfig()
	in := defaultInput()

	args, err := runspec.BuildPodmanCreateArgs(cfg, in)
	if err != nil {
		t.Fatalf("BuildPodmanCreateArgs() error: %v", err)
	}

	var found bool
	for _, kv := range args.Labels {
		if kv.Key == "agentbox.role" && kv.Value == "box" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected agentbox.role=box label, not found in %+v", args.Labels)
	}
}

func TestBuildPodmanCreateArgs_EnvVars_CountUpdated(t *testing.T) {
	cfg := config.DefaultConfig()
	in := defaultInput()

	args, err := runspec.BuildPodmanCreateArgs(cfg, in)
	if err != nil {
		t.Fatalf("BuildPodmanCreateArgs() error: %v", err)
	}
	// 8 base AGENTBOX_* + IS_SANDBOX for claude (v0.2.1).
	if len(args.EnvVars) != 9 {
		t.Errorf("expected 9 EnvVars, got %d: %+v", len(args.EnvVars), args.EnvVars)
	}
}

// ---- v0.2.1: Claude Code root-refusal + ~/.claude.json mount fixes ----

func TestBuildPodmanCreateArgs_ClaudeIsSandboxEnv(t *testing.T) {
	cfg := config.DefaultConfig()
	in := defaultInput() // Agent: "claude"

	args, err := runspec.BuildPodmanCreateArgs(cfg, in)
	if err != nil {
		t.Fatalf("BuildPodmanCreateArgs() error: %v", err)
	}
	found := false
	for _, kv := range args.EnvVars {
		if kv.Key == "IS_SANDBOX" && kv.Value == "1" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected IS_SANDBOX=1 in EnvVars when agent is claude")
	}
}

func TestBuildPodmanCreateArgs_NonClaudeNoIsSandbox(t *testing.T) {
	cfg := config.DefaultConfig()
	in := defaultInput()
	in.Agent = "codex"

	args, err := runspec.BuildPodmanCreateArgs(cfg, in)
	if err != nil {
		t.Fatalf("BuildPodmanCreateArgs() error: %v", err)
	}
	for _, kv := range args.EnvVars {
		if kv.Key == "IS_SANDBOX" {
			t.Errorf("IS_SANDBOX should not be set for agent %q, got %q", in.Agent, kv.Value)
		}
	}
}

func TestBuildPodmanCreateArgs_ClaudeJSONMount(t *testing.T) {
	cfg := config.DefaultConfig()
	in := defaultInput() // Agent: "claude", HomeDir: "/home/user"

	args, err := runspec.BuildPodmanCreateArgs(cfg, in)
	if err != nil {
		t.Fatalf("BuildPodmanCreateArgs() error: %v", err)
	}
	found := false
	for _, m := range args.Mounts {
		if m.Source == "/home/user/.claude.json" &&
			m.Target == "/root/.claude.json" &&
			m.Mode == "rw" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected ~/.claude.json mount for claude agent, mounts: %+v", args.Mounts)
	}
}

func TestBuildPodmanCreateArgs_NonClaudeNoJSONMount(t *testing.T) {
	cfg := config.DefaultConfig()
	in := defaultInput()
	in.Agent = "codex"

	args, err := runspec.BuildPodmanCreateArgs(cfg, in)
	if err != nil {
		t.Fatalf("BuildPodmanCreateArgs() error: %v", err)
	}
	for _, m := range args.Mounts {
		if strings.HasSuffix(m.Source, ".claude.json") {
			t.Errorf("non-claude agent %q should not get .claude.json mount, got: %+v",
				in.Agent, m)
		}
	}
}

// ---- Phase 7: CapAdd + Devices + seccomp mount tests ----

func TestBuildPodmanCreateArgs_ContainersEnable_AddsCapAndDevices(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Containers.Enable = true
	in := defaultInput()

	args, err := runspec.BuildPodmanCreateArgs(cfg, in)
	if err != nil {
		t.Fatalf("BuildPodmanCreateArgs() error: %v", err)
	}

	// CapAdd should have SETUID and SETGID from cfg.Containers.ExtraCaps default.
	if len(args.CapAdd) != 2 {
		t.Fatalf("expected 2 CapAdd entries, got %d: %v", len(args.CapAdd), args.CapAdd)
	}
	capMap := make(map[string]bool)
	for _, c := range args.CapAdd {
		capMap[c] = true
	}
	for _, want := range []string{"SETUID", "SETGID"} {
		if !capMap[want] {
			t.Errorf("CapAdd missing %q; got %v", want, args.CapAdd)
		}
	}

	// Devices should have /dev/fuse from cfg.Containers.ExtraDevices default.
	if len(args.Devices) != 1 || args.Devices[0] != "/dev/fuse" {
		t.Errorf("Devices = %v, want [\"/dev/fuse\"]", args.Devices)
	}
}

func TestBuildPodmanCreateArgs_ContainersDisable_EmptyCapAndDevices(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Containers.Enable = false
	in := defaultInput()

	args, err := runspec.BuildPodmanCreateArgs(cfg, in)
	if err != nil {
		t.Fatalf("BuildPodmanCreateArgs() error: %v", err)
	}
	if len(args.CapAdd) != 0 {
		t.Errorf("CapAdd should be empty when Containers.Enable=false, got %v", args.CapAdd)
	}
	if len(args.Devices) != 0 {
		t.Errorf("Devices should be empty when Containers.Enable=false, got %v", args.Devices)
	}
}

func TestBuildPodmanCreateArgs_ContainersEnable_NoSeccompMountWithoutPath(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Containers.Enable = true
	in := defaultInput()
	// SeccompPath is empty — lifecycle is responsible for populating it.
	// Without it, no seccomp mount should appear.

	args, err := runspec.BuildPodmanCreateArgs(cfg, in)
	if err != nil {
		t.Fatalf("BuildPodmanCreateArgs() error: %v", err)
	}
	for _, m := range args.Mounts {
		if m.Target == "/etc/agentbox/seccomp/containers.json" {
			t.Errorf("unexpected seccomp mount without SeccompPath: %+v", m)
		}
	}
}

func TestBuildPodmanCreateArgs_ContainersEnable_WithSeccompPath_AddsMount(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Containers.Enable = true
	in := defaultInput()
	in.SeccompPath = "/host/state/seccomp/containers.json"

	args, err := runspec.BuildPodmanCreateArgs(cfg, in)
	if err != nil {
		t.Fatalf("BuildPodmanCreateArgs() error: %v", err)
	}
	var found bool
	for _, m := range args.Mounts {
		if m.Source == in.SeccompPath &&
			m.Target == "/etc/agentbox/seccomp/containers.json" &&
			m.Mode == "ro" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected seccomp ro mount {%s:/etc/agentbox/seccomp/containers.json:ro}, not found in %+v",
			in.SeccompPath, args.Mounts)
	}
}

func TestToShell_ContainersEnable_HasCapAddAndDevice(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Containers.Enable = true
	in := defaultInput()

	args, err := runspec.BuildPodmanCreateArgs(cfg, in)
	if err != nil {
		t.Fatalf("BuildPodmanCreateArgs() error: %v", err)
	}
	shell := args.ToShell("podman")

	for _, want := range []string{
		"--cap-add SETUID",
		"--cap-add SETGID",
		`--device "/dev/fuse"`,
	} {
		if !strings.Contains(shell, want) {
			t.Errorf("ToShell missing %q:\n%s", want, shell)
		}
	}

	// Order: cap-drop before cap-add before security-opt.
	dropIdx := strings.Index(shell, "--cap-drop ALL")
	addIdx := strings.Index(shell, "--cap-add SETUID")
	secoptIdx := strings.Index(shell, "--security-opt seccomp=")
	if dropIdx < 0 || addIdx < 0 || secoptIdx < 0 {
		t.Fatalf("missing expected flags in ToShell output:\n%s", shell)
	}
	if dropIdx >= addIdx {
		t.Errorf("--cap-drop (idx %d) should appear before --cap-add (idx %d)", dropIdx, addIdx)
	}
	if addIdx >= secoptIdx {
		t.Errorf("--cap-add (idx %d) should appear before --security-opt (idx %d)", addIdx, secoptIdx)
	}
}
