package lifecycle

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"golang.org/x/term"

	"github.com/nklisch/agentbox/internal/config"
	"github.com/nklisch/agentbox/internal/container"
	"github.com/nklisch/agentbox/internal/exitcode"
	"github.com/nklisch/agentbox/internal/kits"
	"github.com/nklisch/agentbox/internal/network"
	"github.com/nklisch/agentbox/internal/project"
	"github.com/nklisch/agentbox/internal/runspec"
	"github.com/nklisch/agentbox/internal/seccomp"
	"github.com/nklisch/agentbox/internal/state"
	"github.com/nklisch/agentbox/internal/zellij"
)

// NetworkManager is the port over the per-project network orchestrator.
// Implemented by *network.Manager in production; a fake in tests.
// Defined here to avoid lifecycle importing network's concrete type directly
// while still keeping the dependency clean.
type NetworkManager interface {
	SpecFor(cfg config.Config, projectID string) network.Spec
	Setup(spec network.Spec) (network.Info, error)
	Teardown(spec network.Spec) error
}

// Lifecycle orchestrates container lifecycle: create/start/exec/ls/rm.
type Lifecycle struct {
	Cfg     config.Config
	Runtime container.Runtime
	Builder *kits.Builder
	Network NetworkManager // nil means no-op (off/open modes work without it)
	Home    string
	Stdout  io.Writer
	Stderr  io.Writer
}

// EnsureOpts controls box creation.
type EnsureOpts struct {
	Agent string   // overrides Cfg.DefaultAgent if non-empty
	Kits  []string // overrides agent.Kits if non-empty
	Fresh bool
}

// EnsureBox guarantees a running box exists for the current $PWD's project.
// If Fresh, removes any existing box + state first. Returns the resolved Box.
func (l *Lifecycle) EnsureBox(opts EnsureOpts) (container.Box, error) {
	projID, projAbs, err := project.Resolve()
	if err != nil {
		return container.Box{}, exitcode.Wrap(exitcode.Generic, err)
	}

	// Setup network + sidecar before interacting with the box container.
	// For running/stopped boxes, this is idempotent (sidecar already running).
	netInfo, err := l.setupNetwork(projID)
	if err != nil {
		return container.Box{}, exitcode.Wrap(exitcode.Generic,
			fmt.Errorf("network setup: %w", err))
	}

	name := container.ContainerName(projID)

	if opts.Fresh {
		if err := l.Runtime.Rm(name, true); err != nil {
			return container.Box{}, exitcode.Wrap(exitcode.Generic,
				fmt.Errorf("fresh: rm: %w", err))
		}
		if err := state.RemoveSession(projID); err != nil {
			return container.Box{}, exitcode.Wrap(exitcode.Generic,
				fmt.Errorf("fresh: remove state: %w", err))
		}
	}

	box, err := l.Runtime.Inspect(name)
	if err != nil {
		return container.Box{}, exitcode.Wrap(exitcode.Generic, err)
	}
	switch box.Status {
	case container.StatusRunning:
		return box, nil
	case container.StatusStopped:
		if err := l.Runtime.Start(name); err != nil {
			return container.Box{}, exitcode.Wrap(exitcode.Generic, err)
		}
		return l.Runtime.Inspect(name)
	case container.StatusMissing:
		return l.createBox(projID, projAbs, opts, netInfo)
	default:
		return container.Box{}, exitcode.New(exitcode.Generic,
			"unexpected box status %q", box.Status)
	}
}

