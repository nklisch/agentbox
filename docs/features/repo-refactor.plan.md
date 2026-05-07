# Refactor Plan: Repo-wide consolidation

## Overview

Audit of `internal/` (16 packages, ~7.7k LOC source) and `cmd/` surfaced eight
refactor candidates with concrete file:line evidence. Each is small, testable
in isolation, and improves either clarity or contract-fidelity (e.g. the
`network.Manager` direct-`os.Stderr` writes silently bypass `--quiet`).

The codebase is in good shape overall — layering is clean (no `runspec` →
`lifecycle`, no `lifecycle` → `cli`), cobra is confined to `internal/cli/`,
the secrets-by-name policy holds, and there are zero `TODO`/`FIXME`/`HACK`
markers. So this isn't a rescue mission; it's polish on a couple of recently
shipped features (rm-prompt, `--quiet`, `--dry-run`, claude-home symlink
overlay) that left some natural duplication in their wake.

The plan is ordered so each step is a small, low-risk PR-sized commit. Run
`go build ./... && go vet ./... && go test ./...` between every step. Steps
1–4 are mechanical and independent; steps 5–8 build on each other.

## Refactor Steps

### Step 1: Consolidate duplicate `ContainerName`

**Priority**: High
**Risk**: Low
**Files**: `internal/project/id.go`, `internal/runspec/runspec.go`,
`internal/container/types.go`

**Current State**:

`internal/container/types.go:33-35`:
```go
// ContainerName returns the container name for a project ID.
// Mirrors project.ContainerName ("agentbox-<id>"). Defined here so the
// container package is self-contained.
func ContainerName(projectID string) string {
	return "agentbox-" + projectID
}
```

`internal/project/id.go:36`:
```go
func ContainerName(id string) string { return "agentbox-" + id }
func NetworkName(id string) string   { return "agentbox-net-" + id }
```

`internal/runspec/runspec.go:152` (only caller of `project.ContainerName`):
```go
Name: project.ContainerName(in.ProjectID),
```

All ten call sites in `internal/lifecycle/` use `container.ContainerName`. The
`project` package has the duplicate as dead weight.

**Target State**:

Keep `container.ContainerName`. Delete `project.ContainerName`. Update
`runspec.go:152` to call `container.ContainerName`. Move
`project.NetworkName` to `container.NetworkName` for symmetry — both are
"how do we name container-runtime resources for a project," and they belong
together.

`internal/container/types.go`:
```go
func ContainerName(projectID string) string { return "agentbox-" + projectID }
func NetworkName(projectID string)   string { return "agentbox-net-" + projectID }
```

`internal/project/id.go`: delete the `ContainerName` and `NetworkName`
functions; `project` keeps only `Resolve` and `IDFromPath`.

`internal/runspec/runspec.go:152`:
```go
Name: container.ContainerName(in.ProjectID),
```

(Also update the one network-name use site in `runspec.go:145`.)

**Implementation Notes**:

- `runspec` already imports `container` indirectly (no — verify with
  `grep "import" internal/runspec/runspec.go`). If not, add the import.
- `project.NetworkName` may have callers in `internal/network/`; `grep -rn
  "project.NetworkName" internal` first and update all to
  `container.NetworkName`.
- The dropped `project.ContainerName` comment lies — there's no reason
  `container` couldn't always have been the canonical home; the comment was
  retroactive justification.

**Acceptance Criteria**:

- [ ] `go build ./...` passes
- [ ] `go vet ./...` passes
- [ ] `go test ./...` passes
- [ ] `grep -rn "project.ContainerName\|project.NetworkName" internal/ cmd/`
  returns zero matches outside the deleted definitions
- [ ] `grep -rn "container.ContainerName\|container.NetworkName" internal/
  cmd/` returns the same set of call sites that previously used either
  package's helper

---

### Step 2: Extract `state.EnsureFile` for self-heal touch pattern

**Priority**: High
**Risk**: Low (also fixes a latent bug)
**Files**: `internal/state/dir.go` (new fn), `internal/lifecycle/lifecycle.go`,
`internal/lifecycle/trail.go`, `internal/cli/config.go`

**Current State**:

Four sites do the same "stat → create as empty file if missing" pattern with
inconsistent error handling. The lifecycle.go variant silently ignores the
create error — a latent bug.

