# TRAIL

The agent-activity trail is a JSONL event stream written by Claude Code hooks and rendered
live in the `auditor` layout's trail pane. It gives you a glanceable, structured view of
every tool call your agent makes — what it ran, which file it touched, what URL it fetched
— with timing and outcome.

v1 is Claude-only. Codex and opencode have different observability surfaces and are deferred
to v2.

---

## Event schema

Each line in `trail.jsonl` is a JSON object. Two kinds of objects appear:

### Tool events (PreToolUse, PostToolUse, PostToolUseFailure)

These are the raw Claude Code hook payloads, with one field added by `agentbox-hook-record`:

| Field | Type | Description |
| ----- | ---- | ----------- |
| `trail_ts` | string (ISO-8601 UTC) | Timestamp added by `agentbox-hook-record` when the event is recorded. |
| `hook_event_name` | string | `"PreToolUse"`, `"PostToolUse"`, or `"PostToolUseFailure"`. |
| `tool_name` | string | Claude's tool name: `"Bash"`, `"Edit"`, `"Read"`, `"WebFetch"`, etc. |
| `tool_input` | object | Tool-specific input fields (see below). |
| `tool_response` | object | PostToolUse/PostToolUseFailure only. Tool output. For `Bash`: `{"stdout":…,"stderr":…,"interrupted":bool,"isImage":bool,"noOutputExpected":bool}`. |
| `tool_use_id` | string | Opaque ID correlating Pre/PostToolUse pairs. |
| `duration_ms` | number | PostToolUse only. Execution time in milliseconds. |
| `session_id` | string | Claude Code session UUID. |
| `transcript_path` | string | Path to the session's JSONL transcript inside the box. |
| `cwd` | string | Working directory when the hook fired. |
| `permission_mode` | string | Claude's permission mode (e.g. `"bypassPermissions"`). |

#### `tool_input` subfields (common tools)

| Tool | Key fields |
| ---- | ---------- |
| `Bash` | `command` (string), `description` (string) |
| `Edit` / `Write` | `file_path` (string), `old_string`, `new_string` |
| `Read` | `file_path` (string) |
| `WebFetch` | `url` (string) |
| `Glob` / `Grep` | `pattern` (string) |

`box-trail` uses the priority chain `command → file_path → url → last_assistant_message` to
pick the most meaningful detail string for the rendered line.

### Session events (Stop, StopFailure)

| Field | Type | Description |
| ----- | ---- | ----------- |
| `trail_ts` | string | Timestamp added by `agentbox-hook-record`. |
| `hook_event_name` | string | `"Stop"` or `"StopFailure"`. |
| `last_assistant_message` | string | The final message Claude produced before stopping. |
| `stop_hook_active` | bool | Whether a StopHook is currently executing. |
| `session_id` | string | Claude Code session UUID. |
| `transcript_path` | string | Path to the session's JSONL transcript inside the box. |
| `cwd` | string | Working directory. |
| `permission_mode` | string | Claude's permission mode. |

**Schema drift note:** Claude Code 2.1.128 Stop events do NOT include a `reason` field.
They include `last_assistant_message` and `stop_hook_active` instead. The design document
originally anticipated `reason`; `box-trail` uses `.last_assistant_message` as the detail
field for Stop/StopFailure events.

### Error stubs (malformed hook payloads)

When `agentbox-hook-record` receives non-JSON on stdin, it appends a stub:

```json
{"trail_ts":"…","hook_event_name":"?","error":"malformed_input","raw":"<raw input>"}
```

`box-trail` skips lines where `.hook_event_name` is missing or `"?"` — they don't render
as event rows but are preserved in the file for debugging.

---

## How agentbox wires it

Trail wiring is active only when **both** conditions are true:

1. The resolved layout is `auditor`.
2. The resolved agent is `claude`.

When both conditions hold, lifecycle does three things before creating the container:

### 1. Touch `<state>/trail.jsonl`

`EnsureTrailFile` touches `<stateDir>/trail.jsonl` if the file doesn't exist. This ensures
the bind-mount source is a regular file — if it were absent, podman would create it as a
directory, corrupting the mount.

### 2. Write the shadow settings file

