package cli

import (
	"github.com/spf13/cobra"

	"github.com/nklisch/agentbox/internal/config"
	"github.com/nklisch/agentbox/internal/exitcode"
	"github.com/nklisch/agentbox/internal/version"
)

// GlobalFlags holds flags settable on every command. The struct is populated
// by cobra's persistent flag bindings on the root command.
type GlobalFlags struct {
	ConfigPath      string
	NoProjectConfig bool
	Runtime         string
	DryRun          bool
	JSON            bool
	Quiet           bool
	Verbose         bool
}

// global is the package-level holder. Tests use NewRootCmd() (which resets
// it) rather than reading this directly.
var global GlobalFlags

// NewRootCmd builds the cobra command tree. Each call returns a fresh tree
// so tests can build commands per-test without state leakage.
func NewRootCmd() *cobra.Command {
	global = GlobalFlags{}

	cmd := &cobra.Command{
		Use:           "agentbox",
		Short:         "Run AI coding agents inside per-project containers",
		Version:       version.String(),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	pf := cmd.PersistentFlags()
	pf.StringVar(&global.ConfigPath, "config", "", "override global config path")
	pf.BoolVar(&global.NoProjectConfig, "no-project-config", false, "ignore .agentbox.toml in $PWD")
	pf.StringVar(&global.Runtime, "runtime", "", "podman or docker (overrides config)")
	pf.BoolVar(&global.DryRun, "dry-run", false, "print equivalent shell commands; do not execute")
	pf.BoolVar(&global.JSON, "json", false, "machine-readable output where supported")
	pf.BoolVarP(&global.Quiet, "quiet", "q", false, "suppress non-error output")
	pf.BoolVarP(&global.Verbose, "verbose", "v", false, "verbose logging to stderr")

	cmd.AddCommand(
		newRunCmd(),
		newShellCmd(),
		newAttachCmd(),
		newExecCmd(),
		newLsCmd(),
		newRmCmd(),
		newBuildCmd(),
		newDoctorCmd(),
		newConfigCmd(),
		newCompletionCmd(),
	)
	return cmd
}

// Execute runs the root command with args and returns its error.
// main.go translates the error into an exit code.
func Execute(args []string) error {
	cmd := NewRootCmd()
	cmd.SetArgs(args)
	return cmd.Execute()
}

// loadConfig is a CLI-level helper that returns the merged config taking
// global flags into account: --config overrides paths.Global; --no-project-config
// blanks paths.Project; --runtime overrides cfg.Runtime.
func loadConfig() (configResult, error) {
	paths, err := config.DefaultPaths()
	if err != nil {
		return configResult{}, exitcode.Wrap(exitcode.Generic, err)
	}
	if global.ConfigPath != "" {
		paths.Global = global.ConfigPath
	}
	if global.NoProjectConfig {
		paths.Project = ""
	}
	cfg, err := config.Load(paths)
	if err != nil {
		return configResult{}, exitcode.Wrap(exitcode.InvalidArgs, err)
	}
	if global.Runtime != "" {
		cfg.Runtime = global.Runtime
	}
	if err := cfg.Validate(); err != nil {
		return configResult{}, exitcode.Wrap(exitcode.InvalidArgs, err)
	}
	return configResult{Config: cfg, Paths: paths}, nil
}

type configResult struct {
	Config config.Config
	Paths  config.Paths
}
