#!/usr/bin/env bash
# opencode kit installer: sst/opencode binary release from GitHub.
# Verified: latest release v1.14.37 as of 2026-05-05.
# Asset pattern: opencode-linux-{x64|arm64}.tar.gz (repo moved to anomalyco/opencode
# but download URLs via github.com/sst/opencode still resolve via redirect).
# YOLO flag: to be confirmed at smoke-test time (see install notes in phase-5.md).
set -euo pipefail

# Pin to a verified release. Override with OPENCODE_VERSION=X.Y.Z for reproducibility.
OPENCODE_VERSION="${OPENCODE_VERSION:-1.14.37}"

# Architecture detection: x86_64 -> x64, aarch64 -> arm64.
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64)  PLATFORM="linux-x64" ;;
  aarch64) PLATFORM="linux-arm64" ;;
  *)       echo "unsupported arch: $ARCH" >&2; exit 1 ;;
esac

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

ASSET="opencode-${PLATFORM}.tar.gz"
URL="https://github.com/sst/opencode/releases/download/v${OPENCODE_VERSION}/${ASSET}"

echo "Downloading opencode v${OPENCODE_VERSION} (${PLATFORM})..."
curl -fsSL "$URL" -o "$TMP/opencode.tar.gz"

echo "Extracting..."
tar -xzf "$TMP/opencode.tar.gz" -C "$TMP"

# The tarball extracts a single `opencode` binary.
install -m 0755 "$TMP/opencode" /usr/local/bin/opencode

# Verify the binary is reachable on PATH.
command -v opencode >/dev/null || { echo "opencode binary not on PATH after install" >&2; exit 1; }

echo "opencode kit install complete"
