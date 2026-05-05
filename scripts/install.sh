#!/bin/sh
# agentbox installer.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/nklisch/agentbox/main/scripts/install.sh | sh
#
# Environment overrides:
#   AGENTBOX_VERSION         Tag to install (default: latest stable release)
#   AGENTBOX_PREFIX          Install dir   (default: $HOME/.local/bin)
#   AGENTBOX_INSTALL_DEBUG   Set to 1 to enable `set -x` tracing
#   AGENTBOX_NO_PATH         Set to 1 to skip shell-rc PATH editing

set -eu

# test seam, not user-facing
REPO="${AGENTBOX_TEST_REPO:-nklisch/agentbox}"
# test seam, not user-facing
GITHUB_API="${AGENTBOX_TEST_API:-https://api.github.com/repos/${REPO}}"
# test seam, not user-facing
GITHUB_DL="${AGENTBOX_TEST_DL:-https://github.com/${REPO}/releases/download}"

SENTINEL_BEGIN="# >>> agentbox installer >>>"
SENTINEL_END="# <<< agentbox installer <<<"

DEFAULT_PREFIX="${HOME}/.local/bin"

PROGRAM="agentbox-install"

# log <msg...>
#   Print a status line to stderr, prefixed with [agentbox].
log() {
    printf '[agentbox] %s\n' "$*" >&2
}

# err <code> <msg...>
#   Print an error to stderr and exit with the given code.
err() {
    code=$1; shift
    printf '[agentbox] error: %s\n' "$*" >&2
    exit "$code"
}

# need <bin>
#   Verify a tool is on PATH; exit 3 if missing.
need() {
    command -v "$1" >/dev/null 2>&1 || \
        err 3 "missing required tool: $1 (please install it and rerun)"
}

# pick_sha256
#   Echo the name of the available sha256 tool (sha256sum on Linux,
#   shasum on macOS). Exit 3 if neither is available.
pick_sha256() {
    if command -v sha256sum >/dev/null 2>&1; then
        echo sha256sum
    elif command -v shasum >/dev/null 2>&1; then
        echo "shasum -a 256"
    else
        err 3 "neither sha256sum nor shasum is available"
    fi
}

# detect_os_arch
#   Echo "<os> <arch>" mapped to goreleaser's archive naming.
#   Exit 2 on unsupported combos.
#
# Supported (must match the .goreleaser.yaml matrix):
#   linux/amd64, linux/arm64, darwin/arm64
#
# Translation:
#   uname -s = Linux  -> linux
#   uname -s = Darwin -> darwin
#   uname -m = x86_64 | amd64           -> amd64
#   uname -m = aarch64 | arm64          -> arm64
detect_os_arch() {
    raw_os=$(uname -s)
    raw_arch=$(uname -m)
    case "$raw_os" in
        Linux)   os=linux ;;
        Darwin)  os=darwin ;;
        *)       err 2 "unsupported OS: $raw_os (build from source: https://github.com/${REPO})" ;;
    esac
    case "$raw_arch" in
        x86_64|amd64)   arch=amd64 ;;
        aarch64|arm64)  arch=arm64 ;;
        *)              err 2 "unsupported arch: $raw_arch (build from source)" ;;
    esac
    if [ "$os" = "darwin" ] && [ "$arch" = "amd64" ]; then
        err 2 "no prebuilt binary for darwin/amd64 (Intel Mac); build from source"
    fi
    printf '%s %s\n' "$os" "$arch"
}

