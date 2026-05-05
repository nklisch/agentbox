package state

import (
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
