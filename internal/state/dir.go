package state

import (
	"errors"
	"fmt"
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

// EnsureFile stat()s path; if the file does not exist, creates it as an
// empty regular file with mode 0o600. Returns nil on success or if the file
// already existed. Wraps unexpected errors (permission, parent-dir-missing).
//
// Used wherever a host path must exist as a regular file before a podman
// bind-mount references it — podman silently creates missing bind sources
// as directories, which corrupts the mount.
func EnsureFile(path string) error {
	info, err := os.Stat(path)
	if err == nil {
		if info.IsDir() {
			return fmt.Errorf("ensure %s: exists but is a directory", path)
		}
		return nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	return nil
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
	if err := EnsureDir(SavedDirPath(dir)); err != nil {
		return "", err
	}
	// Touch files that are bind-mounted by runspec; podman fails to mount a
	// non-existent source even for ro mounts.
	for _, p := range []string{HistoryPath(dir), LayoutPath(dir), EffectiveConfigPath(dir)} {
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

// Per-session file paths under <sessionDir>. Use these helpers rather than
// inlining the filenames so all references update together.
func HistoryPath(sessionDir string) string         { return filepath.Join(sessionDir, "history") }
func LayoutPath(sessionDir string) string          { return filepath.Join(sessionDir, "layout.kdl") }
func EffectiveConfigPath(sessionDir string) string { return filepath.Join(sessionDir, "effective-config.toml") }
func SavedDirPath(sessionDir string) string        { return filepath.Join(sessionDir, "saved") }
func TrailPath(sessionDir string) string           { return filepath.Join(sessionDir, "trail.jsonl") }
func ClaudeSettingsPath(sessionDir string) string  { return filepath.Join(sessionDir, "claude-settings.json") }
func CorefilePath(sessionDir string) string        { return filepath.Join(sessionDir, "Corefile") }

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
	return os.WriteFile(EffectiveConfigPath(dir), body, 0o600)
}
