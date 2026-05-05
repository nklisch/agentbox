# Design: Phase 1 — CLI scaffold, config loading, dry-run

## Overview

Phase 1 stands up the agentbox binary: project layout, all subcommands wired (most as
stubs), TOML config loading with shallow merge, `agentbox config` working end-to-end,
`agentbox doctor` minimal, and `agentbox run --dry-run` producing a fully-formed
`podman create` invocation. No container is ever started in this phase.

After Phase 1, you should be able to:

- `agentbox --version` and get version output
- `agentbox config path|show|edit` end-to-end, with `--json`, `--global`, `--project`
- `agentbox run --dry-run` print the exact `podman create ...` it *would* run, with the
  same-path mount and `agentbox-<project_id>` container name
- `agentbox doctor` exit 0 when podman is on PATH and `~/.local/share/agentbox/` is writable

The roadmap test checkpoint is the contract.

## Architecture

Standard Go layout. The CLI is the *adapter* layer; domain logic lives in `internal/`
packages with no cobra/CLI dependency, so it's testable in isolation.

```
agentbox/
├── cmd/
│   └── agentbox/
│       └── main.go              # entry point, signal handling, exit-code mapping
├── internal/
│   ├── cli/                     # cobra command tree (adapter)
│   │   ├── root.go
│   │   ├── run.go               # only meaningful --dry-run in P1
│   │   ├── shell.go             # stub
│   │   ├── attach.go            # stub
│   │   ├── exec.go              # stub
│   │   ├── ls.go                # stub
│   │   ├── rm.go                # stub
│   │   ├── build.go             # stub
│   │   ├── doctor.go            # real
│   │   ├── config.go            # real (subcommands: show / edit / path)
│   │   ├── completion.go        # real (cobra's built-in)
│   │   └── stub.go              # shared helper for "not yet implemented"
│   ├── config/                  # domain
│   │   ├── config.go            # types + DefaultConfig
│   │   ├── load.go              # paths + Load (overlay onto defaults)
│   │   └── config_test.go
│   ├── project/                 # domain
│   │   ├── id.go                # IDFromPath / IDFromCwd
│   │   └── id_test.go
│   ├── runspec/                 # domain
│   │   ├── runspec.go           # PodmanCreateArgs + ToShell
│   │   └── runspec_test.go
│   ├── state/                   # domain
│   │   ├── dir.go               # Dir / SessionDir / EnsureDir / IsWritable
│   │   └── dir_test.go
│   ├── doctor/                  # domain
│   │   ├── doctor.go            # Check + RunChecks
│   │   └── doctor_test.go
│   ├── exitcode/                # domain
│   │   └── exitcode.go          # constants + Err type
│   └── version/                 # domain
│       └── version.go           # set via -ldflags
├── go.mod
├── go.sum
├── Makefile                     # build / test / install convenience
└── (existing) docs/, CLAUDE.md
```

### Layering rules

- `cmd/agentbox` imports only `internal/cli` and `internal/exitcode`.
- `internal/cli` imports any internal domain package.
- Domain packages (`config`, `project`, `runspec`, `state`, `doctor`, `exitcode`,
  `version`) **must not** import `internal/cli` and **must not** import cobra. Tests
  prove this implicitly by working without cobra in scope.

### Cross-cutting decisions

- **Config schema fix:** `[runtime.containers]` from SPEC.md is invalid TOML (key
  collision with top-level `runtime = "podman"`). Renamed to `[containers]`. See
  PROGRESS.md → Deviations.
- **XDG compliance:** config dir via `os.UserConfigDir()` (honors `XDG_CONFIG_HOME`).
  Data dir honors `XDG_DATA_HOME`, falls back to `~/.local/share`.
- **Exit codes** propagate via `*exitcode.Err` returned from `RunE`. `main.go` unwraps
  via `errors.As` and calls `os.Exit`. Cobra is configured with `SilenceUsage = true`
  and `SilenceErrors = true` so it never double-prints or exits 1 itself.
- **Output streams:** structured output to stdout via `cmd.OutOrStdout()`, errors to
  stderr via `cmd.ErrOrStderr()`. Tests can capture both.
- **`--dry-run` in P1:** only `agentbox run --dry-run` produces real output. Other
  mutating commands (build, rm) are stubs in P1 and will gain dry-run in their phase.

---

## Implementation Units

### Unit 1: Module skeleton

**Files**:
- `go.mod` — module `github.com/nklisch/agentbox`, Go 1.23 (latest stable; pinned).
- `go.sum`
- `Makefile` with targets: `build`, `test`, `vet`, `install`, `clean`.
- `cmd/agentbox/main.go` — placeholder that calls `cli.Execute(os.Args[1:])`.
- `.gitignore` — `agentbox` binary, `dist/`, `*.test`.

**`Makefile`** (terse, no flair):

```make
VERSION ?= 0.0.1-dev
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
  -X github.com/nklisch/agentbox/internal/version.Version=$(VERSION) \
  -X github.com/nklisch/agentbox/internal/version.Commit=$(COMMIT) \
  -X github.com/nklisch/agentbox/internal/version.Date=$(DATE)

.PHONY: build test vet install clean
build:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o agentbox ./cmd/agentbox
test:
	go test ./...
vet:
	go vet ./...
install: build
	install -m 0755 agentbox $(HOME)/.local/bin/agentbox
clean:
	rm -f agentbox
```

**Acceptance Criteria**:
- [ ] `go build ./...` succeeds with no source files yet besides `main.go` calling a
      placeholder `cli.Execute`.
