package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestEditFile_EmptyDoc(t *testing.T) {
	// Non-existent file → empty map, noop mutate → encodes without error.
	path := filepath.Join(t.TempDir(), "nonexistent.toml")
	body, err := EditFile(path, func(doc map[string]any) error {
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = body // just assert no error
}

func TestEditFile_ReadsMutatesEncodes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("runtime = \"podman\"\n[network]\nmode = \"safe\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	body, err := EditFile(path, func(doc map[string]any) error {
		return Set(doc, []string{"runtime"}, "docker")
	})
	if err != nil {
		t.Fatalf("EditFile: %v", err)
	}

	// Decode the output bytes into a Config to verify the mutation.
	cfg := DefaultConfig()
	if _, err := toml.NewDecoder(bytes.NewReader(body)).Decode(&cfg); err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if cfg.Runtime != "docker" {
		t.Errorf("runtime = %q, want docker", cfg.Runtime)
	}
	if cfg.Network.Mode != "safe" {
		t.Errorf("network.mode = %q, want safe (unchanged)", cfg.Network.Mode)
	}
}

func TestEditFile_MutationError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(path, []byte{}, 0o600)

	wantErr := fmt.Errorf("mutation failed")
	_, err := EditFile(path, func(doc map[string]any) error { return wantErr })
	if !errors.Is(err, wantErr) {
		t.Errorf("expected mutation error, got %v", err)
	}
}

func TestWriteAtomic_WritesContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := []byte("runtime = \"podman\"\n")
	if err := WriteAtomic(path, body); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Errorf("content mismatch: got %q, want %q", got, body)
	}
}

func TestWriteAtomic_Mode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	WriteAtomic(path, []byte("x=1\n"))
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %04o, want 0600", perm)
	}
}

func TestWriteAtomic_CreatesParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.toml")
	if err := WriteAtomic(path, []byte("x=1\n")); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file not created: %v", err)
	}
}

func TestWriteAtomic_ReplacesExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(path, []byte("old\n"), 0o600)
	WriteAtomic(path, []byte("new\n"))
	got, _ := os.ReadFile(path)
	if string(got) != "new\n" {
		t.Errorf("got %q, want \"new\\n\"", got)
	}
}

func TestLoadProposed_SubstitutesGlobal(t *testing.T) {
	dir := t.TempDir()
	p := Paths{
		Global:  filepath.Join(dir, "config.toml"),
		Project: filepath.Join(dir, "project.toml"),
	}
	// Project file on disk overrides network mode.
	os.WriteFile(p.Project, []byte("[network]\nmode = \"open\"\n"), 0o600)

	// Proposed global sets runtime=docker.
	cfg, err := LoadProposed(p, p.Global, []byte("runtime = \"docker\"\n"))
	if err != nil {
		t.Fatalf("LoadProposed: %v", err)
	}
	if cfg.Runtime != "docker" {
		t.Errorf("runtime = %q, want docker", cfg.Runtime)
	}
	// Project layer should also be applied.
	if cfg.Network.Mode != "open" {
		t.Errorf("network.mode = %q, want open", cfg.Network.Mode)
	}
}

func TestLoadProposed_PathMismatch(t *testing.T) {
	p := Paths{Global: "/a", Project: "/b"}
	_, err := LoadProposed(p, "/c", nil)
	if !errors.Is(err, ErrPropPathMismatch) {
		t.Errorf("expected ErrPropPathMismatch, got %v", err)
	}
}

func TestLoadProposed_BadTOML(t *testing.T) {
	dir := t.TempDir()
	p := Paths{Global: filepath.Join(dir, "g.toml"), Project: ""}
	_, err := LoadProposed(p, p.Global, []byte("not: valid: toml!!!"))
	if err == nil {
		t.Error("expected error for invalid TOML body")
	}
}

func TestLoadProposed_EmptyBody(t *testing.T) {
	dir := t.TempDir()
	p := Paths{Global: filepath.Join(dir, "g.toml"), Project: ""}
	// Empty body → treat as no file, returns defaults.
	cfg, err := LoadProposed(p, p.Global, nil)
	if err != nil {
		t.Fatalf("LoadProposed: %v", err)
	}
	def := DefaultConfig()
	if cfg.Runtime != def.Runtime {
		t.Errorf("runtime = %q, want %q (default)", cfg.Runtime, def.Runtime)
	}
}
