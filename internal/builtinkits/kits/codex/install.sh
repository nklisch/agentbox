#!/usr/bin/env bash
# codex kit installer: @openai/codex via npm.
# Verified: package name @openai/codex, binary `codex`.
# Latest version at write time: 0.128.0 (npm view @openai/codex, 2026-05-05).
# YOLO flag: to be confirmed at smoke-test time (see install notes in phase-5.md).
set -euo pipefail

CODEX_VERSION="${CODEX_VERSION:-latest}"

echo "Installing @openai/codex@${CODEX_VERSION}..."
npm install -g "@openai/codex@${CODEX_VERSION}"

# Verify the binary is reachable on PATH.
command -v codex >/dev/null || { echo "codex binary not on PATH after install" >&2; exit 1; }

echo "codex kit install complete"
