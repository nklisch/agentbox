# KITS

The format and composition rules for agentbox kits. A kit is a layered feature that adds
tooling to the in-box environment. Kits compose: pick a list like `polyglot + containers +
claude` and the build system stacks them into a single image.

## Why kits aren't Dockerfiles

Dockerfiles are great for "build *this* image." They're awkward for "stack feature A on
feature B on feature C." Each Dockerfile has its own `FROM`, its own apt invocations, its
own env munging — composing them means concatenation hacks or multi-stage gymnastics.

A kit is the opposite: a *delta* on top of `base`, expressed as four small files. The CLI
walks a list of kits, resolves dependencies, and generates a single Dockerfile that runs
them all in order. One image, one build, predictable layering, cache-friendly.

If you've used [devcontainer features](https://containers.dev/features), the model is
similar — kits are a personal-scale, podman-native version of the same idea.

## Anatomy

A kit is a directory under `~/.config/agentbox/kits/<name>/` (user) or `<binary>/kits/<name>/`
(built-in). Required files:

```
kits/<name>/
  manifest.toml      # required: name, description, depends_on, etc.
  packages.txt       # required (may be empty): apt packages, one per line
  install.sh         # required (may be a no-op): bash, runs as root at build time
  env.sh             # required (may be empty): sourced in shell rc, exports + PATH only
```

All four files must exist. Empty is fine; missing is an error.

### `manifest.toml`

```toml
name             = "cloud"                    # required, ASCII, [a-z0-9_-]
description      = "AWS, GCP, Azure SDKs + IaC tools"   # required
depends_on       = ["base"]                   # optional, default: ["base"]
agentbox_version = ">=0.1.0"                  # optional, semver constraint
provides         = ["aws-cli", "gcloud", "terraform"]   # optional, free-form labels
conflicts_with   = []                         # optional, list of kit names
```

| Field              | Type     | Required | Notes                                                |
| ------------------ | -------- | -------- | ---------------------------------------------------- |
| `name`             | string   | yes      | Must match the directory name. `[a-z0-9_-]+`.        |
| `description`      | string   | yes      | One-liner shown by `agentbox build --list`.          |
| `depends_on`       | [string] | no       | Other kits that must come first. Defaults to `["base"]`. `base` itself sets `depends_on = []`. |
| `agentbox_version` | string   | no       | Semver constraint. Build fails if CLI version doesn't match. |
| `provides`         | [string] | no       | Free-form labels. `agentbox build --list --provides aws` filters by these. |
| `conflicts_with`   | [string] | no       | If any conflicting kit is in the resolved list, build fails with a clear error. |

### `packages.txt`

```
# Cloud SDKs that are packaged in Debian repos.
# Most cloud SDKs are not — they go in install.sh.

unzip
groff
less
```

- One package per line.
- Lines starting with `#` are comments.
- Blank lines are allowed.
- Installed via `apt-get install -y --no-install-recommends`, batched per kit.

If you don't need any apt packages, the file must still exist (empty or comments only).

### `install.sh`

Runs at build time, as root, after `packages.txt` has been installed. Bash, with `set -e`
prepended automatically.

```bash
#!/usr/bin/env bash
# Install gcloud
curl -fsSL https://sdk.cloud.google.com | bash -s -- --disable-prompts --install-dir=/opt
ln -sf /opt/google-cloud-sdk/bin/gcloud /usr/local/bin/gcloud

# Install terraform
TF_VERSION=1.7.0
curl -fsSL "https://releases.hashicorp.com/terraform/${TF_VERSION}/terraform_${TF_VERSION}_linux_amd64.zip" -o /tmp/tf.zip
unzip -q /tmp/tf.zip -d /usr/local/bin/
rm /tmp/tf.zip
```

**Contract:**

