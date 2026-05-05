# agentbox — Roadmap

Built solo with AI assistance throughout. Phases are chunky: each is one design pass +
one implementation pass + one verification pass. Every checkpoint is a script the
implementing agent can run end-to-end to confirm done.

P1 → P2 → P3 → P4 are sequential. P5, P6, P7 can run in parallel after P4. P8 integrates.

---

## Phase 1: CLI scaffold, config loading, dry-run

**Goal:** `agentbox` exists as a binary, parses args, loads merged config, can describe
what it *would* do without touching podman.

**Build:**
- Go module, cobra-based CLI binary (or stdlib `flag` + sub-router; pick at design time)
- All subcommands stubbed: `run`, `shell`, `attach`, `exec`, `ls`, `rm`, `build`, `doctor`,
  `config`, `completion`
- TOML config loading: `~/.config/agentbox/config.toml` + `<repo>/.agentbox.toml`,
  shallow-merged, CLI flags last
- `agentbox config show` / `edit` / `path` working end-to-end
- `--dry-run` on every mutating command prints exact `podman` / `zellij` invocation it
  would run; exits 0
- `project_id = sha1(realpath($PWD))[:12]` computed and shown by `--dry-run`
- `agentbox doctor` minimal: checks runtime binary present + state dir writable
- Exit code conventions per CLI.md

**Test checkpoint:**
```sh
agentbox --version                        # prints version
agentbox config path                      # prints config path
mkdir /tmp/abx-test && cd /tmp/abx-test
agentbox config show --json               # prints merged config as JSON
agentbox run --dry-run > /tmp/out.sh      # prints podman invocation, exits 0
grep -q 'agentbox-' /tmp/out.sh           # container name present
grep -q "$(realpath .)" /tmp/out.sh       # same-path mount present
agentbox doctor                           # exits 0 with podman installed
```

---

## Phase 2: Kit pipeline, base kit, `agentbox build`

**Goal:** `agentbox build base` produces a working image with the in-box DX bundle.

**Build:**
- Kit format parser: `manifest.toml` (with depends_on, conflicts_with, provides),
  `packages.txt`, `install.sh`, `env.sh`
- Composition algorithm: dep walk → topo sort → dedupe → conflict check → sha1 image tag
- Generated Dockerfile writer (one COPY+RUN block per kit, prefix-numbered env.d)
- `agentbox build` with `--print`, `--list`, `--no-cache`, `--prune`
- Kit cache at `~/.local/share/agentbox/cache/kits/<tag>.json` with kit_hashes
- `base` kit shipped with binary: zsh + starship + fzf + zoxide + bat + eza + fd + rg +
  dust + duf + btm + procs + jq + yq + httpie + hyperfine + tldr + watchexec + delta +
  zellij + the `box` helpers (info/scratch/save/help; `box net` deferred to P6)

**Test checkpoint:**
```sh
agentbox build --list | grep -q '^base'
agentbox build --print base > /tmp/Dockerfile
grep -q 'FROM debian:bookworm-slim' /tmp/Dockerfile
agentbox build base                       # builds (slow first time)
TAG=$(agentbox build --print base | grep -oP 'agentbox/\w+' | head -1)
podman run --rm "$TAG" zsh -lc 'box info; rg --version; eza --version; zellij --version; jq --version'
agentbox build base                       # no-op, cache hit, exits fast
agentbox build --no-cache base            # forces rebuild
```

---

## Phase 3: Container lifecycle — run / shell / exec / attach / ls / rm

**Goal:** Per-project boxes that create on demand, persist across sessions, and clean up
cleanly. No zellij yet — `run` drops into a bare shell.

**Build:**
- `agentbox run` (without agent or zellij): ensure box exists → `podman exec -it ... zsh`
- `agentbox shell` (alias for run-without-agent semantics)
- `agentbox exec`, `agentbox attach`
- `agentbox ls` with `--all`, `--json`, `--project`, `--agent`, `--kit` filters
- `agentbox rm <id>` and `agentbox rm --all`, with `--force`, `--keep-state`
- Per-project lifecycle: existing+running → exec; existing+stopped → start; missing → create
- All labels written (`agentbox`, `agentbox.project`, `agentbox.project_id`, `.cwd`,
  `.agent`, `.kits`, `.kit_image`, `.created`)
- Bind mounts: project (same path), `~/.gitconfig` rw, `~/.ssh` ro, agent config dirs rw
  per `[mounts.agent_configs]`, persistent shell history rw
- Env passthrough by name from `secrets.passthrough`
- Network modes `off` (`--network=none`) and `open` (default bridge)
- `--fresh` flag
- `.` and prefix-match identifier resolution

**Test checkpoint:**
```sh
mkdir /tmp/abx-proj && cd /tmp/abx-proj && touch hello.txt
agentbox run --no-attach                  # creates box, exits
agentbox ls                               # shows the box with all label fields
agentbox exec . pwd                       # prints /tmp/abx-proj
agentbox exec . cat hello.txt             # prints file contents
agentbox exec . sh -c 'echo hi > /tmp/in-box'  # writes inside container only
ID=$(agentbox ls --json | head -1 | jq -r .project_id)
agentbox attach "${ID:0:6}"               # prefix match works (exit immediately)
agentbox rm .                             # cleans up
agentbox ls --all                         # box gone
test ! -d ~/.local/share/agentbox/sessions/$ID  # state dir cleaned
```

