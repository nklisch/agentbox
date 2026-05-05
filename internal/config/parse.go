package config

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseValue interprets a raw CLI string as a typed TOML value.
//
// Strict-by-kind rules:
//   - KindBool: only "true" or "false" (lowercase). Friendlier forms
//     (on/off/yes/no) belong in the convenience commands.
//   - KindInt: strconv.ParseInt base-10.
//   - KindFloat: strconv.ParseFloat 64-bit.
//   - KindString: raw as-is.
//   - KindStringList: split on comma, trim whitespace per element.
//     Empty raw produces []string{}.
//
// KindUnknown performs best-effort detection:
//  1. "true"/"false" → bool
//  2. ParseInt succeeds → int64
//  3. ParseFloat succeeds → float64
//  4. contains "," → []string (split + trim)
//  5. fallback → string
func ParseValue(raw string, kind Kind) (any, error) {
	switch kind {
	case KindBool:
		switch raw {
		case "true":
			return true, nil
		case "false":
			return false, nil
		default:
			return nil, fmt.Errorf("invalid bool %q: must be true or false", raw)
		}
	case KindInt:
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid int %q: %w", raw, err)
		}
		return n, nil
	case KindFloat:
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid float %q: %w", raw, err)
		}
		return f, nil
	case KindString:
		return raw, nil
	case KindStringList:
		if raw == "" {
			return []string{}, nil
		}
		parts := strings.Split(raw, ",")
		for i, p := range parts {
			parts[i] = strings.TrimSpace(p)
		}
		return parts, nil
	default: // KindUnknown: best-effort
		return parseBestEffort(raw), nil
	}
}

func parseBestEffort(raw string) any {
	switch raw {
	case "true":
		return true
	case "false":
		return false
	}
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(raw, 64); err == nil {
		return f
	}
	if strings.Contains(raw, ",") {
		parts := strings.Split(raw, ",")
		for i, p := range parts {
			parts[i] = strings.TrimSpace(p)
		}
		return parts
	}
	return raw
}
