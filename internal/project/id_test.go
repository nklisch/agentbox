package project_test

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/nklisch/agentbox/internal/project"
)

// knownFixture is the expected 12-char sha1 prefix of "/tmp/abx-test".
// Verified with: printf '/tmp/abx-test' | sha1sum
// Result: ef38f6eb38e0399d1ed4d5cffa352a58c9fc19e4
// First 12 chars: ef38f6eb38e0
const knownFixture = "ef38f6eb38e0"

func TestIDFromPath_KnownVector(t *testing.T) {
	got := project.IDFromPath("/tmp/abx-test")
	if got != knownFixture {
		t.Errorf("IDFromPath(%q) = %q, want %q", "/tmp/abx-test", got, knownFixture)
	}
}

func TestIDFromPath_Deterministic(t *testing.T) {
	path := "/home/user/myproject"
	got1 := project.IDFromPath(path)
	got2 := project.IDFromPath(path)
	if got1 != got2 {
		t.Errorf("IDFromPath not deterministic: %q != %q", got1, got2)
	}
}

func TestIDFromPath_Format(t *testing.T) {
	id := project.IDFromPath("/some/random/path")
	re := regexp.MustCompile(`^[a-f0-9]{12}$`)
	if !re.MatchString(id) {
		t.Errorf("IDFromPath() = %q, does not match ^[a-f0-9]{12}$", id)
	}
}

func TestIDFromPath_DifferentPaths(t *testing.T) {
	id1 := project.IDFromPath("/path/to/project1")
	id2 := project.IDFromPath("/path/to/project2")
	if id1 == id2 {
		t.Error("different paths should produce different IDs")
	}
}

func TestContainerName(t *testing.T) {
	got := project.ContainerName("abc123")
	want := "agentbox-abc123"
	if got != want {
		t.Errorf("ContainerName(%q) = %q, want %q", "abc123", got, want)
	}
}

func TestNetworkName(t *testing.T) {
	got := project.NetworkName("abc123")
	want := "agentbox-net-abc123"
	if got != want {
		t.Errorf("NetworkName(%q) = %q, want %q", "abc123", got, want)
	}
}

func TestResolve_SymlinkResolution(t *testing.T) {
	// Create a real dir and a symlink pointing to it.
	tmp := t.TempDir()
	realDir := filepath.Join(tmp, "real")
	if err := os.MkdirAll(realDir, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tmp, "link")
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatal(err)
	}

	// Change to the symlink dir and resolve.
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(link); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(orig) }()

	_, abs, err := project.Resolve()
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}

	// The abs should be the real path, not the symlink path.
	resolved, err := filepath.EvalSymlinks(realDir)
	if err != nil {
		t.Fatal(err)
	}
	if abs != resolved {
		t.Errorf("Resolve() abs = %q, want %q (symlink resolved)", abs, resolved)
	}
}

func TestResolve_ReturnsValidID(t *testing.T) {
	id, _, err := project.Resolve()
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	re := regexp.MustCompile(`^[a-f0-9]{12}$`)
	if !re.MatchString(id) {
		t.Errorf("Resolve() id = %q, does not match ^[a-f0-9]{12}$", id)
	}
}
