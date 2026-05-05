package kits

import "io/fs"

// Manifest mirrors manifest.toml.
type Manifest struct {
	Name            string   `toml:"name"`
	Description     string   `toml:"description"`
	DependsOn       []string `toml:"depends_on"`       // nil → defaulted to ["base"] for non-"base" kits
	AgentboxVersion string   `toml:"agentbox_version"` // optional semver constraint; checked at resolve time
	Provides        []string `toml:"provides"`
	ConflictsWith   []string `toml:"conflicts_with"`
}

// Kit is a loaded kit: manifest + access to the kit's directory.
type Kit struct {
	Manifest Manifest
	FS       fs.FS  // rooted at the kit's own dir; reading "manifest.toml" works
	Source   string // "builtin" or "user:<abspath>"
}

// KitInfo is the lightweight description used by `agentbox build --list`.
type KitInfo struct {
	Name        string
	Description string
	Source      string // "builtin" | "user"
}

// Resolved is the output of dependency resolution: a topologically-sorted,
// deduped list of Kits, plus the canonical image tag.
type Resolved struct {
	Kits []Kit  // base first, then in topological order
	Tag  string // from runspec.KitImageTag(Names())
}

// Names returns the kit names of a Resolved in resolution order.
func (r Resolved) Names() []string {
	out := make([]string, len(r.Kits))
	for i, k := range r.Kits {
		out[i] = k.Manifest.Name
	}
	return out
}
