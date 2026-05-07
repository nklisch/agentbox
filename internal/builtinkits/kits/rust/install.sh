#!/usr/bin/env bash
# rust kit installer: rustup with stable + nightly toolchains, cargo-watch, sccache.
# Install root is /opt/rust so all users share the toolchain without root's home dir.
# All tool versions are pinned at the top for reproducible rebuilds.
# Verified against upstream release APIs on 2026-05-05.
set -euo pipefail

# --- Pinned versions (env-var overridable) ---
# RUST_TOOLCHAIN_STABLE and RUST_TOOLCHAIN_NIGHTLY are channel names, not point releases.
# Rust's release cadence (6-week stable, rolling nightly) makes channel names the right
# handle here — pinning a specific rustc version would prevent security patch uptake.
# Use `rustup update` in the container if you need the newest patch on a given channel.
RUST_TOOLCHAIN_STABLE="${RUST_TOOLCHAIN_STABLE:-stable}"
RUST_TOOLCHAIN_NIGHTLY="${RUST_TOOLCHAIN_NIGHTLY:-nightly}"
CARGO_WATCH_VERSION="${CARGO_WATCH_VERSION:-8.5.3}"  # watchexec/cargo-watch tag v8.5.3
SCCACHE_VERSION="${SCCACHE_VERSION:-0.15.0}"          # mozilla/sccache tag v0.15.0

# --- Arch detection ---
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64)
    RUST_TRIPLE="x86_64-unknown-linux-gnu"
    MUSL_TRIPLE="x86_64-unknown-linux-musl"
    GO_ARCH="amd64"
    ;;
  aarch64)
    RUST_TRIPLE="aarch64-unknown-linux-gnu"
    MUSL_TRIPLE="aarch64-unknown-linux-musl"
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

# --- rustup (Rust toolchain installer) ---
# RUSTUP_HOME and CARGO_HOME both point to /opt/rust so the installation is
# in a system-wide directory rather than /root/.rustup. All users in the container
# see the same toolchain; env.sh exports these variables and adds /opt/rust/bin to PATH.
export RUSTUP_HOME="/opt/rust"
export CARGO_HOME="/opt/rust"

echo "Installing rustup (${RUST_TOOLCHAIN_STABLE} toolchain) to /opt/rust..."
curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs \
  | sh -s -- -y --profile minimal --default-toolchain "${RUST_TOOLCHAIN_STABLE}" \
       --no-modify-path
# --no-modify-path: don't touch /root/.profile; env.sh handles PATH instead.

# --- nightly toolchain ---
# Install nightly in addition to stable; useful for macro development,
# nightly-only features, and miri. Both toolchains share the /opt/rust install root.
echo "Installing nightly toolchain..."
/opt/rust/bin/rustup toolchain install "${RUST_TOOLCHAIN_NIGHTLY}"

# --- cargo-watch (watch files and re-run cargo commands) ---
# Use the prebuilt binary instead of `cargo install`. Building from source pulls a
# heavy Rust dep tree and the linker step regularly OOMs on memory-constrained
# podman machines (notably arm64 macOS hosts with default VM memory).
# Archive: cargo-watch-v{VER}-{RUST_TRIPLE}.tar.xz; binary at versioned-subdir/cargo-watch.
# xz-utils is provided by the base kit's packages.txt (rust kit depends on base).
echo "Installing cargo-watch v${CARGO_WATCH_VERSION} (prebuilt)..."
curl -fsSL "https://github.com/watchexec/cargo-watch/releases/download/v${CARGO_WATCH_VERSION}/cargo-watch-v${CARGO_WATCH_VERSION}-${RUST_TRIPLE}.tar.xz" \
  -o "$TMP/cargo-watch.tar.xz"
tar -xJf "$TMP/cargo-watch.tar.xz" -C "$TMP"
install -m 0755 "$TMP/cargo-watch-v${CARGO_WATCH_VERSION}-${RUST_TRIPLE}/cargo-watch" /opt/rust/bin/cargo-watch

# --- sccache (compiler cache for Rust / C++ builds) ---
# Use the prebuilt binary — `cargo install sccache` pulls in tokio, hyper, opendal,
# reqsign, ring, blake3, zstd, etc. The single-codegen-unit release link step needs
# ~3-4 GB RSS and gets SIGKILLed by the kernel OOM killer on podman machines with
# default memory. sccache only publishes musl builds for linux; musl binaries run
# fine on debian:bookworm-slim (glibc) — they're statically linked.
# Archive: sccache-v{VER}-{MUSL_TRIPLE}.tar.gz; binary at versioned-subdir/sccache.
echo "Installing sccache v${SCCACHE_VERSION} (prebuilt)..."
curl -fsSL "https://github.com/mozilla/sccache/releases/download/v${SCCACHE_VERSION}/sccache-v${SCCACHE_VERSION}-${MUSL_TRIPLE}.tar.gz" \
  -o "$TMP/sccache.tar.gz"
tar -xzf "$TMP/sccache.tar.gz" -C "$TMP"
install -m 0755 "$TMP/sccache-v${SCCACHE_VERSION}-${MUSL_TRIPLE}/sccache" /opt/rust/bin/sccache

echo "rust kit install complete"
