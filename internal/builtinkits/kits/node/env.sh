#!/usr/bin/env bash
# node kit env. Exports and PATH only.
# bun and deno are installed to /usr/local/bin by install.sh — already on PATH.
# node-lts symlink is at /usr/local/bin/node-lts; bare `node` points to current.
export PATH="/usr/local/bin:${PATH}"
