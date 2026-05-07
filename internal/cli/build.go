package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/nklisch/agentbox/internal/builtinkits"
	"github.com/nklisch/agentbox/internal/config"
	"github.com/nklisch/agentbox/internal/exitcode"
	"github.com/nklisch/agentbox/internal/kits"
	"github.com/nklisch/agentbox/internal/version"
)

func newBuildCmd() *cobra.Command {
	var (
		noCache     bool
		noPull      bool
		printOnly   bool
		printTag    bool
		listOnly    bool
		prune       bool
		emitContext string
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

			b, err := newBuilder(res.Config)
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
			case printTag:
				return runBuildPrintTag(cmd, b, kitList(args, res.Config.DefaultKits))
			case emitContext != "":
				return runBuildEmitContext(cmd, b, kitList(args, res.Config.DefaultKits), emitContext)
			default:
				return runBuildBuild(cmd, b, kitList(args, res.Config.DefaultKits), noCache, noPull)
			}
		},
	}
	cmd.Flags().BoolVar(&noCache, "no-cache", false, "force a full rebuild (skips agentbox + podman caches)")
	cmd.Flags().BoolVar(&noPull, "no-pull", false, "skip the registry pull attempt; build locally")
	cmd.Flags().BoolVar(&printOnly, "print", false, "print the generated Dockerfile to stdout; do not build")
	cmd.Flags().BoolVar(&printTag, "print-tag", false, "print the resolved image tag (agentbox/<sha>) to stdout; do not build")
	cmd.Flags().BoolVar(&listOnly, "list", false, "list all known kits and exit")
	cmd.Flags().BoolVar(&prune, "prune", false, "remove kit images not referenced by any current box")
	cmd.Flags().StringVar(&emitContext, "emit-context", "",
		"stage the build context (Dockerfile + kit dirs) to <dir> instead of building")
	cmd.MarkFlagsMutuallyExclusive("print", "list", "prune", "emit-context", "print-tag")
	return cmd
}

// newBuilder wires the registry, cache, and runner into a Builder.
// userKitsDir is empty string if unresolvable — non-fatal; the user just
// hasn't authored any custom kits.
func newBuilder(cfg config.Config) (*kits.Builder, error) {
	userKitsDir, _ := userKitsDirPath()
	reg := kits.NewRegistry(builtinkits.FS(), userKitsDir)
	cache, err := kits.NewCache()
	if err != nil {
		return nil, exitcode.Wrap(exitcode.Generic, err)
	}
	timeout, _ := time.ParseDuration(cfg.Registry.PullTimeout) // Validate() already accepted it
	return &kits.Builder{
		Registry:        reg,
		Cache:           cache,
		Runner:          kits.NewPodmanRunner(cfg.Runtime),
		Version:         version.Version,
		RegistryEnabled: cfg.Registry.Enabled,
		RegistryHost:    cfg.Registry.Host,
		RegistryVerify:  cfg.Registry.Verify,
		PullTimeout:     timeout,
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

// runBuildPrintTag prints only the resolved image tag (agentbox/<sha12>), one
// line, no trailing content. Used by CI to compute the version-pinned GHCR tag
// without grepping through the Dockerfile output.
func runBuildPrintTag(cmd *cobra.Command, b *kits.Builder, requested []string) error {
	if len(requested) == 0 {
		return exitcode.New(exitcode.InvalidArgs, "no kits requested and default_kits is empty")
	}
	tag, err := b.ResolveForTag(requested)
	if err != nil {
		return exitcode.Wrap(exitcode.KitBuild, err)
	}
	fmt.Fprintln(cmd.OutOrStdout(), tag)
	return nil
}

// runBuildEmitContext stages the build context (Dockerfile + kit dirs) to dst.
func runBuildEmitContext(cmd *cobra.Command, b *kits.Builder, requested []string, dst string) error {
	if len(requested) == 0 {
		return exitcode.New(exitcode.InvalidArgs, "no kits requested and default_kits is empty")
	}
	if err := b.EmitContext(requested, dst); err != nil {
		return exitcode.Wrap(exitcode.KitBuild, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "context emitted to %s\n", dst)
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

func runBuildBuild(cmd *cobra.Command, b *kits.Builder, requested []string, noCache, noPull bool) error {
	if len(requested) == 0 {
		return exitcode.New(exitcode.InvalidArgs, "no kits requested and default_kits is empty")
	}
	res, err := b.Build(requested, kits.BuildOpts{
		NoCache: noCache,
		NoPull:  noPull,
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
	if res.PullHit {
		fmt.Fprintf(cmd.OutOrStdout(), "pulled: %s (kits: %s)\n", res.Tag, strings.Join(res.Kits, ","))
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "built: %s (kits: %s)\n", res.Tag, strings.Join(res.Kits, ","))
	return nil
}
