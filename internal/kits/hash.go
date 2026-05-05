package kits

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"sort"
)

// HashKit computes a stable content hash for a kit's directory. The hash
// covers every regular file in the kit FS: relative path + NUL separator +
// file bytes + NUL separator. Files are processed in sorted-path order so
// the result is deterministic regardless of filesystem enumeration order.
//
// Used for cache invalidation: change any file in a kit, the hash changes,
// the cache misses, the image rebuilds.
//
// Note: file mode is not hashed — embed.FS doesn't preserve it anyway.
func HashKit(kit Kit) (string, error) {
	h := sha1.New()

	var paths []string
	err := fs.WalkDir(kit.FS, ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("walk kit %q: %w", kit.Manifest.Name, err)
	}
	sort.Strings(paths)

	for _, p := range paths {
		f, err := kit.FS.Open(p)
		if err != nil {
			return "", fmt.Errorf("open %s: %w", p, err)
		}
		// Write path + NUL separator to prevent boundary ambiguity.
		fmt.Fprintf(h, "%s\x00", p)
		if _, err := io.Copy(h, f); err != nil {
			_ = f.Close()
			return "", fmt.Errorf("read %s: %w", p, err)
		}
		_ = f.Close()
		// NUL after content to separate from the next path.
		h.Write([]byte{0})
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}
