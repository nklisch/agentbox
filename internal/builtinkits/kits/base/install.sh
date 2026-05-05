#!/usr/bin/env bash
# Base kit installer: shell + modern CLI + zellij + box helpers.
# All tool versions are pinned at the top for reproducible rebuilds.
# Verified against GitHub releases on 2026-05-04 (gh + glab added 2026-05-05).

set -euo pipefail

# --- Pinned versions ---
# Each version was confirmed against the GitHub releases API before pinning.
# URLs and archive layouts were tested; notes are inline where they differ
# from a naive guess.
ZELLIJ_VERSION="${ZELLIJ_VERSION:-0.44.1}"       # zellij-org/zellij — musl only for linux
STARSHIP_VERSION="${STARSHIP_VERSION:-1.25.1}"   # starship/starship
EZA_VERSION="${EZA_VERSION:-0.23.4}"             # eza-community/eza — underscore before triple
DUST_VERSION="${DUST_VERSION:-1.2.4}"            # bootandy/dust — v-prefix in archive name + subdir
DUF_VERSION="${DUF_VERSION:-0.9.1}"              # muesli/duf — Go tool; uses x86_64 (not amd64) in tar.gz
BTM_VERSION="${BTM_VERSION:-0.12.3}"             # ClementTsang/bottom — version has no v-prefix on tag
PROCS_VERSION="${PROCS_VERSION:-0.14.11}"        # dalance/procs — zip; uses bare arch (x86_64/aarch64)
HYPERFINE_VERSION="${HYPERFINE_VERSION:-1.20.0}" # sharkdp/hyperfine — binary in versioned subdir
WATCHEXEC_VERSION="${WATCHEXEC_VERSION:-2.5.1}"  # watchexec/watchexec — version without v in filename; tar.xz; binary in subdir
YQ_VERSION="${YQ_VERSION:-4.53.2}"               # mikefarah/yq — single binary; Go arch suffix
ZOXIDE_VERSION="${ZOXIDE_VERSION:-0.9.9}"        # ajeetdsouza/zoxide — musl only for linux; binary at archive root
DELTA_VERSION="${DELTA_VERSION:-0.19.2}"         # dandavison/delta — not in Debian Bookworm; binary in versioned subdir
GH_VERSION="${GH_VERSION:-2.92.0}"               # cli/cli — binary in {archive}/bin/gh; uses GO_ARCH (amd64/arm64)
GLAB_VERSION="${GLAB_VERSION:-1.93.0}"           # gitlab-org/cli on GitLab — binary in bin/glab at archive root; uses GO_ARCH

# --- Arch detection ---
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64)
    RUST_TRIPLE="x86_64-unknown-linux-gnu"
    MUSL_TRIPLE="x86_64-unknown-linux-musl"
    GO_ARCH="amd64"
    PROCS_ARCH="x86_64"
    DUF_ARCH="x86_64"
    ;;
  aarch64)
    RUST_TRIPLE="aarch64-unknown-linux-gnu"
    MUSL_TRIPLE="aarch64-unknown-linux-musl"
    GO_ARCH="arm64"
    PROCS_ARCH="aarch64"
    DUF_ARCH="arm64"
    ;;
  *)
    echo "unsupported arch: $ARCH" >&2
    exit 1
    ;;
esac

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# --- Symlink Debian-mangled tool names to canonical names ---
# Debian ships `bat` as `batcat` and `fd-find` as `fdfind`.
# Create canonical names so the rest of the world's scripts and muscle memory work.
[ -x /usr/bin/batcat ] && ln -sf /usr/bin/batcat /usr/local/bin/bat
[ -x /usr/bin/fdfind ] && ln -sf /usr/bin/fdfind /usr/local/bin/fd

# --- zellij (terminal multiplexer) ---
# Only musl linux builds are published; no gnu variant.
curl -fsSL "https://github.com/zellij-org/zellij/releases/download/v${ZELLIJ_VERSION}/zellij-${MUSL_TRIPLE}.tar.gz" \
  | tar -xz -C /usr/local/bin zellij

# --- starship (shell prompt) ---
curl -fsSL "https://github.com/starship/starship/releases/download/v${STARSHIP_VERSION}/starship-${RUST_TRIPLE}.tar.gz" \
  | tar -xz -C /usr/local/bin starship

# --- eza (ls replacement) ---
# Archive name uses underscore before the triple (eza_{triple}.tar.gz).
# Binary is at ./eza inside the archive.
curl -fsSL "https://github.com/eza-community/eza/releases/download/v${EZA_VERSION}/eza_${RUST_TRIPLE}.tar.gz" \
  -o "$TMP/eza.tar.gz"
tar -xzf "$TMP/eza.tar.gz" -C /usr/local/bin ./eza

# --- dust (du replacement) ---
# Archive name: dust-v{VER}-{triple}.tar.gz
# Binary lives in a versioned subdirectory: dust-v{VER}-{triple}/dust
curl -fsSL "https://github.com/bootandy/dust/releases/download/v${DUST_VERSION}/dust-v${DUST_VERSION}-${RUST_TRIPLE}.tar.gz" \
  -o "$TMP/dust.tar.gz"
