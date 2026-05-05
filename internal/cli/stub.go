package cli

import (
	"github.com/spf13/cobra"

	"github.com/nklisch/agentbox/internal/exitcode"
)

// notImplementedRunE returns an exit-1 error explaining the command lands
// in a later phase. Used by all P1 stubs.
func notImplementedRunE(cmd *cobra.Command) error {
	return exitcode.New(
		exitcode.Generic,
		"agentbox %s: not yet implemented (lands in a later phase)",
		cmd.Name(),
	)
}

// stubCmd builds a cobra.Command whose RunE prints the not-implemented
// message and returns the appropriate exit-coded error. Used by shell,
// attach, exec, ls, rm, build.
func stubCmd(use, short string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		RunE:  func(cmd *cobra.Command, args []string) error { return notImplementedRunE(cmd) },
	}
}

func newShellCmd() *cobra.Command {
	return stubCmd("shell", "Bare interactive shell in the per-project box")
}
func newAttachCmd() *cobra.Command {
	return stubCmd("attach <project_id>", "Reattach to a running box's zellij session")
}
func newExecCmd() *cobra.Command {
	return stubCmd("exec <project_id> <command>", "Run a one-off command inside a live box")
}
func newLsCmd() *cobra.Command {
	return stubCmd("ls", "List boxes")
}
func newRmCmd() *cobra.Command {
	return stubCmd("rm <project_id>", "Stop and remove boxes + their session state")
}
