#!/usr/bin/env bash
# claude kit installer: @anthropic-ai/claude-code via npm + claude-mode binary.
# Verified: package name @anthropic-ai/claude-code, binary `claude`.
# Latest version at write time: 2.1.128 (npm view @anthropic-ai/claude-code, 2026-05-05).
# YOLO flag: --dangerously-skip-permissions (confirmed present in this release).
# claude-mode: https://github.com/nklisch/claude-code-modes — installer verified 2026-05-07.
set -euo pipefail

CLAUDE_VERSION="${CLAUDE_VERSION:-latest}"
CLAUDE_MODE_VERSION="${CLAUDE_MODE_VERSION:-latest}"

echo "Installing @anthropic-ai/claude-code@${CLAUDE_VERSION}..."
npm install -g "@anthropic-ai/claude-code@${CLAUDE_VERSION}"

# Verify the binary is reachable on PATH.
# npm globals install to /usr/local/bin when node is installed via NodeSource (node kit).
command -v claude >/dev/null || { echo "claude binary not on PATH after install" >&2; exit 1; }

echo "claude kit install complete"

# --- claude-mode (system-prompt wrapper for claude) ---
# Upstream installer downloads a single static binary into CLAUDE_MODE_INSTALL,
# verifies SHA-256 against the release's checksums.txt, and exits non-zero on
# any failure. Upstream does not yet honor a version env var; we read
# CLAUDE_MODE_VERSION for forward-compat and log it for traceability.
echo "Installing claude-mode (CLAUDE_MODE_VERSION=${CLAUDE_MODE_VERSION})..."
CLAUDE_MODE_INSTALL=/usr/local/bin \
  curl -fsSL https://raw.githubusercontent.com/nklisch/claude-code-modes/main/install.sh | sh

command -v claude-mode >/dev/null || { echo "claude-mode binary not on PATH after install" >&2; exit 1; }

echo "claude-mode install complete"
