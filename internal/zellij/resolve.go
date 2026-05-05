package zellij

import (
	"fmt"
	"os"
	"path/filepath"
)

// LayoutKind discriminates how GenerateKDL should render a layout.
type LayoutKind int

const (
	LayoutBuiltin LayoutKind = iota
	LayoutCustom
)

// String returns a human-readable name for the kind, used in dry-run output.
func (k LayoutKind) String() string {
	switch k {
	case LayoutBuiltin:
		return "builtin"
	case LayoutCustom:
		return "custom"
	default:
		return "unknown"
	}
}

// LayoutSpec is the resolved form of a layout name. Lifecycle resolves this
// BEFORE constructing a Layout for GenerateKDL.
type LayoutSpec struct {
	Name string     // canonical name; empty falls back to "focus"
	Kind LayoutKind // Builtin or Custom
	Path string     // host path to the .kdl file, set when Kind == Custom
}

// builtinNames is the authoritative list of built-in layout names.
// Order is the menu order shown in error messages.
var builtinNames = []string{"focus", "reviewer", "auditor"}

// IsBuiltin reports whether name is one of the built-in layouts.
func IsBuiltin(name string) bool {
	for _, b := range builtinNames {
		if b == name {
			return true
		}
	}
	return false
}

// Resolve takes a layout name and the user's home directory and returns a
// LayoutSpec. Empty name falls back to "focus".
//
// Resolution order:
//  1. "" → "focus" (fallback).
//  2. Built-in → LayoutBuiltin with Name set.
//  3. ~/.config/agentbox/layouts/<name>.kdl exists → LayoutCustom.
//  4. Otherwise → error naming both the built-in list and the custom path tried.
func Resolve(name, hostHome string) (LayoutSpec, error) {
	if name == "" {
		name = "focus"
	}
	if IsBuiltin(name) {
		return LayoutSpec{Name: name, Kind: LayoutBuiltin}, nil
	}
	customPath := filepath.Join(hostHome, ".config", "agentbox", "layouts", name+".kdl")
	if _, err := os.Stat(customPath); err == nil {
		return LayoutSpec{Name: name, Kind: LayoutCustom, Path: customPath}, nil
	} else if !os.IsNotExist(err) {
		return LayoutSpec{}, fmt.Errorf("layout %q: stat %s: %w", name, customPath, err)
	}
	return LayoutSpec{}, fmt.Errorf(
		"layout %q not found: not a built-in (%v) and no file at %s",
		name, builtinNames, customPath,
	)
}