// createBox resolves the agent + kits, ensures the kit image exists,
// writes session state, and creates+starts the container.
func (l *Lifecycle) createBox(projID, projAbs string, opts EnsureOpts, netInfo network.Info) (container.Box, error) {
	agent := l.Cfg.DefaultAgent
	if opts.Agent != "" {
		agent = opts.Agent
	}
	a, ok := l.Cfg.Agents[agent]
	if !ok {
		return container.Box{}, exitcode.New(exitcode.InvalidArgs,
			"agent %q not defined in [agents.*]", agent)
	}
	chosenKits := a.Kits
	if len(opts.Kits) > 0 {
		chosenKits = opts.Kits
	}
	if len(chosenKits) == 0 {
		return container.Box{}, exitcode.New(exitcode.InvalidArgs,
			"no kits specified for agent %q", agent)
	}

	// Build (or cache-hit) the kit image.
	buildRes, err := l.Builder.Build(chosenKits, kits.BuildOpts{
		Stdout: l.Stdout,
		Stderr: l.Stderr,
	})
	if err != nil {
		return container.Box{}, exitcode.Wrap(exitcode.KitBuild, err)
	}

	// Prepare session state.
	stateDir, err := state.EnsureSession(projID)
	if err != nil {
		return container.Box{}, exitcode.Wrap(exitcode.Generic, err)
	}
	if err := writeEffectiveConfig(projID, l.Cfg); err != nil {
		return container.Box{}, exitcode.Wrap(exitcode.Generic, err)
	}

	// If containers.enable, materialize the seccomp profile on disk so the
	// runtime spec's bind-mount has a target.
	var seccompPath string
	if l.Cfg.Containers.Enable {
		p, err := seccomp.EnsureContainersProfile()
		if err != nil {
			return container.Box{}, exitcode.Wrap(exitcode.Generic, err)
		}
		seccompPath = p
	}

	// Build runspec args and create.
	in := runspec.BuildInput{
		ProjectID:   projID,
		ProjectAbs:  projAbs,
		ProjectName: filepath.Base(projAbs),
		Agent:       agent,
		Kits:        buildRes.Kits, // resolved kit list, in topo order
		HomeDir:     l.Home,
		StateDir:    stateDir,
		Created:     time.Now(),
		SidecarDNS:  netInfo.SidecarDNS, // CoreDNS sidecar IP for --dns (safe/allowlist)
		SeccompPath: seccompPath,
	}
	args, err := runspec.BuildPodmanCreateArgs(l.Cfg, in)
	if err != nil {
		return container.Box{}, exitcode.Wrap(exitcode.Generic, err)
	}

	// Validate every bind-mount source exists on the host (exit 7 per CLI.md).
	for _, m := range args.Mounts {
		if _, err := os.Stat(m.Source); errors.Is(err, os.ErrNotExist) {
			return container.Box{}, exitcode.New(exitcode.MountMissing,
				"mount source missing on host: %s", m.Source)
		}
	}

	if err := l.Runtime.Create(args); err != nil {
		return container.Box{}, exitcode.Wrap(exitcode.Generic, err)
	}
	if err := l.Runtime.Start(args.Name); err != nil {
		return container.Box{}, exitcode.Wrap(exitcode.Generic, err)
	}
	return l.Runtime.Inspect(args.Name)
}

// setupNetwork brings up the per-project network + CoreDNS sidecar, returning
// the Info that runspec.BuildInput needs (network name + DNS overrides).
// Replaces Phase 3's degradeNetworkMode warning + fallback.
// When Network is nil (should not happen in production), falls back gracefully.
func (l *Lifecycle) setupNetwork(projectID string) (network.Info, error) {
	if l.Network == nil {
		// Fallback: no network manager — use open/none per mode.
		switch l.Cfg.Network.Mode {
		case "off":
			return network.Info{NetworkName: "none"}, nil
		default:
			return network.Info{NetworkName: "bridge"}, nil
		}
	}
	spec := l.Network.SpecFor(l.Cfg, projectID)
	return l.Network.Setup(spec)
}

// writeEffectiveConfig serialises Cfg as TOML and writes to the session dir.
func writeEffectiveConfig(projID string, cfg config.Config) error {
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(cfg); err != nil {
		return err
	}
	return state.WriteEffectiveConfig(projID, buf.Bytes())
}

// writeLayout serialises a zellij layout and writes it to
// <state-dir>/sessions/<id>/layout.kdl (bind-mounted ro into the box at
// /etc/agentbox/layout.kdl). Called before zellijAttach so the file is never
// empty when zellij starts.
func writeLayout(projID, projAbs string, mode zellij.Mode, agentCmd []string, shellName string) error {
	dir, err := state.SessionDir(projID)
	if err != nil {
		return err
	}
	if err := state.EnsureDir(dir); err != nil {
		return err
	}
	body := zellij.GenerateKDL(zellij.Layout{
		Mode:       mode,
		AgentCmd:   agentCmd,
		ProjectAbs: projAbs,
		Shell:      shellName,
	})
	return os.WriteFile(filepath.Join(dir, "layout.kdl"), []byte(body), 0o600)
}

