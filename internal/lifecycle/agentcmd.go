package lifecycle

import (
	"github.com/nklisch/agentbox/internal/exitcode"
)

// BuildAgentCmd returns the final argv that should be launched in the
// agent pane, applying the --mode flag transformation when set.
//
// When mode is empty, the input is returned unchanged (a fresh copy —
// the caller never mutates the agent config slice).
//
// When mode is non-empty, the function rewrites:
//
//	["claude", arg1, arg2, ...]   →   ["claude-mode", mode, arg1, arg2, ...]
//
// --mode is only legal when base[0] == "claude". Pairing it with any
// other binary returns exitcode.InvalidArgs naming the resolved
// command, so the user sees a clear failure at the host CLI rather
// than a "claude-mode: command not found" deep inside the box (we
// only install claude-mode in the claude kit).
//
// The mode value itself is NOT validated against the built-in preset
// list — claude-mode supports user-defined presets via .claude-mode.json
// inside the box, so any non-empty string is forwarded as-is and
// claude-mode rejects unknown names with its own clear error.
func BuildAgentCmd(base []string, mode string) ([]string, error) {
	out := append([]string(nil), base...) // defensive copy
	if mode == "" {
		return out, nil
	}
	if len(base) == 0 || base[0] != "claude" {
		var resolved string
		if len(base) == 0 {
			resolved = "<empty>"
		} else {
			resolved = base[0]
		}
		return nil, exitcode.New(exitcode.InvalidArgs,
			"--mode is only supported for the claude agent (resolved command: %q)",
			resolved)
	}
	// Replace base[0] ("claude") with two tokens: "claude-mode" and the preset.
	// Keep the remaining args (typically --dangerously-skip-permissions).
	out = append([]string{"claude-mode", mode}, base[1:]...)
	return out, nil
}
