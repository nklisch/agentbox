# CLI

Reference for every `agentbox` command, every `box` helper inside the container, and the
conventions both share. SPEC.md describes *what* the tool does; this doc describes *how to
invoke it*.

## Conventions

### Identifier resolution

Most commands take a `<project_id>` argument. Resolution rules:

- **Exact match** against the 12-char `agentbox.project_id` label — wins outright.
- **Unique prefix match** — `agentbox attach a3f` matches `a3f2c1d4e5b6` if no other id
  starts with `a3f`. Ambiguous prefixes are rejected with a list of matches.
- **`.` (literal dot)** resolves to the project_id of the current `$PWD`. Useful for
  `agentbox attach .`, `agentbox rm .`, etc.
- **Container name `agentbox-<id>`** is also accepted in the same places.

### Exit codes

| Code | Meaning                                                          |
| ---- | ---------------------------------------------------------------- |
| 0    | Success.                                                         |
| 1    | Generic error (printed message will say what).                   |
| 2    | Invalid arguments or config.                                     |
| 3    | Container runtime (`podman`/`docker`) not available or unhealthy. |
| 4    | Project box not found (for commands that require an existing box). |
| 5    | Kit build failed.                                                |
| 6    | Network setup failed.                                            |
| 7    | Bind-mount source missing on host.                               |
| 130  | Interrupted (SIGINT).                                            |

### Global flags

Available on every command:

| Flag                  | Default       | Behavior                                               |
| --------------------- | ------------- | ------------------------------------------------------ |
| `--config <path>`     | `~/.config/agentbox/config.toml` | Override global config path.        |
| `--no-project-config` | false         | Ignore `.agentbox.toml` in `$PWD`.                     |
| `--runtime <name>`    | from config   | `podman` or `docker`. Overrides config.                |
| `--dry-run`           | false         | Print the equivalent shell commands; do not execute.   |
| `--json`              | false         | Machine-readable output where supported.               |
| `--quiet, -q`         | false         | Suppress non-error output.                             |
| `--verbose, -v`       | false         | Verbose logging to stderr.                             |
| `--help, -h`          |               | Show help for this command.                            |
| `--version`           |               | Print agentbox version and exit.                       |

### Output

- Human-readable by default. Tables for lists, key: value for single records.
- `--json` switches list-style commands to NDJSON (one record per line) and single-record
  commands to a single JSON object.
- All errors go to stderr. All structured output goes to stdout.

---

## Commands

### `agentbox run`

Create or attach to the per-project box, launch the configured agent.

```
agentbox run [agent] [flags]
```

**Arguments:**

| Argument | Description                                                          |
| -------- | -------------------------------------------------------------------- |
| `agent`  | Optional. Agent name from `[agents.<name>]` config. Defaults to `default_agent`. |

**Flags:**

| Flag                | Behavior                                                    |
| ------------------- | ----------------------------------------------------------- |
| `--fresh`           | Remove any existing box for this project before creating.   |
| `--kits <list>`     | Override the kit list (comma-separated). Implies `--fresh` if the resolved kit_image differs. |
| `--network <mode>`  | Override `network.mode` for this run.                       |
| `--layout <name>`   | Zellij layout to use: `focus` (default), `reviewer`, `auditor`, or a custom name. Overrides `[zellij].layout` config. Exit code 2 if the name is not a built-in and no file exists at `~/.config/agentbox/layouts/<name>.kdl`. See [docs/LAYOUTS.md](LAYOUTS.md). |
| `--no-attach`       | Create/start the box but don't attach (for scripting).      |
| `--detach-on-exit`  | Stop the container when the agent process exits (default: keep running). |

**Behavior:** see ARCHITECTURE.md "agentbox run lifecycle." TL;DR: ensures a per-project
box is up, then `podman exec`s into it and runs `zellij attach -c agentbox` with a
generated layout that auto-launches the agent.

**Examples:**

```sh
agentbox run                       # default agent in $PWD
agentbox run codex                 # specific agent
agentbox run --fresh               # nuke and recreate
agentbox run --kits polyglot,cloud,claude
agentbox run --network off         # no network for this run
agentbox run --no-attach           # spin up, don't attach
```

### `agentbox shell`

Bare interactive shell in the per-project box. No agent.

```
agentbox shell [flags]
```

Same lifecycle as `run` but the zellij layout is replaced by a single shell pane (zsh by
default, per `[shell].shell`). Useful when you want to poke around the box without an
agent running.

**Flags:**

| Flag      | Behavior                                                      |
| --------- | ------------------------------------------------------------- |
| `--no-zellij` | Skip zellij entirely. Just `podman exec -it ... <shell>`. |

### `agentbox attach`

Reattach to a running box's zellij session.

```
agentbox attach <project_id> [flags]
```

Equivalent to `podman exec -it agentbox-<project_id> zellij attach -c agentbox`. Fails
with exit code 4 if no box exists for that id.

**Examples:**

```sh
agentbox attach a3f2c1d4e5b6
agentbox attach a3f                # prefix match
agentbox attach .                  # current $PWD's project
```

### `agentbox exec`

