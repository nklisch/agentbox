package config

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
)

// segmentRE matches the allowed shape of a single dotted-path segment.
// Schema fields and free-form map keys (agent names, env-var names) all
// fit. Dots inside keys are not supported; users with unusual keys use
// `agentbox config edit`.
var segmentRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

// Sentinel errors returned by path operations.
var (
	ErrEmptyPath    = errors.New("empty path")
	ErrBadSegment   = errors.New("invalid path segment")
	ErrPathConflict = errors.New("path traverses non-map value")
)

// ParsePath splits a dotted path into segments and validates each one.
// Returns ErrEmptyPath for "" and ErrBadSegment (wrapped) for invalid segments.
func ParsePath(path string) ([]string, error) {
	if path == "" {
		return nil, ErrEmptyPath
	}
	segs := strings.Split(path, ".")
	for _, s := range segs {
		if !segmentRE.MatchString(s) {
			return nil, fmt.Errorf("%w: %q", ErrBadSegment, s)
		}
	}
	return segs, nil
}

// Get looks up the value at path in doc. Returns (value, true) if found,
// (nil, false) if the path doesn't exist or if any intermediate node is not
// a map[string]any.
func Get(doc map[string]any, path []string) (any, bool) {
	var cur any = doc
	for _, seg := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[seg]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// Set writes value at path in doc, creating intermediate map[string]any
// sections as needed. Returns ErrPathConflict if any intermediate segment
// is occupied by a non-map value.
func Set(doc map[string]any, path []string, value any) error {
	if len(path) == 0 {
		return ErrEmptyPath
	}
	cur := doc
	for _, seg := range path[:len(path)-1] {
		v, ok := cur[seg]
		if !ok || v == nil {
			next := map[string]any{}
			cur[seg] = next
			cur = next
		} else if next, ok := v.(map[string]any); ok {
			cur = next
		} else {
			return fmt.Errorf("%w at %q", ErrPathConflict, seg)
		}
	}
	cur[path[len(path)-1]] = value
	return nil
}

// Unset removes the leaf at path. Returns true if anything was removed.
// After removal, any ancestor maps that became empty are pruned bottom-up.
func Unset(doc map[string]any, path []string) bool {
	if len(path) == 0 {
		return false
	}

	// ancestors[i] = the map that contains path[i] as a key.
	type frame struct {
		m   map[string]any
		key string
	}
	ancestors := make([]frame, len(path))
	cur := doc
	for i, seg := range path {
		ancestors[i] = frame{m: cur, key: seg}
		if i < len(path)-1 {
			v, ok := cur[seg]
			if !ok {
				return false
			}
			next, ok := v.(map[string]any)
			if !ok {
				return false
			}
			cur = next
		}
	}

	// Check the leaf exists.
	leaf := ancestors[len(path)-1]
	if _, ok := leaf.m[leaf.key]; !ok {
		return false
	}
	delete(leaf.m, leaf.key)

	// Prune empty ancestor maps bottom-up.
	// ancestors[i].m is the map that holds ancestors[i].key.
	// ancestors[i].m is stored at ancestors[i-1].m[ancestors[i-1].key].
	for i := len(ancestors) - 1; i > 0; i-- {
		if len(ancestors[i].m) > 0 {
			break
		}
		parent := ancestors[i-1]
		delete(parent.m, parent.key)
	}
	return true
}

// AppendUnique appends value to the slice at path, creating the slice if
// absent. No-op if value is already present (reflect.DeepEqual). Accepts
// both []any and []string stored at the leaf (TOML decodes homogeneous
// string arrays as []interface{} when targeting map[string]any). Returns
// ErrPathConflict if the leaf exists but isn't a slice.
func AppendUnique(doc map[string]any, path []string, value any) error {
	existing, _ := Get(doc, path)
	slice, err := toAnySlice(existing)
	if err != nil {
		return err
	}
	for _, elem := range slice {
		if reflect.DeepEqual(elem, value) {
			return nil // already present
		}
	}
	return Set(doc, path, append(slice, value))
}

// RemoveValue removes the first occurrence of value from the slice at path.
// Returns (true, nil) if something was removed, (false, nil) if the value
// wasn't present or the path didn't exist. Returns ErrPathConflict if the
// leaf exists but isn't a slice.
func RemoveValue(doc map[string]any, path []string, value any) (bool, error) {
	existing, ok := Get(doc, path)
	if !ok {
		return false, nil
	}
	slice, err := toAnySlice(existing)
	if err != nil {
		return false, err
	}
	for i, elem := range slice {
		if reflect.DeepEqual(elem, value) {
			updated := append(slice[:i:i], slice[i+1:]...)
			return true, Set(doc, path, updated)
		}
	}
	return false, nil
}

// toAnySlice coerces a nil, []any, or []string into []any.
// Returns ErrPathConflict if the value is a non-nil, non-slice type.
func toAnySlice(v any) ([]any, error) {
	if v == nil {
		return []any{}, nil
	}
	switch s := v.(type) {
	case []any:
		return s, nil
	case []string:
		out := make([]any, len(s))
		for i, e := range s {
			out[i] = e
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%w: expected slice, got %T", ErrPathConflict, v)
	}
}