// stdinIsTerminal is a package-level variable so tests can swap it.
var stdinIsTerminal = func() bool { return isTerminal(os.Stdin) }

// SetStdinIsTerminal replaces the TTY-detection function used by Run/Shell/Attach.
// Returns a restore function the caller should defer. Intended for tests.
func SetStdinIsTerminal(fn func() bool) func() {
	orig := stdinIsTerminal
	stdinIsTerminal = fn
	return func() { stdinIsTerminal = orig }
}

// RunOpts controls the Run command.
type RunOpts struct {
	Agent    string
	Kits     []string
	Fresh    bool
	Attach   bool
	Network  string // override Cfg.Network.Mode for this run
	NoZellij bool   // skip zellij and use bare-shell exec (only meaningful for Shell)
}

// Run is the high-level run command. Always writes the ModeRun layout so
// that `agentbox attach .` can reconnect to the session later. If Attach is
// true (default), also opens a zellij session when stdin is a TTY. If false
// (--no-attach), just ensures the box is running and exits.
//
// Run is always zellij-launched in Phase 4. Users who want bare zsh should
// use `agentbox shell --no-zellij`.
func (l *Lifecycle) Run(opts RunOpts) error {
	if opts.Network != "" {
		l.Cfg.Network.Mode = opts.Network
	}
	box, err := l.EnsureBox(EnsureOpts{
		Agent: opts.Agent, Kits: opts.Kits, Fresh: opts.Fresh,
	})
	if err != nil {
		return err
	}

	// Resolve agent config to get the command for the layout.
	agent := l.Cfg.DefaultAgent
	if opts.Agent != "" {
		agent = opts.Agent
	}
	a, ok := l.Cfg.Agents[agent]
	if !ok {
		return exitcode.New(exitcode.InvalidArgs, "agent %q not defined", agent)
	}

	// Write the layout unconditionally so `agentbox attach .` can reconnect
	// even when --no-attach was used to start the box.
	if err := writeLayout(box.ProjectID, box.CWD, zellij.ModeRun, a.Cmd, l.Cfg.Shell.Shell); err != nil {
		return exitcode.Wrap(exitcode.Generic, err)
	}

	if !opts.Attach {
		fmt.Fprintf(l.Stdout, "%s (%s)\n", box.ProjectID, box.Status)
		return nil
	}
	if !stdinIsTerminal() {
		// Zellij needs a real TTY; fall back to liveness print.
		fmt.Fprintf(l.Stdout, "%s (%s)\n", box.ProjectID, box.Status)
		return nil
	}
	return l.zellijAttach(box)
}

// Shell opens an interactive session in the box. With a TTY and no --no-zellij
// flag, it launches a single-pane zellij session. With --no-zellij or without
// a TTY, it execs bare zsh (scripting-friendly path from Phase 3).
func (l *Lifecycle) Shell(opts RunOpts) error {
	if opts.Network != "" {
		l.Cfg.Network.Mode = opts.Network
	}
	box, err := l.EnsureBox(EnsureOpts{Fresh: opts.Fresh})
	if err != nil {
		return err
	}
	if !stdinIsTerminal() {
		// Both zellij and bare shell need a TTY for interactivity.
		fmt.Fprintf(l.Stdout, "%s (%s)\n", box.ProjectID, box.Status)
		return nil
	}
	if opts.NoZellij {
		return l.shellInto(box)
	}
	if err := writeLayout(box.ProjectID, box.CWD, zellij.ModeShell, nil, l.Cfg.Shell.Shell); err != nil {
		return exitcode.Wrap(exitcode.Generic, err)
	}
	return l.zellijAttach(box)
}

// shellInto execs zsh in the running box, inheriting stdio.
//
// TTY allocation tracks whether stdin is itself a terminal: when called from
// an interactive shell we pass `-it` to podman so the user gets line editing
// and signals; when stdin is redirected (a file, /dev/null, a pipe), we pass
// only `-i` so the inner shell reads from the redirected stream and exits on
// EOF instead of blocking on the pty.
func (l *Lifecycle) shellInto(box container.Box) error {
	tty := stdinIsTerminal()
	code, err := l.Runtime.Exec(container.ContainerName(box.ProjectID), container.ExecOpts{
		Argv:        []string{l.Cfg.Shell.Shell},
		Interactive: true,
		TTY:         tty,
		Stdin:       os.Stdin,
		Stdout:      os.Stdout,
		Stderr:      os.Stderr,
	})
	if err != nil {
		return exitcode.Wrap(exitcode.Generic, err)
	}
	if code != 0 {
		return &exitcode.Err{Code: code}
	}
	return nil
}