- [ ] `make build` produces a static binary (`file agentbox` mentions "statically
      linked" or similar — `CGO_ENABLED=0` makes this true on Linux).
- [ ] `go vet ./...` passes.

---

### Unit 2: `internal/version`

**File**: `internal/version/version.go`

```go
package version

import "fmt"

// Set via -ldflags at build time. Defaults are dev fallbacks.
var (
	Version = "0.0.1-dev"
	Commit  = "unknown"
	Date    = "unknown"
)

// String returns the human-readable version string used by --version.
func String() string {
	return fmt.Sprintf("%s (commit %s, built %s)", Version, Commit, Date)
}
```

**Acceptance Criteria**:
- [ ] `version.String()` returns `"0.0.1-dev (commit unknown, built unknown)"` when
      built without `-ldflags`.
- [ ] When `-X` ldflags override the vars, `String()` reflects them.

---

### Unit 3: `internal/exitcode`

**File**: `internal/exitcode/exitcode.go`

```go
package exitcode

import "fmt"

// Exit codes per docs/CLI.md "Exit codes".
const (
	OK           = 0
	Generic      = 1
	InvalidArgs  = 2
	Runtime      = 3
	NotFound     = 4
	KitBuild     = 5
	Network      = 6
	MountMissing = 7
	Interrupted  = 130
)

// Err carries an exit code with an optional underlying error. Returned from
// cobra RunE; main.go unwraps it to set the process exit code.
type Err struct {
	Code int
	Wrap error
}

func (e *Err) Error() string {
	if e.Wrap == nil {
		return fmt.Sprintf("exit code %d", e.Code)
	}
	return e.Wrap.Error()
}

func (e *Err) Unwrap() error { return e.Wrap }

// Wrap returns an *Err with the given code and underlying error. Returns nil
// if err is nil so callers can chain.
func Wrap(code int, err error) error {
	if err == nil {
		return nil
	}
	return &Err{Code: code, Wrap: err}
}

// New creates an *Err with a fresh fmt.Errorf message.
func New(code int, format string, a ...any) error {
	return &Err{Code: code, Wrap: fmt.Errorf(format, a...)}
}
```

**Acceptance Criteria**:
- [ ] `errors.As` extracts an `*Err` from any wrapped chain.
- [ ] `Wrap(2, nil)` returns nil.
- [ ] `New(3, "x %d", 1)` returns an `*Err` whose `.Error()` is `"x 1"`.

---

### Unit 4: `internal/state`

**File**: `internal/state/dir.go`

```go
package state

import (
	"os"
	"path/filepath"
)

// Dir returns the agentbox data directory: $XDG_DATA_HOME/agentbox or
// ~/.local/share/agentbox.
func Dir() (string, error) {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "agentbox"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "agentbox"), nil
}

// SessionDir returns the per-project session directory under Dir().
func SessionDir(projectID string) (string, error) {
	base, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "sessions", projectID), nil
}

// EnsureDir mkdir -p's path with mode 0700.
func EnsureDir(path string) error {
	return os.MkdirAll(path, 0o700)
}

// IsWritable returns true if a file can be created in path. Path must exist
// and be a directory; otherwise returns false.
func IsWritable(path string) bool {
	f, err := os.CreateTemp(path, ".agentbox-write-check-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}
```

**Acceptance Criteria**:
- [ ] `Dir()` returns `"$XDG_DATA_HOME/agentbox"` when env var is set.
- [ ] `Dir()` falls back to `~/.local/share/agentbox` when not.
- [ ] `EnsureDir` is idempotent (calling twice is fine).
- [ ] `IsWritable` returns false for `/proc/self/cmdline` (a non-dir).
- [ ] Test uses `t.TempDir()` + `t.Setenv("XDG_DATA_HOME", ...)` to isolate.

---

### Unit 5: `internal/project`

**File**: `internal/project/id.go`

```go
package project

import (
	"crypto/sha1"
	"encoding/hex"
	"os"
	"path/filepath"
)

// IDFromPath returns the 12-char project ID for an absolute path.
// project_id = sha1(realpath($PWD))[:12]
func IDFromPath(absPath string) string {
	h := sha1.Sum([]byte(absPath))
	return hex.EncodeToString(h[:])[:12]
}

// Resolve returns (project_id, realpath(cwd)) for the current working dir.
// Symlinks are resolved via filepath.EvalSymlinks. If the path does not exist
// after symlink resolution, returns the original cwd unmodified — useful for
// tests that operate in synthetic dirs.
func Resolve() (id string, abs string, err error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", err
	}
	abs, err = filepath.EvalSymlinks(cwd)
	if err != nil {
		// Fallback: use the unresolved cwd. Realistic only on systems where
		// EvalSymlinks fails on a real dir, which we don't expect.
		abs = cwd
	}
	return IDFromPath(abs), abs, nil
}

// ContainerName returns the canonical container name for a project_id.
func ContainerName(id string) string { return "agentbox-" + id }

// NetworkName returns the canonical podman network name for a project_id.
func NetworkName(id string) string { return "agentbox-net-" + id }
```

**Acceptance Criteria**:
- [ ] `IDFromPath("/tmp/abx-test")` returns the 12-char hex prefix of
      `sha1("/tmp/abx-test")`. Test uses a known fixture and verifies bytes.
- [ ] `IDFromPath` is deterministic — same input, same output.
- [ ] Returned ID matches `^[a-f0-9]{12}$`.
- [ ] `ContainerName("abc123")` → `"agentbox-abc123"`.
- [ ] `Resolve()` returns a path that has been symlink-resolved when the test
      sets up a symlink to a real dir.

---

### Unit 6: `internal/config` — types and defaults

**File**: `internal/config/config.go`

The `Containers` struct replaces `[runtime.containers]` from SPEC.md (see Deviations).

```go
package config

// Config is the merged agentbox configuration. Fields with zero values are
// treated as "unset" by toml.Decode and left at their default if the file
// doesn't mention them.
type Config struct {
	Runtime      string             `toml:"runtime" json:"runtime"`
	DefaultAgent string             `toml:"default_agent" json:"default_agent"`
	DefaultKits  []string           `toml:"default_kits" json:"default_kits"`
	Network      Network            `toml:"network" json:"network"`
	Mounts       Mounts             `toml:"mounts" json:"mounts"`
	Secrets      Secrets            `toml:"secrets" json:"secrets"`
	Resources    Resources          `toml:"resources" json:"resources"`
	Containers   Containers         `toml:"containers" json:"containers"`
	Shell        Shell              `toml:"shell" json:"shell"`
	Agents       map[string]Agent   `toml:"agents" json:"agents"`
}

type Network struct {
	Mode      string         `toml:"mode" json:"mode"`
	Safe      NetworkSafe    `toml:"safe" json:"safe"`
	Allowlist NetworkAllow   `toml:"allowlist" json:"allowlist"`
}

type NetworkSafe struct {
	Upstream        string   `toml:"upstream" json:"upstream"`
	UpstreamServers []string `toml:"upstream_servers" json:"upstream_servers"`
	NextDNSID       string   `toml:"nextdns_id" json:"nextdns_id"`
	BlockCategories []string `toml:"block_categories" json:"block_categories"`
	BlockDirectIP   bool     `toml:"block_direct_ip" json:"block_direct_ip"`
	ExtraBlock      []string `toml:"extra_block" json:"extra_block"`
	ExtraAllow      []string `toml:"extra_allow" json:"extra_allow"`
}

type NetworkAllow struct {
	Allow []string `toml:"allow" json:"allow"`
}

type Mounts struct {
	Gitconfig    bool              `toml:"gitconfig" json:"gitconfig"`
	SSHReadonly  bool              `toml:"ssh_readonly" json:"ssh_readonly"`
	Extra        []string          `toml:"extra" json:"extra"`
	AgentConfigs map[string]string `toml:"agent_configs" json:"agent_configs"`
}

type Secrets struct {
	Passthrough []string `toml:"passthrough" json:"passthrough"`
}

type Resources struct {
	CPUs   int    `toml:"cpus" json:"cpus"`
	Memory string `toml:"memory" json:"memory"`
	PIDs   int    `toml:"pids" json:"pids"`
}

// Containers replaces SPEC.md's [runtime.containers] (invalid TOML — key
// collision with top-level `runtime`).
type Containers struct {
	Enable       bool     `toml:"enable" json:"enable"`
	ExtraDevices []string `toml:"extra_devices" json:"extra_devices"`
	ExtraCaps    []string `toml:"extra_caps" json:"extra_caps"`
	Seccomp      string   `toml:"seccomp" json:"seccomp"`
}

type Shell struct {
	Shell   string            `toml:"shell" json:"shell"`
	Prompt  string            `toml:"prompt" json:"prompt"`
	History bool              `toml:"history" json:"history"`
	Aliases map[string]string `toml:"aliases" json:"aliases"`
}

type Agent struct {
	Kits []string `toml:"kits" json:"kits"`
	Cmd  []string `toml:"cmd" json:"cmd"`
}

// DefaultConfig returns the config with all SPEC.md defaults applied.
func DefaultConfig() Config {
	return Config{
		Runtime:      "podman",
		DefaultAgent: "claude",
		DefaultKits:  []string{"polyglot", "containers", "claude"},
		Network: Network{
			Mode: "safe",
			Safe: NetworkSafe{
				Upstream:      "quad9",
				BlockDirectIP: true,
			},
			Allowlist: NetworkAllow{
				Allow: []string{
					"registry.npmjs.org",
					"pypi.org",
					"github.com",
					"api.anthropic.com",
				},
			},
		},
		Mounts: Mounts{
			Gitconfig:   true,
			SSHReadonly: true,
			AgentConfigs: map[string]string{
				"claude":   "~/.claude",
				"codex":    "~/.codex",
				"opencode": "~/.opencode",
			},
		},
		Secrets: Secrets{
			Passthrough: []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY"},
		},
		Resources: Resources{
			CPUs:   4,
			Memory: "8g",
			PIDs:   512,
		},
		Containers: Containers{
			Enable:       false,
			ExtraDevices: []string{"/dev/fuse"},
			ExtraCaps:    []string{"SETUID", "SETGID"},
			Seccomp:      "containers",
		},
		Shell: Shell{
			Shell:   "zsh",
			Prompt:  "starship",
			History: true,
		},
		Agents: map[string]Agent{
			"claude": {
				Kits: []string{"polyglot", "claude"},
				Cmd:  []string{"claude", "--dangerously-skip-permissions"},
			},
			"codex": {
				Kits: []string{"polyglot", "codex"},
				Cmd:  []string{"codex"},
			},
		},
	}
}

// Validate checks that string-enum fields hold permitted values. Returns
// an error formatted for end users.
func (c Config) Validate() error {
	switch c.Runtime {
	case "podman", "docker":
	default:
		return fmt.Errorf("runtime: must be 'podman' or 'docker', got %q", c.Runtime)
	}
	switch c.Network.Mode {
	case "off", "safe", "allowlist", "open":
	default:
		return fmt.Errorf("network.mode: must be off|safe|allowlist|open, got %q", c.Network.Mode)
	}
	return nil
}
```

**Acceptance Criteria**:
- [ ] `DefaultConfig().Runtime == "podman"`.
- [ ] `DefaultConfig().Network.Mode == "safe"`.
- [ ] `DefaultConfig().Containers.Enable == false`.
- [ ] `DefaultConfig().Validate() == nil`.
- [ ] `Config{Runtime: "rkt"}.Validate()` returns an error mentioning "runtime".
- [ ] `Config{Runtime: "podman", Network: Network{Mode: "wrong"}}.Validate()`
      returns an error mentioning `"network.mode"`.

---

### Unit 7: `internal/config` — paths and load

**File**: `internal/config/load.go`

```go
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Paths holds the resolved global+project config paths.
type Paths struct {
	Global  string
	Project string
}

// DefaultPaths returns the standard paths:
//   Global  = $XDG_CONFIG_HOME/agentbox/config.toml (or ~/.config/agentbox/...)
//   Project = $PWD/.agentbox.toml
func DefaultPaths() (Paths, error) {
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve config dir: %w", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve cwd: %w", err)
	}
	return Paths{
		Global:  filepath.Join(cfgDir, "agentbox", "config.toml"),
		Project: filepath.Join(cwd, ".agentbox.toml"),
	}, nil
}

// Load reads (in order) defaults → global → project, overlaying each on the
// previous via toml.Decode. A missing file is not an error; a malformed file
// is. Returns the merged config plus the metadata for caller-visible
// "was this set in a file" checks.
func Load(p Paths) (Config, error) {
	cfg := DefaultConfig()
	if err := decodeIfExists(p.Global, &cfg); err != nil {
		return cfg, fmt.Errorf("global config: %w", err)
	}
	if p.Project != "" {
		if err := decodeIfExists(p.Project, &cfg); err != nil {
			return cfg, fmt.Errorf("project config: %w", err)
		}
	}
	return cfg, nil
}

func decodeIfExists(path string, cfg *Config) error {
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := toml.NewDecoder(f).Decode(cfg); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

// EnsureGlobalDir mkdir -p's the parent dir of paths.Global with 0700.
// Used by `config edit --global` to create the file on first edit.
func EnsureGlobalDir(p Paths) error {
	return os.MkdirAll(filepath.Dir(p.Global), 0o700)
}
```

**Implementation Notes**:
- BurntSushi/toml's `Decode` *overlays* — fields not present in the input file
  are left untouched on the destination struct. This is the merge semantics we
  want. Slices and maps named in the file are replaced wholesale (consistent
  with TOML semantics where redefinition replaces).
- `os.UserConfigDir()` honors `XDG_CONFIG_HOME`. It's the standard library's
  cross-platform choice; on Linux it's `$XDG_CONFIG_HOME` or `~/.config`.

**Acceptance Criteria**:
- [ ] `Load(Paths{Global: nonexistent, Project: nonexistent})` returns
      `DefaultConfig()` with no error.
- [ ] Loading a file with `runtime = "docker"` overrides only that field;
      defaults remain elsewhere.
- [ ] Loading a malformed TOML file returns an error containing the path and
      a parse-related message.
- [ ] Project file overrides global file when both set the same key.
- [ ] `DefaultPaths()` returns paths under `os.UserConfigDir()` and `os.Getwd()`.

---

### Unit 8: `internal/runspec`

**File**: `internal/runspec/runspec.go`

```go
package runspec

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nklisch/agentbox/internal/config"
	"github.com/nklisch/agentbox/internal/project"
)

// Mount is a single bind mount.
type Mount struct {
	Source string
	Target string
	Mode   string // "rw" or "ro"
}

// PodmanCreateArgs is the structured form of a `podman create` invocation.
// Phase 3 will use this to actually call podman; Phase 1 only renders it
// for --dry-run.
type PodmanCreateArgs struct {
	Name      string
	Labels    []KV
	Mounts    []Mount
	Workdir   string
	EnvNames  []string // -e NAME (no value, by name only — secrets policy)
	CPUs      int
	Memory    string
	PIDs      int
	CapDrop   []string
	SecOpt    []string
	Network   string
	Image     string
	Argv      []string // typically [sleep, infinity]
}

// KV is a stable-ordered key/value pair (labels are an ordered list, not a
// map, so output is deterministic).
type KV struct {
	Key   string
	Value string
}

// BuildInput is everything BuildPodmanCreateArgs needs that isn't in Config.
type BuildInput struct {
	ProjectID   string
	ProjectAbs  string
	ProjectName string
	Agent       string
	Kits        []string
	HomeDir     string
	StateDir    string
	Created     time.Time
}

// KitImageTag returns the canonical image tag for a kit list.
//   tag = "agentbox/" + sha1(joined_resolved_list)[:12]
// Phase 1 uses this for the dry-run output even though the image won't
// be built until Phase 2.
func KitImageTag(kits []string) string {
	resolved := make([]string, len(kits))
	copy(resolved, kits)
	sort.Strings(resolved)
	h := sha1.Sum([]byte(strings.Join(resolved, "+")))
	return "agentbox/" + hex.EncodeToString(h[:])[:12]
}

// NetworkArg returns the value for `--network` per network.mode. Phase 1
// returns the placeholder name; Phase 6 will create the network.
func NetworkArg(cfg config.Config, projectID string) string {
	switch cfg.Network.Mode {
	case "off":
		return "none"
	case "open":
		return "bridge"
	default: // safe, allowlist
		return project.NetworkName(projectID)
	}
}

// BuildPodmanCreateArgs assembles the full args from config + input.
func BuildPodmanCreateArgs(cfg config.Config, in BuildInput) (PodmanCreateArgs, error) {
	args := PodmanCreateArgs{
		Name:    project.ContainerName(in.ProjectID),
		Workdir: in.ProjectAbs,
		CPUs:    cfg.Resources.CPUs,
		Memory:  cfg.Resources.Memory,
		PIDs:    cfg.Resources.PIDs,
		CapDrop: []string{"ALL"},
		SecOpt:  []string{"no-new-privileges"},
		Network: NetworkArg(cfg, in.ProjectID),
		Image:   KitImageTag(in.Kits),
		Argv:    []string{"sleep", "infinity"},
	}

	args.Labels = []KV{
		{"agentbox", "1"},
		{"agentbox.project", in.ProjectName},
		{"agentbox.project_id", in.ProjectID},
		{"agentbox.cwd", in.ProjectAbs},
		{"agentbox.agent", in.Agent},
		{"agentbox.kits", strings.Join(in.Kits, ",")},
		{"agentbox.kit_image", args.Image},
		{"agentbox.created", in.Created.UTC().Format(time.RFC3339)},
	}

	// Same-path project mount (non-negotiable per CLAUDE.md).
	args.Mounts = []Mount{
		{Source: in.ProjectAbs, Target: in.ProjectAbs, Mode: "rw"},
	}
	if cfg.Mounts.Gitconfig && in.HomeDir != "" {
		args.Mounts = append(args.Mounts, Mount{
			Source: in.HomeDir + "/.gitconfig",
			Target: "/root/.gitconfig",
			Mode:   "rw",
		})
	}
	if cfg.Mounts.SSHReadonly && in.HomeDir != "" {
		args.Mounts = append(args.Mounts, Mount{
			Source: in.HomeDir + "/.ssh",
			Target: "/root/.ssh",
			Mode:   "ro",
		})
	}
	// Agent config dir for the resolved agent only.
	if src, ok := cfg.Mounts.AgentConfigs[in.Agent]; ok && src != "" {
		expanded := expandHome(src, in.HomeDir)
		args.Mounts = append(args.Mounts, Mount{
			Source: expanded,
			Target: "/root/." + in.Agent,
			Mode:   "rw",
		})
	}
	// Session state dir mounts (shell history, layout, effective config).
	if in.StateDir != "" {
		args.Mounts = append(args.Mounts,
			Mount{Source: in.StateDir + "/history", Target: "/root/.local/share/agentbox-history", Mode: "rw"},
			Mount{Source: in.StateDir + "/layout.kdl", Target: "/etc/agentbox/layout.kdl", Mode: "ro"},
			Mount{Source: in.StateDir + "/effective-config.toml", Target: "/etc/agentbox/config.toml", Mode: "ro"},
		)
	}
	// Extra mounts ("<src>:<dst>:<mode>"). Validation deferred to a later phase.
	for _, e := range cfg.Mounts.Extra {
		m, err := parseExtraMount(e, in.HomeDir)
		if err != nil {
			return args, fmt.Errorf("mounts.extra: %w", err)
		}
		args.Mounts = append(args.Mounts, m)
	}

	args.EnvNames = append(args.EnvNames, cfg.Secrets.Passthrough...)

	if cfg.Containers.Enable {
		// SPEC.md "Conditional flags (nested containers)".
		// Devices added at the runtime layer (not here in P1; Phase 7 wires this).
		// We emit the security-opt entries so the dry-run is honest.
		args.SecOpt = append(args.SecOpt,
			"seccomp=/etc/agentbox/seccomp/containers.json",
			"unmask=/proc/sys/net/ipv4",
		)
		// CapDrop ALL stays; Phase 7's containers kit re-grants SETUID/SETGID
		// via --cap-add at the runtime layer. P1 emits the dropped state and
		// leaves Phase 7 to add the cap-add lines.
	}

	return args, nil
}

// ToShell renders the args as a multi-line shell invocation suitable for
// `agentbox run --dry-run` output. The first line is the runtime + create
// verb; each flag is on its own indented line ending with ` \`.
func (p PodmanCreateArgs) ToShell(runtime string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s create \\\n", runtime)
	fmt.Fprintf(&b, "  --name %q \\\n", p.Name)
	for _, kv := range p.Labels {
		fmt.Fprintf(&b, "  --label %q \\\n", kv.Key+"="+kv.Value)
	}
	for _, m := range p.Mounts {
		fmt.Fprintf(&b, "  -v %q \\\n", m.Source+":"+m.Target+":"+m.Mode)
	}
	if p.Workdir != "" {
		fmt.Fprintf(&b, "  -w %q \\\n", p.Workdir)
	}
	if p.CPUs > 0 {
		fmt.Fprintf(&b, "  --cpus %d \\\n", p.CPUs)
	}
	if p.Memory != "" {
		fmt.Fprintf(&b, "  --memory %q \\\n", p.Memory)
	}
	if p.PIDs > 0 {
		fmt.Fprintf(&b, "  --pids-limit %d \\\n", p.PIDs)
	}
	for _, c := range p.CapDrop {
		fmt.Fprintf(&b, "  --cap-drop %s \\\n", c)
	}
	for _, s := range p.SecOpt {
		fmt.Fprintf(&b, "  --security-opt %s \\\n", s)
	}
	if p.Network != "" {
		fmt.Fprintf(&b, "  --network %q \\\n", p.Network)
	}
	for _, e := range p.EnvNames {
		fmt.Fprintf(&b, "  -e %s \\\n", e)
	}
	fmt.Fprintf(&b, "  %q", p.Image)
	for _, a := range p.Argv {
		fmt.Fprintf(&b, " %q", a)
	}
	b.WriteByte('\n')
	return b.String()
}

// expandHome replaces a leading "~" with homeDir.
func expandHome(s, homeDir string) string {
	if strings.HasPrefix(s, "~/") && homeDir != "" {
		return homeDir + s[1:]
	}
	if s == "~" && homeDir != "" {
		return homeDir
	}
	return s
}

// parseExtraMount parses "<src>:<dst>:<mode>" with ~ expansion on src.
func parseExtraMount(spec, homeDir string) (Mount, error) {
	parts := strings.Split(spec, ":")
	if len(parts) != 3 {
		return Mount{}, fmt.Errorf("expected <src>:<dst>:<mode>, got %q", spec)
	}
	mode := parts[2]
	if mode != "rw" && mode != "ro" {
		return Mount{}, fmt.Errorf("mode must be rw or ro, got %q", mode)
	}
	return Mount{
		Source: expandHome(parts[0], homeDir),
		Target: parts[1],
		Mode:   mode,
	}, nil
}
```

**Implementation Notes**:
- `KitImageTag` sorts the kit list before hashing — same kits in any order
  yield the same tag (per KITS.md "Image tag" requirement).
- Phase 1 emits the SecOpt entries for `containers.enable` honestly; Phase 7
  will add the matching `--cap-add SETUID --cap-add SETGID` and `--device
  /dev/fuse` flags. We don't emit them now to keep the dry-run honest about
  what *this* phase actually supports.
- All flag values are quoted with `%q` so paths containing spaces work.
  `--label` values are joined with `=` then quoted as one string (consistent
  with how SPEC.md shows them).

**Acceptance Criteria**:
- [ ] `KitImageTag(["claude","polyglot"])` equals `KitImageTag(["polyglot","claude"])`.
- [ ] Tag matches `^agentbox/[a-f0-9]{12}$`.
- [ ] `BuildPodmanCreateArgs` always includes a same-path mount where
      `Source == Target == ProjectAbs` and Mode is `"rw"`.
- [ ] Container name is `agentbox-<project_id>`.
- [ ] When `Network.Mode == "off"`, `args.Network == "none"`.
- [ ] When `Network.Mode == "open"`, `args.Network == "bridge"`.
- [ ] Otherwise, `args.Network == "agentbox-net-<project_id>"`.
- [ ] `ToShell("podman")` output contains `agentbox-` and the project abs path.
- [ ] `ToShell` is multi-line with `\` continuations and ends in a newline.
- [ ] Labels list is ordered (deterministic output).
- [ ] `Containers.Enable=true` adds `seccomp=` and `unmask=` security-opts
      but does NOT add cap-adds or devices in P1.

---

### Unit 9: `internal/doctor`

**File**: `internal/doctor/doctor.go`

```go
package doctor

import (
	"fmt"
	"os/exec"

	"github.com/nklisch/agentbox/internal/config"
	"github.com/nklisch/agentbox/internal/state"
)

// Status is the result of a single check.
type Status string

const (
	StatusOK   Status = "OK"
	StatusWarn Status = "WARN"
	StatusFail Status = "FAIL"
)

// Check is a single doctor check.
type Check struct {
	Name    string `json:"name"`
	Status  Status `json:"status"`
	Message string `json:"message"`
}

// Result aggregates all checks and a summary.
type Result struct {
	Checks []Check `json:"checks"`
}

// AnyFail reports whether any check has Status == FAIL.
func (r Result) AnyFail() bool {
	for _, c := range r.Checks {
		if c.Status == StatusFail {
			return true
		}
	}
	return false
}

// Run executes all P1 checks and returns the aggregated result.
func Run(cfg config.Config) Result {
	return Result{
		Checks: []Check{
			runtimeCheck(cfg.Runtime),
			stateDirCheck(),
		},
	}
}

func runtimeCheck(bin string) Check {
	if bin == "" {
		return Check{Name: "runtime", Status: StatusFail, Message: "config.runtime is empty"}
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return Check{
			Name:    "runtime",
			Status:  StatusFail,
			Message: fmt.Sprintf("%s not found in PATH", bin),
		}
	}
	return Check{
		Name:    "runtime",
		Status:  StatusOK,
		Message: fmt.Sprintf("%s found at %s", bin, path),
	}
}

func stateDirCheck() Check {
	dir, err := state.Dir()
	if err != nil {
		return Check{Name: "state-dir", Status: StatusFail, Message: err.Error()}
	}
	if err := state.EnsureDir(dir); err != nil {
		return Check{Name: "state-dir", Status: StatusFail, Message: fmt.Sprintf("create %s: %v", dir, err)}
	}
	if !state.IsWritable(dir) {
		return Check{Name: "state-dir", Status: StatusFail, Message: fmt.Sprintf("%s not writable", dir)}
	}
	return Check{Name: "state-dir", Status: StatusOK, Message: dir + " writable"}
}
```

**Acceptance Criteria**:
- [ ] `Run({Runtime: "podman"})` returns at least 2 checks.
- [ ] When podman is on PATH: `runtime` check status is `OK`.
- [ ] When the config runtime is `"definitely-missing-bin"`: status is `FAIL`.
- [ ] `Result.AnyFail()` is true when any check has FAIL.
- [ ] State-dir check creates the dir if missing (idempotent).

---

### Unit 10: `internal/cli` — root + global flags

**File**: `internal/cli/root.go`

```go
package cli

import (
	"github.com/spf13/cobra"

	"github.com/nklisch/agentbox/internal/version"
)

// GlobalFlags holds flags settable on every command. The struct is populated
// by cobra's persistent flag bindings on the root command.
type GlobalFlags struct {
	ConfigPath      string
	NoProjectConfig bool
	Runtime         string
	DryRun          bool
	JSON            bool
	Quiet           bool
	Verbose         bool
}

// global is the package-level holder. Tests use NewRootCmd() (which resets
// it) rather than reading this directly.
var global GlobalFlags

// NewRootCmd builds the cobra command tree. Each call returns a fresh tree
// so tests can build commands per-test without state leakage.
func NewRootCmd() *cobra.Command {
	global = GlobalFlags{}

	cmd := &cobra.Command{
		Use:           "agentbox",
		Short:         "Run AI coding agents inside per-project containers",
		Version:       version.String(),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	pf := cmd.PersistentFlags()
	pf.StringVar(&global.ConfigPath, "config", "", "override global config path")
	pf.BoolVar(&global.NoProjectConfig, "no-project-config", false, "ignore .agentbox.toml in $PWD")
	pf.StringVar(&global.Runtime, "runtime", "", "podman or docker (overrides config)")
	pf.BoolVar(&global.DryRun, "dry-run", false, "print equivalent shell commands; do not execute")
	pf.BoolVar(&global.JSON, "json", false, "machine-readable output where supported")
	pf.BoolVarP(&global.Quiet, "quiet", "q", false, "suppress non-error output")
	pf.BoolVarP(&global.Verbose, "verbose", "v", false, "verbose logging to stderr")

	cmd.AddCommand(
		newRunCmd(),
		newShellCmd(),
		newAttachCmd(),
		newExecCmd(),
		newLsCmd(),
		newRmCmd(),
		newBuildCmd(),
		newDoctorCmd(),
		newConfigCmd(),
		newCompletionCmd(),
	)
	return cmd
}

// Execute runs the root command with args and returns its error.
// main.go translates the error into an exit code.
func Execute(args []string) error {
	cmd := NewRootCmd()
	cmd.SetArgs(args)
	return cmd.Execute()
}

// loadConfig is a CLI-level helper that returns the merged config taking
// global flags into account: --config overrides paths.Global; --no-project-config
// blanks paths.Project; --runtime overrides cfg.Runtime.
func loadConfig() (configResult, error) {
	paths, err := config.DefaultPaths()
	if err != nil {
		return configResult{}, exitcode.Wrap(exitcode.Generic, err)
	}
	if global.ConfigPath != "" {
		paths.Global = global.ConfigPath
	}
	if global.NoProjectConfig {
		paths.Project = ""
	}
	cfg, err := config.Load(paths)
	if err != nil {
		return configResult{}, exitcode.Wrap(exitcode.InvalidArgs, err)
	}
	if global.Runtime != "" {
		cfg.Runtime = global.Runtime
	}
	if err := cfg.Validate(); err != nil {
		return configResult{}, exitcode.Wrap(exitcode.InvalidArgs, err)
	}
	return configResult{Config: cfg, Paths: paths}, nil
}

type configResult struct {
	Config config.Config
	Paths  config.Paths
}
```

**Implementation Notes**:
- Cobra's built-in `--version` is wired via the root command's `Version` field.
- `SilenceUsage` and `SilenceErrors` prevent cobra from printing its own
  diagnostics on RunE errors — main.go owns that.
- `loadConfig` lives in this package (not in `internal/config`) because it
  reads the package-level `global` flags. It's the bridge between CLI flags
  and domain config.

**Acceptance Criteria**:
- [ ] `NewRootCmd()` returns a tree with all 10 subcommands.
- [ ] `Execute([]string{"--version"})` writes a non-empty version string and
      returns nil.
- [ ] `Execute([]string{"--help"})` writes usage and returns nil.

---

### Unit 11: `internal/cli` — config subcommand

**File**: `internal/cli/config.go`

```go
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"

	"github.com/nklisch/agentbox/internal/config"
	"github.com/nklisch/agentbox/internal/exitcode"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect or edit configuration",
	}
	cmd.AddCommand(newConfigShowCmd(), newConfigEditCmd(), newConfigPathCmd())
	return cmd
}