`internal/lifecycle/trail.go:91-99`:
```go
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
```

`internal/lifecycle/lifecycle.go:223-230` (~/.claude.json sibling):
```go
if in.Agent == "claude" && in.HomeDir != "" {
	p := filepath.Join(in.HomeDir, ".claude.json")
	if _, statErr := os.Stat(p); errors.Is(statErr, os.ErrNotExist) {
		if f, ferr := os.OpenFile(p, os.O_CREATE|os.O_WRONLY, 0o600); ferr == nil {
			_ = f.Close()
		}
		// silently ignored — latent bug
	}
}
```

`internal/cli/config.go:112-115` (config file before `$EDITOR`):
```go
if _, err := os.Stat(target); errors.Is(err, fs.ErrNotExist) {
	if err := os.WriteFile(target, []byte(""), 0o600); err != nil {
		return exitcode.Wrap(exitcode.Generic, err)
	}
}
```

`internal/state/dir.go` already has variants of this for history file etc.

**Target State**:

`internal/state/dir.go`:
```go
// EnsureFile stat()s path; if the file does not exist, creates it as an
// empty regular file with mode 0o600. Returns nil on success or if the file
// already existed. Wraps unexpected errors (permission, parent-dir-missing).
//
// Used wherever a host path must exist as a regular file before a podman
// bind-mount references it — podman silently creates missing bind sources
// as directories, which corrupts the mount.
func EnsureFile(path string) error {
	info, err := os.Stat(path)
	if err == nil {
		if info.IsDir() {
			return fmt.Errorf("ensure %s: exists but is a directory", path)
		}
		return nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	return nil
}
```

Three call sites collapse:
- `EnsureTrailFile` in trail.go becomes a 3-line wrapper around
  `state.EnsureFile`.
- The `lifecycle.go:223-230` block becomes `_ = state.EnsureFile(p)` —
  preserves the "silent" semantics intentionally (claude.json self-heal is
  best-effort), but the call expresses intent.
- `cli/config.go:112-115` becomes `if err := state.EnsureFile(target); err
  != nil { return exitcode.Wrap(exitcode.Generic, err) }`.

**Implementation Notes**:

- The `IsDir()` check in `EnsureFile` is intentional. Today,
  `EnsureTrailFile` would silently succeed if `trail.jsonl` had been created
  as a directory by a previous botched podman create — masking the corrupted
  state. Detecting it now turns a silent failure into a loud one.
- Don't change `state.EnsureSession`'s existing inline create — it creates a
  whole session dir; `EnsureFile` is for individual files.
- The lifecycle.go silent-ignore is preserved because a missing
  `~/.claude.json` is non-fatal (Claude Code will create it on first read).
  `_ = state.EnsureFile(p)` is the cleanest expression of "best-effort."

**Acceptance Criteria**:

- [ ] `go test ./internal/state/...` passes (new test for `EnsureFile`)
- [ ] `go test ./internal/lifecycle/...` passes (existing trail_test.go
  exercises the path)
- [ ] `go test ./internal/cli/...` passes
- [ ] All four call sites use `state.EnsureFile`
- [ ] New test asserts `EnsureFile` returns a clear error if the path is an
  existing directory (the latent-bug protection)

---

### Step 3: Extract `paths.ExpandHome` (or export `runspec.ExpandHome`)

**Priority**: Medium
**Risk**: Low
**Files**: `internal/runspec/runspec.go`, `internal/lifecycle/lifecycle.go`,
optionally new `internal/paths/expand.go`

**Current State**:

`internal/runspec/runspec.go:395-402` (unexported):
```go
func expandHome(s, homeDir string) string {
	if strings.HasPrefix(s, "~/") && homeDir != "" {
		return homeDir + s[1:]
	}
	if s == "~" && homeDir != "" {
		return homeDir
	}
	return s
}
```

`internal/lifecycle/lifecycle.go:202-207` (inline reimplementation):
```go
claudeDir := cfgSrc
if strings.HasPrefix(claudeDir, "~/") {
	claudeDir = filepath.Join(l.Home, claudeDir[2:])
} else if claudeDir == "~" {
	claudeDir = l.Home
}
```

**Target State**:

Move and export to a new `internal/paths/expand.go`:

