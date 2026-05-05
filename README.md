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
| **Go** ≥ 1.25 | to build the binaries | same |
| **Podman** (preferred) or **Docker** | rootless podman | `podman machine` (no Docker Desktop required) |
| **make** | for `make install` | same |
| **iptables** + **ipset** | required for `safe` / `allowlist` network modes | not used (DNS-only on macOS) |
| **passwordless sudo** for `iptables`, `ipset`, `agentbox-netfilter` | required for `safe` / `allowlist` | not used |

`safe` is the default network mode. On Linux it needs the iptables stack and
passwordless sudo (see [Network policy on Linux](#network-policy-on-linux)).
If you want to defer that setup, run with `--network open` until you're ready.

---

## Install

### 1. Build from source

There are no release tarballs or Homebrew taps yet. Build it yourself:

```sh
git clone https://github.com/nklisch/agentbox.git
cd agentbox
make install
```

`make install` builds two static (CGO-disabled) binaries and copies them to
`~/.local/bin/`:

- `agentbox` — the primary CLI.
- `agentbox-netfilter` — a small helper that tails the CoreDNS sidecar's logs
  and populates the per-box ipset used by the `safe` / `allowlist` egress
  filter. Only invoked when those network modes are active.

If you want a different prefix, override the install target by hand:

```sh
make build
install -m 0755 agentbox /usr/local/bin/agentbox
install -m 0755 agentbox-netfilter /usr/local/bin/agentbox-netfilter
```

### 2. Make sure the install dir is on `PATH`

```sh
echo $PATH | tr : '\n' | grep -qx "$HOME/.local/bin" || \
  echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zshrc   # or ~/.bashrc
```

Open a new shell and confirm:

```sh
agentbox --version
```

### 3. (macOS only) Initialize a Podman machine

```sh
podman machine init       # one-time
podman machine start
```

`agentbox doctor --fix` will start the machine for you on subsequent runs if
it's stopped.

### 4. (Linux, optional) Configure passwordless sudo for `safe`/`allowlist`

These modes need `iptables` + `ipset` + `agentbox-netfilter` to run as root.
Add a sudoers entry via `visudo`:

```
%wheel ALL=(root) NOPASSWD: /usr/sbin/iptables, /usr/sbin/ipset, /usr/local/bin/agentbox-netfilter
```

Adjust the user/group and the binary path (`~/.local/bin/agentbox-netfilter`
vs `/usr/local/bin/agentbox-netfilter`) to match your install. `agentbox
doctor` checks this and tells you exactly what's missing.

If you skip this step, run with `--network open` (less safe, no setup
required) or `--network off` (no network at all).

### 5. Verify the install

```sh
agentbox doctor --fix
```

`doctor` runs ten checks (runtime, state dir, iptables/ipset, sudo, CoreDNS
image, containers config, `podman machine`, kit cache, mount sources). The
`--fix` flag pulls the CoreDNS image and starts a stopped `podman machine`
automatically; everything else is reported with the corrective action so you
can fix it by hand.

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

Detach with `Ctrl+p d` (zellij's default). The container keeps running. Come
back later:

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

```sh
agentbox completion zsh > ~/.zsh/completions/_agentbox     # zsh
agentbox completion bash > /etc/bash_completion.d/agentbox # bash
agentbox completion fish > ~/.config/fish/completions/agentbox.fish
```

Then re-source your shell config or open a new shell.

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

If you set up the sudoers line in step 4, remove it via `visudo`.

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
6. [docs/ROADMAP.md](docs/ROADMAP.md) — phase order and per-phase test
   checkpoints. [docs/PROGRESS.md](docs/PROGRESS.md) is the build journal.

---

## Status

**v0.1.0** — first cut. All eight roadmap phases shipped. Linux is the
primary platform; macOS via `podman machine` is supported (smoke-tested on
Apple Silicon). Docker works as a fallback runtime via `--runtime docker`.

Known gaps and v0.2+ work are listed in [docs/PROGRESS.md § Known
issues](docs/PROGRESS.md#known-issues--deferred-to-v02). Notable ones:

- Inner `docker run -p 8080:8080` only binds inside the box. Workaround:
  `agentbox exec . curl localhost:8080` from the host.
- Each box's `containers` kit has its own podman image cache. No shared
  registry yet.
- Kit images are built locally on first run, not pulled. v0.3+ may add a
  registry distribution.

---

## License

MIT — see [LICENSE](LICENSE).
