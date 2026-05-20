#!/usr/bin/env bash
# antigravity kit installer.
# Runs the official installer for the Antigravity CLI binary inside the box.
set -euo pipefail

echo "Downloading and installing Antigravity CLI (agy)..."
# The bootstrapper defaults to $HOME/.local/bin if --dir is not specified, 
# but installing to /usr/local/bin makes it globally available on the PATH.
curl -fsSL https://antigravity.google/cli/install.sh | bash -s -- --dir /usr/local/bin

# Verify the binary is reachable on PATH
command -v agy >/dev/null || { echo "agy binary not on PATH after install" >&2; exit 1; }

echo "antigravity kit install complete"
