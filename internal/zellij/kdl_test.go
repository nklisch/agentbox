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
		// Agent pane wraps cmd in box-agent (v0.2.5+) so pane drops to zsh on exit.
		`command "box-agent"`,
		`args "claude" "--dangerously-skip-permissions"`,
		`cwd "/tmp/abx-proj"`,
		`command "watch"`,
		// Git pane uses box-git-watch dashboard (v0.2.5+) instead of plain git status.
		`args "--color" "-n" "2" "box-git-watch"`,
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

func TestGenerateKDL_AgentPane_WrapsInBoxAgent(t *testing.T) {
	out := GenerateKDL(Layout{Mode: ModeRun, AgentCmd: []string{"claude"}, ProjectAbs: "/p", Shell: "zsh"})
	// v0.2.5+: even a single-element AgentCmd is wrapped, so the pane has
	// `command "box-agent"` and `args "claude"`. The wrapper drops the
	// pane into zsh -l on exit instead of leaving a dead/bare-shell pane.
	agentPane := regexp.MustCompile(`(?s)pane size="70%" name="agent" \{(.*?)\}`).FindStringSubmatch(out)
	if len(agentPane) != 2 {
		t.Fatalf("agent pane block not found in:\n%s", out)
	}
	if !strings.Contains(agentPane[1], `command "box-agent"`) {
		t.Errorf("expected `command \"box-agent\"` in agent pane; got:\n%s", agentPane[1])
	}
	if !strings.Contains(agentPane[1], `args "claude"`) {
		t.Errorf("expected `args \"claude\"` in agent pane; got:\n%s", agentPane[1])
	}
}

