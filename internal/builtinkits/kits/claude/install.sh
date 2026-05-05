#!/usr/bin/env bash
# claude kit installer: @anthropic-ai/claude-code via npm.
# Verified: package name @anthropic-ai/claude-code, binary `claude`.
# Latest version at write time: 2.1.128 (npm view @anthropic-ai/claude-code, 2026-05-05).
# YOLO flag: --dangerously-skip-permissions (confirmed present in this release).
set -euo pipefail

CLAUDE_VERSION="${CLAUDE_VERSION:-latest}"

echo "Installing @anthropic-ai/claude-code@${CLAUDE_VERSION}..."
npm install -g "@anthropic-ai/claude-code@${CLAUDE_VERSION}"

# Verify the binary is reachable on PATH.
# npm globals install to /usr/local/bin when node is installed via NodeSource (node kit).
command -v claude >/dev/null || { echo "claude binary not on PATH after install" >&2; exit 1; }

echo "claude kit install complete"