# resolve_version
#   Echo the tag to install. Honors $AGENTBOX_VERSION if set; otherwise
#   queries the GitHub API for the latest stable release.
#
# The API response is parsed with grep+sed to avoid a `jq` dependency.
# Pattern: { ... "tag_name": "v0.2.0", ... }
resolve_version() {
    if [ -n "${AGENTBOX_VERSION:-}" ]; then
        printf '%s\n' "$AGENTBOX_VERSION"
        return
    fi
    log "resolving latest release..."
    tag=$(curl -fsSL "${GITHUB_API}/releases/latest" \
            | grep -o '"tag_name"[[:space:]]*:[[:space:]]*"[^"]*"' \
            | head -n 1 \
            | sed 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/')
    [ -n "$tag" ] || err 4 "could not resolve latest release tag from $GITHUB_API"
    printf '%s\n' "$tag"
}

# ensure_prefix <dir>
#   mkdir -p the target dir; verify it's writable.
ensure_prefix() {
    dir=$1
    mkdir -p "$dir" 2>/dev/null || \
        err 6 "cannot create $dir (rerun with sudo, or set AGENTBOX_PREFIX=)"
    [ -w "$dir" ] || \
        err 6 "$dir is not writable (rerun with sudo, or set AGENTBOX_PREFIX=)"
}

# download <url> <dest>
#   Curl with retries; exits 4 on failure.
download() {
    url=$1; dest=$2
    curl -fsSL --retry 3 --retry-delay 2 -o "$dest" "$url" || \
        err 4 "download failed: $url"
}

# verify_sha256 <archive_path> <checksums_path> <archive_basename>
#   Compute sha256 of archive, look up the expected hash in checksums.txt,
#   compare. Exit 5 on mismatch.
verify_sha256() {
    archive=$1; sums=$2; basename=$3
    sha_tool=$(pick_sha256)
    actual=$($sha_tool "$archive" | awk '{print $1}')
    expected=$(grep "  ${basename}$" "$sums" | awk '{print $1}' | head -n 1)
    [ -n "$expected" ] || err 5 "checksums.txt has no entry for $basename"
    [ "$actual" = "$expected" ] || \
        err 5 "checksum mismatch for $basename (expected $expected, got $actual)"
}

# install_binaries <archive_path> <prefix>
#   Extract the tarball into a tmp dir and copy both binaries to <prefix>
#   with mode 0755. The archive is flat (wrap_in_directory: false).
install_binaries() {
    archive=$1; prefix=$2
    extract_dir=$(mktemp -d "${TMPDIR:-/tmp}/agentbox-install.XXXXXX")
    # Always clean up the extract dir, even on the success path.
    trap 'rm -rf "$extract_dir"' EXIT INT TERM
    tar -xzf "$archive" -C "$extract_dir"
    [ -f "$extract_dir/agentbox" ]            || err 7 "archive missing 'agentbox'"
    [ -f "$extract_dir/agentbox-netfilter" ]  || err 7 "archive missing 'agentbox-netfilter'"
    install -m 0755 "$extract_dir/agentbox"           "$prefix/agentbox"
    install -m 0755 "$extract_dir/agentbox-netfilter" "$prefix/agentbox-netfilter"
}

# strip_quarantine <prefix>
#   On macOS, remove com.apple.quarantine xattr so the binaries don't trip
#   Gatekeeper. Best-effort: ignore "no such xattr" exits (newer macOS).
strip_quarantine() {
    prefix=$1
    [ "$(uname -s)" = "Darwin" ] || return 0
    xattr -d com.apple.quarantine "$prefix/agentbox"           >/dev/null 2>&1 || true
    xattr -d com.apple.quarantine "$prefix/agentbox-netfilter" >/dev/null 2>&1 || true
}

# detect_shell_rc
#   Echo the path to the user's shell rc file based on $SHELL.
#   Returns empty string if it can't determine a target.
#
#   Supported shells:
#     bash -> ~/.bashrc
#     zsh  -> ~/.zshrc
#     fish -> ~/.config/fish/config.fish
detect_shell_rc() {
    case "${SHELL:-}" in
        */bash) printf '%s\n' "$HOME/.bashrc" ;;
        */zsh)  printf '%s\n' "$HOME/.zshrc"  ;;
        */fish) printf '%s\n' "$HOME/.config/fish/config.fish" ;;
        *)      printf '%s\n' "" ;;
    esac
}

# rc_export_line <prefix> <shell>
#   Echo the shell-appropriate export line for adding <prefix> to PATH.
rc_export_line() {
    prefix=$1; shell=$2
    case "$shell" in
        */fish) printf 'fish_add_path %s\n' "$prefix" ;;
        *)      printf 'export PATH="%s:$PATH"\n' "$prefix" ;;
    esac
}

