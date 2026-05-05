#!/usr/bin/env bash
# python kit installer: uv, ruff, pyenv.
# Python 3 itself comes from packages.txt (system Python for tools that don't need shims).
# All tool versions are pinned at the top for reproducible rebuilds.
# Verified against upstream release APIs on 2026-05-05.
set -euo pipefail

# --- Pinned versions (env-var overridable) ---
UV_VERSION="${UV_VERSION:-0.11.9}"         # astral-sh/uv — v-prefix absent in tag
RUFF_VERSION="${RUFF_VERSION:-0.15.12}"    # astral-sh/ruff — v-prefix absent in tag
PYENV_VERSION="${PYENV_VERSION:-2.6.28}"   # pyenv/pyenv — v-prefix stripped from tag v2.6.28

# --- Arch detection ---
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64)
    RUST_TRIPLE="x86_64-unknown-linux-gnu"
    GO_ARCH="amd64"
    ;;
  aarch64)
    RUST_TRIPLE="aarch64-unknown-linux-gnu"
    GO_ARCH="arm64"
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

# --- uv (fast Python package and project manager) ---
# Archive: uv-{RUST_TRIPLE}.tar.gz; contains binaries `uv` and `uvx`.
# Published at github.com/astral-sh/uv/releases — version tag has no v-prefix.
echo "Installing uv ${UV_VERSION}..."
curl -fsSL "https://github.com/astral-sh/uv/releases/download/${UV_VERSION}/uv-${RUST_TRIPLE}.tar.gz" \
  -o "$TMP/uv.tar.gz"
tar -xzf "$TMP/uv.tar.gz" -C "$TMP"
install -m 0755 "$TMP/uv-${RUST_TRIPLE}/uv"  /usr/local/bin/uv
install -m 0755 "$TMP/uv-${RUST_TRIPLE}/uvx" /usr/local/bin/uvx

# --- ruff (fast Python linter and formatter) ---
# Archive: ruff-{RUST_TRIPLE}.tar.gz; contains binary `ruff`.
# Published at github.com/astral-sh/ruff/releases — version tag has no v-prefix.
echo "Installing ruff ${RUFF_VERSION}..."
curl -fsSL "https://github.com/astral-sh/ruff/releases/download/${RUFF_VERSION}/ruff-${RUST_TRIPLE}.tar.gz" \
  -o "$TMP/ruff.tar.gz"
tar -xzf "$TMP/ruff.tar.gz" -C "$TMP"
install -m 0755 "$TMP/ruff-${RUST_TRIPLE}/ruff" /usr/local/bin/ruff

# --- pyenv (Python version manager) ---
# Installed via git clone to /opt/pyenv so it's available for all users.
# The env.sh exports PYENV_ROOT and puts pyenv on PATH; users who want shims
# run `eval "$(pyenv init -)"` in their interactive shell — env.sh does not do this
# (that's a command, not an export — violates the env.sh contract per KITS.md).
echo "Installing pyenv v${PYENV_VERSION}..."
git clone --depth 1 --branch "v${PYENV_VERSION}" \
  https://github.com/pyenv/pyenv.git /opt/pyenv

echo "python kit install complete"