tar -xzf "$TMP/dust.tar.gz" -C "$TMP"
install -m 0755 "$TMP/dust-v${DUST_VERSION}-${RUST_TRIPLE}/dust" /usr/local/bin/dust

# --- duf (df replacement) ---
# Go binary; archive name: duf_{VER}_linux_{DUF_ARCH}.tar.gz
# DUF_ARCH is x86_64 (not amd64) for x86_64 machines; arm64 for aarch64.
# Binary is at archive root.
curl -fsSL "https://github.com/muesli/duf/releases/download/v${DUF_VERSION}/duf_${DUF_VERSION}_linux_${DUF_ARCH}.tar.gz" \
  -o "$TMP/duf.tar.gz"
tar -xzf "$TMP/duf.tar.gz" -C "$TMP" duf
install -m 0755 "$TMP/duf" /usr/local/bin/duf

# --- bottom / btm (top replacement) ---
# Archive name: bottom_{triple}.tar.gz; binary is `btm` at archive root.
# Release tag has no v-prefix: 0.12.3 not v0.12.3.
curl -fsSL "https://github.com/ClementTsang/bottom/releases/download/${BTM_VERSION}/bottom_${RUST_TRIPLE}.tar.gz" \
  -o "$TMP/btm.tar.gz"
tar -xzf "$TMP/btm.tar.gz" -C "$TMP" btm
install -m 0755 "$TMP/btm" /usr/local/bin/btm

# --- procs (ps replacement) ---
# Zip archive; name: procs-v{VER}-{PROCS_ARCH}-linux.zip (bare arch, no triple).
# Single binary at archive root.
curl -fsSL "https://github.com/dalance/procs/releases/download/v${PROCS_VERSION}/procs-v${PROCS_VERSION}-${PROCS_ARCH}-linux.zip" \
  -o "$TMP/procs.zip"
unzip -q "$TMP/procs.zip" -d "$TMP/procs"
install -m 0755 "$TMP/procs/procs" /usr/local/bin/procs

# --- hyperfine (benchmarking tool) ---
# Archive name: hyperfine-v{VER}-{triple}.tar.gz
# Binary lives in versioned subdir: hyperfine-v{VER}-{triple}/hyperfine
curl -fsSL "https://github.com/sharkdp/hyperfine/releases/download/v${HYPERFINE_VERSION}/hyperfine-v${HYPERFINE_VERSION}-${RUST_TRIPLE}.tar.gz" \
  -o "$TMP/hyperfine.tar.gz"
tar -xzf "$TMP/hyperfine.tar.gz" -C "$TMP"
install -m 0755 "$TMP/hyperfine-v${HYPERFINE_VERSION}-${RUST_TRIPLE}/hyperfine" /usr/local/bin/hyperfine

# --- watchexec (file-watching command runner) ---
# Archive name: watchexec-{VER}-{triple}.tar.xz (no v prefix on version in filename).
# Binary lives in versioned subdir: watchexec-{VER}-{triple}/watchexec
curl -fsSL "https://github.com/watchexec/watchexec/releases/download/v${WATCHEXEC_VERSION}/watchexec-${WATCHEXEC_VERSION}-${RUST_TRIPLE}.tar.xz" \
  -o "$TMP/watchexec.tar.xz"
tar -xJf "$TMP/watchexec.tar.xz" -C "$TMP"
install -m 0755 "$TMP/watchexec-${WATCHEXEC_VERSION}-${RUST_TRIPLE}/watchexec" /usr/local/bin/watchexec

# --- yq (YAML/JSON/TOML query, Go binary) ---
# Single binary download; no archive to extract.
curl -fsSL "https://github.com/mikefarah/yq/releases/download/v${YQ_VERSION}/yq_linux_${GO_ARCH}" \
  -o /usr/local/bin/yq
chmod +x /usr/local/bin/yq

# --- zoxide (frecency-based cd) ---
# Only musl builds are published for linux; no gnu variant.
# Archive name: zoxide-{VER}-{MUSL_TRIPLE}.tar.gz (no v prefix on version in filename).
# Binary is at archive root.
curl -fsSL "https://github.com/ajeetdsouza/zoxide/releases/download/v${ZOXIDE_VERSION}/zoxide-${ZOXIDE_VERSION}-${MUSL_TRIPLE}.tar.gz" \
  -o "$TMP/zoxide.tar.gz"
tar -xzf "$TMP/zoxide.tar.gz" -C "$TMP" zoxide
install -m 0755 "$TMP/zoxide" /usr/local/bin/zoxide

# --- git-delta (pager for git diffs) ---
# Not in Debian Bookworm repos; installed from GitHub.
# Archive: delta-{VER}-{triple}.tar.gz; binary in versioned subdir.
# Tag has no v-prefix: 0.19.2 not v0.19.2.
curl -fsSL "https://github.com/dandavison/delta/releases/download/${DELTA_VERSION}/delta-${DELTA_VERSION}-${RUST_TRIPLE}.tar.gz" \
  -o "$TMP/delta.tar.gz"
