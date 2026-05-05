# Feature: Config write commands (set/unset + toggles + kits add/remove)

## Summary

Today, every persistent config change goes through `agentbox config edit` (which
opens `$EDITOR` on the TOML file). That's fine for big edits, awkward for one-key
flips. This feature adds a small surface for scripted/non-interactive config
mutation: a generic `set`/`unset` for arbitrary dotted-path keys, two convenience
toggles for the most common pain points (`network` mode, `containers` enable),
and `kits add`/`kits remove` for incrementally editing `default_kits`.

It eliminates "open vi to flip one bool" without growing into a full DSL.

## Requirements

### `agentbox config set <key> <value>`

- Sets a TOML key by dotted path: `set network.mode open`,
  `set network.safe.block_direct_ip false`, `set agents.claude.cmd 'claude --foo'`.
- **Acceptance:** after the command exits 0, `agentbox config show` reflects the
  new value, and the file at `agentbox config path` contains the new value.
- Supports scalar TOML types: string, int, float, bool. Booleans accept
  `true|false|on|off|yes|no` (case-insensitive); ints/floats parse via Go's
  `strconv`.
- **List values** are accepted as comma-separated strings:
  `set default_kits polyglot,claude` overwrites the array. Whitespace around
  commas is trimmed. Empty list is `set default_kits ''`.
- Auto-creates intermediate sections that don't exist
  (`set agents.future.cmd ...` creates `[agents.future]` if absent).
- Validates the resulting config (`config.Validate()`) before writing. If
  validation fails, the file is not touched and exit code is `2`.
- Writes atomically: temp file in the same directory + rename. Original file
  intact on failure.
- **Acceptance:** `set network.mode bogus` exits 2 with the validator's error
  message and leaves the file unchanged.
- Targets the global config by default. `--project` writes `.agentbox.toml`.
- `--global` and `--project` are mutually exclusive.

### `agentbox config unset <key>`

- Removes a dotted-path key from the file.
- **Acceptance:** after `unset network.safe.block_direct_ip`,
  `agentbox config show` reports the schema default for that field (because
  the merge layers fall back to `DefaultConfig`), and the file no longer
  contains the key.
- `unset` on a key that doesn't exist exits 0 with no change (idempotent).
- Empty containers (sections that have all keys removed) are pruned from the
  file. Pure-cosmetic; the merged config is unaffected either way.
- Same `--global`/`--project` semantics as `set`.

### `agentbox config network <mode>`

- Sugar for `agentbox config set network.mode <mode>`.
- **Acceptance:** `agentbox config network open` is exactly equivalent to
  `set network.mode open`, including validation and file write semantics.
- `<mode>` must be one of `off|safe|allowlist|open`. Anything else exits 2
  with the same validator error as `set` would have produced.
- Same `--global`/`--project` semantics as `set`.

### `agentbox config containers on|off`

- Sugar for `set containers.enable true|false`.
- **Acceptance:** after `agentbox config containers on`, the merged config
  shows `containers.enable = true`, and `agentbox doctor`'s
  `containers-config` check goes from WARN to OK (when the kit is in
  `default_kits`).
- Accepts `on|off|true|false|enable|disable` (case-insensitive).
- Same `--global`/`--project` semantics.

### `agentbox config kits add <name>`

- Appends `<name>` to `default_kits` if not already present (idempotent).
- **Acceptance:** `kits add foo` followed by `kits add foo` leaves
  `default_kits` containing exactly one `foo`. `agentbox config show`
  reflects the addition.
- Does **not** validate that `<name>` is a real built-in or user kit — that's
  `agentbox build`'s job to fail at resolution time. (Same forgiveness as
  hand-editing the file.)
- Order: appended at the end. The user can reorder via `set` or hand-edit.
- Same `--global`/`--project` semantics.

### `agentbox config kits remove <name>`

- Removes the first occurrence of `<name>` from `default_kits`.
- **Acceptance:** `kits remove polyglot` from a list of
  `[polyglot, claude, polyglot]` produces `[claude, polyglot]`.
- Removing a kit that isn't in the list exits 0 with no change (idempotent).
- Same `--global`/`--project` semantics.

### Cross-cutting

- All write commands respect `--config <path>` (already a global flag) when
  the user wants to operate on a non-default file.
- All write commands fail with exit 2 on invalid input (unparseable value,
  unknown network mode, etc.) with a clear stderr message.
- All write commands honor `--dry-run`: print the proposed file contents
  diff (or the new full file body) and don't write. Exits 0.
- `--json` is not supported on write commands — they don't produce
  structured output, only side effects.
- Comments and blank-line formatting in the existing TOML file are NOT
  preserved (BurntSushi/toml's encoder doesn't round-trip them). The file
  is rewritten with the encoder's deterministic format. This is documented;
  users who care about hand-formatted comments should use `config edit`.

## Scope

**In scope:**

- New cobra subcommands under `agentbox config`: `set`, `unset`, `network`,
  `containers`, and a `kits` group with `add`/`remove`.
- Dotted-path parser/walker for the `config.Config` struct (or a generic
  `map[string]any` representation).