---

## Phase 4: Zellij-in-box, layout generation, `box` helpers

**Goal:** `agentbox run` opens a zellij session inside the box with the right layout.
Detach/reattach works.

**Build:**
- Zellij layout KDL generation: agent main pane (70%) + git ticker + `btm` stats + shell
  tab. For `agentbox shell`, single shell pane (or `--no-zellij`).
- Layout written to `~/.local/share/agentbox/sessions/<id>/layout.kdl`, mounted ro at
  `/etc/agentbox/layout.kdl`
- Effective config dumped to `effective-config.toml`, mounted ro at `/etc/agentbox/config.toml`
- `agentbox run` actually attaches: `podman exec -it <ctr> zellij --layout ... attach -c agentbox`
- `agentbox attach` reattaches via the same exec path
- `agentbox shell --no-zellij` skips zellij entirely
- `box info` reads labels + `/etc/agentbox/config.toml`, formats output per CLI.md
- `box scratch` cd's into a tmpfs scratch dir
- `box save <file>` copies to `~/.local/share/agentbox/sessions/<id>/saved/`
- `box help` lists all subcommands

**Test checkpoint:**
```sh
cd /tmp/abx-proj
# Manually attach + detach + reattach (interactive; agent runs and verifies via expect)
expect <<'EOF'
  spawn agentbox shell
  expect "agentbox"
  send "box info\r"
  expect "project_id"
  send "box save /etc/hostname\r"
  expect "saved"
  send "\x10d"   ;# Ctrl+p then d to detach
  expect eof
EOF
ls ~/.local/share/agentbox/sessions/*/saved/hostname  # confirms box save worked
agentbox attach .                         # reattaches the same zellij session
agentbox rm .
```

---

## Phase 5: Runtime kits + agent kits + agent integration  *(parallel with P6, P7)*

**Goal:** All built-in runtime kits build cleanly. `agentbox run` with an agent launches
the agent inside the zellij main pane.

**Build:**
- `polyglot` kit (node + python + go + rust + ruby + java/kotlin via sdkman + build tools
  + native + db clients) — chunky, ~3-4GB
- `node` kit (LTS + current, pnpm, bun, deno, yarn)
- `python` kit (uv, ruff, pyenv)
- `go` kit (toolchain + gopls + golangci-lint + delve)
- `rust` kit (rustup stable+nightly, cargo-watch, sccache)
- `systems` kit (clang, lld, cmake, ninja, gdb, lldb, valgrind)
- `cloud` kit (aws + gcloud + az + terraform + kubectl + helm)
- `claude` kit (`@anthropic-ai/claude-code` via npm; `depends_on = ["base", "node"]`)
- `codex` kit (`@openai/codex`; verify YOLO flag at this point — update `[agents.codex].cmd`)
- `opencode` kit (opencode binary; verify YOLO flag)
- Layout pane `command` field set from `[agents.<name>].cmd` so the agent auto-launches
- Default `default_agent = "claude"`, `default_kits = ["polyglot", "containers", "claude"]`

**Test checkpoint:**
```sh
for K in polyglot node python go rust systems cloud; do
  agentbox build "$K" || { echo "FAIL: $K"; exit 1; }
done
agentbox build polyglot,claude
agentbox build polyglot,codex
agentbox build polyglot,opencode

# Smoke test claude inside a real run (interactive — use expect)
cd /tmp/abx-proj
ANTHROPIC_API_KEY=$ANTHROPIC_API_KEY agentbox run --no-attach
agentbox exec . sh -c 'which claude && claude --version'
agentbox rm .
```

---

## Phase 6: Network policy — `safe` + `allowlist` + CoreDNS sidecar  *(parallel with P5, P7)*

**Goal:** `agentbox run --network safe` and `--network allowlist` apply real DNS-and-IP
filtering. `box net` shows what's been queried.

> **Implementation deviations** (see PROGRESS.md for detail): CoreDNS is pinned to
> `docker.io/coredns/coredns:1.14.3`, not `:latest`. CoreDNS 1.14.3's `log` plugin is
> stdout-only — `box net` reads queries via `podman logs <coredns-sidecar>` instead of a
> mounted `queries.log` file, and the netfilter daemon tails `podman logs --follow` to
> populate the ipset. The ipset name is `abx-<12hex>-a` (length-constrained).

**Build:**
- Per-project podman network creation: `agentbox-net-<project_id>`
- CoreDNS sidecar container (small image: `coredns/coredns:latest` + generated Corefile)
- Corefile generator for `safe`: forward to `network.safe.upstream` (quad9 / cloudflare-security
  / nextdns / custom); RPZ-style `extra_block` and `extra_allow` overrides
- Corefile generator for `allowlist`: forward only `network.allowlist.allow` zones; NXDOMAIN
  for everything else
