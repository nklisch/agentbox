package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/nklisch/agentbox/internal/doctor"
	"github.com/nklisch/agentbox/internal/exitcode"
)

func newDoctorCmd() *cobra.Command {
	var fix bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Verify runtime, mounts, kits",
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := loadConfig()
			if err != nil {
				return err
			}
			result := doctor.Run(res.Config)

			if global.JSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				if err := enc.Encode(result); err != nil {
					return exitcode.Wrap(exitcode.Generic, err)
				}
			} else {
				for _, c := range result.Checks {
					fmt.Fprintf(cmd.OutOrStdout(), "[%s] %s — %s\n", c.Status, c.Name, c.Message)
				}
			}
			if result.AnyFail() {
				return exitcode.New(exitcode.Generic, "doctor reported failures")
			}
			_ = fix
			return nil
		},
	}
	cmd.Flags().BoolVar(&fix, "fix", false, "attempt safe auto-remediation")
	return cmd
}
