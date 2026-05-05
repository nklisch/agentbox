package kits

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nklisch/agentbox/internal/state"
)

// CacheEntry is the JSON written to ~/.local/share/agentbox/cache/kits/<tag>.json.
type CacheEntry struct {
	Tag             string            `json:"tag"`
	Kits            []string          `json:"kits"`
	KitHashes       map[string]string `json:"kit_hashes"`
	BuiltAt         time.Time         `json:"built_at"`
	AgentboxVersion string            `json:"agentbox_version"`
}

// Cache is the kit-image cache. Each entry is a pair of files:
// <tag>.json (the CacheEntry) and <tag>.Dockerfile (the generated Dockerfile).
// Tags containing "/" are sanitised to "_" for filesystem safety.
type Cache struct {
	Dir string // ~/.local/share/agentbox/cache/kits
}

// NewCache returns a Cache rooted at <state-dir>/cache/kits and ensures
// the directory exists.
func NewCache() (*Cache, error) {
	base, err := state.Dir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(base, "cache", "kits")
	if err := state.EnsureDir(dir); err != nil {
		return nil, fmt.Errorf("ensure cache dir %s: %w", dir, err)
	}
	return &Cache{Dir: dir}, nil
}

// HashesFromResolved computes per-kit content hashes for a resolved kit list.
func HashesFromResolved(res Resolved) (map[string]string, error) {
	out := make(map[string]string, len(res.Kits))
	for _, k := range res.Kits {
		h, err := HashKit(k)
		if err != nil {
			return nil, err
		}
		out[k.Manifest.Name] = h
	}
	return out, nil
}

// Lookup reads <tag>.json. Returns (CacheEntry{}, fs.ErrNotExist) if absent.
func (c *Cache) Lookup(tag string) (CacheEntry, error) {
	p := c.path(tag, ".json")
	b, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return CacheEntry{}, fs.ErrNotExist
	}
	if err != nil {
		return CacheEntry{}, fmt.Errorf("read cache %s: %w", p, err)
	}
	var entry CacheEntry
	if err := json.Unmarshal(b, &entry); err != nil {
		return CacheEntry{}, fmt.Errorf("parse cache %s: %w", p, err)
	}
	return entry, nil
}

// Save writes both <tag>.json and <tag>.Dockerfile atomically.
func (c *Cache) Save(entry CacheEntry, dockerfile string) error {
	if err := state.EnsureDir(c.Dir); err != nil {
		return err
	}
	jsonBytes, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return err
	}
	if err := writeAtomic(c.path(entry.Tag, ".json"), jsonBytes, 0o644); err != nil {
		return err
	}
	return writeAtomic(c.path(entry.Tag, ".Dockerfile"), []byte(dockerfile), 0o644)
}

// HasMatch returns true if a cache entry exists for res.Tag and every
// kit's content hash matches the on-disk record.
func (c *Cache) HasMatch(res Resolved) (bool, error) {
	entry, err := c.Lookup(res.Tag)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	current, err := HashesFromResolved(res)
	if err != nil {
		return false, err
	}
	if len(current) != len(entry.KitHashes) {
		return false, nil
	}
	for k, h := range current {
		if entry.KitHashes[k] != h {
			return false, nil
		}
	}
	return true, nil
}

// ListEntries returns every <tag>.json entry the cache knows about.
// Entries that can't be parsed are silently skipped.
func (c *Cache) ListEntries() ([]CacheEntry, error) {
	entries, err := os.ReadDir(c.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []CacheEntry
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		// Derive the tag from the filename by reversing the "_" → "/" substitution.
		// The tag is stored inside the JSON as well — prefer that over filename mangling.
		tag := strings.TrimSuffix(e.Name(), ".json")
		entry, err := c.Lookup(tag)
		if err != nil {
			continue // skip unreadable entries; don't fail the whole list
		}
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Tag < out[j].Tag })
	return out, nil
}

// Remove deletes <tag>.json and <tag>.Dockerfile from the cache. The
// associated podman image is not touched (Builder.Prune handles that).
func (c *Cache) Remove(tag string) error {
	for _, ext := range []string{".json", ".Dockerfile"} {
		p := c.path(tag, ext)
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// path returns the cache path for (tag, ext). The tag may contain "/"
// (e.g., "agentbox/abc123") so we replace "/" with "_" for filesystem safety.
func (c *Cache) path(tag, ext string) string {
	safe := strings.ReplaceAll(tag, "/", "_")
	return filepath.Join(c.Dir, safe+ext)
}

// writeAtomic writes data to a temp file in the same directory as path, then
// renames it into place. This ensures readers never see a partial write.
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	dir, base := filepath.Split(path)
	tmp, err := os.CreateTemp(dir, base+".tmp-*")
	if err != nil {
		return err
	}
	// Clean up the temp file if anything goes wrong before the rename.
	defer func() { _ = os.Remove(tmp.Name()) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
