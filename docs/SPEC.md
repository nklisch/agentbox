# SPEC

Technical decisions for agentbox v0.1. Reference, not prose. If a decision lives here, it's
been made; if it's in "Open" at the bottom, it hasn't.

## Stack

| Layer            | Choice                                  | Notes                                      |
| ---------------- | --------------------------------------- | ------------------------------------------ |
| CLI binary       | Go (stable; latest minor)               | Single static binary. No CGO unless forced. |
| Container runtime | Podman (primary) / Docker (fallback)   | Detected at runtime. Configurable.         |
| Multiplexer      | Zellij (in-box only)                    | Bundled in `base` kit, not a host dep.     |
| Config format    | TOML                                    | `BurntSushi/toml` for parsing.             |
| In-box shell     | zsh + starship                          | bash/fish selectable in config.            |

No SDKs for the container runtime. The CLI shells out and parses output. `--dry-run` on any
mutating command prints the exact `podman` / `docker` / `zellij` commands without executing.

## Platforms

| Platform        | Status         | Runtime         | Notes                                  |
| --------------- | -------------- | --------------- | -------------------------------------- |
| Linux (any)     | Primary        | Rootless Podman | Docker also supported.                 |
| macOS (Apple Si) | Primary       | `podman machine` | Native VM. No Docker Desktop required. |
| macOS (Intel)   | Best-effort    | `podman machine` | Same as Apple Silicon.                 |
| Windows         | Not supported  | —               | May revisit. WSL is not a target.      |

## Configuration

### Files

- `~/.config/agentbox/config.toml` — global, optional. Created on first run with sane defaults.
- `<repo>/.agentbox.toml` — per-project, optional. Shallow-merged over global.
- CLI flags override both.

The effective config (post-merge) is dumped to the session state directory on every `run`,
named `effective-config.toml`, for debugging.

### Top-level keys

```toml
runtime        = "podman"          # podman | docker
default_agent  = "claude"
default_kits   = ["polyglot", "containers", "claude"]   # composable list, see KITS.md

[network]
mode = "safe"                      # off | safe | allowlist | open

[network.safe]
# Used when network.mode = "safe"
upstream         = "quad9"         # quad9 | cloudflare-security | nextdns | custom
upstream_servers = []              # ["1.1.1.2", "9.9.9.9"] when upstream = "custom"
nextdns_id       = ""              # required when upstream = "nextdns"
block_categories = []              # ["newly-registered", "uncategorized",
                                   #  "crypto-mining", "ads-trackers"]
                                   # only effective when upstream supports them (nextdns)
block_direct_ip  = true            # drop egress to IPs not resolved through CoreDNS
extra_block      = []              # extra domains to NXDOMAIN
extra_allow      = []              # extra domains to bypass blocking

[network.allowlist]
# Used when network.mode = "allowlist"
allow = ["registry.npmjs.org", "pypi.org", "github.com", "api.anthropic.com"]

[mounts]
gitconfig    = true                # mount ~/.gitconfig writable
ssh_readonly = true                # mount ~/.ssh ro (writable not supported)
extra        = []                  # ["~/.config/foo:/root/.config/foo:rw", ...]

[mounts.agent_configs]
# Per-agent config dir mounts. Writable by default.
claude   = "~/.claude"             # → /root/.claude inside box
codex    = "~/.codex"
opencode = "~/.opencode"

[secrets]
passthrough = ["ANTHROPIC_API_KEY", "OPENAI_API_KEY"]
# CLI never reads or logs values; passes by name only.

[resources]
cpus   = 4
memory = "8g"
pids   = 512

[containers]
# Enables nested rootless podman inside the box (for `docker run` / `compose` from
# inside). Adds /dev/fuse and re-grants SETUID/SETGID. Pairs with the `containers` kit.
enable        = false              # off by default; opt-in
extra_devices = ["/dev/fuse"]
extra_caps    = ["SETUID", "SETGID"]
seccomp       = "containers"       # bundled looser profile name; or path; or "unconfined"

[shell]
shell    = "zsh"                   # zsh | bash | fish
prompt   = "starship"              # starship | minimal
history  = true                    # persist to state dir
aliases  = { ll = "eza -la", g = "git" }

[agents.claude]
kits = ["polyglot", "claude"]
cmd  = ["claude", "--dangerously-skip-permissions"]

[agents.codex]
kits = ["polyglot", "codex"]
cmd  = ["codex", "--dangerously-bypass-approvals-and-sandbox"]

[agents.opencode]
kits = ["polyglot", "opencode"]
cmd  = ["opencode"]                # no root-level YOLO flag; see Open / deferred
```

