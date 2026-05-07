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
	"github.com/nklisch/agentbox/internal/seccomp"
	"github.com/nklisch/agentbox/internal/state"
	"github.com/nklisch/agentbox/internal/zellij"
)

func newRunCmd() *cobra.Command {
	var (
		fresh        bool
		noPull       bool
		kitsFlag     string
		networkFlag  string
		layoutFlag   string
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
				return runDryRun(cmd, res, args, kitsFlag, networkFlag, layoutFlag)
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
				NoPull:  noPull,
				Attach:  !noAttach,
				Network: networkFlag,
				Layout:  layoutFlag,
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
	cmd.Flags().BoolVar(&noPull, "no-pull", false, "skip the registry pull attempt; build locally")
	cmd.Flags().StringVar(&kitsFlag, "kits", "", "override the kit list (comma-separated)")
	cmd.Flags().StringVar(&networkFlag, "network", "", "override network.mode for this run")
	cmd.Flags().StringVar(&layoutFlag, "layout", "",
		"zellij layout name (focus|reviewer|auditor|<custom>); overrides [zellij].layout config")
	cmd.Flags().BoolVar(&noAttach, "no-attach", false, "create/start the box but don't attach")
	cmd.Flags().BoolVar(&detachOnExit, "detach-on-exit", false, "stop the container when the agent process exits")
	return cmd
}

// runDryRun preserves Phase 1's dry-run behavior. Extracted so RunE stays clean.
func runDryRun(cmd *cobra.Command, cfg configResult, args []string, kitsFlag, networkFlag, layoutFlag string) error {
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

	// If containers.enable, materialize the seccomp profile so the dry-run
	// output accurately shows the bind-mount (same path as the live path).
	var seccompPath string
	if c.Containers.Enable {
		p, err := seccomp.EnsureContainersProfile()
		if err != nil {
			return exitcode.Wrap(exitcode.Generic, err)
		}
		seccompPath = p
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
		SeccompPath: seccompPath,
	}

	rs, err := runspec.BuildPodmanCreateArgs(c, in)
	if err != nil {
		return exitcode.Wrap(exitcode.Generic, err)
	}

	// Resolve layout for dry-run output. Prefer --layout flag; fall back to
	// config value (which defaults to "focus").
	layoutName := layoutFlag
	if layoutName == "" {
		layoutName = c.Zellij.Layout
	}
	spec, err := zellij.Resolve(layoutName, home)
	if err != nil {
		return exitcode.Wrap(exitcode.InvalidArgs, err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "# project_id = %s\n", id)
	fmt.Fprintf(cmd.OutOrStdout(), "# kits = %s\n", strings.Join(kitList, ","))
	fmt.Fprintf(cmd.OutOrStdout(), "# network = %s\n", c.Network.Mode)
	if spec.Kind == zellij.LayoutCustom {
		fmt.Fprintf(cmd.OutOrStdout(), "# layout = %s (%s, %s)\n", spec.Name, spec.Kind, spec.Path)
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "# layout = %s (%s)\n", spec.Name, spec.Kind)
	}
	fmt.Fprint(cmd.OutOrStdout(), rs.ToShell(c.Runtime))
	return nil
}