// zellijAttach execs `zellij --layout /etc/agentbox/layout.kdl attach -c agentbox`
// inside the box. The session name is the literal string "agentbox" — zellij
// creates it if missing, joins it if present (-c = create if not exists).
// Only called from TTY-gated paths; always passes TTY: true.
func (l *Lifecycle) zellijAttach(box container.Box) error {
	code, err := l.Runtime.Exec(container.ContainerName(box.ProjectID), container.ExecOpts{
		Argv: []string{
			"zellij",
			"--layout", "/etc/agentbox/layout.kdl",
			"attach", "-c", "agentbox",
		},
		Interactive: true,
		TTY:         true,
		Stdin:       os.Stdin,
		Stdout:      os.Stdout,
		Stderr:      os.Stderr,
	})
	if err != nil {
		return exitcode.Wrap(exitcode.Generic, err)
	}
	if code != 0 {
		return &exitcode.Err{Code: code}
	}
	return nil
}

// ExecOpts controls the Exec command.
type ExecOpts struct {
	Input       string // resolved via ResolveID
	Argv        []string
	Workdir     string
	Interactive bool
	TTY         bool
	UseShell    bool // wrap argv in $SHELL -c '<argv joined>'
}

// Exec runs a one-off command in a running box.
func (l *Lifecycle) Exec(opts ExecOpts) error {
	projID, err := l.ResolveID(opts.Input)
	if err != nil {
		return err
	}
	name := container.ContainerName(projID)
	box, err := l.Runtime.Inspect(name)
	if err != nil {
		return exitcode.Wrap(exitcode.Generic, err)
	}
	if box.Status != container.StatusRunning {
		return exitcode.New(exitcode.NotFound,
			"box %s is not running (status: %s)", projID, box.Status)
	}

	workdir := opts.Workdir
	if workdir == "" {
		workdir = box.CWD
	}

	argv := opts.Argv
	if opts.UseShell {
		argv = []string{l.Cfg.Shell.Shell, "-c", strings.Join(opts.Argv, " ")}
	}

	code, err := l.Runtime.Exec(name, container.ExecOpts{
		Argv:        argv,
		Workdir:     workdir,
		Interactive: opts.Interactive,
		TTY:         opts.TTY,
		Stdin:       os.Stdin,
		Stdout:      os.Stdout,
		Stderr:      os.Stderr,
	})
	if err != nil {
		return exitcode.Wrap(exitcode.Generic, err)
	}
	if code != 0 {
		return &exitcode.Err{Code: code}
	}
	return nil
}

// Attach connects to a running box. With a TTY on stdin it joins the existing
// zellij session (or creates one if the layout file is missing/empty). Without
// a TTY (scripted invocations, test checkpoints) it verifies liveness and
// exits — zellij needs a real TTY.
func (l *Lifecycle) Attach(input string) error {
	projID, err := l.ResolveID(input)
	if err != nil {
		return err
	}
	name := container.ContainerName(projID)
	box, err := l.Runtime.Inspect(name)
	if err != nil {
		return exitcode.Wrap(exitcode.Generic, err)
	}
	if box.Status != container.StatusRunning {
		return exitcode.New(exitcode.NotFound,
			"box %s is not running (status: %s)", projID, box.Status)
	}
	if !stdinIsTerminal() {
		// Liveness check — exit clean without blocking.
		fmt.Fprintf(l.Stdout, "%s (running)\n", box.ProjectID)
		return nil
	}
	// Regenerate the layout only when the file is empty or missing (e.g. if the
	// box was started via --no-attach before P4 wrote it).
	if sessionDir, serr := state.SessionDir(projID); serr == nil {
		layoutPath := filepath.Join(sessionDir, "layout.kdl")
		if info, ferr := os.Stat(layoutPath); ferr != nil || info.Size() == 0 {
			agent := box.Agent
			a, ok := l.Cfg.Agents[agent]
			var cmd []string
			if ok {
				cmd = a.Cmd
			}
			_ = writeLayout(projID, box.CWD, zellij.ModeRun, cmd, l.Cfg.Shell.Shell)
		}
	}
	return l.zellijAttach(box)
}

