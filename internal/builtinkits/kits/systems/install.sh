#!/usr/bin/env bash
# systems kit installer.
# All tools come from packages.txt (apt). Nothing extra to install here.
# The arch-detection block is kept for structural consistency with other kits,
# even though no arch-specific work occurs in this kit.
set -euo pipefail

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

# --- Wire env.d into zsh (idempotent; first kit to run does it once) ---
# /etc/profile.d/agentbox.sh is only sourced by bash login shells, not zsh.
# /etc/zsh/zshenv is sourced for every zsh invocation — login, non-login, interactive, not.
if ! grep -q "agentbox/env.d" /etc/zsh/zshenv 2>/dev/null; then
  cat >> /etc/zsh/zshenv <<'ZSHENV'

# --- agentbox kit env ---
for _f in /etc/agentbox/env.d/*.sh; do
  [ -r "$_f" ] && . "$_f"
done
unset _f
ZSHENV
fi

echo "systems kit: all tools installed via packages.txt (clang, lld, lldb, cmake, ninja-build, gdb, valgrind, pkg-config, build-essential)"