| Aspect                  | Requirement                                                       |
| ----------------------- | ----------------------------------------------------------------- |
| Shebang                 | `#!/usr/bin/env bash`. Other shells not supported.                |
| Executable bit          | Must be set. `chmod +x install.sh`.                               |
| Errors                  | `set -e` is prepended by the build system. Exit non-zero = build fails. |
| Working directory       | The kit's directory inside the build context.                     |
| Env available           | `KIT_NAME`, `KIT_DIR` (kit's directory), `AGENTBOX_VERSION`.       |
| Idempotency             | Should be re-runnable without producing a different result.       |
| Cleanup                 | Should clean its own temp files. `apt-get clean` is run by the build wrapper. |
| Long-running processes  | Forbidden. No daemons, no background services.                    |
| Network                 | Allowed (build time). Pin versions; don't `curl latest`.          |

### `env.sh`

Sourced into `/etc/profile.d/agentbox.sh` (and equivalents for zsh/fish) at build time.
**Exports and PATH manipulation only.** No commands, no functions that do work.

```bash
export GOOGLE_CLOUD_SDK_HOME=/opt/google-cloud-sdk
export PATH="${GOOGLE_CLOUD_SDK_HOME}/bin:${PATH}"
```

**Contract:**

| Aspect                | Requirement                                                       |
| --------------------- | ----------------------------------------------------------------- |
| Idempotency           | Sourcing multiple times must not corrupt PATH or other vars.      |
| No side effects       | No `mkdir`, no daemon starts, no DNS lookups. `export` and `alias` only. |
| Shell compatibility   | Should parse cleanly under bash, zsh, and fish. (In practice: stick to `export VAR=value` and `PATH=...:$PATH`.) |
| Per-shell variants    | If a kit needs zsh-specific or fish-specific env, it can ship `env.zsh` and `env.fish` alongside `env.sh`. The build picks the right one per shell rc. |

## Composition

### Resolution algorithm

Given a requested kit list `[polyglot, containers, claude]`:

1. **Expand dependencies.** For each requested kit, recursively walk `depends_on`. Result:
   `[base, polyglot, containers, claude]` (`base` was implicitly required by all three;
   `claude` also pulls in `node`).
2. **Deduplicate.** Identical kit names collapse to one entry.
3. **Topologically sort.** Each kit comes after everything in its `depends_on` (transitively).
   `base` always comes first.
4. **Conflict check.** For every kit in the resolved list, intersect `conflicts_with` with
   the rest of the list. If non-empty, fail with a clear error naming both kits.

The resolved list (after sort + dedup) is the canonical form. `[claude, polyglot, containers]`
and `[polyglot, containers, claude]` produce the same resolved list, hence the same image.

### Image tag

```
kit_image_tag = "agentbox/" + sha1(joined_resolved_list)[:12]
```

Where `joined_resolved_list` is the resolved list joined with `+`. Example:

```
[base, polyglot, containers, claude]
  → "base+polyglot+containers+claude"
  → sha1 → "8c3f1a2b4d5e..."
  → tag  → "agentbox/8c3f1a2b4d5e"
```

Stable. Same kit list = same tag, regardless of input order.

`agentbox build --print-tag <kit_list>` prints the resolved tag without building — useful
for scripting or computing the corresponding remote tag.

### Generated Dockerfile

```dockerfile
# Generated by agentbox vX.Y.Z. Do not edit; regenerate with `agentbox build --print`.
FROM debian:bookworm-slim

ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl gnupg

# === kit: base ===
COPY kits/base/ /tmp/kit-base/
RUN xargs -a /tmp/kit-base/packages.txt apt-get install -y --no-install-recommends
RUN KIT_NAME=base KIT_DIR=/tmp/kit-base AGENTBOX_VERSION=X.Y.Z bash -e /tmp/kit-base/install.sh
RUN mkdir -p /etc/agentbox/env.d && cp /tmp/kit-base/env.sh /etc/agentbox/env.d/00-base.sh

# === kit: polyglot ===
COPY kits/polyglot/ /tmp/kit-polyglot/
RUN xargs -a /tmp/kit-polyglot/packages.txt apt-get install -y --no-install-recommends
RUN KIT_NAME=polyglot KIT_DIR=/tmp/kit-polyglot AGENTBOX_VERSION=X.Y.Z bash -e /tmp/kit-polyglot/install.sh
RUN cp /tmp/kit-polyglot/env.sh /etc/agentbox/env.d/10-polyglot.sh

# ... (one block per kit, prefixes 00, 10, 20, ... by sort order)

# Final wireup: source all env.d/*.sh from shell rc
RUN echo 'for f in /etc/agentbox/env.d/*.sh; do . "$f"; done' >> /etc/profile.d/agentbox.sh

RUN apt-get clean && rm -rf /var/lib/apt/lists/* /tmp/kit-*

# (no CMD — agentbox runs `sleep infinity` at container create time)
```

Inspect with `agentbox build --print <kit_list>`. The actual generated file is also
written to `~/.local/share/agentbox/cache/kits/<tag>.Dockerfile` on each build.

### Cache

`agentbox build` writes `~/.local/share/agentbox/cache/kits/<tag>.json`:

```json
{
  "tag": "agentbox/8c3f1a2b4d5e",
  "kits": ["base", "polyglot", "containers", "claude"],
  "kit_hashes": {
    "base": "sha1-of-kit-dir-contents",
    "polyglot": "...",
    "containers": "...",
    "claude": "..."
  },
  "built_at": "2026-05-04T14:32:00Z",
  "agentbox_version": "0.1.0"
}
```

On a subsequent `agentbox build` with the same kit list, the CLI re-hashes each kit's
directory and compares. If everything matches, it's a no-op. If any kit changed, it
rebuilds the whole image (no incremental rebuilds — Docker's layer cache covers that, and
mixing both caches just adds confusion).

`agentbox build --no-cache` skips both agentbox's cache and podman's layer cache.

`agentbox build --prune` removes any `agentbox/<tag>` image not referenced by a current
container.

### Registry pull

On a cache miss, `agentbox build` (and `agentbox run` via the same path) tries to pull a
pre-built image from the registry before building locally. The pull path is active when:

- `[registry] enabled = true` (the default).
- `--no-pull` was not passed.
- **Every kit in the resolved list is a built-in** — none are user-authored and none shadow
  a built-in by name. "Built-in kits are read-only; user kits shadow built-ins by name."
  A single user kit anywhere in the resolved list (including one pulled in transitively via
  `depends_on`) disables the pull path for the whole list.

When eligible, the CLI runs `podman pull <host>:<version>-<sha[:12]>`, retags the pulled
image as the canonical local tag (`agentbox/<sha[:12]>`), and writes a cache entry with
`source = "registry"`. Subsequent builds short-circuit on the cache entry — no repeated
network calls.

On any pull failure (404 not found, 401/403 auth, network timeout/DNS, or unknown), the
CLI logs the failure to stderr as `[registry] ...` and falls back to a local build. Pull
failure never aborts the build. Auth failures include a `podman login <host>` suggestion.

Pass `--no-pull` to skip the registry attempt unconditionally and build locally. Useful
when iterating on a custom kit or when you want to force a fresh local build.

#### Pre-published kit images

The project publishes four kit-list compositions to `ghcr.io/nklisch/agentbox-kits` on
every `v*` release, for `linux/amd64` and `linux/arm64`:

| Kit list (pass to `--kits`)   | GHCR nickname                      |
| ----------------------------- | ---------------------------------- |
| `polyglot,containers,claude`  | `polyglot-containers-claude`       |
| `polyglot,containers,codex`   | `polyglot-containers-codex`        |
| `polyglot,claude`             | `polyglot-claude`                  |
| `node,claude`                 | `node-claude`                      |

Each image is published under three tag aliases:

- `<version>-<sha[:12]>` — the CLI's deterministic lookup; immutable.
- `<version>-<nickname>` — versioned alias for human `podman pull`.
- `latest-<nickname>` — rolling alias, reassigned each release.

The source of truth for which compositions are published is
`.github/published-kits.yml`. Adding a new pre-published composition is a one-PR change
to that file. For full design details see
[`docs/features/registry-images.design.md`](features/registry-images.design.md).

## Default kit catalog

Built-in kits, shipped with the binary. All except `base` set `depends_on = ["base"]`
(or to a specific runtime kit, where called out).

| Kit          | Adds                                                                       |
| ------------ | -------------------------------------------------------------------------- |
| `base`       | `depends_on = []`. Shell (zsh + starship), modern CLI replacements (bat, eza, fd, rg, dust, duf, btm, procs), inspection tools (jq, yq, gron, `jgrep`, httpie, hyperfine, tldr, watchexec), git stack (git, git-lfs, delta), forge clients (`gh`, `glab`), build basics (make, tree), zellij, the `box` helpers. The minimum to feel pleasant. |
| `polyglot`   | Node LTS + current (pnpm, bun, deno, yarn), Python (uv, ruff, pyenv), Go (gopls, golangci-lint, delve), Rust (rustup, cargo-watch, sccache), Ruby (rbenv), Java/Kotlin (sdkman, no JDK pre-installed), build tools (make, cmake, ninja, pkg-config), native (clang, lld, gdb, lldb), DB clients (psql, mysql, sqlite3, redis-cli). Chunky (~5GB). |
| `node`       | Node LTS + current, pnpm, bun, deno, yarn. For when polyglot is overkill.  |
| `python`     | uv (project + tool runner), ruff, pyenv.                                   |
| `go`         | Current stable Go toolchain, gopls, golangci-lint, delve.                  |
| `rust`       | rustup (stable + nightly), cargo-watch, sccache.                           |
| `systems`    | clang, lld, cmake, ninja, gdb, lldb, valgrind. For native / FFI work.      |
| `cloud`      | aws-cli, gcloud, az, terraform, kubectl, helm.                             |
| `containers` | Rootless podman, buildah, skopeo, podman-compose, docker-compose. Aliases `docker → podman` and configures the docker-compatibility socket so `docker compose` v2 works. **Requires `containers.enable = true`** in the host config — the kit installs the binaries; the host CLI grants the runtime relaxations (see SPEC.md). |
| `claude`     | `@anthropic-ai/claude-code` (npm) + `claude-mode` static binary (system-prompt wrapper). Implicitly `depends_on = ["base", "node"]`. |
| `codex`      | `@openai/codex` (npm). Implicitly `depends_on = ["base", "node"]`.         |
| `opencode`   | opencode binary. `depends_on = ["base"]`.                                  |

Agent kits (`claude`, `codex`, `opencode`) are intentionally thin — they install the agent
and nothing else. Pair them with a runtime kit (`polyglot` or specific languages) for a
working environment.

The recommended `default_kits` is `["polyglot", "containers", "claude"]` — the everything
kit + nested containers + Claude Code. Trim down to lean alternatives (`["node",
"claude"]`) when you don't need the kitchen sink.

### Note on `containers`: kit alone is not enough

The `containers` kit installs nested rootless podman + the docker aliases inside the box.
For the box's *runtime spec* to actually allow nested containers (the `/dev/fuse` device,
the SETUID/SETGID caps, the looser seccomp profile), you also need:

