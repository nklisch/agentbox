# ARCHITECTURE

How the pieces fit. Read SPEC.md first for the *what*; this doc is the *how*.

## Component overview

```
┌──────────────────────────────────────────────────────────────────────────┐
│                                HOST                                      │
│                                                                          │
│   ┌──────────────────┐    ┌───────────────────────────────────────────┐  │
│   │  agentbox CLI    │    │  agentbox-netfilter (safe/allowlist only) │  │
│   │  (Go binary)     │    │  tails podman logs --follow of sidecar;   │  │
│   │  → shell out to  │    │  populates ipset abx-<id>-a;              │  │
│   │  podman/zellij   │    │  run via sudo -n agentbox-netfilter        │  │
│   └────────┬─────────┘    └───────────────────────────────────────────┘  │
│            │                                                             │
│            │ shells out                                                  │
│            ▼                                                             │
│   ┌──────────────────┐                                                   │
│   │  podman / docker │  manages container + network + volumes             │
│   └────────┬─────────┘                                                   │
│            │                                                             │
│   ┌────────┴──────────────────────────────────────────────────────┐      │
│   │                 custom podman network                          │      │
│   │                                                                │      │
│   │  ┌──────────────────────┐    ┌──────────────────────────────┐  │      │
│   │  │  agentbox-<id>       │    │  agentbox-coredns-<id>       │  │      │
│   │  │  role=box            │    │  role=coredns                │  │      │
│   │  │                      │    │  coredns:1.14.3              │  │      │
│   │  │  ┌────────────────┐  │    │                              │  │      │
│   │  │  │  zellij        │  │    │  forwards to threat-feed DNS │  │      │
│   │  │  │  ├ agent pane  │  │    │  or resolves only allowed    │  │      │
│   │  │  │  ├ git pane    │  │    │  hosts; logs to stdout       │  │      │
│   │  │  │  └ stats pane  │  │    │                              │  │      │
│   │  │  ├ shell tab      │  │    │  iptables FORWARD + ipset    │  │      │
│   │  │  └ box helpers    │  │    │  block unresolved-IP egress  │  │      │
│   │  └────────────────────┘  │    └──────────────────────────────┘  │      │
│   │      │                                                          │      │
│   │      │ bind mounts                                              │      │
│   │      ▼                                                          │      │
│   │  ┌────────────────────────────────────────────────┐             │      │
│   │  │  $PWD (project)        same path inside        │             │      │
│   │  │  ~/.gitconfig          rw                      │             │      │
│   │  │  ~/.ssh                ro                      │             │      │
│   │  │  ~/.claude (etc.)      rw                      │             │      │
│   │  │  STATE_DIR/history     rw (persistent)         │             │      │
│   │  │  STATE_DIR/layout.kdl  ro                      │             │      │
│   │  └────────────────────────────────────────────────┘             │      │
│   └──────────────────────────────────────────────────────────────────┘      │
│                                                                             │
│   ┌──────────────────────────────────────────────────────┐                  │
│   │  ~/.config/agentbox/        (config, custom kits)    │                  │
│   │  ~/.local/share/agentbox/   (sessions, cache)        │                  │
│   └──────────────────────────────────────────────────────┘                  │
└──────────────────────────────────────────────────────────────────────────────┘
```

In `safe` and `allowlist` modes three actors collaborate per box: the **box container**
(role=box), the **CoreDNS sidecar** (role=coredns, image `docker.io/coredns/coredns:1.14.3`),
and the host-side **agentbox-netfilter** process. The sidecar and netfilter are absent in
`off` and `open` modes. `agentbox ls` filters to role=box so sidecars are hidden.

Three things to internalize:

1. **The CLI is short-lived.** Every `agentbox` command runs, mutates state via podman or
   the filesystem, and exits. There is no background process — except `agentbox-netfilter`,
   which runs as a host-side child process for the duration of a box using `safe` or
   `allowlist` network mode.
2. **The container is long-lived.** Once created (per-project), it runs `sleep infinity` and
   stays up across detaches. All interaction happens through `podman exec`.
