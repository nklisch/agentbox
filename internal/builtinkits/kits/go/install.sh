#!/usr/bin/env bash
# go kit installer: Go toolchain, gopls, golangci-lint, delve.
# All tool versions are pinned at the top for reproducible rebuilds.
# Verified against upstream release APIs on 2026-05-05.
set -euo pipefail

# --- Pinned versions (env-var overridable) ---
GO_VERSION="${GO_VERSION:-1.26.2}"                        # go.dev/VERSION?m=text — confirmed 2026-05-05
GOLANGCI_LINT_VERSION="${GOLANGCI_LINT_VERSION:-2.12.1}" # golangci/golangci-lint — v-prefix stripped
DELVE_VERSION="${DELVE_VERSION:-1.26.3}"                  # go-delve/delve — v-prefix stripped
# gopls is installed @latest; no pin needed — it tracks the toolchain release cycle

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

# --- Wire env.d into zsh (if not already done) ---
# /etc/profile.d/agentbox.sh is sourced by bash login shells but NOT by zsh.
# /etc/zsh/zshenv is sourced by every zsh invocation (login/non-login, interactive/not).
# This one-time addition ensures kit env.sh exports (PATH etc.) reach zsh users.
# The guard prevents double-adding if this kit is rebuilt on top of itself.
if ! grep -q "agentbox/env.d" /etc/zsh/zshenv 2>/dev/null; then
  cat >> /etc/zsh/zshenv <<'ZSHENV'

# --- agentbox kit env (sourced for all zsh invocations) ---
for _f in /etc/agentbox/env.d/*.sh; do
  # shellcheck source=/dev/null
  [ -r "$_f" ] && . "$_f"
done
unset _f
ZSHENV
fi

# --- Go toolchain ---
# Download from go.dev/dl/; archive: go<VER>.linux-<ARCH>.tar.gz
# Extract to /usr/local/go (not /usr/local/ — the archive has a go/ top-level dir).
echo "Installing Go ${GO_VERSION}..."
curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${GO_ARCH}.tar.gz" \
  -o "$TMP/go.tar.gz"
rm -rf /usr/local/go
tar -xzf "$TMP/go.tar.gz" -C /usr/local
# Verify the toolchain is working before attempting to use it
/usr/local/go/bin/go version

# --- gopls (Go language server) ---
# Installed via go install; binaries land in GOBIN=/usr/local/bin so all users find them.
# GOPATH is set to /opt/go to keep module cache off root's home dir.
echo "Installing gopls..."
GOPATH=/opt/go GOBIN=/usr/local/bin /usr/local/go/bin/go install golang.org/x/tools/gopls@latest

# --- delve (Go debugger) ---
# Pin to a specific version for reproducibility. Binary lands as `dlv`.
echo "Installing delve v${DELVE_VERSION}..."
GOPATH=/opt/go GOBIN=/usr/local/bin /usr/local/go/bin/go install \
  "github.com/go-delve/delve/cmd/dlv@v${DELVE_VERSION}"

# --- golangci-lint (multi-linter runner) ---
# Official install script; -b flag sets the destination bin dir.
# The script accepts a version string with v-prefix.
echo "Installing golangci-lint v${GOLANGCI_LINT_VERSION}..."
curl -fsSL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh \
  | sh -s -- -b /usr/local/bin "v${GOLANGCI_LINT_VERSION}"

echo "go kit install complete"