```toml
[containers]
enable = true
```

If the kit is in your kit list but `enable = false`, you'll get podman installed but inner
operations will fail with mount/permission errors. `agentbox doctor` warns when these two
are out of sync.

## Helpers shipped by the base kit

The `base` kit installs a family of small scripts at `/usr/local/bin/`. They're on `PATH`
inside every box and designed to be discoverable when you `agentbox shell` in.

| Helper | Description | Since |
| ------ | ----------- | ----- |
| `box` | Dispatcher — bare `box` runs `box help`. | v0.1 |
| `box-help` | List all `box` subcommands with one-line descriptions. | v0.1 |
| `box-info` | Print project_id, agent, kits, mounts, network mode, resource limits. | v0.1 |
| `box-net` | Show current network policy + recent DNS queries (safe/allowlist modes). | v0.1 |
| `box-save` | Copy a file from the box to host state dir so it survives `agentbox rm`. | v0.1 |
| `box-scratch` | `cd` into a tmpfs scratch dir at `/tmp/box-scratch` for ephemeral work. | v0.1 |
| `box-agent` | Agent wrapper that drops the pane into `zsh -l` when the agent exits. | v0.2.5 |
| `box-git-watch` | Git status dashboard used in the `focus` layout's git pane. | v0.2.5 |
| `box-diff-watch` | Delta-rendered diff of HEAD vs base branch; used in the `reviewer` layout's diff pane. | v0.3.0 |
| `box-tests-watch` | Project-aware test runner via watchexec; used in the `reviewer` layout's tests pane. | v0.3.0 |
| `box-trail` | Color-coded JSONL trail renderer; used in the `auditor` layout's trail pane. | v0.3.0 |
| `agentbox-hook-record` | Claude Code hook command that writes events to the trail file (`$BOX_TRAIL_FILE`). | v0.3.0 |

