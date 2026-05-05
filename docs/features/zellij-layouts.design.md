# Design: Zellij layout system + agent-activity trail

## Overview

Implements `docs/features/zellij-layouts.md`. Adds:

- A **layout system** with three named built-ins (`focus`, `reviewer`,
  `auditor`) and user-defined custom layouts at
  `~/.config/agentbox/layouts/<name>.kdl`. Selection via `agentbox run
  --layout <name>` flag and `[zellij] layout = "..."` config; flag
  overrides config; default is `focus`.
- A **minimal agent-activity trail** for the `auditor` layout: agentbox
  writes a session-merged Claude settings file (containing
  `PreToolUse`/`PostToolUse`/`PostToolUseFailure`/`Stop`/`StopFailure`
  hooks) and shadow-mounts it on top of the user's bind-mounted
  `~/.claude/settings.json`. Hooks append JSONL events to
  `<state>/trail.jsonl`. A new `box-trail` helper tails that file and
  renders one color-coded line per event.
- Three new base-kit helpers: `box-diff-watch` (delta-rendered diff vs
  base branch for the reviewer pane), `box-tests-watch` (project-aware
  test runner via watchexec for the reviewer pane), `box-trail` (color-
  coded JSONL renderer for the auditor pane), plus a tiny
  `agentbox-hook-record` script invoked by the trail hooks.

The work is split into two **decouplable groups** so the layout system
ships even if the trail hooks turn out impractical at implementation
time:

- **Group A: layout foundation + reviewer + custom layouts** (always
  ships). Units 1–5, 7, 9, 10, 12.
- **Group B: auditor + trail** (ships only if Claude Code hooks API
  research at implementation start confirms the schema below). Units
  6, 8, 11. Unit 13 (TRAIL.md) is conditional on Group B shipping.

If Group B is dropped: the layout resolver returns `layout "auditor" not
yet shipped — set [zellij] layout = "focus"|"reviewer" or pick a
custom layout name` for `--layout auditor`. No other unit changes.

## Pre-design verification

Per CLAUDE.md's "fast-moving ecosystem" rule, the following surfaces were
verified at design time. Versions are pinned to what's in the box's base
kit on disk (`agentbox exec . <bin> --version`):

- **zellij 0.44.1** — KDL syntax confirmed in v0.2.3 (default_tab_template,
  plugin location, side-by-side layouts). No new verification needed.
- **delta 0.19.2** — flags verified: `-s`/`--side-by-side`,
  `-n`/`--line-numbers`, `--features`, `--color-only`. Auto-disables
  paging when stdout is not a TTY (which is true under `watch`).
- **watchexec 2.5.1** — flags verified: `-w`/`--watch`, `-e`/`--exts`,
  `-r`/`--restart`, `-c`/`--clear`, `--no-vcs-ignore`. Default behavior
  runs the command once at startup, then watches.
- **Claude Code hooks API** (via the user's `plugin-dev:hook-development`
  skill) — schema confirmed:
  - Hook stdin is JSON. Common fields: `session_id`, `transcript_path`,
    `cwd`, `permission_mode`, `hook_event_name`.
  - Tool-event-specific fields: `tool_name`, `tool_input` (object,
    nested fields per tool kind), `tool_result` (object,
    PostToolUse only). Some events also include `tool_use_id` and
    `duration_ms`.
  - Stop/StopFailure include `reason`.
  - Hooks load from fixed paths only — no `CLAUDE_CONFIG`/`CLAUDE_SETTINGS`
    env-var override. Confirmed.
  - All matching hooks for an event run in parallel; identical hooks are
    deduplicated. POSIX `O_APPEND` makes our short-line writes atomic.
  - Essential events for a tool-call trail: `PreToolUse`,
    `PostToolUse`, `PostToolUseFailure`, `Stop`, `StopFailure`.

The Claude hooks schema must be re-verified at implementation start
against `@anthropic-ai/claude-code@2.x` actually installed in the box.
If schema drift breaks the trail hook script, fall back: drop Group B
and ship Group A only.

## Implementation Units

### Unit 1: `[zellij]` config schema

**File:** `internal/config/config.go` (edit)

Add a `Zellij` struct and a top-level field on `Config`. Place it
adjacent to the existing `Shell` field for narrative flow.

```go
type Config struct {
    Runtime      string           `toml:"runtime" json:"runtime"`
    DefaultAgent string           `toml:"default_agent" json:"default_agent"`
    DefaultKits  []string         `toml:"default_kits" json:"default_kits"`
    Network      Network          `toml:"network" json:"network"`
    Mounts       Mounts           `toml:"mounts" json:"mounts"`
    Secrets      Secrets          `toml:"secrets" json:"secrets"`
    Resources    Resources        `toml:"resources" json:"resources"`
    Containers   Containers       `toml:"containers" json:"containers"`
    Shell        Shell            `toml:"shell" json:"shell"`
    Zellij       Zellij           `toml:"zellij" json:"zellij"` // NEW
    Agents       map[string]Agent `toml:"agents" json:"agents"`
}

// Zellij holds the zellij-related run-time options.
type Zellij struct {
    // Layout names a built-in (`focus`, `reviewer`, `auditor`) or a
    // user-defined layout file at ~/.config/agentbox/layouts/<name>.kdl.
    // Empty falls back to "focus" at resolution time.
    Layout string `toml:"layout" json:"layout"`
}
```

In `DefaultConfig()` add to the returned struct:

```go
Zellij: Zellij{
    Layout: "focus",
},
```

**Implementation Notes:**

