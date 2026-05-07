package cli

import (
	"github.com/spf13/cobra"

	"github.com/nklisch/agentbox/internal/lifecycle"
)

func newRmCmd() *cobra.Command {
	var (
		all       bool
		force     bool
		keepState bool
	)
	cmd := &cobra.Command{
		Use:   "rm <project_id>",
		Short: "Stop and remove boxes + their session state",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			l, _, err := initLifecycleCmd(cmd)
			if err != nil {
				return err
			}
			opts := lifecycle.RmOpts{All: all, Force: force, KeepState: keepState, DryRun: global.DryRun}
			if len(args) == 1 {
				opts.Input = args[0]
			}
			return l.Rm(opts)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "remove every agentbox box")
	cmd.Flags().BoolVar(&force, "force", false, "skip the y/N confirmation prompt when using --all")
	cmd.Flags().BoolVar(&keepState, "keep-state", false, "remove container but keep session state dir")
	return cmd
}
