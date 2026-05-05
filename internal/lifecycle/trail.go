package lifecycle

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// trailEnabled reports whether the resolved (layoutName, agent) tuple
// activates trail wiring. v1: only "auditor" + "claude".
func trailEnabled(layoutName, agent string) bool {
	return layoutName == "auditor" && agent == "claude"
}

// trailHookCommand is the in-container path of the hook recorder installed
// by the base kit. The hook's settings.json entries reference this absolute
// path so Claude Code can invoke it on each hook event.
const trailHookCommand = "/usr/local/bin/agentbox-hook-record"

// trailHookEvents are the Claude Code hook events the trail listens to.
// PostToolUseFailure and StopFailure are required for a complete trail.
// These are the authoritative names; nothing else in the codebase should
// hardcode Claude Code event strings.
var trailHookEvents = []string{
	"PreToolUse",
	"PostToolUse",
	"PostToolUseFailure",
	"Stop",
	"StopFailure",
}

// ErrInvalidUserSettings indicates the user's host settings.json is malformed
// and trail wiring couldn't proceed cleanly. The caller decides whether to
// abort or fall back — this function fails fast per the project's principles.
var ErrInvalidUserSettings = errors.New("invalid user claude settings")

// MergeTrailHooks reads the user's host claude settings.json (if any), adds
// agentbox's trail hooks, and returns the merged JSON bytes.
//
// The user's existing hooks are preserved. agentbox's hooks are appended to
// each event's array as a new matcher group (matcher "*", with a single
// command-type hook pointing at trailHookCommand).
//
// userSettingsPath may be empty (or point to a non-existent file); in that
// case MergeTrailHooks starts from an empty document.
//
// Returns ErrInvalidUserSettings if the user's file exists but isn't valid
// JSON — caller decides whether to abort or fall back.
func MergeTrailHooks(userSettingsPath string) ([]byte, error) {
	var doc map[string]any
	if userSettingsPath != "" {
		body, err := os.ReadFile(userSettingsPath)
		if err == nil {
			if jerr := json.Unmarshal(body, &doc); jerr != nil {
				return nil, fmt.Errorf("%w: %s: %v", ErrInvalidUserSettings, userSettingsPath, jerr)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("read %s: %w", userSettingsPath, err)
		}
	}
	if doc == nil {
		doc = map[string]any{}
	}
	hooks, _ := doc["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		doc["hooks"] = hooks
	}
	for _, ev := range trailHookEvents {
		existing, _ := hooks[ev].([]any)
		agentboxGroup := map[string]any{
			"matcher": "*",
			"hooks": []any{
				map[string]any{
					"type":    "command",
					"command": trailHookCommand,
				},
			},
		}
		hooks[ev] = append(existing, agentboxGroup)
	}
	return json.MarshalIndent(doc, "", "  ")
}

// EnsureTrailFile touches <stateDir>/trail.jsonl if missing so the container's
// bind-mount source is a real file (not auto-created as a directory by podman,
// which would corrupt the mount). Follows the same self-heal pattern as the
// ~/.claude.json touch in lifecycle.go.
func EnsureTrailFile(stateDir string) (string, error) {
	p := filepath.Join(stateDir, "trail.jsonl")
	if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
		f, ferr := os.OpenFile(p, os.O_CREATE|os.O_WRONLY, 0o600)
		if ferr != nil {
			return "", ferr
		}
		_ = f.Close()
	}
	return p, nil
}

// WriteShadowSettings writes the merged claude settings (user settings +
// agentbox trail hooks) to <stateDir>/claude-settings.json with mode 0o600.
// Returns the host path. The caller bind-mounts it on top of
// /root/.claude/settings.json inside the container (read-only), shadowing
// the user's bind-mounted ~/.claude/settings.json without touching it.
//
// The host's actual ~/.claude/settings.json is never written by agentbox.
func WriteShadowSettings(stateDir, hostHome string) (string, error) {
	userPath := filepath.Join(hostHome, ".claude", "settings.json")
	body, err := MergeTrailHooks(userPath)
	if err != nil {
		return "", err
	}
	out := filepath.Join(stateDir, "claude-settings.json")
	if err := os.WriteFile(out, body, 0o600); err != nil {
		return "", err
	}
	return out, nil
}
