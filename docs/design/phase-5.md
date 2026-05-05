# Design: Phase 5 — Runtime kits + agent kits + agent integration

## Overview

Phase 5 fills out the kit catalog. After this phase, `default_kits = ["polyglot", "containers", "claude"]` (the recipe agentbox ships with) actually works without user overrides. Ten new built-in kits land:

- **Single-language runtime kits (Part A):** `node`, `python`, `go`, `rust`, `systems`, `cloud`. Each is a focused install (Node LTS + current, Python with uv/ruff/pyenv, etc.). Buildable in isolation, composable with anything.
- **Polyglot kit (Part B):** the kitchen-sink kit. Self-contained: doesn't depend on the single-language kits — it installs the union directly so users who pick polyglot get one chunky kit instead of seven. Conflicts with the single-lang kits to keep the resolver honest.
- **Agent kits (Part C):** `claude`, `codex`, `opencode`. Each is small (just installs the agent binary). The implementer verifies each agent CLI's permission-bypass flag and updates `[agents.<name>].cmd` accordingly.

The infrastructure for all of this is already built: Phase 2's kit pipeline parses, resolves, hashes, generates Dockerfiles, and builds images; Phase 4's zellij layout reads `cfg.Agents[agent].Cmd` for the agent pane's command. Phase 5 is mostly kit content — bash scripts, manifests, package lists. **No new Go code is required.** (Verified: the resolver doesn't use `provides`, but the design works around that by accepting node-installed-twice in the polyglot+claude composition.)

## Cross-cutting decisions

- **Pin every tool version via env-vars at the top of install.sh.** Same pattern as base kit's install.sh: `FOO_VERSION="${FOO_VERSION:-X.Y.Z}"`. The implementer **verifies each version + URL at write time** by hitting the upstream release API; this design specifies the structure and tool list, not specific versions.
- **Architecture detection:** every install.sh starts with the same `uname -m` switch from base kit's install.sh — `x86_64` → `amd64` (Go arch) and `x86_64-unknown-linux-gnu` (Rust triple); `aarch64` → `arm64` and `aarch64-unknown-linux-gnu`. Other archs error out. Copy this verbatim from base — no helper extraction (each kit is self-contained per KITS.md "No COPY from host").
- **`provides` is not used by the resolver in Phase 5.** Phase 2 stores the field on Manifest but doesn't act on it. Polyglot's manifest still declares `provides = ["node", "python", "go", "rust", "systems"]` for documentation, but resolver semantics are unchanged: claude pulls `node` separately even when polyglot is in the resolved set.
- **`polyglot` + `claude` overlap:** when both are requested, the resolved kit list is `[base, node, polyglot, claude]`. Node's install.sh runs first, then polyglot's install.sh re-installs node (apt will say "already installed"; npm globals are idempotent). Disk overhead ~50-100MB; correctness preserved. Documented in PROGRESS.md as a known cost.
- **Conflicts on polyglot:** `conflicts_with = ["node", "python", "go", "rust", "systems"]`. Using polyglot AND any single-language kit explicitly errors at resolve time — they'd duplicate without value. (The polyglot+claude case is exempt because claude's `depends_on` brings node implicitly; the conflict check fires only on the user's *requested* set after walk + dedup, which includes the implicit deps. **Verify this resolver behavior**: if conflicts fire on the depends_on-walked set, polyglot+claude breaks. If they fire only on the originally-requested set, it works. Read `internal/kits/resolve.go` carefully before committing.)
  - **Resolver-behavior fallback:** if conflicts fire on the walked set and break polyglot+claude, drop the conflicts_with for `node` only. Keep conflicts for python/go/rust/systems. claude bypass'd through node is the only legitimate composition we need.
- **No agent process inside the kits.** The kit installs the agent binary; the *runtime* runs it via zellij's `command "<agent.cmd[0]>"` field. Phase 4 already wires this. So agent kits' env.sh is empty (no `command` exports needed).

---

## Part A — Single-language runtime kits (Units 1–6)

Each Part-A kit follows this exact file layout:

