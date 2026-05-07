package cli

import (
	"github.com/spf13/cobra"

	"github.com/nklisch/agentbox/internal/exitcode"
)

func newAttachCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attach <project_id>",
		Short: "Reattach to a running box",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := loadConfig()
			if err != nil {
				return err
			}
			l, err := newLifecycle(res.Config)
			if err != nil {
				return exitcode.Wrap(exitcode.Generic, err)
			}
			l.Stdout = cmd.OutOrStdout()
			l.Stderr = cmd.ErrOrStderr()
			l.Quiet = global.Quiet
			return l.Attach(args[0])
		},
	}
	return cmd
}
