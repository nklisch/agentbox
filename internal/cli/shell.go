package cli

import (
	"github.com/spf13/cobra"

	"github.com/nklisch/agentbox/internal/exitcode"
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
			_ = noZellij // P3 has no zellij; flag accepted for future-compat.
			return l.Shell(lifecycle.RunOpts{Fresh: fresh, Attach: true})
		},
	}
	cmd.Flags().BoolVar(&fresh, "fresh", false, "remove any existing box first")
	cmd.Flags().BoolVar(&noZellij, "no-zellij", false, "skip zellij entirely (no-op in P3)")
	return cmd
}
