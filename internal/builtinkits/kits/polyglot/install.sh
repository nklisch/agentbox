#!/usr/bin/env bash
# polyglot kit installer: Node + Python + Go + Rust + Ruby (rbenv) + Java/Kotlin (sdkman)
# + build tools + DB clients. This is the headline kit for the default agentbox config.
#
# All tool versions are pinned at the top for reproducible rebuilds.
# Versions verified against upstream release APIs on 2026-05-05 (copied from Part A kits
# which were also verified on 2026-05-05). Only rbenv is a new pin verified today.
#
# This file inlines the install logic from each Part A kit rather than sourcing them —
# KITS.md forbids COPY-from-host and each kit's directory is its own build context.
# Duplication is the format's intended approach.
set -euo pipefail

# =============================================================================
# PINNED VERSIONS (env-var overridable)
# =============================================================================

# --- Node (from node kit — verified 2026-05-05) ---
# Node LTS major: 24 (codename "Krypton", became LTS Oct 2025, EOL April 2028).
# Node current: 25 (odd-number releases are not LTS; latest on nodejs.org: v25.9.0).
NODE_LTS_MAJOR="${NODE_LTS_MAJOR:-24}"
NODE_CURRENT_MAJOR="${NODE_CURRENT_MAJOR:-25}"
# pnpm + yarn via corepack — no explicit version pin; corepack prepare @latest fetches current.
BUN_VERSION="${BUN_VERSION:-1.3.13}"    # oven-sh/bun — v-prefix stripped from tag bun-v1.3.13
DENO_VERSION="${DENO_VERSION:-2.7.14}"  # denoland/deno — v-prefix stripped from tag v2.7.14

# --- Python (from python kit — verified 2026-05-05) ---
UV_VERSION="${UV_VERSION:-0.11.9}"         # astral-sh/uv — v-prefix absent in tag
RUFF_VERSION="${RUFF_VERSION:-0.15.12}"    # astral-sh/ruff — v-prefix absent in tag
PYENV_VERSION="${PYENV_VERSION:-2.6.28}"   # pyenv/pyenv — v-prefix stripped from tag v2.6.28

# --- Go (from go kit — verified 2026-05-05) ---
GO_VERSION="${GO_VERSION:-1.26.2}"                        # go.dev/VERSION?m=text
GOLANGCI_LINT_VERSION="${GOLANGCI_LINT_VERSION:-2.12.1}" # golangci/golangci-lint — v-prefix stripped
DELVE_VERSION="${DELVE_VERSION:-1.26.3}"                  # go-delve/delve — v-prefix stripped
# gopls installed @latest; no pin — it tracks the toolchain release cycle

# --- Rust (from rust kit — verified 2026-05-05) ---
# RUST_TOOLCHAIN_STABLE and RUST_TOOLCHAIN_NIGHTLY are channel names, not point releases.
# Rust's 6-week cadence makes channel names the correct handle; use `rustup update` in
# the container to get the latest patch on a given channel.
RUST_TOOLCHAIN_STABLE="${RUST_TOOLCHAIN_STABLE:-stable}"
RUST_TOOLCHAIN_NIGHTLY="${RUST_TOOLCHAIN_NIGHTLY:-nightly}"
CARGO_WATCH_VERSION="${CARGO_WATCH_VERSION:-8.5.3}"  # watchexec/cargo-watch tag v8.5.3
SCCACHE_VERSION="${SCCACHE_VERSION:-0.15.0}"          # mozilla/sccache tag v0.15.0

# --- Ruby via rbenv (NEW — verified 2026-05-05) ---
RBENV_VERSION="${RBENV_VERSION:-1.3.2}"  # rbenv/rbenv tag v1.3.2 — latest as of 2026-05-05

# --- Java/Kotlin via sdkman (NEW) ---
# sdkman is installed via get.sdkman.io which always pulls the current stable version.
# No version pin in the URL — the installer is the canonical distribution point and
# pinning a specific sdkman version here would quickly become stale. This is intentional.
SDKMAN_DIR="${SDKMAN_DIR:-/opt/sdkman}"

