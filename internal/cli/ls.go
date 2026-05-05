package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nklisch/agentbox/internal/container"
	"github.com/nklisch/agentbox/internal/exitcode"
	"github.com/nklisch/agentbox/internal/lifecycle"
)

func newLsCmd() *cobra.Command {
	var (
		all     bool
		project string
		agentN  string
		kit     string
	)
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List boxes",
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
			boxes, err := l.Ls(lifecycle.LsFilter{
				All:     all,
				Project: project,
				Agent:   agentN,
				Kit:     kit,
			})
			if err != nil {
				return err
			}
			if global.JSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				for _, b := range boxes {
					if err := enc.Encode(b); err != nil {
						return exitcode.Wrap(exitcode.Generic, err)
					}
				}
				return nil
			}
			return printLsTable(cmd.OutOrStdout(), boxes)
		},
	}
	cmd.Flags().BoolVarP(&all, "all", "a", false, "include stopped boxes")
	cmd.Flags().StringVar(&project, "project", "", "filter by project name")
	cmd.Flags().StringVar(&agentN, "agent", "", "filter by agent")
	cmd.Flags().StringVar(&kit, "kit", "", "filter by kit membership")
	return cmd
}

func printLsTable(w io.Writer, boxes []container.Box) error {
	if len(boxes) == 0 {
		return nil
	}
	fmt.Fprintf(w, "%-12s  %-20s  %-8s  %-30s  %-9s  %s\n",
		"PROJECT_ID", "PROJECT", "AGENT", "KITS", "STATUS", "CREATED")
	for _, b := range boxes {
		fmt.Fprintf(w, "%-12s  %-20s  %-8s  %-30s  %-9s  %s\n",
			b.ProjectID,
			truncate(b.Project, 20),
			truncate(b.Agent, 8),
			truncate(strings.Join(b.Kits, ","), 30),
			string(b.Status),
			b.Created.Format("2006-01-02T15:04"))
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
