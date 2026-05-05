package config

import (
	"errors"
	"testing"
)

func TestParsePath(t *testing.T) {
	cases := []struct {
		in      string
		want    []string
		wantErr error
	}{
		{"a.b.c", []string{"a", "b", "c"}, nil},
		{"a", []string{"a"}, nil},
		{"network.safe.block_direct_ip", []string{"network", "safe", "block_direct_ip"}, nil},
		{"agents.claude-1.cmd", []string{"agents", "claude-1", "cmd"}, nil},
		{"_foo._bar", []string{"_foo", "_bar"}, nil},
		{"", nil, ErrEmptyPath},
		{"a..b", nil, ErrBadSegment},
		{"a.1invalid", nil, ErrBadSegment},
		{"a.b.has space", nil, ErrBadSegment},
		{"a.b.has.dot.ok", []string{"a", "b", "has", "dot", "ok"}, nil},
	}
	for _, c := range cases {
		got, err := ParsePath(c.in)
		if !errors.Is(err, c.wantErr) {
			t.Errorf("ParsePath(%q): err = %v, want %v", c.in, err, c.wantErr)
			continue
		}
		if c.wantErr != nil {
			continue
		}
		if len(got) != len(c.want) {
			t.Errorf("ParsePath(%q): got %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("ParsePath(%q)[%d]: got %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

func TestGet(t *testing.T) {
	doc := map[string]any{
		"a": map[string]any{
			"b": map[string]any{
				"c": "found",
			},
			"x": 42,
		},
		"top": "level",
	}

	got, ok := Get(doc, []string{"a", "b", "c"})
	if !ok || got != "found" {
		t.Errorf("Get a.b.c: got (%v, %v), want (found, true)", got, ok)
	}
	got, ok = Get(doc, []string{"top"})
	if !ok || got != "level" {
		t.Errorf("Get top: got (%v, %v), want (level, true)", got, ok)
	}
	_, ok = Get(doc, []string{"missing"})
	if ok {
		t.Error("Get missing: expected false")
	}
	_, ok = Get(doc, []string{"a", "b", "c", "too_deep"})
	if ok {
		t.Error("Get too deep: expected false")
	}
	_, ok = Get(doc, []string{"a", "x", "not_a_map"})
	if ok {
		t.Error("Get through scalar: expected false")
	}
	// Empty path returns the doc itself (which is a valid any).
	got, ok = Get(doc, []string{})
	if !ok {
		t.Error("Get empty path: expected true (returns root)")
	}
	_ = got
}

func TestSet(t *testing.T) {
	t.Run("creates_nested", func(t *testing.T) {
		doc := map[string]any{}
		if err := Set(doc, []string{"a", "b", "c"}, "v"); err != nil {
			t.Fatal(err)
		}
		got, ok := Get(doc, []string{"a", "b", "c"})
		if !ok || got != "v" {
			t.Errorf("got %v, want v", got)
		}
	})
	t.Run("overwrites_leaf", func(t *testing.T) {
		doc := map[string]any{"a": map[string]any{"b": "old"}}
		if err := Set(doc, []string{"a", "b"}, "new"); err != nil {
			t.Fatal(err)
		}
		got, _ := Get(doc, []string{"a", "b"})
		if got != "new" {
			t.Errorf("got %v, want new", got)
		}
	})
	t.Run("conflict", func(t *testing.T) {
		doc := map[string]any{"a": "string_not_map"}
		err := Set(doc, []string{"a", "b"}, "v")
		if !errors.Is(err, ErrPathConflict) {
			t.Errorf("expected ErrPathConflict, got %v", err)
		}
	})
	t.Run("empty_path", func(t *testing.T) {
		doc := map[string]any{}
		if err := Set(doc, []string{}, "v"); !errors.Is(err, ErrEmptyPath) {
			t.Errorf("expected ErrEmptyPath, got %v", err)
		}
	})
}

func TestUnset(t *testing.T) {
	t.Run("removes_leaf_and_prunes", func(t *testing.T) {
		doc := map[string]any{
			"a": map[string]any{
				"b": map[string]any{
					"c": "v",
				},
			},
		}
		removed := Unset(doc, []string{"a", "b", "c"})
		if !removed {
			t.Fatal("expected true")
		}
		if len(doc) != 0 {
			t.Errorf("expected empty doc after full prune, got %v", doc)
		}
	})
	t.Run("prunes_only_empty", func(t *testing.T) {
		doc := map[string]any{
			"a": map[string]any{
				"b": map[string]any{
					"c": "v",
					"d": "x",
				},
			},
		}
		Unset(doc, []string{"a", "b", "c"})
		// b.d should remain; a.b should remain.
		got, ok := Get(doc, []string{"a", "b", "d"})
		if !ok || got != "x" {
			t.Errorf("sibling d should remain: got %v, ok=%v", got, ok)
		}
	})
	t.Run("missing_path", func(t *testing.T) {
		doc := map[string]any{"x": "y"}
		removed := Unset(doc, []string{"missing"})
		if removed {
			t.Error("expected false for missing key")
		}
		if len(doc) != 1 {
			t.Error("doc should be unchanged")
		}
	})
	t.Run("empty_path", func(t *testing.T) {
		doc := map[string]any{"x": "y"}
		removed := Unset(doc, []string{})
		if removed {
			t.Error("expected false for empty path")
		}
	})
	t.Run("through_non_map", func(t *testing.T) {
		doc := map[string]any{"a": "string"}
		removed := Unset(doc, []string{"a", "b"})
		if removed {
			t.Error("expected false when intermediate is scalar")
		}
	})
}

func TestAppendUnique(t *testing.T) {
	t.Run("creates_on_empty_doc", func(t *testing.T) {
		doc := map[string]any{}
		if err := AppendUnique(doc, []string{"k"}, "x"); err != nil {
			t.Fatal(err)
		}
		v, _ := Get(doc, []string{"k"})
		sl, ok := v.([]any)
		if !ok || len(sl) != 1 || sl[0] != "x" {
			t.Errorf("expected [x], got %v", v)
		}
	})
	t.Run("idempotent", func(t *testing.T) {
		doc := map[string]any{}
		AppendUnique(doc, []string{"k"}, "x")
		AppendUnique(doc, []string{"k"}, "x")
		v, _ := Get(doc, []string{"k"})
		sl := v.([]any)
		if len(sl) != 1 {
			t.Errorf("expected 1 element, got %d", len(sl))
		}
	})
	t.Run("appends_to_string_slice", func(t *testing.T) {
		doc := map[string]any{"k": []string{"a", "b"}}
		if err := AppendUnique(doc, []string{"k"}, "c"); err != nil {
			t.Fatal(err)
		}
		v, _ := Get(doc, []string{"k"})
		sl := v.([]any)
		if len(sl) != 3 {
			t.Errorf("expected 3 elements, got %d: %v", len(sl), sl)
		}
	})
	t.Run("conflict", func(t *testing.T) {
		doc := map[string]any{"k": "not_a_slice"}
		if err := AppendUnique(doc, []string{"k"}, "v"); !errors.Is(err, ErrPathConflict) {
			t.Errorf("expected ErrPathConflict, got %v", err)
		}
	})
}

func TestRemoveValue(t *testing.T) {
	t.Run("removes_first_occurrence", func(t *testing.T) {
		doc := map[string]any{"k": []any{"a", "x", "b", "x"}}
		removed, err := RemoveValue(doc, []string{"k"}, "x")
		if err != nil || !removed {
			t.Fatalf("expected (true, nil), got (%v, %v)", removed, err)
		}
		v, _ := Get(doc, []string{"k"})
		sl := v.([]any)
		if len(sl) != 3 || sl[1] != "b" {
			t.Errorf("expected [a b x], got %v", sl)
		}
	})
	t.Run("not_present", func(t *testing.T) {
		doc := map[string]any{"k": []any{"a", "b"}}
		removed, err := RemoveValue(doc, []string{"k"}, "x")
		if err != nil || removed {
			t.Errorf("expected (false, nil), got (%v, %v)", removed, err)
		}
	})
	t.Run("missing_path", func(t *testing.T) {
		doc := map[string]any{}
		removed, err := RemoveValue(doc, []string{"k"}, "x")
		if err != nil || removed {
			t.Errorf("expected (false, nil), got (%v, %v)", removed, err)
		}
	})
	t.Run("conflict", func(t *testing.T) {
		doc := map[string]any{"k": "not_a_slice"}
		_, err := RemoveValue(doc, []string{"k"}, "x")
		if !errors.Is(err, ErrPathConflict) {
			t.Errorf("expected ErrPathConflict, got %v", err)
		}
	})
}
