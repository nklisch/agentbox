# Design: Phase 4 — Zellij-in-box, layout generation, `box` helpers

## Overview

Phase 4 lights up the in-box terminal experience. After this phase, `agentbox run`
opens a zellij session with the agent in a 70% pane, a git ticker and `btm` stats
beneath it, and a separate shell tab. Detach with zellij's default binding (Ctrl+p d),
walk away, come back hours later, `agentbox attach .` reconnects to the same session
with state intact. `agentbox shell` with no flag opens a single-pane zellij session
(so `agentbox shell` and `agentbox run` both produce a familiar "agentbox" prompt
the test checkpoint's `expect` script can match). `agentbox shell --no-zellij` keeps
the bare-zsh path from Phase 3 for scripting.

Two `box` helpers from Phase 2 get final treatment:
- **`box info`** is rewritten to read `/etc/agentbox/config.toml` (now genuinely
  mounted via Phase 3) and produce the exact format documented in CLI.md.
- **`box save`** finally has a saved/ host mount so files survive `agentbox rm`.

## Cross-cutting decisions

- **One new package: `internal/zellij`.** Holds KDL types and `GenerateKDL`. Pure
  string output, no I/O. Naming is honest about what it does — if we ever swap
  multiplexers, we'd write a new package, not pretend at abstraction here.
- **Layout file is regenerated on every run/shell/attach, mounted ro.** Cheap to
  rewrite (a few KB), and ensures the layout always matches the current config /
  agent. Zellij ignores `--layout` when joining an existing session, so the
  rewrite is harmless when reattaching.
- **Zellij KDL syntax — VERIFY AT WRITE TIME.** Don't trust this design's KDL
  examples without checking against the actual base kit's zellij (0.44.1). The
  implementer should run, against the freshly-built base image:
  ```
  podman run --rm <base-image> zellij setup --dump-layout default
  ```
  …to get a canonical reference, and adjust this design's KDL accordingly.
  Treat the examples below as starting templates, not as gospel.
- **Git pane runs unconditionally even outside git repos.** `watch` keeps polling;
  if `git status` errors, the user sees an error string in that pane and the rest
  of the layout still works. Adding repo detection would add complexity for marginal
  gain — agentbox is for development, dev dirs are git repos in the common case.
- **Session name is the literal string `"agentbox"`.** Zellij singleton per box
  per ARCHITECTURE.md. `zellij attach -c <name>` "creates the session named name
  if it doesn't exist." So the same invocation works for first connect and reattach.
- **TTY guard from Phase 3 stays.** Lifecycle.Run/Shell/Attach short-circuit to
  liveness-print when `!isTerminal(os.Stdin)`. Zellij needs a real TTY; without one,
  fall back gracefully.

---

## Implementation Units

### Unit 1: `internal/runspec/runspec.go` — saved/ mount

**File**: `internal/runspec/runspec.go` (edit existing)

In `BuildPodmanCreateArgs`, the existing block:

```go
if in.StateDir != "" {
    args.Mounts = append(args.Mounts,
        Mount{Source: in.StateDir + "/history",                 Target: "/root/.local/share/agentbox-history", Mode: "rw"},
        Mount{Source: in.StateDir + "/layout.kdl",              Target: "/etc/agentbox/layout.kdl",            Mode: "ro"},
        Mount{Source: in.StateDir + "/effective-config.toml",   Target: "/etc/agentbox/config.toml",           Mode: "ro"},
    )
}
```

Add a fourth line for `saved/`:

```go
        Mount{Source: in.StateDir + "/saved", Target: "/root/.local/share/agentbox-saved", Mode: "rw"},
```

This matches the `AGENTBOX_SAVED_DIR` env var that `BuildPodmanCreateArgs` already
populates (`in.StateDir + "/saved"`), so `box save` writes to a directory that
genuinely persists on the host.

**Acceptance Criteria**:
- [ ] `args.Mounts` for a typical `BuildInput` includes a Mount with
      `Source = "<stateDir>/saved"`, `Target = "/root/.local/share/agentbox-saved"`,
      `Mode = "rw"`.
- [ ] Existing tests (`runspec_test.go`) still pass; extend one or two to assert
      the new mount appears.

---

### Unit 2: `internal/state/dir.go` — EnsureSession creates saved/

**File**: `internal/state/dir.go` (edit existing)

`EnsureSession` already touches `history`, `layout.kdl`, and `effective-config.toml`
(per Phase 3's deviation note). Add `saved/` directory creation:

```go
func EnsureSession(projectID string) (string, error) {
    dir, err := SessionDir(projectID)
    if err != nil {
        return "", err
    }
    if err := EnsureDir(dir); err != nil {
        return "", err
    }
    if err := EnsureDir(filepath.Join(dir, "saved")); err != nil {
        return "", err
    }
    // ... existing touch logic for history / layout.kdl / effective-config.toml ...
    return dir, nil
}
```

**Acceptance Criteria**:
- [ ] After `EnsureSession("abc123def456")`, `<state-dir>/sessions/abc123def456/saved/`
      exists as a directory with mode 0700.
- [ ] Calling `EnsureSession` twice doesn't error.

---

### Unit 3: `internal/zellij/types.go`

**File**: `internal/zellij/types.go` (new package)

```go
package zellij

// Mode selects the layout shape.
type Mode int

const (
    // ModeRun is the full agentbox run layout: agent main pane (70%) +
    // git ticker + btm stats + shell tab.
    ModeRun Mode = iota
    // ModeShell is the single-pane shell layout used by `agentbox shell`
    // when --no-zellij is not set.
    ModeShell
)

// Layout describes everything GenerateKDL needs to produce a layout file.
type Layout struct {
    Mode       Mode
    AgentCmd   []string // e.g. ["claude", "--dangerously-skip-permissions"]
    ProjectAbs string   // absolute path of the project, used as cwd for panes
    Shell      string   // "zsh" / "bash" / "fish"; from cfg.Shell.Shell
}
```

**Acceptance Criteria**:
- [ ] `Mode`'s zero value is `ModeRun`.
- [ ] No file I/O in this file.

---

### Unit 4: `internal/zellij/kdl.go` — KDL generator

**File**: `internal/zellij/kdl.go` (new)

```go
package zellij

import (
    "fmt"
    "strings"
)

// GenerateKDL renders the zellij layout KDL for the given Layout.
//
// Schema reference: zellij 0.44.x. The implementer should verify the
// emitted output by feeding it to `zellij --layout <file>` against the
// base kit's zellij (0.44.1) before committing — KDL field names are
// fast-moving across zellij versions.
func GenerateKDL(l Layout) string {
    switch l.Mode {
    case ModeRun:
        return runLayout(l)
    case ModeShell:
        return shellLayout(l)
    default:
        return shellLayout(l)
    }
}

// runLayout: agent (70%) on top, git+stats (30%) on bottom; second tab is shell.
func runLayout(l Layout) string {
    var b strings.Builder
    fmt.Fprintln(&b, "// Generated by agentbox; regenerated on every run.")
    fmt.Fprintln(&b, "layout {")
    fmt.Fprintln(&b, `    tab name="agentbox" focus=true {`)
    fmt.Fprintln(&b, `        pane split_direction="horizontal" {`)
    // Agent pane — 70% top
    fmt.Fprintln(&b, `            pane size="70%" name="agent" {`)
    writeCommand(&b, "                ", l.AgentCmd)
    fmt.Fprintf(&b, "                cwd %q\n", l.ProjectAbs)
    fmt.Fprintln(&b, `            }`)
    // Bottom row — git + stats
    fmt.Fprintln(&b, `            pane split_direction="vertical" size="30%" {`)
    fmt.Fprintln(&b, `                pane name="git" {`)
    fmt.Fprintln(&b, `                    command "watch"`)
    fmt.Fprintln(&b, `                    args "-n" "2" "git" "status" "-s"`)
    fmt.Fprintf(&b, "                    cwd %q\n", l.ProjectAbs)
    fmt.Fprintln(&b, `                }`)
    fmt.Fprintln(&b, `                pane name="stats" {`)
    fmt.Fprintln(&b, `                    command "btm"`)
    fmt.Fprintln(&b, `                }`)
    fmt.Fprintln(&b, `            }`)
    fmt.Fprintln(&b, `        }`)
    fmt.Fprintln(&b, `    }`)

    // Tab 2: shell
    fmt.Fprintln(&b, `    tab name="shell" {`)
    fmt.Fprintln(&b, `        pane name="shell" {`)
    fmt.Fprintf(&b, "            command %q\n", l.Shell)
    fmt.Fprintf(&b, "            cwd %q\n", l.ProjectAbs)
    fmt.Fprintln(&b, `        }`)
    fmt.Fprintln(&b, `    }`)
    fmt.Fprintln(&b, `}`)
    return b.String()
}

// shellLayout: single pane with the user's shell.
func shellLayout(l Layout) string {
    var b strings.Builder
    fmt.Fprintln(&b, "// Generated by agentbox; regenerated on every run.")
    fmt.Fprintln(&b, "layout {")
    fmt.Fprintln(&b, `    tab name="agentbox" focus=true {`)
    fmt.Fprintln(&b, `        pane name="shell" {`)
    fmt.Fprintf(&b, "            command %q\n", l.Shell)
    fmt.Fprintf(&b, "            cwd %q\n", l.ProjectAbs)
    fmt.Fprintln(&b, `        }`)
    fmt.Fprintln(&b, `    }`)
    fmt.Fprintln(&b, `}`)
    return b.String()
}

// writeCommand writes `command "<cmd[0]>"` and (if more than one element)
// `args "..." "..."` indented with `indent`.
func writeCommand(b *strings.Builder, indent string, cmd []string) {
    if len(cmd) == 0 {
        fmt.Fprintf(b, "%scommand %q\n", indent, "zsh")
        return
    }
    fmt.Fprintf(b, "%scommand %q\n", indent, cmd[0])
    if len(cmd) > 1 {
        fmt.Fprintf(b, "%sargs", indent)
        for _, a := range cmd[1:] {
            fmt.Fprintf(b, " %q", a)
        }
        b.WriteByte('\n')
    }
}
```