`WriteShadowSettings` reads the user's host `~/.claude/settings.json` (if any) and merges in
agentbox's trail hooks via `MergeTrailHooks`. The merged JSON is written to
`<stateDir>/claude-settings.json` with mode `0o600`.

The merge appends a new hook group for each of the five hook events:

```json
{
  "matcher": "*",
  "hooks": [{"type": "command", "command": "/usr/local/bin/agentbox-hook-record"}]
}
```

User-defined hooks in the same event array are preserved — agentbox only appends, never
replaces. If the user's `settings.json` is malformed JSON, `MergeTrailHooks` returns
`ErrInvalidUserSettings` and lifecycle aborts with a clear error message.

### 3. Bind-mount trail file + shadow settings

The runspec adds two conditional mounts when `TrailHostPath` is non-empty:

| Mount | Direction | In-container path |
| ----- | --------- | ----------------- |
| `<state>/trail.jsonl` | read-write | `/etc/agentbox/trail.jsonl` |
| `<state>/claude-settings.json` | read-only | `/root/.claude/settings.json` |

The shadow settings mount comes AFTER the `~/.claude:/root/.claude` directory mount in the
podman arguments, so the file-level mount layers on top of the directory mount correctly.

agentbox also sets `BOX_TRAIL_FILE=/etc/agentbox/trail.jsonl` as a container env var so
`box-trail` knows where to tail without arguments.

**The host's `~/.claude/settings.json` is never written.** The shadow is a separate file in
the session state dir.

### Hook execution model

Claude Code runs all matching hooks for an event in parallel. Our hook is:

```sh
/usr/local/bin/agentbox-hook-record
```

It reads stdin (the hook payload), adds `trail_ts`, and appends to `$BOX_TRAIL_FILE` using
`>>` (O_APPEND). Writes shorter than 4KB (PIPE_BUF on Linux) are atomic; larger payloads
(e.g. a tool response with a large file diff) may interleave in theory. v1 accepts this;
large interleaved lines are skipped by `box-trail` (the `jq -r '.hook_event_name // empty'`
check fails on invalid JSON and the line is silently skipped).

`agentbox-hook-record` exits 0 always. Exiting 2 would cause Claude Code to treat the hook
as a blocking error, interrupting the agent.

---

## Adding a new agent adapter (v2 work)

To add trail support for codex or opencode:

1. **Research the hook mechanism.** Both codex and opencode have different observability
   surfaces. Codex exposes an `--on-event` callback; opencode's interface is not yet
   confirmed. Verify the current hook payload schema before writing code (per CLAUDE.md's
   "stale training data" rule).

2. **Update `trailEnabled` in `internal/lifecycle/trail.go`** to include the new agent name
   in the gate condition (or refactor to a list of supported agents).

3. **Update `MergeTrailHooks`** to write the correct hook config format for the new agent's
   settings file. For agents that don't use Claude's `settings.json`, this may mean writing
   to a different file path (and mounting it differently in runspec).

4. **Update `box-trail`** if the new agent's hook payload uses different field names (e.g.
   `tool_name` may be called `function` in codex). The jq pipeline is the single rendering
   point; adapt the field chain there.

5. **Update `docs/TRAIL.md`** (this file) with the new agent's event schema and wiring notes.

The JSONL format itself (including `trail_ts`, `hook_event_name`) is a stable v1 contract.
New agent adapters must produce compatible JSONL so `box-trail`'s renderer works unchanged.

---

## Trail file location

```
~/.local/share/agentbox/sessions/<project_id>/trail.jsonl
```

This is on the **host** filesystem and bind-mounted read-write into the box. It is part of
the session state and is removed by `agentbox rm .` (unless `--keep-state` is passed).

Trail files are NOT persisted across `--fresh` runs (which remove the session state dir
before creating a new box).

## Concurrent write safety

`agentbox-hook-record` uses `>>` (O_APPEND). On Linux ext4/btrfs, appends under 4KB are
atomic. Tool-call payloads with large file diffs may exceed 4KB; those lines could interleave
in theory. `box-trail` silently skips lines that fail `jq -r '.hook_event_name // empty'`,
so a corrupted line is dropped from the display but the remainder of the file is unaffected.

v1 accepts this limitation. If atomicity becomes a problem in practice, v2 can introduce a
queue (e.g. a FIFO or a locking wrapper) in `agentbox-hook-record`.