func newConfigShowCmd() *cobra.Command {
	var effective bool
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Print the merged configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := loadConfig()
			if err != nil {
				return err
			}
			cfg := res.Config
			// --effective applies the same global-flag overrides that
			// loadConfig already applied, so for P1 there's nothing extra
			// to do here. Future flags (network, kits) would apply here.
			_ = effective

			if global.JSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(cfg)
			}
			return toml.NewEncoder(cmd.OutOrStdout()).Encode(cfg)
		},
	}
	cmd.Flags().BoolVar(&effective, "effective", false, "include CLI-flag overrides as if a `run` were happening now")
	return cmd
}

func newConfigPathCmd() *cobra.Command {
	var (
		globalFlag  bool
		projectFlag bool
	)
	cmd := &cobra.Command{
		Use:   "path",
		Short: "Print the config file path and exit",
		RunE: func(cmd *cobra.Command, args []string) error {
			paths, err := config.DefaultPaths()
			if err != nil {
				return exitcode.Wrap(exitcode.Generic, err)
			}
			if global.ConfigPath != "" {
				paths.Global = global.ConfigPath
			}
			target := paths.Global
			if projectFlag {
				target = paths.Project
			}
			fmt.Fprintln(cmd.OutOrStdout(), target)
			return nil
		},
	}
	cmd.Flags().BoolVar(&globalFlag, "global", false, "print global config path (default)")
	cmd.Flags().BoolVar(&projectFlag, "project", false, "print project config path")
	cmd.MarkFlagsMutuallyExclusive("global", "project")
	return cmd
}