```
internal/builtinkits/kits/<name>/
  manifest.toml      # depends_on = ["base"]
  packages.txt       # apt-installable subset (may be empty)
  install.sh         # pinned versions + arch detect + per-tool sections
  env.sh             # PATH/export-only per KITS.md
```

All bash scripts must have the executable bit set in the source tree
(`chmod +x install.sh`). Verify with `git ls-files --stage` showing `100755`.

### Unit 1: `node` kit

**File**: `internal/builtinkits/kits/node/`

#### `manifest.toml`
```toml
name        = "node"
description = "Node LTS + current, with pnpm, bun, deno, yarn"
depends_on  = ["base"]
provides    = ["node"]   # documentation only — resolver ignores
```

#### `packages.txt`
```
ca-certificates
gnupg
```
(NodeSource's apt repo handles the rest.)

#### `install.sh` structure
- Pinned versions at top (env-var overridable):
  - `NODE_LTS_VERSION` (e.g., 20)
  - `NODE_CURRENT_VERSION` (e.g., 22 or 23 — verify what NodeSource ships)
  - `BUN_VERSION` (from oven-sh/bun GitHub releases)
  - `DENO_VERSION` (from denoland/deno GitHub releases)
  - `PNPM_VERSION` and `YARN_VERSION` (npm-installable, latest is fine — pin if reproducibility matters)
- Sections in order:
  1. **NodeSource setup** for LTS: `curl -fsSL https://deb.nodesource.com/setup_${NODE_LTS_VERSION}.x | bash -` then `apt-get install -y nodejs`. This makes `node` and `npm` available.
  2. **`nodenv`-style additional Node version** (current line): install via NodeSource's separate repo OR `n` (npm package: `npm install -g n && n ${NODE_CURRENT_VERSION}`). Recommend `n` for simplicity; document where each version lives (`/usr/local/bin/node` is the LTS; current Node is at `/usr/local/n/versions/node/<ver>/bin/node`). Symlink `node-current` to the current version's binary if useful.
  3. **pnpm**: `npm install -g pnpm@${PNPM_VERSION}` (or `corepack enable && corepack prepare pnpm@latest --activate`).
  4. **yarn**: `npm install -g yarn@${YARN_VERSION}` (or via corepack).
  5. **bun**: `curl -fsSL https://bun.sh/install | bash` ── installs to `~/.bun/bin/bun`. Move to `/usr/local/bin/bun` so it's on PATH for all users.
  6. **deno**: GitHub release download (`deno-x86_64-unknown-linux-gnu.zip`); extract to `/usr/local/bin/deno`.

#### `env.sh`
```bash
# node kit env. Exports/PATH only.
export PATH="/usr/local/bin:${PATH}"  # already in PATH via base; no-op but explicit
# bun + deno install to /usr/local/bin via install.sh; no extra PATH needed.
```

**Acceptance Criteria**:
- [ ] `agentbox build node` exits 0.
- [ ] In the resulting image: `node --version`, `npm --version`, `pnpm --version`, `yarn --version`, `bun --version`, `deno --version` all succeed.
- [ ] `chmod +x install.sh` set in source tree.

---

### Unit 2: `python` kit

**File**: `internal/builtinkits/kits/python/`

#### `manifest.toml`
```toml
name        = "python"
description = "Python with uv (project + tool runner), ruff, pyenv"
depends_on  = ["base"]
provides    = ["python"]
```

#### `packages.txt`
```
python3
python3-venv
python3-pip
python3-dev
build-essential
libssl-dev
zlib1g-dev
libbz2-dev
libreadline-dev
libsqlite3-dev
libffi-dev
liblzma-dev
```
(pyenv compiles Python from source; needs the dev headers.)

#### `install.sh` structure
- Pinned versions: `UV_VERSION`, `RUFF_VERSION`, `PYENV_VERSION` (or use `git clone --branch`)
- Sections:
  1. **uv**: download from astral-sh/uv release (`uv-x86_64-unknown-linux-gnu.tar.gz`). Extract `uv` and `uvx` to `/usr/local/bin/`.
  2. **ruff**: download from astral-sh/ruff release. `ruff-x86_64-unknown-linux-gnu.tar.gz` → `/usr/local/bin/ruff`.
  3. **pyenv**: `git clone https://github.com/pyenv/pyenv.git /opt/pyenv` (pin via `--branch v${PYENV_VERSION}`).

#### `env.sh`
```bash
export PYENV_ROOT="/opt/pyenv"
export PATH="${PYENV_ROOT}/bin:${PATH}"
```
(Don't `eval "$(pyenv init -)"` — that's a command, violates env.sh contract. Users who want pyenv shims source it themselves; the binary is on PATH.)

**Acceptance Criteria**:
- [ ] `agentbox build python` exits 0.
- [ ] `python3 --version`, `uv --version`, `ruff --version`, `pyenv --version` all succeed.

---

### Unit 3: `go` kit

**File**: `internal/builtinkits/kits/go/`

#### `manifest.toml`
```toml
name        = "go"
description = "Go toolchain + gopls + golangci-lint + delve"
depends_on  = ["base"]
provides    = ["go"]
```

#### `packages.txt`
```
ca-certificates
git
```

#### `install.sh` structure
- Pinned versions: `GO_VERSION` (e.g. 1.25.0), `GOLANGCI_LINT_VERSION`, `DELVE_VERSION`
- Sections:
  1. **Go toolchain**: download `go${GO_VERSION}.linux-${ARCH}.tar.gz` from `go.dev/dl/`, extract to `/usr/local/go`, ensure `/usr/local/go/bin` on PATH (via env.sh).
  2. **gopls**: `GOPATH=/opt/go GOBIN=/usr/local/bin /usr/local/go/bin/go install golang.org/x/tools/gopls@latest`. Pin commit/version env-var if reproducibility matters; default `@latest`.
  3. **golangci-lint**: install via official script `curl -fsSL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh | sh -s -- -b /usr/local/bin v${GOLANGCI_LINT_VERSION}`.
  4. **delve**: `go install github.com/go-delve/delve/cmd/dlv@v${DELVE_VERSION}`. Move resulting binary into `/usr/local/bin/dlv` if needed.

#### `env.sh`
```bash
export PATH="/usr/local/go/bin:${PATH}"
export GOPATH="${GOPATH:-/opt/go}"
export GOBIN="${GOBIN:-/usr/local/bin}"
```

**Acceptance Criteria**:
- [ ] `agentbox build go` exits 0.
- [ ] `go version`, `gopls -h`, `golangci-lint --version`, `dlv version` succeed.

---

### Unit 4: `rust` kit

**File**: `internal/builtinkits/kits/rust/`

#### `manifest.toml`
```toml
name        = "rust"
description = "rustup with stable + nightly toolchains, cargo-watch, sccache"
depends_on  = ["base"]
provides    = ["rust"]
```

#### `packages.txt`
```
ca-certificates
build-essential
pkg-config
libssl-dev
```

#### `install.sh` structure
- Pinned versions: `RUST_TOOLCHAIN_STABLE` (defaults to `stable`), `RUST_TOOLCHAIN_NIGHTLY` (defaults to `nightly`), `CARGO_WATCH_VERSION`, `SCCACHE_VERSION`
- Sections:
  1. **rustup**: `curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y --profile minimal --default-toolchain ${RUST_TOOLCHAIN_STABLE}`. Install to `/opt/rust` (set `RUSTUP_HOME=/opt/rust` and `CARGO_HOME=/opt/rust` before running).
  2. **nightly**: `rustup toolchain install ${RUST_TOOLCHAIN_NIGHTLY}`.
  3. **cargo-watch + sccache**: `cargo install cargo-watch@${CARGO_WATCH_VERSION} sccache@${SCCACHE_VERSION}`. Cargo binaries land in `/opt/rust/bin/`.

#### `env.sh`
```bash
export RUSTUP_HOME="/opt/rust"
export CARGO_HOME="/opt/rust"
export PATH="/opt/rust/bin:${PATH}"
```

**Acceptance Criteria**:
- [ ] `agentbox build rust` exits 0.
- [ ] `rustc --version`, `cargo --version`, `rustup show`, `cargo-watch --version`, `sccache --version` succeed.

---

### Unit 5: `systems` kit

**File**: `internal/builtinkits/kits/systems/`

#### `manifest.toml`
```toml
name        = "systems"
description = "C/C++ toolchain: clang, lld, cmake, ninja, gdb, lldb, valgrind"
depends_on  = ["base"]
provides    = ["systems"]
```

#### `packages.txt`
```
clang
lld
lldb
cmake
ninja-build
gdb
valgrind
pkg-config
build-essential
```
(Everything is in apt for Debian Bookworm.)

#### `install.sh`
Empty (`set -euo pipefail; echo "systems kit: nothing extra to install"; exit 0`). All tools come from packages.txt.

#### `env.sh`
```bash
# systems kit env. Empty — apt handles PATH for /usr/bin/clang etc.
```

**Acceptance Criteria**:
- [ ] `agentbox build systems` exits 0.
- [ ] `clang --version`, `lld --version`, `cmake --version`, `ninja --version`, `gdb --version`, `lldb --version`, `valgrind --version` succeed.

---

### Unit 6: `cloud` kit

**File**: `internal/builtinkits/kits/cloud/`

#### `manifest.toml`
```toml
name        = "cloud"
description = "AWS, GCP, Azure CLIs + Terraform + kubectl + Helm"
depends_on  = ["base"]
provides    = ["cloud"]
```

#### `packages.txt`
```
ca-certificates
curl
gnupg
unzip
groff
less
```

#### `install.sh` structure
- Pinned versions: `AWSCLI_VERSION`, `TERRAFORM_VERSION`, `KUBECTL_VERSION`, `HELM_VERSION`. (gcloud and az use their own apt repos, so versions track those repos' "latest" by default.)
- Sections:
  1. **aws-cli**: download `awscli-exe-linux-${ARCH}-${AWSCLI_VERSION}.zip` from `awscli.amazonaws.com`, unzip, run `./aws/install --bin-dir /usr/local/bin --install-dir /usr/local/aws-cli --update`.
  2. **gcloud**: add Google Cloud SDK apt repo (`echo 'deb https://packages.cloud.google.com/apt cloud-sdk main' > /etc/apt/sources.list.d/google-cloud-sdk.list`; import key); `apt-get update && apt-get install -y google-cloud-cli`.
  3. **az**: install Microsoft's apt repo + `azure-cli` package per their official Debian docs.
  4. **terraform**: download `terraform_${TERRAFORM_VERSION}_linux_${ARCH}.zip` from `releases.hashicorp.com`, unzip → `/usr/local/bin/terraform`.
  5. **kubectl**: `curl -L https://dl.k8s.io/release/v${KUBECTL_VERSION}/bin/linux/${ARCH}/kubectl -o /usr/local/bin/kubectl && chmod +x /usr/local/bin/kubectl`.
  6. **helm**: `curl https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | DESIRED_VERSION=v${HELM_VERSION} bash`.

#### `env.sh`
```bash
# cloud kit env. CLIs land in /usr/local/bin which is already on PATH.
```

**Acceptance Criteria**:
- [ ] `agentbox build cloud` exits 0.
- [ ] `aws --version`, `gcloud --version`, `az --version`, `terraform version`, `kubectl version --client`, `helm version` succeed.

---

## Part B — Polyglot kit (Unit 7)

### Unit 7: `polyglot` kit

**File**: `internal/builtinkits/kits/polyglot/`

#### `manifest.toml`
```toml
name        = "polyglot"
description = "Everything kit: Node + Python + Go + Rust + Ruby + Java/Kotlin (sdkman) + build tools + DB clients. Chunky (~3-4GB)."
depends_on  = ["base"]
provides    = ["node", "python", "go", "rust", "systems"]   # documentation only
conflicts_with = ["python", "go", "rust", "systems"]
# NOTE: "node" is intentionally NOT in conflicts_with so claude's depends_on=["base","node"]
# can compose with polyglot. See cross-cutting decision in design overview.
```

#### `packages.txt`
Union of node + python + go + rust + systems packages plus DB clients:
```
# node
ca-certificates
gnupg
# python (pyenv build deps)
python3
python3-venv
python3-pip
python3-dev
build-essential
libssl-dev
zlib1g-dev
libbz2-dev
libreadline-dev
libsqlite3-dev
libffi-dev
liblzma-dev
# go
git
# rust
pkg-config
# systems
clang
lld
lldb
cmake
ninja-build
gdb
valgrind
# DB clients
postgresql-client
default-mysql-client
sqlite3
redis-tools
# ruby (rbenv build deps)
autoconf
patch
libyaml-dev
libgmp-dev
libgdbm-dev
libgdbm-compat-dev
# java/kotlin via sdkman — needs zip, unzip, curl
zip
unzip
```

#### `install.sh` structure
The implementer assembles this by **inlining** the install steps from each Part-A kit (don't `source` them — KITS.md forbids COPY-from-host and the install runs in a single context). Order matters:

1. **Architecture detection** (same block as Part A kits).
2. **Pinned versions** for every tool the kit installs (Node, bun, deno, uv, ruff, pyenv, Go, golangci-lint, delve, rustup toolchains, cargo-watch, sccache, rbenv, sdkman) — all env-var overridable.
3. **Node section** (NodeSource + n + pnpm + yarn + bun + deno) — same as Unit 1.
4. **Python section** (uv + ruff + pyenv) — same as Unit 2.
5. **Go section** (toolchain + gopls + golangci-lint + delve) — same as Unit 3.
6. **Rust section** (rustup + nightly + cargo-watch + sccache) — same as Unit 4.
7. **Ruby section**: `git clone --branch v${RBENV_VERSION} https://github.com/rbenv/rbenv.git /opt/rbenv`. Don't pre-install a Ruby — KITS.md says "Ruby (rbenv)" not "Ruby N.M with rbenv".
8. **Java/Kotlin via sdkman**: `curl -s https://get.sdkman.io | bash`. Install to `/opt/sdkman`. **No JDK pre-installed** per KITS.md ("sdkman, no JDK pre-installed").
9. (No DB-client section — they came from packages.txt above.)

#### `env.sh`
Combine the export blocks from Part A kits, plus rbenv and sdkman:
```bash
# Common
export PATH="/usr/local/bin:${PATH}"

# python
export PYENV_ROOT="/opt/pyenv"
export PATH="${PYENV_ROOT}/bin:${PATH}"

# go
export PATH="/usr/local/go/bin:${PATH}"
export GOPATH="${GOPATH:-/opt/go}"
export GOBIN="${GOBIN:-/usr/local/bin}"

# rust
export RUSTUP_HOME="/opt/rust"
export CARGO_HOME="/opt/rust"
export PATH="/opt/rust/bin:${PATH}"

# ruby via rbenv
export RBENV_ROOT="/opt/rbenv"
export PATH="${RBENV_ROOT}/bin:${PATH}"

# java/kotlin via sdkman
export SDKMAN_DIR="/opt/sdkman"
# Note: sdkman wants `source $SDKMAN_DIR/bin/sdkman-init.sh` for activation, but
# that's a command, not an export — violates env.sh contract. Users source it
# themselves in their interactive shell if they need sdkman shims. The PATH
# below covers the most common case (any binary `sdk` lives at $SDKMAN_DIR/bin).
export PATH="${SDKMAN_DIR}/bin:${PATH}"
```

**Implementation Notes**:
- This install.sh will be the longest single file in the project. ~400-500 lines. Worth it: it's the headline kit for the default config.
- Image size will land around 4-5GB. KITS.md says "polyglot + containers + cloud + claude is genuinely 6-7GB." Phase 5 should measure and document.
- Don't try to share code across kits. Each kit's directory is its own build context (KITS.md: "No COPY from host"); duplication is the format's choice.

**Acceptance Criteria**:
- [ ] `agentbox build polyglot` exits 0 (slow first time — 10-20 min on a typical laptop).
- [ ] In the resulting image: `node`, `npm`, `pnpm`, `yarn`, `bun`, `deno`, `python3`, `uv`, `ruff`, `pyenv`, `go`, `gopls`, `golangci-lint`, `dlv`, `rustc`, `cargo`, `cargo-watch`, `sccache`, `rbenv`, `psql`, `mysql`, `sqlite3`, `redis-cli`, `clang`, `cmake`, `ninja`, `gdb`, `lldb`, `valgrind`, `sdk` — all on PATH and runnable.
- [ ] `agentbox build polyglot,claude` exits 0 (should be a near-no-op for polyglot since it's cached, claude builds on top with its own node-install duplication).

---

## Part C — Agent kits + integration (Units 8–11)

### Unit 8: `claude` kit

**File**: `internal/builtinkits/kits/claude/`

#### `manifest.toml`
```toml
name        = "claude"
description = "Claude Code (@anthropic-ai/claude-code via npm)"
depends_on  = ["base", "node"]
```

#### `packages.txt`
Empty. Node comes from the depends_on; nothing else needed at apt level.

#### `install.sh`
```bash
#!/usr/bin/env bash
set -euo pipefail

# Verify package name + binary name at write time. The package is currently
# documented as @anthropic-ai/claude-code on npm; the binary is `claude`.
CLAUDE_VERSION="${CLAUDE_VERSION:-latest}"
npm install -g "@anthropic-ai/claude-code@${CLAUDE_VERSION}"

# Sanity-check the binary is on PATH (npm globals land in /usr/local/lib + /usr/local/bin
# when the node kit's install.sh sets npm prefix to /usr/local).
command -v claude >/dev/null || { echo "claude binary not on PATH after install"; exit 1; }
```

#### `env.sh`
Empty. The Anthropic API key flows in as `ANTHROPIC_API_KEY` via Phase 1's secrets passthrough; nothing for env.sh to declare.

**Acceptance Criteria**:
- [ ] `agentbox build claude` exits 0 (node dep walks in).
- [ ] `which claude` and `claude --version` succeed in the resulting image.
- [ ] Default config's `[agents.claude].cmd = ["claude", "--dangerously-skip-permissions"]` is unchanged (Phase 1's value is correct — the implementer should re-confirm by running `claude --help` against the freshly-built image to ensure the flag is still spelled this way).

---

### Unit 9: `codex` kit

**File**: `internal/builtinkits/kits/codex/`

#### `manifest.toml`
```toml
name        = "codex"
description = "OpenAI Codex (@openai/codex via npm)"
depends_on  = ["base", "node"]
```

#### `packages.txt`
Empty.

#### `install.sh`
```bash
#!/usr/bin/env bash
set -euo pipefail

# Verify the npm package name and binary name at write time:
#   - npm view @openai/codex
#   - which codex (after install)
# If the package is named differently (e.g. "openai-codex" or "openai-cli"),
# update accordingly.
CODEX_VERSION="${CODEX_VERSION:-latest}"
npm install -g "@openai/codex@${CODEX_VERSION}"

command -v codex >/dev/null || { echo "codex binary not on PATH after install"; exit 1; }
```

#### `env.sh`
Empty.

**Implementation Notes — YOLO flag verification:**
After install, the implementer should run `podman run --rm <image> codex --help` (or check upstream docs at the time of writing) to identify Codex's permission-bypass flag. Update `internal/config/config.go`'s `DefaultConfig()` accordingly:

```go
"codex": {
    Kits: []string{"polyglot", "codex"},
    Cmd:  []string{"codex", "<the-correct-flag>"},  // update from "codex"
},
```

If Codex doesn't ship a permission-bypass flag (e.g., it prompts unconditionally, or it has a config-file knob instead of a flag), document that in PROGRESS.md and leave `cmd = ["codex"]` as-is. Phase 5 still ships, just without auto-YOLO for codex. The user can configure it manually.

**Acceptance Criteria**:
- [ ] `agentbox build codex` exits 0.
- [ ] `which codex` and `codex --version` (or `codex --help`) succeed in the resulting image.
- [ ] If the YOLO flag exists, default config's `[agents.codex].cmd` is updated.
- [ ] If the YOLO flag does not exist, PROGRESS.md documents the situation.

---

### Unit 10: `opencode` kit

**File**: `internal/builtinkits/kits/opencode/`

#### `manifest.toml`
```toml
name        = "opencode"
description = "opencode (sst/opencode binary)"
depends_on  = ["base"]
```

#### `packages.txt`
```
curl
ca-certificates
unzip
```

#### `install.sh`
```bash
#!/usr/bin/env bash
set -euo pipefail

# Verify the install method at write time. As of v0.1, opencode publishes
# release binaries at github.com/sst/opencode/releases. Pin to a specific
# tag for reproducibility.
OPENCODE_VERSION="${OPENCODE_VERSION:-0.13.0}"

ARCH="$(uname -m)"
case "$ARCH" in
  x86_64)  PLATFORM="linux-x64" ;;
  aarch64) PLATFORM="linux-arm64" ;;
  *) echo "unsupported arch: $ARCH" >&2; exit 1 ;;
esac

curl -fsSL "https://github.com/sst/opencode/releases/download/v${OPENCODE_VERSION}/opencode-${PLATFORM}.zip" \
  -o /tmp/opencode.zip
unzip -q /tmp/opencode.zip -d /tmp/opencode-extract
install -m 0755 /tmp/opencode-extract/opencode /usr/local/bin/opencode
rm -rf /tmp/opencode.zip /tmp/opencode-extract

command -v opencode >/dev/null || { echo "opencode binary not on PATH after install"; exit 1; }
```

#### `env.sh`
Empty.

**Implementation Notes — YOLO flag verification:**
Same drill as codex. Run `opencode --help` against the freshly-built image; update `internal/config/config.go`'s `DefaultConfig()` to add:

```go
"opencode": {
    Kits: []string{"polyglot", "opencode"},
    Cmd:  []string{"opencode", "<the-correct-flag>"},  // or just ["opencode"] if no flag
},
```

(Phase 1's `DefaultConfig()` doesn't currently include an `opencode` agents entry — Phase 5 adds one.)

**Acceptance Criteria**:
- [ ] `agentbox build opencode` exits 0.
- [ ] `which opencode` and `opencode --version` (or `--help`) succeed.
- [ ] DefaultConfig has `Agents["opencode"]` populated.

---

### Unit 11: Default config — `Agents["opencode"]` entry

**File**: `internal/config/config.go` (edit existing)

In `DefaultConfig()`'s `Agents` map, add the opencode entry alongside claude and codex:

```go
Agents: map[string]Agent{
    "claude": {
        Kits: []string{"polyglot", "claude"},
        Cmd:  []string{"claude", "--dangerously-skip-permissions"},
    },
    "codex": {
        Kits: []string{"polyglot", "codex"},
        Cmd:  []string{"codex" /* + verified YOLO flag, or just "codex" */},
    },
    "opencode": {
        Kits: []string{"polyglot", "opencode"},
        Cmd:  []string{"opencode" /* + verified YOLO flag, or just "opencode" */},
    },
},
```

If the codex/opencode YOLO flags are verified, update `Cmd` accordingly. Same goes for whatever kit list each agent should default to (currently mirrors claude's pattern of `["polyglot", "<agent>"]`).

**Implementation Notes**:
- `internal/config/config_test.go` has tests asserting DefaultConfig values. Update assertions to cover the new opencode entry.
- This is the only Go-level change in Phase 5.

**Acceptance Criteria**:
- [ ] `agentbox config show --json | jq '.agents | keys'` includes `"opencode"`.
- [ ] Existing config tests pass; new test covers the opencode default.

---

## Implementation Order

| Layer | Units | Notes |
|-------|-------|-------|
| **Part A — single-language kits** | 1 (node), 2 (python), 3 (go), 4 (rust), 5 (systems), 6 (cloud) | Independent; can run in parallel. Each kit must verify versions + URLs at write time. |
| **Part B — polyglot** | 7 | After Part A so the implementer has known-working installs to copy from. |
| **Part C — agent kits + integration** | 8 (claude), 9 (codex), 10 (opencode), 11 (config defaults) | Each agent kit's install is small; verification of YOLO flags is the time-consuming part. |

**Implement-orchestrator suggestion:** three Sonnet agents in sequence:
- Agent 1: Part A (6 kits, parallel-safe within the agent's session).
- Agent 2: Part B (polyglot).
- Agent 3: Part C (3 agent kits + config edit).

OR two Sonnet agents:
- Agent 1: Part A + Part B (heavy on bash + version verification).
- Agent 2: Part C (mostly small kits + Go config edit + YOLO verification).

Single-agent is also defensible if context fits — the patterns are extremely repetitive after the first kit lands.

---

## Verification Checklist

```sh
cd /home/nathan/dev/agent-box
go vet ./...
go test ./...                                         # config tests cover new opencode entry
go build ./...
make build

# Build every Part-A kit individually.
for K in node python go rust systems cloud; do
  echo "=== building $K ==="
  ./agentbox build "$K" 2>&1 | tail -3
done

# Build polyglot — slow.
./agentbox build polyglot 2>&1 | tail -3

# Build each agent + polyglot composition.
./agentbox build polyglot,claude 2>&1 | tail -3
./agentbox build polyglot,codex 2>&1 | tail -3
./agentbox build polyglot,opencode 2>&1 | tail -3

# Verify each kit's tools work in the resulting images.
for K in node python go rust systems cloud; do
  TAG=$(./agentbox build --print "$K" | grep -oP 'agentbox/[a-f0-9]+' | head -1)
  echo "=== $K image: $TAG ==="
  case $K in
    node)    podman run --rm "localhost/$TAG" zsh -lc 'node --version; pnpm --version; bun --version; deno --version' ;;
    python)  podman run --rm "localhost/$TAG" zsh -lc 'python3 --version; uv --version; ruff --version; pyenv --version' ;;
    go)      podman run --rm "localhost/$TAG" zsh -lc 'go version; gopls -h | head -1; golangci-lint --version; dlv version' ;;
    rust)    podman run --rm "localhost/$TAG" zsh -lc 'rustc --version; cargo --version; cargo-watch --version; sccache --version' ;;
    systems) podman run --rm "localhost/$TAG" zsh -lc 'clang --version; cmake --version; ninja --version; gdb --version' ;;
    cloud)   podman run --rm "localhost/$TAG" zsh -lc 'aws --version; terraform version; kubectl version --client; helm version' ;;
  esac
done

# Verify claude binary is reachable in the polyglot+claude image.
TAG=$(./agentbox build --print polyglot,claude | grep -oP 'agentbox/[a-f0-9]+' | head -1)
podman run --rm "localhost/$TAG" zsh -lc 'which claude && claude --version'

# Default-config end-to-end — no .agentbox.toml override.
mkdir -p /tmp/abx-default && cd /tmp/abx-default && touch x
ANTHROPIC_API_KEY=sk-test /home/nathan/dev/agent-box/agentbox run --no-attach
/home/nathan/dev/agent-box/agentbox exec . sh -c 'which claude && claude --version'
/home/nathan/dev/agent-box/agentbox rm .
rm -rf /tmp/abx-default

# Image-size sanity (informational; record in PROGRESS.md).
podman images | grep '^localhost/agentbox/' | awk '{print $1, $4, $5}'
```

The `agentbox run` smoke test in the test checkpoint that uses `expect` to drive an interactive zellij+claude session is for **manual verification**, not the orchestrator's automated checks (zellij needs a real TTY).

Phase 5 is "done" for autopilot purposes when:
- Every Part-A kit builds and its tools are reachable.
- Polyglot builds.
- Each agent kit builds in composition with polyglot.
- The default-config `agentbox run --no-attach` flow succeeds without any user override.
- Codex and opencode YOLO flags are documented (either verified-and-applied or noted-as-unavailable in PROGRESS.md).