```go
// Package paths provides path manipulation helpers shared by lifecycle,
// runspec, and state. Lower-level than runspec; never imports lifecycle or cli.
package paths

import "strings"

// ExpandHome replaces a leading "~" in s with homeDir. Empty homeDir is a
// no-op (returns s unchanged). Other path components are not interpreted.
//
// Examples:
//   ExpandHome("~/.claude", "/home/foo") → "/home/foo/.claude"
//   ExpandHome("~",         "/home/foo") → "/home/foo"
//   ExpandHome("/abs/path", "/home/foo") → "/abs/path"
//   ExpandHome("~/x",       "")          → "~/x"
func ExpandHome(s, homeDir string) string {
	if homeDir == "" {
		return s
	}
	if strings.HasPrefix(s, "~/") {
		return homeDir + s[1:]
	}
	if s == "~" {
		return homeDir
	}
	return s
}
```

`runspec.go`: replace `expandHome` calls with `paths.ExpandHome`. Remove the
private function.

`lifecycle.go:202-207`:
```go
claudeDir := paths.ExpandHome(cfgSrc, l.Home)
```

**Implementation Notes**:

- A new `internal/paths` package is justified by the "shared by 2+ packages,
  belongs nowhere else" rule. Alternatively, export `ExpandHome` from
  `internal/state` if you prefer fewer packages — `state` is also a
  filesystem-y package. Recommend `paths` for separation of concerns: state
  is about *agentbox's* state dirs, paths is generic.
- `parseExtraMount` in runspec.go also calls `expandHome`. It will use the
  new exported version too.

**Acceptance Criteria**:

- [ ] New package `internal/paths/` with `expand.go` and `expand_test.go`
- [ ] `internal/runspec/runspec.go` no longer defines `expandHome`
- [ ] `internal/lifecycle/lifecycle.go:202-207` uses `paths.ExpandHome`
- [ ] All tests pass
- [ ] `paths` does not import `lifecycle`, `cli`, `runspec`, or `state`
  (verify: `grep -E "import" internal/paths/*.go`)

---

### Step 4: Add `Lifecycle.info`/`Lifecycle.warn` quiet-aware helpers

**Priority**: High
**Risk**: Low
**Files**: `internal/lifecycle/output.go` (new), `internal/lifecycle/lifecycle.go`

**Current State**:

Seven `if !l.Quiet { fmt.Fprintf(...) }` guards in lifecycle.go (lines 420,
427, 468, 610, 761, 789, 806):

```go
if !l.Quiet {
	fmt.Fprintf(l.Stdout, "%s (%s)\n", box.ProjectID, box.Status)
}
```

```go
if !l.Quiet {
	fmt.Fprintf(l.Stderr, "removed %s\n", projID)
}
```

The pattern works but is verbose, and the choice of stdout vs stderr is
inconsistent: some informational lines go to `l.Stdout`, others to
`l.Stderr`. Per the `cli.info` convention shipped alongside `--quiet`,
informational messages should consistently go to **stderr** so primary
output (table, JSON, dry-run shell) on stdout stays pipe-clean.

**Target State**:

`internal/lifecycle/output.go` (new):
```go
package lifecycle

import (
	"fmt"
	"io"
)

// info writes an informational message to l.Stderr (not Stdout — primary
// output goes there). Suppressed when l.Quiet is true. Mirrors cli.info but
// for code paths inside lifecycle that don't have a *cobra.Command.
func (l *Lifecycle) info(format string, args ...any) {
	if l.Quiet {
		return
	}
	fmt.Fprintf(l.Stderr, format+"\n", args...)
}

// warn writes a warning to l.Stderr regardless of l.Quiet. --quiet suppresses
// status chatter, not warnings about partial failures.
func (l *Lifecycle) warn(format string, args ...any) {
	fmt.Fprintf(l.Stderr, format+"\n", args...)
}

// stderrOf returns l.Stderr if non-nil, else io.Discard. Used for handing a
// writer to lower-level helpers that always need a non-nil writer.
func (l *Lifecycle) stderrOf() io.Writer {
	if l.Stderr == nil {
		return io.Discard
	}
	return l.Stderr
}
```

Replace each of the seven `if !l.Quiet` blocks with `l.info(...)` calls.
Move the lines that currently print to `l.Stdout` (Run/Shell/Attach
liveness) to use `l.info` (which goes to stderr) — this is a small
behavior change, justified by the `--json` / pipe-cleanness principle from
the `--quiet` design and matches `cli.info`'s stderr default.