- The existing `config-set` machinery handles `set zellij.layout reviewer`
  via reflection (it's a string scalar) — no special parsing needed
  beyond the new struct field.
- No `Validate()` change. Layout resolution happens later in lifecycle;
  bad names exit there with a more specific error message and the file-
  path tried.
- TOML tag `toml:"zellij"` matches the brief.

**Acceptance Criteria:**

- [ ] `cfg := config.DefaultConfig(); cfg.Zellij.Layout` is `"focus"`.
- [ ] `agentbox config show --json | jq -r .zellij.layout` prints
      `"focus"` on a fresh config.
- [ ] `agentbox config set zellij.layout reviewer` writes the value;
      `agentbox config show --json | jq -r .zellij.layout` returns
      `"reviewer"`.
- [ ] Loading a TOML file with `[zellij]\nlayout = "reviewer"` over the
      defaults produces `cfg.Zellij.Layout == "reviewer"`.

---

### Unit 2: `zellij.LayoutSpec` + resolver

**File:** `internal/zellij/types.go` (edit), `internal/zellij/resolve.go` (new)

Extend `Layout` with new fields and add a resolver type. The existing
`Mode` (Run vs Shell) stays — it differentiates the run-mode shell tab
from the bare shell mode. The new `LayoutName` is what selects the
run-mode shape.

```go
// Layout describes everything GenerateKDL needs to produce a layout file.
type Layout struct {
    Mode       Mode
    AgentCmd   []string
    ProjectAbs string
    Shell      string

    // NEW: name of the run-mode layout shape. One of "focus",
    // "reviewer", "auditor", or a custom name. Ignored when
    // Mode == ModeShell. Empty falls back to "focus".
    LayoutName string

    // NEW: pre-loaded body for custom layouts. Populated by lifecycle
    // (via LoadCustom) before GenerateKDL is called. Empty for built-ins.
    CustomKDL string

    // NEW: in-container path to the trail file. Populated by lifecycle
    // when LayoutName == "auditor" AND agent == "claude". Empty
    // otherwise. The auditor layout uses this to set BOX_TRAIL_FILE
    // for box-trail.
    TrailFile string
}
```

```go
// internal/zellij/resolve.go
package zellij

import (
    "fmt"
    "os"
    "path/filepath"
)

// LayoutKind discriminates how GenerateKDL should render a layout.
type LayoutKind int

const (
    LayoutBuiltin LayoutKind = iota
    LayoutCustom
)

// LayoutSpec is the resolved form of a layout name. Lifecycle resolves
// this BEFORE constructing a Layout for GenerateKDL.
type LayoutSpec struct {
    Name string     // canonical name; empty falls back to "focus"
    Kind LayoutKind // Builtin or Custom
    Path string     // host path to the .kdl file, set when Kind == Custom
}

// Built-in names. Order is the menu order shown in errors.
var builtinNames = []string{"focus", "reviewer", "auditor"}

// IsBuiltin reports whether name is one of the built-in layouts.
func IsBuiltin(name string) bool {
    for _, b := range builtinNames {
        if b == name {
            return true
        }
    }
    return false
}

// Resolve takes a layout name and the user's home directory and returns
// a LayoutSpec. Empty name falls back to "focus".
//
// Resolution order:
//  1. "" → "focus" (fallback).
//  2. Built-in → LayoutBuiltin with Name set.
//  3. ~/.config/agentbox/layouts/<name>.kdl exists → LayoutCustom.
//  4. Otherwise → ErrLayoutNotFound, with the searched custom path
//     included in the error message.
func Resolve(name, hostHome string) (LayoutSpec, error) {
    if name == "" {
        name = "focus"
    }
    if IsBuiltin(name) {
        return LayoutSpec{Name: name, Kind: LayoutBuiltin}, nil
    }
    customPath := filepath.Join(hostHome, ".config", "agentbox", "layouts", name+".kdl")
    if _, err := os.Stat(customPath); err == nil {
        return LayoutSpec{Name: name, Kind: LayoutCustom, Path: customPath}, nil
    } else if !os.IsNotExist(err) {
        return LayoutSpec{}, fmt.Errorf("layout %q: stat %s: %w", name, customPath, err)
    }
    return LayoutSpec{}, fmt.Errorf(
        "layout %q not found: not a built-in (%v) and no file at %s",
        name, builtinNames, customPath,
    )
}
```

**Implementation Notes:**

- `Resolve` does filesystem IO (`os.Stat`) — that's why it lives in a
  new file rather than the pure `kdl.go`. `GenerateKDL` stays IO-free.
- The error message lists both the menu of built-ins and the exact custom
  path tried. This is a UX choice: when a user types `--layout reviwer`
  (typo) the message tells them what's available AND where they could
  drop a file.
- `Resolve` does NOT validate the KDL contents — zellij does that at
  parse time. We just confirm the file exists.
- For Group B-defer scenario: `Resolve` includes `auditor` in
  `builtinNames` regardless. The lifecycle's "actually render auditor"
  path is what falls back to an error if Group B isn't shipped — see
  Unit 7.

**Acceptance Criteria:**

- [ ] `Resolve("focus", "/home/u")` returns `{Name: "focus", Kind: Builtin}`.
- [ ] `Resolve("", "/home/u")` returns `{Name: "focus", Kind: Builtin}` —
      empty falls back.
- [ ] `Resolve("reviewer", "/home/u")` returns
      `{Name: "reviewer", Kind: Builtin}`.
- [ ] `Resolve("myown", tmphome)` where
      `tmphome/.config/agentbox/layouts/myown.kdl` exists returns
      `{Name: "myown", Kind: Custom, Path: <expected>}`.
- [ ] `Resolve("myown", tmphome)` where the file does NOT exist returns
      an error mentioning both the built-in list and the custom path
      tried.
- [ ] `IsBuiltin("focus")` true; `IsBuiltin("foo")` false.

---

### Unit 3: Custom KDL template substitution

**File:** `internal/zellij/custom.go` (new)

Custom layouts support template substitution via Go's `text/template`
with `{{` `}}` delimiters. Variables exposed to user templates:

```go
// internal/zellij/custom.go
package zellij

import (
    "bytes"
    "fmt"
    "os"
    "strings"
    "text/template"
)

// TemplateVars are the values substituted into custom layout files
// before zellij parses them. Field tags are the names users reference
// in their KDL.
type TemplateVars struct {
    ProjectAbs    string // host absolute path of the project (cwd for panes)
    Shell         string // configured shell ("zsh", "bash", "fish")
    AgentCommand  string // first element of agent.cmd (e.g. "claude")
    AgentArgs     string // remaining args, KDL-quoted+space-joined
                         // (e.g. `"--dangerously-skip-permissions"`)
    AgentCmdFull  string // ready-to-paste KDL block:
                         //     command "claude"
                         //     args "--dangerously-skip-permissions"
                         // with no leading whitespace; user is responsible
                         // for indenting if their KDL is nested.
    TrailFile     string // in-container trail file path; empty unless
                         // lifecycle chose to wire trail (auditor + claude)
}

// LoadCustom reads a layout KDL file from disk and substitutes
// TemplateVars. Returns the rendered KDL body or an error if the file
// can't be read or templated.
//
// The template uses default Go delimiters ({{ and }}). KDL doesn't use
// double braces syntactically, so collision is unlikely. If a user's
// layout legitimately contains `{{` (e.g. a comment or string literal
// with embedded braces), they can `{{"{{"}}` to escape — standard Go
// template idiom. v1 doesn't document escaping; we'll iterate if it
// becomes a real complaint.
func LoadCustom(path string, vars TemplateVars) (string, error) {
    body, err := os.ReadFile(path)
    if err != nil {
        return "", fmt.Errorf("read custom layout %s: %w", path, err)
    }
    tmpl, err := template.New(path).Parse(string(body))
    if err != nil {
        return "", fmt.Errorf("parse custom layout %s: %w", path, err)
    }
    var buf bytes.Buffer
    if err := tmpl.Execute(&buf, vars); err != nil {
        return "", fmt.Errorf("render custom layout %s: %w", path, err)
    }
    return buf.String(), nil
}

// BuildTemplateVars assembles the TemplateVars for a Layout. Mirrors
// what writeCommand does in kdl.go: handles empty AgentCmd by falling
// back to the configured shell, KDL-quotes args, etc.
func BuildTemplateVars(l Layout) TemplateVars {
    vars := TemplateVars{
        ProjectAbs: l.ProjectAbs,
        Shell:      l.Shell,
        TrailFile:  l.TrailFile,
    }
    if len(l.AgentCmd) == 0 {
        vars.AgentCommand = l.Shell
        vars.AgentArgs = ""
        vars.AgentCmdFull = fmt.Sprintf("command %q", l.Shell)
        return vars
    }
    vars.AgentCommand = l.AgentCmd[0]
    if len(l.AgentCmd) > 1 {
        quoted := make([]string, 0, len(l.AgentCmd)-1)
        for _, a := range l.AgentCmd[1:] {
            quoted = append(quoted, fmt.Sprintf("%q", a))
        }
        vars.AgentArgs = strings.Join(quoted, " ")
        vars.AgentCmdFull = fmt.Sprintf("command %q\nargs %s",
            l.AgentCmd[0], vars.AgentArgs)
    } else {
        vars.AgentCmdFull = fmt.Sprintf("command %q", l.AgentCmd[0])
    }
    return vars
}
```

**Implementation Notes:**

- `text/template` is the right choice over `html/template` — no escaping
  needed, KDL is plain text. No XSS surface in a TUI layout file.
- `TemplateVars` is a value type passed by value into `Execute`. Field
  names are the user-visible API and become load-bearing once a user has
  written a custom layout against them.
- `AgentCmdFull` deliberately has no leading whitespace. Users embed it
  in their pane block and KDL is whitespace-tolerant for the relevant
  positions.
- KDL string syntax uses `"..."` with backslash escapes — exactly what
  Go's `%q` produces. Confirmed against zellij 0.44.1 KDL parser.

**Acceptance Criteria:**

- [ ] `LoadCustom` reading a file with `{{.ProjectAbs}}` returns the
      file with that token replaced by the input's `ProjectAbs`.
- [ ] `LoadCustom` reading a file with `{{.AgentCmdFull}}` for AgentCmd
      `["claude", "--dangerously-skip-permissions"]` substitutes:
      ```
      command "claude"
      args "--dangerously-skip-permissions"
      ```
- [ ] `LoadCustom` reading a non-existent file returns a wrapped error
      naming the path.
- [ ] `LoadCustom` reading a file with malformed template syntax (e.g.
      `{{.Foo`) returns a wrapped parse error.
- [ ] `BuildTemplateVars` for an empty AgentCmd produces
      `AgentCommand == Shell`, `AgentArgs == ""`,
      `AgentCmdFull == "command \"<shell>\""`.
- [ ] `BuildTemplateVars` for `["claude"]` (no args) produces
      `AgentArgs == ""`, `AgentCmdFull == "command \"claude\""` (no
      `args` line).

---

### Unit 4: Built-in `focus` layout (rename current default)

**File:** `internal/zellij/kdl.go` (edit)

Rename the existing `runLayout` to `focusLayout` and route to it through
a `LayoutName`-aware dispatcher in `GenerateKDL`. Behavior is byte-for-
byte unchanged — just renamed.

```go
// GenerateKDL renders the zellij layout KDL for the given Layout.
//
// For ModeShell, always returns the single-pane shell layout regardless
// of LayoutName.
//
// For ModeRun, dispatches by LayoutName:
//   - "" or "focus": focusLayout (current default behavior)
//   - "reviewer":    reviewerLayout (Unit 5)
//   - "auditor":     auditorLayout (Unit 6, Group B)
//   - anything else WITH l.CustomKDL non-empty: write CustomKDL verbatim
//   - anything else with empty CustomKDL: caller bug — fall back to focus
func GenerateKDL(l Layout) string {
    if l.Mode == ModeShell {
        return shellLayout(l)
    }
    if l.CustomKDL != "" {
        return l.CustomKDL
    }
    switch l.LayoutName {
    case "", "focus":
        return focusLayout(l)
    case "reviewer":
        return reviewerLayout(l)
    case "auditor":
        return auditorLayout(l)
    default:
        // Caller misuse: a custom name with empty CustomKDL. Fail safe.
        return focusLayout(l)
    }
}

// focusLayout produces the original three-pane run layout: agent pane
// (70%) + git pane + btm pane on tab 1; shell on tab 2. Unchanged
// behavior from v0.2.5.
func focusLayout(l Layout) string {
    // ... existing runLayout body, verbatim ...
}
```

**Implementation Notes:**

- The function `runLayout` is renamed but its body doesn't change. Move
  it; don't edit it.
- The `default` branch in the switch (custom name + empty CustomKDL) is
  a defensive fallback. In practice, lifecycle always populates CustomKDL
  for custom names before calling GenerateKDL — so this branch is dead
  code in production but valuable as a guard.
- `auditor` references `auditorLayout` which is defined in Unit 6; if
  Group B is dropped at implementation time, the case can be left as-is
  (auditorLayout will be in Group B's diff). If Group A ships first
  without Group B, the `case "auditor"` branch can either stay (returning
  a "trail not yet shipped" placeholder KDL) or be removed entirely.
  Recommend keeping the case and having auditorLayout return a stub.

**Acceptance Criteria:**

- [ ] `GenerateKDL(Layout{Mode: ModeRun, LayoutName: "focus", ...})` produces
      identical output to v0.2.5's `GenerateKDL(Layout{Mode: ModeRun, ...})`
      (byte-for-byte; preserve fragment-level test assertions).
- [ ] `GenerateKDL(Layout{Mode: ModeRun, LayoutName: "", ...})` is
      equivalent (empty falls back to focus).
- [ ] All existing v0.2.5 KDL test assertions for run mode still pass
      after the rename.
- [ ] `GenerateKDL(Layout{Mode: ModeRun, LayoutName: "myown", CustomKDL: "x"})`
      returns the literal string `"x"`.

---

### Unit 5: Built-in `reviewer` layout

**File:** `internal/zellij/reviewer.go` (new)

```go
// internal/zellij/reviewer.go
package zellij

import (
    "fmt"
    "strings"
)

// reviewerLayout renders the reviewer KDL: agent (50% width) + diff
// (50% width) on top, tests pane (30% rows) on bottom; shell second
// tab. Wraps every tab in default_tab_template (tab-bar + status-bar).
func reviewerLayout(l Layout) string {
    var b strings.Builder
    fmt.Fprintln(&b, "// Generated by agentbox; regenerated on every run.")
    fmt.Fprintln(&b, "layout {")
    writeTabTemplate(&b)

    fmt.Fprintln(&b, `    tab name="reviewer" focus=true {`)
    // Top row: agent | diff (vertical split).
    fmt.Fprintln(&b, `        pane split_direction="horizontal" {`)
    fmt.Fprintln(&b, `            pane split_direction="vertical" size="70%" {`)

    // Agent pane (50% width of top row). Wrapped in box-agent same as focus.
    fmt.Fprintln(&b, `                pane size="50%" name="agent" {`)
    agentCmd := l.AgentCmd
    if len(agentCmd) > 0 {
        agentCmd = append([]string{"box-agent"}, agentCmd...)
    }
    writeCommand(&b, "                    ", agentCmd)
    fmt.Fprintf(&b, "                    cwd %q\n", l.ProjectAbs)
    fmt.Fprintln(&b, `                }`)

    // Diff pane (50% width of top row).
    fmt.Fprintln(&b, `                pane size="50%" name="diff" {`)
    fmt.Fprintln(&b, `                    command "watch"`)
    fmt.Fprintln(&b, `                    args "--color" "-n" "2" "box-diff-watch"`)
    fmt.Fprintf(&b, "                    cwd %q\n", l.ProjectAbs)
    fmt.Fprintln(&b, `                }`)

    fmt.Fprintln(&b, `            }`)
    // Bottom: tests pane (30% rows of tab).
    fmt.Fprintln(&b, `            pane size="30%" name="tests" {`)
    fmt.Fprintln(&b, `                command "box-tests-watch"`)
    fmt.Fprintf(&b, "                cwd %q\n", l.ProjectAbs)
    fmt.Fprintln(&b, `            }`)

    fmt.Fprintln(&b, `        }`)
    fmt.Fprintln(&b, `    }`)

    // Tab 2: bare shell, same as focus.
    fmt.Fprintln(&b, `    tab name="shell" {`)
    fmt.Fprintln(&b, `        pane name="shell" {`)
    fmt.Fprintf(&b, "            command %q\n", l.Shell)
    fmt.Fprintf(&b, "            cwd %q\n", l.ProjectAbs)
    fmt.Fprintln(&b, `        }`)
    fmt.Fprintln(&b, `    }`)

    fmt.Fprintln(&b, `}`)
    return b.String()
}
```

**Implementation Notes:**

- Uses the existing `writeTabTemplate` and `writeCommand` helpers from
  `kdl.go`. Don't duplicate.
- Three named panes: `agent`, `diff`, `tests`. Names are user-visible
  via zellij's pane navigation.
- The agent pane uses `box-agent` wrapping (v0.2.5 behavior) so on agent
  exit the pane drops to zsh.
- The diff pane uses `box-diff-watch` (Unit 9) via `watch --color -n 2`.
  Same pattern as the v0.2.5 `box-git-watch`.
- The tests pane runs `box-tests-watch` directly (NOT via watch — the
  helper itself handles file-watching via watchexec internally).
- KDL splits: outer pane is horizontal (rows top/bottom). Inner top pane
  has size 70% rows, then the bottom tests pane gets 30% rows. The top
  pane is split vertical for agent | diff (50/50 columns).

**Acceptance Criteria:**

- [ ] `GenerateKDL(Layout{Mode: ModeRun, LayoutName: "reviewer", AgentCmd: ["claude"], ProjectAbs: "/p", Shell: "zsh"})`
      returns a string containing all of:
      - `tab name="reviewer" focus=true`
      - `pane size="50%" name="agent"`
      - `pane size="50%" name="diff"`
      - `pane size="30%" name="tests"`
      - `command "box-agent"` and `args "claude"` (agent wrapper)
      - `command "watch"` and `args "--color" "-n" "2" "box-diff-watch"` (diff)
      - `command "box-tests-watch"` (tests, no watch wrapper)
      - `tab name="shell"` (second tab present)
      - `default_tab_template` (status bar wrapping)

---

### Unit 6: Built-in `auditor` layout (Group B)

**File:** `internal/zellij/auditor.go` (new)

```go
// internal/zellij/auditor.go
package zellij

import (
    "fmt"
    "strings"
)

// auditorLayout renders the auditor KDL: agent (60% rows) on top, trail
// (40% rows) on bottom; shell second tab. The trail pane reads
// l.TrailFile via the BOX_TRAIL_FILE env var (set by lifecycle on the
// container) — for non-claude agents, lifecycle leaves TrailFile empty
// and the trail pane prints a static "trail not configured for agent
// <name>" message via the box-trail helper's fallback path.
func auditorLayout(l Layout) string {
    var b strings.Builder
    fmt.Fprintln(&b, "// Generated by agentbox; regenerated on every run.")
    fmt.Fprintln(&b, "layout {")
    writeTabTemplate(&b)

    fmt.Fprintln(&b, `    tab name="auditor" focus=true {`)
    fmt.Fprintln(&b, `        pane split_direction="horizontal" {`)

    // Agent pane (60% rows).
    fmt.Fprintln(&b, `            pane size="60%" name="agent" {`)
    agentCmd := l.AgentCmd
    if len(agentCmd) > 0 {
        agentCmd = append([]string{"box-agent"}, agentCmd...)
    }
    writeCommand(&b, "                ", agentCmd)
    fmt.Fprintf(&b, "                cwd %q\n", l.ProjectAbs)
    fmt.Fprintln(&b, `            }`)

    // Trail pane (40% rows).
    fmt.Fprintln(&b, `            pane size="40%" name="trail" {`)
    fmt.Fprintln(&b, `                command "box-trail"`)
    // No args needed — box-trail reads $BOX_TRAIL_FILE from env, set
    // at container create time when lifecycle decides trail is wired.
    fmt.Fprintf(&b, "                cwd %q\n", l.ProjectAbs)
    fmt.Fprintln(&b, `            }`)

    fmt.Fprintln(&b, `        }`)
    fmt.Fprintln(&b, `    }`)

    // Tab 2: shell, same as other layouts.
    fmt.Fprintln(&b, `    tab name="shell" {`)
    fmt.Fprintln(&b, `        pane name="shell" {`)
    fmt.Fprintf(&b, "            command %q\n", l.Shell)
    fmt.Fprintf(&b, "            cwd %q\n", l.ProjectAbs)
    fmt.Fprintln(&b, `        }`)
    fmt.Fprintln(&b, `    }`)

    fmt.Fprintln(&b, `}`)
    return b.String()
}
```

**Implementation Notes:**

- The auditor layout is two named panes (`agent`, `trail`) on tab 1, plus
  the standard shell tab. Simpler than reviewer.
- Trail file path is NOT in the KDL — it's an env var set on the
  container in Unit 8. Keeps the layout file portable across sessions.
- For non-claude agents, lifecycle leaves the BOX_TRAIL_FILE env var
  unset; box-trail (Unit 11) prints a placeholder message and exits
  cleanly.

**Acceptance Criteria:**

- [ ] `GenerateKDL(Layout{Mode: ModeRun, LayoutName: "auditor", AgentCmd: ["claude"], ProjectAbs: "/p", Shell: "zsh"})`
      contains:
      - `tab name="auditor" focus=true`
      - `pane size="60%" name="agent"`
      - `pane size="40%" name="trail"`
      - `command "box-agent"` (agent wrapper still applies)
      - `command "box-trail"` (no args; reads BOX_TRAIL_FILE)
      - `tab name="shell"` (second tab still present)
      - `default_tab_template`

---

### Unit 7: Lifecycle + CLI wiring

**File:** `internal/lifecycle/lifecycle.go` (edit), `internal/cli/run.go` (edit)

#### CLI `--layout` flag

```go
// internal/cli/run.go (additions to newRunCmd)
var (
    fresh        bool
    kitsFlag     string
    networkFlag  string
    layoutFlag   string  // NEW
    noAttach     bool
    detachOnExit bool
)
// ... in cmd.RunE:
opts := lifecycle.RunOpts{
    Fresh:   fresh,
    Attach:  !noAttach,
    Network: networkFlag,
    Layout:  layoutFlag, // NEW
}
// ... after existing flags:
cmd.Flags().StringVar(&layoutFlag, "layout", "",
    "zellij layout name (focus|reviewer|auditor|<custom>); "+
        "overrides [zellij].layout config")
```

NOT added to `agentbox shell` — per the brief, shell-mode layouts are
out of scope. `agentbox shell` keeps its single-pane layout regardless
of `[zellij].layout`.

#### `RunOpts` extension

```go
// internal/lifecycle/lifecycle.go
type RunOpts struct {
    Agent    string
    Kits     []string
    Fresh    bool
    Attach   bool
    Network  string
    NoZellij bool
    Layout   string // NEW: --layout flag value; empty falls back to cfg.Zellij.Layout
}
```

#### Layout resolution in `Run`

The existing `writeLayout` becomes `writeLayoutFor(spec LayoutSpec, ...)`:

```go
// writeLayoutFor renders the resolved layout to <state>/layout.kdl.
// For built-ins, it dispatches via GenerateKDL. For custom layouts, it
// loads the file, runs template substitution, and writes the result.
func writeLayoutFor(
    projID, projAbs string,
    spec zellij.LayoutSpec,
    mode zellij.Mode,
    agentCmd []string,
    shellName string,
    trailFile string, // empty unless trail is wired
) error {
    layout := zellij.Layout{
        Mode:       mode,
        AgentCmd:   agentCmd,
        ProjectAbs: projAbs,
        Shell:      shellName,
        LayoutName: spec.Name,
        TrailFile:  trailFile,
    }
    if spec.Kind == zellij.LayoutCustom {
        body, err := zellij.LoadCustom(spec.Path, zellij.BuildTemplateVars(layout))
        if err != nil {
            return exitcode.Wrap(exitcode.Generic, err)
        }
        layout.CustomKDL = body
    }
    body := zellij.GenerateKDL(layout)
    dir, err := state.SessionDir(projID)
    if err != nil {
        return err
    }
    if err := state.EnsureDir(dir); err != nil {
        return err
    }
    return os.WriteFile(filepath.Join(dir, "layout.kdl"), []byte(body), 0o600)
}
```

The existing `writeLayout` keeps its old signature as a thin shim for
the ModeShell call site (it always uses `focus`):

```go
func writeLayout(projID, projAbs string, mode zellij.Mode, agentCmd []string, shellName string) error {
    spec := zellij.LayoutSpec{Name: "focus", Kind: zellij.LayoutBuiltin}
    return writeLayoutFor(projID, projAbs, spec, mode, agentCmd, shellName, "")
}
```

In `Run`, before calling `writeLayoutFor`, resolve the layout:

```go
// In Lifecycle.Run, after EnsureBox returns the box:
home, _ := os.UserHomeDir()
layoutName := opts.Layout
if layoutName == "" {
    layoutName = l.Cfg.Zellij.Layout
}
spec, err := zellij.Resolve(layoutName, home)
if err != nil {
    return exitcode.Wrap(exitcode.InvalidArgs, err)
}

// Group B: trail wiring decision.
var trailFile string
if spec.Name == "auditor" && agent == "claude" {
    trailFile = "/etc/agentbox/trail.jsonl" // in-container path
}

if err := writeLayoutFor(box.ProjectID, box.CWD, spec, zellij.ModeRun, a.Cmd, l.Cfg.Shell.Shell, trailFile); err != nil {
    return err
}
```

The same resolution + write happens in the attach-rewrite path
(currently line 483 of lifecycle.go).

#### Dry-run handling

`agentbox run --layout reviewer --dry-run` prints the resolved layout
file path so users can inspect templated output.

```go
// In runDryRun (cli/run.go), after kitList computation, add:
home, _ := os.UserHomeDir()
spec, err := zellij.Resolve(layoutFlag, home) // empty falls back to "focus"
if err != nil {
    return exitcode.Wrap(exitcode.InvalidArgs, err)
}
// ... existing dry-run output ...
fmt.Fprintf(out, "# layout = %s (%s, %s)\n", spec.Name, spec.Kind, spec.Path)
```

(Output format: `# layout = reviewer (builtin)` or `# layout = myown (custom, /home/u/.config/agentbox/layouts/myown.kdl)`.)

**Implementation Notes:**

- Layout resolution happens at `Run` time, not at config load time. This
  lets users have a config-set layout that doesn't exist yet (file
  hasn't been created); they only hit the error when they `run`.
- Resolution errors exit with code 2 (`InvalidArgs`) — same as bad agent
  names, malformed kit lists, etc. (per `exitcode` conventions).
- `os.UserHomeDir()` failure (rare) is silently ignored — `home` becomes
  empty, custom resolution falls back to looking in
  `/.config/agentbox/layouts/...` which won't match anything sensible.
  This is good enough for v1.
- The trailFile decision is gated on BOTH `spec.Name == "auditor"` AND
  `agent == "claude"`. For other agents on auditor, lifecycle leaves
  `BOX_TRAIL_FILE` unset on the container; `box-trail` falls back to
  the placeholder message.

**Acceptance Criteria:**

- [ ] `agentbox run --layout reviewer --dry-run` includes
      `# layout = reviewer (builtin)` in stdout.
- [ ] `agentbox run --layout missing` exits 2 with the resolution
      error message naming both the built-in list and the path tried.
- [ ] `agentbox config set zellij.layout reviewer && agentbox run`
      uses the reviewer layout when `--layout` is not passed.
- [ ] `agentbox run --layout focus` overrides a config-set
      `zellij.layout = "reviewer"`.
- [ ] `agentbox shell --layout reviewer` is rejected by cobra
      (unknown flag) — `shell` doesn't define `--layout`.
- [ ] When `--layout auditor` is passed AND `agent == "claude"`:
      lifecycle's `EnsureBox` adds the trail file mount and
      BOX_TRAIL_FILE env var (Unit 8 covers the mount details).
- [ ] When `--layout auditor` is passed AND `agent != "claude"`:
      lifecycle does NOT add the trail mount; the layout still
      renders; box-trail prints the placeholder.

---

### Unit 8: Trail wiring — settings shadow + trail file mount (Group B)

**File:** `internal/lifecycle/lifecycle.go` (edit), `internal/lifecycle/trail.go` (new), `internal/runspec/runspec.go` (edit)

This unit wires three things into the container when auditor + claude
fires:

1. **Trail file mount.** `<state>/trail.jsonl` (host, touched if
   missing) → `/etc/agentbox/trail.jsonl` (container, RW).
2. **`BOX_TRAIL_FILE` env var.** Set to the in-container path so
   `box-trail` can find it without arguments.
3. **Claude settings shadow mount.** Lifecycle reads the user's host
   `~/.claude/settings.json` (if any), JSON-merges in agentbox's trail
   hooks, writes the result to `<state>/claude-settings.json`, and
   bind-mounts that file on top of `/root/.claude/settings.json` (RO,
   shadowing the bind-mounted host file inside the box).

#### `internal/lifecycle/trail.go` (new)

```go
package lifecycle

import (
    "encoding/json"
    "errors"
    "fmt"
    "os"
    "path/filepath"
)

// trailEnabled reports whether the resolved (layoutName, agent) tuple
// activates trail wiring. v1: only "auditor" + "claude".
func trailEnabled(layoutName, agent string) bool {
    return layoutName == "auditor" && agent == "claude"
}

// trailHookCommand is the in-container path of the hook recorder
// (installed by base kit). The hook's settings.json entries reference
// this absolute path.
const trailHookCommand = "/usr/local/bin/agentbox-hook-record"

// trailHookEvents are the Claude Code hook events the trail listens
// to. PostToolUseFailure and StopFailure are required for a complete
// trail per the design's research gate.
var trailHookEvents = []string{
    "PreToolUse",
    "PostToolUse",
    "PostToolUseFailure",
    "Stop",
    "StopFailure",
}

// MergeTrailHooks reads the user's host claude settings.json (if any),
// adds agentbox's trail hooks, and returns the merged JSON bytes.
//
// The user's existing hooks are preserved. agentbox's hooks are appended
// to each event's array as a new matcher group (matcher "*", with a
// single command-type hook pointing at trailHookCommand).
//
// userSettingsPath may be empty (or point to a non-existent file); in
// that case MergeTrailHooks starts from an empty document.
//
// Returns ErrInvalidUserSettings if the user's file exists but isn't
// valid JSON — caller decides whether to abort or fall back.
func MergeTrailHooks(userSettingsPath string) ([]byte, error) {
    var doc map[string]any
    if userSettingsPath != "" {
        body, err := os.ReadFile(userSettingsPath)
        if err == nil {
            if jerr := json.Unmarshal(body, &doc); jerr != nil {
                return nil, fmt.Errorf("%w: %s: %v", ErrInvalidUserSettings, userSettingsPath, jerr)
            }
        } else if !errors.Is(err, os.ErrNotExist) {
            return nil, fmt.Errorf("read %s: %w", userSettingsPath, err)
        }
    }
    if doc == nil {
        doc = map[string]any{}
    }
    hooks, _ := doc["hooks"].(map[string]any)
    if hooks == nil {
        hooks = map[string]any{}
        doc["hooks"] = hooks
    }
    for _, ev := range trailHookEvents {
        existing, _ := hooks[ev].([]any)
        agentboxGroup := map[string]any{
            "matcher": "*",
            "hooks": []any{
                map[string]any{
                    "type":    "command",
                    "command": trailHookCommand,
                },
            },
        }
        hooks[ev] = append(existing, agentboxGroup)
    }
    return json.MarshalIndent(doc, "", "  ")
}

// ErrInvalidUserSettings indicates the user's host settings.json is
// malformed and trail wiring couldn't proceed cleanly.
var ErrInvalidUserSettings = errors.New("invalid user claude settings")

// EnsureTrailFile touches <stateDir>/trail.jsonl if missing so the
// container's bind-mount source is a real file (not auto-created as a
// directory by podman, which would corrupt the mount).
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

// WriteShadowSettings writes the merged claude settings to
// <stateDir>/claude-settings.json with mode 0o600. Returns the host
// path. Caller bind-mounts it on top of /root/.claude/settings.json
// in the container.
func WriteShadowSettings(stateDir, hostHome string) (string, error) {
    userPath := filepath.Join(hostHome, ".claude", "settings.json")
    body, err := MergeTrailHooks(userPath)
    if err != nil {
        return "", err
    }
    out := filepath.Join(stateDir, "claude-settings.json")
    if err := os.WriteFile(out, body, 0o600); err != nil {
        return "", err
    }
    return out, nil
}
```

#### Runspec hook for trail mounts

`internal/runspec/runspec.go` gains conditional trail-related mounts and
env var, matching the existing claude-specific code path:

```go
// In BuildPodmanCreateArgs, after the existing claude.json mount block,
// add a trail block guarded on a new BuildInput field:

// Trail mount (auditor + claude only). Lifecycle sets in.TrailHostPath
// to the host JSONL path; we bind-mount it to /etc/agentbox/trail.jsonl
// and set BOX_TRAIL_FILE so box-trail can find it.
if in.TrailHostPath != "" {
    args.Mounts = append(args.Mounts, Mount{
        Source: in.TrailHostPath,
        Target: "/etc/agentbox/trail.jsonl",
        Mode:   "rw",
    })
    args.EnvVars = append(args.EnvVars, KV{
        Key:   "BOX_TRAIL_FILE",
        Value: "/etc/agentbox/trail.jsonl",
    })
}

// Settings shadow mount (auditor + claude only). Lifecycle has merged
// trail hooks into the user's settings.json and written the result to
// in.ClaudeSettingsHostPath; we bind-mount it on top of the existing
// ~/.claude bind so /root/.claude/settings.json is the agentbox copy
// inside the box. The host's actual ~/.claude/settings.json is never
// touched by agentbox.
if in.ClaudeSettingsHostPath != "" {
    args.Mounts = append(args.Mounts, Mount{
        Source: in.ClaudeSettingsHostPath,
        Target: "/root/.claude/settings.json",
        Mode:   "ro",
    })
}
```

`BuildInput` gains:

```go
type BuildInput struct {
    // ... existing fields ...
    TrailHostPath          string // <state>/trail.jsonl; set when trail wired
    ClaudeSettingsHostPath string // <state>/claude-settings.json; same gate
}
```

#### Lifecycle assembly

Inside `Lifecycle.createBox` (where `BuildInput` is constructed), after
the existing `state.EnsureSession` call:

```go
in := runspec.BuildInput{
    // ... existing fields ...
}
if trailEnabled(spec.Name, agent) {
    trail, err := EnsureTrailFile(stateDir)
    if err != nil {
        return container.Box{}, exitcode.Wrap(exitcode.Generic, err)
    }
    home, _ := os.UserHomeDir()
    settings, err := WriteShadowSettings(stateDir, home)
    if err != nil {
        return container.Box{}, exitcode.Wrap(exitcode.Generic, err)
    }
    in.TrailHostPath = trail
    in.ClaudeSettingsHostPath = settings
}
```

**Implementation Notes:**

- The shadow mount is **read-only** so a misbehaving Claude (or hook)
  can't corrupt the agentbox-managed settings. Hooks are write-only
  toward `<state>/trail.jsonl` (which IS RW).
- Mount ORDER matters in podman create: the shadow must come AFTER the
  parent `~/.claude:/root/.claude` mount. The runspec append-order is
  preserved in `PodmanRuntime.Create` (it iterates Mounts in order), so
  the existing claude-dir mount + the new shadow file mount produce the
  correct layered effect.
- `MergeTrailHooks` returns indented JSON for human inspection (some
  users will look at `<state>/claude-settings.json` to debug). Compact
  JSON would also work; indented is friendlier.
- `ErrInvalidUserSettings` is fail-fast: agentbox doesn't try to "fix"
  the user's malformed settings.json. The user gets a clear error and
  can either fix it or `unset zellij.layout` to step around.
- Concurrent JSONL writes: each hook command opens with `O_APPEND` (the
  default for `>>`). Writes < 4KB are atomic on Linux. Tool-call payloads
  routinely exceed 4KB (e.g. a large file diff in `tool_input`). For
  v1 we accept that very-large lines may interleave. Document in
  TRAIL.md.

**Acceptance Criteria:**

- [ ] `MergeTrailHooks("")` returns valid JSON with five hook events
      and one matcher group per event referencing `trailHookCommand`.
- [ ] `MergeTrailHooks(<path with existing PreToolUse hook>)` returns
      JSON where the user's PreToolUse group is preserved AND a new
      agentbox group is appended (so `len(hooks.PreToolUse) == 2`).
- [ ] `MergeTrailHooks(<path to malformed JSON>)` returns
      `ErrInvalidUserSettings` (errors.Is matches).
- [ ] `EnsureTrailFile(<dir>)` creates `<dir>/trail.jsonl` if absent;
      idempotent (running twice doesn't change content).
- [ ] `WriteShadowSettings(<state>, <home>)` writes
      `<state>/claude-settings.json` with mode 0o600.
- [ ] After `agentbox run --layout auditor` (claude agent), the
      resulting box has a mount entry for
      `<state>/trail.jsonl:/etc/agentbox/trail.jsonl:rw` and one for
      `<state>/claude-settings.json:/root/.claude/settings.json:ro`.
      EnvVars contains `BOX_TRAIL_FILE=/etc/agentbox/trail.jsonl`.
- [ ] After the same run with a non-claude agent, NEITHER trail mount
      is added; BOX_TRAIL_FILE is absent.
- [ ] After the run, the user's host `~/.claude/settings.json` is
      bit-identical to its pre-run state (verify via mtime/checksum).

---

### Unit 9: Helper — `box-diff-watch`

**File:** `internal/builtinkits/kits/base/box-diff-watch` (new, mode 0755)

```sh
#!/usr/bin/env bash
# box-diff-watch — render a delta-formatted diff of HEAD against the
# detected base branch. Designed for `watch --color -n 2 box-diff-watch`
# in the reviewer layout's diff pane.
#
# Base branch detection (in order):
#   1. git symbolic-ref refs/remotes/origin/HEAD → strip prefix
#   2. local "main" branch
#   3. local "master" branch
#   4. fallback: HEAD~5 (last 5 commits' worth)
#
# Side-by-side mode is enabled when terminal is wide enough (≥160 cols).

set -u

base=""

# 1. Try origin/HEAD.
if origin_head=$(git symbolic-ref -q refs/remotes/origin/HEAD 2>/dev/null); then
    base="${origin_head#refs/remotes/origin/}"
fi

# 2. Try main / master.
if [ -z "$base" ] && git rev-parse --verify --quiet main >/dev/null 2>&1; then
    base="main"
fi
if [ -z "$base" ] && git rev-parse --verify --quiet master >/dev/null 2>&1; then
    base="master"
fi

# 3. Header + delta diff.
hdr() { printf '\033[1;2m── %s ──\033[0m\n' "$1"; }

if [ -n "$base" ]; then
    hdr "diff against $base"
    range="$base..HEAD"
else
    hdr "no base branch — last 5 commits"
    range="HEAD~5..HEAD"
fi

# Side-by-side when terminal is wide.
cols=$(tput cols 2>/dev/null || echo 80)
delta_args=(--line-numbers --color-only)
if [ "$cols" -ge 160 ]; then
    delta_args+=(--side-by-side)
fi

# `git diff` returns 0 even with no diff; delta passes through cleanly.
git diff --color=always "$range" 2>/dev/null | delta "${delta_args[@]}"
if [ "${PIPESTATUS[0]}" -ne 0 ]; then
    echo
    hdr "git diff failed for $range"
fi
```

**Implementation Notes:**

- `--color-only` tells delta to emit ANSI without trying to page;
  paging is auto-disabled under `watch` anyway, but explicit is safer.
- `--line-numbers` is on always (small terminals can scroll horizontally
  in zellij if needed).
- Side-by-side threshold (160 cols) was chosen because delta's docs
  recommend ≥ 100 cols per side; 160 cols total = ~75 cols per side after
  delta's chrome. May tune in v2.
- delta auto-uses 24-bit color when supported; agentbox boxes always
  support it. No flag needed.
- The bracket form `delta_args+=(...)` requires bash, not POSIX sh.
  Hashbang is `bash` (consistent with existing base-kit helpers).

**Acceptance Criteria:**

- [ ] In a repo on a feature branch with two file changes against
      origin/main, `box-diff-watch` (run manually) prints a delta-
      rendered diff including the section header.
- [ ] In a repo with no remote and only `main`, the helper detects
      `main` as the base via heuristic 2.
- [ ] In an empty git repo (no commits), the helper falls through to
      `HEAD~5..HEAD` heuristic, sees `git diff` fail, prints the "git
      diff failed" header, exits cleanly.
- [ ] In a wide terminal (≥160 cols), output uses delta's side-by-side
      mode; in a narrow terminal it uses unified mode.

---

### Unit 10: Helper — `box-tests-watch`

**File:** `internal/builtinkits/kits/base/box-tests-watch` (new, mode 0755)

```sh
#!/usr/bin/env bash
# box-tests-watch — run the project's canonical test command on file
# change. Used in the reviewer layout's tests pane.
#
# Detection order (first match wins):
#   1. AGENTBOX_REVIEWER_TEST_CMD env var (set by lifecycle from
#      [zellij.reviewer] test_cmd in config, if present).
#   2. Makefile with a `test:` target → make test
#   3. Cargo.toml → cargo test
#   4. go.mod → go test ./...
#   5. package.json with a "test" script → bun/pnpm/yarn/npm test
#      (chosen by lockfile presence)
#   6. pyproject.toml or pytest.ini → pytest
#   7. None matched → print message, sleep forever (pane stays alive).

set -u

cmd=""

# 1. Explicit override.
if [ -n "${AGENTBOX_REVIEWER_TEST_CMD:-}" ]; then
    cmd="$AGENTBOX_REVIEWER_TEST_CMD"
fi

# 2-6. Heuristics, in order.
if [ -z "$cmd" ] && [ -f Makefile ] && grep -qE '^test:' Makefile; then
    cmd="make test"
elif [ -z "$cmd" ] && [ -f Cargo.toml ]; then
    cmd="cargo test"
elif [ -z "$cmd" ] && [ -f go.mod ]; then
    cmd="go test ./..."
elif [ -z "$cmd" ] && [ -f package.json ] && jq -e '.scripts.test' package.json >/dev/null 2>&1; then
    if [ -f bun.lock ] || [ -f bun.lockb ]; then
        cmd="bun test"
    elif [ -f pnpm-lock.yaml ]; then
        cmd="pnpm test"
    elif [ -f yarn.lock ]; then
        cmd="yarn test"
    else
        cmd="npm test"
    fi
elif [ -z "$cmd" ] && { [ -f pyproject.toml ] || [ -f pytest.ini ]; }; then
    cmd="pytest"
fi

# 7. Fallback.
if [ -z "$cmd" ]; then
    printf '\033[1;33m── no test command detected ──\033[0m\n'
    printf 'Set [zellij.reviewer] test_cmd = "..." in config, or set\n'
    printf 'AGENTBOX_REVIEWER_TEST_CMD env. Pane idle.\n'
    # Sleep forever so the pane stays visible.
    while :; do sleep 3600; done
fi

# Run with watchexec: --clear screen, --restart on busy, default
# .gitignore honored. watchexec runs the command once at startup.
exec watchexec --clear --restart -- "$cmd"
```

**Implementation Notes:**

- The override env var (`AGENTBOX_REVIEWER_TEST_CMD`) is plumbed by
  lifecycle when reviewer layout is selected AND
  `[zellij.reviewer].test_cmd` exists in config. v1 doesn't add that
  config field (per brief out-of-scope), but the helper supports it
  defensively so future config additions just work.
- watchexec's default (run at startup, then watch) is exactly what
  reviewers want — see something on first launch, see updates as you
  edit.
- `--clear` clears the pane between runs so old output doesn't pile up.
- `--restart` kills the previous run if a change fires before it
  completes — matches "rerun" intuition.
- For monorepos / multi-module, the heuristic uses the project root only.
  Per brief: "v1 fallback is the explicit `test_cmd` override".
- Lockfile detection is explicit per package manager — bun.lock /
  bun.lockb cover both old and new bun lockfile naming.

**Acceptance Criteria:**

- [ ] In a Go project (`go.mod` present), the helper invokes
      `watchexec ... -- go test ./...`.
- [ ] In a Rust project (`Cargo.toml`), invokes `cargo test`.
- [ ] In a JS project with `package.json` containing a `test` script
      AND `bun.lock`, invokes `bun test`.
- [ ] With `AGENTBOX_REVIEWER_TEST_CMD="echo hi"` set, invokes
      `watchexec ... -- echo hi` regardless of project markers.
- [ ] In an empty directory, prints the "no test command detected"
      message and stays alive (doesn't exit).
- [ ] In a Go project, editing a `.go` file triggers a re-run within
      ~1s.

---

### Unit 11: Helpers — `box-trail` + `agentbox-hook-record` (Group B)

**File:** `internal/builtinkits/kits/base/box-trail` (new, mode 0755)

```sh
#!/usr/bin/env bash
# box-trail — render Claude Code hook events from the JSONL trail file
# as one color-coded line per event. Used in the auditor layout.
#
# Reads $BOX_TRAIL_FILE; defaults to /etc/agentbox/trail.jsonl. If the
# var is empty (e.g. trail not wired for this agent), prints a
# placeholder message and sleeps forever (pane stays alive).

set -u

trail="${BOX_TRAIL_FILE:-}"

if [ -z "$trail" ]; then
    printf '\033[1;33m── trail not configured for this agent ──\033[0m\n'
    printf 'Trail is currently Claude-only. The auditor layout works\n'
    printf 'best with `--agent claude`.\n'
    while :; do sleep 3600; done
fi

# Header.
hdr() { printf '\033[1;2m── %s ──\033[0m\n' "$1"; }
hdr "trail: $trail"

# tail -F handles missing files (waits for creation), truncation, and
# rotation. Pipe to a jq filter that renders one line per event.
tail -n +1 -F "$trail" 2>/dev/null | jq -rR '
    fromjson? // empty
    | (.hook_event_name // "?") as $ev
    | (.tool_name // "")     as $tool
    | (.tool_input.command // .tool_input.file_path // .tool_input.url // "") as $detail
    | (.duration_ms // 0)    as $dur
    | (.reason // "")        as $reason
    | (
        ($ev | tojson),
        " ",
        ($tool | tojson),
        " ",
        ($detail | tojson),
        " ",
        ($dur | tostring),
        " ",
        ($reason | tojson)
      )
    | tostring
'
```

Wait — that's awkward. Let me write the renderer in plain bash so the
color-coding is straightforward. (The above sketch was too clever.)

```sh
#!/usr/bin/env bash
# box-trail — render Claude Code hook events from the JSONL trail file
# as one color-coded line per event.

set -u

trail="${BOX_TRAIL_FILE:-}"

if [ -z "$trail" ]; then
    printf '\033[1;33m── trail not configured for this agent ──\033[0m\n'
    printf 'Trail is currently Claude-only. The auditor layout works\n'
    printf 'best with `--agent claude`.\n'
    while :; do sleep 3600; done
fi

printf '\033[1;2m── trail: %s ──\033[0m\n' "$trail"

# Color codes per event (24-bit safe, fall back to 16-color).
color_for_event() {
    case "$1" in
        PreToolUse)         printf '\033[2m'   ;;  # dim (lots of these; quiet them)
        PostToolUse)        printf '\033[36m'  ;;  # cyan
        PostToolUseFailure) printf '\033[31m'  ;;  # red
        Stop)               printf '\033[35m'  ;;  # magenta
        StopFailure)        printf '\033[1;31m';;  # bold red
        *)                  printf '\033[37m'  ;;  # white (unknown)
    esac
}

# tail -F: missing-file-tolerant, follows truncation and rotation.
tail -n +1 -F "$trail" 2>/dev/null | while IFS= read -r line; do
    # Skip blank lines and lines that aren't valid JSON.
    [ -z "$line" ] && continue
    event=$(printf '%s' "$line" | jq -r '.hook_event_name // empty' 2>/dev/null)
    [ -z "$event" ] && continue

    tool=$(printf '%s' "$line"  | jq -r '.tool_name // empty' 2>/dev/null)
    detail=$(printf '%s' "$line" | jq -r '
        .tool_input.command
        // .tool_input.file_path
        // .tool_input.url
        // .reason
        // ""
    ' 2>/dev/null)
    dur=$(printf '%s' "$line" | jq -r '.duration_ms // empty' 2>/dev/null)
    ts=$(printf '%s' "$line" | jq -r '.trail_ts // ""' 2>/dev/null | cut -c12-19)
    [ -z "$ts" ] && ts="--:--:--"

    # Truncate detail to 60 cols.
    [ ${#detail} -gt 60 ] && detail="${detail:0:57}..."

    color=$(color_for_event "$event")
    suffix=""
    [ -n "$dur" ] && [ "$dur" != "null" ] && suffix=$(printf '\033[2m %sms\033[0m' "$dur")

    printf '%s%s  %-20s  %-6s  %s%s\n' \
        "$color" "$ts" "$event" "$tool" "$detail" "$suffix"
    printf '\033[0m' # reset
done
```

**File:** `internal/builtinkits/kits/base/agentbox-hook-record` (new, mode 0755)

```sh
#!/usr/bin/env bash
# agentbox-hook-record — invoked by Claude Code hooks. Reads JSON from
# stdin, adds a UTC ISO-8601 timestamp as `trail_ts`, and appends one
# line to $BOX_TRAIL_FILE.
#
# Configured by agentbox in the session-merged claude settings.json:
#   "hooks": {
#     "PostToolUse": [{
#       "matcher": "*",
#       "hooks": [{"type": "command", "command": "/usr/local/bin/agentbox-hook-record"}]
#     }],
#     ...
#   }

set -u

trail="${BOX_TRAIL_FILE:-/etc/agentbox/trail.jsonl}"
ts=$(date -u +%Y-%m-%dT%H:%M:%SZ)

# Read stdin once into a variable so we can preserve invalid lines.
input=$(cat)

# Append the timestamped record. jq renders compact (-c) JSON; valid JSON
# always produces ≤ a single line, valid for readers.
#
# If jq fails (malformed input), emit a stub record containing the raw
# input so the trail captures the failure visibly.
if printf '%s' "$input" | jq -e . >/dev/null 2>&1; then
    printf '%s' "$input" | jq -c --arg ts "$ts" '. + {trail_ts: $ts}' >> "$trail"
else
    jq -nc --arg ts "$ts" --arg raw "$input" \
        '{trail_ts: $ts, hook_event_name: "?", error: "malformed_input", raw: $raw}' >> "$trail"
fi

exit 0
```

**Implementation Notes:**

- `box-trail`'s `tail -F` handles every realistic file-state transition:
  the file may not exist when box-trail starts, may be created later
  (auditor session not fully wired yet), may be truncated/rotated.
- `jq -r '.tool_input.command // .tool_input.file_path // ...'` chains
  through the most common tool input shapes. Bash/Edit/Write/Read/
  WebFetch all populate at least one of those fields.
- `agentbox-hook-record` exits 0 always — even on malformed input. The
  Claude Code docs say "exit 2 = blocking error"; we don't want trail
  recording to ever block the agent. Worst case, we lose some events.
- Color truncation at 60 cols keeps lines readable in the typical
  trail-pane width (40% of an 80-col tab = 32 cols, but most users have
  wider terminals; 60 cols is a sensible cap).
- The hook record's `trail_ts` is added as a top-level field on top of
  whatever Claude Code sends. We don't replace any of Claude's fields;
  only add ours.
- `set -u` is intentional in agentbox-hook-record; lifecycle should
  always set BOX_TRAIL_FILE inside the box, but if it's missing we fall
  back to `/etc/agentbox/trail.jsonl` (same path).

**Acceptance Criteria:**

- [ ] `box-trail` with `BOX_TRAIL_FILE=` (empty) prints the placeholder
      and stays alive.
- [ ] `box-trail` with a fresh empty trail file prints just the header
      and idles.
- [ ] After feeding a sample tool-use JSONL into the trail file,
      `box-trail` prints a single-line entry with timestamp + event +
      tool name + truncated detail.
- [ ] Malformed JSON appended to the trail file causes box-trail to
      skip the line silently (no crash).
- [ ] `agentbox-hook-record` invoked with valid JSON on stdin appends
      one line to `$BOX_TRAIL_FILE` containing the original fields plus
      `trail_ts`.
- [ ] `agentbox-hook-record` invoked with garbage on stdin appends one
      line containing `error: "malformed_input"` and the raw input.
- [ ] `agentbox-hook-record` exits 0 in both happy and malformed-input
      cases.

---

### Unit 12: Install all helpers in base kit

**File:** `internal/builtinkits/kits/base/install.sh` (edit)

Append to the existing `box helpers` block:

```sh
install -m 0755 "${KIT_DIR}/box-diff-watch"        /usr/local/bin/box-diff-watch
install -m 0755 "${KIT_DIR}/box-tests-watch"       /usr/local/bin/box-tests-watch
install -m 0755 "${KIT_DIR}/box-trail"             /usr/local/bin/box-trail
install -m 0755 "${KIT_DIR}/agentbox-hook-record"  /usr/local/bin/agentbox-hook-record
```

If Group B is dropped, omit the last two lines.

**Acceptance Criteria:**

- [ ] After kit rebuild, `agentbox exec . which box-diff-watch box-tests-watch box-trail agentbox-hook-record`
      returns four executable paths.

---

### Unit 13: Documentation

**Files:** README.md (edit), docs/CLI.md (edit), docs/SPEC.md (edit), docs/LAYOUTS.md (new), docs/TRAIL.md (new — Group B only)

#### README

Add a short layout-mention to Quickstart and a `## Layouts` subsection
linking to docs/LAYOUTS.md:

```markdown
## Layouts

`agentbox run --layout <name>` selects a zellij layout. Built-ins:

- `focus` (default) — agent + git + system stats. The original layout.
- `reviewer` — agent + live diff vs base branch + watchexec test runner.
- `auditor` — agent + live trail of Claude Code tool calls (Claude only).

Sticky per project: set `[zellij] layout = "..."` in `.agentbox.toml`.
Drop your own KDL at `~/.config/agentbox/layouts/<name>.kdl` to add a
custom layout. See [docs/LAYOUTS.md](docs/LAYOUTS.md) for details.
```

#### docs/CLI.md

Add `--layout <name>` row to the `agentbox run` flag table; document
exit code 2 for unresolved names; cross-link to LAYOUTS.md.

#### docs/SPEC.md

Add a `[zellij]` subsection under "Top-level keys":

```markdown
[zellij]
layout = "focus"   # focus | reviewer | auditor | <custom>
```

#### docs/LAYOUTS.md (new)

Following the shape of `docs/KITS.md`:

1. Overview: what layouts are, when to use which.
2. Built-in layouts: one section each with an ASCII diagram + the panes
   + the helpers used.
3. Custom layouts: file location, template variables (with a code
   example), worked example.
4. New base-kit helpers: `box-diff-watch`, `box-tests-watch`, `box-trail`
   — what they do, env vars they read, when they're invoked.

#### docs/TRAIL.md (new — Group B only)

Following the shape of TRAIL-related sections elsewhere:

1. Why a trail (1 paragraph).
2. Event schema: list each top-level JSONL field, link to Claude
   Code's hook docs for tool-input subfields.
3. How agentbox wires it: settings.json shadow, mount layout, hook
   command, lifecycle gates.
4. Adding new agent adapters (codex, opencode in v2): what would need
   to change in MergeTrailHooks and box-trail's renderer.

**Acceptance Criteria:**

- [ ] README mentions `--layout` and links to docs/LAYOUTS.md.
- [ ] docs/CLI.md documents `--layout` for `agentbox run`.
- [ ] docs/SPEC.md `[zellij]` section exists.
- [ ] docs/LAYOUTS.md exists with sections for each built-in.
- [ ] docs/TRAIL.md exists (if Group B shipped) with the JSONL schema.
- [ ] All intra-doc links resolve to actual files.

---

## Implementation Order

Build in this order. Each unit's tests pass before the next starts.
Group A (must ship) ends after Unit 10; Group B (optional, hooks-API-
gated) is units 6, 8, 11.

**Group A — layout system + reviewer + custom layouts:**

1. **Unit 1** — `[zellij]` config schema. Foundation; no deps.
2. **Unit 2** — LayoutSpec + Resolve. Pure Go; depends on Unit 1
   only via TOML loading at integration time.
3. **Unit 3** — LoadCustom + BuildTemplateVars. Pure Go.
4. **Unit 4** — Rename runLayout → focusLayout in kdl.go. Behavior-
   preserving refactor; depends on Unit 2 for type changes.
5. **Unit 5** — reviewerLayout. Depends on Unit 4's helper functions
   (writeTabTemplate, writeCommand) and Unit 9's helper script for
   runtime use, but tests can pass with unit 9 not yet implemented
   (the layout just renders the `command "box-diff-watch"` reference).
6. **Unit 9** — box-diff-watch helper script. Depends on base kit
   already installing delta + git + tput.
7. **Unit 10** — box-tests-watch helper script. Depends on watchexec +
   jq in base kit.
8. **Unit 7** — Lifecycle + CLI wiring (--layout flag, RunOpts.Layout,
   writeLayoutFor, dry-run output). Depends on Units 1–5 for types and
   layout dispatch.
9. **Unit 12** — install.sh additions (layout helpers only; defer
   trail helpers to Unit 11 if Group B is in scope).
10. **Unit 13 (partial)** — README, CLI.md, SPEC.md updates +
    LAYOUTS.md (without auditor and trail sections).

**Group B — auditor + trail (decision gate at the start of this group):**

Before starting Unit 6, the implementer MUST verify the Claude Code
hooks API against `@anthropic-ai/claude-code@2.x` actually installed in
the box. Concrete check: capture a real PostToolUse hook payload from a
live claude session and confirm the schema matches what
`agentbox-hook-record` and `box-trail` expect (specifically: presence
of `tool_name`, `tool_input` subfields, `hook_event_name`, etc.). If
schema drift is significant, abort Group B and ship Group A only.

11. **Unit 8** — Trail wiring (settings shadow + trail file mount +
    runspec extension). Depends on lifecycle from Unit 7.
12. **Unit 11** — box-trail + agentbox-hook-record helpers.
13. **Unit 6** — auditorLayout. Depends on Unit 11 for the helper it
    references.
14. **Unit 12 (extension)** — install.sh adds trail helpers.
15. **Unit 13 (extension)** — README mentions auditor; LAYOUTS.md adds
    auditor section; new TRAIL.md.

## Testing

### Lint gates (run on every change)

```sh
go build ./...
go vet ./...
go test -count=1 ./...
shellcheck -s bash internal/builtinkits/kits/base/box-diff-watch
shellcheck -s bash internal/builtinkits/kits/base/box-tests-watch
shellcheck -s bash internal/builtinkits/kits/base/box-trail
shellcheck -s bash internal/builtinkits/kits/base/agentbox-hook-record
```

### Go unit tests

#### `internal/zellij/resolve_test.go` (new)

```go
// Table-driven coverage of Resolve: built-in names, empty fallback,
// existing custom file, missing custom file, stat error, special
// characters in name (rejected via filepath.Join's behavior).
```

Cases:

- `Resolve("focus", "/h")` → builtin
- `Resolve("", "/h")` → builtin "focus"
- `Resolve("auditor", "/h")` → builtin
- `Resolve("myown", tmphome)` with file present → custom
- `Resolve("myown", tmphome)` with no file → ErrLayoutNotFound
- `Resolve("../../etc/passwd", "/h")` → ErrLayoutNotFound (the join
  resolves but the file shouldn't exist; verifies traversal isn't a
  surprise feature)

#### `internal/zellij/custom_test.go` (new)

Table-driven `LoadCustom` cases:

- valid template with `{{.ProjectAbs}}` → substituted
- valid template with `{{.AgentCmdFull}}` for various AgentCmd shapes
- malformed template (`{{.Foo`) → parse error
- missing file → wrapped error naming path
- empty file → empty string output (no template vars to substitute)

`BuildTemplateVars` cases:

- empty AgentCmd → falls back to Shell
- single-element AgentCmd → no `args` line in AgentCmdFull
- multi-element AgentCmd → KDL-quoted args, properly joined
- AgentCmd with embedded quotes → properly escaped via `%q`

#### `internal/zellij/kdl_test.go` (extend)

Add test cases for `reviewerLayout` and (if Group B) `auditorLayout`
mirroring the existing fragment-level assertions for focus.

For the rename: existing `TestGenerateKDL_Run_HasAllPanes` etc. should
be updated to pass `LayoutName: "focus"` (or rely on the empty fallback)
and continue to pass byte-for-byte.

#### `internal/lifecycle/trail_test.go` (new — Group B only)

```go
// Table-driven MergeTrailHooks:
//  - empty user path → produces hooks for all 5 events
//  - user path doesn't exist → same as empty
//  - user path with existing PreToolUse → preserves user, appends agentbox
//  - user path with malformed JSON → ErrInvalidUserSettings
//  - user path with hooks but no PreToolUse → adds all 5
```

Plus:

- `EnsureTrailFile` in a tmp dir → creates file; second call no-op.
- `WriteShadowSettings` in a tmp dir → writes 0o600 file with
  expected JSON shape.

#### `internal/lifecycle/lifecycle_test.go` (extend)

Test that the trail mount + env var appear iff `trailEnabled(name, agent)`
returns true. Use an existing fake runtime path.

### Shell helper tests

A new `scripts/test/layouts_test.sh` (mirroring the easy-install harness
pattern from v0.2.0) exercising each helper:

- `box-diff-watch` against a fixture repo with main + feature branches
  + uncommitted changes.
- `box-tests-watch` against fixture project dirs (Go, Rust, Node, empty)
  — verify it picks the right cmd; doesn't actually run watchexec in
  the test (or runs it briefly and SIGTERM).
- `box-trail` with a fixture trail.jsonl containing 4 sample lines —
  verify exactly 4 rendered lines on stdout.
- `agentbox-hook-record` with sample valid JSON on stdin — verify trail
  file gets a new line with `trail_ts`.

A new `make test-layouts` target alongside `make test-install` to run
the harness.

### Manual verification (for Group B)

Cannot be fully automated because it requires Claude Code:

1. `agentbox run --layout auditor` (claude agent).
2. In the agent pane, trigger a tool call (e.g. ask claude to run
   `git status`).
3. Verify a line appears in the trail pane within ~1s.
4. Verify host's `~/.claude/settings.json` is bit-identical (mtime
   unchanged) before and after the run.
5. `cat <state>/trail.jsonl` has the expected JSONL records.

## Verification Checklist

After all Group A units land:

```sh
# Lint + tests
go build ./...
go vet ./...
go test -count=1 ./...

# Helpers compile cleanly
shellcheck -s bash internal/builtinkits/kits/base/box-diff-watch
shellcheck -s bash internal/builtinkits/kits/base/box-tests-watch

# CLI surface
agentbox run --help | grep -q '\-\-layout'
agentbox run --layout missing 2>&1 | grep -q 'not found'
agentbox config show --json | jq -r .zellij.layout  # should print "focus"

# Layouts smoke
make install
agentbox run --fresh --layout reviewer --no-attach
# Then: agentbox attach . and visually verify reviewer panes
```

After Group B (if shipped):

```sh
shellcheck -s bash internal/builtinkits/kits/base/box-trail
shellcheck -s bash internal/builtinkits/kits/base/agentbox-hook-record

# Trail smoke (Claude required)
agentbox run --fresh --layout auditor --no-attach
agentbox exec . sh -c '
    test -f /etc/agentbox/trail.jsonl
    test -n "$BOX_TRAIL_FILE"
    test -f /root/.claude/settings.json
    jq -e ".hooks.PostToolUse" /root/.claude/settings.json >/dev/null
'

# Verify host settings unchanged
sha256sum ~/.claude/settings.json  # should equal pre-run hash
```

## Notes for the implementer

- **Group B research gate is not optional.** Capture a real Claude Code
  hook payload before starting Unit 8/11/6. If the schema doesn't match
  what `box-trail` and `agentbox-hook-record` assume, fix THIS design
  doc first (or drop Group B), then implement.
- **Don't add a `[zellij.reviewer]` config table.** v1 has no per-layout
  config knobs except the top-level `[zellij].layout`. Out of scope.
- **Don't add mid-session layout switching.** Out of scope. Layouts are
  picked at create time only.
- **Don't ship a `dashboard` layout.** Out of scope (depends on
  DNS-stream + doctor-watch helpers that need their own design).
- **Match v0.2.5's box-agent wrapping in reviewer/auditor agent panes.**
  Both layouts wrap the agent command in `box-agent` for the same
  pane-stays-useful-on-exit behavior.
- **Mount order matters.** When trail wiring is active, the shadow
  settings mount MUST come AFTER the existing `~/.claude:/root/.claude`
  mount in `args.Mounts`. The current append order in BuildPodmanCreateArgs
  preserves this because the shadow runs in a later code branch.
- **Lifecycle's existing self-heal touch logic** (for `~/.claude.json`
  in v0.2.1) is the precedent for `EnsureTrailFile`. Same pattern: touch
  empty if missing so podman doesn't auto-create as a directory.