## Mount semantics

Same-path bind mount is non-negotiable: `-v $PWD:$PWD -w $PWD`. Error messages, lockfiles,
absolute paths in tooling output all match the host.

| Mount                     | Mode | Source                        | Destination                  | Notes                                |
| ------------------------- | ---- | ----------------------------- | ---------------------------- | ------------------------------------ |
| Project                   | rw   | `$PWD`                        | `$PWD`                       | Always. Same path inside and out.    |
| `~/.gitconfig`            | rw   | `~/.gitconfig`                | `/root/.gitconfig`           | If `mounts.gitconfig = true`.        |
| `~/.ssh`                  | ro   | `~/.ssh`                      | `/root/.ssh`                 | RO only. Writable is rejected.       |
| Agent config              | rw   | e.g. `~/.claude`              | e.g. `/root/.claude`         | Per `[mounts.agent_configs]`.        |
| Shell history             | rw   | session state `history` file  | `/root/.local/share/agentbox-history` | Persists across box recreation. |
| Layout                    | ro   | session state `layout.kdl`    | `/etc/agentbox/layout.kdl`   | Generated per session.               |
| Effective config          | ro   | session state `effective-config.toml` | `/etc/agentbox/config.toml` | For in-box `box info`.       |

`mounts.extra` accepts arbitrary entries in `<src>:<dst>:<mode>` form. No interpolation
beyond `~`.

## Identity and lifecycle

### Project key

A box belongs to a project. The project key is:

```
project_id = sha1(realpath($PWD))[:12]
```

12 hex chars, stable across `cd` from symlinks (`realpath` resolves), unique enough that
collision is irrelevant for personal use.

- Container name: `agentbox-<project_id>`
- Zellij session name (inside the box): `agentbox`
- State directory: `~/.local/share/agentbox/sessions/<project_id>/`

### `agentbox run` lifecycle

1. Resolve config (global + project + flags).
2. Compute `project_id` from `realpath($PWD)`.
3. Look for a container named `agentbox-<project_id>`.
   - **Exists, running**: skip to step 7.
   - **Exists, stopped**: `podman start` it. Skip to step 7.
   - **Doesn't exist**: continue.
4. If `default_kits` image is not built or its tag is stale, build it (see KITS.md).
5. `podman create` with the runtime spec (see "Runtime spec" below). Container starts on
   `sleep infinity`.
6. Write `effective-config.toml` and `layout.kdl` to the session state directory.
7. `podman exec -it <container> zellij --layout /etc/agentbox/layout.kdl attach -c agentbox`.
   Layout auto-launches the agent in the main pane.
8. User detaches with zellij binding (`Ctrl+p d` by default). Container keeps running.

`agentbox run --fresh` removes the existing box for this project (if any) before step 3.

### Disposal

- `agentbox rm` (no args) — refuses, asks for an id or `--all`.
- `agentbox rm <project_id>` — stops + removes container, removes session state directory,
  preserves the project files (they live on the host).
- `agentbox rm --all` — same, for every container with `agentbox=1` label.

## Runtime spec

The `podman create` invocation:

