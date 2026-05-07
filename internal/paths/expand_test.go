package paths_test

import (
	"testing"

	"github.com/nklisch/agentbox/internal/paths"
)

func TestExpandHome(t *testing.T) {
	tests := []struct {
		name    string
		s       string
		homeDir string
		want    string
	}{
		{"tilde prefix", "~/.claude", "/home/foo", "/home/foo/.claude"},
		{"tilde only", "~", "/home/foo", "/home/foo"},
		{"absolute path unchanged", "/abs/path", "/home/foo", "/abs/path"},
		{"relative path unchanged", "rel/path", "/home/foo", "rel/path"},
		{"empty homeDir no-op tilde-prefix", "~/x", "", "~/x"},
		{"empty homeDir no-op tilde", "~", "", "~"},
		{"empty string", "", "/home/foo", ""},
		{"tilde prefix deep", "~/a/b/c", "/home/bar", "/home/bar/a/b/c"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := paths.ExpandHome(tt.s, tt.homeDir)
			if got != tt.want {
				t.Errorf("ExpandHome(%q, %q) = %q, want %q", tt.s, tt.homeDir, got, tt.want)
			}
		})
	}
}
