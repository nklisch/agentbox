package container

import (
	"encoding/json"
	"testing"
	"time"
)

func TestParseInspect_Running(t *testing.T) {
	raw := inspectFormat{
		Name: "agentbox-abc123456789",
		State: struct {
			Running bool   `json:"Running"`
			Status  string `json:"Status"`
		}{Running: true, Status: "running"},
		Config: struct {
			Labels map[string]string `json:"Labels"`
			Image  string            `json:"Image"`
		}{
			Labels: map[string]string{
				"agentbox.project_id": "abc123456789",
				"agentbox.project":    "myproject",
				"agentbox.cwd":        "/home/user/myproject",
				"agentbox.agent":      "claude",
				"agentbox.kits":       "polyglot,claude",
				"agentbox.kit_image":  "agentbox/abc123456789",
				"agentbox.created":    "2024-01-01T00:00:00Z",
			},
		},
		Created: "2024-01-01T00:00:00Z",
	}
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	box, err := parseInspect(b)
	if err != nil {
		t.Fatalf("parseInspect: %v", err)
	}
	if box.Status != StatusRunning {
		t.Errorf("Status = %q, want %q", box.Status, StatusRunning)
	}
	if box.ProjectID != "abc123456789" {
		t.Errorf("ProjectID = %q, want %q", box.ProjectID, "abc123456789")
	}
	if box.Project != "myproject" {
		t.Errorf("Project = %q, want %q", box.Project, "myproject")
	}
	if len(box.Kits) != 2 {
		t.Errorf("Kits len = %d, want 2", len(box.Kits))
	}
}

func TestParseInspect_Stopped(t *testing.T) {
	raw := inspectFormat{
		Name: "agentbox-abc123456789",
		State: struct {
			Running bool   `json:"Running"`
			Status  string `json:"Status"`
		}{Running: false, Status: "exited"},
		Config: struct {
			Labels map[string]string `json:"Labels"`
			Image  string            `json:"Image"`
		}{
			Labels: map[string]string{
				"agentbox.project_id": "abc123456789",
				"agentbox.created":    "2024-01-01T00:00:00Z",
			},
		},
		Created: "2024-01-01T00:00:00Z",
	}
	b, _ := json.Marshal(raw)

	box, err := parseInspect(b)
	if err != nil {
		t.Fatalf("parseInspect: %v", err)
	}
	if box.Status != StatusStopped {
		t.Errorf("Status = %q, want %q", box.Status, StatusStopped)
	}
}

func TestParseInspect_InvalidJSON(t *testing.T) {
	_, err := parseInspect([]byte("not json"))
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestBoxFromLabels_Created_LabelPreferred(t *testing.T) {
	labelTime := "2024-06-01T12:00:00Z"
	containerTime := "2024-01-01T00:00:00.123456789Z"

	box := boxFromLabels(map[string]string{
		"agentbox.created": labelTime,
	}, true, containerTime)

	want, _ := time.Parse(time.RFC3339, labelTime)
	if !box.Created.Equal(want) {
		t.Errorf("Created = %v, want %v (label preferred over container time)", box.Created, want)
	}
}

func TestBoxFromLabels_Created_FallsBackToContainer(t *testing.T) {
	containerTime := "2024-01-01T00:00:00.123456789Z"

	box := boxFromLabels(map[string]string{}, true, containerTime)

	want, _ := time.Parse(time.RFC3339Nano, containerTime)
	if !box.Created.Equal(want) {
		t.Errorf("Created = %v, want %v (container time fallback)", box.Created, want)
	}
}

func TestBoxFromLabels_KitsSplit(t *testing.T) {
	box := boxFromLabels(map[string]string{
		"agentbox.kits": "base,polyglot,claude",
	}, true, "")

	if len(box.Kits) != 3 {
		t.Errorf("Kits = %v, want 3 elements", box.Kits)
	}
	if box.Kits[0] != "base" || box.Kits[1] != "polyglot" || box.Kits[2] != "claude" {
		t.Errorf("Kits = %v, want [base polyglot claude]", box.Kits)
	}
}

func TestBoxFromLabels_EmptyKits(t *testing.T) {
	box := boxFromLabels(map[string]string{}, false, "")
	if box.Kits != nil {
		t.Errorf("Kits = %v, want nil for empty label", box.Kits)
	}
}

func TestContainerName(t *testing.T) {
	if got := ContainerName("abc123"); got != "agentbox-abc123" {
		t.Errorf("ContainerName(%q) = %q, want %q", "abc123", got, "agentbox-abc123")
	}
}
