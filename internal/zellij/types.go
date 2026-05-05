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

	// LayoutName names the run-mode layout shape. One of "focus",
	// "reviewer", "auditor", or a custom name. Ignored when
	// Mode == ModeShell. Empty falls back to "focus".
	LayoutName string

	// CustomKDL holds the pre-loaded body for custom layouts. Populated by
	// lifecycle (via LoadCustom) before GenerateKDL is called. Empty for
	// built-ins.
	CustomKDL string

	// TrailFile is the in-container path to the trail file. Populated by
	// lifecycle when LayoutName == "auditor" AND agent == "claude". Empty
	// otherwise. The auditor layout uses this to set BOX_TRAIL_FILE for
	// box-trail.
	TrailFile string
}
