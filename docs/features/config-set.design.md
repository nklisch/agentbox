# Design: Config write commands (set/unset + toggles + kits add/remove)

## Overview

Implements the feature brief in `docs/features/config-set.md`. Adds six cobra
subcommands under `agentbox config` for non-interactive config mutation:
`set`, `unset`, `network <mode>`, `containers on|off`, `kits add <name>`,
`kits remove <name>`.

The implementation is a hybrid:

- **Document carrier:** `map[string]any` decoded from the target TOML file.
  This is what gets mutated and re-encoded. The map representation preserves
  "what's in the file" vs. "what the merged config resolves to" — we do not
  want to write zero-value defaults to disk.
- **Type discovery:** reflection over the typed `config.Config` struct via
  its TOML tags. Used to resolve the target kind for a dotted path so the
  value parser produces the right TOML scalar type (bool vs int vs string,
  string vs []string).
- **Validation gate:** before persisting, we re-merge the proposed bytes
  with the *other* layer (whichever file we're not editing) plus
  `DefaultConfig`, then call `cfg.Validate()`. Invalid mutations are
  rejected with exit 2; the file on disk is never touched.
- **Atomic write:** decode → mutate → encode → temp file in the same
  directory → `os.Rename`. A crash mid-write leaves the original file
  untouched.

The CLI layer is thin wrappers around domain helpers. The domain layer
stays cobra-free per the project's Phase 1 invariants.

## Implementation Units

### Unit 1: dotted-path operations

**File:** `internal/config/path.go`

```go
package config

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// segmentRE matches the allowed shape of a single dotted-path segment.
// Schema fields and free-form map keys (agent names, env-var names) all
// fit. Dots inside keys are not supported; users with weird keys use
// `agentbox config edit`.
var segmentRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

// ParsePath splits a dotted path into segments and validates each one.
// Returns ErrEmptyPath if path is "" and ErrBadSegment naming the offender.
func ParsePath(path string) ([]string, error)

// Get looks up the value at path in doc. Returns (value, true) if found,
// (nil, false) otherwise. Walking through anything other than
// map[string]any along the way returns (nil, false).
func Get(doc map[string]any, path []string) (any, bool)

// Set writes value at path in doc. Intermediate sections are auto-created
// as map[string]any. If the path traverses a non-map value (e.g. trying
// to set foo.bar when foo is a string), Set returns ErrPathConflict.
// The leaf is overwritten unconditionally.
func Set(doc map[string]any, path []string, value any) error

// Unset removes the leaf at path. Returns true if anything was removed,
// false if the path didn't resolve. Empty intermediate maps left behind
// by the removal are pruned: if foo.bar.baz is removed and foo.bar is now
// an empty map, foo.bar is removed too. Recurses up to the root.
func Unset(doc map[string]any, path []string) bool

// AppendUnique appends value to a []any (or []string) at path, creating
// the slice if absent. No-op if value is already present (deep-equal). The
// existing slice's element type is preserved when possible; a brand-new
// slice is created as []any. Returns ErrPathConflict if the leaf exists
// but isn't a slice.
func AppendUnique(doc map[string]any, path []string, value any) error

// RemoveValue removes the first occurrence of value from a []any at path
// (deep-equal). Returns (true, nil) if anything was removed, (false, nil)
// if the slice didn't contain value or didn't exist. Returns
// ErrPathConflict if the leaf exists but isn't a slice.
func RemoveValue(doc map[string]any, path []string, value any) (bool, error)

// Sentinel errors. Wrap with fmt.Errorf("...: %w", err) for context.
var (
	ErrEmptyPath    = errors.New("empty path")
	ErrBadSegment   = errors.New("invalid path segment")
	ErrPathConflict = errors.New("path traverses non-map value")
)
```

**Implementation notes:**

- `Set` walks segments[:-1], `mkdir -p`-style: at each step, if the slot is
  absent OR is `nil`, create a new `map[string]any`; if it's already a map,
  descend; otherwise return `ErrPathConflict` wrapping the offending segment.
- `Unset`'s prune-empty-ancestors behavior is implemented iteratively in a
  helper: walk segments from root to leaf, recording each parent map; after
  removing the leaf, walk back recording maps that became empty and remove
  them from their respective parents.
- `AppendUnique` and `RemoveValue` use `reflect.DeepEqual` for comparison so
  scalar types and string slices both work. The slice type detection: if the
  existing slot is `[]any`, append/remove preserves that. If the slot is a
  TOML-decoded `[]string` (BurntSushi/toml may decode homogeneous string
  arrays this way), convert to `[]any` for uniform handling.
- For `AppendUnique`/`RemoveValue` when the path doesn't exist yet, treat
  the missing slot as an empty `[]any{}` and proceed (creates the array
  on first add).

**Acceptance criteria:**

- [ ] `ParsePath("a.b.c")` → `[]string{"a", "b", "c"}`, no error.
- [ ] `ParsePath("")` → `ErrEmptyPath`.
- [ ] `ParsePath("a..b")` → `ErrBadSegment`.
- [ ] `ParsePath("agents.claude-1.cmd")` succeeds (`-` allowed in segments).
- [ ] `Get` on a missing path returns `(nil, false)`, never panics.
- [ ] `Set(doc, ["a","b","c"], "v")` on an empty doc creates
      `{"a": {"b": {"c": "v"}}}`.
- [ ] `Set` overwriting an existing leaf returns nil and replaces the value.
- [ ] `Set(doc, ["a","b"], "v")` when `a` is a string returns `ErrPathConflict`.
- [ ] `Unset(doc, ["a","b","c"])` on `{"a": {"b": {"c": "v"}}}` leaves
      `{}` (full ancestor pruning).
- [ ] `Unset(doc, ["a","b","c"])` on `{"a": {"b": {"c": "v", "d": "x"}}}`
      leaves `{"a": {"b": {"d": "x"}}}` (no pruning when sibling exists).
- [ ] `Unset` on a missing path returns false, doc unchanged.
- [ ] `AppendUnique(doc, ["k"], "x")` on empty doc creates `{"k": ["x"]}`.
- [ ] `AppendUnique` is idempotent: adding "x" twice produces `["x"]`.
- [ ] `RemoveValue(doc, ["k"], "x")` on `{"k": ["a","x","b","x"]}` produces
      `{"k": ["a","b","x"]}` (first-occurrence only).

---

### Unit 2: type discovery via reflection

**File:** `internal/config/typeinfo.go`

```go
package config

import "reflect"

// Kind is the abstract value kind the path resolves to. Used by ParseValue
// to decide how to interpret a raw string from the CLI.
type Kind int

const (
	KindUnknown    Kind = iota // path not found in schema; best-effort parse
	KindString
	KindBool
	KindInt
	KindFloat
	KindStringList            // []string (e.g. default_kits, network.allowlist.allow)
)

// LookupKind walks the Config struct's TOML tags following path and returns
// the kind of the leaf field. Returns KindUnknown if path doesn't match the
// schema (e.g. typo, or a free-form map key for which we have no schema
// information beyond the value type).
//
// Map fields are handled specially:
//   - map[string]string (mounts.agent_configs.*) — any segment after the
//     map-typed field is the key; the kind is KindString.
//   - map[string]Agent (agents.*) — the segment after `agents` is the
//     agent name; resolution continues on the Agent struct's tags.
func LookupKind(path []string) Kind
```

**Implementation notes:**

- Use `reflect.TypeOf(DefaultConfig())` as the starting type. Walk segments
  recursively. At each step:
  - If current type is a struct: find the field whose `toml` tag matches
    the segment (split tag on `,` to drop options like `omitempty`).
    Descend into the field's type.
  - If current type is `map[string]X`: the current segment is the map key.
    The next descent operates on `X` (the element type).
  - If current type is a slice or scalar: stop. If we've consumed all
    segments, the kind is the leaf type's kind. Otherwise the path goes
    too deep (unknown).
- Map kind detection at the leaf:
  - `bool` → KindBool
  - `int*`, `uint*` → KindInt
  - `float32`, `float64` → KindFloat
  - `string` → KindString
  - `[]string` → KindStringList
  - anything else → KindUnknown
- Tag parsing: segments must match `tag := strings.Split(field.Tag.Get("toml"), ",")[0]`.
- Test path examples that must resolve correctly:
  - `runtime` → KindString
  - `default_agent` → KindString
  - `default_kits` → KindStringList
  - `network.mode` → KindString
  - `network.safe.block_direct_ip` → KindBool
  - `network.safe.upstream_servers` → KindStringList
  - `mounts.gitconfig` → KindBool
  - `mounts.agent_configs.claude` → KindString (free-form key under map)
  - `containers.enable` → KindBool
  - `resources.cpus` → KindInt
  - `resources.memory` → KindString
  - `agents.claude.kits` → KindStringList
  - `agents.claude.cmd` → KindStringList
  - `agents.future-agent.cmd` → KindStringList (free-form agent name)

**Acceptance criteria:**

- [ ] All paths in the list above return the documented kind.
- [ ] `LookupKind([]string{"nonexistent"})` returns `KindUnknown`.
- [ ] `LookupKind([]string{"runtime", "extra"})` returns `KindUnknown` (path
      goes deeper than the schema).

---

### Unit 3: value parser

**File:** `internal/config/parse.go`

```go
package config

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseValue interprets the raw CLI string as a TOML value of the given
// kind. Returns the typed value (any) suitable for inserting into a
// map[string]any document.
//
// Strict-by-kind:
//   - KindBool: only "true" / "false" (lowercase). Other forms error;
//     friendlier inputs go through the convenience commands.
//   - KindInt: strconv.ParseInt(raw, 10, 64).
//   - KindFloat: strconv.ParseFloat(raw, 64).
//   - KindString: raw as-is.
//   - KindStringList: split raw on `,`, trim whitespace from each element.
//     Empty raw produces []string{}.
//
// Best-effort for KindUnknown:
//   1. raw == "true" || raw == "false" → bool
//   2. strconv.ParseInt → int64
//   3. strconv.ParseFloat → float64
//   4. strings.Contains(raw, ",") → []string (split + trim)
//   5. fallback → string
//
// Returns (typed value, nil) on success, (nil, error) on parse failure.
func ParseValue(raw string, kind Kind) (any, error)
```

**Implementation notes:**

- StringList split: `strings.Split(raw, ",")` then `strings.TrimSpace` on
  each element. `raw == ""` produces `[]string{}` (not `[""]`).
- Quote handling is shell's job. `agentbox config set foo "a, b"` arrives
  as `a, b`; we split on commas regardless. Users wanting a literal comma
  in a string field can shell-quote and use a non-list field — for
  list fields, comma is the separator and that's accepted as the contract.
- BurntSushi/toml's encoder accepts `[]string`, `[]any`, scalars. We emit
  `[]string` from KindStringList for cleanliness (homogeneous output).

**Acceptance criteria:**

- [ ] `ParseValue("true", KindBool)` → `(true, nil)`.
- [ ] `ParseValue("True", KindBool)` → error (strict).
- [ ] `ParseValue("yes", KindBool)` → error (strict).
- [ ] `ParseValue("42", KindInt)` → `(int64(42), nil)`.
- [ ] `ParseValue("4.2", KindFloat)` → `(float64(4.2), nil)`.
- [ ] `ParseValue("polyglot,claude", KindStringList)` →
      `([]string{"polyglot","claude"}, nil)`.
- [ ] `ParseValue("polyglot, claude ", KindStringList)` →
      `([]string{"polyglot","claude"}, nil)` (trim whitespace).
- [ ] `ParseValue("", KindStringList)` → `([]string{}, nil)`.
- [ ] `ParseValue("hello", KindString)` → `("hello", nil)`.
- [ ] `ParseValue("true", KindUnknown)` → `(true, nil)` (best-effort detects bool).
- [ ] `ParseValue("a,b", KindUnknown)` →
      `([]string{"a","b"}, nil)` (best-effort detects list).
- [ ] `ParseValue("hello", KindUnknown)` → `("hello", nil)`.

---

### Unit 4: file edit driver + atomic write + validation hook

**File:** `internal/config/save.go`

```go
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// EditFile reads path, decodes it as a generic map[string]any document
// (empty map if the file doesn't exist), invokes mutate, and re-encodes
// the result. Returns the encoded TOML body. Does NOT write to disk —
// callers handle persistence (with validation) separately.
//
// path == "" returns ErrNoPath. Read errors and decode errors are wrapped.
// mutate errors are returned verbatim.
func EditFile(path string, mutate func(doc map[string]any) error) ([]byte, error)

// WriteAtomic writes body to path via a tempfile in the same directory,
// then renames over the target. The parent directory is created with mode
// 0700 if missing. The file is written with mode 0600. Returns nil on
// success, wrapped error on any step.
func WriteAtomic(path string, body []byte) error

// LoadProposed is like Load but substitutes proposedBody for whichever
// file path matches proposedPath. Used by the edit pipeline to validate
// a proposed mutation against the *merged* config (defaults + the other
// layer + the proposed change) before persisting.
//
// proposedPath must equal p.Global or p.Project; otherwise ErrPropPathMismatch.
func LoadProposed(p Paths, proposedPath string, proposedBody []byte) (Config, error)

var (
	ErrNoPath            = errors.New("config: no file path")
	ErrPropPathMismatch  = errors.New("config: proposed path does not match Paths.Global or Paths.Project")
)
```

**Implementation notes:**

- `EditFile` decode: `toml.NewDecoder(bytes.NewReader(body)).Decode(&doc)`.
  When the file doesn't exist, start with `doc := map[string]any{}` and skip
  the decode.
- `EditFile` encode: use a `bytes.Buffer` and `toml.NewEncoder(&buf).Encode(doc)`.
  BurntSushi/toml's encoder for `map[string]any` writes keys in alphabetical
  order, which is fine — comments and blank-line formatting are not preserved
  (documented in the brief).
- `WriteAtomic` flow:
  1. `os.MkdirAll(filepath.Dir(path), 0o700)` (no-op if exists).
  2. `tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")`.
  3. Write body, `tmp.Sync()`, `tmp.Close()`.
  4. `os.Chmod(tmp.Name(), 0o600)` (CreateTemp uses 0600 by default on Linux,
     but make it explicit for portability).
  5. `os.Rename(tmp.Name(), path)`.
  6. On any error after CreateTemp, `os.Remove(tmp.Name())` to avoid
     littering. Capture the temp path in a deferred cleanup that's a no-op
     once Rename succeeded.
- `LoadProposed` mirrors the existing `Load` but, instead of reading the
  proposed file from disk, decodes the supplied bytes:

  ```go
  func LoadProposed(p Paths, proposedPath string, body []byte) (Config, error) {
      if proposedPath != p.Global && proposedPath != p.Project {
          return Config{}, ErrPropPathMismatch
      }
      cfg := DefaultConfig()
      if err := overlayLayer(p.Global, body, proposedPath == p.Global, &cfg); err != nil {
          return cfg, fmt.Errorf("global config: %w", err)
      }
      if p.Project != "" {
          if err := overlayLayer(p.Project, body, proposedPath == p.Project, &cfg); err != nil {
              return cfg, fmt.Errorf("project config: %w", err)
          }
      }
      return cfg, nil
  }

  func overlayLayer(path string, body []byte, useBody bool, cfg *Config) error {
      if useBody {
          if len(body) == 0 {
              return nil
          }
          _, err := toml.NewDecoder(bytes.NewReader(body)).Decode(cfg)
          return err
      }
      return decodeIfExists(path, cfg) // existing helper
  }
  ```

**Acceptance criteria:**

- [ ] `EditFile("/nonexistent/path.toml", noop)` returns the encoding of an
      empty map (i.e. an empty/whitespace-only byte slice — TOML's
      encoding of `{}`).
- [ ] `EditFile(path, mutateThatErrs)` returns the mutation error
      unchanged, no encode happens.
- [ ] `WriteAtomic(path, body)` produces a file at `path` with `body` as
      contents and mode 0600. Original file (if present) replaced atomically.
- [ ] `WriteAtomic` to a path whose parent doesn't exist creates the parent
      with mode 0700.
- [ ] `WriteAtomic` failing during write does not leave a temp file behind
      (best-effort cleanup on error).
- [ ] `LoadProposed(p, p.Global, validBody)` returns the merged config with
      `validBody` substituted for the global file (project file still read
      from disk if present).
- [ ] `LoadProposed(p, p.Global, badTOMLBody)` returns a wrapped TOML parse
      error.
- [ ] `LoadProposed(p, "/some/random/path", body)` returns
      `ErrPropPathMismatch`.

---

### Unit 5: CLI subcommands

**File:** `internal/cli/config.go` (extend existing)

```go
package cli

import (
	// ...existing imports plus:
	"github.com/nklisch/agentbox/internal/config"
)

// Existing newConfigCmd() updated to register the new subcommands:
func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect or edit configuration",
	}
	cmd.AddCommand(
		newConfigShowCmd(),
		newConfigEditCmd(),
		newConfigPathCmd(),
		newConfigSetCmd(),
		newConfigUnsetCmd(),
		newConfigNetworkCmd(),
		newConfigContainersCmd(),
		newConfigKitsCmd(),
	)
	return cmd
}

// newConfigSetCmd: `agentbox config set <key> <value>`.
func newConfigSetCmd() *cobra.Command

// newConfigUnsetCmd: `agentbox config unset <key>`.
func newConfigUnsetCmd() *cobra.Command

// newConfigNetworkCmd: `agentbox config network <off|safe|allowlist|open>`.
func newConfigNetworkCmd() *cobra.Command

// newConfigContainersCmd: `agentbox config containers <on|off>`.
func newConfigContainersCmd() *cobra.Command

// newConfigKitsCmd builds the `kits` group with `add` and `remove` subcommands.
func newConfigKitsCmd() *cobra.Command
```

**Shared helper, also in `internal/cli/config.go`:**

```go
// editTarget resolves which file (global/project) to edit and runs the
// pipeline: read → mutate → encode → validate (LoadProposed) → write.
//
// In --dry-run mode, prints the proposed body to stdout (with a `# would
// write to <path>` header) and returns nil without writing.
//
// Returns *exitcode.Err on validation failure (InvalidArgs) or IO failure
// (Generic).
func editTarget(
	cmd *cobra.Command,
	useProject bool,
	mutate func(doc map[string]any) error,
) error
```

**Per-command structure:**

Each new subcommand:
1. Parses positional args (validates count up-front via `cobra.ExactArgs(N)`).
2. Builds a `mutate` closure that takes `map[string]any` and applies the
   specific change (calling Unit 1 helpers).
3. Calls `editTarget(cmd, projectFlag, mutate)`.

Specific mutate closures:

```go
// set <key> <value>
func setMutate(key, rawValue string) func(map[string]any) error {
    return func(doc map[string]any) error {
        path, err := config.ParsePath(key)
        if err != nil { return err }
        kind := config.LookupKind(path)
        v, err := config.ParseValue(rawValue, kind)
        if err != nil { return fmt.Errorf("parse value: %w", err) }
        return config.Set(doc, path, v)
    }
}

// unset <key>
func unsetMutate(key string) func(map[string]any) error {
    return func(doc map[string]any) error {
        path, err := config.ParsePath(key)
        if err != nil { return err }
        config.Unset(doc, path) // ignore "did anything change" — idempotent
        return nil
    }
}

// network <mode>
func networkMutate(mode string) func(map[string]any) error {
    return func(doc map[string]any) error {
        // Validation of the mode value happens during LoadProposed
        // (cfg.Validate() rejects unknown modes). No client-side check
        // here so we have a single source of truth for the enum.
        return config.Set(doc, []string{"network", "mode"}, mode)
    }
}

// containers on|off (also accepts true/false/enable/disable, case-insensitive)
func containersMutate(toggle string) func(map[string]any) error {
    enable, err := parseBoolFriendly(toggle) // private helper below
    return func(doc map[string]any) error {
        if err != nil { return err }
        return config.Set(doc, []string{"containers", "enable"}, enable)
    }
}

// kits add <name>
func kitsAddMutate(name string) func(map[string]any) error {
    return func(doc map[string]any) error {
        // We want to append to the *merged* default_kits, not just
        // what's in the file. Read the current value via LoadProposed
        // semantics — but for simplicity here we read the file's
        // default_kits if present, falling back to the merged value
        // by reading DefaultConfig.
        path := []string{"default_kits"}
        if _, ok := doc["default_kits"]; !ok {
            // Seed with the current merged default (DefaultConfig) so
            // `kits add` from a fresh file doesn't drop the defaults.
            seed := append([]string(nil), config.DefaultConfig().DefaultKits...)
            asAny := make([]any, len(seed))
            for i, s := range seed { asAny[i] = s }
            doc["default_kits"] = asAny
        }
        return config.AppendUnique(doc, path, name)
    }
}

// kits remove <name>
func kitsRemoveMutate(name string) func(map[string]any) error {
    return func(doc map[string]any) error {
        path := []string{"default_kits"}
        if _, ok := doc["default_kits"]; !ok {
            seed := append([]string(nil), config.DefaultConfig().DefaultKits...)
            asAny := make([]any, len(seed))
            for i, s := range seed { asAny[i] = s }
            doc["default_kits"] = asAny
        }
        _, err := config.RemoveValue(doc, path, name)
        return err // idempotent on "not found", reported on type conflict
    }
}

// parseBoolFriendly: accepts true/false/on/off/enable/disable/yes/no
// (case-insensitive).
func parseBoolFriendly(s string) (bool, error)
```

**`editTarget` implementation:**

```go
func editTarget(cmd *cobra.Command, useProject bool, mutate func(map[string]any) error) error {
    paths, err := config.DefaultPaths()
    if err != nil { return exitcode.Wrap(exitcode.Generic, err) }
    if global.ConfigPath != "" { paths.Global = global.ConfigPath }

    target := paths.Global
    if useProject {
        if paths.Project == "" {
            return exitcode.New(exitcode.InvalidArgs, "no project config path (not in a directory)")
        }
        target = paths.Project
    }

    body, err := config.EditFile(target, mutate)
    if err != nil { return exitcode.Wrap(exitcode.InvalidArgs, err) }

    // Validation gate: re-merge with the proposed body and call Validate.
    cfg, err := config.LoadProposed(paths, target, body)
    if err != nil { return exitcode.Wrap(exitcode.InvalidArgs, err) }
    if err := cfg.Validate(); err != nil {
        return exitcode.Wrap(exitcode.InvalidArgs, fmt.Errorf("config invalid after mutation: %w", err))
    }

    if global.DryRun {
        fmt.Fprintf(cmd.OutOrStdout(), "# would write to %s:\n%s", target, string(body))
        return nil
    }

    if err := config.WriteAtomic(target, body); err != nil {
        return exitcode.Wrap(exitcode.Generic, err)
    }
    return nil
}
```

**Per-command flag wiring (representative — `set`):**

```go
func newConfigSetCmd() *cobra.Command {
    var (
        globalFlag  bool
        projectFlag bool
    )
    cmd := &cobra.Command{
        Use:   "set <key> <value>",
        Short: "Set a config key by dotted path",
        Args:  cobra.ExactArgs(2),
        RunE: func(cmd *cobra.Command, args []string) error {
            return editTarget(cmd, projectFlag, setMutate(args[0], args[1]))
        },
    }
    cmd.Flags().BoolVar(&globalFlag, "global", false, "edit global config (default)")
    cmd.Flags().BoolVar(&projectFlag, "project", false, "edit project config (.agentbox.toml)")
    cmd.MarkFlagsMutuallyExclusive("global", "project")
    return cmd
}
```

`unset`, `network`, `containers`, and the `kits add`/`kits remove`
subcommands follow the same shape with their respective `mutate` closure
and arg counts.

`newConfigKitsCmd` is a parent with no `RunE`:

```go
func newConfigKitsCmd() *cobra.Command {
    cmd := &cobra.Command{
        Use:   "kits",
        Short: "Modify default_kits incrementally",
    }
    cmd.AddCommand(newConfigKitsAddCmd(), newConfigKitsRemoveCmd())
    return cmd
}
```

**Acceptance criteria:**

- [ ] `agentbox config set network.mode open` writes the change and exits 0;
      `agentbox config show --json | jq -r .network.mode` prints "open".
- [ ] `agentbox config set network.mode bogus` exits 2, leaves the file
      untouched, prints the validator's error to stderr.
- [ ] `agentbox config set containers.enable true` exits 0; merged config
      reflects the change.
- [ ] `agentbox config set default_kits node,claude` overwrites the array;
      `agentbox config show --json | jq -r '.default_kits | join(",")'`
      prints "node,claude".
- [ ] `agentbox config set runtime docker --dry-run` prints the proposed
      file body to stdout, exits 0, leaves the file unchanged.
- [ ] `agentbox config unset network.safe.block_direct_ip` removes the key
      from the file (verifiable by reading the file directly); merged
      config returns to the schema default `true`.
- [ ] `agentbox config unset` of a missing key exits 0 with no change.
- [ ] `agentbox config network safe` is equivalent to
      `agentbox config set network.mode safe`.
- [ ] `agentbox config containers on` is equivalent to
      `agentbox config set containers.enable true`.
- [ ] `agentbox config containers OFF` (case-insensitive) sets enable=false.
- [ ] `agentbox config containers maybe` exits 2 with a clear error.
- [ ] `agentbox config kits add foo` from a fresh-no-file state writes
      `default_kits = ["polyglot", "containers", "claude", "foo"]`
      (preserving DefaultConfig's seed + the addition).
- [ ] `agentbox config kits add foo; agentbox config kits add foo` produces
      a single `foo` in the array.
- [ ] `agentbox config kits remove polyglot` removes the first occurrence.
- [ ] `agentbox config kits remove not-there` exits 0 with no change.
- [ ] `agentbox config set --project foo bar` writes to `.agentbox.toml`
      and creates the file if missing.
- [ ] `agentbox config set --global --project foo bar` exits with cobra's
      mutually-exclusive error (exit 1 from cobra).
- [ ] All write commands honor `--dry-run`: print the would-write body
      with a `# would write to <path>:` header, no disk change.

---

## Implementation Order

Build in this order — each unit's tests pass before the next starts:

1. **Unit 1** (`internal/config/path.go`) — pure functions, no deps.
2. **Unit 2** (`internal/config/typeinfo.go`) — pure reflection, no deps.
3. **Unit 3** (`internal/config/parse.go`) — depends on Unit 2's `Kind` enum.
4. **Unit 4** (`internal/config/save.go`) — depends on existing `Load` and
   `decodeIfExists` (extends `load.go` with `LoadProposed`); does not
   depend on Units 1–3 (they're consumed by the CLI layer).
5. **Unit 5** (`internal/cli/config.go`) — wires Units 1–4 into cobra.

Tests live alongside each unit in `_test.go` files, written together with
the production code (TDD optional, but recommended for the path walker).

## Testing

### Unit 1 — `internal/config/path_test.go`

Table-driven tests for each exported function. Coverage targets:

- `ParsePath`: empty, single segment, multi-segment, invalid characters,
  segments starting with digits/underscores, segments with hyphens.
- `Get`/`Set`/`Unset`: cases enumerated in Unit 1's acceptance criteria.
  Use literal `map[string]any` fixtures.
- `AppendUnique`/`RemoveValue`: empty doc, existing slice, value present,
  value absent, type-conflict (slot exists but isn't a slice), `[]any` vs.
  `[]string` slot handling.

### Unit 2 — `internal/config/typeinfo_test.go`

Table-driven tests for `LookupKind`. Each row: `{path []string, want Kind}`.
Covers all paths listed in Unit 2's notes plus a few negative cases
(unknown top-level field, going too deep into a scalar).

### Unit 3 — `internal/config/parse_test.go`

Table-driven tests for `ParseValue`. Each row: `{raw string, kind Kind,
want any, wantErr bool}`. Covers the acceptance-criteria list verbatim.

### Unit 4 — `internal/config/save_test.go`

- `EditFile`: write a fixture file with known TOML, decode-mutate-encode,
  assert the result decodes back to the expected shape. Test the
  nonexistent-file case (returns encoded empty map). Test mutation error
  propagation.
- `WriteAtomic`: write to a tempdir, assert file exists, mode is 0600,
  contents match, parent dir was created if missing. Test "rename over
  existing file" (atomic replace). Test cleanup on simulated write
  failure (use a subdir we can't write to and assert no temp files left
  behind in the parent).
- `LoadProposed`: build a `Paths{Global, Project}` against a tempdir,
  write the project file with known content, call `LoadProposed` with a
  proposed global body, assert the merged config has both layers applied.
  Test the path-mismatch error.

### Unit 5 — extend `internal/cli/cli_test.go`

Add a `configWriteHarness(t)` helper that:

1. Creates a tempdir, sets `XDG_CONFIG_HOME` and `HOME` to it.
2. `chdir`s into a per-test subdirectory.
3. Returns the resolved global/project paths for assertions.

Each command gets at least:

- A success case (write happens, merged config reflects the change).
- A validation-failure case (where applicable — `set network.mode bogus`).
- A `--dry-run` case (output contains the would-write header, no file
  change).
- A `--project` case (writes to `.agentbox.toml`, not the global file).

Don't test `cobra.MarkFlagsMutuallyExclusive` itself — that's cobra's job.

## Verification Checklist

Run after implementation:

```sh
go vet ./...
go test ./...
go build ./...

# Smoke against the live binary in a fresh tempdir:
TMP=$(mktemp -d)
XDG_CONFIG_HOME=$TMP HOME=$TMP ./agentbox config set network.mode open
XDG_CONFIG_HOME=$TMP HOME=$TMP ./agentbox config show --json | jq -r .network.mode
# expect: open

XDG_CONFIG_HOME=$TMP HOME=$TMP ./agentbox config set network.mode bogus
echo "exit: $?"
# expect: 2, error to stderr, file unchanged

XDG_CONFIG_HOME=$TMP HOME=$TMP ./agentbox config kits add foo
XDG_CONFIG_HOME=$TMP HOME=$TMP ./agentbox config kits add foo
XDG_CONFIG_HOME=$TMP HOME=$TMP ./agentbox config show --json | jq -r '.default_kits | join(",")'
# expect: polyglot,containers,claude,foo  (one foo, not two)

XDG_CONFIG_HOME=$TMP HOME=$TMP ./agentbox config containers on --dry-run
# expect: # would write to <path>: ... containers.enable = true ... no file change

cat $TMP/agentbox/config.toml
# expect: previous state preserved (dry-run didn't write)
```

If all of the above behave as documented, the feature is shipping-ready.

## Open Questions Resolved

The brief left five open questions; this design resolves them as:

1. **map[string]any vs struct reflection** → hybrid (map carries the
   document, reflection on Config struct provides type info).
2. **Boolean parsing convention** → `set` is strict (true/false only);
   `containers on|off` accepts the friendlier forms.
3. **`unset` of array element** → not in scope; users use `kits remove`
   for the kit array, or `set <key> '<comma-list>'` to overwrite for
   any other array.
4. **`--dry-run` output format** → full new file body with a
   `# would write to <path>:` header.
5. **Kit ordering flags** → not in v1; revisit if asked.