**Implementation Notes**:
- KDL field name verification (per cross-cutting decisions above): if
  `zellij setup --dump-layout default` shows `command_args` instead of `args`,
  or `direction` instead of `split_direction`, adjust accordingly. Zellij has
  rotated through several KDL spellings; 0.44 is what the base kit installs.
- Comments at the top (`// Generated by agentbox`) are KDL line comments — KDL's
  comment syntax matches Rust/JS. Confirm zellij accepts them; if not, drop them.
- The `focus=true` on tab 1 makes zellij open with that tab focused.

**Acceptance Criteria**:
- [ ] `GenerateKDL(Layout{Mode: ModeRun, AgentCmd: ["claude", "--y"], ProjectAbs: "/p", Shell: "zsh"})`
      output contains `name="agentbox"`, `command "claude"`, `args "--y"`, `cwd "/p"`,
      and `name="shell"` with `command "zsh"`.
- [ ] `GenerateKDL(Layout{Mode: ModeShell, ...})` output contains a single tab
      with one pane running the configured shell.
- [ ] Output is deterministic (calling twice with same input produces byte-identical
      strings).
- [ ] `command` value is properly quoted via `%q` so paths/strings with spaces work.
- [ ] No bare `args` line is emitted when `len(AgentCmd) == 1`.

