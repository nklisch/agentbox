package zellij

import (
	"regexp"
	"strings"
	"testing"
)

func TestGenerateKDL_Run_HasAllPanes(t *testing.T) {
	out := GenerateKDL(Layout{
		Mode:       ModeRun,
		AgentCmd:   []string{"claude", "--dangerously-skip-permissions"},
		ProjectAbs: "/tmp/abx-proj",
		Shell:      "zsh",
	})
	for _, frag := range []string{
		`name="agentbox"`,
		`name="agent"`,
		`name="git"`,
		`name="stats"`,
		`name="shell"`,
		`command "claude"`,
		`args "--dangerously-skip-permissions"`,
		`cwd "/tmp/abx-proj"`,
		`command "watch"`,
		`command "btm"`,
		`command "zsh"`,
	} {
		if !strings.Contains(out, frag) {
			t.Errorf("missing fragment %q in:\n%s", frag, out)
		}
	}
}

func TestGenerateKDL_Shell_SinglePane(t *testing.T) {
	out := GenerateKDL(Layout{Mode: ModeShell, ProjectAbs: "/p", Shell: "zsh"})
	// Only one tab.
	if got := strings.Count(out, "tab name="); got != 1 {
		t.Errorf("want 1 tab, got %d:\n%s", got, out)
	}
	// No agent / git / stats panes in shell mode.
	for _, forbidden := range []string{`name="agent"`, `name="git"`, `name="stats"`, `command "watch"`, `command "btm"`} {
		if strings.Contains(out, forbidden) {
			t.Errorf("unexpected fragment %q in shell-mode output:\n%s", forbidden, out)
		}
	}
}

func TestGenerateKDL_Deterministic(t *testing.T) {
	in := Layout{Mode: ModeRun, AgentCmd: []string{"a", "b"}, ProjectAbs: "/p", Shell: "zsh"}
	a := GenerateKDL(in)
	b := GenerateKDL(in)
	if a != b {
		t.Fatalf("non-deterministic output")
	}
}

func TestGenerateKDL_NoArgsWhenSingleCommand(t *testing.T) {
	out := GenerateKDL(Layout{Mode: ModeRun, AgentCmd: []string{"claude"}, ProjectAbs: "/p", Shell: "zsh"})
	// The agent pane block should have `command "claude"` but NO `args` line.
	agentPane := regexp.MustCompile(`(?s)pane size="70%" name="agent" \{(.*?)\}`).FindStringSubmatch(out)
	if len(agentPane) != 2 {
		t.Fatalf("agent pane block not found in:\n%s", out)
	}
	if strings.Contains(agentPane[1], "args") {
		t.Errorf("expected no args line for single-element AgentCmd; got:\n%s", agentPane[1])
	}
}

func TestGenerateKDL_EmptyAgentCmd_FallsBackToZsh(t *testing.T) {
	out := GenerateKDL(Layout{Mode: ModeRun, AgentCmd: nil, ProjectAbs: "/p", Shell: "bash"})
	// With empty AgentCmd, writeCommand falls back to "zsh".
	if !strings.Contains(out, `command "zsh"`) {
		t.Errorf("expected fallback to command \"zsh\" for empty AgentCmd:\n%s", out)
	}
}

func TestGenerateKDL_UnknownMode_FallsBackToShell(t *testing.T) {
	out := GenerateKDL(Layout{Mode: Mode(99), ProjectAbs: "/p", Shell: "zsh"})
	// Unknown mode should fall back to shell layout (single tab).
	if got := strings.Count(out, "tab name="); got != 1 {
		t.Errorf("unknown mode: want 1 tab (shell fallback), got %d:\n%s", got, out)
	}
}

func TestGenerateKDL_CwdQuoted(t *testing.T) {
	// Paths with spaces must be safely quoted.
	out := GenerateKDL(Layout{Mode: ModeRun, AgentCmd: []string{"zsh"}, ProjectAbs: "/home/user/my project", Shell: "zsh"})
	if !strings.Contains(out, `cwd "/home/user/my project"`) {
		t.Errorf("expected quoted cwd with space:\n%s", out)
	}
}

func TestModeRun_IsZeroValue(t *testing.T) {
	var m Mode
	if m != ModeRun {
		t.Errorf("ModeRun should be zero value, got %d", m)
	}
}
