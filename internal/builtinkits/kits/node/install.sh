#!/usr/bin/env bash
# node kit installer: Node LTS (24) + current (25) via NodeSource + n,
# pnpm + yarn via corepack, bun, deno.
# All tool versions are pinned at the top for reproducible rebuilds.
# Verified against upstream release APIs and nodejs.org on 2026-05-05.
set -euo pipefail

# --- Pinned versions (env-var overridable) ---
# Node LTS major: 24 (codename "Krypton" — became LTS in Oct 2025; EOL April 2028).
# Node current: 25 (odd-number releases are not LTS; latest on nodejs.org: v25.9.0).
NODE_LTS_MAJOR="${NODE_LTS_MAJOR:-24}"
NODE_CURRENT_MAJOR="${NODE_CURRENT_MAJOR:-25}"
# pnpm via corepack — no explicit version pin; corepack prepare @latest fetches current.
# yarn via corepack — same.
BUN_VERSION="${BUN_VERSION:-1.3.13}"    # oven-sh/bun — v-prefix stripped from tag bun-v1.3.13
DENO_VERSION="${DENO_VERSION:-2.7.14}"  # denoland/deno — v-prefix stripped from tag v2.7.14

# --- Arch detection ---
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

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# --- Wire env.d into zsh (idempotent; first kit to run does it once) ---
if ! grep -q "agentbox/env.d" /etc/zsh/zshenv 2>/dev/null; then
  cat >> /etc/zsh/zshenv <<'ZSHENV'

# --- agentbox kit env ---
for _f in /etc/agentbox/env.d/*.sh; do
  [ -r "$_f" ] && . "$_f"
done
unset _f
ZSHENV
fi

# --- Node LTS via NodeSource ---
# NodeSource publishes a setup script that adds its apt repo and imports its GPG key.
# After running, `apt-get install nodejs` installs the requested major version.
# The installed `node` binary will be the LTS version.
echo "Installing Node LTS (v${NODE_LTS_MAJOR}.x) via NodeSource..."
curl -fsSL "https://deb.nodesource.com/setup_${NODE_LTS_MAJOR}.x" | bash -
apt-get install -y --no-install-recommends nodejs
rm -rf /var/lib/apt/lists/*

# Save the LTS node binary path before `n` potentially overwrites /usr/bin/node.
# After NodeSource, node lives at /usr/bin/node.
LTS_BIN="$(readlink -f /usr/bin/node)"
echo "Node LTS binary: ${LTS_BIN}"

# --- Node current via `n` (npm package) ---
# `n` swaps /usr/local/bin/node to the requested version. We preserve LTS as `node-lts`.
# After `n`, the default `node` command points to the current version.
echo "Installing Node current (v${NODE_CURRENT_MAJOR}.x) via n..."
npm install -g n
n "${NODE_CURRENT_MAJOR}"

# Create `node-lts` symlink pointing at the saved LTS binary.
# This lets scripts that need LTS specifically call `node-lts` while
# the bare `node` command picks up the current version.
ln -sf "${LTS_BIN}" /usr/local/bin/node-lts
echo "node-lts -> ${LTS_BIN}"

# --- pnpm + yarn via corepack ---
# corepack ships with Node ≥16 and manages pnpm/yarn without global npm installs.
# `corepack enable` adds shims; `corepack prepare ... --activate` downloads and activates.
echo "Enabling corepack for pnpm and yarn..."
corepack enable
corepack prepare pnpm@latest --activate
corepack prepare yarn@stable --activate

# --- bun (fast JavaScript runtime + package manager) ---
# bun publishes zip releases on GitHub. The archive contains a single `bun` binary.
# Archive name: bun-linux-{BUN_ARCH}.zip (BUN_ARCH is x64 or aarch64 — NOT the Go arch).
# Tag format: bun-v{VERSION}; strip the "bun-v" prefix to get the bare version number.
echo "Installing bun v${BUN_VERSION}..."
curl -fsSL "https://github.com/oven-sh/bun/releases/download/bun-v${BUN_VERSION}/bun-linux-${BUN_ARCH}.zip" \
  -o "$TMP/bun.zip"
unzip -q "$TMP/bun.zip" -d "$TMP/bun-extract"
# The zip extracts to a subdirectory: bun-linux-{ARCH}/bun
install -m 0755 "$TMP/bun-extract/bun-linux-${BUN_ARCH}/bun" /usr/local/bin/bun

# --- deno (secure TypeScript/JavaScript runtime) ---
# deno publishes zip releases on GitHub. The archive contains a single `deno` binary.
# Archive name: deno-{RUST_TRIPLE}.zip
echo "Installing deno v${DENO_VERSION}..."
curl -fsSL "https://github.com/denoland/deno/releases/download/v${DENO_VERSION}/deno-${RUST_TRIPLE}.zip" \
  -o "$TMP/deno.zip"
unzip -q "$TMP/deno.zip" -d "$TMP/deno-extract"
install -m 0755 "$TMP/deno-extract/deno" /usr/local/bin/deno

echo "node kit install complete"
