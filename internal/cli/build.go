package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nklisch/agentbox/internal/builtinkits"
	"github.com/nklisch/agentbox/internal/exitcode"
	"github.com/nklisch/agentbox/internal/kits"
	"github.com/nklisch/agentbox/internal/version"
)

func newBuildCmd() *cobra.Command {
	var (
		noCache   bool
		printOnly bool
		listOnly  bool
		prune     bool
	)
	cmd := &cobra.Command{
		Use:   "build [kit_list]",
		Short: "Build (or rebuild) a composed kit image",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := loadConfig()
			if err != nil {
				return err
			}

			b, err := newBuilder(res.Config.Runtime)
			if err != nil {
				return err
			}

			switch {
			case listOnly:
				return runBuildList(cmd, b)
			case prune:
				return runBuildPrune(cmd, b)
			case printOnly:
				return runBuildPrint(cmd, b, kitList(args, res.Config.DefaultKits))
			default:
				return runBuildBuild(cmd, b, kitList(args, res.Config.DefaultKits), noCache)
			}
		},
	}
	cmd.Flags().BoolVar(&noCache, "no-cache", false, "force a full rebuild (skips agentbox + podman caches)")
	cmd.Flags().BoolVar(&printOnly, "print", false, "print the generated Dockerfile to stdout; do not build")
	cmd.Flags().BoolVar(&listOnly, "list", false, "list all known kits and exit")
	cmd.Flags().BoolVar(&prune, "prune", false, "remove kit images not referenced by any current box")
	cmd.MarkFlagsMutuallyExclusive("print", "list", "prune")
	return cmd
}

// newBuilder wires the registry, cache, and runner into a Builder.
// userKitsDir is empty string if unresolvable — non-fatal; the user just
// hasn't authored any custom kits.
func newBuilder(runtimeBin string) (*kits.Builder, error) {
	userKitsDir, _ := userKitsDirPath()
	reg := kits.NewRegistry(builtinkits.FS(), userKitsDir)
	cache, err := kits.NewCache()
	if err != nil {
		return nil, exitcode.Wrap(exitcode.Generic, err)
	}
	return &kits.Builder{
		Registry: reg,
		Cache:    cache,
		Runner:   kits.NewPodmanRunner(runtimeBin),
		Version:  version.Version,
	}, nil
}

// userKitsDirPath returns the path to the user's custom kit directory
// (~/.config/agentbox/kits). Returns ("", err) if the config dir is
// unresolvable — callers treat this as non-fatal.
func userKitsDirPath() (string, error) {
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfgDir, "agentbox", "kits"), nil
}

// kitList resolves the kit list to build: the CLI arg (comma-split) if
// provided, otherwise the configured default_kits.
func kitList(args []string, defaultKits []string) []string {
	if len(args) == 1 {
		return strings.Split(args[0], ",")
	}
	return defaultKits
}

func runBuildList(cmd *cobra.Command, b *kits.Builder) error {
	infos, err := b.Registry.Describe()
	if err != nil {
		return exitcode.Wrap(exitcode.KitBuild, err)
	}
	for _, ki := range infos {
		fmt.Fprintf(cmd.OutOrStdout(), "%-12s %s  (%s)\n", ki.Name, ki.Description, ki.Source)
	}
	return nil
}

func runBuildPrint(cmd *cobra.Command, b *kits.Builder, requested []string) error {
	if len(requested) == 0 {
		return exitcode.New(exitcode.InvalidArgs, "no kits requested and default_kits is empty")
	}
	df, err := b.PrintDockerfile(requested)
	if err != nil {
		return exitcode.Wrap(exitcode.KitBuild, err)
	}
	fmt.Fprint(cmd.OutOrStdout(), df)
	return nil
}

func runBuildPrune(cmd *cobra.Command, b *kits.Builder) error {
	res, err := b.Prune()
	if err != nil {
		return exitcode.Wrap(exitcode.KitBuild, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "removed %d image(s); kept %d\n", len(res.Removed), len(res.Kept))
	for _, t := range res.Removed {
		fmt.Fprintf(cmd.OutOrStdout(), "  removed: %s\n", t)
	}
	return nil
}

func runBuildBuild(cmd *cobra.Command, b *kits.Builder, requested []string, noCache bool) error {
	if len(requested) == 0 {
		return exitcode.New(exitcode.InvalidArgs, "no kits requested and default_kits is empty")
	}
	res, err := b.Build(requested, kits.BuildOpts{
		NoCache: noCache,
		Stdout:  cmd.OutOrStdout(),
		Stderr:  cmd.ErrOrStderr(),
	})
	if err != nil {
		return exitcode.Wrap(exitcode.KitBuild, err)
	}
	if res.CacheHit {
		fmt.Fprintf(cmd.OutOrStdout(), "cache hit: %s (kits: %s)\n", res.Tag, strings.Join(res.Kits, ","))
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "built: %s (kits: %s)\n", res.Tag, strings.Join(res.Kits, ","))
	return nil
}
