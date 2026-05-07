package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nklisch/agentbox/internal/exitcode"
	"github.com/nklisch/agentbox/internal/kits"
	"github.com/nklisch/agentbox/internal/runspec"
)

// runBuildDryRun resolves the kit list and prints the commands the live
// runBuildBuild would invoke, without touching the cache, the registry,
// or the build context on disk.
//
// Output structure:
//
//	# kits = polyglot,claude
//	# resolved = base,polyglot,claude
//	# tag = agentbox/<sha[:12]>
//	# remote = ghcr.io/.../agentbox-kits:<version>-<sha[:12]> (when pull-eligible)
//	podman pull ghcr.io/.../agentbox-kits:<version>-<sha[:12]>
//	podman tag <remote> agentbox/<sha[:12]>
//	# OR (when pull skipped or ineligible):
//	podman build -t agentbox/<sha[:12]> -f -
//	# Dockerfile generated in-process (use --print to inspect)
//
// Prefer printing the pull commands when registry+kit list is pull-eligible
// (matching the live ordering: pull first, fall back to build). When pull is
// disabled (--no-pull, registry.enabled=false, or any user kit in the list),
// print only the build command.
func runBuildDryRun(cmd *cobra.Command, b *kits.Builder, requested []string, _, noPull bool) error {
	if len(requested) == 0 {
		return exitcode.New(exitcode.InvalidArgs, "no kits requested and default_kits is empty")
	}

	res, err := b.Resolve(requested)
	if err != nil {
		return exitcode.Wrap(exitcode.KitBuild, err)
	}

	rt := b.Runner.Bin()

	fmt.Fprintf(cmd.OutOrStdout(), "# kits = %s\n", strings.Join(requested, ","))
	if !sameSlice(requested, res.Names()) {
		fmt.Fprintf(cmd.OutOrStdout(), "# resolved = %s\n", strings.Join(res.Names(), ","))
	}
	fmt.Fprintf(cmd.OutOrStdout(), "# tag = %s\n", res.Tag)

	if kits.PullEligible(b, res, noPull) {
		remoteRef := runspec.RemoteImageRef(b.RegistryHost, b.Version, res.Names())
		fmt.Fprintf(cmd.OutOrStdout(), "# remote = %s\n", remoteRef)
		fmt.Fprintf(cmd.OutOrStdout(), "%s pull %s\n", rt, remoteRef)
		fmt.Fprintf(cmd.OutOrStdout(), "%s tag %s %s\n", rt, remoteRef, res.Tag)
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "%s build -t %s -f -\n", rt, res.Tag)
		fmt.Fprintf(cmd.OutOrStdout(), "# Dockerfile generated in-process (use --print to inspect)\n")
	}
	return nil
}