3. **Zellij lives inside the box.** `agentbox run` is essentially "podman exec + zellij
   attach". The host doesn't need zellij installed.

## Layout subsystem

Every `agentbox run` resolves a named layout, renders a KDL file into the session state dir,
and mounts it read-only into the box. Zellij loads it on attach.

**Resolution order:** `--layout` flag → `[zellij].layout` in project config → `[zellij].layout`
in global config → `"focus"` (built-in default).

**Built-in layouts** are embedded in the binary under `internal/zellij/`:

| Layout | Purpose |
| ------ | ------- |
| `focus` | Agent (70%) + git ticker + btm stats + shell tab. The original layout. |
| `reviewer` | Agent (50%) + delta diff dashboard + watchexec test runner + shell tab. |
| `auditor` | Agent (60%) + live Claude Code tool-call trail (40%) + shell tab. |

**Custom layouts** live at `~/.config/agentbox/layouts/<name>.kdl`. agentbox performs Go
`text/template` substitution before writing the rendered file, injecting:

```
{{.ProjectAbs}}    absolute project path (for cwd = in panes)
{{.Shell}}         configured shell name
{{.AgentCommand}}  first element of agent cmd
{{.AgentArgs}}     remaining args, KDL-quoted
{{.AgentCmdFull}}  ready-to-paste KDL command + args block
{{.TrailFile}}     in-container trail path (empty unless auditor + claude)
```

**Plumbing:** `RunOpts.Layout` (from flag or config) → `LayoutSpec` (resolved name + source
path if custom) → `writeLayoutFor` (template substitution for custom; built-in expansion for
built-ins) → `<state>/layout.kdl` (written to session dir) → mounted ro at
`/etc/agentbox/layout.kdl` → `zellij --layout /etc/agentbox/layout.kdl attach -c agentbox`.

Unknown layout names fail at run time with exit code 2.

See docs/LAYOUTS.md for the full layout reference and custom layout guide.

## Agent-activity trail

The trail is a JSONL event stream that captures every Claude Code tool call in real time.
It is active only when **both** `layout = auditor` and `agent = claude` are true.

### 1. Hook setup (shadow settings merge)

Before container creation, lifecycle calls `WriteShadowSettings` (`internal/lifecycle/trail.go`):

