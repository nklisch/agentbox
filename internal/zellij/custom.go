package zellij

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"text/template"
)

// TemplateVars are the values substituted into custom layout files before
// zellij parses them. Field names are the user-visible API — once published,
// they are load-bearing (users embed them in their KDL files).
type TemplateVars struct {
	ProjectAbs   string // host absolute path of the project (cwd for panes)
	Shell        string // configured shell ("zsh", "bash", "fish")
	AgentCommand string // first element of agent.cmd (e.g. "claude")
	AgentArgs    string // remaining args, KDL-quoted+space-joined
	// (e.g. `"--dangerously-skip-permissions"`)
	AgentCmdFull string // ready-to-paste KDL block:
	//     command "claude"
	//     args "--dangerously-skip-permissions"
	// with no leading whitespace; user is responsible for indenting.
	TrailFile string // in-container trail file path; empty unless lifecycle
	// chose to wire trail (auditor + claude)
}

// LoadCustom reads a layout KDL file from disk and substitutes TemplateVars.
// Returns the rendered KDL body or an error if the file can't be read or
// templated.
//
// The template uses default Go delimiters ({{ and }}). KDL doesn't use double
// braces syntactically, so collision is unlikely. If a user's layout contains
// `{{` they can escape via `{{"{{"}}` — standard Go template idiom.
func LoadCustom(path string, vars TemplateVars) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read custom layout %s: %w", path, err)
	}
	tmpl, err := template.New(path).Parse(string(body))
	if err != nil {
		return "", fmt.Errorf("parse custom layout %s: %w", path, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, vars); err != nil {
		return "", fmt.Errorf("render custom layout %s: %w", path, err)
	}
	return buf.String(), nil
}

// BuildTemplateVars assembles the TemplateVars for a Layout. Handles empty
// AgentCmd by falling back to the configured shell, and KDL-quotes args.
func BuildTemplateVars(l Layout) TemplateVars {
	vars := TemplateVars{
		ProjectAbs: l.ProjectAbs,
		Shell:      l.Shell,
		TrailFile:  l.TrailFile,
	}
	if len(l.AgentCmd) == 0 {
		vars.AgentCommand = l.Shell
		vars.AgentArgs = ""
		vars.AgentCmdFull = fmt.Sprintf("command %q", l.Shell)
		return vars
	}
	vars.AgentCommand = l.AgentCmd[0]
	if len(l.AgentCmd) > 1 {
		quoted := make([]string, 0, len(l.AgentCmd)-1)
		for _, a := range l.AgentCmd[1:] {
			quoted = append(quoted, fmt.Sprintf("%q", a))
		}
		vars.AgentArgs = strings.Join(quoted, " ")
		vars.AgentCmdFull = fmt.Sprintf("command %q\nargs %s",
			l.AgentCmd[0], vars.AgentArgs)
	} else {
		vars.AgentCmdFull = fmt.Sprintf("command %q", l.AgentCmd[0])
	}
	return vars
}
