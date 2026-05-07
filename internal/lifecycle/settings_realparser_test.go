package lifecycle_test

// E2E shape-validation test for the claude-settings.json artifact agentbox
// writes when wiring the trail (auditor + claude). The existing trail_test.go
// suite checks structure with encoding/json — the same library that produced
// the bytes — so it cannot detect regressions where the file is valid JSON
// but the wrong *shape* for Claude Code's hooks loader. This file closes
// that gap two ways:
//
//  1. Independent re-parse: decode the file into a typed struct that mirrors
//     Claude Code's hooks schema (matcher/hooks/type/command), then assert
//     the values. A wrong key, wrong nesting, or wrong type fails here even
//     if the bytes round-trip through encoding/json fine.
//  2. Real `jq` exec: when jq is on PATH, run the file through `jq -e` with
//     selectors matching what Claude Code's loader looks for. jq is an
//     independent JSON implementation, so it catches encoding edge cases
//     (e.g. invalid UTF-8 escape sequences) Go's encoder might tolerate.
//
// Both checks together approximate "would Claude Code accept this?" without
// requiring Claude Code itself in the test environment. When Claude Code is
// available the agentbox doctor runs the same artifact through it; the unit
// test stays fast and self-contained.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/nklisch/agentbox/internal/lifecycle"
)

// claudeSettingsShape mirrors the subset of Claude Code's settings.json
// hooks schema that agentbox writes. Field tags are load-bearing — if
// MergeTrailHooks ever emits a different key (e.g. "Matcher" vs "matcher")
// the unmarshal will silently produce zero values and the assertions below
// will catch it.
type claudeSettingsShape struct {
	Hooks map[string][]struct {
		Matcher string `json:"matcher"`
		Hooks   []struct {
			Type    string `json:"type"`
			Command string `json:"command"`
		} `json:"hooks"`
	} `json:"hooks"`
}

// requiredEvents is the set of Claude Code hook events agentbox must wire
// for the trail to be complete. Mirrors trail.go::trailHookEvents but
// independent — if someone deletes one from the producer, this test fails.
var requiredEvents = []string{
	"PreToolUse",
	"PostToolUse",
	"PostToolUseFailure",
	"Stop",
	"StopFailure",
}

// expectedHookCommand is the absolute path agentbox stamps into every hook.
// Keep in sync with trail.go::trailHookCommand. Hardcoded here on purpose
// so a regression in the producer doesn't sneak through by importing the
// same constant.
const expectedHookCommand = "/usr/local/bin/agentbox-hook-record"

func writeShadowAndRead(t *testing.T, homeDir string) (string, []byte) {
	t.Helper()
	stateDir := t.TempDir()
	path, err := lifecycle.WriteShadowSettings(stateDir, homeDir)
	if err != nil {
		t.Fatalf("WriteShadowSettings: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read shadow settings: %v", err)
	}
	return path, body
}

// assertShape decodes body into claudeSettingsShape and asserts every
// required event has at least one matcher group whose inner hook is the
// agentbox recorder command. This is the contract Claude Code's hooks
// loader will exercise; if it fails, Claude Code would reject too.
func assertShape(t *testing.T, body []byte) {
	t.Helper()
	var doc claudeSettingsShape
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("unmarshal into typed shape: %v\nbody:\n%s", err, body)
	}
	if len(doc.Hooks) == 0 {
		t.Fatalf("no hooks in shadow settings:\n%s", body)
	}
	for _, ev := range requiredEvents {
		groups, ok := doc.Hooks[ev]
		if !ok {
			t.Errorf("missing required event %q\nbody:\n%s", ev, body)
			continue
		}
		var found bool
		for _, g := range groups {
			if g.Matcher == "" {
				t.Errorf("event %q: matcher empty in group %+v", ev, g)
			}
			for _, h := range g.Hooks {
				if h.Type != "command" {
					t.Errorf("event %q: hook type %q, want %q", ev, h.Type, "command")
				}
				if h.Command == expectedHookCommand {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("event %q has no agentbox recorder hook (%s)\nbody:\n%s",
				ev, expectedHookCommand, body)
		}
	}
}

// runJq, when jq is available, runs the body through jq with the given
// filter and asserts exit 0. jq -e returns nonzero if the filter evaluates
// to false/null, so the filter doubles as an assertion language.
func runJq(t *testing.T, body []byte, filter string) {
	t.Helper()
	jq, err := exec.LookPath("jq")
	if err != nil {
		t.Logf("jq not on PATH; skipping jq cross-check (filter %q)", filter)
		return
	}
	tmp := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		t.Fatalf("write tmp: %v", err)
	}
	cmd := exec.Command(jq, "-e", filter, tmp)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Errorf("jq filter %q failed: %v\noutput: %s\nbody:\n%s",
			filter, err, out, body)
	}
}