func newConfigEditCmd() *cobra.Command {
	var (
		globalFlag  bool
		projectFlag bool
	)
	cmd := &cobra.Command{
		Use:   "edit",
		Short: "Open $EDITOR on the config file",
		RunE: func(cmd *cobra.Command, args []string) error {
			paths, err := config.DefaultPaths()
			if err != nil {
				return exitcode.Wrap(exitcode.Generic, err)
			}
			if global.ConfigPath != "" {
				paths.Global = global.ConfigPath
			}
			target := paths.Global
			if projectFlag {
				target = paths.Project
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return exitcode.Wrap(exitcode.Generic, err)
			}
			if _, err := os.Stat(target); errors.Is(err, fs.ErrNotExist) {
				if err := os.WriteFile(target, []byte(""), 0o600); err != nil {
					return exitcode.Wrap(exitcode.Generic, err)
				}
			}
			editor := os.Getenv("EDITOR")
			if editor == "" {
				editor = "vi"
			}
			ec := exec.Command(editor, target)
			ec.Stdin = os.Stdin
			ec.Stdout = cmd.OutOrStdout()
			ec.Stderr = cmd.ErrOrStderr()
			if err := ec.Run(); err != nil {
				return exitcode.Wrap(exitcode.Generic, fmt.Errorf("editor %s: %w", editor, err))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&globalFlag, "global", false, "edit global config (default)")
	cmd.Flags().BoolVar(&projectFlag, "project", false, "edit project config")
	cmd.MarkFlagsMutuallyExclusive("global", "project")
	return cmd
}
```

**Acceptance Criteria**:
- [ ] `agentbox config path` prints a path containing `agentbox/config.toml`.
- [ ] `agentbox config path --project` prints a path ending in `.agentbox.toml`.
- [ ] `agentbox config show --json` produces valid JSON parseable by `jq .`.
- [ ] `agentbox config show` produces TOML containing `runtime = "podman"`.
- [ ] `agentbox config show --json` includes the field `"runtime": "podman"`.

---

### Unit 12: `internal/cli` — run command (real --dry-run)

**File**: `internal/cli/run.go`

```go
package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/nklisch/agentbox/internal/config"
	"github.com/nklisch/agentbox/internal/exitcode"
	"github.com/nklisch/agentbox/internal/project"
	"github.com/nklisch/agentbox/internal/runspec"
	"github.com/nklisch/agentbox/internal/state"
)

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

			// Apply per-command overrides.
			if networkFlag != "" {
				cfg.Network.Mode = networkFlag
				if err := cfg.Validate(); err != nil {
					return exitcode.Wrap(exitcode.InvalidArgs, err)
				}
			}

			agent := cfg.DefaultAgent
			if len(args) == 1 {
				agent = args[0]
			}
			a, ok := cfg.Agents[agent]
			if !ok {
				return exitcode.New(exitcode.InvalidArgs, "agent %q not defined in [agents.*]", agent)
			}

			kits := a.Kits
			if kitsFlag != "" {
				kits = strings.Split(kitsFlag, ",")
			}

			id, abs, err := project.Resolve()
			if err != nil {
				return exitcode.Wrap(exitcode.Generic, err)
			}

			home, _ := os.UserHomeDir()
			stateDir, err := state.SessionDir(id)
			if err != nil {
				return exitcode.Wrap(exitcode.Generic, err)
			}

			in := runspec.BuildInput{
				ProjectID:   id,
				ProjectAbs:  abs,
				ProjectName: filepath.Base(abs),
				Agent:       agent,
				Kits:        kits,
				HomeDir:     home,
				StateDir:    stateDir,
				Created:     time.Now(),
			}

			if global.DryRun {
				rs, err := runspec.BuildPodmanCreateArgs(cfg, in)
				if err != nil {
					return exitcode.Wrap(exitcode.Generic, err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "# project_id = %s\n", id)
				fmt.Fprintf(cmd.OutOrStdout(), "# kits = %s\n", strings.Join(kits, ","))
				fmt.Fprintf(cmd.OutOrStdout(), "# network = %s\n", cfg.Network.Mode)
				fmt.Fprint(cmd.OutOrStdout(), rs.ToShell(cfg.Runtime))
				_ = fresh
				_ = noAttach
				_ = detachOnExit
				return nil
			}
			return notImplementedRunE(cmd)
		},
	}
	cmd.Flags().BoolVar(&fresh, "fresh", false, "remove any existing box for this project before creating")
	cmd.Flags().StringVar(&kitsFlag, "kits", "", "override the kit list (comma-separated)")
	cmd.Flags().StringVar(&networkFlag, "network", "", "override network.mode for this run")
	cmd.Flags().BoolVar(&noAttach, "no-attach", false, "create/start the box but don't attach")
	cmd.Flags().BoolVar(&detachOnExit, "detach-on-exit", false, "stop the container when the agent process exits")
	return cmd
}

