package zellij_test

// E2E parse-validation test for the layout artifacts agentbox actually writes
// to disk. Pipes the bytes from GenerateKDL through the real `zellij` binary
// (`zellij --layout <path> setup --check`), which exercises zellij's KDL
// parser end-to-end and exits non-zero with a "Failed to parse" diagnostic
// on malformed input. A pass means zellij accepts the file the way it would
// on first run inside the box; a fail means the produced artifact is
// malformed *as zellij sees it* — exactly the bug class plain
// strings.Contains tests can't catch.
//
// Skips cleanly when `zellij` is not on PATH so `go test ./...` stays green
// on machines without zellij installed. CI is expected to install zellij
// (matching the kit image's version, currently 0.44.x) so this test runs
// in the pipeline.
//
// We use `setup --check` (with --layout pre-loaded) rather than launching
// zellij because:
//   - `zellij setup --dump-layout` is a passthrough; it doesn't validate.
//     (Confirmed against zellij 0.44.1: it dumps malformed input verbatim
//     with exit 0.)
//   - Launching zellij needs a TTY and spawns panes.
//   - `zellij --layout <path> setup --check` parses the layout, dumps
//     directories, and exits 0/1 cleanly.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nklisch/agentbox/internal/zellij"
)

// requireZellij returns the resolved path to the zellij binary or skips the
// test. We deliberately don't try `podman exec <kit-image>` as a fallback —
// that would couple this test to having a built kit image, which isn't
// guaranteed in dev or CI.
func requireZellij(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("zellij")
	if err != nil {
		t.Skip("zellij not on PATH; install zellij 0.44+ to run this e2e test")
	}
	return p
}

// validateKDL exec's `zellij --layout <path> setup --check` and returns
// (stdout, stderr, error). Exit 0 with `[Version]:` on stdout means zellij
// parsed the layout successfully. Exit 1 with `Failed to parse Zellij
// configuration` on stderr means the layout is malformed.
func validateKDL(zbin, path string) (string, string, error) {
	cmd := exec.Command(zbin, "--layout", path, "setup", "--check")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// writeAndDump renders `layout` via GenerateKDL, writes it to a temp file
// (the same shape lifecycle.writeLayoutFor does), and runs zellij against
// it. Returns the rendered body (for diagnostics on failure).
func writeAndDump(t *testing.T, zbin string, layout zellij.Layout) string {
	t.Helper()
	body := zellij.GenerateKDL(layout)
	dir := t.TempDir()
	path := filepath.Join(dir, "layout.kdl")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write kdl: %v", err)
	}
	stdout, stderr, err := validateKDL(zbin, path)
	combined := stdout + "\n" + stderr
	if strings.Contains(combined, "Failed to parse") {
		t.Fatalf("zellij rejected layout (parse failure)\nstderr:\n%s\nbody:\n%s",
			stderr, body)
	}
	if err != nil {
		t.Fatalf("zellij --layout %s setup --check failed: %v\nstderr:\n%s\nbody:\n%s",
			path, err, stderr, body)
	}
	// `setup --check` always emits the version line on success.
	if !strings.Contains(stdout, "[Version]:") {
		t.Fatalf("zellij setup --check emitted no version line; stdout:\n%s\nbody:\n%s",
			stdout, body)
	}
	return body
}

func TestRealZellij_ParsesFocusLayout(t *testing.T) {
	zbin := requireZellij(t)
	writeAndDump(t, zbin, zellij.Layout{
		Mode:       zellij.ModeRun,
		LayoutName: "focus",
		AgentCmd:   []string{"claude", "--dangerously-skip-permissions"},
		ProjectAbs: "/tmp/agentbox-e2e-focus",
		Shell:      "zsh",
	})
}

func TestRealZellij_ParsesReviewerLayout(t *testing.T) {
	zbin := requireZellij(t)
	writeAndDump(t, zbin, zellij.Layout{
		Mode:       zellij.ModeRun,
		LayoutName: "reviewer",
		AgentCmd:   []string{"claude"},
		ProjectAbs: "/tmp/agentbox-e2e-reviewer",
		Shell:      "zsh",
	})
}

func TestRealZellij_ParsesAuditorLayout(t *testing.T) {
	zbin := requireZellij(t)
	writeAndDump(t, zbin, zellij.Layout{
		Mode:       zellij.ModeRun,
		LayoutName: "auditor",
		AgentCmd:   []string{"claude"},
		ProjectAbs: "/tmp/agentbox-e2e-auditor",
		Shell:      "zsh",
	})
}

func TestRealZellij_ParsesShellLayout(t *testing.T) {
	zbin := requireZellij(t)
	writeAndDump(t, zbin, zellij.Layout{
		Mode:       zellij.ModeShell,
		ProjectAbs: "/tmp/agentbox-e2e-shell",
		Shell:      "zsh",
	})
}

// Path with spaces — quoted cwd is a known KDL-quoting risk surface. If the
// quoting ever regresses, zellij will reject the layout here.
func TestRealZellij_HandlesPathWithSpaces(t *testing.T) {
	zbin := requireZellij(t)
	writeAndDump(t, zbin, zellij.Layout{
		Mode:       zellij.ModeRun,
		LayoutName: "focus",
		AgentCmd:   []string{"claude"},
		ProjectAbs: "/tmp/has spaces/agentbox-e2e",
		Shell:      "zsh",
	})
}

// Empty AgentCmd is the fallback path — emits `command "zsh"` directly.
// Distinct codepath through writeCommand; warrants its own real-parser run.
func TestRealZellij_HandlesEmptyAgentCmd(t *testing.T) {
	zbin := requireZellij(t)
	writeAndDump(t, zbin, zellij.Layout{
		Mode:       zellij.ModeRun,
		LayoutName: "focus",
		AgentCmd:   nil,
		ProjectAbs: "/tmp/agentbox-e2e-empty",
		Shell:      "bash",
	})
}

// Custom KDL is rendered verbatim (no template expansion at this level —
// LoadCustom owns that). We feed it a minimally valid custom layout to
// confirm the verbatim path doesn't accidentally wrap or mutate.
func TestRealZellij_PassesCustomKDLVerbatim(t *testing.T) {
	zbin := requireZellij(t)
	custom := `// custom
layout {
    tab name="custom" focus=true {
        pane name="x" {
            command "zsh"
        }
    }
}
`
	writeAndDump(t, zbin, zellij.Layout{
		Mode:       zellij.ModeRun,
		LayoutName: "myown",
		CustomKDL:  custom,
		ProjectAbs: "/tmp/agentbox-e2e-custom",
		Shell:      "zsh",
	})
}
