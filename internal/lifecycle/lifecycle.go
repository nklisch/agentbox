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

	"github.com/nklisch/agentbox/internal/config"
	"github.com/nklisch/agentbox/internal/container"
	"github.com/nklisch/agentbox/internal/exitcode"
	"github.com/nklisch/agentbox/internal/kits"
	"github.com/nklisch/agentbox/internal/project"
	"github.com/nklisch/agentbox/internal/runspec"
	"github.com/nklisch/agentbox/internal/state"
)

// Lifecycle orchestrates container lifecycle: create/start/exec/ls/rm.
type Lifecycle struct {
	Cfg     config.Config
	Runtime container.Runtime
	Builder *kits.Builder
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
	l.degradeNetworkMode()

	projID, projAbs, err := project.Resolve()
	if err != nil {
		return container.Box{}, exitcode.Wrap(exitcode.Generic, err)
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
		return l.createBox(projID, projAbs, opts)
	default:
		return container.Box{}, exitcode.New(exitcode.Generic,
			"unexpected box status %q", box.Status)
	}
}

// createBox resolves the agent + kits, ensures the kit image exists,
// writes session state, and creates+starts the container.
func (l *Lifecycle) createBox(projID, projAbs string, opts EnsureOpts) (container.Box, error) {
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

// degradeNetworkMode prints a warning and falls back to "open" when safe/allowlist
// is configured, because those modes require Phase 6 infrastructure.
// This mutates l.Cfg (value receiver stores, caller must use method receiver).
func (l *Lifecycle) degradeNetworkMode() {
	if l.Cfg.Network.Mode == "safe" || l.Cfg.Network.Mode == "allowlist" {
		fmt.Fprintf(l.Stderr,
			"warning: network mode %q is not yet implemented; falling back to 'open' for this run (Phase 6 will add it)\n",
			l.Cfg.Network.Mode)
		l.Cfg.Network.Mode = "open"
	}
}

// writeEffectiveConfig serialises Cfg as TOML and writes to the session dir.
func writeEffectiveConfig(projID string, cfg config.Config) error {
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(cfg); err != nil {
		return err
	}
	return state.WriteEffectiveConfig(projID, buf.Bytes())
}

// RunOpts controls the Run command.
type RunOpts struct {
	Agent   string
	Kits    []string
	Fresh   bool
	Attach  bool
	Network string // override Cfg.Network.Mode for this run
}

// Run is the high-level run command. If Attach is true (default), execs
// into the box with an interactive zsh after creating/starting. If false,
// just ensures and exits.
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
	if !opts.Attach {
		fmt.Fprintf(l.Stdout, "%s (%s)\n", box.ProjectID, box.Status)
		return nil
	}
	return l.shellInto(box)
}

// Shell is currently identical to Run with an interactive shell. P4 will
// diverge them: Run launches the agent via zellij; Shell stays as bare zsh.
func (l *Lifecycle) Shell(opts RunOpts) error {
	return l.Run(opts)
}

// shellInto execs an interactive zsh in the running box, inheriting stdio.
func (l *Lifecycle) shellInto(box container.Box) error {
	code, err := l.Runtime.Exec(container.ContainerName(box.ProjectID), container.ExecOpts{
		Argv:        []string{l.Cfg.Shell.Shell},
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

// Attach drops into an interactive shell in a running box. P3 implementation
// is equivalent to Exec with the default shell + i+t flags.
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
	return l.shellInto(box)
}

// LsFilter is the rich filter for `agentbox ls`.
type LsFilter struct {
	All     bool
	Project string // exact match on Box.Project
	Agent   string // exact match on Box.Agent
	Kit     string // membership in Box.Kits
}

// Ls returns boxes matching the filter.
func (l *Lifecycle) Ls(f LsFilter) ([]container.Box, error) {
	boxes, err := l.Runtime.Ls(f.All)
	if err != nil {
		return nil, exitcode.Wrap(exitcode.Generic, err)
	}
	out := boxes[:0]
	for _, b := range boxes {
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
	if err := l.Runtime.Rm(container.ContainerName(projID), true); err != nil {
		return exitcode.Wrap(exitcode.Generic, err)
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
	if !force && len(boxes) > 0 {
		return exitcode.New(exitcode.InvalidArgs,
			"would remove %d box(es); pass --force to skip confirmation", len(boxes))
	}
	var firstErr error
	for _, b := range boxes {
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