// (filepath imported via internal/cli/run.go header — added in actual implementation)
```

**Implementation Notes**:
- Project name is `filepath.Base(abs)` per SPEC.md "agentbox.project = basename($PWD)".
- Comments at the top of dry-run output (`# project_id = ...`) document context
  for humans without breaking shell-script parsability — bash treats `#` lines
  as comments.
- `_ = fresh` etc. are deliberate — the flags are accepted now to lock the CLI
  surface, but only used in later phases.

**Acceptance Criteria**:
- [ ] `agentbox run --dry-run` exits 0.
- [ ] Output contains a line starting with `# project_id =` followed by 12
      hex chars.
- [ ] Output contains the substring `agentbox-` (the container name prefix).
- [ ] Output contains `realpath($PWD)` (the project abs path appears in `-w`
      and in the same-path mount).
- [ ] Output contains `--cap-drop ALL` and `--security-opt no-new-privileges`.
- [ ] `agentbox run nonexistent --dry-run` exits 2 with stderr message about
      the agent not being defined.

---

### Unit 13: `internal/cli` — doctor command

**File**: `internal/cli/doctor.go`

```go
package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/nklisch/agentbox/internal/doctor"
	"github.com/nklisch/agentbox/internal/exitcode"
)

func newDoctorCmd() *cobra.Command {
	var fix bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Verify runtime, mounts, kits",
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := loadConfig()
			if err != nil {
				return err
			}
			result := doctor.Run(res.Config)

			if global.JSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				if err := enc.Encode(result); err != nil {
					return exitcode.Wrap(exitcode.Generic, err)
				}
			} else {
				for _, c := range result.Checks {
					fmt.Fprintf(cmd.OutOrStdout(), "[%s] %s — %s\n", c.Status, c.Name, c.Message)
				}
			}
			if result.AnyFail() {
				return exitcode.New(exitcode.Generic, "doctor reported failures")
			}
			_ = fix
			return nil
		},
	}
	cmd.Flags().BoolVar(&fix, "fix", false, "attempt safe auto-remediation")
	return cmd
}
```