- Box's `/etc/resolv.conf` rewritten to point only at the sidecar's IP
- iptables + ipset egress filter on host: drop egress on the network's interface to any IP
  not in the per-network ipset; populate the ipset by tailing CoreDNS's query log
- `box net` reads `/var/log/coredns/queries.log` and prints recent activity + policy
- `block_direct_ip = true` default for `safe`; configurable to false
- `network.safe.block_categories` forwarded as NextDNS query parameters when `upstream = "nextdns"`

**Test checkpoint:**
```sh
cd /tmp/abx-proj
agentbox run --network safe --no-attach
agentbox exec . dig +short api.anthropic.com   # resolves
agentbox exec . dig +short dns.malware.test.example  # NXDOMAIN if in feed
agentbox exec . sh -c 'curl -fsS --max-time 3 https://1.1.1.1 || echo BLOCKED'  # BLOCKED (direct IP)
agentbox exec . box net                  # shows queries with ALLOWED/BLOCKED labels
agentbox rm .

# Allowlist
cat > /tmp/abx-proj/.agentbox.toml <<'EOF'
[network]
mode = "allowlist"
[network.allowlist]
allow = ["github.com"]
EOF
agentbox run --no-attach
agentbox exec . sh -c 'curl -fsS --max-time 3 https://github.com >/dev/null && echo OK'  # OK
agentbox exec . sh -c 'curl -fsS --max-time 3 https://npmjs.org >/dev/null || echo BLOCKED'  # BLOCKED
agentbox rm .
```

---

## Phase 7: `containers` kit + nested rootless podman + `[runtime.containers]`  *(parallel with P5, P6)*

**Goal:** `docker run` and `docker compose up` work from inside the box, without
`--privileged` and without mounting the host docker socket.

> **Implementation deviation:** the config table is `[containers]`, not
> `[runtime.containers]`. The original spelling collided with the top-level `runtime =
> "podman"` key (TOML forbids a key being both a value and a table parent). The block was
> renamed during Phase 1; Phase 7 picked it up as `[containers]`. SPEC.md, CLI.md, and
> the live config reflect the renamed shape.

**Build:**
- `containers` kit: podman + buildah + skopeo + podman-compose + docker-compose +
  fuse-overlayfs + slirp4netns
- `env.sh`: alias `docker → podman`; `export DOCKER_HOST=unix:///run/user/.../podman.sock`
- Configure `containers/storage.conf` and `containers/registries.conf` for nested rootless
- `[runtime.containers]` config block in CLI
- Conditional `podman create` flags when `enable = true`: `--device /dev/fuse`,
  `--cap-add SETUID --cap-add SETGID`, `--security-opt seccomp=/etc/agentbox/seccomp/containers.json`,
  `--security-opt unmask=/proc/sys/net/ipv4`
- Bundled `containers.json` seccomp profile (podman default minus a handful of restrictions)
- `agentbox doctor` warns when `containers` kit is in `default_kits` but `enable = false`

**Test checkpoint:**
```sh
cd /tmp/abx-proj
agentbox run --kits polyglot,containers,claude --no-attach
agentbox exec . docker run --rm hello-world      # exits 0 with hello text
agentbox exec . sh -c 'docker run --rm alpine ip route'  # has a default route
cat > /tmp/abx-proj/compose.yml <<'EOF'
services:
  redis:
    image: redis:7-alpine
EOF
agentbox exec -w /tmp/abx-proj . docker compose up -d
agentbox exec . docker ps                        # shows redis container
agentbox exec . sh -c 'docker exec $(docker ps -q) redis-cli ping'  # PONG
agentbox exec -w /tmp/abx-proj . docker compose down
agentbox rm .
```

---

## Phase 8: macOS support, Docker fallback, completion, ship

**Goal:** Tool runs cleanly on macOS via `podman machine`, works against Docker as a
fallback runtime, has shell completion. v1 is shippable.

**Build:**
- Runtime abstraction layer: `runtime` interface with `podman` and `docker` impls (flag
  surfaces are nearly identical — diffs go in one file each)
- `agentbox doctor` extended: checks `podman machine` running on macOS, kit cache health,
  bind-mount source existence per running box, network mode dependencies (iptables/ipset
  available, CoreDNS sidecar image present)
- `agentbox doctor --fix` attempts safe auto-remediation (start `podman machine` if stopped)
- `agentbox completion bash|zsh|fish` prints a completion script
- macOS smoke test: full P3-P7 flow on Apple Silicon under `podman machine`
- Project README at root linking to docs/
- Tag v0.1.0

**Test checkpoint:**
```sh
agentbox doctor --json | jq '[.checks[] | select(.status != "ok")] | length'  # → 0
agentbox completion zsh > /tmp/comp && grep -q '_agentbox' /tmp/comp
agentbox --runtime docker ls              # works against docker too
agentbox --runtime docker run --no-attach && agentbox --runtime docker rm .

# macOS (manual smoke; agent runs on host with podman machine up)
podman machine list | grep -q Running
cd ~/dev/some-real-project
agentbox run                              # full flow: zellij, agent, network policy, mounts
# Detach. Reattach. Rm.

git tag v0.1.0
```
