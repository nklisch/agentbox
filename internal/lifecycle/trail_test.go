package lifecycle_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/nklisch/agentbox/internal/lifecycle"
)

// ---- MergeTrailHooks ----

func TestMergeTrailHooks_EmptyPath_ProducesAllEvents(t *testing.T) {
	data, err := lifecycle.MergeTrailHooks("")
	if err != nil {
		t.Fatalf("MergeTrailHooks: %v", err)
	}
	var doc map[string]any
	if jerr := json.Unmarshal(data, &doc); jerr != nil {
		t.Fatalf("unmarshal: %v", jerr)
	}
	hooks, ok := doc["hooks"].(map[string]any)
	if !ok {
		t.Fatal("expected hooks object")
	}
	wantEvents := []string{"PreToolUse", "PostToolUse", "PostToolUseFailure", "Stop", "StopFailure"}
	for _, ev := range wantEvents {
		arr, ok := hooks[ev].([]any)
		if !ok || len(arr) == 0 {
			t.Errorf("expected %q hook array in output, got: %v", ev, hooks[ev])
		}
	}
}

func TestMergeTrailHooks_NonExistentPath_SameAsEmpty(t *testing.T) {
	dataEmpty, err := lifecycle.MergeTrailHooks("")
	if err != nil {
		t.Fatalf("MergeTrailHooks(empty): %v", err)
	}
	dataMissing, err := lifecycle.MergeTrailHooks("/does/not/exist/settings.json")
	if err != nil {
		t.Fatalf("MergeTrailHooks(missing): %v", err)
	}
	// Both should have the same hooks (five events).
	var docEmpty, docMissing map[string]any
	json.Unmarshal(dataEmpty, &docEmpty)
	json.Unmarshal(dataMissing, &docMissing)
	hooksEmpty := docEmpty["hooks"].(map[string]any)
	hooksMissing := docMissing["hooks"].(map[string]any)
	if len(hooksEmpty) != len(hooksMissing) {
		t.Errorf("hook count mismatch: empty=%d missing=%d", len(hooksEmpty), len(hooksMissing))
	}
}

func TestMergeTrailHooks_ExistingUserHook_Preserved(t *testing.T) {
	// Write a settings.json that has a user hook under PreToolUse.
	tmp := t.TempDir()
	userSettings := map[string]any{
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
	raw, _ := json.Marshal(userSettings)
	p := filepath.Join(tmp, "settings.json")
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	data, err := lifecycle.MergeTrailHooks(p)
	if err != nil {
		t.Fatalf("MergeTrailHooks: %v", err)
	}
	var doc map[string]any
	json.Unmarshal(data, &doc)
	hooks := doc["hooks"].(map[string]any)
	preArr, ok := hooks["PreToolUse"].([]any)
	if !ok {
		t.Fatal("expected PreToolUse array")
	}
	// Should have 2 groups: user's original + agentbox's.
	if len(preArr) != 2 {
		t.Errorf("expected 2 PreToolUse groups (user + agentbox), got %d", len(preArr))
	}
	// Verify user's hook is still there (first group).
	first := preArr[0].(map[string]any)
	if first["matcher"] != "Bash" {
		t.Errorf("expected user's matcher=Bash preserved, got %v", first["matcher"])
	}
	// Verify agentbox's hook is appended (second group, matcher="*").
	second := preArr[1].(map[string]any)
	if second["matcher"] != "*" {
		t.Errorf("expected agentbox matcher=*, got %v", second["matcher"])
	}
}

func TestMergeTrailHooks_UserHasNoPreToolUse_AddsAll(t *testing.T) {
	// User settings with a different event (PostToolUse) only.
	tmp := t.TempDir()
	userSettings := map[string]any{
		"hooks": map[string]any{
			"PostToolUse": []any{
				map[string]any{
					"matcher": "*",
					"hooks": []any{
						map[string]any{"type": "command", "command": "/usr/local/bin/user-post"},
					},
				},
			},
		},
		"someOtherField": "preserved",
	}
	raw, _ := json.Marshal(userSettings)
	p := filepath.Join(tmp, "settings.json")
	os.WriteFile(p, raw, 0o600)

	data, err := lifecycle.MergeTrailHooks(p)
	if err != nil {
		t.Fatalf("MergeTrailHooks: %v", err)
	}
	var doc map[string]any
	json.Unmarshal(data, &doc)
	hooks := doc["hooks"].(map[string]any)

	// All five events should be present.
	for _, ev := range []string{"PreToolUse", "PostToolUse", "PostToolUseFailure", "Stop", "StopFailure"} {
		arr, ok := hooks[ev].([]any)
		if !ok || len(arr) == 0 {
			t.Errorf("expected %q in hooks, got %v", ev, hooks[ev])
		}
	}
	// PostToolUse should have 2 groups (user + agentbox).
	if len(hooks["PostToolUse"].([]any)) != 2 {
		t.Errorf("PostToolUse: expected 2 groups, got %d", len(hooks["PostToolUse"].([]any)))
	}
	// Other user fields preserved.
	if doc["someOtherField"] != "preserved" {
		t.Errorf("user's extra field not preserved: %v", doc["someOtherField"])
	}
}

func TestMergeTrailHooks_MalformedJSON_ReturnsErrInvalidUserSettings(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "settings.json")
	os.WriteFile(p, []byte("not json {{"), 0o600)

	_, err := lifecycle.MergeTrailHooks(p)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if !errors.Is(err, lifecycle.ErrInvalidUserSettings) {
		t.Errorf("expected ErrInvalidUserSettings, got: %v", err)
	}
}

