package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Sentinel errors for save operations.
var (
	ErrNoPath           = errors.New("config: no file path")
	ErrPropPathMismatch = errors.New("config: proposed path does not match Paths.Global or Paths.Project")
)

// EditFile reads path into a map[string]any (empty map if the file doesn't
// exist), calls mutate, and re-encodes the result. Returns the proposed TOML
// bytes. Does NOT write to disk — callers validate and persist separately.
func EditFile(path string, mutate func(doc map[string]any) error) ([]byte, error) {
	if path == "" {
		return nil, ErrNoPath
	}

	doc := map[string]any{}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) > 0 {
		if _, err := toml.NewDecoder(bytes.NewReader(data)).Decode(&doc); err != nil {
			return nil, fmt.Errorf("decode %s: %w", path, err)
		}
	}

	if err := mutate(doc); err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(doc); err != nil {
		return nil, fmt.Errorf("encode config: %w", err)
	}
	return buf.Bytes(), nil
}

// WriteAtomic writes body to path via a temp file in the same directory,
// then atomically renames it over the target. Creates parent dirs (mode 0700)
// if missing. File mode is 0600. Cleans up the temp file on any write failure.
func WriteAtomic(path string, body []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create dir %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()

	// On any failure after CreateTemp, best-effort remove the temp file.
	cleanup := true
	defer func() {
		if cleanup {
			os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpName, err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("chmod %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename %s → %s: %w", tmpName, path, err)
	}
	cleanup = false // rename succeeded; nothing to clean up
	return nil
}

// LoadProposed is like Load but substitutes proposedBody for whichever file
// path matches proposedPath (p.Global or p.Project). Used by the edit
// pipeline to validate a proposed mutation against the merged config before
// persisting. Returns ErrPropPathMismatch if proposedPath matches neither.
func LoadProposed(p Paths, proposedPath string, proposedBody []byte) (Config, error) {
	if proposedPath != p.Global && proposedPath != p.Project {
		return Config{}, ErrPropPathMismatch
	}
	cfg := DefaultConfig()
	if err := overlayLayer(p.Global, proposedBody, proposedPath == p.Global, &cfg); err != nil {
		return cfg, fmt.Errorf("global config: %w", err)
	}
	if p.Project != "" {
		if err := overlayLayer(p.Project, proposedBody, proposedPath == p.Project, &cfg); err != nil {
			return cfg, fmt.Errorf("project config: %w", err)
		}
	}
	return cfg, nil
}

// overlayLayer applies one config layer. If useBody is true, it decodes
// body into cfg (skip if body is empty). Otherwise it reads from path on
// disk (skip silently if the file doesn't exist).
func overlayLayer(path string, body []byte, useBody bool, cfg *Config) error {
	if useBody {
		if len(body) == 0 {
			return nil
		}
		_, err := toml.NewDecoder(bytes.NewReader(body)).Decode(cfg)
		return err
	}
	return decodeIfExists(path, cfg)
}
