package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"github.com/nklisch/agentbox/internal/builtinkits"
	"github.com/nklisch/agentbox/internal/config"
	"github.com/nklisch/agentbox/internal/container"
	"github.com/nklisch/agentbox/internal/exitcode"
	"github.com/nklisch/agentbox/internal/kits"
	"github.com/nklisch/agentbox/internal/lifecycle"
	"github.com/nklisch/agentbox/internal/network"
	"github.com/nklisch/agentbox/internal/version"
)

// initLifecycleCmd is the standard prelude for every cobra command that
// uses lifecycle. It loads config, builds a Lifecycle wired to the cobra
// command's stdio, and propagates --quiet. Use it in RunE:
//
//	RunE: func(cmd *cobra.Command, args []string) error {
//		l, _, err := initLifecycleCmd(cmd)
//		if err != nil { return err }
//		return l.Run(...)
//	}
//
// The second return value (configResult) lets callers reach res.Config or
// res.Paths when needed (rare — most commands only need the Lifecycle).
func initLifecycleCmd(cmd *cobra.Command) (*lifecycle.Lifecycle, configResult, error) {
	res, err := loadConfig()
	if err != nil {
		return nil, configResult{}, err
	}
	l, err := newLifecycle(res.Config)
	if err != nil {
		return nil, configResult{}, exitcode.Wrap(exitcode.Generic, err)
	}
	l.Stdout = cmd.OutOrStdout()
	l.Stderr = cmd.ErrOrStderr()
	l.Quiet = global.Quiet
	return l, res, nil
}

// newLifecycle is overridable at test time so the CLI can use a fakeRuntime
// without invoking podman. Tests override this package-level var.
var newLifecycle = func(cfg config.Config) (*lifecycle.Lifecycle, error) {
	home, _ := os.UserHomeDir()

	var rt container.Runtime
	switch cfg.Runtime {
	case "docker":
		rt = container.NewDockerRuntime()
	default: // podman is the default; also handles explicit "podman"
		rt = container.NewPodmanRuntime(cfg.Runtime)
	}

	cfgDir, _ := os.UserConfigDir()
	userKitsDir := filepath.Join(cfgDir, "agentbox", "kits")
	reg := kits.NewRegistry(builtinkits.FS(), userKitsDir)
	cache, err := kits.NewCache()
	if err != nil {
		return nil, err
	}
	timeout, _ := time.ParseDuration(cfg.Registry.PullTimeout) // Validate() already accepted it
	builder := &kits.Builder{
		Registry:        reg,
		Cache:           cache,
		Runner:          kits.NewPodmanRunner(cfg.Runtime),
		Version:         version.Version,
		RegistryEnabled: cfg.Registry.Enabled,
		RegistryHost:    cfg.Registry.Host,
		RegistryVerify:  cfg.Registry.Verify,
		PullTimeout:     timeout,
	}
	// Determine whether to use sudo for iptables/ipset.
	// On macOS, ipset/iptables enforcement is not available; Sudo is irrelevant
	// (IpsetEnabled() returns false on darwin). On Linux, use sudo unless already root.
	sudoIPTables := runtime.GOOS != "darwin" && os.Getuid() != 0

	return &lifecycle.Lifecycle{
		Cfg:     cfg,
		Runtime: rt,
		Builder: builder,
		Network: &network.Manager{
			Runtime:      rt,
			IPTables:     &network.IPTables{Sudo: sudoIPTables},
			NetfilterBin: "agentbox-netfilter",
			Stderr:       os.Stderr,
		},
		Home:   home,
		Quiet:  global.Quiet,
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}, nil
}
