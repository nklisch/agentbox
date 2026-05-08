package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Paths holds the resolved global+project config paths.
type Paths struct {
	Global  string
	Project string
}

// DefaultPaths returns the standard paths:
//
//	Global  = $XDG_CONFIG_HOME/agentbox/config.toml (or ~/.config/agentbox/...)
//	Project = $PWD/.agentbox.toml
//
// $XDG_CONFIG_HOME is honored on all platforms when set. This allows tests
// (and Linux deployments) to override the config dir via the XDG standard.
func DefaultPaths() (Paths, error) {
	var cfgDir string
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		cfgDir = xdg
	} else {
		var err error
		cfgDir, err = os.UserConfigDir()
		if err != nil {
			return Paths{}, fmt.Errorf("resolve config dir: %w", err)
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve cwd: %w", err)
	}
	return Paths{
		Global:  filepath.Join(cfgDir, "agentbox", "config.toml"),
		Project: filepath.Join(cwd, ".agentbox.toml"),
	}, nil
}

// Load reads (in order) defaults → global → project, overlaying each on the
// previous via toml.Decode. A missing file is not an error; a malformed file
// is. Returns the merged config plus the metadata for caller-visible
// "was this set in a file" checks.
func Load(p Paths) (Config, error) {
	cfg := DefaultConfig()
	if err := decodeIfExists(p.Global, &cfg); err != nil {
		return cfg, fmt.Errorf("global config: %w", err)
	}
	if p.Project != "" {
		if err := decodeIfExists(p.Project, &cfg); err != nil {
			return cfg, fmt.Errorf("project config: %w", err)
		}
	}
	return cfg, nil
}

func decodeIfExists(path string, cfg *Config) error {
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := toml.NewDecoder(f).Decode(cfg); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

