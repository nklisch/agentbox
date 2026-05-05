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
	// AGENTBOX_SAVED_DIR should end in /saved
	if v := kvMap["AGENTBOX_SAVED_DIR"]; !strings.HasSuffix(v, "/saved") {
		t.Errorf("AGENTBOX_SAVED_DIR %q does not end in '/saved'", v)
	}
	// AGENTBOX_CREATED should be RFC3339
	if v := kvMap["AGENTBOX_CREATED"]; v == "" {
		t.Errorf("AGENTBOX_CREATED is empty")
	}
	if len(args.EnvVars) != 8 {
		t.Errorf("expected 8 EnvVars, got %d", len(args.EnvVars))
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