---

### Unit 5: `internal/zellij/kdl_test.go`

**File**: `internal/zellij/kdl_test.go` (new)

Golden tests that pin the expected KDL output for representative inputs.

```go
package zellij

import (
    "regexp"
    "strings"
    "testing"
)

func TestGenerateKDL_Run_HasAllPanes(t *testing.T) {
    out := GenerateKDL(Layout{
        Mode: ModeRun,
        AgentCmd: []string{"claude", "--dangerously-skip-permissions"},
        ProjectAbs: "/tmp/abx-proj",
        Shell: "zsh",
    })
    for _, frag := range []string{
        `name="agentbox"`,
        `name="agent"`,
        `name="git"`,
        `name="stats"`,
        `name="shell"`,
        `command "claude"`,
        `args "--dangerously-skip-permissions"`,
        `cwd "/tmp/abx-proj"`,
        `command "watch"`,
        `command "btm"`,
        `command "zsh"`,
    } {
        if !strings.Contains(out, frag) {
            t.Errorf("missing fragment %q in:\n%s", frag, out)
        }
    }
}

func TestGenerateKDL_Shell_SinglePane(t *testing.T) {
    out := GenerateKDL(Layout{Mode: ModeShell, ProjectAbs: "/p", Shell: "zsh"})
    // Only one tab (excluding the closing brace count).
    if got := strings.Count(out, "tab name="); got != 1 {
        t.Errorf("want 1 tab, got %d:\n%s", got, out)
    }
    // No agent / git / stats panes in shell mode.
    for _, forbidden := range []string{`name="agent"`, `name="git"`, `name="stats"`, `command "watch"`, `command "btm"`} {
        if strings.Contains(out, forbidden) {
            t.Errorf("unexpected fragment %q in shell-mode output:\n%s", forbidden, out)
        }
    }
}

func TestGenerateKDL_Deterministic(t *testing.T) {
    in := Layout{Mode: ModeRun, AgentCmd: []string{"a", "b"}, ProjectAbs: "/p", Shell: "zsh"}
    a := GenerateKDL(in)
    b := GenerateKDL(in)
    if a != b {
        t.Fatalf("non-deterministic output")
    }
}

func TestGenerateKDL_NoArgsWhenSingleCommand(t *testing.T) {
    out := GenerateKDL(Layout{Mode: ModeRun, AgentCmd: []string{"claude"}, ProjectAbs: "/p", Shell: "zsh"})
    // The agent pane block should have `command "claude"` but NO `args` line.
    agentPane := regexp.MustCompile(`(?s)pane size="70%" name="agent" \{(.*?)\}`).FindStringSubmatch(out)
    if len(agentPane) != 2 {
        t.Fatalf("agent pane block not found in:\n%s", out)
    }
    if strings.Contains(agentPane[1], "args") {
        t.Errorf("expected no args line for single-element AgentCmd; got:\n%s", agentPane[1])
    }
}
```