- Atomic write helper (`writeAtomic` style: tempfile in same dir + rename).
- Validation hook — call existing `cfg.Validate()` before persisting.
- Tests covering: scalar set, array overwrite via comma-list, unset of
  scalar/array/section, kit add idempotence, kit remove first-occurrence,
  network/containers sugar invocations, validation rejection, atomic-write
  failure mode, `--project` vs `--global`, `--dry-run` output shape.
- Doc updates: `docs/CLI.md` (new subcommands), `README.md` (Configuration
  section gets a one-line mention), examples for the most common toggles.

**Out of scope:**

- Comment-preserving TOML round-trip. (BurntSushi/toml strips comments;
  switching libraries is a much bigger change with its own trade-offs.)
- Convenience commands beyond the chosen set:
  - `allowlist add/remove` (less common; v0.2 if pain emerges)
  - `secret add/remove` (less common; same)
  - `agent <name>` (rare; `set default_agent` is fine)
  - `mount add/remove` (rare; users edit file)
  - `reset` (uncommon; `rm <path>` is a fine workaround)
- Type hints on `set` (`set --type=int foo.bar 42`). The auto-detection
  from string parsing is enough for the supported scalar types.
- Editing nested map keys with arbitrary characters
  (`set agents."weird name".cmd ...`). Dotted-path parser assumes
  `[a-zA-Z0-9_-]+` segments. Users with weird agent names use
  `config edit`.
- A `config diff` or `config check` command. `agentbox config show` already
  covers reading; `config edit` + a separate `git diff` covers diffing.

## Technical Context

### Existing code this touches

- `internal/cli/config.go` — the cobra command tree for `agentbox config`.
  Currently has `show`, `edit`, `path`. New subcommands attach here. Same
  pattern as the existing `--global`/`--project` handling.
- `internal/config/config.go` — `Config` struct with TOML tags,
  `DefaultConfig()`, `Validate()`. Validation hook reuses `Validate()`
  unchanged.
- `internal/config/load.go` — reads TOML files via `BurntSushi/toml`.
  Need a sibling `Save()` (or similar) that encodes a `Config` back to a
  file, atomically. Today there's no write path.
- `internal/exitcode` — exit code constants. Reuse `InvalidArgs` (2) for
  validation failures and unparseable values.

### Patterns to follow

- The existing `config edit`/`path` commands handle `--global`/`--project`
  via mutually-exclusive bool flags. New commands match that shape exactly
  for consistency.
- `--dry-run` is universal on mutating commands per CLAUDE.md. New write
  commands honor it.
- Domain layer stays cobra-free (per CLAUDE.md and Phase 1 invariants):
  the dotted-path walker, save logic, and value-parsing live in
  `internal/config`. Cobra handlers in `internal/cli/config.go` are thin
  wrappers.

### Dependencies

- `BurntSushi/toml` — already a dependency; encoder is sufficient for the
  output side.
- No new third-party dependencies.

### Constraints

- **No daemon, all one-shot.** Each command reads → mutates → writes →
  exits. No locking required for personal-use single-machine workflow
  (matches the existing `config edit` story — no lock there either).
- **`--dry-run` must not write.** Including the temp file. Output goes to
  stdout.
- **Atomic write.** A failure mid-write must not corrupt the existing
  file. Rename-from-tempfile is the standard approach.
- **Don't grow the binary unnecessarily.** Estimate: ~300 LOC of code +
  ~200 LOC of tests, no new deps.

## Open Questions

1. **Dotted-path representation.** Two options the design pass should pick
   between:
   - **(a)** Decode TOML into `map[string]any` ahead of editing, mutate the
     map, encode back. Simple, type-loose, doesn't lean on the typed
     `Config` struct.
   - **(b)** Reflect on the `Config` struct via the existing TOML tags and
     mutate field-by-field. Type-strict, surfaces invalid keys earlier,
     but requires careful reflection work for nested maps
     (`agents.<name>.cmd`, `mounts.agent_configs.<name>`).

   I'd lean (a) for simplicity, with the caveat that we still call
   `cfg.Validate()` after re-decoding into `Config` for the validation
   gate. Design can confirm.

2. **Boolean parsing convention.** Is `set foo true|false` enough, or do we
   accept `on|off|yes|no` for `set` too (matching the `containers on|off`
   sugar)? I'd say: `set` accepts only `true|false` (matches TOML's own
   syntax), `containers` sugar accepts the friendlier forms. Design can
   confirm.

3. **`unset` of an array element vs. the whole array.** Today this brief
   says `unset` removes the *whole* key. If a user wants to drop one entry
   from an array, they use `kits remove` or rewrite via `set`. Acceptable?

4. **`--dry-run` output format.** Two reasonable shapes:
   - **Diff-like:** show `- old line` / `+ new line` style.
   - **Full new file:** print the full new TOML to stdout.

   Full-file output is simpler to implement and matches what `config show`
   already does. Diff is friendlier for big files. I'd ship full-file in
   v1; we can add `--diff` later if anyone asks.

5. **`kits add <name>` ordering.** Currently appended at the end. Should
   we add an `--after <other>` or `--first` flag for the rare case where
   ordering matters? Topological sort happens at build time anyway, so
   ordering in `default_kits` mostly doesn't matter — but for kit
   conflicts where the user wants to express precedence, it could.
   Probably skip in v1; revisit if asked.
