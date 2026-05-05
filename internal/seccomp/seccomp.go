package seccomp

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nklisch/agentbox/internal/state"
)

//go:embed containers.json
var containersJSON []byte

// ContainersJSONBytes returns the embedded seccomp profile as bytes.
// Tests use this directly to verify the profile is parseable JSON.
func ContainersJSONBytes() []byte {
	return containersJSON
}

// EnsureContainersProfile writes the embedded containers seccomp JSON to a
// stable host path and returns that path. Idempotent: writes only when the
// file is missing or its content differs from the embedded version.
//
// The host path is <state-dir>/seccomp/containers.json so it lives alongside
// other agentbox state. The path is bind-mounted ro into the box at
// /etc/agentbox/seccomp/containers.json. SecOpt references the in-container path.
func EnsureContainersProfile() (string, error) {
	base, err := state.Dir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "seccomp")
	if err := state.EnsureDir(dir); err != nil {
		return "", fmt.Errorf("ensure seccomp dir: %w", err)
	}
	path := filepath.Join(dir, "containers.json")
	if needsWrite(path, containersJSON) {
		if err := os.WriteFile(path, containersJSON, 0o644); err != nil {
			return "", fmt.Errorf("write %s: %w", path, err)
		}
	}
	return path, nil
}

// needsWrite returns true when path is missing or differs from want.
func needsWrite(path string, want []byte) bool {
	have, err := os.ReadFile(path)
	if err != nil {
		return true
	}
	if len(have) != len(want) {
		return true
	}
	for i := range have {
		if have[i] != want[i] {
			return true
		}
	}
	return false
}
