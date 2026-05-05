package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/nklisch/agentbox/internal/exitcode"
	"github.com/nklisch/agentbox/internal/lifecycle"
)

func newExecCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "exec <project_id> <command> [args...]",
		Short: "Run a one-off command inside a live box",
		// DisableFlagParsing passes all args raw so that command flags like
		// `sh -c 'echo hi'` are not interpreted as agentbox flags.
		// We manually parse our own flags (-i, -t, -w, --shell) from os.Args.
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, rawArgs []string) error {
			// Manually parse agentbox-exec flags from rawArgs.
			// Flags: -i/--interactive, -t/--tty, -w/--workdir <val>, --shell
			// Everything after the first non-flag positional arg that isn't
			// one of our flags is passed through as the command + args.
			var (
				interactive bool
				tty         bool
				workdir     string
				useShell    bool
				positional  []string
			)
			for i := 0; i < len(rawArgs); i++ {
				a := rawArgs[i]
				switch {
				case a == "-i" || a == "--interactive":
					interactive = true
				case a == "-t" || a == "--tty":
					tty = true
				case a == "--shell":
					useShell = true
				case a == "-w" || a == "--workdir":
					if i+1 < len(rawArgs) {
						i++
						workdir = rawArgs[i]
					}
				case strings.HasPrefix(a, "--workdir="):
					workdir = strings.TrimPrefix(a, "--workdir=")
				case strings.HasPrefix(a, "-w="):
					workdir = strings.TrimPrefix(a, "-w=")
				case a == "--help" || a == "-h":
					return cmd.Help()
				default:
					// First non-flag arg and everything after becomes positional.
					positional = append(positional, rawArgs[i:]...)
					i = len(rawArgs)
				}
			}

			if len(positional) < 2 {
				return exitcode.New(exitcode.InvalidArgs,
					"exec requires <project_id> <command> [args...], got %d arg(s)", len(positional))
			}

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
			return l.Exec(lifecycle.ExecOpts{
				Input:       positional[0],
				Argv:        positional[1:],
				Workdir:     workdir,
				Interactive: interactive,
				TTY:         tty,
				UseShell:    useShell,
			})
		},
	}
	return cmd
}