**Acceptance Criteria**:
- [ ] `agentbox doctor` prints `[OK]` or `[FAIL]` lines.
- [ ] Exits 0 when all checks pass; exits 1 when any FAIL.
- [ ] `agentbox doctor --json` produces parseable JSON with a top-level
      `"checks"` array.

---

### Unit 14: `internal/cli` — completion command

**File**: `internal/cli/completion.go`

```go
package cli

import "github.com/spf13/cobra"

func newCompletionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                   "completion <shell>",
		Short:                 "Generate shell completion script",
		DisableFlagsInUseLine: true,
		ValidArgs:             []string{"bash", "zsh", "fish"},
		Args:                  cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return cmd.Root().GenBashCompletion(cmd.OutOrStdout())
			case "zsh":
				return cmd.Root().GenZshCompletion(cmd.OutOrStdout())
			case "fish":
				return cmd.Root().GenFishCompletion(cmd.OutOrStdout(), true)
			}
			return nil
		},
	}
	return cmd
}
```

**Acceptance Criteria**:
- [ ] `agentbox completion bash` writes a non-empty script.
- [ ] `agentbox completion zsh` writes a script containing `_agentbox`.
- [ ] `agentbox completion fish` writes a non-empty script.
- [ ] `agentbox completion ksh` returns a usage error (invalid arg).