Run a one-off command inside a live box.

```
agentbox exec <project_id> <command> [args...] [flags]
```

**Flags:**

| Flag                | Behavior                                                |
| ------------------- | ------------------------------------------------------- |
| `-i, --interactive` | Attach stdin (default: off).                            |
| `-t, --tty`         | Allocate a TTY (default: off).                          |
| `-w, --workdir <d>` | Working directory (default: the project's mount point). |
| `--shell`           | Wrap the command in `$SHELL -c '<cmd>'`.                |

Stdin/stdout/stderr stream from/to the host. Exit code is the command's exit code.

**Examples:**

```sh
agentbox exec . ls -la
agentbox exec a3f --shell "rg TODO | wc -l"
agentbox exec . -it -- bash        # interactive shell, no zellij
```

### `agentbox ls`

List boxes.

```
agentbox ls [flags]
```

**Flags:**

| Flag                    | Behavior                                                     |
| ----------------------- | ------------------------------------------------------------ |
| `--all, -a`             | Include stopped/exited boxes (default: running only).        |
| `--project <name>`      | Filter by `agentbox.project` label.                          |
| `--agent <name>`        | Filter by `agentbox.agent` label.                            |
| `--kit <name>`          | Filter by membership in `agentbox.kits`.                     |
| `--json`                | NDJSON output (one record per line).                         |

**Default human output:**

```
PROJECT_ID    PROJECT      AGENT    KITS              STATUS         CREATED
a3f2c1d4e5b6  myapp        claude   polyglot,claude   Up 12 minutes  2026-05-04T14:32
b8d4e7a1c2f3  api-svc      codex    polyglot,codex    Up 2 hours     2026-05-04T12:40
c1a9f30b4d5e  scratch      shell    polyglot          Exited (0)     2026-05-04T09:15
```

**JSON record shape:**

```json
{"project_id":"a3f2c1d4e5b6","project":"myapp","cwd":"/home/nathan/dev/myapp","agent":"claude","kits":["polyglot","claude"],"kit_image":"agentbox/...","status":"running","created":"2026-05-04T14:32:00Z"}
```

### `agentbox rm`

Stop and remove boxes + their session state.

```
agentbox rm <project_id> [flags]
agentbox rm --all
```

**Flags:**

| Flag        | Behavior                                                              |
| ----------- | --------------------------------------------------------------------- |
| `--all`     | Remove every container with `agentbox=1` label. Required if no `<project_id>`. |
| `--force`   | Don't prompt for confirmation when using `--all`.                     |
| `--keep-state` | Remove container but keep `~/.local/share/agentbox/sessions/<id>/`. |

**Behavior:**

- Stops the container if running, then removes it.
- Removes the per-project podman network (`agentbox-net-<id>`) if no other boxes use it.
- Removes the session state directory unless `--keep-state`.
- Does **not** remove the kit image. Use `agentbox build --prune` for that.
- Does **not** touch the project files.

Refuses to run with no arguments and no `--all` (exit code 2). This is intentional — `rm`
defaulting to "current project" felt too loaded.

### `agentbox build`

Build (or rebuild) a composed kit image.

```
agentbox build [kit_list] [flags]
```

**Arguments:**

| Argument    | Description                                                                |
| ----------- | -------------------------------------------------------------------------- |
| `kit_list`  | Comma-separated list of kits. Defaults to `default_kits` from config.      |

**Flags:**

| Flag           | Behavior                                                            |
| -------------- | ------------------------------------------------------------------- |
| `--no-cache`   | Force a full rebuild. Skips both agentbox's cache and podman's layer cache. |
| `--print`      | Print the generated Dockerfile to stdout. Don't build.              |
| `--list`       | List all known kits (built-in + user-authored) and exit.            |
| `--prune`      | Remove kit images not referenced by any current box.                |

**Examples:**

```sh
agentbox build                            # build default_kits
agentbox build polyglot,cloud,claude
agentbox build --print polyglot,go        # inspect the generated Dockerfile
agentbox build --no-cache base
agentbox build --prune
```

### `agentbox doctor`

Check that the system is set up correctly.

```
agentbox doctor [flags]
```

Checks (run in order; each prints `[OK]`, `[WARN]`, or `[FAIL]`):

| # | Check name          | What it verifies                                                              |
|---|---------------------|-------------------------------------------------------------------------------|
| 1 | `runtime`           | `podman` or `docker` is on PATH and responds to `version`.                    |
| 2 | `state-dir`         | `~/.local/share/agentbox/` exists and is writable.                           |
| 3 | `iptables`          | (Linux) `iptables` is installed.                                              |
| 4 | `ipset`             | (Linux) `ipset` is installed.                                                 |
| 5 | `sudo-iptables`     | (Linux) Passwordless `sudo iptables` and `sudo ipset` work. Required for `safe`/`allowlist` modes. |
| 6 | `coredns-image`     | `docker.io/coredns/coredns:1.14.3` is present locally.                       |
| 7 | `containers-config` | WARN when `containers` kit is in `default_kits` but `containers.enable=false`. |
| 8 | `podman-machine`    | (macOS) A `podman machine` is running.                                        |
| 9 | `kit-cache`         | Cache JSON entries match real images in the podman image store.               |
|10 | `mount-sources`     | Each running box's bind-mount sources still exist on the host.                |

Exits 0 if all checks pass, 1 if any check FAIL.

**`--fix` semantics:** runs auto-remediation for checks that support it. Currently fixes:
- `podman-machine` — starts the machine if stopped.
- `coredns-image` — pulls `docker.io/coredns/coredns:1.14.3`.

Other checks report the corrective action (e.g. the sudoers line to add) but don't modify
the system. `--fix` is the first-run equivalent of `agentbox init` — no separate init
command exists.

**sudo requirement:** `safe` and `allowlist` modes need passwordless sudo for `iptables`,
`ipset`, and `agentbox-netfilter`. Binary paths vary by distro — `agentbox doctor` prints
a copy-pasteable line for the current system. Examples:

```
# Fedora / RHEL / Nobara:
%wheel ALL=(root) NOPASSWD: /usr/bin/iptables, /usr/bin/ipset, /home/<you>/.local/bin/agentbox-netfilter

# Debian / Ubuntu:
%sudo  ALL=(root) NOPASSWD: /usr/sbin/iptables, /usr/sbin/ipset, /usr/local/bin/agentbox-netfilter
```

**Flags:**

| Flag         | Behavior                                                       |
| ------------ | -------------------------------------------------------------- |
| `--fix`      | Auto-remediate checks that support it (see above).             |
| `--json`     | JSON output for scripting.                                     |

### `agentbox config`

Inspect or edit configuration.

```
agentbox config show [--effective] [--json]
agentbox config edit [--global | --project]
agentbox config path [--global | --project]
```

**Subcommands:**

| Subcommand | Behavior                                                              |
| ---------- | --------------------------------------------------------------------- |
| `show`     | Print the merged configuration. With `--effective`, includes CLI-flag overrides as if a `run` were happening now. |
| `edit`     | Open `$EDITOR` on the config file. `--global` (default) or `--project`. Creates the file if missing. |
| `path`     | Print the file path and exit.                                         |

`config show` redacts nothing — but the only secrets in the config are env var **names**,
not values, so there's nothing to redact.

---

## In-box `box` helpers

Available on the `PATH` inside any agentbox container. All implemented as small bash
scripts under `/usr/local/bin/`. Designed to be discoverable when you `agentbox shell` in
and want to know what you're looking at.

### `box`

Dispatcher. Bare `box` runs `box help`.

### `box info`

Print everything the container knows about itself.

```
$ box info
project_id   a3f2c1d4e5b6
project      myapp
cwd          /home/nathan/dev/myapp
agent        claude
kits         polyglot, claude
kit_image    agentbox/8c3f1a2b4d5e
network      safe (upstream: quad9, block_direct_ip: true)
mounts
  /home/nathan/dev/myapp     rw  (project)
  /root/.gitconfig            rw
  /root/.ssh                  ro
  /root/.claude               rw
resources
  cpus     4
  memory   8g
  pids     512
created      2026-05-04T14:32:00Z
```

Sources data from container labels and `/etc/agentbox/config.toml` (mounted in by the CLI).

### `box scratch`

`cd` into a tmpfs scratch directory at `/tmp/box-scratch`. Anything written there
disappears on container restart.

```
$ box scratch
/tmp/box-scratch $ # ephemeral work goes here
```

### `box net`

Show the current network policy and recent DNS activity. Only useful in `safe` /
`allowlist` modes.

```
$ box net
mode         safe (upstream: quad9)
block_direct_ip: true
allow:       (none — safe mode)
extra_allow: example.internal
extra_block: facebook.com

recent queries (last 50):
  2026-05-04T15:10:01  registry.npmjs.org           → 104.16.16.35     ALLOWED
  2026-05-04T15:10:02  api.anthropic.com            → 172.66.146.21    ALLOWED
  2026-05-04T15:10:14  evil-c2.example              → NXDOMAIN          BLOCKED (threat-feed)
```

Reads recent queries via `podman logs` of the CoreDNS sidecar. CoreDNS 1.14.3's `log`
plugin writes to container stdout only — there is no log file to mount.

### `box save`

Snapshot a file from the box back to the host's session state directory so it survives
`agentbox rm`.

```
$ box save /tmp/agent-output.json
saved → ~/.local/share/agentbox/sessions/a3f2c1d4e5b6/saved/agent-output.json
```

The host path is also writable from outside the container — convenient for grabbing
artifacts without exiting the box.

### `box help`

List all `box` subcommands with one-line descriptions.

```
$ box help
box info       Show this box's project, agent, kits, mounts, network, resources
box scratch    cd into a tmpfs scratch directory
box net        Show network policy + recent DNS queries
box save <f>   Snapshot a file to the host so it survives `agentbox rm`
box help       This message
```

---

## Bash completion

`agentbox completion <bash|zsh|fish>` prints a completion script to stdout. Wire it up
per your shell's convention. Completes commands, flags, project_ids, agent names, and kit
names.