**Acceptance Criteria**:
- [ ] `go test ./internal/zellij/...` passes.

---

### Unit 6: `internal/lifecycle/lifecycle.go` — wire zellij into Run/Shell/Attach

**File**: `internal/lifecycle/lifecycle.go` (edit existing)

Add to `RunOpts`:

```go
type RunOpts struct {
    Agent    string
    Kits     []string
    Fresh    bool
    Attach   bool
    Network  string
    NoZellij bool // NEW: skip zellij and use bare shell exec (only meaningful for Shell)
}
```

Add a method to write the layout file alongside `writeEffectiveConfig`:

```go
// writeLayout serialises a zellij layout to <state-dir>/sessions/<id>/layout.kdl.
func writeLayout(projID, projAbs string, mode zellij.Mode, agentCmd []string, shellName string) error {
    dir, err := state.SessionDir(projID)
    if err != nil {
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
```

Update `Lifecycle.Run` (the agent path):

```go
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
    if !isTerminal(os.Stdin) {
        // Liveness print + exit; zellij needs a TTY.
        fmt.Fprintf(l.Stdout, "%s (%s)\n", box.ProjectID, box.Status)
        return nil
    }

    agent := l.Cfg.DefaultAgent
    if opts.Agent != "" {
        agent = opts.Agent
    }
    a, ok := l.Cfg.Agents[agent]
    if !ok {
        return exitcode.New(exitcode.InvalidArgs, "agent %q not defined", agent)
    }

    if err := writeLayout(box.ProjectID, box.CWD, zellij.ModeRun, a.Cmd, l.Cfg.Shell.Shell); err != nil {
        return exitcode.Wrap(exitcode.Generic, err)
    }
    return l.zellijAttach(box)
}
```

Update `Lifecycle.Shell`:

```go
func (l *Lifecycle) Shell(opts RunOpts) error {
    if opts.Network != "" {
        l.Cfg.Network.Mode = opts.Network
    }
    box, err := l.EnsureBox(EnsureOpts{Fresh: opts.Fresh})
    if err != nil {
        return err
    }
    if !isTerminal(os.Stdin) {
        // Bare-shell or zellij both need a TTY for interactivity.
        fmt.Fprintf(l.Stdout, "%s (%s)\n", box.ProjectID, box.Status)
        return nil
    }
    if opts.NoZellij {
        return l.shellInto(box) // existing P3 path
    }
    if err := writeLayout(box.ProjectID, box.CWD, zellij.ModeShell, nil, l.Cfg.Shell.Shell); err != nil {
        return exitcode.Wrap(exitcode.Generic, err)
    }
    return l.zellijAttach(box)
}
```

Update `Lifecycle.Attach`:

```go
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
        return exitcode.New(exitcode.NotFound, "box %s is not running (status: %s)", projID, box.Status)
    }
    if !isTerminal(os.Stdin) {
        // Liveness check, exit clean — preserves Phase 3's TTY contract.
        fmt.Fprintf(l.Stdout, "%s (running)\n", box.ProjectID)
        return nil
    }
    // Layout was written when the box was created; no need to re-render here.
    // But if the file is empty or missing (created via --no-zellij path), regenerate
    // a default ModeRun layout so attach has something coherent to use.
    sessionDir, _ := state.SessionDir(projID)
    layoutPath := filepath.Join(sessionDir, "layout.kdl")
    if info, err := os.Stat(layoutPath); err != nil || info.Size() == 0 {
        agent := box.Agent
        a, ok := l.Cfg.Agents[agent]
        cmd := []string(nil)
        if ok {
            cmd = a.Cmd
        }
        _ = writeLayout(projID, box.CWD, zellij.ModeRun, cmd, l.Cfg.Shell.Shell)
    }
    return l.zellijAttach(box)
}
```

Add `zellijAttach`:

```go
// zellijAttach execs `zellij --layout /etc/agentbox/layout.kdl attach -c agentbox`
// inside the box. The session name is the literal string "agentbox" — zellij
// creates it if missing, joins it if present.
func (l *Lifecycle) zellijAttach(box container.Box) error {
    code, err := l.Runtime.Exec(container.ContainerName(box.ProjectID), container.ExecOpts{
        Argv: []string{
            "zellij",
            "--layout", "/etc/agentbox/layout.kdl",
            "attach", "-c", "agentbox",
        },
        Interactive: true,
        TTY:         true, // we already verified isTerminal(stdin) above
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
```

Add the `zellij` import:

```go
import (
    // ... existing ...
    "github.com/nklisch/agentbox/internal/zellij"
)
```

**Implementation Notes**:
- `Run`'s bare-shell path is removed — Run is always `zellij`-launched in P4.
  `agentbox run` users who want bare zsh should use `agentbox shell --no-zellij`.
- `Attach` regenerates layout.kdl ONLY when the file is empty/missing. Normal flow
  (Run → Attach) finds the layout already written.
- `zellijAttach` always passes `TTY: true` because the path is gated on
  `isTerminal(os.Stdin)` upstream.

**Acceptance Criteria**:
- [ ] `Run({Attach: true})` writes layout.kdl with ModeRun and execs zellij when
      stdin is a TTY (verified via fakeRuntime in tests; the agent kits + cmd come
      from `cfg.Agents[agent]`).
- [ ] `Run({Attach: false})` does NOT write layout, does NOT exec zellij.
- [ ] `Run({Attach: true})` with non-TTY stdin prints liveness and exits.
- [ ] `Shell({NoZellij: true})` calls `shellInto` (bare zsh).
- [ ] `Shell({NoZellij: false})` writes layout.kdl with ModeShell and execs zellij.
- [ ] `Attach(id)` execs zellij when the box is running.
- [ ] `Attach(id)` against a stopped box returns exit 4.
- [ ] Lifecycle tests use the existing `fakeRuntime`; assertions inspect the
      `ExecOpts.Argv` to confirm `[zellij, --layout, ..., attach, -c, agentbox]`
      shape.

---

### Unit 7: `internal/cli/shell.go` — wire `--no-zellij`

**File**: `internal/cli/shell.go` (edit existing)

The `--no-zellij` flag is already declared but not used. Wire it through:

