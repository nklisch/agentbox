package cli

import (
	"os"
	"path/filepath"

	"github.com/nklisch/agentbox/internal/builtinkits"
	"github.com/nklisch/agentbox/internal/config"
	"github.com/nklisch/agentbox/internal/container"
	"github.com/nklisch/agentbox/internal/kits"
	"github.com/nklisch/agentbox/internal/lifecycle"
	"github.com/nklisch/agentbox/internal/network"
	"github.com/nklisch/agentbox/internal/version"
)

// newLifecycle is overridable at test time so the CLI can use a fakeRuntime
// without invoking podman. Tests override this package-level var.
var newLifecycle = func(cfg config.Config) (*lifecycle.Lifecycle, error) {
	home, _ := os.UserHomeDir()
	rt := container.NewPodmanRuntime(cfg.Runtime)

	cfgDir, _ := os.UserConfigDir()
	userKitsDir := filepath.Join(cfgDir, "agentbox", "kits")
	reg := kits.NewRegistry(builtinkits.FS(), userKitsDir)
	cache, err := kits.NewCache()
	if err != nil {
		return nil, err
	}
	builder := &kits.Builder{
		Registry: reg,
		Cache:    cache,
		Runner:   kits.NewPodmanRunner(cfg.Runtime),
		Version:  version.Version,
	}
	return &lifecycle.Lifecycle{
		Cfg:     cfg,
		Runtime: rt,
		Builder: builder,
		Network: &network.Manager{Runtime: rt},
		Home:    home,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
	}, nil
}
