# Pattern: Dry-Run Output Format

Print `# key = value` comment headers, then one shell command per line, all to stdout.

## Rationale

CLAUDE.md: "`--dry-run` is universal on mutating commands and must print the exact shell invocation." All three dry-run implementations follow the same format: bash-safe (every line is either a `#` comment or a runnable command), headers first, commands second. Output goes to stdout so users can pipe it to `bash -e` or `tee plan.sh`.

## Examples

### Example 1: run --dry-run (via runspec.ToShell)
**File**: `internal/cli/run.go:177`
```go
fmt.Fprintf(cmd.OutOrStdout(), "# project_id = %s\n", id)
fmt.Fprintf(cmd.OutOrStdout(), "# kits = %s\n", strings.Join(kitList, ","))
if !sameSlice(kitList, resolved.Names()) {
    fmt.Fprintf(cmd.OutOrStdout(), "# resolved = %s\n", strings.Join(resolved.Names(), ","))
}
fmt.Fprintf(cmd.OutOrStdout(), "# network = %s\n", c.Network.Mode)
// ...
fmt.Fprint(cmd.OutOrStdout(), rs.ToShell(c.Runtime))  // PodmanCreateArgs → shell
```

### Example 2: build --dry-run
**File**: `internal/cli/build_dryrun.go:46`
```go
fmt.Fprintf(cmd.OutOrStdout(), "# kits = %s\n", strings.Join(requested, ","))
if !sameSlice(requested, res.Names()) {
    fmt.Fprintf(cmd.OutOrStdout(), "# resolved = %s\n", strings.Join(res.Names(), ","))
}
fmt.Fprintf(cmd.OutOrStdout(), "# tag = %s\n", res.Tag)
// ... then either:
fmt.Fprintf(cmd.OutOrStdout(), "podman pull %s\n", remoteRef)
fmt.Fprintf(cmd.OutOrStdout(), "podman tag %s %s\n", remoteRef, res.Tag)
// or:
fmt.Fprintf(cmd.OutOrStdout(), "podman build -t %s ...\n", res.Tag)
```

### Example 3: rm --dry-run (via renderRmShell)
**File**: `internal/lifecycle/rm_dryrun.go:27`
```go
// One comment header per box, then the stop/rm/network/state commands.
fmt.Fprintf(w, "# project_id = %s\n", projID)
if box.Status == container.StatusRunning {
    fmt.Fprintf(w, "podman stop %s\n", name)
}
fmt.Fprintf(w, "podman rm %s\n", name)
fmt.Fprintf(w, "podman network rm %s\n", spec.NetworkName)
if !keepState {
    fmt.Fprintf(w, "rm -rf %s\n", stateDir)
}
```

### Example 4: PodmanCreateArgs.ToShell (the low-level renderer)
**File**: `internal/runspec/runspec.go:337`
```go
// Each flag on its own indented line ending with ` \`.
func (p PodmanCreateArgs) ToShell(runtime string) string {
    var b strings.Builder
    fmt.Fprintf(&b, "%s create \\\n", runtime)
    fmt.Fprintf(&b, "  --name %q \\\n", p.Name)
    // ...
    return b.String()
}
```

## When to Use
- Any mutating command that gets `--dry-run` support per CLAUDE.md
- Output goes to **stdout** (primary output channel, not stderr chatter)
- Comments use `# ` prefix; commands are verbatim shell

## When NOT to Use
- Don't use this format for `--json` output — dry-run is human/shell-script oriented
- Don't print to stderr — the whole point is pipe-ability (`agentbox rm --all --dry-run | bash`)

## Common Violations
- Mixing `--dry-run` with introspection flags (`--print`, `--list`) — these are mutually exclusive; reject with `exitcode.InvalidArgs` at the top of RunE
- Printing the runtime as a constant `"podman"` — use `cfg.Runtime` or the runtime string from `PodmanCreateArgs` so `--runtime docker` is respected