```go
RunE: func(cmd *cobra.Command, args []string) error {
    res, err := loadConfig()
    if err != nil {
        return err
    }
    l, err := newLifecycle(res.Config)
    if err != nil {
        return exitcode.Wrap(exitcode.Generic, err)
    }
    return l.Shell(lifecycle.RunOpts{
        Fresh:    fresh,
        Attach:   true,
        NoZellij: noZellij, // NEW: pass through to lifecycle
    })
},
```

**Acceptance Criteria**:
- [ ] `agentbox shell` (no flag) opens zellij (verified by `cli_test.go` against the
      fake runtime — assert ExecOpts.Argv starts with `[zellij, --layout, ...]`).
- [ ] `agentbox shell --no-zellij` calls the bare-shell path (assert ExecOpts.Argv
      is `[zsh]` or whatever cfg.Shell.Shell is).

---

### Unit 8: `internal/builtinkits/kits/base/box-info` — polish

**File**: `internal/builtinkits/kits/base/box-info` (rewrite to match CLI.md)

Match the exact format documented in CLI.md "box info" example:

```
project_id   a3f2c1d4e5b6
project      myapp
cwd          /home/nathan/dev/myapp
agent        claude
kits         polyglot, claude
kit_image    agentbox/8c3f1a2b4d5e
network      safe
mounts
  /home/nathan/dev/myapp     rw  (project)
  /root/.gitconfig            rw
  /root/.ssh                  ro
  /root/.claude               rw
resources
  cpus     4
  memory   8g
  pids     512
created      2026-05-04T14:32:00Z
```

Source data:
- `project_id`, `project`, `agent`, `kits`, `kit_image`, `network`, `created` —
  from `AGENTBOX_*` env vars (Phase 3 sets these).
- `cwd` — from `pwd` (the box runs with `-w $PWD`, so `pwd` returns the project
  abs path) OR from a new `AGENTBOX_CWD` env var (cleaner — propose adding).
- `mounts` — best-effort: list known mount paths inside the container. Use
  `findmnt` or just list specific known mount points (`/etc/agentbox/config.toml`,
  `/root/.gitconfig`, `/root/.ssh`, `/root/.claude`, `/root/.local/share/agentbox-history`,
  `/root/.local/share/agentbox-saved`). Print "rw" or "ro" based on `findmnt -no
  OPTIONS`.
- `resources` — read from cgroups v2: `/sys/fs/cgroup/cpu.max`,
  `/sys/fs/cgroup/memory.max`, `/sys/fs/cgroup/pids.max`. Format human-readably.

The script should still degrade gracefully when run outside an agentbox container
(no env vars, no mounts) — print "n/a" rather than crashing.

```bash
#!/usr/bin/env bash
# box info — print this box's project, agent, kits, mounts, network, resources.
set -u

# Top section: simple env-var lookups with n/a fallback.
printf "project_id   %s\n" "${AGENTBOX_PROJECT_ID:-n/a}"
printf "project      %s\n" "${AGENTBOX_PROJECT:-n/a}"
printf "cwd          %s\n" "${AGENTBOX_CWD:-$(pwd 2>/dev/null || echo n/a)}"
printf "agent        %s\n" "${AGENTBOX_AGENT:-n/a}"
printf "kits         %s\n" "${AGENTBOX_KITS:-n/a}"
printf "kit_image    %s\n" "${AGENTBOX_KIT_IMAGE:-n/a}"
printf "network      %s\n" "${AGENTBOX_NETWORK:-n/a}"

# Mounts: enumerate known agentbox bind mounts.
printf "mounts\n"
for spec in \
    "$AGENTBOX_CWD              project" \
    "/root/.gitconfig                  " \
    "/root/.ssh                        " \
    "/root/.claude                     " \
    "/root/.local/share/agentbox-history " \
    "/root/.local/share/agentbox-saved   "; do
    path=$(echo "$spec" | awk '{print $1}')
    label=$(echo "$spec" | awk '{$1=""; print $0}' | xargs)
    [ -z "$path" ] && continue
    if [ -e "$path" ]; then
        # findmnt -no OPTIONS; rw|ro is in the comma-separated list.
        opts=$(findmnt -no OPTIONS "$path" 2>/dev/null | tr ',' '\n' | grep -E '^rw$|^ro$' | head -1)
        [ -z "$opts" ] && opts="?"
        if [ -n "$label" ]; then
            printf "  %-30s %s  (%s)\n" "$path" "$opts" "$label"
        else
            printf "  %-30s %s\n" "$path" "$opts"
        fi
    fi
done

# Resources: cgroups v2.
printf "resources\n"
if [ -f /sys/fs/cgroup/cpu.max ]; then
    cpu=$(cat /sys/fs/cgroup/cpu.max 2>/dev/null)
    printf "  cpus     %s\n" "$cpu"
fi
if [ -f /sys/fs/cgroup/memory.max ]; then
    mem=$(cat /sys/fs/cgroup/memory.max 2>/dev/null)
    if [ "$mem" != "max" ] && [ -n "$mem" ]; then
        # Convert bytes to human-readable
        human=$(numfmt --to=iec-i --suffix=B --format='%.1f' "$mem" 2>/dev/null || echo "$mem")
        printf "  memory   %s\n" "$human"
    else
        printf "  memory   %s\n" "${mem:-n/a}"
    fi
fi
if [ -f /sys/fs/cgroup/pids.max ]; then
    pids=$(cat /sys/fs/cgroup/pids.max 2>/dev/null)
    printf "  pids     %s\n" "${pids:-n/a}"
fi

printf "created      %s\n" "${AGENTBOX_CREATED:-n/a}"
```

