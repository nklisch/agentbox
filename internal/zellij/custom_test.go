package zellij

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- LoadCustom tests ---

func TestLoadCustom_SubstitutesProjectAbs(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "layout.kdl")
	if err := os.WriteFile(path, []byte(`cwd "{{.ProjectAbs}}"`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadCustom(path, TemplateVars{ProjectAbs: "/home/u/proj"})
	if err != nil {
		t.Fatalf("LoadCustom: %v", err)
	}
	if !strings.Contains(got, "/home/u/proj") {
		t.Errorf("LoadCustom result %q missing ProjectAbs substitution", got)
	}
}

func TestLoadCustom_SubstitutesAgentCmdFull(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "layout.kdl")
	content := `pane { {{.AgentCmdFull}} }`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	vars := BuildTemplateVars(Layout{
		AgentCmd: []string{"claude", "--dangerously-skip-permissions"},
	})
	got, err := LoadCustom(path, vars)
	if err != nil {
		t.Fatalf("LoadCustom: %v", err)
	}
	if !strings.Contains(got, `command "claude"`) {
		t.Errorf("LoadCustom result missing command line:\n%s", got)
	}
	if !strings.Contains(got, `args "--dangerously-skip-permissions"`) {
		t.Errorf("LoadCustom result missing args line:\n%s", got)
	}
}

func TestLoadCustom_MissingFile(t *testing.T) {
	_, err := LoadCustom("/nonexistent/path/layout.kdl", TemplateVars{})
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
	if !strings.Contains(err.Error(), "/nonexistent/path/layout.kdl") {
		t.Errorf("error %q should mention the path", err.Error())
	}
}

func TestLoadCustom_MalformedTemplate(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "layout.kdl")
	if err := os.WriteFile(path, []byte(`{{.Foo`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadCustom(path, TemplateVars{})
	if err == nil {
		t.Fatal("expected error for malformed template, got nil")
	}
	if !strings.Contains(err.Error(), "parse custom layout") {
		t.Errorf("error %q should mention parse", err.Error())
	}
}

func TestLoadCustom_EmptyFile(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "empty.kdl")
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadCustom(path, TemplateVars{})
	if err != nil {
		t.Fatalf("LoadCustom: %v", err)
	}
	if got != "" {
		t.Errorf("LoadCustom of empty file: got %q, want empty string", got)
	}
}

// --- BuildTemplateVars tests ---

func TestBuildTemplateVars_EmptyAgentCmd(t *testing.T) {
	vars := BuildTemplateVars(Layout{Shell: "zsh"})
	if vars.AgentCommand != "zsh" {
		t.Errorf("AgentCommand = %q, want %q", vars.AgentCommand, "zsh")
	}
	if vars.AgentArgs != "" {
		t.Errorf("AgentArgs = %q, want empty", vars.AgentArgs)
	}
	if vars.AgentCmdFull != `command "zsh"` {
		t.Errorf("AgentCmdFull = %q, want %q", vars.AgentCmdFull, `command "zsh"`)
	}
}

func TestBuildTemplateVars_SingleElementAgentCmd(t *testing.T) {
	vars := BuildTemplateVars(Layout{AgentCmd: []string{"claude"}})
	if vars.AgentCommand != "claude" {
		t.Errorf("AgentCommand = %q, want %q", vars.AgentCommand, "claude")
	}
	if vars.AgentArgs != "" {
		t.Errorf("AgentArgs = %q, want empty (single-element has no args)", vars.AgentArgs)
	}
	if vars.AgentCmdFull != `command "claude"` {
		t.Errorf("AgentCmdFull = %q, want no args line for single-element cmd", vars.AgentCmdFull)
	}
}

func TestBuildTemplateVars_MultiElementAgentCmd(t *testing.T) {
	vars := BuildTemplateVars(Layout{
		AgentCmd: []string{"claude", "--dangerously-skip-permissions"},
	})
	if vars.AgentCommand != "claude" {
		t.Errorf("AgentCommand = %q, want %q", vars.AgentCommand, "claude")
	}
	wantArgs := `"--dangerously-skip-permissions"`
	if vars.AgentArgs != wantArgs {
		t.Errorf("AgentArgs = %q, want %q", vars.AgentArgs, wantArgs)
	}
	wantFull := "command \"claude\"\nargs \"--dangerously-skip-permissions\""
	if vars.AgentCmdFull != wantFull {
		t.Errorf("AgentCmdFull = %q, want %q", vars.AgentCmdFull, wantFull)
	}
}

func TestBuildTemplateVars_AgentCmdWithEmbeddedQuotes(t *testing.T) {
	// %q must properly escape embedded quotes.
	vars := BuildTemplateVars(Layout{
		AgentCmd: []string{"cmd", `arg"with"quotes`},
	})
	// The arg must be properly quoted — %q adds backslash escapes.
	if !strings.Contains(vars.AgentArgs, `arg`) {
		t.Errorf("AgentArgs %q should contain the arg", vars.AgentArgs)
	}
	// Ensure the embedded quotes are escaped, not bare.
	if strings.Contains(vars.AgentArgs, `arg"with"quotes`) {
		t.Errorf("AgentArgs %q should have escaped embedded quotes", vars.AgentArgs)
	}
}

func TestBuildTemplateVars_TrailFile(t *testing.T) {
	vars := BuildTemplateVars(Layout{
		AgentCmd:  []string{"claude"},
		TrailFile: "/etc/agentbox/trail.jsonl",
	})
	if vars.TrailFile != "/etc/agentbox/trail.jsonl" {
		t.Errorf("TrailFile = %q, want %q", vars.TrailFile, "/etc/agentbox/trail.jsonl")
	}
}

// Integration: full round-trip of BuildTemplateVars + LoadCustom.
func TestBuildTemplateVars_LoadCustom_RoundTrip(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "layout.kdl")
	content := `layout {
    tab name="{{.Shell}}" {
        pane { {{.AgentCmdFull}} cwd "{{.ProjectAbs}}" }
    }
}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	l := Layout{
		AgentCmd:   []string{"claude", "--dangerously-skip-permissions"},
		ProjectAbs: "/home/u/proj",
		Shell:      "zsh",
	}
	vars := BuildTemplateVars(l)
	got, err := LoadCustom(path, vars)
	if err != nil {
		t.Fatalf("LoadCustom: %v", err)
	}
	for _, frag := range []string{
		`tab name="zsh"`,
		`command "claude"`,
		`args "--dangerously-skip-permissions"`,
		`cwd "/home/u/proj"`,
	} {
		if !strings.Contains(got, frag) {
			t.Errorf("round-trip result missing fragment %q:\n%s", frag, got)
		}
	}
}
