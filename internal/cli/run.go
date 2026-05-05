package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/nklisch/agentbox/internal/exitcode"
	"github.com/nklisch/agentbox/internal/project"
	"github.com/nklisch/agentbox/internal/runspec"
	"github.com/nklisch/agentbox/internal/state"
)

func newRunCmd() *cobra.Command {
	var (
		fresh        bool
		kitsFlag     string
		networkFlag  string
		noAttach     bool
		detachOnExit bool
	)
	cmd := &cobra.Command{
		Use:   "run [agent]",
		Short: "Create or attach to the per-project box, launch the configured agent",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := loadConfig()
			if err != nil {
				return err
			}
			cfg := res.Config

			// Apply per-command overrides.
			if networkFlag != "" {
				cfg.Network.Mode = networkFlag
				if err := cfg.Validate(); err != nil {
					return exitcode.Wrap(exitcode.InvalidArgs, err)
				}
			}

			agent := cfg.DefaultAgent
			if len(args) == 1 {
				agent = args[0]
			}
			a, ok := cfg.Agents[agent]
			if !ok {
				return exitcode.New(exitcode.InvalidArgs, "agent %q not defined in [agents.*]", agent)
			}

			kits := a.Kits
			if kitsFlag != "" {
				kits = strings.Split(kitsFlag, ",")
			}

			id, abs, err := project.Resolve()
			if err != nil {
				return exitcode.Wrap(exitcode.Generic, err)
			}

			home, _ := os.UserHomeDir()
			stateDir, err := state.SessionDir(id)
			if err != nil {
				return exitcode.Wrap(exitcode.Generic, err)
			}

			in := runspec.BuildInput{
				ProjectID:   id,
				ProjectAbs:  abs,
				ProjectName: filepath.Base(abs),
				Agent:       agent,
				Kits:        kits,
				HomeDir:     home,
				StateDir:    stateDir,
				Created:     time.Now(),
			}

			if global.DryRun {
				rs, err := runspec.BuildPodmanCreateArgs(cfg, in)
				if err != nil {
					return exitcode.Wrap(exitcode.Generic, err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "# project_id = %s\n", id)
				fmt.Fprintf(cmd.OutOrStdout(), "# kits = %s\n", strings.Join(kits, ","))
				fmt.Fprintf(cmd.OutOrStdout(), "# network = %s\n", cfg.Network.Mode)
				fmt.Fprint(cmd.OutOrStdout(), rs.ToShell(cfg.Runtime))
				_ = fresh
				_ = noAttach
				_ = detachOnExit
				return nil
			}
			return notImplementedRunE(cmd)
		},
	}
	cmd.Flags().BoolVar(&fresh, "fresh", false, "remove any existing box for this project before creating")
	cmd.Flags().StringVar(&kitsFlag, "kits", "", "override the kit list (comma-separated)")
	cmd.Flags().StringVar(&networkFlag, "network", "", "override network.mode for this run")
	cmd.Flags().BoolVar(&noAttach, "no-attach", false, "create/start the box but don't attach")
	cmd.Flags().BoolVar(&detachOnExit, "detach-on-exit", false, "stop the container when the agent process exits")
	return cmd
}