1. Reads the user's host `~/.claude/settings.json` (if present).
2. Calls `MergeTrailHooks` to append agentbox's hook group to each of the five hook events
   (`PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `Stop`, `StopFailure`). User hooks are
   preserved; agentbox only appends.
3. Writes the merged JSON to `<state>/claude-settings.json` (mode 0600).

The merged file is bind-mounted **read-only** at `/root/.claude/settings.json` inside the
box, layered on top of the `~/.claude:/root/.claude` directory mount. The host's actual
`~/.claude/settings.json` is bit-identical before and after the run.

### 2. Trail file lifecycle

```
host:      <state>/trail.jsonl     ←  touched empty at box-create time
             (bind-mounted rw)
in-box:    /etc/agentbox/trail.jsonl
env:       BOX_TRAIL_FILE=/etc/agentbox/trail.jsonl
```

`agentbox-hook-record` (the Claude hook command) reads stdin, adds `trail_ts`, and
O_APPEND-writes a JSONL line to `$BOX_TRAIL_FILE`. Writes under 4KB are atomic on Linux.

### 3. Rendering pipeline

```
Claude Code → fires hook → agentbox-hook-record (appends JSONL)
                                     ↓
                          trail.jsonl (in-container)
                                     ↓
                          box-trail (tail -F + jq + ANSI color)
                                     ↓
                          auditor layout trail pane
```

`box-trail` tails the file and renders one color-coded line per event: cyan for
`PostToolUse`, red for `PostToolUseFailure`, magenta for `Stop`, dim for `PreToolUse`.

See docs/TRAIL.md for the JSONL event schema, schema drift notes (Claude 2.1.128 Stop events
use `last_assistant_message`, not `reason`; tool result field is `tool_response`), and the
guide for adding new agent adapters in v2.

## `agentbox run` lifecycle

The flow, step by step:

```
agentbox run [agent] [--fresh]
       │
       ▼
┌──────────────────────────────────────────────────────────────┐
│ 1. Resolve config                                            │
│    - Load ~/.config/agentbox/config.toml                     │
│    - Shallow-merge .agentbox.toml from $PWD if present       │
│    - Apply CLI flags last                                    │
│    - Pick agent + kit list from config or args               │
└──────────────────┬───────────────────────────────────────────┘
                   ▼
┌──────────────────────────────────────────────────────────────┐
│ 2. Compute project_id                                        │
│    project_id = sha1(realpath($PWD))[:12]                    │
└──────────────────┬───────────────────────────────────────────┘
                   ▼
┌──────────────────────────────────────────────────────────────┐
│ 3. (--fresh only) Tear down existing box                     │
│    podman rm -f agentbox-<project_id>                        │
│    rm -rf ~/.local/share/agentbox/sessions/<project_id>      │
└──────────────────┬───────────────────────────────────────────┘
                   ▼
┌──────────────────────────────────────────────────────────────┐
│ 4. Inspect existing box                                      │
│    podman inspect agentbox-<project_id> --format ...         │
│    ┌─ exists, running    ─┐                                  │
│    ├─ exists, stopped    ─┤  → handle below                  │
│    └─ doesn't exist      ─┘                                  │
└──────────────────┬───────────────────────────────────────────┘
                   ▼
┌──────────────────────────────────────────────────────────────┐
│ 5a. Doesn't exist: ensure kit image, create container        │
│     - resolve kit list → kit_image_tag (hash of list)        │
│     - if image missing: agentbox build <kit_list>            │
│     - resolve layout: --layout flag → config → "focus"       │
│       built-in: expand from internal/zellij/                 │
│       custom: read ~/.config/agentbox/layouts/<name>.kdl,    │
│               apply text/template substitution               │
│     - render layout.kdl + effective-config.toml              │
│     - if auditor + claude: write shadow settings, touch trail │
│     - podman create with full runtime spec (see SPEC.md)     │
│     - podman start                                           │
│                                                              │
│ 5b. Exists, stopped: render layout if missing, start         │
│     - podman start agentbox-<project_id>                     │
│                                                              │
│ 5c. Exists, running: nothing to do                           │
└──────────────────┬───────────────────────────────────────────┘
                   ▼
┌──────────────────────────────────────────────────────────────┐
│ 6. Attach via zellij-in-box                                  │
│    podman exec -it agentbox-<project_id> \                   │
│      zellij --layout /etc/agentbox/layout.kdl \              │
│      attach -c agentbox                                      │
│                                                              │
│    Layout's main pane has:                                   │
│      command "<agent.cmd[0]>"                                │
│      args    [...rest]                                       │
│    so the agent launches automatically.                      │
└──────────────────────────────────────────────────────────────┘
```

Detach via zellij's binding. The container keeps running. The CLI process exits cleanly.

## Kit build pipeline

```
agentbox build [kit_list]
       │
       ▼
┌──────────────────────────────────────────────────────────────┐
│ 1. Resolve kit list                                          │
│    - Walk depends_on graph from each named kit               │
│    - Topological sort (base first)                           │
│    - Dedupe                                                  │
└──────────────────┬───────────────────────────────────────────┘
                   ▼
┌──────────────────────────────────────────────────────────────┐
│ 2. Compute kit_image_tag                                     │
│    tag = "agentbox/" + sha1(joined_kit_list)[:12]            │
│    Tag is stable: same kit list = same tag.                  │
└──────────────────┬───────────────────────────────────────────┘
                   ▼
┌──────────────────────────────────────────────────────────────┐
│ 3. Cache check                                               │
│    If ~/.local/share/agentbox/cache/kits/<tag>.json exists   │
│    AND content hashes match → no-op, use existing image.     │
└──────────────────┬───────────────────────────────────────────┘
                   ▼ (cache miss)
┌──────────────────────────────────────────────────────────────┐
│ 4. Registry pull (optional, eligible lists only)             │
│    Eligibility: [registry] enabled = true, --no-pull not     │
│    passed, and every kit in the resolved list is a built-in  │
│    kit (no user kit shadows a built-in by name).             │
│                                                              │
│    podman pull ghcr.io/nklisch/agentbox-kits:<ver>-<sha>     │
│    podman tag  <remote-ref> <kit_image_tag>                  │
│    Write cache entry (source = "registry").                  │
│                                                              │
│    Pull failures fall through to step 5 — never abort.      │
│    NotFound: silent. Auth/Network/Unknown: visible warning.  │
└──────────────────┬───────────────────────────────────────────┘
                   ▼ (pull failed or skipped)
┌──────────────────────────────────────────────────────────────┐
│ 5. Generate Dockerfile                                       │
│    - FROM debian:bookworm-slim                               │
│    - For each kit in resolved order:                         │
│        COPY kit/<name>/ /tmp/kit-<name>/                     │
│        RUN apt-get install $(cat /tmp/kit-<name>/packages.txt) │
│        RUN /tmp/kit-<name>/install.sh                        │
│        RUN cat /tmp/kit-<name>/env.sh >> /etc/agentbox/env.sh │
│    - Final stage sources /etc/agentbox/env.sh in shell rc    │
└──────────────────┬───────────────────────────────────────────┘
                   ▼
┌──────────────────────────────────────────────────────────────┐
│ 6. Build                                                     │
│    podman build -t <kit_image_tag> -f <generated> <ctx>      │
│    Write cache metadata (source = "local").                  │
└──────────────────────────────────────────────────────────────┘
```

The generated Dockerfile is written to `~/.local/share/agentbox/cache/kits/<tag>.Dockerfile`
for inspection. `agentbox build --print` outputs it to stdout instead of building.
`agentbox build --print-tag` prints only the local tag without building (used by CI).
`agentbox build --emit-context <dir>` stages the Dockerfile and kit directories to disk;
CI uses this to hand the context to `docker buildx build --push` for multi-arch publishing.
`agentbox build --no-pull` skips step 4 unconditionally.

The eligibility check for the registry pull path is: all kits in the resolved list must
have `Source == "builtin"`. A user kit at `~/.config/agentbox/kits/<name>/` (including
one that shadows a built-in by name) disables the pull path for that kit list — user kit
content cannot match a pre-published image.

See `KITS.md` for the kit format spec. See `docs/features/registry-images.design.md` for
the full registry design.

## Network architecture

Four modes. The CLI sets up the network before container creation.

| Mode        | When to use                                       | Default? |
| ----------- | ------------------------------------------------- | -------- |
| `off`       | Audit/review with zero network                    |          |
| `safe`      | Day-to-day, broad access with threat-feed safety  | **yes**  |
| `allowlist` | Unattended runs locked to known hosts             |          |
| `open`      | Debugging the network policy itself               |          |

The two filtering modes (`safe` and `allowlist`) share the same plumbing: a per-project
podman network, a CoreDNS sidecar, and an iptables + ipset egress filter populated by
tailing CoreDNS's query log. They differ only in CoreDNS's Corefile and in whether the
ipset starts empty or pre-populated.

### `off` — no network

```
podman create --network=none ...
```

Container has loopback only. Useful for "definitely no exfiltration possible" runs.

### `safe` — threat-intel DNS + direct-IP egress block (default)

The day-to-day mode. Same sidecar topology as `allowlist`, different Corefile, no explicit
allow set.

```
                      ┌────────────────────────────────────────┐
                      │    podman network: agentbox-net-<id>   │
                      │                                        │
   ┌────────────────┐ │  ┌──────────────────┐  DNS query       │
   │  agentbox-<id> │ │  │  CoreDNS sidecar │  ───────────────┐│
   │  (the box)     │─┤  │                  │                 ││
   │                │ │  │  forward . →     │                 ││
   │  /etc/resolv.conf│ │  │    9.9.9.9     │                 ││
   │  → CoreDNS IP   │ │  │    (Quad9)     │                 ││
   │                │ │  │                 │                 ││
   │  egress to     │ │  │  Quad9 returns  │                 ││
   │  resolved IPs  │ │  │  NXDOMAIN for   │                 ││
   │  allowed;      │ │  │  known-bad      │                 ││
   │  direct IPs    │ │  │  domains        │                 ││
   │  dropped       │◄┤  │                 │  resolved IPs    ││
   └────────────────┘ │  └──────────────────┘  ◄───────────────┘│
                      │                                        │
                      └────────────────────────────────────────┘
                          │
                          │ iptables on host (block_direct_ip = true)
                          ▼
                      Egress allowed only to IPs CoreDNS has resolved
                      (same ipset machinery as allowlist mode, but the
                      set fills dynamically rather than from a fixed list).
```

**Generated Corefile (Quad9 example):**

```
. {
  forward . 9.9.9.9 149.112.112.112 {
    policy random
  }
  cache 300
  errors
  log . {
    class all
  }
}
```

CoreDNS 1.14.3's `log` plugin writes to **container stdout only** — no log file path is
supported. Query history is read via `podman logs <coredns-sidecar>` from the host. `box net`
inside the container fetches it the same way. The `agentbox-netfilter` daemon tails
`podman logs --follow` live to populate the ipset.

For `nextdns`, the forward target is `<id>.dns.nextdns.io` over DoH (CoreDNS's `forward`
plugin supports DoH). For `custom`, the forward target is `network.safe.upstream_servers`.

**`block_categories`** is forwarded as NextDNS query parameters (only effective with
`upstream = "nextdns"`). For Quad9 / Cloudflare the field is silently ignored.

**`extra_block` and `extra_allow`** are layered as RPZ-style overrides in the Corefile —
`extra_block` returns NXDOMAIN before forwarding, `extra_allow` short-circuits the
threat-intel response if it ever matches.

**`block_direct_ip = true` (the default)** uses the same iptables + ipset machinery as
`allowlist`: drop egress to any IP not in the set; populate the set by tailing CoreDNS's
query log. The ipset starts empty and fills as the box resolves things. Setting it to
`false` drops the egress filter entirely — DNS-based protection only.

The honest framing: `safe` blocks known-bad destinations and direct-IP exfil. It does not
prevent an agent from posting your secrets to a public Gist on `github.com`. Use
`allowlist` for that.

### `allowlist` — strict, explicit hostname list

The strict mode. Same machinery as `safe`, but CoreDNS only resolves the hosts in
`network.allowlist.allow` and the ipset is pre-populated from the resolutions of those
hosts at container start.

```
                      ┌────────────────────────────────────────┐
                      │    podman network: agentbox-net-<id>   │
                      │                                        │
   ┌────────────────┐ │  ┌──────────────────┐  DNS query       │
   │  agentbox-<id> │ │  │  CoreDNS sidecar │  ───────────────┐│
   │  (the box)     │─┤  │                  │                 ││
   │                │ │  │  forward zone:   │                 ││
   │  /etc/resolv.conf│ │  │    npmjs.org   │                 ││
   │  → CoreDNS IP   │ │  │    pypi.org    │                 ││
   │                │ │  │    github.com  │                 ││
   │  egress to     │ │  │  block all else│                 ││
   │  any IP        │ │  │                │                 ││
   │  blocked except│ │  │  forwards to   │  resolved IPs    ││
   │  resolved ones │◄┤  │  upstream DNS  │  ◄───────────────┘│
   └────────────────┘ │  └──────────────────┘                  │
                      │                                        │
                      └────────────────────────────────────────┘
                          │
                          │ iptables on host
                          ▼
                      Default route to outside world is DROP'd
                      for source IPs in the agentbox network,
                      EXCEPT for IPs that CoreDNS has resolved
                      (which are only the allow-list hosts).
```

The mechanics:

1. **Per-project podman network**, created on-demand: `agentbox-net-<project_id>`.
2. **CoreDNS container** (`docker.io/coredns/coredns:1.14.3`, label `agentbox.role=coredns`)
   attached to that network at a fixed IP. Its Corefile is generated from
   `network.allowlist.allow`:
   ```
   . {
     template IN ANY {
       rcode NXDOMAIN
     }
     forward npmjs.org pypi.org github.com . 1.1.1.1 8.8.8.8
     errors
     log . {
       class all
     }
   }
   ```
   Queries go to container stdout (CoreDNS 1.14.3 `log` plugin is stdout-only).
   Read via `podman logs <sidecar>`.
3. **Box's `/etc/resolv.conf` points only at CoreDNS.** No other resolvers.
4. **Egress filtering** via iptables + ipset on the host. A per-project ipset
   (`abx-<12hex>-a`) is populated by `agentbox-netfilter`, which tails
   `podman logs --follow` of the CoreDNS sidecar and adds resolved IPs. An iptables
   FORWARD DROP rule covers the agentbox network; traffic to IPs in the ipset is
   ACCEPT'd. The netfilter binary runs on the host (not in a container), launched by
   the CLI via `sudo -n agentbox-netfilter`.
5. **`box net`** inside the container shows recent queries +
   resolution status via `podman logs` of the sidecar, so you can debug "why can't I reach X."

If `network.allowlist.allow` is empty, allowlist mode degrades to "DNS resolves nothing,
egress blocked" — effectively the same as `off` but with the resolver wired up so error
messages are clearer.

### `open` — default bridge

```
podman create --network=bridge ...
```

No filtering. Escape hatch for debugging the network policy itself.

## State flows

Who owns what, and what happens on each operation:

| State                          | Owner             | Survives `rm`? | Survives `--fresh`? |
| ------------------------------ | ----------------- | -------------- | -------------------- |
| Project files                  | Host              | yes            | yes                  |
| `~/.gitconfig`                 | Host              | yes            | yes                  |
| `~/.claude/`, `~/.codex/`, etc | Host              | yes            | yes                  |
| Shell history (state dir)      | Host              | no             | no                   |
| Layout, effective-config       | Host (state dir)  | no             | no                   |
| Container filesystem changes   | Container         | no             | no                   |
| Installed packages in box      | Container         | no             | no                   |
| Kit image                      | Podman image cache | yes           | yes                  |
| Container labels               | Container         | no             | no                   |

The mental model: anything in your home directory survives. Anything that lives only in the
container does not. Kits (the images) survive both `rm` and `--fresh` — they're cached at
the podman level.

## Nested containers (when the `containers` kit is in use)

When `containers.enable = true`, the box has a working rootless podman inside it,
with `docker` aliased to it for ergonomics. The agent can run `docker compose up`, build
images, and so on, without the box being `--privileged` and without mounting the host's
docker socket.

```
┌──────────────────────────────────────────────────────────────────┐
│  agentbox-<id>                                                   │
│                                                                  │
│  ┌────────────────────┐                                          │
│  │  agent (in zellij) │  → docker compose up                     │
│  └─────────┬──────────┘                                          │
│            │                                                     │
│            ▼                                                     │
│  ┌────────────────────┐                                          │
│  │  podman (rootless) │  uses fuse-overlayfs storage             │
│  └─────────┬──────────┘                                          │
│            │                                                     │
│            ├─ inner container: postgres:16                       │
│            ├─ inner container: redis:7                           │
│            └─ inner container: app                               │
│                                                                  │
│  All inner-container egress: ─────────────────────────────────┐  │
└────────────────────────────────────────────────────────────────┼─┘
                                                                 │
                                                                 ▼
                                  ┌─────────────────────────────────┐
                                  │  CoreDNS sidecar + iptables     │
                                  │  (the outer agentbox network    │
                                  │   policy applies to everything  │
                                  │   inside the box)               │
                                  └─────────────────────────────────┘
```

Key properties:

- **No `--privileged`, no host socket.** Just `--device /dev/fuse` + a few re-granted caps.
- **Network policy applies to inner containers.** They share the box's network namespace,
  so they hit the same CoreDNS sidecar and the same egress filter. `safe`/`allowlist`
  threat-feed protection extends through to `docker compose` services.
- **Image cache is per-box.** Each agentbox container with the kit has its own podman
  image storage. Not shared with the host's images. (See SPEC.md "Open / deferred" for
  shared cache as a future feature.)
- **Port publishing is in-box only.** `docker run -p 8080:8080` binds 8080 inside the box,
  not on the host. Reach it with `agentbox exec . curl localhost:8080` from the host, or
  use a future `[runtime.ports]` knob.

Without the `containers` kit (or with `containers.enable = false`), the box has
no nested container capability at all and the runtime spec is the strict default.

## In-box process model

Inside a running box:

```
PID 1: sleep infinity                       (the container's main process)
       │
       ├─ podman exec -it ... zellij attach (one per attach)
       │     │
       │     └─ zellij server (in-box)
       │           ├─ agent pane:  claude / codex / ...  (all layouts)
       │           │   (focus)  ├─ git pane:   box-git-watch
       │           │            └─ stats pane: btm
       │           │   (reviewer)├─ diff pane:  box-diff-watch
       │           │            └─ tests pane: box-tests-watch
       │           │   (auditor)└─ trail pane: box-trail
       │           └─ shell tab:  zsh  (all layouts)
       │
       ├─ podman exec ... <one-off>          (agentbox exec ...)
       └─ ...
```

Multiple `agentbox exec` and `agentbox attach` invocations all share the same container.
The zellij session is named `agentbox` and is a singleton per box — additional `attach`
invocations join the existing session, not create new ones.

When the user closes their terminal without detaching cleanly, the `podman exec` process
dies but zellij keeps running inside the container. Reattach picks up exactly where they
left off.

## Failure modes worth designing for

| Symptom                                           | Likely cause                                   | Recovery                            |
| ------------------------------------------------- | ---------------------------------------------- | ----------------------------------- |
| `agentbox run` hangs at "attach"                  | Zellij in box died but container still running | `agentbox exec <id> pkill zellij`   |
| `podman exec` returns "container not running"     | Container OOM-killed or manually stopped       | `agentbox run` restarts it          |
| Layout pane crashes (agent exits)                 | Agent crashed or was killed                    | Reattach, zellij re-runs the cmd    |
| Kit image referenced by container no longer exists | User pruned podman images                     | `agentbox run --fresh`              |
| Bind-mount source disappears (`$PWD` deleted)     | User deleted the project on host               | `agentbox rm <id>`, recreate project |
| `~/.ssh` permissions wrong inside box             | Host umask drifted                             | Fix on host; box re-reads next start |
| `safe` mode: legitimate domain returns NXDOMAIN   | Threat feed false positive                     | Add to `network.safe.extra_allow`   |
| `safe` mode: agent reaches sketchy host anyway    | Domain not in any feed yet                     | Switch to `allowlist` for this run  |
| Inner `docker run` fails with mount/permission error | `containers.enable` is false        | Set true and `agentbox run --fresh` |
| Inner container can't reach the internet          | Outer network policy is too tight              | Adjust `network.allowlist.allow` or switch to `safe` |

`agentbox doctor` checks the runtime, the kit cache, mount source existence, DNS
sidecar health, and registry reachability (`registry-reachable` — warn-only HEAD probe
of the GHCR manifest endpoint; see SPEC.md "Open / deferred" for the known 401 limitation
on the probe). Run it when something's weird.

## What's deliberately not architected

- **No event bus.** `events.jsonl` is reserved as a future feature; v0.1/v0.2 don't write it.
- **No IPC between boxes.** Each project box is an island. Shared services (DBs, etc.) run
  on the host or in their own infrastructure.
- **No supervision.** If a box's agent crashes, it stays crashed until you reattach and the
  zellij pane re-runs the command. That's the contract — agentbox doesn't restart agents.
- **No telemetry.** No metrics, no usage reporting, no remote anything.
- **No TLS interception.** `safe` mode operates at DNS + IP level. Inspecting HTTPS payloads
  is out of scope and would require trusting a CA inside the box, which is a much bigger
  threat-model change.
