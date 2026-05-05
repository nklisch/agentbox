package kits

import (
	"errors"
	"fmt"
	"io/fs"
	"regexp"

	"github.com/BurntSushi/toml"
)

// requiredFiles lists the four files that every kit directory must contain.
var requiredFiles = []string{"manifest.toml", "packages.txt", "install.sh", "env.sh"}

// nameRE matches valid kit names: must start with a lowercase alphanumeric
// character and contain only [a-z0-9_-].
var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// Load loads a kit from an fs.FS rooted at the kit's directory.
// Verifies the four required files exist; parses + validates manifest.toml;
// applies the depends_on default (["base"] for non-"base" kits).
func Load(name string, kitFS fs.FS, source string) (Kit, error) {
	if !nameRE.MatchString(name) {
		return Kit{}, fmt.Errorf("invalid kit name %q (must match [a-z0-9_-]+)", name)
	}

	for _, f := range requiredFiles {
		if _, err := fs.Stat(kitFS, f); err != nil {
			return Kit{}, fmt.Errorf("kit %q: missing required file %s", name, f)
		}
	}

	mf, err := readManifest(kitFS)
	if err != nil {
		return Kit{}, fmt.Errorf("kit %q: %w", name, err)
	}

	// If the manifest omits name, default to the directory name.
	// If present, it must agree (catches "dir renamed but manifest wasn't updated").
	if mf.Name == "" {
		mf.Name = name
	}
	if mf.Name != name {
		return Kit{}, fmt.Errorf("kit %q: manifest name %q does not match directory name", name, mf.Name)
	}
	if mf.Description == "" {
		return Kit{}, fmt.Errorf("kit %q: manifest is missing description", name)
	}

	// depends_on default: ["base"] except for "base" itself.
	if mf.DependsOn == nil && name != "base" {
		mf.DependsOn = []string{"base"}
	}
	if name == "base" && len(mf.DependsOn) > 0 {
		return Kit{}, errors.New(`kit "base": depends_on must be empty`)
	}

	return Kit{Manifest: mf, FS: kitFS, Source: source}, nil
}

func readManifest(kitFS fs.FS) (Manifest, error) {
	b, err := fs.ReadFile(kitFS, "manifest.toml")
	if err != nil {
		return Manifest{}, fmt.Errorf("read manifest: %w", err)
	}
	var mf Manifest
	if _, err := toml.Decode(string(b), &mf); err != nil {
		return Manifest{}, fmt.Errorf("parse manifest: %w", err)
	}
	return mf, nil
}
