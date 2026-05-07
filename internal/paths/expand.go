// Package paths provides path manipulation helpers shared by lifecycle,
// runspec, and state. Lower-level than runspec; never imports lifecycle or cli.
package paths

import "strings"

// ExpandHome replaces a leading "~" in s with homeDir. Empty homeDir is a
// no-op (returns s unchanged). Other path components are not interpreted.
//
// Examples:
//
//	ExpandHome("~/.claude", "/home/foo") → "/home/foo/.claude"
//	ExpandHome("~",         "/home/foo") → "/home/foo"
//	ExpandHome("/abs/path", "/home/foo") → "/abs/path"
//	ExpandHome("~/x",       "")          → "~/x"
func ExpandHome(s, homeDir string) string {
	if homeDir == "" {
		return s
	}
	if strings.HasPrefix(s, "~/") {
		return homeDir + s[1:]
	}
	if s == "~" {
		return homeDir
	}
	return s
}