# =============================================================================
# ARCHITECTURE DETECTION
# =============================================================================
# Copied verbatim from base kit's install.sh — each kit is self-contained per KITS.md.
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64)
    RUST_TRIPLE="x86_64-unknown-linux-gnu"
    GO_ARCH="amd64"
    BUN_ARCH="x64"
    ;;
  aarch64)
    RUST_TRIPLE="aarch64-unknown-linux-gnu"
    GO_ARCH="arm64"
    BUN_ARCH="aarch64"
    ;;
  *)
    echo "unsupported arch: $ARCH" >&2
    exit 1
    ;;
esac

# =============================================================================
# TEMP DIRECTORY (shared across all sections)
# =============================================================================
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# =============================================================================
# ZSH ENV.D BRIDGE (idempotent belt-and-braces)
# =============================================================================
# base kit now provides this bridge (commit 18473b1), but we include the same
# idempotent guard here as belt-and-braces — polyglot runs after base, so this
# is a no-op in practice but safe to have.
mkdir -p /etc/zsh
if ! grep -q '/etc/agentbox/env.d' /etc/zsh/zshenv 2>/dev/null; then
    cat >> /etc/zsh/zshenv <<'ZSHENV'
# agentbox: source kit env exports
if [ -d /etc/agentbox/env.d ]; then
    for f in /etc/agentbox/env.d/*.sh; do
        [ -r "$f" ] && . "$f"
    done
fi
ZSHENV
fi

# =============================================================================
# NODE SECTION
# Inlined from internal/builtinkits/kits/node/install.sh
# =============================================================================
echo "=== [polyglot] Installing Node LTS (v${NODE_LTS_MAJOR}.x) + current (v${NODE_CURRENT_MAJOR}.x) ==="

# NodeSource publishes a setup script that adds its apt repo and imports its GPG key.
# After running, `apt-get install nodejs` installs the requested major version.
curl -fsSL "https://deb.nodesource.com/setup_${NODE_LTS_MAJOR}.x" | bash -
apt-get install -y --no-install-recommends nodejs
rm -rf /var/lib/apt/lists/*

# Save the LTS node binary path before `n` potentially overwrites /usr/bin/node.
LTS_BIN="$(readlink -f /usr/bin/node)"
echo "Node LTS binary: ${LTS_BIN}"

# `n` swaps /usr/local/bin/node to the requested version. We preserve LTS as `node-lts`.
echo "Installing Node current (v${NODE_CURRENT_MAJOR}.x) via n..."
npm install -g n
n "${NODE_CURRENT_MAJOR}"

# Create `node-lts` symlink pointing at the saved LTS binary.
ln -sf "${LTS_BIN}" /usr/local/bin/node-lts
echo "node-lts -> ${LTS_BIN}"

# corepack ships with Node >=16 and manages pnpm/yarn without global npm installs.
echo "Enabling corepack for pnpm and yarn..."
corepack enable
corepack prepare pnpm@latest --activate
corepack prepare yarn@stable --activate

# bun: single binary zip from GitHub.
# Archive name: bun-linux-{BUN_ARCH}.zip; BUN_ARCH is x64 or aarch64 (NOT the Go arch).
echo "Installing bun v${BUN_VERSION}..."
curl -fsSL "https://github.com/oven-sh/bun/releases/download/bun-v${BUN_VERSION}/bun-linux-${BUN_ARCH}.zip" \
  -o "$TMP/bun.zip"
unzip -q "$TMP/bun.zip" -d "$TMP/bun-extract"
install -m 0755 "$TMP/bun-extract/bun-linux-${BUN_ARCH}/bun" /usr/local/bin/bun
rm -f "$TMP/bun.zip"

# deno: single binary zip from GitHub.
# Archive name: deno-{RUST_TRIPLE}.zip
echo "Installing deno v${DENO_VERSION}..."
curl -fsSL "https://github.com/denoland/deno/releases/download/v${DENO_VERSION}/deno-${RUST_TRIPLE}.zip" \
  -o "$TMP/deno.zip"
unzip -q "$TMP/deno.zip" -d "$TMP/deno-extract"
install -m 0755 "$TMP/deno-extract/deno" /usr/local/bin/deno
rm -f "$TMP/deno.zip"

echo "Node section complete"

# =============================================================================
# PYTHON SECTION
# Inlined from internal/builtinkits/kits/python/install.sh
# =============================================================================
echo "=== [polyglot] Installing Python tools (uv, ruff, pyenv) ==="

# uv: fast Python package and project manager.
# Archive: uv-{RUST_TRIPLE}.tar.gz; contains binaries `uv` and `uvx`.
echo "Installing uv ${UV_VERSION}..."
curl -fsSL "https://github.com/astral-sh/uv/releases/download/${UV_VERSION}/uv-${RUST_TRIPLE}.tar.gz" \
  -o "$TMP/uv.tar.gz"
tar -xzf "$TMP/uv.tar.gz" -C "$TMP"
install -m 0755 "$TMP/uv-${RUST_TRIPLE}/uv"  /usr/local/bin/uv
install -m 0755 "$TMP/uv-${RUST_TRIPLE}/uvx" /usr/local/bin/uvx
rm -f "$TMP/uv.tar.gz"

# ruff: fast Python linter and formatter.
# Archive: ruff-{RUST_TRIPLE}.tar.gz; contains binary `ruff`.
echo "Installing ruff ${RUFF_VERSION}..."
curl -fsSL "https://github.com/astral-sh/ruff/releases/download/${RUFF_VERSION}/ruff-${RUST_TRIPLE}.tar.gz" \
  -o "$TMP/ruff.tar.gz"
tar -xzf "$TMP/ruff.tar.gz" -C "$TMP"
install -m 0755 "$TMP/ruff-${RUST_TRIPLE}/ruff" /usr/local/bin/ruff
rm -f "$TMP/ruff.tar.gz"

# pyenv: Python version manager, installed via git clone to /opt/pyenv.
# env.sh exports PYENV_ROOT and puts pyenv on PATH; users who want shims
# run `eval "$(pyenv init -)"` themselves — env.sh does not do this (command, not export).
echo "Installing pyenv v${PYENV_VERSION}..."
git clone --depth 1 --branch "v${PYENV_VERSION}" \
  https://github.com/pyenv/pyenv.git /opt/pyenv

echo "Python section complete"

# =============================================================================
# GO SECTION
# Inlined from internal/builtinkits/kits/go/install.sh
# =============================================================================
echo "=== [polyglot] Installing Go toolchain + gopls + golangci-lint + delve ==="

# Go toolchain: download from go.dev/dl/; extract to /usr/local/go.
echo "Installing Go ${GO_VERSION}..."
curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${GO_ARCH}.tar.gz" \
  -o "$TMP/go.tar.gz"
rm -rf /usr/local/go
tar -xzf "$TMP/go.tar.gz" -C /usr/local
# Verify the toolchain is working before using it.
/usr/local/go/bin/go version
rm -f "$TMP/go.tar.gz"

# gopls: Go language server. Installed @latest; tracks the toolchain release cycle.
# Binaries land in GOBIN=/usr/local/bin so all users find them.
echo "Installing gopls..."
GOPATH=/opt/go GOBIN=/usr/local/bin /usr/local/go/bin/go install golang.org/x/tools/gopls@latest

# delve: Go debugger. Version-pinned for reproducibility. Binary lands as `dlv`.
echo "Installing delve v${DELVE_VERSION}..."
GOPATH=/opt/go GOBIN=/usr/local/bin /usr/local/go/bin/go install \
  "github.com/go-delve/delve/cmd/dlv@v${DELVE_VERSION}"

# golangci-lint: multi-linter runner. Official install script; -b sets destination bin dir.
echo "Installing golangci-lint v${GOLANGCI_LINT_VERSION}..."
curl -fsSL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh \
  | sh -s -- -b /usr/local/bin "v${GOLANGCI_LINT_VERSION}"

echo "Go section complete"

# =============================================================================
# RUST SECTION
# Inlined from internal/builtinkits/kits/rust/install.sh
# =============================================================================
echo "=== [polyglot] Installing Rust (rustup, stable + nightly, cargo-watch, sccache) ==="

# RUSTUP_HOME and CARGO_HOME both point to /opt/rust so the installation is
# in a system-wide directory (not /root/.rustup). All container users see the same
# toolchain; env.sh exports these vars and adds /opt/rust/bin to PATH.
export RUSTUP_HOME="/opt/rust"
export CARGO_HOME="/opt/rust"

echo "Installing rustup (${RUST_TOOLCHAIN_STABLE} toolchain) to /opt/rust..."
curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs \
  | sh -s -- -y --profile minimal --default-toolchain "${RUST_TOOLCHAIN_STABLE}" \
       --no-modify-path
# --no-modify-path: don't touch /root/.profile; env.sh handles PATH instead.

# Nightly toolchain: useful for macro development, nightly-only features, and miri.
echo "Installing nightly toolchain..."
/opt/rust/bin/rustup toolchain install "${RUST_TOOLCHAIN_NIGHTLY}"

# cargo-watch: watch files and re-run cargo commands. Version-pinned for reproducibility.
echo "Installing cargo-watch v${CARGO_WATCH_VERSION}..."
/opt/rust/bin/cargo install "cargo-watch@${CARGO_WATCH_VERSION}"

# sccache: compiler cache for Rust/C++ builds. Speeds up repeated container builds.
echo "Installing sccache v${SCCACHE_VERSION}..."
/opt/rust/bin/cargo install "sccache@${SCCACHE_VERSION}"

# Verify rustc is working.
/opt/rust/bin/rustc --version

echo "Rust section complete"

# =============================================================================
# RUBY SECTION (NEW — not in any Part A kit)
# rbenv: Ruby version manager. No Ruby pre-installed per KITS.md
# ("Ruby (rbenv)" — rbenv is the tool; users `rbenv install <ver>` themselves).
# =============================================================================
echo "=== [polyglot] Installing rbenv v${RBENV_VERSION} ==="

git clone --depth 1 --branch "v${RBENV_VERSION}" \
  https://github.com/rbenv/rbenv.git /opt/rbenv

# Verify rbenv binary works (it's a shell script; check it's executable and readable).
/opt/rbenv/bin/rbenv --version

echo "Ruby section complete"

# =============================================================================
# JAVA/KOTLIN SECTION (NEW — not in any Part A kit)
# sdkman: Software Development Kit Manager for JVM-based tools (Java, Kotlin, Gradle, etc.)
# No JDK pre-installed per KITS.md ("sdkman, no JDK pre-installed").
# Users run `sdk install java` themselves inside the container.
# =============================================================================
echo "=== [polyglot] Installing sdkman to ${SDKMAN_DIR} ==="

# sdkman's installer respects SDKMAN_DIR if set in the environment.
# The URL https://get.sdkman.io always serves the current stable installer.
# No version pin in the URL — intentional; see comments in the pinned versions block above.
export SDKMAN_DIR
curl -s https://get.sdkman.io | bash

# Verify the init script exists (sdkman's canonical "installed" check).
[ -f "${SDKMAN_DIR}/bin/sdkman-init.sh" ] || {
    echo "sdkman install failed: ${SDKMAN_DIR}/bin/sdkman-init.sh not found" >&2
    exit 1
}
echo "sdkman installed at ${SDKMAN_DIR}"

echo "Java/Kotlin section complete"

# =============================================================================
# FINAL
# =============================================================================
echo "polyglot kit install complete"