**Implementation Notes**:
- `AGENTBOX_CWD` is a new env var; if you'd rather avoid changing runspec, falling
  back to `pwd` is fine because the container's `-w` is set to the project abs
  path so `pwd` returns it. **Pick the simpler path: don't add a new env var; use
  `pwd` exclusively.**
- `findmnt` is provided by `util-linux` which is installed by default in Debian
  bookworm — no kit change needed.
- `numfmt` is GNU coreutils — also pre-installed.
- The mount label trick (`spec | awk`) is brittle. Cleaner: use a small helper
  function that takes path + label as arguments. The example above is dense
  for design-doc compactness; the implementer should prefer readability.

**Acceptance Criteria**:
- [ ] Inside an agentbox container, `box info` prints all the fields documented
      in CLI.md with real values from env + cgroups.
- [ ] Run via `podman run --rm <base-image> box info` (no agentbox wrapper, no
      env vars), exits 0 with `n/a` for project_id/project/agent/etc.
- [ ] When `git status` is run inside an agentbox container at the project's
      mount path, `pwd` resolves to the same path that `box info`'s `cwd` line
      shows.

---

### Unit 9: `internal/builtinkits/kits/base/box-save` — verify the saved/ mount works

**File**: `internal/builtinkits/kits/base/box-save` (no edit needed if Phase 2's
script is correct; verify behavior)

The Phase 2 script writes to `${AGENTBOX_SAVED_DIR:-/root/.local/share/agentbox-saved}`.
Phase 3 sets `AGENTBOX_SAVED_DIR` via runspec to `<state-dir>/saved`. Phase 4 mounts
that directory into the box (Unit 1) and creates it on the host (Unit 2). After
all four pieces are in place, `box save /etc/hostname` inside the box copies the
file to `<host>/.local/share/agentbox/sessions/<id>/saved/hostname`.

**Acceptance Criteria** (verified manually via the test checkpoint):
- [ ] `box save /etc/hostname` inside the box prints `saved → ...`.
- [ ] After exiting the box, `ls ~/.local/share/agentbox/sessions/<id>/saved/`
      shows `hostname` on the host.
- [ ] `agentbox rm` removes the session dir, including saved/. (This is by
      design — saved/ persists across the *box* lifecycle, not the *project*
      lifecycle. If users want files to outlive `agentbox rm`, they should
      `cp` from saved/ to elsewhere on the host before running rm.)

---

### Unit 10: `internal/cli/cli_test.go` — extend tests for zellij paths

**File**: `internal/cli/cli_test.go` (extend)

Add tests that assert the lifecycle-driven CLI commands invoke zellij correctly:

