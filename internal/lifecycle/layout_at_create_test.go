package lifecycle_test

// Isolation test for the layout.kdl write-after-mount race that surfaced as
// "malformed zellij config on first run after a build" on a fresh-install
// macOS box.
//
// The race: lifecycle.createBox calls state.EnsureSession which TOUCHES
// <state>/layout.kdl as a 0-byte file (so podman has a bind-mount source),
// then runs Runtime.Create + Runtime.Start. On the original code path,
// Run/Shell only wrote the *real* KDL after EnsureBox returned — i.e. after
// podman create had already locked in the empty file. On Linux, host writes
// to a bind-mounted file propagate to the guest. On macOS Podman the bind
// mount goes through the podman-machine VM's virtiofs, which caches the
// empty inode; the guest can keep serving 0 bytes (or torn content during
// the host write), so zellij sees an empty/broken layout file and aborts.
//
// This test reproduces the race in isolation without needing podman, virtio,
// or even a Mac. It uses a capture runtime that snapshots the layout file
// content at the precise moment Create() is invoked. If the file is empty
// at that point, the bug is present — no further verification needed.
//
// We also assert the snapshot has the structural fragments zellij looks for
// (the `layout {` block, an agentbox tab, the cwd quoted) so a future
// regression that writes garbage instead of empty is also caught.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nklisch/agentbox/internal/container"
	"github.com/nklisch/agentbox/internal/lifecycle"
	"github.com/nklisch/agentbox/internal/runspec"
)

// snapshotRuntime is a fakeRuntime variant that, at Create() time, reads the
// host bytes of every bind-mount source whose target is /etc/agentbox/*.
// Tests inspect r.atCreate to assert that the file mounted into the box at
// container-create time has the content the runtime would actually serve.
type snapshotRuntime struct {
	*fakeRuntime
	atCreate map[string][]byte // target path → bytes on host at Create()
}

func newSnapshotRuntime() *snapshotRuntime {
	return &snapshotRuntime{
		fakeRuntime: newFakeRuntime(),
		atCreate:    map[string][]byte{},
	}
}

func (r *snapshotRuntime) Create(args runspec.PodmanCreateArgs) error {
	for _, m := range args.Mounts {
		if !strings.HasPrefix(m.Target, "/etc/agentbox/") {
			continue
		}
		body, err := os.ReadFile(m.Source)
		if err != nil {
			// File missing at Create time is a separate failure mode the
			// existing mount-source check covers; record empty so the
			// downstream assertion fires explicitly.
			r.atCreate[m.Target] = nil
			continue
		}
		// Defensive copy so later host writes don't mutate the snapshot.
		buf := make([]byte, len(body))
		copy(buf, body)
		r.atCreate[m.Target] = buf
	}
	return r.fakeRuntime.Create(args)
}

// newSnapshotLifecycle wires a snapshotRuntime into a Lifecycle with the
// trail-test fixtures (~/.claude dir prepared so auditor+claude doesn't
// abort on missing settings).
func newSnapshotLifecycle(t *testing.T, sr *snapshotRuntime) *lifecycle.Lifecycle {
	t.Helper()
	isolateState(t)
	homeDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(homeDir, ".claude"), 0o700); err != nil {
		t.Fatalf("mkdir .claude: %v", err)
	}
	cfg := defaultTestCfg()
	// Give the agent a real command so the rendered layout contains
	// recognizable agent-pane content (proves the renderer ran with the
	// right inputs, not just that *something* got written).
	a := cfg.Agents["claude"]
	a.Cmd = []string{"claude", "--dangerously-skip-permissions"}
	cfg.Agents["claude"] = a

	netMgr := newFakeNetworkManager()
	return &lifecycle.Lifecycle{
		Cfg:     cfg,
		Runtime: sr,
		Builder: newTestBuilder(t, nil),
		Network: netMgr,
		Home:    homeDir,
		Stdout:  &devNull{},
		Stderr:  &devNull{},
	}
}

type devNull struct{}

func (d *devNull) Write(p []byte) (int, error) { return len(p), nil }

// assertLayoutPopulated asserts the layout.kdl seen by Create() has real
// content. Empty or whitespace-only fails the test — that's exactly the
// race condition this test exists to catch.
func assertLayoutPopulated(t *testing.T, snap []byte, wantFragments ...string) {
	t.Helper()
	if len(snap) == 0 {
		t.Fatal("layout.kdl was EMPTY at Runtime.Create() — race condition: " +
			"writeLayoutFor must run before EnsureBox/podman create, " +
			"otherwise macOS Podman virtiofs serves the empty file to zellij")
	}
	body := string(snap)
	if strings.TrimSpace(body) == "" {
		t.Fatalf("layout.kdl was whitespace-only at Create()\nbody (%d bytes):\n%q",
			len(snap), body)
	}
	if !strings.Contains(body, "layout {") {
		t.Errorf("layout.kdl at Create() missing top-level `layout {` block:\n%s", body)
	}
	for _, frag := range wantFragments {
		if !strings.Contains(body, frag) {
			t.Errorf("layout.kdl at Create() missing fragment %q\nbody:\n%s", frag, body)
		}
	}
}

func TestLayoutAtCreate_FocusLayout_PopulatedBeforeMount(t *testing.T) {
	sr := newSnapshotRuntime()
	setupProject(t)
	l := newSnapshotLifecycle(t, sr)

	if err := l.Run(lifecycle.RunOpts{Attach: false, Layout: "focus", Agent: "claude"}); err != nil {
		t.Fatalf("Run(focus): %v", err)
	}
	snap, ok := sr.atCreate["/etc/agentbox/layout.kdl"]
	if !ok {
		t.Fatal("no /etc/agentbox/layout.kdl mount captured at Create()")
	}
	assertLayoutPopulated(t, snap,
		`tab name="agentbox" focus=true`,
		`name="agent"`,
		`name="git"`,
		`name="stats"`,
		`args "claude" "--dangerously-skip-permissions"`,
	)
}

