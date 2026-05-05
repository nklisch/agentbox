// export_test.go exposes internal test seams for the external cli_test package.
package cli

import (
	"github.com/nklisch/agentbox/internal/config"
	"github.com/nklisch/agentbox/internal/lifecycle"
)

// SetLifecycleFactory replaces the newLifecycle factory used by CLI commands
// with a test double. Returns a restore function the caller should defer.
func SetLifecycleFactory(fn func(config.Config) (*lifecycle.Lifecycle, error)) func() {
	orig := newLifecycle
	newLifecycle = fn
	return func() { newLifecycle = orig }
}

// NewLifecycle exposes the real newLifecycle factory for type-dispatch tests.
func NewLifecycle(cfg config.Config) (*lifecycle.Lifecycle, error) {
	return newLifecycle(cfg)
}