tar -xzf "$TMP/delta.tar.gz" -C "$TMP"
install -m 0755 "$TMP/delta-${DELTA_VERSION}-${RUST_TRIPLE}/delta" /usr/local/bin/delta

# Configure git to use delta as the pager when it's available.
git config --system core.pager delta || true
git config --system interactive.diffFilter "delta --color-only" || true

# --- gh (GitHub CLI) ---
# Archive name: gh_{VER}_linux_{GO_ARCH}.tar.gz
# Binary lives at: gh_{VER}_linux_{GO_ARCH}/bin/gh
# Also ships shell completions and man pages we don't bother installing.
curl -fsSL "https://github.com/cli/cli/releases/download/v${GH_VERSION}/gh_${GH_VERSION}_linux_${GO_ARCH}.tar.gz" \
  -o "$TMP/gh.tar.gz"
tar -xzf "$TMP/gh.tar.gz" -C "$TMP"
install -m 0755 "$TMP/gh_${GH_VERSION}_linux_${GO_ARCH}/bin/gh" /usr/local/bin/gh

# --- glab (GitLab CLI) ---
# Archive name: glab_{VER}_linux_{GO_ARCH}.tar.gz
# Hosted on GitLab's release downloads endpoint (not GitHub).
# Binary lives at: bin/glab at archive root (no top-level versioned dir).
curl -fsSL "https://gitlab.com/gitlab-org/cli/-/releases/v${GLAB_VERSION}/downloads/glab_${GLAB_VERSION}_linux_${GO_ARCH}.tar.gz" \
  -o "$TMP/glab.tar.gz"
tar -xzf "$TMP/glab.tar.gz" -C "$TMP"
install -m 0755 "$TMP/bin/glab" /usr/local/bin/glab

# --- httpie (pretty HTTP client) and tldr (community man pages) ---
# pip3 packages — python3-pip is not in packages.txt because we want the
# install.sh apt layer separate from the packages.txt apt layer.
apt-get update -qq
apt-get install -y --no-install-recommends python3-pip
pip3 install --break-system-packages --no-cache-dir "httpie==3.2.4"
pip3 install --break-system-packages --no-cache-dir "tldr"
rm -rf /var/lib/apt/lists/*

# --- box helpers (copied from this kit's dir via $KIT_DIR) ---
install -m 0755 "${KIT_DIR}/box"         /usr/local/bin/box
install -m 0755 "${KIT_DIR}/box-info"    /usr/local/bin/box-info
install -m 0755 "${KIT_DIR}/box-net"     /usr/local/bin/box-net
install -m 0755 "${KIT_DIR}/box-scratch" /usr/local/bin/box-scratch
install -m 0755 "${KIT_DIR}/box-save"    /usr/local/bin/box-save
install -m 0755 "${KIT_DIR}/box-help"    /usr/local/bin/box-help

# --- Bridge env.d into zsh ---
# Phase 2's Dockerfile generator writes the env.d sourcing loop to
# /etc/profile.d/agentbox.sh, which only runs for bash login shells.
# zsh (the default agentbox shell) doesn't source /etc/profile.d. Append
# the loop to /etc/zsh/zshenv (sourced for ALL zsh invocations:
# interactive, login, scripts) so kit env.sh files take effect there too.
mkdir -p /etc/zsh
if ! grep -q '/etc/agentbox/env.d' /etc/zsh/zshenv 2>/dev/null; then
    cat >> /etc/zsh/zshenv <<'ZSHENV'
# agentbox: source kit env exports
if [ -d /etc/agentbox/env.d ]; then
    for f in /etc/agentbox/env.d/*.sh; do
        [ -r "$f" ] && . "$f"
    done
fi
ZSHENV
fi

# --- Interactive shell setup via /etc/zsh/zshrc ---
# Per KITS.md, env.sh is exports+PATH only. Interactive-shell init
# (prompt, key bindings, aliases) lives here instead.
cat >> /etc/zsh/zshrc <<'ZSHRC'

# --- agentbox base kit additions ---

# fzf key bindings and tab completion
[ -f /usr/share/doc/fzf/examples/key-bindings.zsh ] && . /usr/share/doc/fzf/examples/key-bindings.zsh
[ -f /usr/share/doc/fzf/examples/completion.zsh ]   && . /usr/share/doc/fzf/examples/completion.zsh

# zoxide — frecency cd (replaces z/autojump)
command -v zoxide >/dev/null && eval "$(zoxide init zsh)"

# starship — cross-shell prompt
command -v starship >/dev/null && eval "$(starship init zsh)"

# Aliases
alias ll='eza -la'
alias l='eza -l'
alias tree='eza --tree'
ZSHRC

# Default shell for root is zsh. The agentbox container runs as root.
chsh -s /usr/bin/zsh root || true

echo "base kit install complete"
