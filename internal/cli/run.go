package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/nklisch/agentbox/internal/exitcode"
	"github.com/nklisch/agentbox/internal/lifecycle"
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

			if global.DryRun {
				return runDryRun(cmd, res, args, kitsFlag, networkFlag)
			}

			l, err := newLifecycle(cfg)
			if err != nil {
				return exitcode.Wrap(exitcode.Generic, err)
			}
			// Redirect lifecycle output through cobra's writer so tests can capture it.
			l.Stdout = cmd.OutOrStdout()
			l.Stderr = cmd.ErrOrStderr()

			opts := lifecycle.RunOpts{
				Fresh:   fresh,
				Attach:  !noAttach,
				Network: networkFlag,
			}
			if len(args) == 1 {
				opts.Agent = args[0]
			}
			if kitsFlag != "" {
				opts.Kits = strings.Split(kitsFlag, ",")
			}
			_ = detachOnExit // P3 doesn't auto-stop on exit; future phase.
			return l.Run(opts)
		},
	}
	cmd.Flags().BoolVar(&fresh, "fresh", false, "remove any existing box for this project before creating")
	cmd.Flags().StringVar(&kitsFlag, "kits", "", "override the kit list (comma-separated)")
	cmd.Flags().StringVar(&networkFlag, "network", "", "override network.mode for this run")
	cmd.Flags().BoolVar(&noAttach, "no-attach", false, "create/start the box but don't attach")
	cmd.Flags().BoolVar(&detachOnExit, "detach-on-exit", false, "stop the container when the agent process exits")
	return cmd
}

// runDryRun preserves Phase 1's dry-run behavior. Extracted so RunE stays clean.
func runDryRun(cmd *cobra.Command, cfg configResult, args []string, kitsFlag, networkFlag string) error {
	// Apply per-command overrides.
	c := cfg.Config
	if networkFlag != "" {
		c.Network.Mode = networkFlag
		if err := c.Validate(); err != nil {
			return exitcode.Wrap(exitcode.InvalidArgs, err)
		}
	}

	agent := c.DefaultAgent
	if len(args) == 1 {
		agent = args[0]
	}
	a, ok := c.Agents[agent]
	if !ok {
		return exitcode.New(exitcode.InvalidArgs, "agent %q not defined in [agents.*]", agent)
	}

	kitList := a.Kits
	if kitsFlag != "" {
		kitList = strings.Split(kitsFlag, ",")
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
		Kits:        kitList,
		HomeDir:     home,
		StateDir:    stateDir,
		Created:     time.Now(),
	}

	rs, err := runspec.BuildPodmanCreateArgs(c, in)
	if err != nil {
		return exitcode.Wrap(exitcode.Generic, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "# project_id = %s\n", id)
	fmt.Fprintf(cmd.OutOrStdout(), "# kits = %s\n", strings.Join(kitList, ","))
	fmt.Fprintf(cmd.OutOrStdout(), "# network = %s\n", c.Network.Mode)
	fmt.Fprint(cmd.OutOrStdout(), rs.ToShell(c.Runtime))
	return nil
}
