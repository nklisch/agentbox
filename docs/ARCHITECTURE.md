# ARCHITECTURE

How the pieces fit. Read SPEC.md first for the *what*; this doc is the *how*.

## Component overview

```
┌─────────────────────────────────────────────────────────────────────┐
│                            HOST                                     │
│                                                                     │
│   ┌──────────────────┐                                              │
│   │  agentbox CLI    │  parse args → load config → resolve kit      │
│   │  (Go binary)     │  → shell out to podman / zellij → exit       │
│   └────────┬─────────┘                                              │
│            │                                                        │
│            │ shells out                                             │
│            ▼                                                        │
│   ┌──────────────────┐                                              │
│   │  podman / docker │  manages container + network + volumes        │
│   └────────┬─────────┘                                              │
│            │                                                        │
│   ┌────────┴────────────────────────────────────────────────┐       │
│   │              custom podman network                       │       │
│   │                                                          │       │
│   │   ┌──────────────────────┐    ┌──────────────────────┐  │       │
│   │   │  agentbox-<id>       │    │  CoreDNS sidecar     │  │       │
│   │   │  (the box)           │    │  (safe / allowlist)  │  │       │
│   │   │                      │    │                      │  │       │
│   │   │  ┌────────────────┐  │    │  forwards to         │  │       │
│   │   │  │  zellij        │  │    │  threat-feed DNS or  │  │       │
│   │   │  │  ├ agent pane  │  │    │  resolves only       │  │       │
│   │   │  │  ├ git pane    │  │    │  allowed hosts       │  │       │
│   │   │  │  └ stats pane  │  │    │                      │  │       │
│   │   │  │                │  │    │  iptables blocks     │  │       │
│   │   │  ├ shell tab      │  │    │  unresolved-IP       │  │       │
│   │   │  └ box helpers    │  │    │  egress              │  │       │
│   │   └────────────────────┘  │    └──────────────────────┘  │       │
│   │       │                                                  │       │
│   │       │ bind mounts                                      │       │
│   │       ▼                                                  │       │
│   │   ┌────────────────────────────────────────────────┐     │       │
│   │   │  $PWD (project)        same path inside        │     │       │
│   │   │  ~/.gitconfig          rw                      │     │       │
│   │   │  ~/.ssh                ro                      │     │       │
│   │   │  ~/.claude (etc.)      rw                      │     │       │
│   │   │  STATE_DIR/history     rw (persistent)         │     │       │
│   │   │  STATE_DIR/layout.kdl  ro                      │     │       │
│   │   └────────────────────────────────────────────────┘     │       │
│   └──────────────────────────────────────────────────────────┘       │
│                                                                      │
│   ┌──────────────────────────────────────────────────────┐           │
│   │  ~/.config/agentbox/        (config, custom kits)    │           │
│   │  ~/.local/share/agentbox/   (sessions, cache)        │           │
│   └──────────────────────────────────────────────────────┘           │
└─────────────────────────────────────────────────────────────────────┘
```

Three things to internalize:

1. **The CLI is short-lived.** Every `agentbox` command runs, mutates state via podman or
   the filesystem, and exits. There is no background process.
2. **The container is long-lived.** Once created (per-project), it runs `sleep infinity` and
   stays up across detaches. All interaction happens through `podman exec`.
3. **Zellij lives inside the box.** `agentbox run` is essentially "podman exec + zellij
   attach". The host doesn't need zellij installed.

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
│     - render layout.kdl + effective-config.toml              │
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
                   ▼
┌──────────────────────────────────────────────────────────────┐
│ 4. Generate Dockerfile                                       │
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
│ 5. Build                                                     │
│    podman build -t <kit_image_tag> -f <generated> <ctx>      │
│    Write cache metadata.                                     │
└──────────────────────────────────────────────────────────────┘
```

The generated Dockerfile is written to `~/.local/share/agentbox/cache/kits/<tag>.Dockerfile`
for inspection. `agentbox build --print` outputs it to stdout instead of building.

See `KITS.md` for the kit format spec.

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
  log /var/log/coredns/queries.log
}
```

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
2. **CoreDNS container** attached to that network at a fixed IP. Runs from a small image
   built into the kit pipeline. Its Corefile is generated from `network.allowlist.allow`:
   ```
   . {
     forward npmjs.org pypi.org github.com . 1.1.1.1 8.8.8.8
     errors
     log ./allowlist.log
   }
   ```
3. **Box's `/etc/resolv.conf` points only at CoreDNS.** No other resolvers.
4. **Egress filtering** — two strategies, both viable:
   - **Netavark plugin** (Podman): drop all egress on the network's interface, allow only
     IPs that CoreDNS has recently resolved (read from CoreDNS's log). This is the cleaner
     long-term solution.
   - **iptables on the host**: per-network DROP rule, with an `ipset` populated by tailing
     CoreDNS's log. Simpler for v0.2.

   v0.2 ships the iptables approach. Plugin is a v0.3+ refactor.

5. **`box net`** inside the container reads the DNS log and shows recent queries +
   resolution status, so you can debug "why can't I reach X."

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

When `runtime.containers.enable = true`, the box has a working rootless podman inside it,
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

Without the `containers` kit (or with `runtime.containers.enable = false`), the box has
no nested container capability at all and the runtime spec is the strict default.

## In-box process model

Inside a running box:

```
PID 1: sleep infinity                       (the container's main process)
       │
       ├─ podman exec -it ... zellij attach (one per attach)
       │     │
       │     └─ zellij server (in-box)
       │           ├─ agent pane: claude / codex / ...
       │           ├─ git pane:   watch -n 2 git status -s
       │           ├─ stats pane: btm
       │           └─ shell tab:  zsh
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
| Inner `docker run` fails with mount/permission error | `runtime.containers.enable` is false        | Set true and `agentbox run --fresh` |
| Inner container can't reach the internet          | Outer network policy is too tight              | Adjust `network.allowlist.allow` or switch to `safe` |

`agentbox doctor` checks the runtime, the kit cache, mount source existence, and DNS
sidecar health. Run it when something's weird.

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
