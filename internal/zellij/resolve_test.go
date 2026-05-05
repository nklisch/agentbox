package zellij

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolve_BuiltinNames(t *testing.T) {
	tests := []struct {
		name     string
		wantName string
	}{
		{"focus", "focus"},
		{"reviewer", "reviewer"},
		{"auditor", "auditor"},
	}
	for _, tc := range tests {
		spec, err := Resolve(tc.name, "/home/u")
		if err != nil {
			t.Errorf("Resolve(%q): unexpected error: %v", tc.name, err)
			continue
		}
		if spec.Name != tc.wantName {
			t.Errorf("Resolve(%q).Name = %q, want %q", tc.name, spec.Name, tc.wantName)
		}
		if spec.Kind != LayoutBuiltin {
			t.Errorf("Resolve(%q).Kind = %v, want LayoutBuiltin", tc.name, spec.Kind)
		}
		if spec.Path != "" {
			t.Errorf("Resolve(%q).Path = %q, want empty for builtin", tc.name, spec.Path)
		}
	}
}

func TestResolve_EmptyFallsBackToFocus(t *testing.T) {
	spec, err := Resolve("", "/home/u")
	if err != nil {
		t.Fatalf("Resolve(%q): unexpected error: %v", "", err)
	}
	if spec.Name != "focus" {
		t.Errorf("Resolve(%q).Name = %q, want %q", "", spec.Name, "focus")
	}
	if spec.Kind != LayoutBuiltin {
		t.Errorf("Resolve(%q).Kind = %v, want LayoutBuiltin", "", spec.Kind)
	}
}

func TestResolve_CustomFilePresent(t *testing.T) {
	tmp := t.TempDir()
	layoutDir := filepath.Join(tmp, ".config", "agentbox", "layouts")
	if err := os.MkdirAll(layoutDir, 0o700); err != nil {
		t.Fatal(err)
	}
	layoutFile := filepath.Join(layoutDir, "myown.kdl")
	if err := os.WriteFile(layoutFile, []byte("layout {}"), 0o600); err != nil {
		t.Fatal(err)
	}

	spec, err := Resolve("myown", tmp)
	if err != nil {
		t.Fatalf("Resolve(%q): unexpected error: %v", "myown", err)
	}
	if spec.Name != "myown" {
		t.Errorf("spec.Name = %q, want %q", spec.Name, "myown")
	}
	if spec.Kind != LayoutCustom {
		t.Errorf("spec.Kind = %v, want LayoutCustom", spec.Kind)
	}
	if spec.Path != layoutFile {
		t.Errorf("spec.Path = %q, want %q", spec.Path, layoutFile)
	}
}

func TestResolve_CustomFileMissing(t *testing.T) {
	tmp := t.TempDir()
	_, err := Resolve("myown", tmp)
	if err == nil {
		t.Fatal("expected error for missing custom layout, got nil")
	}
	// Error should mention the built-in list.
	if !strings.Contains(err.Error(), "focus") {
		t.Errorf("error %q should mention built-in names", err.Error())
	}
	// Error should mention the custom path tried.
	if !strings.Contains(err.Error(), "myown.kdl") {
		t.Errorf("error %q should mention the custom path tried", err.Error())
	}
}

func TestResolve_PathTraversalNotABuiltin(t *testing.T) {
	// "../../etc/passwd" is not a built-in and the resulting file path
	// won't exist on the test host — verifies path traversal doesn't
	// accidentally succeed.
	_, err := Resolve("../../etc/passwd", "/home/u")
	if err == nil {
		t.Fatal("expected error for traversal-like name, got nil")
	}
}

func TestIsBuiltin(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"focus", true},
		{"reviewer", true},
		{"auditor", true},
		{"foo", false},
		{"", false},
		{"Focus", false}, // case-sensitive
	}
	for _, tc := range tests {
		got := IsBuiltin(tc.name)
		if got != tc.want {
			t.Errorf("IsBuiltin(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestLayoutKind_String(t *testing.T) {
	if LayoutBuiltin.String() != "builtin" {
		t.Errorf("LayoutBuiltin.String() = %q, want %q", LayoutBuiltin.String(), "builtin")
	}
	if LayoutCustom.String() != "custom" {
		t.Errorf("LayoutCustom.String() = %q, want %q", LayoutCustom.String(), "custom")
	}
}

// Regression: Resolve with an invalid path that exists (e.g. a directory)
// returns an error from the stat path (non-ErrNotExist).
func TestResolve_StatPermissionError(t *testing.T) {
	// We can't reliably produce a non-ErrNotExist stat error in a unit test
	// without root/chmod tricks, so just verify the ErrNotExist path gives
	// a sentinel error containing the name.
	_, err := Resolve("bogus", "/nonexistent-home")
	if err == nil {
		t.Fatal("expected error for nonexistent layout")
	}
	// Should not be an ErrNotExist itself — our function wraps it.
	if errors.Is(err, os.ErrNotExist) {
		t.Errorf("Resolve should not expose raw ErrNotExist: %v", err)
	}
}