The `box-diff-watch`, `box-tests-watch`, `box-trail`, and `agentbox-hook-record` helpers are
wired into the v0.3.0 layout system. See docs/LAYOUTS.md and docs/TRAIL.md for details.

## Authoring a custom kit

1. Create the directory:

   ```sh
   mkdir -p ~/.config/agentbox/kits/myproject
   cd ~/.config/agentbox/kits/myproject
   ```

2. Write the four files:

   ```
   manifest.toml
   packages.txt
   install.sh
   env.sh
   ```

3. Mark `install.sh` executable:

   ```sh
   chmod +x install.sh
   ```

4. Reference it in config:

   ```toml
   default_kits = ["polyglot", "containers", "myproject", "claude"]
   ```

   Or as a one-off:

   ```sh
   agentbox run --kits polyglot,containers,myproject,claude
   ```

5. Build:

   ```sh
   agentbox build polyglot,containers,myproject,claude
   # or just `agentbox run` — it'll build on demand
   ```

### Example: a project-specific extension kit

Adds postgres + redis client tools, a custom CA cert, and an env var.

```
~/.config/agentbox/kits/work/
  manifest.toml
  packages.txt
  install.sh
  env.sh
```

`manifest.toml`:

```toml
name        = "work"
description = "Work-specific extensions: pg/redis clients + internal CA"
depends_on  = ["base"]
```

