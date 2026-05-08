# agentbox

Single-user CLI for running AI coding agents inside per-project Podman
containers.

`agentbox run` ensures a per-project container exists for the current
directory, bind-mounts the project at the same absolute path inside the box,
and drops you into a [zellij](https://zellij.dev/) session where the
configured agent (Claude Code, Codex, opencode) is already running with its
permission guardrails turned off. Detach, walk away, come back hours later,
reattach. The project, agent config, and shell history persist on the host.
The container is disposable.

This is calibrated for one user — me, the author. Decisions favor my workflow
over generality. If it works for you too, great.

---

## Requirements

| Requirement | Linux | macOS |
| ----------- | ----- | ----- |
| **Podman** (preferred) or **Docker** | rootless podman | `podman machine` (no Docker Desktop required) |
| **iptables** + **ipset** | required for `safe` / `allowlist` network modes | not used (DNS-only on macOS) |
| **passwordless sudo** for `iptables`, `ipset`, `agentbox-netfilter` | required for `safe` / `allowlist` | not used |

`safe` is the default network mode. On Linux it needs the iptables stack and
passwordless sudo (see [Network policy on Linux](#network-policy-on-linux)).
If you want to defer that setup, run with `--network open` until you're ready.

---

## Install

Three options. Pick one.

### Option 1: `curl | sh` (recommended)

```sh
curl -fsSL https://raw.githubusercontent.com/nklisch/agentbox/main/scripts/install.sh | sh
```

Drops `agentbox` and `agentbox-netfilter` into `~/.local/bin/`, verifies the
sha256 against the release's `checksums.txt`, and appends a sentinel-wrapped
block to your shell rc so `~/.local/bin` is on `PATH`. On macOS it also strips
the Gatekeeper quarantine attribute.

Environment overrides:

| Variable | Effect |
| -------- | ------ |
| `AGENTBOX_VERSION=v0.2.0` | Pin to a specific release tag (default: latest) |
| `AGENTBOX_PREFIX=/usr/local/bin` | Install elsewhere (default: `~/.local/bin`) |
| `AGENTBOX_NO_PATH=1` | Skip the shell-rc PATH edit |
| `AGENTBOX_INSTALL_DEBUG=1` | Enable `set -x` tracing |

Open a new shell, then:

```sh
agentbox --version
agentbox doctor --fix
```

### Option 2: download a release tarball

Browse [releases](https://github.com/nklisch/agentbox/releases) and grab the
archive for your platform:

| Platform | Archive |
| -------- | ------- |
| Linux x86_64 | `agentbox_<version>_linux_amd64.tar.gz` |
| Linux arm64 | `agentbox_<version>_linux_arm64.tar.gz` |
| macOS Apple Silicon | `agentbox_<version>_darwin_arm64.tar.gz` |

```sh
tar -xzf agentbox_*.tar.gz
install -m 0755 agentbox agentbox-netfilter ~/.local/bin/
```

Verify against `checksums.txt` (also attached to each release):

```sh
sha256sum -c <(grep agentbox_*_linux_amd64.tar.gz checksums.txt)
```

### Option 3: `go install`

If you already have Go (≥1.25):

```sh
go install github.com/nklisch/agentbox/cmd/agentbox@latest
go install github.com/nklisch/agentbox/cmd/agentbox-netfilter@latest
```

`agentbox --version` will report `(devel)` because `go install` doesn't run
the project's `-ldflags`. Use Option 1 or 2 if you want a stamped version.

### macOS Apple Silicon: initialize Podman

```sh
podman machine init       # one-time
podman machine start
```

`agentbox doctor --fix` will start a stopped machine for you on later runs.

### Linux: configure passwordless sudo for `safe`/`allowlist`

These network modes need `iptables`, `ipset`, and `agentbox-netfilter` to run
as root. `agentbox doctor` resolves the right paths for your distro:

```sh
agentbox doctor      # look for the [WARN] sudo-iptables line
```

Drop the suggested line into `/etc/sudoers.d/agentbox` (mode `0440`, owner
`root`) and validate with `sudo visudo -c`.

If you skip this, run with `--network open` (less safe, no setup) or
`--network off` (no network).

### Verify

```sh
agentbox doctor --fix
```

Runs ten checks. `--fix` pulls the CoreDNS image and starts a stopped
`podman machine`; everything else is reported with the corrective action.

### Building from source

For development or distros without prebuilt binaries:

```sh
git clone https://github.com/nklisch/agentbox.git
cd agentbox
make install
```

Requires **Go** ≥ 1.25 and **make**. `make install` builds two static
(`CGO_ENABLED=0`) binaries and copies them to `~/.local/bin/`. Override the
prefix by hand if needed:

```sh
make build
install -m 0755 agentbox /usr/local/bin/agentbox
install -m 0755 agentbox-netfilter /usr/local/bin/agentbox-netfilter
```

---

## Quickstart

```sh
cd ~/dev/myproject
agentbox run
```

First run takes a while: it builds the default kit image (`polyglot +
containers + claude` is roughly 6–7 GB and takes several minutes the first
time, then cache-hits forever). Subsequent runs are instant.

You'll land in a zellij session inside the container with:

- **Main pane:** `claude --dangerously-skip-permissions` already running.
- **Bottom panes:** a git status ticker and `btm` (system stats).
- **Shell tab:** a plain `zsh` for poking around. Try `box info`, `box net`,
  `box scratch`.
- **Tab bar (top) and status bar (bottom):** zellij's built-in plugins are on
  so you can see the current mode and keybinds at all times. Press `Ctrl+p`
  for pane mode, `Ctrl+t` for tab mode, `Ctrl+o` for session mode, etc.

This is the `focus` layout (default). Try `--layout reviewer` for a live diff +
test runner alongside the agent, or `--layout auditor` for a real-time trail of
every tool call Claude makes. See [## Layouts](#layouts) below.

Detach with `Ctrl+o d` — that's `Ctrl+o` to enter session mode, then `d` for
detach. The container keeps running, your zellij session keeps its layout
and scrollback, and you can reattach later:

```sh
agentbox attach .
```

When you're done with a project's box:

```sh
agentbox rm .
```

The project files, your `~/.gitconfig`, your `~/.claude` config, and your
shell history all live on the host and survive `rm`. Anything inside the
container that wasn't bind-mounted is gone.

---

## Common operations

```sh
agentbox run                     # default agent in $PWD (defaults to claude)
agentbox run codex               # specific agent (must be in [agents.<name>])
agentbox run --fresh             # nuke the existing box and recreate
agentbox run --kits node,claude  # one-off kit override (ignores default_kits)
agentbox run --network open      # one-off network override
agentbox run --no-attach         # create/start, don't drop into zellij
agentbox run --mode safe         # claude-mode preset (claude agent only)
agentbox shell                   # bare shell, no agent
agentbox attach .                # reattach to current project's box
agentbox exec . pwd              # one-off command inside the box
agentbox ls                      # list running boxes
agentbox ls --all --json         # everything, NDJSON
agentbox rm .                    # stop + remove this project's box
agentbox rm --all --force        # nuke every agentbox container
agentbox build polyglot,claude   # build/rebuild a kit image
agentbox build --print base      # print the generated Dockerfile
agentbox doctor                  # verify everything's wired up
agentbox config show --json      # dump the merged config
```

Identifier resolution: most commands accept `.` (current `$PWD`'s
project_id), a 12-hex `project_id`, a unique prefix, or `agentbox-<id>`.

`--dry-run` works on every mutating command and prints the exact `podman` /
`docker` invocation it would run, so you can inspect before executing.

See [docs/CLI.md](docs/CLI.md) for the full command reference (every flag,
every exit code).

---

## Network policy on Linux

The default mode is `safe`: a per-box CoreDNS sidecar forwards to a
threat-intel-filtered upstream (Quad9 by default), and an iptables FORWARD
rule + ipset drop egress to any IP that wasn't resolved through the sidecar.
The `agentbox-netfilter` binary tails the sidecar's logs and populates the
ipset.

This requires:

- `iptables` and `ipset` installed (most distros: `pacman -S iptables ipset`,
  `apt install iptables ipset`).
- Passwordless sudo for `iptables`, `ipset`, and the `agentbox-netfilter`
  binary (see [step 4](#4-linux-optional-configure-passwordless-sudo-for-safeallowlist)).

If you don't want to set this up, you have three options:

```sh
agentbox run --network open      # default bridge, no filtering
agentbox run --network off       # no network at all
# or in ~/.config/agentbox/config.toml:
# [network] mode = "open"
```

`agentbox doctor` will tell you exactly which prerequisites are missing.

On macOS, agentbox runs in DNS-only mode automatically — the iptables checks
are skipped because `podman machine` runs in a VM.

---

## Configuration

- **Global:** `~/.config/agentbox/config.toml` (created lazily by `agentbox
  config edit`).
- **Per-project:** `<repo>/.agentbox.toml` (shallow-merged over global).
- **CLI flags** override both.

Open the global file with your `$EDITOR`:

```sh
agentbox config edit          # global
agentbox config edit --project   # per-project
agentbox config show --json   # dump the merged result
```

The most useful keys to know about:

```toml
runtime        = "podman"                            # or "docker"
default_agent  = "claude"
default_kits   = ["polyglot", "containers", "claude"]

[network]
mode = "safe"                                        # off | safe | allowlist | open

[containers]
enable = false                                       # set true for nested docker/compose

[agents.claude]
kits = ["polyglot", "claude"]
cmd  = ["claude", "--dangerously-skip-permissions"]
```

Full schema with defaults: [docs/SPEC.md § Configuration](docs/SPEC.md#configuration).

---

## Nested containers (`docker run` inside the box)

Off by default. To turn it on, add the `containers` kit and flip
`[containers] enable = true`:

```toml
default_kits = ["polyglot", "containers", "claude"]

[containers]
enable = true
```

Then `agentbox run --fresh`. Inside the box:

```sh
docker run --rm hello-world
docker compose up -d postgres
```

This works without `--privileged` and without mounting the host's docker
socket. The outer agentbox network policy (CoreDNS + ipset) extends to inner
containers because rootless podman shares the box's network namespace.

`agentbox doctor` warns when the kit is enabled in `default_kits` but
`enable = false` (the most common misconfiguration).

---

## Shell completion

User-level paths — no sudo:

```sh
# bash (auto-loaded from the XDG path by bash-completion 2.x)
mkdir -p ~/.local/share/bash-completion/completions
agentbox completion bash > ~/.local/share/bash-completion/completions/agentbox

# zsh (the dir must be on $fpath; add `fpath=(~/.zsh/completions $fpath)` to .zshrc)
mkdir -p ~/.zsh/completions
agentbox completion zsh > ~/.zsh/completions/_agentbox

# fish
mkdir -p ~/.config/fish/completions
agentbox completion fish > ~/.config/fish/completions/agentbox.fish
```

Open a new shell to pick up the completions. If you'd rather install
system-wide, the corresponding paths are `/etc/bash_completion.d/`,
`/usr/share/zsh/site-functions/`, and `/usr/share/fish/vendor_completions.d/`
— each requires sudo.

---

## Layouts

`agentbox run --layout <name>` selects a zellij layout. Built-ins:

- `focus` (default) — agent + git status ticker + system stats. The original layout.
- `reviewer` — agent + live diff vs base branch + watchexec test runner.
- `auditor` — agent + live trail of Claude Code tool calls (Claude only).

Sticky per project: set `[zellij] layout = "..."` in `.agentbox.toml`, or globally in
`~/.config/agentbox/config.toml`.

Drop your own KDL at `~/.config/agentbox/layouts/<name>.kdl` to add a custom layout —
template variables (`{{.ProjectAbs}}`, `{{.Shell}}`, `{{.AgentCmdFull}}`) are substituted
before zellij parses the file.

See [docs/LAYOUTS.md](docs/LAYOUTS.md) for the full reference.

---

## Uninstall

```sh
rm ~/.local/bin/agentbox ~/.local/bin/agentbox-netfilter

# Stop and remove every agentbox container + per-project state:
agentbox rm --all --force

# Or, if the binary is already gone:
podman ps -a --filter label=agentbox=1 -q | xargs -r podman rm -f
podman network ls --filter label=agentbox=1 -q | xargs -r podman network rm

# State and config:
rm -rf ~/.local/share/agentbox ~/.config/agentbox
```

If you set up a sudoers line for `safe`/`allowlist`, remove it via `visudo`.

# If you installed via curl|sh, also remove the sentinel-wrapped PATH block
# from your shell rc (~/.zshrc, ~/.bashrc, or ~/.config/fish/config.fish):
#   sed -i '/# >>> agentbox installer >>>/,/# <<< agentbox installer <<</d' ~/.zshrc

---

## Documentation

Read in this order:

1. [docs/VISION.md](docs/VISION.md) — why this exists, what it isn't.
2. [docs/SPEC.md](docs/SPEC.md) — every technical decision (config, runtime
   spec, labels, mounts, exit codes).
3. [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — how the pieces fit
   (lifecycle, kit pipeline, network topology).
4. [docs/CLI.md](docs/CLI.md) — command surface, flags, identifier resolution.
5. [docs/KITS.md](docs/KITS.md) — kit format and composition (read this
   before authoring a custom kit).
6. [docs/LAYOUTS.md](docs/LAYOUTS.md) — named layout system (focus/reviewer/auditor + custom KDL).
7. [docs/TRAIL.md](docs/TRAIL.md) — agent-activity trail (hook wiring, JSONL schema, auditor layout).
8. [docs/ROADMAP.md](docs/ROADMAP.md) — phase order and per-phase test
   checkpoints. [docs/PROGRESS.md](docs/PROGRESS.md) is the build journal.

---

## Status

**v0.3.0** (2026-05-05) — named layout system + agent-activity trail. Three built-in
layouts (`focus`, `reviewer`, `auditor`), custom KDL layout support, and a real-time
Claude Code tool-call trail for the `auditor` layout. Linux is the primary platform;
macOS via `podman machine` is supported. Docker works as a fallback runtime via
`--runtime docker`.

Known gaps are tracked in [docs/PROGRESS.md](docs/PROGRESS.md). Notable open items:

- Inner `docker run -p 8080:8080` only binds inside the box. Workaround:
  `agentbox exec . curl localhost:8080` from the host.
- Each box's `containers` kit has its own podman image cache. No shared
  registry yet.
- Kit images are built locally on first run, not pulled.
- Trail support is Claude-only in v1. Codex and opencode adapters are deferred.

---

## License

MIT — see [LICENSE](LICENSE).