```
podman create \
  --name "agentbox-${PROJECT_ID}" \
  --label "agentbox=1" \
  --label "agentbox.project=$(basename $PWD)" \
  --label "agentbox.project_id=${PROJECT_ID}" \
  --label "agentbox.cwd=$(realpath $PWD)" \
  --label "agentbox.agent=${AGENT}" \
  --label "agentbox.kits=${KIT_LIST}" \
  --label "agentbox.kit_image=${KIT_IMAGE_TAG}" \
  --label "agentbox.created=$(date -Iseconds)" \
  -v "$(realpath $PWD):$(realpath $PWD)" \
  -w "$(realpath $PWD)" \
  -v "${HOME}/.gitconfig:/root/.gitconfig:rw" \
  -v "${HOME}/.ssh:/root/.ssh:ro" \
  -v "${HOME}/.claude:/root/.claude:rw" \
  -v "${STATE_DIR}/history:/root/.local/share/agentbox-history:rw" \
  -v "${STATE_DIR}/layout.kdl:/etc/agentbox/layout.kdl:ro" \
  -v "${STATE_DIR}/effective-config.toml:/etc/agentbox/config.toml:ro" \
  --cpus "${CPUS}" \
  --memory "${MEMORY}" \
  --pids-limit "${PIDS}" \
  --cap-drop ALL \
  --security-opt no-new-privileges \
  --network "${NET_NAME}" \
  -e ANTHROPIC_API_KEY \
  -e OPENAI_API_KEY \
  "${KIT_IMAGE_TAG}" \
  sleep infinity
```

Then `podman start <container>`. Agent launches via the zellij layout on `exec`, not at
container start.

### Why these choices

- **Same-path mount.** Paths in error messages and lockfiles match the host. Non-negotiable.
- **`sleep infinity` + `exec`.** Lets multiple panes (agent, shell) all run inside the same
  container without competing for the foreground process.
- **Drop caps + no-new-privileges.** Cheap defense in depth. The agent doesn't need them.
- **Env vars by name.** Host env is the source of truth. CLI never reads or logs values.
- **Labels are the source of truth.** No state file required for `agentbox ls`. Loss of the
  state directory doesn't lose track of containers.

### Conditional flags (nested containers)

When `containers.enable = true`, the CLI appends the following to `podman create`:

```
--device /dev/fuse                                       # fuse-overlayfs storage
--cap-add SETUID --cap-add SETGID                        # newuidmap/newgidmap
--security-opt seccomp=/etc/agentbox/seccomp/containers.json  # looser profile
--security-opt unmask=/proc/sys/net/ipv4                 # rootless network setup
```

These re-grant a small slice of what `--cap-drop ALL` removed and loosen seccomp enough
for nested rootless podman to function. They do **not** require `--privileged` and they do
**not** mount the host docker socket. Inner containers' egress still flows through the
agentbox network policy (CoreDNS sidecar + iptables egress filter).

The seccomp profile `containers.json` is shipped with the binary and is a copy of
podman's default profile minus a handful of restrictions on `clone3`, `mount`,
`unshare`, and friends. The exact profile is at `<binary>/seccomp/containers.json`;
inspect with `agentbox config show --effective`.

**Security trade-off:** a box with this enabled has a wider attack surface than a box
without. The agent can spawn arbitrary containers (subject to the network policy), mount
fuse filesystems, and use a fuller set of namespacing syscalls. Acceptable for the use
case (development inside the box) but worth knowing.

## Label schema

| Label                    | Purpose                                       |
| ------------------------ | --------------------------------------------- |
| `agentbox`               | `1` — discovery filter.                       |
| `agentbox.project`       | Basename of `$PWD` for human display.         |
| `agentbox.project_id`    | 12-char hash of `realpath($PWD)`.             |
| `agentbox.cwd`           | Absolute path of the project on the host.     |
| `agentbox.agent`         | Agent name (e.g. `claude`).                   |
| `agentbox.kits`          | Comma-separated kit list (e.g. `polyglot,claude`). |
| `agentbox.kit_image`     | Image tag the box was created from.           |
| `agentbox.created`       | ISO-8601 creation timestamp.                  |

## Network modes

| Mode        | Implementation                                                       |
| ----------- | -------------------------------------------------------------------- |
| `off`       | `--network=none`. No interfaces beyond loopback.                     |
| `safe`      | Custom Podman network with a CoreDNS sidecar forwarding to a threat-intel-filtered upstream (Quad9 by default). Egress to IPs not resolved via CoreDNS is dropped (`block_direct_ip = true` by default). See ARCHITECTURE.md. |
| `allowlist` | Custom Podman network with a CoreDNS sidecar resolving only `network.allowlist.allow`; default route blocked via iptables. See ARCHITECTURE.md. |
| `open`      | Default bridge network. No filtering.                                |