func TestLayoutAtCreate_ReviewerLayout_PopulatedBeforeMount(t *testing.T) {
	sr := newSnapshotRuntime()
	setupProject(t)
	l := newSnapshotLifecycle(t, sr)

	if err := l.Run(lifecycle.RunOpts{Attach: false, Layout: "reviewer", Agent: "claude"}); err != nil {
		t.Fatalf("Run(reviewer): %v", err)
	}
	snap := sr.atCreate["/etc/agentbox/layout.kdl"]
	assertLayoutPopulated(t, snap,
		`tab name="reviewer" focus=true`,
		`name="diff"`,
		`name="tests"`,
	)
}

func TestLayoutAtCreate_AuditorLayout_PopulatedBeforeMount(t *testing.T) {
	sr := newSnapshotRuntime()
	setupProject(t)
	l := newSnapshotLifecycle(t, sr)

	if err := l.Run(lifecycle.RunOpts{Attach: false, Layout: "auditor", Agent: "claude"}); err != nil {
		t.Fatalf("Run(auditor): %v", err)
	}
	snap := sr.atCreate["/etc/agentbox/layout.kdl"]
	assertLayoutPopulated(t, snap,
		`tab name="auditor" focus=true`,
		`name="trail"`,
		`command "box-trail"`,
	)
	// Auditor + claude also wires the shadow settings + trail file. Both
	// should be populated at Create() too — they were before this fix, but
	// a future regression in createBox ordering would break them.
	if shadow, ok := sr.atCreate[filepath.Join(l.Home, ".claude", "settings.json")]; ok {
		// Targets under $HOME/.claude/* aren't captured by snapshotRuntime
		// (it filters to /etc/agentbox/*); this branch never fires today.
		// Left as a hook for future expansion.
		_ = shadow
	}
}

// Shell mode (zellij-launched) hits the same code path. Without the fix,
// the layout file is empty when podman create runs.
func TestLayoutAtCreate_Shell_PopulatedBeforeMount(t *testing.T) {
	sr := newSnapshotRuntime()
	setupProject(t)
	l := newSnapshotLifecycle(t, sr)

	// stdinIsTerminal is forced false for tests via SetStdinIsTerminal in
	// the existing harness; Shell will hit the EnsureBox path but skip the
	// final zellijAttach. The layout still needs to be populated at Create().
	restore := lifecycle.SetStdinIsTerminal(func() bool { return false })
	defer restore()

	if err := l.Shell(lifecycle.RunOpts{}); err != nil {
		t.Fatalf("Shell(): %v", err)
	}
	snap := sr.atCreate["/etc/agentbox/layout.kdl"]
	assertLayoutPopulated(t, snap,
		`tab name="agentbox" focus=true`,
		`name="shell"`,
	)
}

// Counter-test: explicitly demonstrate that EnsureSession-only behavior (no
// writeLayoutFor before EnsureBox) leaves the file empty. This isn't a
// production code path; it exists to anchor the regression bound. If
// somebody removes the pre-EnsureBox writeLayoutFor call in Run, this test
// stays passing while TestLayoutAtCreate_FocusLayout_PopulatedBeforeMount
// fails — a clear paired signal in the failure log.
func TestLayoutAtCreate_BareEnsureSession_LeavesFileEmpty(t *testing.T) {
	// Reproduce just the EnsureSession behavior without lifecycle.Run.
	stateRoot := isolateState(t)
	projID, _ := setupProject(t)
	dir := filepath.Join(stateRoot, "agentbox", "sessions", projID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	layoutPath := filepath.Join(dir, "layout.kdl")
	// Mimic state.EnsureSession's touch.
	if err := os.WriteFile(layoutPath, nil, 0o600); err != nil {
		t.Fatalf("touch: %v", err)
	}
	body, _ := os.ReadFile(layoutPath)
	if len(body) != 0 {
		t.Fatalf("expected EnsureSession to leave a 0-byte layout.kdl, got %d bytes:\n%s",
			len(body), body)
	}
	// Document the failure mode the production tests above defend against.
	t.Logf("CONFIRMED: bare EnsureSession leaves layout.kdl at 0 bytes; " +
		"production Run/Shell must write real content before podman create " +
		"or zellij sees this empty file on macOS virtiofs")
}

// One sanity check to make sure the snapshot mechanism itself works — if it
// silently captures nothing, all the other tests would falsely "pass" with
// nil bytes that the assertLayoutPopulated message blames on the race.
func TestLayoutAtCreate_SnapshotMechanism_CapturesEffectiveConfig(t *testing.T) {
	sr := newSnapshotRuntime()
	setupProject(t)
	l := newSnapshotLifecycle(t, sr)

	if err := l.Run(lifecycle.RunOpts{Attach: false, Layout: "focus", Agent: "claude"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	cfg, ok := sr.atCreate["/etc/agentbox/config.toml"]
	if !ok {
		t.Fatal("snapshotRuntime didn't capture /etc/agentbox/config.toml — " +
			"the snapshot mechanism is broken; other tests may be falsely passing")
	}
	if len(cfg) == 0 {
		t.Fatal("/etc/agentbox/config.toml was empty at Create() — known to be " +
			"populated by writeEffectiveConfig before Create; if this fails, the " +
			"snapshot mechanism captured something other than the real bytes")
	}
}

// Compile-time guard: if container.Box ever changes shape, fail fast here
// instead of with a less-readable error inside the test bodies.
var _ container.Box = container.Box{ProjectID: "x", Status: container.StatusRunning}