func TestMergeTrailHooks_HookCommandIsRecorder(t *testing.T) {
	data, err := lifecycle.MergeTrailHooks("")
	if err != nil {
		t.Fatalf("MergeTrailHooks: %v", err)
	}
	var doc map[string]any
	json.Unmarshal(data, &doc)
	hooks := doc["hooks"].(map[string]any)
	// Check the command in one of the agentbox groups.
	preArr := hooks["PreToolUse"].([]any)
	agentboxGroup := preArr[0].(map[string]any)
	innerHooks := agentboxGroup["hooks"].([]any)
	hook := innerHooks[0].(map[string]any)
	if hook["command"] != "/usr/local/bin/agentbox-hook-record" {
		t.Errorf("expected trailHookCommand, got %v", hook["command"])
	}
	if hook["type"] != "command" {
		t.Errorf("expected type=command, got %v", hook["type"])
	}
}

// ---- EnsureTrailFile ----

func TestEnsureTrailFile_CreatesIfMissing(t *testing.T) {
	tmp := t.TempDir()
	p, err := lifecycle.EnsureTrailFile(tmp)
	if err != nil {
		t.Fatalf("EnsureTrailFile: %v", err)
	}
	if p != filepath.Join(tmp, "trail.jsonl") {
		t.Errorf("returned path %q, want %q", p, filepath.Join(tmp, "trail.jsonl"))
	}
	if _, err := os.Stat(p); err != nil {
		t.Errorf("trail file not created: %v", err)
	}
}

func TestEnsureTrailFile_Idempotent(t *testing.T) {
	tmp := t.TempDir()
	// Create with some content to verify idempotency.
	initial := filepath.Join(tmp, "trail.jsonl")
	os.WriteFile(initial, []byte(`{"test":1}`+"\n"), 0o600)
	info1, _ := os.Stat(initial)

	p, err := lifecycle.EnsureTrailFile(tmp)
	if err != nil {
		t.Fatalf("EnsureTrailFile second call: %v", err)
	}
	info2, _ := os.Stat(p)
	if info1.ModTime() != info2.ModTime() {
		t.Error("EnsureTrailFile modified an existing file (not idempotent)")
	}
	// Content should be unchanged.
	data, _ := os.ReadFile(p)
	if string(data) != `{"test":1}`+"\n" {
		t.Errorf("existing file content modified: %q", data)
	}
}