**Sudo requirement:** `safe` mode with `block_direct_ip = true` (the default) and `allowlist`
mode both require passwordless `sudo` for `iptables` and `ipset` on Linux. Configure via:

```
ALL ALL=(root) NOPASSWD: /usr/sbin/iptables, /usr/sbin/ipset
```

or equivalent via `visudo`. `agentbox doctor` verifies this with the `sudo-iptables` check.

`safe` is the **default and recommended mode** — broad internet access with a threat-intel
DNS upstream and direct-IP egress blocked. Catches known-malicious destinations without the
operational overhead of explicit allowlisting. `allowlist` is for unattended runs where you
want to lock the box to a known set of hosts. `off` is for "definitely no network"
(audit/review). `open` is a debugging escape hatch — use it when diagnosing the network
policy itself.

### `safe` — what it catches and what it doesn't

**Catches:** packages or scripts phoning home to known-malicious domains, fetches from
threat-fed URLs, exfiltration to attacker-registered domains in the upstream's feeds,
direct-IP traffic that bypasses DNS (when `block_direct_ip = true`).

**Does not catch:** exfiltration via legitimate services (Gist, Pastebin, transfer.sh,
ngrok, etc.), traffic over the agent's own provider HTTPS (Anthropic / OpenAI), DoH/DoT
used to bypass the local resolver. `safe` raises the floor; it doesn't prevent a
sufficiently determined agent from misbehaving via legitimate channels.

### Upstream choices for `safe`

| Upstream             | Address(es)                | Notes                                          |
| -------------------- | -------------------------- | ---------------------------------------------- |
| `quad9` (default)    | 9.9.9.9, 149.112.112.112   | Free, ~25+ threat feeds, GDPR-friendly.        |
| `cloudflare-security`| 1.1.1.2, 1.0.0.2           | Free, Cloudflare's malware-blocking resolver.  |
| `nextdns`            | derived from `nextdns_id`  | Free tier (300k queries/mo), supports `block_categories`. |
| `custom`             | from `upstream_servers`    | Bring your own.                                |

## State directory layout

```
~/.config/agentbox/
  config.toml                  # global config
  kits/                        # user-authored kits

~/.local/share/agentbox/
  sessions/
    <project_id>/
      layout.kdl               # rendered zellij layout (mounted ro into box)
      effective-config.toml    # resolved config used at create time
      history                  # persistent shell history (mounted rw into box)
      saved/                   # files snapshotted out by `box save`
      events.jsonl             # future: docker events + DNS log
  cache/
    kits/
      <kit_image_tag>.json     # build metadata (kit list, content hashes, build timestamp)
```

`agentbox rm <project_id>` deletes `sessions/<project_id>/`. The cache survives.

## In-box DX bundle (shipped in `base` kit)

### Shell

- `zsh` (default), `bash`, `fish`
- `starship` prompt — preconfigured to show: project, git status, container indicator
- `fzf` with shell key bindings (Ctrl-R / Ctrl-T / Alt-C)
- `zoxide` (`z` for frecency-based cd)
- Persistent history mounted from host state dir

### Modern CLI replacements (aliased over defaults)

| Default | Replacement | Why |
| ------- | ----------- | --- |
| `cat` | `bat` | syntax highlighting |
| `ls` | `eza` | git-aware, tree mode |
| `find` | `fd` | sane defaults |
| `grep` | `ripgrep` (`rg`) | gitignore-aware |
| `du` | `dust` | visual, sorted |
| `df` | `duf` | readable |
| `top` | `btm` (bottom) | charts, sortable |
| `ps` | `procs` | colors, tree |
| `cd` | `zoxide` (`z`) | frecency |

### Inspection / debugging

`jq`, `yq`, `htmlq`, `httpie` (`http` / `xh`), `dog`, `gron`, `hyperfine`, `tokei` /
`scc`, `tealdeer` (`tldr`), `entr`, `watchexec`, `delta` (default git pager).

### Forge clients

`gh` (GitHub CLI) and `glab` (GitLab CLI). Both authenticate via the agent's normal
flow (env tokens or `gh auth login` / `glab auth login`). Useful for `gh pr create`,
`gh issue list`, `glab mr create`, etc., without leaving the box.

### Multiplexer

`zellij` — bundled and used by `agentbox run`.

