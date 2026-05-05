package config

import (
	"reflect"
	"strings"
)

// Kind is the abstract value kind that a dotted path resolves to.
// Used by ParseValue to select the correct parser for a raw CLI string.
type Kind int

const (
	KindUnknown    Kind = iota // path not in schema; best-effort parse
	KindString                 // string scalar
	KindBool                   // bool scalar
	KindInt                    // integer scalar
	KindFloat                  // float scalar
	KindStringList             // []string (e.g. default_kits, allowlist.allow)
)

// LookupKind walks the Config struct's TOML tags following path and returns
// the Kind of the leaf field. Returns KindUnknown if the path doesn't match
// the schema (typo, path too deep, or a free-form map-key context where the
// value type is not a recognised scalar/slice).
//
// Special handling for map-typed fields:
//   - map[string]string (e.g. mounts.agent_configs): any subsequent path
//     segment is a free-form key; the value kind is KindString.
//   - map[string]Agent (e.g. agents): the next segment is the agent name;
//     resolution continues on the Agent struct's TOML tags.
func LookupKind(path []string) Kind {
	if len(path) == 0 {
		return KindUnknown
	}
	return lookupInType(reflect.TypeOf(DefaultConfig()), path)
}

func lookupInType(t reflect.Type, path []string) Kind {
	// Dereference pointers.
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	// Base case: consumed all path segments.
	if len(path) == 0 {
		return reflectKind(t)
	}

	seg := path[0]
	rest := path[1:]

	switch t.Kind() {
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			tag := strings.Split(f.Tag.Get("toml"), ",")[0]
			if tag == seg {
				return lookupInType(f.Type, rest)
			}
		}
		return KindUnknown

	case reflect.Map:
		// The current segment is the map key. Descend into the element type.
		return lookupInType(t.Elem(), rest)

	default:
		// Scalar or slice at an intermediate position: path goes too deep.
		return KindUnknown
	}
}

// reflectKind converts a reflect.Type to our Kind enum.
func reflectKind(t reflect.Type) Kind {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Bool:
		return KindBool
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return KindInt
	case reflect.Float32, reflect.Float64:
		return KindFloat
	case reflect.String:
		return KindString
	case reflect.Slice:
		if t.Elem().Kind() == reflect.String {
			return KindStringList
		}
		return KindUnknown
	default:
		return KindUnknown
	}
}