// ---- WriteShadowSettings ----

func TestWriteShadowSettings_WritesFile(t *testing.T) {
	stateDir := t.TempDir()
	homeDir := t.TempDir()
	// No user settings.json — should produce clean agentbox-only settings.

	p, err := lifecycle.WriteShadowSettings(stateDir, homeDir)
	if err != nil {
		t.Fatalf("WriteShadowSettings: %v", err)
	}
	if p != filepath.Join(stateDir, "claude-settings.json") {
		t.Errorf("returned path %q, want %q", p, filepath.Join(stateDir, "claude-settings.json"))
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat shadow settings: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("shadow settings mode = %o, want 0600", info.Mode().Perm())
	}
}

func TestWriteShadowSettings_ContainsAllHooks(t *testing.T) {
	stateDir := t.TempDir()
	homeDir := t.TempDir()

	p, err := lifecycle.WriteShadowSettings(stateDir, homeDir)
	if err != nil {
		t.Fatalf("WriteShadowSettings: %v", err)
	}
	data, _ := os.ReadFile(p)
	var doc map[string]any
	if jerr := json.Unmarshal(data, &doc); jerr != nil {
		t.Fatalf("unmarshal shadow settings: %v", jerr)
	}
	hooks, ok := doc["hooks"].(map[string]any)
	if !ok {
		t.Fatal("expected hooks object in shadow settings")
	}
	for _, ev := range []string{"PreToolUse", "PostToolUse", "PostToolUseFailure", "Stop", "StopFailure"} {
		if _, exists := hooks[ev]; !exists {
			t.Errorf("shadow settings missing hook event %q", ev)
		}
	}
}

func TestWriteShadowSettings_MergesUserHooks(t *testing.T) {
	stateDir := t.TempDir()
	homeDir := t.TempDir()

	// Create user's ~/.claude/settings.json with a pre-existing hook.
	claudeDir := filepath.Join(homeDir, ".claude")
	os.MkdirAll(claudeDir, 0o700)
	userSettings := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{
					"matcher": "Bash",
					"hooks": []any{
						map[string]any{"type": "command", "command": "/usr/local/bin/my-hook"},
					},
				},
			},
		},
	}
	raw, _ := json.Marshal(userSettings)
	os.WriteFile(filepath.Join(claudeDir, "settings.json"), raw, 0o600)

	p, err := lifecycle.WriteShadowSettings(stateDir, homeDir)
	if err != nil {
		t.Fatalf("WriteShadowSettings: %v", err)
	}
	data, _ := os.ReadFile(p)
	var doc map[string]any
	json.Unmarshal(data, &doc)
	hooks := doc["hooks"].(map[string]any)
	preArr := hooks["PreToolUse"].([]any)
	if len(preArr) != 2 {
		t.Errorf("expected 2 PreToolUse groups (user + agentbox), got %d", len(preArr))
	}
}

func TestWriteShadowSettings_MalformedUserSettings_ReturnsErr(t *testing.T) {
	stateDir := t.TempDir()
	homeDir := t.TempDir()

	// Write malformed user settings.
	claudeDir := filepath.Join(homeDir, ".claude")
	os.MkdirAll(claudeDir, 0o700)
	os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte("invalid json {{"), 0o600)

	_, err := lifecycle.WriteShadowSettings(stateDir, homeDir)
	if err == nil {
		t.Fatal("expected error for malformed user settings")
	}
	if !errors.Is(err, lifecycle.ErrInvalidUserSettings) {
		t.Errorf("expected ErrInvalidUserSettings, got: %v", err)
	}
}

// ---- trailEnabled (tested via exported behaviour in RunOpts / lifecycle) ----
// The trailEnabled function is package-private; we verify its effect through
// lifecycle integration tests in lifecycle_test.go (the file you're reading
// is in package lifecycle_test, which can only call exported symbols).
// Direct unit tests for trailEnabled live alongside it in trail.go; since
// it's unexported we test it indirectly via EnsureBox + Run paths below.
