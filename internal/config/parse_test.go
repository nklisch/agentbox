package config

import (
	"testing"
)

func TestParseValue(t *testing.T) {
	cases := []struct {
		raw     string
		kind    Kind
		want    any
		wantErr bool
	}{
		// KindBool strict
		{"true", KindBool, true, false},
		{"false", KindBool, false, false},
		{"True", KindBool, nil, true},
		{"yes", KindBool, nil, true},
		{"on", KindBool, nil, true},
		{"1", KindBool, nil, true},

		// KindInt
		{"42", KindInt, int64(42), false},
		{"-7", KindInt, int64(-7), false},
		{"abc", KindInt, nil, true},
		{"3.14", KindInt, nil, true},

		// KindFloat
		{"3.14", KindFloat, float64(3.14), false},
		{"42", KindFloat, float64(42), false},
		{"abc", KindFloat, nil, true},

		// KindString
		{"hello", KindString, "hello", false},
		{"", KindString, "", false},
		{"a,b,c", KindString, "a,b,c", false}, // comma NOT split for KindString

		// KindStringList
		{"polyglot,claude", KindStringList, []string{"polyglot", "claude"}, false},
		{"polyglot, claude ", KindStringList, []string{"polyglot", "claude"}, false},
		{"single", KindStringList, []string{"single"}, false},
		{"", KindStringList, []string{}, false},

		// KindUnknown: best-effort
		{"true", KindUnknown, true, false},
		{"false", KindUnknown, false, false},
		{"99", KindUnknown, int64(99), false},
		{"1.5", KindUnknown, float64(1.5), false},
		{"a,b", KindUnknown, []string{"a", "b"}, false},
		{"hello", KindUnknown, "hello", false},
	}

	for _, c := range cases {
		got, err := ParseValue(c.raw, c.kind)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseValue(%q, %v): expected error, got nil (value=%v)", c.raw, c.kind, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseValue(%q, %v): unexpected error: %v", c.raw, c.kind, err)
			continue
		}
		// Deep compare for slices.
		switch want := c.want.(type) {
		case []string:
			gotSl, ok := got.([]string)
			if !ok {
				t.Errorf("ParseValue(%q, %v): got type %T, want []string", c.raw, c.kind, got)
				continue
			}
			if len(gotSl) != len(want) {
				t.Errorf("ParseValue(%q, %v): got %v, want %v", c.raw, c.kind, gotSl, want)
				continue
			}
			for i := range want {
				if gotSl[i] != want[i] {
					t.Errorf("ParseValue(%q, %v)[%d]: got %q, want %q", c.raw, c.kind, i, gotSl[i], want[i])
				}
			}
		default:
			if got != c.want {
				t.Errorf("ParseValue(%q, %v) = %v (%T), want %v (%T)", c.raw, c.kind, got, got, c.want, c.want)
			}
		}
	}
}
