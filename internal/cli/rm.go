package cli

import (
	"github.com/spf13/cobra"

	"github.com/nklisch/agentbox/internal/exitcode"
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
			opts := lifecycle.RmOpts{All: all, Force: force, KeepState: keepState}
			if len(args) == 1 {
				opts.Input = args[0]
			}
			return l.Rm(opts)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "remove every agentbox box")
	cmd.Flags().BoolVar(&force, "force", false, "skip confirmation when using --all")
	cmd.Flags().BoolVar(&keepState, "keep-state", false, "remove container but keep session state dir")
	return cmd
}
