package cli

import (
	"github.com/spf13/cobra"

	"github.com/nklisch/agentbox/internal/lifecycle"
)

func newShellCmd() *cobra.Command {
	var (
		fresh    bool
		noZellij bool
	)
	cmd := &cobra.Command{
		Use:   "shell",
		Short: "Bare interactive shell in the per-project box",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			l, _, err := initLifecycleCmd(cmd)
			if err != nil {
				return err
			}
			return l.Shell(lifecycle.RunOpts{
				Fresh:    fresh,
				Attach:   true,
				NoZellij: noZellij,
			})
		},
	}
	cmd.Flags().BoolVar(&fresh, "fresh", false, "remove any existing box first")
	cmd.Flags().BoolVar(&noZellij, "no-zellij", false, "skip zellij and use bare shell exec (for scripting)")
	return cmd
}