# already_on_path <prefix>
#   Returns 0 if <prefix> is on PATH, 1 otherwise.
already_on_path() {
    prefix=$1
    case ":${PATH:-}:" in
        *":${prefix}:"*) return 0 ;;
        *)               return 1 ;;
    esac
}

# update_path <prefix>
#   Idempotently append a sentinel-wrapped block to the user's shell rc
#   that adds <prefix> to PATH. No-op if:
#     - $AGENTBOX_NO_PATH=1
#     - <prefix> already on $PATH
#     - the rc file already contains the sentinel block
#     - the user's shell isn't recognized
update_path() {
    prefix=$1
    if [ "${AGENTBOX_NO_PATH:-0}" = "1" ]; then
        log "AGENTBOX_NO_PATH=1; skipping shell rc edit"
        return 0
    fi
    if already_on_path "$prefix"; then
        log "$prefix is already on PATH; not editing shell rc"
        return 0
    fi
    rc=$(detect_shell_rc)
    if [ -z "$rc" ]; then
        log "could not detect shell rc; add to PATH manually: $prefix"
        return 0
    fi
    if [ -f "$rc" ] && grep -qF "$SENTINEL_BEGIN" "$rc"; then
        log "$rc already contains the agentbox installer block; leaving as-is"
        return 0
    fi
    mkdir -p "$(dirname "$rc")"
    {
        printf '\n%s\n' "$SENTINEL_BEGIN"
        rc_export_line "$prefix" "${SHELL:-}"
        printf '%s\n'   "$SENTINEL_END"
    } >> "$rc"
    log "added $prefix to PATH in $rc (open a new shell to pick it up)"
}

main() {
    if [ "${AGENTBOX_INSTALL_DEBUG:-0}" = "1" ]; then
        set -x
    fi

    need curl
    need tar
    need uname
    need mkdir
    need install
    need awk
    need grep
    need sed
    pick_sha256 >/dev/null   # validates one of sha256sum/shasum exists

    _os_arch=$(detect_os_arch)
    set -- $_os_arch
    os=$1; arch=$2
    log "detected ${os}/${arch}"

    version=$(resolve_version)
    log "installing version ${version}"

    prefix=${AGENTBOX_PREFIX:-$DEFAULT_PREFIX}
    ensure_prefix "$prefix"
    log "install prefix: $prefix"

    # version-without-leading-v for the archive filename.
    bare=${version#v}
    archive_name="agentbox_${bare}_${os}_${arch}.tar.gz"
    archive_url="${GITHUB_DL}/${version}/${archive_name}"
    sums_url="${GITHUB_DL}/${version}/checksums.txt"

    tmp=$(mktemp -d "${TMPDIR:-/tmp}/agentbox-install.XXXXXX")
    trap 'rm -rf "$tmp"' EXIT INT TERM

    log "downloading $archive_name"
    download "$archive_url" "$tmp/$archive_name"
    log "downloading checksums.txt"
    download "$sums_url"    "$tmp/checksums.txt"

    log "verifying sha256"
    verify_sha256 "$tmp/$archive_name" "$tmp/checksums.txt" "$archive_name"

    log "installing binaries"
    install_binaries "$tmp/$archive_name" "$prefix"
    strip_quarantine "$prefix"

    update_path "$prefix"

    rc=$(detect_shell_rc)
    cat >&2 <<EOF

[agentbox] installed ${version} to ${prefix}
[agentbox] next steps:
  1. open a new shell (or 'source ${rc:-your shell rc}') to pick up PATH
  2. run 'agentbox doctor --fix' to verify the runtime is wired up
  3. cd into a project and 'agentbox run'

uninstall:
  rm "${prefix}/agentbox" "${prefix}/agentbox-netfilter"
  # then remove the block between '${SENTINEL_BEGIN}' and '${SENTINEL_END}'
  # from your shell rc (e.g. ~/.zshrc).

EOF
}

main "$@"
