package project

import (
	"crypto/sha1"
	"encoding/hex"
	"os"
	"path/filepath"
)

// IDFromPath returns the 12-char project ID for an absolute path.
// project_id = sha1(realpath($PWD))[:12]
func IDFromPath(absPath string) string {
	h := sha1.Sum([]byte(absPath))
	return hex.EncodeToString(h[:])[:12]
}

// Resolve returns (project_id, realpath(cwd)) for the current working dir.
// Symlinks are resolved via filepath.EvalSymlinks. If the path does not exist
// after symlink resolution, returns the original cwd unmodified — useful for
// tests that operate in synthetic dirs.
func Resolve() (id string, abs string, err error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", err
	}
	abs, err = filepath.EvalSymlinks(cwd)
	if err != nil {
		// Fallback: use the unresolved cwd. Realistic only on systems where
		// EvalSymlinks fails on a real dir, which we don't expect.
		abs = cwd
	}
	return IDFromPath(abs), abs, nil
}

// ContainerName returns the canonical container name for a project_id.
func ContainerName(id string) string { return "agentbox-" + id }

// NetworkName returns the canonical podman network name for a project_id.
func NetworkName(id string) string { return "agentbox-net-" + id }
