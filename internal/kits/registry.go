package kits

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
)

// Registry resolves kit names to Kits, sourcing from a built-in fs.FS first
// and a user kit directory second. User kits with the same name as a
// built-in shadow the built-in.
type Registry struct {
	builtin fs.FS  // rooted at the dir containing kit subdirs (e.g., the embed FS subbed at "kits")
	userDir string // absolute path to ~/.config/agentbox/kits, or "" if missing
}

// NewRegistry constructs a Registry. `userDir` may be "" — the registry
// gracefully omits it. If userDir is set but doesn't exist on disk, that's
// not an error (the user just doesn't have any user-authored kits yet).
func NewRegistry(builtin fs.FS, userDir string) *Registry {
	return &Registry{builtin: builtin, userDir: userDir}
}

// List returns every kit name known to the registry, alphabetically sorted.
// User-shadowed names appear once.
func (r *Registry) List() ([]string, error) {
	seen := make(map[string]struct{})

	if r.builtin != nil {
		entries, err := fs.ReadDir(r.builtin, ".")
		if err != nil {
			return nil, fmt.Errorf("list builtin kits: %w", err)
		}
		for _, e := range entries {
			if e.IsDir() {
				seen[e.Name()] = struct{}{}
			}
		}
	}

	if r.userDir != "" {
		entries, err := os.ReadDir(r.userDir)
		if err == nil {
			for _, e := range entries {
				if e.IsDir() {
					seen[e.Name()] = struct{}{}
				}
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("list user kits: %w", err)
		}
	}

	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out, nil
}

// Get loads the named kit. User kits shadow built-ins. Returns an error
// (wrapping fs.ErrNotExist) if the kit is unknown.
func (r *Registry) Get(name string) (Kit, error) {
	if r.userDir != "" {
		dir := r.userDir + string(os.PathSeparator) + name
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return Load(name, os.DirFS(dir), "user:"+dir)
		}
	}
	if r.builtin != nil {
		sub, err := fs.Sub(r.builtin, name)
		if err == nil {
			if _, statErr := fs.Stat(sub, "manifest.toml"); statErr == nil {
				return Load(name, sub, "builtin")
			}
		}
	}
	return Kit{}, fmt.Errorf("kit %q: %w", name, fs.ErrNotExist)
}

// Describe returns name + description + source for every known kit, in
// alphabetical order. Used by `agentbox build --list`.
func (r *Registry) Describe() ([]KitInfo, error) {
	names, err := r.List()
	if err != nil {
		return nil, err
	}
	out := make([]KitInfo, 0, len(names))
	for _, n := range names {
		k, err := r.Get(n)
		if err != nil {
			// Skip kits that fail to load — tolerate broken user kits without
			// failing the whole list.
			out = append(out, KitInfo{Name: n, Description: "(load error: " + err.Error() + ")", Source: "?"})
			continue
		}
		src := "builtin"
		if k.Source != "builtin" {
			src = "user"
		}
		out = append(out, KitInfo{Name: n, Description: k.Manifest.Description, Source: src})
	}
	return out, nil
}