// Empty AgentCmd should NOT be wrapped — when no agent is configured,
// the fallback shell is what runs in the pane directly.
func TestGenerateKDL_EmptyAgentCmd_NotWrapped(t *testing.T) {
	out := GenerateKDL(Layout{Mode: ModeRun, AgentCmd: nil, ProjectAbs: "/p", Shell: "bash"})
	if strings.Contains(out, "box-agent") {
		t.Errorf("empty AgentCmd should not be wrapped in box-agent:\n%s", out)
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

// ---- Layout name dispatch ----

func TestGenerateKDL_LayoutName_FocusExplicit(t *testing.T) {
	// Explicit "focus" should produce the same output as empty LayoutName.
	withName := GenerateKDL(Layout{Mode: ModeRun, LayoutName: "focus", AgentCmd: []string{"claude"}, ProjectAbs: "/p", Shell: "zsh"})
	withEmpty := GenerateKDL(Layout{Mode: ModeRun, LayoutName: "", AgentCmd: []string{"claude"}, ProjectAbs: "/p", Shell: "zsh"})
	if withName != withEmpty {
		t.Errorf("LayoutName=focus should produce identical output to LayoutName=''")
	}
}

func TestGenerateKDL_LayoutName_CustomKDL(t *testing.T) {
	out := GenerateKDL(Layout{
		Mode:       ModeRun,
		LayoutName: "myown",
		CustomKDL:  "custom body",
	})
	if out != "custom body" {
		t.Errorf("CustomKDL should be returned verbatim, got: %q", out)
	}
}

func TestGenerateKDL_LayoutName_CustomKDL_OverridesLayoutName(t *testing.T) {
	// CustomKDL is checked before LayoutName dispatch for run mode.
	out := GenerateKDL(Layout{
		Mode:       ModeRun,
		LayoutName: "focus",
		CustomKDL:  "custom takes priority",
	})
	if out != "custom takes priority" {
		t.Errorf("CustomKDL should take priority over LayoutName, got: %q", out)
	}
}

// ---- reviewer layout ----

func TestGenerateKDL_Reviewer_HasAllPanes(t *testing.T) {
	out := GenerateKDL(Layout{
		Mode:       ModeRun,
		LayoutName: "reviewer",
		AgentCmd:   []string{"claude"},
		ProjectAbs: "/p",
		Shell:      "zsh",
	})
	for _, frag := range []string{
		`tab name="reviewer" focus=true`,
		`pane size="50%" name="agent"`,
		`pane size="50%" name="diff"`,
		`pane size="30%" name="tests"`,
		`command "box-agent"`,
		`args "claude"`,
		`command "watch"`,
		`args "--color" "-n" "2" "box-diff-watch"`,
		`command "box-tests-watch"`,
		`tab name="shell"`,
		`default_tab_template`,
		`plugin location="tab-bar"`,
		`plugin location="status-bar"`,
	} {
		if !strings.Contains(out, frag) {
			t.Errorf("reviewer layout missing fragment %q in:\n%s", frag, out)
		}
	}
}

func TestGenerateKDL_Reviewer_TwoTabs(t *testing.T) {
	out := GenerateKDL(Layout{
		Mode:       ModeRun,
		LayoutName: "reviewer",
		AgentCmd:   []string{"claude"},
		ProjectAbs: "/p",
		Shell:      "zsh",
	})
	if got := strings.Count(out, "tab name="); got != 2 {
		t.Errorf("reviewer layout: want 2 tabs, got %d:\n%s", got, out)
	}
}

func TestGenerateKDL_Reviewer_EmptyAgentCmd_NotWrapped(t *testing.T) {
	out := GenerateKDL(Layout{
		Mode:       ModeRun,
		LayoutName: "reviewer",
		AgentCmd:   nil,
		ProjectAbs: "/p",
		Shell:      "zsh",
	})
	if strings.Contains(out, "box-agent") {
		t.Errorf("empty AgentCmd should not be wrapped in box-agent in reviewer layout:\n%s", out)
	}
}

func TestGenerateKDL_Reviewer_Cwd(t *testing.T) {
	out := GenerateKDL(Layout{
		Mode:       ModeRun,
		LayoutName: "reviewer",
		AgentCmd:   []string{"claude"},
		ProjectAbs: "/home/u/my project",
		Shell:      "zsh",
	})
	if !strings.Contains(out, `cwd "/home/u/my project"`) {
		t.Errorf("reviewer layout missing quoted cwd:\n%s", out)
	}
}

// ---- auditor layout ----

func TestGenerateKDL_Auditor_HasAllPanes(t *testing.T) {
	out := GenerateKDL(Layout{
		Mode:       ModeRun,
		LayoutName: "auditor",
		AgentCmd:   []string{"claude"},
		ProjectAbs: "/p",
		Shell:      "zsh",
	})
	for _, frag := range []string{
		`tab name="auditor" focus=true`,
		`pane size="60%" name="agent"`,
		`pane size="40%" name="trail"`,
		`command "box-agent"`,
		`args "claude"`,
		`command "box-trail"`,
		`tab name="shell"`,
		`default_tab_template`,
		`plugin location="tab-bar"`,
		`plugin location="status-bar"`,
	} {
		if !strings.Contains(out, frag) {
			t.Errorf("auditor layout missing fragment %q in:\n%s", frag, out)
		}
	}
}

func TestGenerateKDL_Auditor_TwoTabs(t *testing.T) {
	out := GenerateKDL(Layout{
		Mode:       ModeRun,
		LayoutName: "auditor",
		AgentCmd:   []string{"claude"},
		ProjectAbs: "/p",
		Shell:      "zsh",
	})
	if got := strings.Count(out, "tab name="); got != 2 {
		t.Errorf("auditor layout: want 2 tabs, got %d:\n%s", got, out)
	}
}

func TestGenerateKDL_Auditor_EmptyAgentCmd_NotWrapped(t *testing.T) {
	out := GenerateKDL(Layout{
		Mode:       ModeRun,
		LayoutName: "auditor",
		AgentCmd:   nil,
		ProjectAbs: "/p",
		Shell:      "zsh",
	})
	if strings.Contains(out, "box-agent") {
		t.Errorf("empty AgentCmd should not be wrapped in box-agent in auditor layout:\n%s", out)
	}
}

func TestGenerateKDL_Auditor_NoArgsForTrail(t *testing.T) {
	// box-trail reads $BOX_TRAIL_FILE from env — no args in the KDL.
	out := GenerateKDL(Layout{
		Mode:       ModeRun,
		LayoutName: "auditor",
		AgentCmd:   []string{"claude"},
		ProjectAbs: "/p",
		Shell:      "zsh",
	})
	// The trail pane should have command "box-trail" but no args line.
	if !strings.Contains(out, `command "box-trail"`) {
		t.Errorf("auditor layout missing box-trail command:\n%s", out)
	}
	// Verify no args after box-trail (trail reads from env only).
	if strings.Contains(out, `args "box-trail"`) {
		t.Errorf("box-trail should not have args in the KDL:\n%s", out)
	}
}

func TestGenerateKDL_Auditor_Cwd(t *testing.T) {
	out := GenerateKDL(Layout{
		Mode:       ModeRun,
		LayoutName: "auditor",
		AgentCmd:   []string{"claude"},
		ProjectAbs: "/home/u/my project",
		Shell:      "zsh",
	})
	if !strings.Contains(out, `cwd "/home/u/my project"`) {
		t.Errorf("auditor layout missing quoted cwd:\n%s", out)
	}
}

// ---- v0.2.3: tab-bar + status-bar via default_tab_template ----

func TestGenerateKDL_Run_HasTabAndStatusBars(t *testing.T) {
	out := GenerateKDL(Layout{
		Mode:       ModeRun,
		AgentCmd:   []string{"claude", "--dangerously-skip-permissions"},
		ProjectAbs: "/tmp/abx-proj",
		Shell:      "zsh",
	})
	for _, frag := range []string{
		`default_tab_template`,
		`plugin location="tab-bar"`,
		`plugin location="status-bar"`,
		`children`,
	} {
		if !strings.Contains(out, frag) {
			t.Errorf("expected %q in run-mode output to surface zellij keybinds:\n%s", frag, out)
		}
	}
}

func TestGenerateKDL_Shell_HasTabAndStatusBars(t *testing.T) {
	out := GenerateKDL(Layout{Mode: ModeShell, ProjectAbs: "/p", Shell: "zsh"})
	for _, frag := range []string{
		`default_tab_template`,
		`plugin location="tab-bar"`,
		`plugin location="status-bar"`,
	} {
		if !strings.Contains(out, frag) {
			t.Errorf("expected %q in shell-mode output:\n%s", frag, out)
		}
	}
}
