package kits

import (
	"errors"
	"fmt"

	"github.com/nklisch/agentbox/internal/runspec"
)

// Resolve walks depends_on starting from `requested`, topo-sorts, dedupes,
// and conflict-checks. Returns the resolved kit list and image tag.
//
// The DFS post-order naturally produces a topological ordering with "base"
// first (it has no deps and is visited before its dependents). runspec.KitImageTag
// is the single source of truth for the tag formula — we don't redefine it here.
func Resolve(reg *Registry, requested []string) (Resolved, error) {
	if len(requested) == 0 {
		return Resolved{}, errors.New("at least one kit must be requested")
	}

	loaded := make(map[string]Kit) // by name; also serves as "visited" after DFS completes
	visiting := make(map[string]bool)
	order := make([]string, 0, 8) // post-order names

	var visit func(name string, path []string) error
	visit = func(name string, path []string) error {
		if _, ok := loaded[name]; ok {
			return nil // already resolved — deduplication
		}
		if visiting[name] {
			// Cycle detected; reconstruct the cycle path for a useful error message.
			cycle := append(append([]string{}, path...), name)
			return fmt.Errorf("dependency cycle: %v", cycle)
		}
		visiting[name] = true

		k, err := reg.Get(name)
		if err != nil {
			return err // wraps fs.ErrNotExist for unknown kits
		}
		// Recurse into dependencies first (DFS post-order).
		// "base" has no deps (Load enforces that); all other kits have at least ["base"].
		for _, dep := range k.Manifest.DependsOn {
			if err := visit(dep, append(path, name)); err != nil {
				return err
			}
		}

		visiting[name] = false
		loaded[name] = k
		order = append(order, name)
		return nil
	}

	for _, name := range requested {
		if err := visit(name, nil); err != nil {
			return Resolved{}, err
		}
	}

	// Conflict check: for each kit, verify nothing in conflicts_with appears
	// in the resolved set. This runs after the full set is known.
	for _, n := range order {
		k := loaded[n]
		for _, c := range k.Manifest.ConflictsWith {
			if _, present := loaded[c]; present {
				return Resolved{}, fmt.Errorf("kits %q and %q conflict (per %q.conflicts_with)", n, c, n)
			}
		}
	}

	kitsList := make([]Kit, len(order))
	for i, n := range order {
		kitsList[i] = loaded[n]
	}
	return Resolved{
		Kits: kitsList,
		Tag:  runspec.KitImageTag(order),
	}, nil
}