---

### Unit 15: `internal/cli` — stub commands

**File**: `internal/cli/stub.go`

```go
package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/nklisch/agentbox/internal/exitcode"
)

// notImplementedRunE returns an exit-1 error explaining the command lands
// in a later phase. Used by all P1 stubs.
func notImplementedRunE(cmd *cobra.Command) error {
	return exitcode.New(
		exitcode.Generic,
		"agentbox %s: not yet implemented (lands in a later phase)",
		cmd.Name(),
	)
}

// stubCmd builds a cobra.Command whose RunE prints the not-implemented
// message and returns the appropriate exit-coded error. Used by shell,
// attach, exec, ls, rm, build.
func stubCmd(use, short string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		RunE:  func(cmd *cobra.Command, args []string) error { return notImplementedRunE(cmd) },
	}
}

func newShellCmd() *cobra.Command  { return stubCmd("shell", "Bare interactive shell in the per-project box") }
func newAttachCmd() *cobra.Command { return stubCmd("attach <project_id>", "Reattach to a running box's zellij session") }
func newExecCmd() *cobra.Command   { return stubCmd("exec <project_id> <command>", "Run a one-off command inside a live box") }
func newLsCmd() *cobra.Command     { return stubCmd("ls", "List boxes") }
func newRmCmd() *cobra.Command     { return stubCmd("rm <project_id>", "Stop and remove boxes + their session state") }
func newBuildCmd() *cobra.Command  { return stubCmd("build [kit_list]", "Build (or rebuild) a composed kit image") }

// Redacted from this design: a fake unused import so the file in fmt isn't
// dead — fmt is unused once we delete this comment. (Implementer should
// drop the import if not needed.)
var _ = fmt.Sprintf
```

**Implementation Notes**:
- Stubs deliberately accept any args without parsing — they don't implement
  anything. The detailed flag surfaces will be added in their respective phases.
- `stubCmd` keeps the CLI surface complete enough that `agentbox --help` shows
  every command. Discoverability matters even for unimplemented commands.
- Drop the `var _ = fmt.Sprintf` if you don't use `fmt` in this file. (It was
  in the design to remind you fmt may not be needed.)

**Acceptance Criteria**:
- [ ] `agentbox shell` exits 1 with stderr `"agentbox shell: not yet implemented..."`.
- [ ] `agentbox ls` does the same with `ls` as the command name.
- [ ] All 6 stubs exist and appear in `agentbox --help` output.

---

### Unit 16: `cmd/agentbox/main.go`

**File**: `cmd/agentbox/main.go`

```go
package main

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/nklisch/agentbox/internal/cli"
	"github.com/nklisch/agentbox/internal/exitcode"
)

func main() {
	// SIGINT/SIGTERM → exit 130 immediately. Subcommands that need cleanup
	// can register their own handlers in a later phase; P1 has nothing to
	// clean up.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		os.Exit(exitcode.Interrupted)
	}()

	err := cli.Execute(os.Args[1:])
	if err == nil {
		return
	}
	var ee *exitcode.Err
	if errors.As(err, &ee) {
		if ee.Wrap != nil {
			fmt.Fprintln(os.Stderr, "agentbox:", ee.Wrap.Error())
		}
		os.Exit(ee.Code)
	}
	// Unwrapped error from cobra (e.g. unknown flag): treat as InvalidArgs.
	fmt.Fprintln(os.Stderr, "agentbox:", err.Error())
	os.Exit(exitcode.InvalidArgs)
}
```

**Implementation Notes**:
- Cobra's argument-parsing errors come back from `Execute` *not* wrapped in
  `*exitcode.Err`. We map those to exit code 2 (`InvalidArgs`) per CLI.md.
  Tests verify this for `agentbox --bogus-flag`.
- `SilenceUsage = true` on the root command means cobra won't print help on
  RunE errors. We print the error message ourselves.

**Acceptance Criteria**:
- [ ] `agentbox` (no args) prints help and exits 0.
- [ ] `agentbox --bogus` exits 2 with an error message on stderr.
- [ ] `agentbox doctor` (with podman installed) exits 0.

---

## Implementation Order

Layered so each unit only depends on already-built units. Within a layer,
agents can run in parallel.