// LsFilter is the rich filter for `agentbox ls`.
type LsFilter struct {
	All     bool
	Project string // exact match on Box.Project
	Agent   string // exact match on Box.Agent
	Kit     string // membership in Box.Kits
}

// Ls returns boxes matching the filter. Always filters to role=box (or
// unlabeled, for backward compat with pre-Phase-6 boxes) so sidecar and
// netfilter containers don't appear in the user-facing list.
func (l *Lifecycle) Ls(f LsFilter) ([]container.Box, error) {
	boxes, err := l.Runtime.Ls(f.All)
	if err != nil {
		return nil, exitcode.Wrap(exitcode.Generic, err)
	}
	out := boxes[:0]
	for _, b := range boxes {
		// Filter out sidecar/netfilter containers by role label.
		// Empty role = pre-Phase-6 box; treat as "box" for backward compat.
		if b.Role != "" && b.Role != "box" {
			continue
		}
		if f.Project != "" && b.Project != f.Project {
			continue
		}
		if f.Agent != "" && b.Agent != f.Agent {
			continue
		}
		if f.Kit != "" && !contains(b.Kits, f.Kit) {
			continue
		}
		out = append(out, b)
	}
	return out, nil
}

// RmOpts controls the Rm command.
type RmOpts struct {
	Input     string // single project_id (mutually exclusive with All)
	All       bool
	Force     bool
	KeepState bool
}

// Rm removes a box and its session state.
func (l *Lifecycle) Rm(opts RmOpts) error {
	if opts.All {
		return l.rmAll(opts.Force, opts.KeepState)
	}
	if opts.Input == "" {
		return exitcode.New(exitcode.InvalidArgs,
			"agentbox rm requires <project_id> or --all")
	}
	projID, err := l.ResolveID(opts.Input)
	if err != nil {
		return err
	}
	return l.rmOne(projID, opts.KeepState)
}

func (l *Lifecycle) rmOne(projID string, keepState bool) error {
	// Remove the user-facing box container.
	if err := l.Runtime.Rm(container.ContainerName(projID), true); err != nil {
		return exitcode.Wrap(exitcode.Generic, err)
	}
	// Teardown the per-project network + sidecar (idempotent).
	if l.Network != nil {
		spec := l.Network.SpecFor(l.Cfg, projID)
		if err := l.Network.Teardown(spec); err != nil {
			// Non-fatal: log but don't fail the remove. The box container is
			// already gone; a stale sidecar/network is recoverable.
			fmt.Fprintf(l.Stderr, "warning: network teardown for %s: %v\n", projID, err)
		}
	}
	if !keepState {
		if err := state.RemoveSession(projID); err != nil {
			return exitcode.Wrap(exitcode.Generic, err)
		}
	}
	return nil
}

func (l *Lifecycle) rmAll(force, keepState bool) error {
	boxes, err := l.Runtime.Ls(true) // all = include stopped
	if err != nil {
		return exitcode.Wrap(exitcode.Generic, err)
	}
	// Filter to role=box only — sidecar/netfilter containers are cleaned up
	// per-box by Network.Teardown called from rmOne.
	var userBoxes []container.Box
	for _, b := range boxes {
		if b.Role == "" || b.Role == "box" {
			userBoxes = append(userBoxes, b)
		}
	}
	if !force && len(userBoxes) > 0 {
		return exitcode.New(exitcode.InvalidArgs,
			"would remove %d box(es); pass --force to skip confirmation", len(userBoxes))
	}
	var firstErr error
	for _, b := range userBoxes {
		if err := l.rmOne(b.ProjectID, keepState); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

// isTerminal reports whether f is connected to a real TTY (not /dev/null,
// not a pipe, not a regular file). Uses golang.org/x/term so /dev/null is
// correctly distinguished from a real terminal — both are character devices
// to the file mode, but only a terminal answers the TIOCGWINSZ ioctl.
func isTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}