`packages.txt`:

```
postgresql-client
redis-tools
```

`install.sh`:

```bash
#!/usr/bin/env bash
# Trust the internal CA so curl/git work against internal hosts.
curl -fsSL https://internal-pki.example.com/ca.pem -o /usr/local/share/ca-certificates/internal.crt
update-ca-certificates
```

`env.sh`:

```bash
export INTERNAL_CA_BUNDLE=/etc/ssl/certs/ca-certificates.crt
```

Build & use:

```sh
chmod +x ~/.config/agentbox/kits/work/install.sh
agentbox build polyglot,work,claude
agentbox run --kits polyglot,work,claude
```

## Constraints and gotchas

- **No multi-stage builds.** The composition pipeline is single-stage. If you need
  toolchain isolation, do it inside `install.sh` (download, build, copy artifact, clean up).
- **No build args.** Kits don't take parameters. Variation comes from composing different
  kits, not from passing args. If you find yourself wanting args, that's a sign you should
  split into two kits.
- **No COPY from host.** `install.sh` runs in the build context with access to the kit's
  own directory, nothing else. Need a file from the host? Bake it into the kit directory
  or fetch it over HTTP at build time.
- **Apt cache is cleaned automatically.** Don't run `apt-get clean` in your `install.sh`;
  the wrapper does it once at the end.
- **`base` is special.** It's the only kit with `depends_on = []`, and the build always
  starts from `debian:bookworm-slim` + base. You can author a kit that replaces base
  conceptually, but you can't change the FROM.
- **Built-in kits are read-only.** A user kit with the same name as a built-in shadows the
  built-in. Use this to override behavior. Note: any user kit in the resolved list
  (including one that shadows a built-in) disables the registry pull path — the whole list
  is built locally.
- **Image size adds up.** `polyglot + containers + cloud + claude` is genuinely 6-7GB. The
  cache makes that a one-time cost per kit-list, but it's there. Use lean kits (`node +
  claude` etc.) when you don't need the kitchen sink.
- **Some kits need host-side cooperation.** The `containers` kit is the only built-in
  example today: it needs `containers.enable = true` to function. A kit can't
  unilaterally relax the runtime spec — that's a host-config decision. Custom kits that
  need anything beyond apt + install.sh + env.sh should document the corresponding host
  config explicitly.

## What kits don't do

- **Kits don't run at container start.** `install.sh` runs at *build* time. The container's
  startup is `sleep infinity` — no init, no service supervision, nothing kit-specific.
- **Kits don't configure the agent.** The agent's command line lives in `[agents.<name>]`
  config, not in the kit. The kit just installs the agent binary.
- **Kits don't manage the network.** Network mode is host-side config; the kit doesn't see it.
- **Kits don't persist state.** Anything written by `install.sh` lives in the image and is
  the same for every box that uses it. Per-box state happens at runtime via mounts.
- **Kits don't relax the runtime spec on their own.** Even `containers` needs the host to
  opt in via `containers.enable`. Kits change the *image*; the host CLI controls
  the *runtime*.