The existing direct `fmt.Fprintf(l.Stderr, "warning: ...")` calls (3
sites: lifecycle.go:213, 435, ~750) become `l.warn(...)`.

**Implementation Notes**:

- Move the liveness-print lines from stdout → stderr. Three test cases assert
  on stdout for these — update them to assert on stderr. CLI tests might
  also need updates if any call `runCmd(t, "run", "--no-attach")` and check
  stdout.
- The `confirmRmAll` prompt at line ~770 stays on stderr and is NOT gated by
  `l.Quiet` — a prompt the user can't see is a hang. Keep that one as raw
  `fmt.Fprintf(l.Stderr, ...)`, not via `l.info` or `l.warn`.

**Acceptance Criteria**:

- [ ] `internal/lifecycle/lifecycle.go` has zero `if !l.Quiet` blocks
- [ ] All informational prints go through `l.info` or `l.warn`
- [ ] Liveness/status prints go to `l.Stderr` (was `l.Stdout` for some)
- [ ] All tests pass; if any test asserted on stdout liveness, it now
  asserts on stderr
- [ ] The `confirmRmAll` prompt is unchanged (still raw `fmt.Fprintf`)

---

### Step 5: Wire `network.Manager.Stderr io.Writer` field

**Priority**: High
**Risk**: Low–Medium (small API change, contained)
**Files**: `internal/network/manager.go`, `internal/cli/lifecycle.go`,
`internal/network/manager_test.go`

**Current State**:

`internal/network/manager.go:111`:
```go
fmt.Fprintf(os.Stderr, "warning: allowlist prepopulate: %v\n", err)
```

`internal/network/manager.go:210`:
```go
fmt.Fprintf(os.Stderr, "warning: write netfilter.pid: %v\n", err)
```

`Manager` is invoked by `Lifecycle.setupNetwork` and `Lifecycle.rmOne`. Both
have access to `l.Stderr`. But Manager writes directly to `os.Stderr`,
bypassing:
- `--quiet` (these warnings print even when the user asked for silence —
  arguably correct for warnings, but should be configurable)
