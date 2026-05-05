package state

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// Dir returns the agentbox data directory: $XDG_DATA_HOME/agentbox or
// ~/.local/share/agentbox.
func Dir() (string, error) {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "agentbox"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "agentbox"), nil
}

// SessionDir returns the per-project session directory under Dir().
func SessionDir(projectID string) (string, error) {
	base, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "sessions", projectID), nil
}

// EnsureDir mkdir -p's path with mode 0700.
func EnsureDir(path string) error {
	return os.MkdirAll(path, 0o700)
}

// IsWritable returns true if a file can be created in path. Path must exist
// and be a directory; otherwise returns false.
func IsWritable(path string) bool {
	f, err := os.CreateTemp(path, ".agentbox-write-check-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

// EnsureSession creates the session dir for projectID with mode 0700 and
// touches the files that are bind-mounted into the box (so podman can mount them
// as rw/ro even before they have real content).
func EnsureSession(projectID string) (string, error) {
	dir, err := SessionDir(projectID)
	if err != nil {
		return "", err
	}
	if err := EnsureDir(dir); err != nil {
		return "", err
	}
	// Create the saved/ subdir so the bind mount source exists before podman starts.
	if err := EnsureDir(filepath.Join(dir, "saved")); err != nil {
		return "", err
	}
	// Touch files that are bind-mounted by runspec; podman fails to mount a
	// non-existent source even for ro mounts.
	for _, name := range []string{"history", "layout.kdl", "effective-config.toml"} {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); errors.Is(err, fs.ErrNotExist) {
			if err := os.WriteFile(p, nil, 0o600); err != nil {
				return "", err
			}
		}
	}
	return dir, nil
}

// RemoveSession deletes the session dir for projectID. Idempotent: missing
// dir is not an error.
func RemoveSession(projectID string) error {
	dir, err := SessionDir(projectID)
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

// WriteEffectiveConfig writes the merged config to <state-dir>/sessions/<id>/effective-config.toml.
// The file is mounted ro into the box at /etc/agentbox/config.toml.
func WriteEffectiveConfig(projectID string, body []byte) error {
	dir, err := SessionDir(projectID)
	if err != nil {
		return err
	}
	if err := EnsureDir(dir); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "effective-config.toml"), body, 0o600)
}