func TestRealParser_NoUserSettings_ProducesAcceptableShape(t *testing.T) {
	homeDir := t.TempDir() // empty home — no ~/.claude/settings.json exists
	_, body := writeShadowAndRead(t, homeDir)
	assertShape(t, body)
	runJq(t, body, `.hooks | type == "object"`)
	runJq(t, body, `.hooks.PreToolUse | length >= 1`)
	runJq(t, body, `.hooks.PreToolUse[0].hooks[0].command == "`+expectedHookCommand+`"`)
}

func TestRealParser_PreservesUserHook_ShapeIntact(t *testing.T) {
	homeDir := t.TempDir()
	claudeDir := filepath.Join(homeDir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	user := map[string]any{
		"model": "opus",
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{
					"matcher": "Bash",
					"hooks": []any{
						map[string]any{"type": "command", "command": "/usr/local/bin/user-hook"},
					},
				},
			},
		},
	}
	raw, _ := json.Marshal(user)
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), raw, 0o600); err != nil {
		t.Fatalf("write user settings: %v", err)
	}
	_, body := writeShadowAndRead(t, homeDir)
	assertShape(t, body)
	// User hook still present — shadow merges, doesn't replace.
	runJq(t, body, `[.hooks.PreToolUse[].matcher] | contains(["Bash"])`)
	// Agentbox hook also present.
	runJq(t, body, `[.hooks.PreToolUse[].matcher] | contains(["*"])`)
	// Top-level user fields preserved (not under hooks).
	runJq(t, body, `.model == "opus"`)
}

// Probes a quirky-but-valid user settings file: extra unknown keys, deep
// nesting, mixed types under non-hook fields. Claude Code ignores keys it
// doesn't recognize, so the shadow must too.
func TestRealParser_PreservesUnknownTopLevelKeys(t *testing.T) {
	homeDir := t.TempDir()
	claudeDir := filepath.Join(homeDir, ".claude")
	_ = os.MkdirAll(claudeDir, 0o700)
	user := map[string]any{
		"permissions":             map[string]any{"defaultMode": "auto"},
		"enabledPlugins":          map[string]any{"x": true},
		"skipDangerousModePrompt": true,
		"weirdNumber":             3.14,
	}
	raw, _ := json.Marshal(user)
	_ = os.WriteFile(filepath.Join(claudeDir, "settings.json"), raw, 0o600)
	_, body := writeShadowAndRead(t, homeDir)
	assertShape(t, body)
	runJq(t, body, `.permissions.defaultMode == "auto"`)
	runJq(t, body, `.weirdNumber == 3.14`)
	runJq(t, body, `.enabledPlugins.x == true`)
}

// The bytes that hit disk are the bytes that hit Claude Code. Validate the
// on-disk file directly (not the in-memory result) so any post-Marshal step
// — encoding, file mode, partial writes — is in scope.
func TestRealParser_OnDiskFileMatchesContract(t *testing.T) {
	homeDir := t.TempDir()
	path, body := writeShadowAndRead(t, homeDir)
	// File mode: contract says 0600 (trail.go:117).
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("on-disk mode = %o, want 0600", info.Mode().Perm())
	}
	// Body parses as plain JSON via independent path (jq) AND typed struct.
	assertShape(t, body)
	runJq(t, body, `type == "object"`)
}