### `box` helpers

Tiny scripts at `/usr/local/bin/box-*`, plus a `box` dispatcher:

| Command         | Behavior                                                                 |
| --------------- | ------------------------------------------------------------------------ |
| `box info`      | Prints project_id, agent, kits, mounts, network mode, resource limits.   |
| `box scratch`   | `cd` into a tmpfs scratch dir for ephemeral work.                        |
| `box net`       | Show current allowlist + recent DNS queries (allowlist mode only).        |
| `box save <f>`  | Copy a file from the box to host state dir so it survives `rm`.          |
| `box help`      | List all `box` commands.                                                 |

### Not bundled

No editor (no neovim, micro, lazygit, glow). The shell tab + the project mounted at the
host path is enough — edit on the host with whatever editor you already use.

## Env passthrough rules

- `secrets.passthrough` is a list of env var **names**.
- The CLI passes each name to `-e <NAME>` (without value), so podman/docker reads from the
  parent process env at create time.
- The CLI never logs, prints, or writes secret values. The effective-config dump shows
  names only.
- Unset vars are silently dropped (no error).

## Installed binaries

`make install` copies two binaries to `~/.local/bin/` (or the configured install prefix):

- **`agentbox`** — the primary CLI.
- **`agentbox-netfilter`** — a small daemon that tails `podman logs --follow` of the CoreDNS
  sidecar and populates an ipset (`abx-<12hex>-a`) used by an iptables FORWARD rule. Launched
  by the CLI when `block_direct_ip = true` or `mode = allowlist`; must be reachable via
  `sudo -n agentbox-netfilter` (add to sudoers alongside `iptables`/`ipset`).

## Constraints

- **No daemon.** All operations are one-shot CLI invocations. State lives in labels + state dir.
  (`agentbox-netfilter` is a short-lived child process of the network-setup path, not a
  persistent service.)
- **No SDK for the container runtime.** Shell out to `podman` / `docker`. `--dry-run` prints
  the exact commands.
- **Single static binary (main).** Distribute as a tarball or via Homebrew (later). Both
  `agentbox` and `agentbox-netfilter` are CGO_ENABLED=0 static binaries.
- **Shell out for zellij too.** No zellij library bindings.
- **Per-project isolation.** No mechanism for sharing state between project boxes. If two
  projects need to talk, they're not isolated and shouldn't be in agentbox.

## CLI surface (brief)

Full reference in CLI.md. Verbs:

```
agentbox run [agent] [--fresh]    create/attach to per-project box, launch agent
agentbox shell                    bare shell session in per-project box (no agent)
agentbox attach <project_id>      reattach to a running session
agentbox exec <project_id> <cmd>  one-off command in a live box
agentbox ls [--all] [--json]      list boxes (running by default)
agentbox rm <project_id> | --all  stop + remove boxes + session state
agentbox build [kit_list]         build/rebuild a composed kit image
agentbox doctor                   verify runtime, mounts, kits
agentbox config [edit|show]       open or print effective config
```

## Open / deferred

- macOS container-only volumes for big build dirs (`node_modules`, `target`). Deferred until
  perf is actually a problem.
- Kit image registry distribution (so first run is `pull` not `build`). v0.3+.
- Worktree / auto-commit / sandbox-branch mode. Deferred indefinitely.
- Snapshot / resume (`podman commit` + restart from snapshot). Deferred.
- ttyd in the kit for browser-based attach. Out of scope.
- **Port forwarding from the box to the host.** Inner `docker run -p 8080:8080` only binds
  inside the box. v0.1 workaround: `agentbox exec . curl localhost:8080` from the host.
  v0.3+: a `[runtime.ports]` config block that publishes a list of ports on `agentbox run`.
- **Shared rootless image cache across boxes.** Today every box with the `containers` kit
  has its own image cache; each project pulls its own `postgres:16`. A shared opt-in volume
  could amortize that. Out of scope for v0.1.
- **`opencode` YOLO flag.** `opencode` has no root-level YOLO flag — `--dangerously-skip-permissions`
  only applies to the `opencode run` subcommand, not the TUI. Users who want auto-confirm for
  opencode must configure `cmd` in `[agents.opencode]` explicitly.
