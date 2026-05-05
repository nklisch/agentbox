package zellij

// Mode selects the layout shape.
type Mode int

const (
	// ModeRun is the full agentbox run layout: agent main pane (70%) +
	// git ticker + btm stats + shell tab.
	ModeRun Mode = iota
	// ModeShell is the single-pane shell layout used by `agentbox shell`
	// when --no-zellij is not set.
	ModeShell
)

// Layout describes everything GenerateKDL needs to produce a layout file.
type Layout struct {
	Mode       Mode
	AgentCmd   []string // e.g. ["claude", "--dangerously-skip-permissions"]
	ProjectAbs string   // absolute path of the project, used as cwd for panes
	Shell      string   // "zsh" / "bash" / "fish"; from cfg.Shell.Shell
}
