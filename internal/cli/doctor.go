package cli

import (
	"encoding/json"
	"fmt"
	"strings"

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

			if fix && (result.AnyFail() || hasWarnWithFix(result)) {
				attempted, fixErr := result.ApplyFixes()
				if !global.JSON && len(attempted) > 0 {
					fmt.Fprintf(cmd.OutOrStdout(), "applied fixes: %s\n", strings.Join(attempted, ", "))
				}
				if fixErr != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "fix error: %v\n", fixErr)
				}
				// Re-run checks so output reflects the new state.
				result = doctor.Run(res.Config)
			}

			if global.JSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				if err := enc.Encode(result); err != nil {
					return exitcode.Wrap(exitcode.Generic, err)
				}
			} else {
				for _, c := range result.Checks {
					if global.Quiet && c.Status != doctor.StatusFail {
						continue
					}
					fmt.Fprintf(cmd.OutOrStdout(), "[%s] %s — %s\n", c.Status, c.Name, c.Message)
				}
			}
			if result.AnyFail() {
				return exitcode.New(exitcode.Generic, "doctor reported failures")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&fix, "fix", false, "attempt safe auto-remediation")
	return cmd
}

// hasWarnWithFix reports whether any check is WARN with a non-nil Fix.
func hasWarnWithFix(r doctor.Result) bool {
	for _, c := range r.Checks {
		if c.Status == doctor.StatusWarn && c.Fix != nil {
			return true
		}
	}
	return false
}