```go
func TestRun_AttachInteractive_InvokesZellij(t *testing.T) {
    // Override stdin to be a fake TTY (or skip the TTY check via a test helper).
    // Use newFakeRuntime that captures ExecOpts.Argv.
    // Run agentbox run with attach (default).
    // Assert: fake.lastExec.Argv[0] == "zellij" and contains "--layout" and "attach -c agentbox".
}

func TestShell_NoZellij_FallsBackToShellExec(t *testing.T) {
    // ...
    // Assert: fake.lastExec.Argv == [shell.Shell] (e.g., "zsh").
}

func TestShell_DefaultPath_InvokesZellij(t *testing.T) {
    // ...
    // Assert: fake.lastExec.Argv[0] == "zellij".
}

func TestAttach_StoppedBoxReturnsExit4(t *testing.T) {
    // Existing P3 test; verify it still passes with P4 changes.
}
```

If overriding `isTerminal` for tests is awkward, expose it as a package-level
variable in lifecycle so tests can swap:

```go
// In lifecycle.go:
var stdinIsTerminal = func() bool { return isTerminal(os.Stdin) }
```

…and use `stdinIsTerminal()` in the lifecycle methods. Tests set it to `func() bool { return true }`.

**Acceptance Criteria**:
- [ ] All new tests pass without invoking podman.
- [ ] Existing Phase 1/2/3 cli_test.go cases still pass.

---

## Implementation Order

| # | Unit | Depends on |
|---:|------|-----------|
| 1 | runspec saved/ mount | — |
| 2 | state EnsureSession saved/ subdir | — |
| 3 | zellij/types.go | — |
| 4 | zellij/kdl.go | Unit 3 |
| 5 | zellij/kdl_test.go | Unit 4 |
| 6 | lifecycle integration | Units 1, 2, 4 |
| 7 | cli/shell.go --no-zellij wiring | Unit 6 |
| 8 | box-info rewrite | — (independent of Go code) |
| 9 | box-save (verify only) | Units 1, 2 |
| 10 | cli_test.go extensions | Units 6, 7 |

One Sonnet agent can do all of this in a single pass — patterns are well-established,
total ~10 file changes, ~500-800 lines including tests.

---

## Verification Checklist

```sh
cd /home/nathan/dev/agent-box
go vet ./...
go test ./...                                  # all green; new zellij tests pass
go build ./...
make build

# Ports & Adapters preserved
grep -rn 'spf13/cobra\|internal/cli' \
    internal/version/ internal/exitcode/ internal/state/ internal/project/ \
    internal/config/ internal/runspec/ internal/doctor/ internal/kits/ \
    internal/builtinkits/ internal/container/ internal/lifecycle/ internal/zellij/
# Only the kits_test.go comment line should match.

# Verify zellij KDL syntax against real zellij in the base image:
TAG=$(./agentbox build --print base | grep -oP 'agentbox/[a-f0-9]+' | head -1)
podman run --rm "localhost/$TAG" zsh -lc 'zellij setup --dump-layout default | head -40'
# Compare against your generated KDL — adjust field names if needed.

# ROADMAP Phase 4 test checkpoint (real podman, real zellij)
mkdir -p /tmp/abx-proj && cd /tmp/abx-proj && touch hello.txt
cat > .agentbox.toml <<'EOF'
[agents.claude]
kits = ["base"]
cmd = ["zsh"]
EOF

# Interactive verification with expect.
expect <<'EOF'
spawn /home/nathan/dev/agent-box/agentbox shell
set timeout 30
expect "agentbox"
send "box info\r"
expect "project_id"
send "box save /etc/hostname\r"
expect "saved"
send "\x10d"
expect eof
EOF

# Verify the saved file made it to the host.
ls ~/.local/share/agentbox/sessions/*/saved/hostname

# Reattach test.
/home/nathan/dev/agent-box/agentbox attach . </dev/null     # liveness check exits 0
# (For real interactive reattach, run from a terminal: `agentbox attach .` reconnects to zellij.)

/home/nathan/dev/agent-box/agentbox rm .

# Cleanup
rm -rf /tmp/abx-proj
```

Phase 4 is "done" for autopilot purposes when:
- `go test ./...` is green.
- The expect script completes without timing out.
- `~/.local/share/agentbox/sessions/<id>/saved/hostname` exists after the script.
- `agentbox attach . </dev/null` exits 0 with the liveness print.