- Test capture (tests using `cmd.ErrOrStderr()` won't see these)
- Future `--json` log routing if it's added

**Target State**:

`internal/network/manager.go`:
```go
type Manager struct {
	Runtime      container.Runtime
	IPTables     *IPTables
	NetfilterBin string
	Stderr       io.Writer // nil → io.Discard; populated by Lifecycle factory
}

// stderrOf returns m.Stderr or io.Discard if unset.
func (m *Manager) stderrOf() io.Writer {
	if m.Stderr == nil {
		return io.Discard
	}
	return m.Stderr
}
```

Replace the two `os.Stderr` writes with `fmt.Fprintf(m.stderrOf(), ...)`.

`internal/cli/lifecycle.go` — when constructing the Manager:
```go
return &lifecycle.Lifecycle{
	// ...
	Network: &network.Manager{
		Runtime:      rt,
		IPTables:     &network.IPTables{Sudo: sudoIPTables},
		NetfilterBin: "agentbox-netfilter",
		Stderr:       os.Stderr,
	},
	// ...
}
```

Test factories should set `Stderr: io.Discard` (or a captured buffer if the
test asserts on warnings).

**Implementation Notes**:

- These are warnings, not status. They should NOT be gated on `--quiet` —
  the user wants to know when allowlist prepopulate or pid-file write fails,
  even in quiet mode. Just route them through the configured writer.
- The `Manager` already has dependencies injected (`Runtime`, `IPTables`).
  Adding `Stderr` is consistent with that pattern.

**Acceptance Criteria**:

- [ ] `grep -n "os\.Stderr" internal/network/*.go` returns zero matches
  (excluding test files)
- [ ] All tests pass
- [ ] `internal/cli/lifecycle.go` factory sets `Stderr: os.Stderr` on the
  Manager

---

### Step 6: Extract state path helpers

**Priority**: Medium
**Risk**: Low
**Files**: `internal/state/dir.go`, `internal/runspec/runspec.go`,
`internal/network/sidecar.go`

**Current State**:

`internal/runspec/runspec.go:292-295`:
```go
args.Mounts = append(args.Mounts,
	Mount{Source: in.StateDir + "/history",                Target: "/root/.local/share/agentbox-history", Mode: "rw"},
	Mount{Source: in.StateDir + "/layout.kdl",             Target: "/etc/agentbox/layout.kdl",            Mode: "ro"},
	Mount{Source: in.StateDir + "/effective-config.toml",  Target: "/etc/agentbox/config.toml",           Mode: "ro"},
	Mount{Source: in.StateDir + "/saved",                  Target: "/root/.local/share/agentbox-saved",  Mode: "rw"},
)
```

`internal/network/sidecar.go:42`:
```go
Source: in.StateDir + "/Corefile"
```

`internal/lifecycle/trail.go:92`:
```go
p := filepath.Join(stateDir, "trail.jsonl")
```

`internal/lifecycle/lifecycle.go:230` (writes shadow settings):
```go
out := filepath.Join(stateDir, "claude-settings.json")
```

State filenames are hardcoded across four packages. `internal/state` should
own these names so renames or additions happen in one place.

**Target State**:

`internal/state/dir.go`:
```go
// Per-session file paths under <stateDir>. Use these helpers rather than
// inlining the filenames so all references update together.
func HistoryPath(sessionDir string) string         { return filepath.Join(sessionDir, "history") }
func LayoutPath(sessionDir string) string          { return filepath.Join(sessionDir, "layout.kdl") }
func EffectiveConfigPath(sessionDir string) string { return filepath.Join(sessionDir, "effective-config.toml") }
func SavedDirPath(sessionDir string) string        { return filepath.Join(sessionDir, "saved") }
func TrailPath(sessionDir string) string           { return filepath.Join(sessionDir, "trail.jsonl") }
func ClaudeSettingsPath(sessionDir string) string  { return filepath.Join(sessionDir, "claude-settings.json") }
func CorefilePath(sessionDir string) string        { return filepath.Join(sessionDir, "Corefile") }
```

Update each call site to use the helper.

**Implementation Notes**:

- These are simple wrappers, but they make grep across the codebase more
  effective — `grep -rn "state.HistoryPath" internal/` finds every consumer
  in one shot.
- Non-rename change. The strings are the same as before.
- Don't replace `runspec.go`'s mount construction with a "build-all-mounts"
  helper — keep the explicit Mount{} list. Just replace the source-path
  strings.

**Acceptance Criteria**:

- [ ] All inline `stateDir + "/<name>"` and
  `filepath.Join(stateDir, "<name>")` strings for known files use a `state.*Path`
  helper
- [ ] `grep -rn '"history"\|"layout.kdl"\|"trail.jsonl"\|"claude-settings.json"\|"Corefile"\|"effective-config.toml"\|"saved"' internal/`
  returns matches only inside `internal/state/dir.go` and tests
- [ ] All tests pass

---

### Step 7: Extract `resolveConfigTarget` and adopt `globalProjectFlags` everywhere

**Priority**: Medium
**Risk**: Low
**Files**: `internal/cli/config.go`

**Current State**:

`internal/cli/config.go:185-192` defines `globalProjectFlags(cmd)`. Six
subcommands use it: `set`, `unset`, `network`, `containers`, `kits add`,
`kits remove` (lines 225, 259, 290, 314, 347, 383).

`path` (line 66) and `edit` (line 89) inline both the flag declaration AND
the path-resolution:

```go
// repeated in path and edit:
var (
	globalFlag  bool
	projectFlag bool
)
// ... RunE body ...
paths, err := config.DefaultPaths()
if err != nil { return exitcode.Wrap(exitcode.Generic, err) }
if global.ConfigPath != "" { paths.Global = global.ConfigPath }
target := paths.Global
if projectFlag { target = paths.Project }
// ...
cmd.Flags().BoolVar(&globalFlag, "global", false, "...")
cmd.Flags().BoolVar(&projectFlag, "project", false, "...")
cmd.MarkFlagsMutuallyExclusive("global", "project")
```

The same path resolution exists in `editTarget` (lines 142-156).

**Target State**:

Add a helper that returns the resolved file path:

`internal/cli/config.go`:
```go
// resolveConfigTarget returns the absolute path of the config file the user
// wants to read or write, honoring --project (override to project file) and
// the global --config flag (override to a custom global path).
//
// Returns InvalidArgs if useProject is true but no project config exists
// for the current directory.
func resolveConfigTarget(useProject bool) (string, error) {
	paths, err := config.DefaultPaths()
	if err != nil {
		return "", exitcode.Wrap(exitcode.Generic, err)
	}
	if global.ConfigPath != "" {
		paths.Global = global.ConfigPath
	}
	if useProject {
		if paths.Project == "" {
			return "", exitcode.New(exitcode.InvalidArgs,
				"no project config path (run from a project directory)")
		}
		return paths.Project, nil
	}
	return paths.Global, nil
}
```

Update `path`, `edit`, and `editTarget` to use it. Update `path` and `edit`
to also use `globalProjectFlags(cmd)` instead of inline declarations.

**Implementation Notes**:

- The `editTarget` function predates the new helper but conceptually needs
  the same logic. Refactor it to call `resolveConfigTarget`.
- `path` previously didn't error on missing project file — it would just
  print empty. With the new helper it errors with InvalidArgs. That's a
  small behavior change but a correct one (printing nothing was a footgun
  that masked the error).

**Acceptance Criteria**:

- [ ] `grep -n 'globalFlag.*bool' internal/cli/config.go` shows zero declared
  vars (the helper handles it; only the var the helper returns remains)
- [ ] `path` and `edit` subcommands use `globalProjectFlags(cmd)` and
  `resolveConfigTarget(...)`
- [ ] `editTarget` uses `resolveConfigTarget`
- [ ] All tests pass; new test: `agentbox config path --project` outside a
  project dir exits 2

---

### Step 8: Extract `initLifecycleCmd` for CLI command boilerplate

**Priority**: High
**Risk**: Medium (touches 6 commands; do last so smaller refactors land first)
**Files**: `internal/cli/lifecycle.go` (helper), `internal/cli/run.go`,
`internal/cli/shell.go`, `internal/cli/attach.go`, `internal/cli/exec.go`,
`internal/cli/ls.go`, `internal/cli/rm.go`

**Current State**:

Six commands (`run`, `shell`, `attach`, `exec`, `ls`, `rm`) repeat this
8-line prelude:

```go
res, err := loadConfig()
if err != nil {
	return err
}
l, err := newLifecycle(res.Config)
if err != nil {
	return exitcode.Wrap(exitcode.Generic, err)
}
l.Stdout = cmd.OutOrStdout()
l.Stderr = cmd.ErrOrStderr()
l.Quiet = global.Quiet
```

The factory at `internal/cli/lifecycle.go:54` already initializes `Stdin`,
`Stdout`, `Stderr`, `Quiet` to global handles, but each command immediately
overrides Stdout/Stderr/Quiet to the cobra writers. The factory's settings
are dead code in the test path (the test factory in `setupFakeLifecycle`
sets bare `os.Stdout`/`os.Stderr` and tests rely on the per-command
override).

**Target State**:

`internal/cli/lifecycle.go`:
```go
// initLifecycleCmd is the standard prelude for every cobra command that
// uses lifecycle. It loads config, builds a Lifecycle wired to the cobra
// command's stdio, and propagates --quiet. Use it in RunE:
//
//	RunE: func(cmd *cobra.Command, args []string) error {
//		l, _, err := initLifecycleCmd(cmd)
//		if err != nil { return err }
//		return l.Run(...)
//	}
//
// The second return value (configResult) lets callers reach `res.Paths`
// when needed (rare — most commands only need the Lifecycle).
func initLifecycleCmd(cmd *cobra.Command) (*lifecycle.Lifecycle, configResult, error) {
	res, err := loadConfig()
	if err != nil {
		return nil, configResult{}, err
	}
	l, err := newLifecycle(res.Config)
	if err != nil {
		return nil, configResult{}, exitcode.Wrap(exitcode.Generic, err)
	}
	l.Stdout = cmd.OutOrStdout()
	l.Stderr = cmd.ErrOrStderr()
	l.Quiet = global.Quiet
	return l, res, nil
}
```

Each of the six commands' RunE shrinks from 10 lines of prelude to 1-3:

```go
RunE: func(cmd *cobra.Command, args []string) error {
	l, _, err := initLifecycleCmd(cmd)
	if err != nil {
		return err
	}
	// ...rest of the command
}
```

Commands that need `res` (e.g., `run` for the dry-run branch) keep it:
```go
l, res, err := initLifecycleCmd(cmd)
if err != nil { return err }
if global.DryRun {
	return runDryRun(cmd, res, args, kitsFlag, networkFlag, layoutFlag)
}
```

**Implementation Notes**:

- The factory's `Stdin: os.Stdin` line stays (initLifecycleCmd doesn't
  override Stdin — there's no cobra equivalent). For tests that need stdin
  control, they continue to set `l.Stdin` directly (existing pattern).
- The factory's pre-set Stdout/Stderr/Quiet lines can stay (harmless) or be
  removed (cleaner — initLifecycleCmd is now the canonical wiring point).
  Recommend removing them from the factory for clarity.
- Touch each command's RunE one at a time, run tests after each, commit
  the batch as a single refactor commit at the end.
- The doctor and config commands don't go through this prelude (doctor
  doesn't use lifecycle; config has its own helpers). Don't try to force
  them into the same shape.

**Acceptance Criteria**:

- [ ] `grep -n "newLifecycle(res.Config)" internal/cli/` shows only
  `internal/cli/lifecycle.go` (the helper) — zero call sites in commands
- [ ] All six commands use `initLifecycleCmd`
- [ ] `grep -c "l.Stdout = cmd.OutOrStdout()" internal/cli/` returns 1 (only
  in `initLifecycleCmd`)
- [ ] All tests pass
- [ ] `cli_test.go` is unchanged or simpler (no helper-specific tests
  needed; existing command tests cover this)

---

## Implementation Order

1. **Step 1** — Consolidate `ContainerName`. Mechanical, lowest risk.
2. **Step 2** — `state.EnsureFile`. Small, fixes a latent bug.
3. **Step 3** — `paths.ExpandHome`. New package, three call-site updates.
4. **Step 4** — `Lifecycle.info`/`warn`. Internal to lifecycle package; no
   external API change. Couple of test-assertion updates if they checked
   stdout for status lines.
5. **Step 5** — `network.Manager.Stderr` field. Small contained change.
6. **Step 6** — State path helpers. Mechanical string-to-helper conversion.
7. **Step 7** — `resolveConfigTarget` + `globalProjectFlags` adoption.
   Small contained change in config.go.
8. **Step 8** — `initLifecycleCmd`. Bigger touch surface (6 files); save for
   last so the smaller refactors don't conflict in review.

Each step is one commit. Verify with the standard sequence after each:

```sh
go build ./... && go vet ./... && go test ./...
```

If any step fails, roll back that step only — the others are independent.

## Estimated Impact

| Step | Source Δ | New file? | Risk | Bug fix? |
| ---- | -------- | --------- | ---- | -------- |
| 1    | −5       |            | low  | no       |
| 2    | −12      | no         | low  | yes (silent error in lifecycle.go) |
| 3    | −5       | yes (`internal/paths/`) | low | no |
| 4    | −14      | yes (`internal/lifecycle/output.go`) | low | partial (consistency) |
| 5    | +6 / −2  | no         | low  | partial (network warnings now capturable + testable) |
| 6    | ~0       | no         | low  | no       |
| 7    | −20      | no         | low  | yes (`config path --project` outside a project dir was silently empty) |
| 8    | −50      | no         | medium | no     |

Total: ~−100 LOC source, three small bug fixes, two new helper packages
(`paths`, lifecycle/output.go is intra-package), no public API breakage
outside the `project` package's `ContainerName`/`NetworkName` deletion (no
external consumers).

## Out-of-scope (mentioned, not planned)

- **Splitting `internal/lifecycle/lifecycle.go` (854 lines)** — splitting
  into `lifecycle_ensure.go` / `lifecycle_run.go` / `lifecycle_inspect.go`
  is cosmetic; navigation is fine via grep, and the file isn't growing.
  Defer until it crosses 1000 lines or someone has trouble navigating.
- **Adding `--json` to more commands** — that's a feature, not a refactor.
- **Dry-run header rendering consolidation** — the `# project_id`,
  `# kits`, `# tag` headers in `runDryRun`, `runBuildDryRun`, and
  `renderRmShell` look similar but encode genuinely different metadata.
  Forcing a generic helper would obscure intent.
- **Wholesale layering review** — already clean (see Phase 3 verification
  notes). No package boundary fixes needed.
