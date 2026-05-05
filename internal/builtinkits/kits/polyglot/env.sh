#!/usr/bin/env bash
# polyglot kit env. Exports and PATH only — no commands, no init calls.
# Per KITS.md: env.sh is sourced for every shell; keep it idempotent and fast.
# Order: /usr/local/bin first, then language-specific bins prepended in reverse
# dependency order so the last prepend (rust) ends up earliest in PATH.

# node: bun, deno, node-lts are all in /usr/local/bin — already covered below.
export PATH="/usr/local/bin:${PATH}"

# python: pyenv binary
export PYENV_ROOT="/opt/pyenv"
export PATH="${PYENV_ROOT}/bin:${PATH}"
# Note: `eval "$(pyenv init -)"` is intentionally NOT here — that's a command,
# not an export. Users who need pyenv shims run it in their interactive shell.

# go: toolchain + gopls/golangci-lint/dlv land in GOBIN=/usr/local/bin
export PATH="/usr/local/go/bin:${PATH}"
export GOPATH="${GOPATH:-/opt/go}"
export GOBIN="${GOBIN:-/usr/local/bin}"

# rust: rustup + cargo + cargo-watch + sccache all live in /opt/rust/bin
export RUSTUP_HOME="/opt/rust"
export CARGO_HOME="/opt/rust"
export PATH="/opt/rust/bin:${PATH}"

# ruby: rbenv binary (users run `rbenv install <ver>` to install a Ruby version;
# `eval "$(rbenv init -)"` for shims is intentionally NOT here — command, not export)
export RBENV_ROOT="/opt/rbenv"
export PATH="${RBENV_ROOT}/bin:${PATH}"

# java/kotlin: sdkman directory and sdk binary
# Note: sdkman wants `source $SDKMAN_DIR/bin/sdkman-init.sh` for full activation
# (shims, version management), but that's a command — violates env.sh contract.
# The PATH entry below covers `sdk` for interactive use; users who need full sdkman
# functionality source sdkman-init.sh in their interactive shell config.
export SDKMAN_DIR="/opt/sdkman"
export PATH="${SDKMAN_DIR}/bin:${PATH}"
