package cli

import (
	"github.com/spf13/cobra"
)

func newAttachCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attach <project_id>",
		Short: "Reattach to a running box",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			l, _, err := initLifecycleCmd(cmd)
			if err != nil {
				return err
			}
			return l.Attach(args[0])
		},
	}
	return cmd
}
