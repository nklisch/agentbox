# Design: Phase 3 — Container lifecycle (run / shell / exec / attach / ls / rm)

## Overview

Phase 3 turns agentbox into a *real* CLI: it can create per-project boxes, exec into
them, list them, and clean them up. The kit images Phase 2 produces are finally used.
No zellij yet — `agentbox run` drops the user into a bare zsh shell. After Phase 3 you
can:

- `agentbox run` in a project directory — creates the box (building the kit image first
  if needed via Phase 2's Builder) and drops into zsh.
- `agentbox run --no-attach` — creates and starts, exits 0 without execing in.
- `agentbox shell` — same as run for now (Phase 4 will diverge them with zellij).
- `agentbox exec <id> <cmd>` — one-off command in a live box, with `-i/-t/-w/--shell`.
- `agentbox attach <id>` — interactive zsh in a running box (P4 will swap zellij in).
- `agentbox ls [--all] [--json] [--project|--agent|--kit ...]` — enumerate boxes.
- `agentbox rm <id> | --all [--force] [--keep-state]` — tear down + clean state.
- Identifier resolution accepts `<id>` (12-char), `<id-prefix>`, `agentbox-<id>`, or `.`.

The pattern is consistent with Phase 2: a `Runtime` port in a domain package, a
`PodmanRuntime` adapter, and a thin `lifecycle` orchestrator that the CLI calls.

## Cross-cutting decisions

- **One new domain package: `internal/container`.** Holds `Runtime` (the port), `Box`
  (the value type returned by Inspect/Ls), `ExecOpts` (Exec input), and `PodmanRuntime`
  (the adapter). Mirrors Phase 2's `internal/kits` structure.
- **One orchestrator package: `internal/lifecycle`.** Holds `Lifecycle` with methods
  `EnsureBox`, `Run`, `Shell`, `Exec`, `Attach`, `Ls`, `Rm`, plus `ResolveID` for
  identifier resolution. Tested with a fake Runtime.
- **Lifecycle owns both Runtime and Builder.** `EnsureBox` calls `Builder.Build` first
  to make sure the kit image exists (cache hit makes this fast), then `Runtime.Create`
  + `Runtime.Start`. The CLI's RunE remains thin.
- **`runspec.PodmanCreateArgs` gets a new `EnvVars []KV` field.** Currently only
  `EnvNames` exists for secrets-by-name. Phase 3 needs `-e NAME=VALUE` for non-secret
  context (`AGENTBOX_PROJECT_ID`, `AGENTBOX_PROJECT`, etc., for `box info` to display).
  This is an additive, backward-compatible change to Phase 1's runspec. ToShell renders
  the new field; BuildPodmanCreateArgs populates it from BuildInput. Phase 1's existing
  dry-run output gains those lines for free.
- **Network modes off/open only in P3.** `safe`/`allowlist` Fail Fast with exit code 6:
  "network mode 'safe' requires Phase 6; use 'off' or 'open' for now." This avoids
  silently doing the wrong thing.
- **Identifier resolution is in lifecycle, not its own package.** `Lifecycle.ResolveID`
  takes the user input and returns a 12-char ProjectID. Lives next to Lifecycle because
  it needs Runtime.Ls for prefix matching.
- **CLI tests use a fake Runtime injected via a constructor seam.** The CLI package
  exposes a package-level `newLifecycle` function (defaulting to the real one) that
  tests can override. Same shape as Phase 1's pattern; keeps cli_test.go cobra-style.

---

## Implementation Units

### Unit 1: `internal/runspec/runspec.go` — add `EnvVars` field

**File**: `internal/runspec/runspec.go` (edit existing)

Add to `PodmanCreateArgs`:

```go
type PodmanCreateArgs struct {
    Name     string
    Labels   []KV
    Mounts   []Mount
    Workdir  string
    EnvNames []string // -e NAME (no value, by name only — secrets policy)
    EnvVars  []KV     // -e NAME=VALUE (non-secret context: AGENTBOX_*, etc.)
    CPUs     int
    Memory   string
    PIDs     int
    CapDrop  []string
    SecOpt   []string
    Network  string
    Image    string
    Argv     []string
}
```

Update `BuildPodmanCreateArgs` to populate `EnvVars` from `BuildInput`. Add a constant
ordered list of the agentbox context env vars:

```go
// In BuildPodmanCreateArgs after Labels are built:
args.EnvVars = []KV{
    {Key: "AGENTBOX_PROJECT_ID", Value: in.ProjectID},
    {Key: "AGENTBOX_PROJECT",    Value: in.ProjectName},
    {Key: "AGENTBOX_AGENT",      Value: in.Agent},
    {Key: "AGENTBOX_KITS",       Value: strings.Join(in.Kits, ",")},
    {Key: "AGENTBOX_KIT_IMAGE",  Value: args.Image},
    {Key: "AGENTBOX_NETWORK",    Value: cfg.Network.Mode},
    {Key: "AGENTBOX_CREATED",    Value: in.Created.UTC().Format(time.RFC3339)},
    {Key: "AGENTBOX_SAVED_DIR",  Value: in.StateDir + "/saved"},  // for box save
}
```

Update `ToShell` to render `EnvVars`:

```go
// After the existing EnvNames loop, before the image line:
for _, e := range p.EnvVars {
    fmt.Fprintf(&b, "  -e %q \\\n", e.Key+"="+e.Value)
}
```

**Implementation notes**:
- `EnvVars` is for non-secret context. Per CLAUDE.md the CLI never reads or logs secret
  values; `EnvVars` carries values, but only ones agentbox itself computed (project_id,
  basename, agent name, etc.). No risk of leaking user secrets.
- The order matters for deterministic dry-run output and for label-style consistency.

**Acceptance Criteria**:
- [ ] `BuildPodmanCreateArgs(...)` returns args with all 8 `AGENTBOX_*` `EnvVars` populated.
- [ ] `args.ToShell("podman")` output now contains `-e "AGENTBOX_PROJECT_ID=<id>"` etc.
- [ ] Existing `EnvNames` (`-e NAME`) lines still appear correctly in ToShell output.
- [ ] Phase 1 runspec tests still pass (extend them to assert the new env vars).
- [ ] Phase 1 cli_test.go's `TestRunDryRun` still passes; assert output now contains
      `AGENTBOX_PROJECT_ID=`.

---

### Unit 2: `internal/state/dir.go` — session helpers

**File**: `internal/state/dir.go` (edit existing)

Add helpers for managing per-project session state:

```go
// EnsureSession creates the session dir for projectID with mode 0700 and
// touches the history file (so it can be bind-mounted as rw).
func EnsureSession(projectID string) (string, error) {
    dir, err := SessionDir(projectID)
    if err != nil {
        return "", err
    }
    if err := EnsureDir(dir); err != nil {
        return "", err
    }
    history := filepath.Join(dir, "history")
    if _, err := os.Stat(history); errors.Is(err, fs.ErrNotExist) {
        if err := os.WriteFile(history, nil, 0o600); err != nil {
            return "", err
        }
    }
    return dir, nil
}

// RemoveSession deletes the session dir for projectID. Idempotent: missing
// dir is not an error.
func RemoveSession(projectID string) error {
    dir, err := SessionDir(projectID)
    if err != nil {
        return err
    }
    if err := os.RemoveAll(dir); err != nil {
        return err
    }
    return nil
}

// WriteEffectiveConfig writes the merged config to <state-dir>/sessions/<id>/effective-config.toml.
// The file is mounted ro into the box at /etc/agentbox/config.toml.
func WriteEffectiveConfig(projectID string, body []byte) error {
    dir, err := SessionDir(projectID)
    if err != nil {
        return err
    }
    if err := EnsureDir(dir); err != nil {
        return err
    }
    return os.WriteFile(filepath.Join(dir, "effective-config.toml"), body, 0o600)
}
```

**Implementation notes**:
- `EnsureSession` must touch `history` because Phase 1's runspec mounts it as `rw` —
  podman fails to bind-mount a non-existent source.
- `WriteEffectiveConfig` takes `[]byte` (caller marshals via `toml.NewEncoder`) so
  the state package doesn't grow a TOML dependency.

**Acceptance Criteria**:
- [ ] `EnsureSession` creates the dir and history file.
- [ ] `RemoveSession` returns nil on missing dir.
- [ ] `WriteEffectiveConfig` writes to the right path.
- [ ] Test isolation via `t.Setenv("XDG_DATA_HOME", t.TempDir())`.

---

### Unit 3: `internal/container/types.go`

**File**: `internal/container/types.go` (new)

```go
package container

import (
    "io"
    "time"
)

// Status represents a container's current state.
type Status string

const (
    StatusRunning Status = "running"
    StatusStopped Status = "stopped"
    StatusMissing Status = "missing"
)

// Box describes an agentbox container as projected from podman labels.
type Box struct {
    ProjectID string    `json:"project_id"`
    Project   string    `json:"project"`
    CWD       string    `json:"cwd"`
    Agent     string    `json:"agent"`
    Kits      []string  `json:"kits"`
    KitImage  string    `json:"kit_image"`
    Status    Status    `json:"status"`
    Created   time.Time `json:"created"`
}

// ContainerName returns the canonical podman container name for a box.
// Mirrors project.ContainerName ("agentbox-<id>"). Defined here so the
// container package is self-contained.
func ContainerName(projectID string) string {
    return "agentbox-" + projectID
}

// ExecOpts controls Runtime.Exec.
type ExecOpts struct {
    Argv        []string
    Workdir     string
    Interactive bool
    TTY         bool
    Stdin       io.Reader
    Stdout      io.Writer
    Stderr      io.Writer
}
```

**Acceptance Criteria**:
- [ ] `Status` constants are typed strings (`"running"`, `"stopped"`, `"missing"`).
- [ ] `Box.Kits` JSON-marshals as an array.
- [ ] `Box.Created` round-trips via JSON.

---

### Unit 4: `internal/container/runtime.go` — Runtime port

**File**: `internal/container/runtime.go` (new)

```go
package container

import (
    "github.com/nklisch/agentbox/internal/runspec"
)

// Runtime is the port over a container engine (podman or docker). The
// PodmanRuntime adapter implements it via os/exec; tests use a fake.
type Runtime interface {
    // Create creates a container per args. Container is in "stopped" state
    // (per SPEC.md's `sleep infinity` model). Returns nil if a container
    // with the same name already exists; caller checks via Inspect first.
    Create(args runspec.PodmanCreateArgs) error

    // Start moves a stopped container to running. No-op if already running.
    Start(name string) error

    // Stop stops a running container. No-op if already stopped.
    Stop(name string) error

    // Inspect returns the Box for `name`. Status=Missing if no such container.
    Inspect(name string) (Box, error)

    // Exec runs a command inside a running container, streaming stdio per
    // ExecOpts. Returns the command's exit code (or an error if exec itself
    // failed to start).
    Exec(name string, opts ExecOpts) (int, error)

    // Ls returns all agentbox-labeled boxes. all=true includes stopped ones;
    // all=false returns running only.
    Ls(all bool) ([]Box, error)

    // Rm removes a container. force=true sends SIGKILL first if running.
    // Idempotent: missing container returns nil.
    Rm(name string, force bool) error
}
```

**Implementation Notes**:
- Returning `(int, error)` from `Exec` lets the CLI propagate the inner command's exit
  code (a failing test or a non-zero `cat` is the user's signal, not a CLI error).
  Errors from this return are reserved for "exec itself couldn't start" (e.g., container
  missing).
- `Inspect` returning `Box{Status: StatusMissing}` (no error) for absent containers is
  intentional: matches the "missing → continue" branch in SPEC.md's lifecycle.
- `Ls` filtering is just by `all`; rich filtering (project/agent/kit) happens in lifecycle.

**Acceptance Criteria**:
- [ ] Compiles. The interface is exported.
- [ ] No `os/exec` or `io` imports — types only.

---

### Unit 5: `internal/container/podman.go` — adapter

**File**: `internal/container/podman.go` (new)

```go
package container

import (
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "os/exec"
    "strings"
    "time"

    "github.com/nklisch/agentbox/internal/runspec"
)

// PodmanRuntime shells out to `podman` (or `docker`).
type PodmanRuntime struct {
    Bin string // "podman" or "docker"
}

// NewPodmanRuntime returns a Runtime backed by the named binary.
func NewPodmanRuntime(bin string) *PodmanRuntime {
    return &PodmanRuntime{Bin: bin}
}

// Create runs `<bin> create [...args from PodmanCreateArgs]`.
func (r *PodmanRuntime) Create(args runspec.PodmanCreateArgs) error {
    argv := []string{"create", "--name", args.Name}
    for _, kv := range args.Labels {
        argv = append(argv, "--label", kv.Key+"="+kv.Value)
    }
    for _, m := range args.Mounts {
        argv = append(argv, "-v", m.Source+":"+m.Target+":"+m.Mode)
    }
    if args.Workdir != "" {
        argv = append(argv, "-w", args.Workdir)
    }
    if args.CPUs > 0 {
        argv = append(argv, "--cpus", fmt.Sprintf("%d", args.CPUs))
    }
    if args.Memory != "" {
        argv = append(argv, "--memory", args.Memory)
    }
    if args.PIDs > 0 {
        argv = append(argv, "--pids-limit", fmt.Sprintf("%d", args.PIDs))
    }
    for _, c := range args.CapDrop {
        argv = append(argv, "--cap-drop", c)
    }
    for _, s := range args.SecOpt {
        argv = append(argv, "--security-opt", s)
    }
    if args.Network != "" {
        argv = append(argv, "--network", args.Network)
    }
    for _, e := range args.EnvNames {
        argv = append(argv, "-e", e)
    }
    for _, e := range args.EnvVars {
        argv = append(argv, "-e", e.Key+"="+e.Value)
    }
    argv = append(argv, args.Image)
    argv = append(argv, args.Argv...)

    cmd := exec.Command(r.Bin, argv...)
    cmd.Stdout = io.Discard
    cmd.Stderr = io.Discard // Quiet; errors surfaced via exit code below.
    if err := cmd.Run(); err != nil {
        return fmt.Errorf("%s create: %w", r.Bin, err)
    }
    return nil
}

// Start runs `<bin> start <name>`.
func (r *PodmanRuntime) Start(name string) error {
    cmd := exec.Command(r.Bin, "start", name)
    cmd.Stdout = io.Discard
    cmd.Stderr = io.Discard
    return cmd.Run()
}

// Stop runs `<bin> stop <name>`.
func (r *PodmanRuntime) Stop(name string) error {
    cmd := exec.Command(r.Bin, "stop", name)
    cmd.Stdout = io.Discard
    cmd.Stderr = io.Discard
    return cmd.Run()
}

// Inspect returns the Box for name. Status=Missing if no such container.
func (r *PodmanRuntime) Inspect(name string) (Box, error) {
    cmd := exec.Command(r.Bin, "inspect", "--type", "container",
        "--format", "{{json .}}", name)
    out, err := cmd.Output()
    if err != nil {
        var ee *exec.ExitError
        if errors.As(err, &ee) {
            // Non-zero from inspect = container not found.
            return Box{Status: StatusMissing}, nil
        }
        return Box{}, err
    }
    return parseInspect(out)
}

// inspectFormat is the subset we need from `podman inspect`.
type inspectFormat struct {
    Name   string `json:"Name"`
    State  struct {
        Running bool   `json:"Running"`
        Status  string `json:"Status"`
    } `json:"State"`
    Config struct {
        Labels map[string]string `json:"Labels"`
        Image  string            `json:"Image"`
    } `json:"Config"`
    Created string `json:"Created"`
}

func parseInspect(b []byte) (Box, error) {
    var raw inspectFormat
    if err := json.Unmarshal(b, &raw); err != nil {
        return Box{}, fmt.Errorf("parse inspect: %w", err)
    }
    return boxFromLabels(raw.Config.Labels, raw.State.Running, raw.Created), nil
}

func boxFromLabels(labels map[string]string, running bool, created string) Box {
    box := Box{
        ProjectID: labels["agentbox.project_id"],
        Project:   labels["agentbox.project"],
        CWD:       labels["agentbox.cwd"],
        Agent:     labels["agentbox.agent"],
        KitImage:  labels["agentbox.kit_image"],
    }
    if k := labels["agentbox.kits"]; k != "" {
        box.Kits = strings.Split(k, ",")
    }
    if running {
        box.Status = StatusRunning
    } else {
        box.Status = StatusStopped
    }
    // Created may be the container's creation time or the agentbox label;
    // prefer the label when present (deterministic).
    if c := labels["agentbox.created"]; c != "" {
        if t, err := time.Parse(time.RFC3339, c); err == nil {
            box.Created = t
        }
    } else if created != "" {
        if t, err := time.Parse(time.RFC3339Nano, created); err == nil {
            box.Created = t
        }
    }
    return box
}

// Exec runs a command in a running container, streaming stdio per opts.
func (r *PodmanRuntime) Exec(name string, opts ExecOpts) (int, error) {
    argv := []string{"exec"}
    if opts.Interactive {
        argv = append(argv, "-i")
    }
    if opts.TTY {
        argv = append(argv, "-t")
    }
    if opts.Workdir != "" {
        argv = append(argv, "-w", opts.Workdir)
    }
    argv = append(argv, name)
    argv = append(argv, opts.Argv...)

    cmd := exec.Command(r.Bin, argv...)
    cmd.Stdin = opts.Stdin
    cmd.Stdout = opts.Stdout
    cmd.Stderr = opts.Stderr
    if err := cmd.Run(); err != nil {
        var ee *exec.ExitError
        if errors.As(err, &ee) {
            return ee.ExitCode(), nil
        }
        return -1, fmt.Errorf("%s exec: %w", r.Bin, err)
    }
    return 0, nil
}

// Ls returns all agentbox-labeled boxes.
func (r *PodmanRuntime) Ls(all bool) ([]Box, error) {
    argv := []string{"ps", "--filter", "label=agentbox=1", "--format", "{{.Names}}"}
    if all {
        argv = append(argv[:1], append([]string{"-a"}, argv[1:]...)...)
    }
    cmd := exec.Command(r.Bin, argv...)
    out, err := cmd.Output()
    if err != nil {
        return nil, fmt.Errorf("%s ps: %w", r.Bin, err)
    }
    var boxes []Box
    for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
        line = strings.TrimSpace(line)
        if line == "" {
            continue
        }
        box, err := r.Inspect(line)
        if err != nil {
            continue // skip unreadable
        }
        if box.Status == StatusMissing {
            continue
        }
        boxes = append(boxes, box)
    }
    return boxes, nil
}

// Rm removes a container. force=true uses --force.
func (r *PodmanRuntime) Rm(name string, force bool) error {
    argv := []string{"rm"}
    if force {
        argv = append(argv, "--force")
    }
    argv = append(argv, name)
    cmd := exec.Command(r.Bin, argv...)
    cmd.Stdout = io.Discard
    cmd.Stderr = io.Discard
    if err := cmd.Run(); err != nil {
        var ee *exec.ExitError
        if errors.As(err, &ee) {
            // Already absent — non-fatal.
            return nil
        }
        return err
    }
    return nil
}
```

**Implementation Notes**:
- `Ls` calls `Inspect` per container to project labels into `Box`. This is two passes
  (one `podman ps`, then one `podman inspect` per name), simple and correct. `podman ps
  --format` with all labels would be one pass but the label rendering syntax is brittle
  across versions; per-container inspect is the boring choice.
- `Exec` returns the inner command's exit code via `ee.ExitCode()`. The CLI uses this
  as agentbox's own exit code when execing user commands.
- `Stop` is unused in P3's CLI flow but defined for symmetry and because `Rm(force=true)`
  needs to stop first internally — letting podman handle that is fine.
- `Inspect` uses `--type container` because `podman inspect` ambiguously inspects images
  and containers; we always mean container.

**Acceptance Criteria**:
- [ ] Compiles. (Behavior is verified end-to-end via the ROADMAP test checkpoint.)
- [ ] `Inspect` of a missing container returns `(Box{Status: Missing}, nil)`, not an error.
- [ ] `Exec` returns the inner command's exit code as `int` (verified manually).
- [ ] No imports of cobra or `internal/cli`.

---

### Unit 6: `internal/lifecycle/lifecycle.go`

**File**: `internal/lifecycle/lifecycle.go` (new)

```go
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
    if err := l.guardNetworkMode(); err != nil {
        return container.Box{}, err
    }

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

// guardNetworkMode rejects safe/allowlist modes in P3.
func (l *Lifecycle) guardNetworkMode() error {
    switch l.Cfg.Network.Mode {
    case "safe", "allowlist":
        return exitcode.New(exitcode.Network,
            "network mode %q requires Phase 6; use 'off' or 'open' for now",
            l.Cfg.Network.Mode)
    }
    return nil
}

// writeEffectiveConfig serialises Cfg as TOML and writes to the session dir.
func writeEffectiveConfig(projID string, cfg config.Config) error {
    var buf bytes.Buffer
    if err := toml.NewEncoder(&buf).Encode(cfg); err != nil {
        return err
    }
    return state.WriteEffectiveConfig(projID, buf.Bytes())
}

// Run is the high-level run command. If Attach is true (default), execs
// into the box with an interactive zsh after creating/starting. If false,
// just ensures and exits.
type RunOpts struct {
    Agent    string
    Kits     []string
    Fresh    bool
    Attach   bool
    Network  string // override Cfg.Network.Mode for this run
}

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

// Exec runs a one-off command in a running box.
type ExecOpts struct {
    Input        string // resolved via ResolveID
    Argv         []string
    Workdir      string
    Interactive  bool
    TTY          bool
    UseShell     bool // wrap argv in $SHELL -c '<argv joined>'
}

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

// Rm removes a box and its session state.
type RmOpts struct {
    Input     string // single project_id (mutually exclusive with All)
    All       bool
    Force     bool
    KeepState bool
}

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
```

**Implementation Notes**:
- `EnsureBox` is idempotent on Status (running → no-op; stopped → start; missing → create).
- The mount-source-exists check (`os.Stat`) before create maps mount-failure-during-create
  to exit code 7 per CLI.md, which is more useful than the generic podman error.
- `Run` with `--no-attach` prints `<project_id> (<status>)` so scripts can capture the id.
- The CLI is responsible for wiring `os.Stdin`/`Stdout`/`Stderr` into the lifecycle's
  Exec calls; lifecycle uses them directly (no buffering needed for streaming).
- `--all` confirmation: P3 keeps it simple — if `--force` is not set and there are boxes,
  exit 2 with a message. Tests use `--force` to avoid interactive prompts. (A real
  prompt could be added later but isn't needed for the test checkpoint.)

**Acceptance Criteria**:
- [ ] `EnsureBox` with a running box returns the existing box (no extra create).
- [ ] `EnsureBox` with a stopped box calls `Start` then returns running box.
- [ ] `EnsureBox` with missing box calls `Builder.Build`, then `Create`+`Start`.
- [ ] `EnsureBox` with `Fresh: true` calls `Rm` (force) and `RemoveSession` first.
- [ ] `Run(noAttach=true)` does NOT call `Runtime.Exec`; just prints `<id> (running)`.
- [ ] `Exec` against a non-running box returns exit code 4.
- [ ] `Exec` returns the inner command's exit code (not always 0).
- [ ] `Exec` with `UseShell=true` wraps argv in `[shell, -c, joined]`.
- [ ] `Ls` filters by project/agent/kit client-side.
- [ ] `Rm({All: true, Force: false})` with existing boxes errors with exit 2.
- [ ] `Rm({Input: ""})` errors with exit 2.
- [ ] Network mode `safe` returns exit 6 (until P6).

---

### Unit 7: `internal/lifecycle/idresolve.go`

**File**: `internal/lifecycle/idresolve.go` (new)

```go
package lifecycle

import (
    "regexp"
    "sort"
    "strings"

    "github.com/nklisch/agentbox/internal/exitcode"
    "github.com/nklisch/agentbox/internal/project"
)

var hexID = regexp.MustCompile(`^[a-f0-9]{12}$`)

// ResolveID resolves a user-provided string to a 12-char ProjectID:
//   - "."                  → project_id of current $PWD
//   - "agentbox-<id>"      → strips the prefix
//   - 12-char hex          → returned as-is
//   - shorter prefix       → unique prefix-match against existing boxes
// Errors:
//   - exit 4 (NotFound):    no match (when prefix-matching against boxes)
//   - exit 2 (InvalidArgs): empty input or ambiguous prefix
func (l *Lifecycle) ResolveID(input string) (string, error) {
    input = strings.TrimSpace(input)
    if input == "" {
        return "", exitcode.New(exitcode.InvalidArgs, "project_id required")
    }

    // Dot → current $PWD's project_id (no Runtime call needed).
    if input == "." {
        id, _, err := project.Resolve()
        if err != nil {
            return "", exitcode.Wrap(exitcode.Generic, err)
        }
        return id, nil
    }

    // Strip "agentbox-" prefix if present.
    input = strings.TrimPrefix(input, "agentbox-")

    // Exact 12-char hex → no lookup needed.
    if hexID.MatchString(input) {
        return input, nil
    }

    // Prefix match against existing boxes (running OR stopped).
    boxes, err := l.Runtime.Ls(true)
    if err != nil {
        return "", exitcode.Wrap(exitcode.Generic, err)
    }
    var matches []string
    for _, b := range boxes {
        if strings.HasPrefix(b.ProjectID, input) {
            matches = append(matches, b.ProjectID)
        }
    }
    sort.Strings(matches)
    switch len(matches) {
    case 0:
        return "", exitcode.New(exitcode.NotFound,
            "no box matches %q", input)
    case 1:
        return matches[0], nil
    default:
        return "", exitcode.New(exitcode.InvalidArgs,
            "ambiguous prefix %q matches: %s", input, strings.Join(matches, ", "))
    }
}
```

**Acceptance Criteria**:
- [ ] `ResolveID(".")` returns the project_id of `os.Getwd()`.
- [ ] `ResolveID("agentbox-abc123def456")` returns `"abc123def456"`.
- [ ] `ResolveID("abc123def456")` returns it as-is (no Runtime call).
- [ ] `ResolveID("abc")` with one matching box returns the full id.
- [ ] `ResolveID("abc")` with two matching boxes errors with exit 2 + matches list.
- [ ] `ResolveID("nope")` errors with exit 4.
- [ ] `ResolveID("")` errors with exit 2.

---

### Unit 8: tests for container + lifecycle

**Files**:
- `internal/container/container_test.go` — small; tests `parseInspect` + `boxFromLabels`
- `internal/lifecycle/lifecycle_test.go` — orchestration tests with `fakeRuntime`
- `internal/lifecycle/idresolve_test.go` — id resolution edge cases

`fakeRuntime` lives in `lifecycle_test.go` (test-only; container_test.go doesn't need it):

```go
type fakeRuntime struct {
    boxes  map[string]container.Box // by name
    failOn map[string]error         // method-name → error to return
    calls  []string                 // method names, for ordering assertions
    execStub func(name string, opts container.ExecOpts) (int, error)
}

func (r *fakeRuntime) Create(args runspec.PodmanCreateArgs) error {
    r.calls = append(r.calls, "Create")
    if err := r.failOn["Create"]; err != nil {
        return err
    }
    box := container.Box{
        ProjectID: idFromName(args.Name),
        Status:    container.StatusStopped,
    }
    // Populate from Labels for realism:
    for _, kv := range args.Labels {
        switch kv.Key {
        case "agentbox.project":
            box.Project = kv.Value
        case "agentbox.cwd":
            box.CWD = kv.Value
        // ... etc.
        }
    }
    r.boxes[args.Name] = box
    return nil
}

// (etc. for Start, Stop, Inspect, Exec, Ls, Rm)
```

Cases to cover (lifecycle_test.go):
- `TestEnsureBox_RunningBoxIsNoop` — Inspect returns running; no Create/Start calls.
- `TestEnsureBox_StoppedBoxStarts` — Inspect returns stopped; calls Start.
- `TestEnsureBox_MissingBoxCreates` — Inspect returns missing; calls Build, Create, Start.
- `TestEnsureBox_FreshRemovesFirst` — Fresh=true: Rm called before Create.
- `TestEnsureBox_BuildFailureReturnsKitBuildExit` — Builder.Build error → exit 5.
- `TestEnsureBox_MountMissingReturnsExit7` — fixture creates an args with non-existent
  mount source; verify exit 7.
- `TestEnsureBox_NetworkSafeRejected` — config has mode=safe; exit 6.
- `TestRun_NoAttach` — Run(Attach=false): stdout has `<id> (running)`; no Exec call.
- `TestRun_AttachInteractive` — Run(Attach=true): Exec called with `[zsh]` + i+t.
- `TestExec_PassesExitCode` — execStub returns 7; Exec returns *exitcode.Err{Code:7}.
- `TestExec_NotRunning` — Inspect returns stopped; exit 4.
- `TestExec_UseShellWraps` — UseShell=true: argv wrapped in `[zsh, -c, ...]`.
- `TestLs_FiltersClientSide` — All/Project/Agent/Kit filter combinations.
- `TestRm_RemovesContainerAndState` — Rm called; state.RemoveSession called.
- `TestRm_KeepStateSkipsRemoveSession` — KeepState=true: state dir survives.
- `TestRm_All_RequiresForce` — without --force: exit 2.
- `TestRm_NoArgs` — Input="" and All=false: exit 2.

Cases for idresolve_test.go are listed above in Unit 7.

Test isolation: `t.Setenv("XDG_DATA_HOME", t.TempDir())` so state writes don't pollute.

**Acceptance Criteria**:
- [ ] `go test ./internal/lifecycle/...` passes.
- [ ] `go test ./internal/container/...` passes.
- [ ] No podman invocations from tests (fakeRuntime exclusively).

---

### Unit 9: CLI wiring — `internal/cli/{run,shell,exec,attach,ls,rm}.go`

**Files**: edit `internal/cli/run.go`; replace stubs at `internal/cli/{shell,attach,exec,ls,rm}.go`.

**Shared helper** — add to `internal/cli/root.go` or a new `internal/cli/lifecycle.go`:

```go
// newLifecycle is overridable at test time so the CLI can use a fakeRuntime.
var newLifecycle = func(cfg config.Config) (*lifecycle.Lifecycle, error) {
    home, _ := os.UserHomeDir()
    rt := container.NewPodmanRuntime(cfg.Runtime)

    cfgDir, _ := os.UserConfigDir()
    userKitsDir := filepath.Join(cfgDir, "agentbox", "kits")
    reg := kits.NewRegistry(builtinkits.FS(), userKitsDir)
    cache, err := kits.NewCache()
    if err != nil {
        return nil, err
    }
    builder := &kits.Builder{
        Registry: reg, Cache: cache,
        Runner:   kits.NewPodmanRunner(cfg.Runtime),
        Version:  version.Version,
    }
    return &lifecycle.Lifecycle{
        Cfg: cfg, Runtime: rt, Builder: builder, Home: home,
        Stdout: os.Stdout, Stderr: os.Stderr,
    }, nil
}
```

**`internal/cli/run.go`** — preserve --dry-run path, add real path:

```go
func newRunCmd() *cobra.Command {
    var (
        fresh        bool
        kitsFlag     string
        networkFlag  string
        noAttach     bool
        detachOnExit bool
    )
    cmd := &cobra.Command{
        Use:   "run [agent]",
        Short: "Create or attach to the per-project box, launch the configured agent",
        Args:  cobra.MaximumNArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            res, err := loadConfig()
            if err != nil {
                return err
            }
            cfg := res.Config

            if global.DryRun {
                return runDryRun(cmd, cfg, args, kitsFlag, networkFlag)
            }

            l, err := newLifecycle(cfg)
            if err != nil {
                return exitcode.Wrap(exitcode.Generic, err)
            }
            opts := lifecycle.RunOpts{
                Fresh:   fresh,
                Attach:  !noAttach,
                Network: networkFlag,
            }
            if len(args) == 1 {
                opts.Agent = args[0]
            }
            if kitsFlag != "" {
                opts.Kits = strings.Split(kitsFlag, ",")
            }
            _ = detachOnExit // P3 doesn't auto-stop on exit; future phase.
            return l.Run(opts)
        },
    }
    cmd.Flags().BoolVar(&fresh, "fresh", false, "remove any existing box for this project before creating")
    cmd.Flags().StringVar(&kitsFlag, "kits", "", "override the kit list (comma-separated)")
    cmd.Flags().StringVar(&networkFlag, "network", "", "override network.mode for this run")
    cmd.Flags().BoolVar(&noAttach, "no-attach", false, "create/start the box but don't attach")
    cmd.Flags().BoolVar(&detachOnExit, "detach-on-exit", false, "stop the container when the agent process exits")
    return cmd
}

// runDryRun preserves Phase 1's dry-run behavior. Extracted so RunE stays clean.
func runDryRun(cmd *cobra.Command, cfg config.Config, args []string, kitsFlag, networkFlag string) error {
    // ... existing Phase 1 dry-run body, unchanged ...
}
```

**`internal/cli/shell.go`**:

```go
func newShellCmd() *cobra.Command {
    var (
        fresh    bool
        noZellij bool
    )
    cmd := &cobra.Command{
        Use:   "shell",
        Short: "Bare interactive shell in the per-project box",
        Args:  cobra.NoArgs,
        RunE: func(cmd *cobra.Command, args []string) error {
            res, err := loadConfig()
            if err != nil {
                return err
            }
            l, err := newLifecycle(res.Config)
            if err != nil {
                return exitcode.Wrap(exitcode.Generic, err)
            }
            _ = noZellij // P3 has no zellij; flag accepted for future-compat.
            return l.Shell(lifecycle.RunOpts{Fresh: fresh, Attach: true})
        },
    }
    cmd.Flags().BoolVar(&fresh, "fresh", false, "remove any existing box first")
    cmd.Flags().BoolVar(&noZellij, "no-zellij", false, "skip zellij entirely (no-op in P3)")
    return cmd
}
```

**`internal/cli/exec.go`**:

```go
func newExecCmd() *cobra.Command {
    var (
        interactive bool
        tty         bool
        workdir     string
        useShell    bool
    )
    cmd := &cobra.Command{
        Use:   "exec <project_id> <command> [args...]",
        Short: "Run a one-off command inside a live box",
        Args:  cobra.MinimumNArgs(2),
        DisableFlagParsing: false, // need flags before args
        RunE: func(cmd *cobra.Command, args []string) error {
            res, err := loadConfig()
            if err != nil {
                return err
            }
            l, err := newLifecycle(res.Config)
            if err != nil {
                return exitcode.Wrap(exitcode.Generic, err)
            }
            return l.Exec(lifecycle.ExecOpts{
                Input:       args[0],
                Argv:        args[1:],
                Workdir:     workdir,
                Interactive: interactive,
                TTY:         tty,
                UseShell:    useShell,
            })
        },
    }
    cmd.Flags().BoolVarP(&interactive, "interactive", "i", false, "attach stdin")
    cmd.Flags().BoolVarP(&tty, "tty", "t", false, "allocate a TTY")
    cmd.Flags().StringVarP(&workdir, "workdir", "w", "", "working directory (default: project mount path)")
    cmd.Flags().BoolVar(&useShell, "shell", false, "wrap command in $SHELL -c '<cmd>'")
    return cmd
}
```

**`internal/cli/attach.go`**:

```go
func newAttachCmd() *cobra.Command {
    cmd := &cobra.Command{
        Use:   "attach <project_id>",
        Short: "Reattach to a running box",
        Args:  cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            res, err := loadConfig()
            if err != nil {
                return err
            }
            l, err := newLifecycle(res.Config)
            if err != nil {
                return exitcode.Wrap(exitcode.Generic, err)
            }
            return l.Attach(args[0])
        },
    }
    return cmd
}
```

**`internal/cli/ls.go`** — human + JSON output:

```go
func newLsCmd() *cobra.Command {
    var (
        all      bool
        project  string
        agentN   string
        kit      string
    )
    cmd := &cobra.Command{
        Use:   "ls",
        Short: "List boxes",
        RunE: func(cmd *cobra.Command, args []string) error {
            res, err := loadConfig()
            if err != nil {
                return err
            }
            l, err := newLifecycle(res.Config)
            if err != nil {
                return exitcode.Wrap(exitcode.Generic, err)
            }
            boxes, err := l.Ls(lifecycle.LsFilter{All: all, Project: project, Agent: agentN, Kit: kit})
            if err != nil {
                return err
            }
            if global.JSON {
                enc := json.NewEncoder(cmd.OutOrStdout())
                for _, b := range boxes {
                    if err := enc.Encode(b); err != nil {
                        return exitcode.Wrap(exitcode.Generic, err)
                    }
                }
                return nil
            }
            return printLsTable(cmd.OutOrStdout(), boxes)
        },
    }
    cmd.Flags().BoolVarP(&all, "all", "a", false, "include stopped boxes")
    cmd.Flags().StringVar(&project, "project", "", "filter by project name")
    cmd.Flags().StringVar(&agentN, "agent", "", "filter by agent")
    cmd.Flags().StringVar(&kit, "kit", "", "filter by kit membership")
    return cmd
}

func printLsTable(w io.Writer, boxes []container.Box) error {
    if len(boxes) == 0 {
        return nil
    }
    fmt.Fprintf(w, "%-12s  %-20s  %-8s  %-30s  %-9s  %s\n",
        "PROJECT_ID", "PROJECT", "AGENT", "KITS", "STATUS", "CREATED")
    for _, b := range boxes {
        fmt.Fprintf(w, "%-12s  %-20s  %-8s  %-30s  %-9s  %s\n",
            b.ProjectID, truncate(b.Project, 20), truncate(b.Agent, 8),
            truncate(strings.Join(b.Kits, ","), 30), string(b.Status),
            b.Created.Format("2006-01-02T15:04"))
    }
    return nil
}

func truncate(s string, n int) string {
    if len(s) <= n {
        return s
    }
    return s[:n-1] + "…"
}
```

**`internal/cli/rm.go`**:

```go
func newRmCmd() *cobra.Command {
    var (
        all       bool
        force     bool
        keepState bool
    )
    cmd := &cobra.Command{
        Use:   "rm <project_id>",
        Short: "Stop and remove boxes + their session state",
        Args:  cobra.MaximumNArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            res, err := loadConfig()
            if err != nil {
                return err
            }
            l, err := newLifecycle(res.Config)
            if err != nil {
                return exitcode.Wrap(exitcode.Generic, err)
            }
            opts := lifecycle.RmOpts{All: all, Force: force, KeepState: keepState}
            if len(args) == 1 {
                opts.Input = args[0]
            }
            return l.Rm(opts)
        },
    }
    cmd.Flags().BoolVar(&all, "all", false, "remove every agentbox box")
    cmd.Flags().BoolVar(&force, "force", false, "skip confirmation when using --all")
    cmd.Flags().BoolVar(&keepState, "keep-state", false, "remove container but keep session state dir")
    return cmd
}
```

**`internal/cli/stub.go` edit** — drop `newShellCmd`, `newAttachCmd`, `newExecCmd`,
`newLsCmd`, `newRmCmd`. Only `notImplementedRunE` and `stubCmd` (and the helper signature
function `stubCmd`) should remain — though if no stubs reference them after the edit,
delete the file entirely. **Verify**: `grep -rn newShellCmd internal/cli/stub.go` finds nothing
after the edit; the same names live in their new files; `grep -rn newShellCmd
internal/cli/` finds exactly one definition each.

**Implementation Notes**:
- `--detach-on-exit` is parsed but unused in P3 (the post-agent state machine lands later).
  Discarding via `_ = detachOnExit` keeps the flag in the CLI surface for future use.
- `--no-zellij` on shell is similarly accepted but no-op'd.
- The `truncate` helper uses `…` (single Unicode char) so the column widths stay correct.
- `agentbox ls` JSON output is NDJSON (one JSON object per line) per CLI.md.
- `agentbox rm` with no args returns exit 2 (handled by lifecycle.Rm).

**Acceptance Criteria**:
- [ ] `agentbox run --dry-run` still works (Phase 1 contract preserved).
- [ ] `agentbox run --no-attach` creates a box and exits 0 (verified via test checkpoint).
- [ ] `agentbox ls --json` produces NDJSON with the schema in CLI.md.
- [ ] `agentbox ls` (no flags) prints a table with header + rows.
- [ ] `agentbox exec <id> <cmd>` returns the inner command's exit code.
- [ ] `agentbox exec <id>` (no command) errors with exit 2.
- [ ] `agentbox rm` (no args) errors with exit 2.
- [ ] `agentbox rm <prefix>` succeeds when the prefix is unique.
- [ ] All Phase 1 cli_test.go cases still pass.

---

### Unit 10: integration tests in `internal/cli/cli_test.go`

**File**: `internal/cli/cli_test.go` (extend)

Add tests that override `newLifecycle` with a fake to drive the CLI without podman:

```go
func TestRun_NoAttach_FakeRuntime(t *testing.T) {
    fake := newFakeRuntime()
    fake.boxes["agentbox-XXXXXXXXXXXX"] = container.Box{Status: container.StatusMissing}
    // ... build a Lifecycle with fake runtime + fake builder
    origNewLifecycle := newLifecycle
    newLifecycle = func(cfg config.Config) (*lifecycle.Lifecycle, error) { return fakeL, nil }
    defer func() { newLifecycle = origNewLifecycle }()

    // Run --no-attach in a temp project dir.
    t.Chdir(t.TempDir())
    stdout, stderr, err := runCmd(t, "run", "--no-attach")
    if err != nil { t.Fatal(err) }
    // ... assertions on stdout
}
```

Tests to add:
- `TestRun_NoAttach_HappyPath`
- `TestExec_RoutesArgsToLifecycle`
- `TestLs_JSON_NDJSON`
- `TestLs_HumanTable_HasHeader`
- `TestRm_NoArgsExits2`
- `TestRm_AllRequiresForce`
- `TestAttach_NotRunningExits4`

**Acceptance Criteria**:
- [ ] All cli_test.go tests pass without invoking podman.
- [ ] Integration tests use the seam-overridden `newLifecycle`.

---

## Implementation Order

| # | Unit | Depends on |
|---:|------|-----------|
| 1 | runspec.go edit (EnvVars) | — |
| 2 | state/dir.go edit | — |
| 3 | container/types.go | — |
| 4 | container/runtime.go | Unit 3, runspec |
| 5 | container/podman.go | Units 3, 4 |
| 6 | lifecycle/lifecycle.go | Units 1, 2, 3, 4, kits |
| 7 | lifecycle/idresolve.go | Unit 6, project (Phase 1) |
| 8 | container/lifecycle tests | Units 1–7 |
| 9 | cli wiring + stub edits | Units 6, 7 + cli (Phase 1) |
| 10 | cli_test.go extensions | Unit 9 |

Layer 1 (Units 1–5) can land in one orchestration; Layer 2 (Units 6–10) in a second.
Or all in one Sonnet agent if context fits (recommended — patterns are fully repetitive
from Phase 2's Runner+adapter pattern).

---

## Verification Checklist

```sh
cd /home/nathan/dev/agent-box

# Static + unit
go vet ./...
go test ./...
go build ./...
make build

# Ports & Adapters preserved
grep -rn 'spf13/cobra\|internal/cli' \
  internal/version/ internal/exitcode/ internal/state/ internal/project/ \
  internal/config/ internal/runspec/ internal/doctor/ internal/kits/ \
  internal/builtinkits/ internal/container/ internal/lifecycle/
# (only the kits_test.go comment line should match; nothing in non-test source)

# ROADMAP Phase 3 test checkpoint (real podman)
mkdir -p /tmp/abx-proj && cd /tmp/abx-proj && touch hello.txt
/home/nathan/dev/agent-box/agentbox run --no-attach
/home/nathan/dev/agent-box/agentbox ls
/home/nathan/dev/agent-box/agentbox exec . pwd                     # /tmp/abx-proj
/home/nathan/dev/agent-box/agentbox exec . cat hello.txt           # ""
/home/nathan/dev/agent-box/agentbox exec . sh -c 'echo hi > /tmp/in-box'
ID=$(/home/nathan/dev/agent-box/agentbox ls --json | head -1 | jq -r .project_id)
/home/nathan/dev/agent-box/agentbox attach "${ID:0:6}" </dev/null   # exits cleanly
/home/nathan/dev/agent-box/agentbox rm .
/home/nathan/dev/agent-box/agentbox ls --all                        # box gone
test ! -d ~/.local/share/agentbox/sessions/$ID
```

Phase 3 is "done" for autopilot purposes when all of the above succeed and `go test
./...` is green.