**Layer 1 (no internal deps):**
1. Unit 1 — Module skeleton (`go.mod`, `Makefile`, placeholder `main.go`)
2. Unit 2 — `internal/version`
3. Unit 3 — `internal/exitcode`
4. Unit 4 — `internal/state`
5. Unit 5 — `internal/project`

**Layer 2 (depends on Layer 1):**
6. Unit 6 — `internal/config` types + defaults
7. Unit 7 — `internal/config` paths + Load (depends on Unit 6)

**Layer 3 (depends on Layer 2):**
8. Unit 8 — `internal/runspec` (depends on Units 5, 6)
9. Unit 9 — `internal/doctor` (depends on Units 4, 6)

**Layer 4 (depends on Layer 3):**
10. Unit 10 — `internal/cli/root.go` (depends on Units 2, 6, 7, 3)
11. Unit 11 — `internal/cli/config.go` (depends on Units 6, 7, 10)
12. Unit 12 — `internal/cli/run.go` (depends on Units 4, 5, 6, 8, 10)
13. Unit 13 — `internal/cli/doctor.go` (depends on Units 9, 10)
14. Unit 14 — `internal/cli/completion.go` (depends on Unit 10)
15. Unit 15 — `internal/cli/stub.go` (depends on Units 3, 10)

**Layer 5 (final wireup):**
16. Unit 16 — `cmd/agentbox/main.go` (depends on Units 10, 3)

Tests for each unit are written alongside the unit. Run `go test ./...` after
the full set is in place.

---

## Testing

Each domain package gets a test file. The CLI package gets a single integration
test file using cobra's `Execute` with captured buffers.

### `internal/config/config_test.go`
- `TestDefaultConfig` — defaults match SPEC.md.
- `TestValidate` — table-driven: valid / invalid runtime / invalid network.mode.

### `internal/config/load_test.go` (or add to config_test.go)
- `TestLoad_NoFiles` — Load with non-existent paths returns DefaultConfig and nil.
- `TestLoad_GlobalOnly` — global file overrides specific fields, defaults remain.
- `TestLoad_ProjectOverridesGlobal` — project's value wins.
- `TestLoad_MalformedTOML` — error contains the path.
- `TestDefaultPaths` — path strings are non-empty and contain expected suffixes.
- All file fixtures via `t.TempDir()`; `t.Setenv("XDG_CONFIG_HOME", ...)`.

### `internal/project/id_test.go`
- `TestIDFromPath_Deterministic` — same input → same output.
- `TestIDFromPath_KnownVector` — `IDFromPath("/tmp/abx-test")` matches a
  pre-computed sha1 prefix (verify via Python or `printf %s | sha1sum`
  during test authoring).
- `TestIDFromPath_Format` — output matches `^[a-f0-9]{12}$`.
- `TestContainerName` / `TestNetworkName` — exact prefixes.

### `internal/state/dir_test.go`
- `TestDir_XDG` — with `XDG_DATA_HOME` set, returns `$XDG_DATA_HOME/agentbox`.
- `TestDir_FallbackHome` — without it, returns under home.
- `TestEnsureDir_Idempotent` — calling twice on same path is fine.
- `TestIsWritable` — true on a temp dir, false on a non-existent path.

### `internal/runspec/runspec_test.go`
- `TestKitImageTag_OrderIndependent` — equal for any permutation.
- `TestKitImageTag_Format` — matches `^agentbox/[a-f0-9]{12}$`.
- `TestNetworkArg` — table-driven across all four modes.
- `TestBuildPodmanCreateArgs` — given a fixture Config + BuildInput,
  verifies: container name, same-path mount present, image tag set, labels
  in expected order, `cap-drop ALL` and `no-new-privileges` always present.
- `TestToShell_Multiline` — output contains `\n` and ends with `\n`.
- `TestToShell_ContainsExpected` — output contains the project abs path
  and the container name.
- `TestContainersEnable` — `Containers.Enable=true` adds seccomp/unmask.

### `internal/doctor/doctor_test.go`
- `TestRun_OK` — using a runtime that exists in PATH (`sh` for testing —
  override via the runtime field), all checks pass.
- `TestRun_RuntimeFail` — runtime `"definitely-missing-bin"` → FAIL.
- `TestAnyFail` — table-driven.

### `internal/cli/cli_test.go`

Integration-style tests using cobra.Execute with captured stdout/stderr.
Each test isolates filesystem state via `t.TempDir()` + `t.Setenv` for
`XDG_CONFIG_HOME`, `XDG_DATA_HOME`, and `HOME` (for paths consistency),
and `t.Chdir(...)` (Go 1.24+) or manual `os.Chdir` for the project dir.

```go
func runCmd(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := cli.NewRootCmd()
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return outBuf.String(), errBuf.String(), err
}
```

- `TestVersion` — `--version` writes a version string.
- `TestHelp` — `--help` writes "Usage:".
- `TestConfigPath_Global` — output contains `agentbox/config.toml`.
- `TestConfigPath_Project` — `--project` flag → output ends in `.agentbox.toml`.
- `TestConfigShow_JSON` — `config show --json` produces valid JSON
  (`json.Unmarshal` round-trips).
- `TestConfigShow_TOML` — default output is TOML containing `runtime`.
- `TestRunDryRun` — `run --dry-run` output contains `agentbox-`, the cwd,
  `--cap-drop ALL`, exits with nil error.
- `TestRunUnknownAgent` — `run nonexistent --dry-run` → `*exitcode.Err`
  with `Code == 2`.
- `TestStubReturnsUnimplemented` — `agentbox shell` returns exit code 1
  with the not-implemented message.
- `TestCompletionZsh` — `completion zsh` output contains `_agentbox`.
- `TestUnknownFlag` — `--bogus` returns a non-nil error (cobra surfaces it,
  main.go maps to exit 2).

### `cmd/agentbox/main_test.go`
- Skipped — main.go is exit-code wiring; covered indirectly by integration
  tests of the build through `make build && ./agentbox ...` in the
  verification checklist below.

---

## Verification Checklist

Run after the full implementation set is in place. These are the
ROADMAP.md Phase 1 test checkpoint commands plus a few sanity checks.

```sh
# Build clean.
go vet ./...
go test ./...
make build

# 1. ROADMAP checkpoint.
./agentbox --version                           # prints "0.0.1-dev (commit ..., built ...)"
./agentbox config path                          # prints global path
mkdir -p /tmp/abx-test
(cd /tmp/abx-test && \
  ../../$(pwd | sed 's|.*||')/agentbox config show --json | jq . >/dev/null) \
  || ./agentbox config show --json > /tmp/cfg.json && jq . /tmp/cfg.json >/dev/null

(cd /tmp/abx-test && /path/to/agentbox run --dry-run > /tmp/out.sh)
grep -q 'agentbox-' /tmp/out.sh                 # container name present
grep -q "$(realpath /tmp/abx-test)" /tmp/out.sh # same-path mount present

./agentbox doctor                               # exits 0 with podman installed

# 2. Sanity beyond the checkpoint.
./agentbox completion zsh | grep -q '_agentbox' # completion works
./agentbox shell; [ $? -eq 1 ]                  # stub exits 1
./agentbox --bogus; [ $? -eq 2 ]                # unknown flag exits 2
./agentbox config show | head -5                # TOML output works
```

The implementation is "done" for Phase 1 when all of the above succeed and
`go test ./...` is green.
