# Design: Easy install (no clone required)

## Overview

Implements `docs/features/easy-install.md`. Adds a tag-triggered release pipeline
and a `curl | sh` installer so users can land both binaries (`agentbox`,
`agentbox-netfilter`) in `~/.local/bin/` without cloning the repo.

The work is six new/edited files, no Go code:

1. `.goreleaser.yaml` — declares the cross-compile matrix, archive layout,
   checksum file, and changelog format. Single source of truth for release
   artifacts.
2. `.github/workflows/release.yml` — runs goreleaser on `v*` tag push.
3. `scripts/install.sh` — POSIX `sh` installer hosted on the `main` branch via
   raw.githubusercontent.com. Resolves a version, downloads the right tarball,
   verifies its sha256 against `checksums.txt`, copies binaries into the prefix,
   strips macOS quarantine on darwin, and idempotently amends shell rc for PATH.
4. `Makefile` — add a `release-check` target that runs `goreleaser check` and a
   snapshot build for local verification. (Not a release path; the workflow is.)
5. `.gitignore` — add `dist/` (goreleaser's output dir).
6. Doc updates: `README.md` (rewritten Install section), `docs/SPEC.md`
   (Installed binaries note), `docs/VISION.md` (one-line softening), and
   `docs/PROGRESS.md` (entry).

The existing `make install` flow stays. Nothing in `cmd/` or `internal/` changes.
The `internal/version/` package's `Version`/`Commit`/`Date` ldflags contract is
the same one goreleaser will populate, so `agentbox --version` output is
identical between `make install` and a release-tarball install.

## Pre-design verification

CLAUDE.md flags Podman/Docker/CoreDNS/Zellij/agent CLIs as fast-moving. The
release pipeline adds two more: goreleaser and the GitHub Actions ecosystem.
Versions and YAML shapes verified against current upstream docs at design time:

- **goreleaser** v2 (action pin: `~> v2`); archives use plural `formats:` (since
  v2.6); `version: 2` schema header is required at the top of `.goreleaser.yaml`.
- **goreleaser/goreleaser-action** v7 (current major).
- **actions/checkout** v6 — `fetch-depth: 0` is required for changelog generation.
- **actions/setup-go** v6 — `go-version-file: go.mod` reads the project's pinned
  Go (currently `1.25.0`).

If any of these have moved by the time this lands, the implementer pins to the
then-current major and notes the bump in the PR.

## Implementation Units

### Unit 1: `.goreleaser.yaml`

**File:** `.goreleaser.yaml` (repo root)

```yaml
version: 2

project_name: agentbox

before:
  hooks:
    - go mod tidy

builds:
  - id: agentbox
    main: ./cmd/agentbox
    binary: agentbox
    env:
      - CGO_ENABLED=0
    flags:
      - -trimpath
    ldflags:
      - -s -w
      - -X github.com/nklisch/agentbox/internal/version.Version={{.Version}}
      - -X github.com/nklisch/agentbox/internal/version.Commit={{.ShortCommit}}
      - -X github.com/nklisch/agentbox/internal/version.Date={{.Date}}
    goos: [linux, darwin]
    goarch: [amd64, arm64]
    ignore:
      - goos: darwin
        goarch: amd64

  - id: agentbox-netfilter
    main: ./cmd/agentbox-netfilter
    binary: agentbox-netfilter
    env:
      - CGO_ENABLED=0
    flags:
      - -trimpath
    ldflags:
      - -s -w
      - -X github.com/nklisch/agentbox/internal/version.Version={{.Version}}
      - -X github.com/nklisch/agentbox/internal/version.Commit={{.ShortCommit}}
      - -X github.com/nklisch/agentbox/internal/version.Date={{.Date}}
    goos: [linux, darwin]
    goarch: [amd64, arm64]
    ignore:
      - goos: darwin
        goarch: amd64

archives:
  - id: default
    ids:
      - agentbox
      - agentbox-netfilter
    formats: [tar.gz]
    name_template: "agentbox_{{ .Version }}_{{ .Os }}_{{ .Arch }}"
    wrap_in_directory: false
    files:
      - LICENSE
      - README.md

checksum:
  algorithm: sha256
  name_template: "checksums.txt"

changelog:
  use: git
  abbrev: -1
  sort: asc
  filters:
    exclude:
      - "^docs:"
      - "(?i)^chore"
      - "^test:"
      - "(?i)typo"
      - "^Merge "

snapshot:
  version_template: "{{ .Version }}-SNAPSHOT-{{ .ShortCommit }}"

release:
  github:
    owner: nklisch
    name: agentbox
  draft: false
  prerelease: auto
  mode: append
```

**Implementation Notes:**

- The two `builds` entries share matrix and ldflags; they cannot be merged into
  one because each binary has a distinct `main:` and `binary:`.
- `ldflags` mirror the `Makefile`'s `LDFLAGS` exactly (same `-X` paths). The
  one nuance: goreleaser exposes `{{.ShortCommit}}` (7-char), which matches what
  `make` injects via `git rev-parse --short HEAD`. Don't use `{{.FullCommit}}` —
  it diverges from `make install` output and breaks "version is identical" parity.
- `archives.ids` must list both build IDs so a single tarball contains both
  binaries, satisfying the brief's requirement that doctor's sudoers suggestion
  resolves a real path next to `agentbox`.
- `wrap_in_directory: false` produces a flat archive: `tar -xzf` extracts
  `agentbox`, `agentbox-netfilter`, `LICENSE`, `README.md` into the cwd. The
  installer relies on this layout.
- `name_template` does **not** include the `v` prefix (`{{.Version}}` is the tag
  with `v` stripped). This is goreleaser's default; the installer also strips
  the `v` when constructing URLs (`${version#v}`).
- `checksum.name_template: "checksums.txt"` overrides goreleaser's default
  (`{{ .ProjectName }}_{{ .Version }}_checksums.txt`) to a stable, predictable
  filename the installer hard-codes.
- `changelog.use: git` autogenerates from `git log` between tags. `filters.exclude`
  drops noise. `abbrev: -1` strips commit hashes from the rendered notes.
- `snapshot.version_template` lets `goreleaser release --snapshot --clean`
  produce non-publishable artifacts for local verification.
- `release.prerelease: auto` flags any tag containing `-rc`, `-alpha`, etc., as
  a GitHub prerelease. Stable tags (`v0.2.0`) are full releases.
- `release.mode: append` lets a manually-pre-created Release object on GitHub
  receive the artifacts without goreleaser refusing because the Release exists.

**Acceptance Criteria:**

- [ ] `goreleaser check` exits 0 against this file.
- [ ] `goreleaser release --snapshot --clean` produces `dist/` with three
      tarballs (`linux_amd64`, `linux_arm64`, `darwin_arm64`), one
      `checksums.txt`, and a `metadata.json`.
- [ ] `tar -tzf dist/agentbox_*_linux_amd64.tar.gz` lists exactly:
      `agentbox`, `agentbox-netfilter`, `LICENSE`, `README.md` (order may vary).
- [ ] No `dist/agentbox_*_darwin_amd64.tar.gz` is produced (Intel Mac ignored).
- [ ] `file dist/agentbox_*_linux_amd64/agentbox` reports a statically linked
      ELF for x86-64.
- [ ] `dist/agentbox_*_linux_amd64/agentbox --version` prints
      `<version>-SNAPSHOT-<short> (commit <short>, built <date>)` — confirming
      the ldflags landed.
- [ ] `cat dist/checksums.txt` shows three `<sha256>  agentbox_<v>_<os>_<arch>.tar.gz`
      lines, one per archive.

---

### Unit 2: `.github/workflows/release.yml`

**File:** `.github/workflows/release.yml` (new)

```yaml
name: release

on:
  push:
    tags:
      - 'v*'

permissions:
  contents: write

jobs:
  goreleaser:
    runs-on: ubuntu-latest
    steps:
      - name: Checkout
        uses: actions/checkout@v6
        with:
          fetch-depth: 0

      - name: Set up Go
        uses: actions/setup-go@v6
        with:
          go-version-file: go.mod
          cache: true

      - name: Run GoReleaser
        uses: goreleaser/goreleaser-action@v7
        with:
          distribution: goreleaser
          version: '~> v2'
          args: release --clean
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

**Implementation Notes:**

- Single Linux runner suffices because all builds are pure Go cross-compiled
  via `GOOS`/`GOARCH`. No darwin or arm runners needed.
- `fetch-depth: 0` is mandatory: goreleaser needs the full git history to walk
  back from the new tag to the previous one for the changelog.
- `cache: true` on setup-go enables module + build cache across workflow runs.
  Cache key is derived from `go.sum` automatically.
- `version: '~> v2'` floats inside the v2 minor line so security/bug fixes land
  without manual bumps. Major bumps (v3) require a deliberate edit.
- `secrets.GITHUB_TOKEN` is the workflow's auto-provisioned token. It has
  `contents: write` per the job-level `permissions` block, which is enough to
  create/update the Release and upload assets. No PAT needed.
- The trigger `tags: ['v*']` matches `v0.2.0`, `v0.2.0-rc1`, `v1.0.0`. Pushing
  a non-matching tag is a no-op.

**Acceptance Criteria:**

- [ ] `actionlint .github/workflows/release.yml` exits 0.
- [ ] Pushing a tag like `v0.0.0-test` to a fork triggers the workflow.
- [ ] On a green run, the workflow creates (or appends to) a GitHub Release
      with three tarballs and `checksums.txt` attached.
- [ ] `gh release view v0.0.0-test --json assets -q '.assets[].name'` lists
      `agentbox_0.0.0-test_linux_amd64.tar.gz`,
      `agentbox_0.0.0-test_linux_arm64.tar.gz`,
      `agentbox_0.0.0-test_darwin_arm64.tar.gz`,
      `checksums.txt` (order may vary).
- [ ] The Release notes contain the autogenerated changelog (subject lines from
      git log between the previous tag and this one), with `docs:`, `chore:`,
      `test:`, typo, and merge commits filtered out.

---

### Unit 3: `scripts/install.sh`

**File:** `scripts/install.sh` (new, mode `0755`)

POSIX `sh`, no bashisms (no `[[ ]]`, no arrays, no `local`, no `pipefail`).
Verified by `shellcheck -s sh`.

#### File header and globals

```sh
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

REPO="nklisch/agentbox"
GITHUB_API="https://api.github.com/repos/${REPO}"
GITHUB_DL="https://github.com/${REPO}/releases/download"

SENTINEL_BEGIN="# >>> agentbox installer >>>"
SENTINEL_END="# <<< agentbox installer <<<"

DEFAULT_PREFIX="${HOME}/.local/bin"

PROGRAM="agentbox-install"
```

#### Logging helpers

```sh
# log <msg...>
#   Print a status line to stderr, prefixed with [agentbox].
log() {
    printf '[agentbox] %s\n' "$*" >&2
}

# err <msg...>
#   Print an error to stderr and exit with the given code.
#   Usage: err <code> <msg...>
err() {
    code=$1; shift
    printf '[agentbox] error: %s\n' "$*" >&2
    exit "$code"
}
```

#### Prerequisite checks

```sh
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
```

#### OS / arch detection

```sh
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
```

#### Version resolution

```sh
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
```

#### Prefix preparation

```sh
# ensure_prefix <dir>
#   mkdir -p the target dir; verify it's writable.
ensure_prefix() {
    dir=$1
    mkdir -p "$dir" 2>/dev/null || \
        err 6 "cannot create $dir (rerun with sudo, or set AGENTBOX_PREFIX=)"
    [ -w "$dir" ] || \
        err 6 "$dir is not writable (rerun with sudo, or set AGENTBOX_PREFIX=)"
}
```

#### Download + checksum

```sh
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
    expected=$(grep "  ${basename}\$" "$sums" | awk '{print $1}' | head -n 1)
    [ -n "$expected" ] || err 5 "checksums.txt has no entry for $basename"
    [ "$actual" = "$expected" ] || \
        err 5 "checksum mismatch for $basename (expected $expected, got $actual)"
}
```

#### Install + extract

```sh
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
```

#### PATH handling

```sh
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
```

#### Main flow

```sh
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

    set -- $(detect_os_arch)
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
```

**Implementation Notes:**

- The script is one file; the function-by-function presentation above is
  organizational, not file-layout. The file is concatenated in the order shown
  with the `main "$@"` call at the bottom.
- POSIX `sh` constraints honored: no `[[`, no arrays, no `local`, no
  `set -o pipefail`, no `${var,,}` lowercase expansion. Variables are
  function-scoped by convention (caller-affecting names are `os`, `arch`,
  `version`, `prefix`, `tmp` — these are deliberately set in `main`).
- `set -- $(detect_os_arch)` shell-splits the function's stdout into `$1`/`$2`.
  This works because os/arch are short ASCII tokens with no whitespace.
- `verify_sha256`'s grep pattern is `"  <basename>$"` (two spaces, then the
  basename, anchored at end of line) — that's the exact format `sha256sum`
  produces and what goreleaser writes to `checksums.txt`. Anchoring at end of
  line prevents `agentbox_0.2.0_linux_amd64.tar.gz` from accidentally matching
  a substring of `agentbox_0.2.0_linux_amd64.tar.gz.sig` if signing is added
  later.
- The sentinel block is a fixed 3-line shape:
  ```
  # >>> agentbox installer >>>
  export PATH="$HOME/.local/bin:$PATH"
  # <<< agentbox installer <<<
  ```
  Idempotency check is `grep -qF "$SENTINEL_BEGIN"` — `-F` for fixed-string
  match (no regex surprises). Re-running the installer never duplicates.
- `detect_shell_rc` reads `$SHELL` (set by login). If the user runs the
  installer from inside a different shell (e.g. dash for testing), the rc edit
  may target the wrong file. Acceptable: the user can override via
  `AGENTBOX_NO_PATH=1` and edit manually.
- The post-install message references `${rc}`, which is set as a side effect of
  `update_path` calling `detect_shell_rc`. Because `update_path` may early-return
  before setting `rc`, the heredoc uses `${rc:-your shell rc}` for safety.
- `mktemp -d "${TMPDIR:-/tmp}/agentbox-install.XXXXXX"` works on both Linux
  (`mktemp` from coreutils) and macOS (`mktemp` from BSD). Both honor the
  `XXXXXX` template suffix.
- The trap inside `install_binaries` shadows the outer `main`-level trap
  briefly. Both target different tmp dirs, so the order of cleanup doesn't
  matter; the outer trap runs last on normal exit.
- `curl --retry 3 --retry-delay 2` covers transient flakes against
  `objects.githubusercontent.com` (release asset CDN). If both sites are
  down for >10s, the script fails fast.

**Acceptance Criteria:**

- [ ] `shellcheck -s sh scripts/install.sh` exits 0.
- [ ] On a Linux x86_64 host with curl/tar/sha256sum, running the script with
      `AGENTBOX_VERSION=v0.2.0` (an actual release) leaves
      `~/.local/bin/agentbox` and `~/.local/bin/agentbox-netfilter` executable
      and exits 0.
- [ ] `~/.local/bin/agentbox --version` reports `0.2.0`.
- [ ] On a host where `~/.local/bin` is not on PATH, running the installer
      appends one sentinel block to `~/.zshrc` (or the appropriate rc).
- [ ] Running the installer a second time produces exactly one sentinel block
      in the rc file (idempotent).
- [ ] `AGENTBOX_NO_PATH=1` causes the installer to skip rc-file editing
      entirely — no sentinel block appears.
- [ ] `AGENTBOX_PREFIX=/tmp/abx-test` installs to that prefix; the rc edit
      uses that path in the export line.
- [ ] On a writeable-prefix-required path (e.g. `AGENTBOX_PREFIX=/usr/local/bin`
      without sudo), the script exits 6 and the prefix is unchanged.
- [ ] Corrupting the downloaded tarball (e.g. `truncate -s 1024` on the temp
      file before `verify_sha256`) — simulated in tests by editing the
      checksums file — exits 5 and leaves the prefix unchanged.
- [ ] On macOS, `xattr -p com.apple.quarantine ~/.local/bin/agentbox`
      exits non-zero after install (attribute removed). First `agentbox`
      invocation does not pop a Gatekeeper dialog.
- [ ] `uname -s` reporting an unsupported value (e.g. spoofed to `FreeBSD`
      via a wrapper) exits 2 with a "build from source" message.
- [ ] `AGENTBOX_INSTALL_DEBUG=1` enables `set -x` (verifiable by stderr
      containing `+ ` lines).

---

### Unit 4: `Makefile` additions

**File:** `Makefile` (edit)

```make
.PHONY: build test vet install clean release-check

# ... existing targets ...

release-check:
	goreleaser check
	goreleaser release --snapshot --clean --skip=publish
```

**Implementation Notes:**

- `release-check` is a developer aid, not a release path. The brief explicitly
  excludes a `make release` target. This target only validates the
  `.goreleaser.yaml` and produces unpublished local artifacts under `dist/`.
- `--skip=publish` ensures the target never reaches GitHub even if invoked from
  a tag-state repo with a token in env.
- Add `release-check` to the `.PHONY` line alongside the existing targets.

**Acceptance Criteria:**

- [ ] `make release-check` exits 0 on a clean checkout (assumes goreleaser is
      installed locally — the workflow installs it in CI; locally the dev runs
      `brew install goreleaser` or downloads a binary).
- [ ] After `make release-check`, `dist/` contains the snapshot artifacts.
- [ ] `make release-check` does not network-publish (no GitHub Release
      created).

---

### Unit 5: `.gitignore` additions

**File:** `.gitignore` (edit)

Append:

```
# goreleaser output
dist/
```

**Acceptance Criteria:**

- [ ] After running `make release-check`, `git status` does not list anything
      under `dist/`.

---

### Unit 6: `README.md` Install rewrite

**File:** `README.md` (edit lines ~35–125, the `## Install` section)

Replace the existing `## Install` section with the structure below. Other
sections (Quickstart, Common operations, Network policy, Configuration,
Nested containers, Shell completion, Uninstall, Documentation, Status, License)
are untouched.

```markdown
## Install

Three options. Pick one.

### Option 1: `curl | sh` (recommended)

```sh
curl -fsSL https://raw.githubusercontent.com/nklisch/agentbox/main/scripts/install.sh | sh
```

Drops `agentbox` and `agentbox-netfilter` into `~/.local/bin/`, verifies the
sha256 against the release's `checksums.txt`, and appends a sentinel-wrapped
block to your shell rc so `~/.local/bin` is on `PATH`. On macOS it also strips
the Gatekeeper quarantine attribute.

Environment overrides:

| Variable | Effect |
| -------- | ------ |
| `AGENTBOX_VERSION=v0.2.0` | Pin to a specific release tag (default: latest) |
| `AGENTBOX_PREFIX=/usr/local/bin` | Install elsewhere (default: `~/.local/bin`) |
| `AGENTBOX_NO_PATH=1` | Skip the shell-rc PATH edit |
| `AGENTBOX_INSTALL_DEBUG=1` | Enable `set -x` tracing |

Open a new shell, then:

```sh
agentbox --version
agentbox doctor --fix
```

### Option 2: download a release tarball

Browse [releases](https://github.com/nklisch/agentbox/releases) and grab the
archive for your platform:

| Platform | Archive |
| -------- | ------- |
| Linux x86_64 | `agentbox_<version>_linux_amd64.tar.gz` |
| Linux arm64 | `agentbox_<version>_linux_arm64.tar.gz` |
| macOS Apple Silicon | `agentbox_<version>_darwin_arm64.tar.gz` |

```sh
tar -xzf agentbox_*.tar.gz
install -m 0755 agentbox agentbox-netfilter ~/.local/bin/
```

Verify against `checksums.txt` (also attached to each release):

```sh
sha256sum -c <(grep agentbox_*_linux_amd64.tar.gz checksums.txt)
```

### Option 3: `go install`

If you already have Go (≥1.25):

```sh
go install github.com/nklisch/agentbox/cmd/agentbox@latest
go install github.com/nklisch/agentbox/cmd/agentbox-netfilter@latest
```

`agentbox --version` will report `(devel)` because `go install` doesn't run
the project's `-ldflags`. Use Option 1 or 2 if you want a stamped version.

### macOS Apple Silicon: initialize Podman

```sh
podman machine init       # one-time
podman machine start
```

`agentbox doctor --fix` will start a stopped machine for you on later runs.

### Linux: configure passwordless sudo for `safe`/`allowlist`

These network modes need `iptables`, `ipset`, and `agentbox-netfilter` to run
as root. `agentbox doctor` resolves the right paths for your distro:

```sh
agentbox doctor      # look for the [WARN] sudo-iptables line
```

Drop the suggested line into `/etc/sudoers.d/agentbox` (mode `0440`, owner
`root`) and validate with `sudo visudo -c`.

If you skip this, run with `--network open` (less safe, no setup) or
`--network off` (no network).

### Verify

```sh
agentbox doctor --fix
```

Runs ten checks. `--fix` pulls the CoreDNS image and starts a stopped
`podman machine`; everything else is reported with the corrective action.

### Building from source

For development or distros without prebuilt binaries:

```sh
git clone https://github.com/nklisch/agentbox.git
cd agentbox
make install
```

`make install` builds both static binaries (`CGO_ENABLED=0`) and copies them
to `~/.local/bin/`. Override the prefix by hand if needed:

```sh
make build
install -m 0755 agentbox /usr/local/bin/agentbox
install -m 0755 agentbox-netfilter /usr/local/bin/agentbox-netfilter
```
```

**Implementation Notes:**

- The current README's "Install" section is steps 1–5. After rewrite, the
  three install options become the new lead, with the macOS/Linux-sudoers
  steps preserved (now under "macOS Apple Silicon" and "Linux: configure
  passwordless sudo"). "Verify" and "Building from source" follow.
- The Requirements table at the top of the README (lines ~19–32) needs one
  edit: drop the `make` and `Go ≥ 1.25` requirements from the user-facing
  table, since they only apply to "Building from source." Move them under the
  "Building from source" subsection.
- The `## Uninstall` section at the bottom of the README needs a one-line
  addition pointing at the sentinel-block removal:
  ```
  # If you installed via curl|sh, also remove the sentinel-wrapped block
  # from your shell rc (~/.zshrc, ~/.bashrc, or ~/.config/fish/config.fish):
  #   sed -i '/# >>> agentbox installer >>>/,/# <<< agentbox installer <<</d' ~/.zshrc
  ```

**Acceptance Criteria:**

- [ ] The Install section's first heading after `## Install` is "Option 1: `curl | sh` (recommended)".
- [ ] All three install options appear in numbered order.
- [ ] The Requirements table no longer lists `Go` or `make` as required for
      the common path.
- [ ] The Uninstall section includes the `sed` snippet for sentinel-block
      removal.
- [ ] The "Building from source" subsection still documents `git clone +
      make install` so the dev path is intact.
- [ ] No broken intra-doc links: every `docs/...` reference in the rewritten
      section resolves to a file.

---

### Unit 7: `docs/SPEC.md` Installed-binaries note

**File:** `docs/SPEC.md` (edit the existing `## Installed binaries` section)

Replace the current paragraph (lines ~401–409) with:

```markdown
## Installed binaries

Two binaries ship together; they must always live in the same prefix because
`agentbox doctor`'s sudoers suggestion resolves `agentbox-netfilter` by real
path.

- **`agentbox`** — the primary CLI.
- **`agentbox-netfilter`** — a small daemon that tails `podman logs --follow`
  of the CoreDNS sidecar and populates an ipset (`abx-<12hex>-a`) used by an
  iptables FORWARD rule. Launched by the CLI when `block_direct_ip = true` or
  `mode = allowlist`; must be reachable via `sudo -n agentbox-netfilter` (add
  to sudoers alongside `iptables` / `ipset`).

Install paths (canonical → fallback):

1. **`curl | sh` installer** — `scripts/install.sh` on the `main` branch,
   served via `raw.githubusercontent.com`. Default prefix `~/.local/bin`,
   overridable with `AGENTBOX_PREFIX`. Verifies sha256 against the release's
   `checksums.txt`. Idempotently amends the user's shell rc.
2. **Prebuilt release tarballs** on GitHub Releases, named
   `agentbox_<version>_<os>_<arch>.tar.gz` for `linux/amd64`, `linux/arm64`,
   `darwin/arm64`. Each release also publishes `checksums.txt` (sha256). No
   `darwin/amd64` build.
3. **`go install`** for users who already have Go ≥1.25; lacks the version
   ldflags injection, so `--version` reports `(devel)`.
4. **`make install`** from a clone — the development path, retained for
   local iteration.
```

**Acceptance Criteria:**

- [ ] The section enumerates all four install paths.
- [ ] The "must live in the same prefix" constraint is explicit.

---

### Unit 8: `docs/VISION.md` softening

**File:** `docs/VISION.md` (edit one bullet)

Change:

```markdown
- **Not for distribution.** No marketing, no onboarding, no Homebrew tap (yet). If it
  spreads, fine. That's not why I'm building it.
```

to:

```markdown
- **Not a marketed product.** No funnel, no onboarding, no Homebrew tap. Prebuilt
  binaries exist (`curl | sh` and tarballs on GitHub Releases) so installing on a
  fresh laptop is one command, but that's a calibration for the author's own
  reprovisions — not a distribution push. If it spreads, fine. That's not why
  I'm building it.
```

**Acceptance Criteria:**

- [ ] The "Not for distribution" line no longer contradicts the existence of
      prebuilt-binary install paths.

---

### Unit 9: `docs/PROGRESS.md` entry

**File:** `docs/PROGRESS.md` (append to the most appropriate section)

Add a short entry under the "Notable deviations from VISION/SPEC after
implementation" or equivalent section. Suggested wording:

```markdown
7. **Easy install landed (post-v0.1.0).** `scripts/install.sh` (curl | sh),
   prebuilt tarballs on GitHub Releases via tag-triggered goreleaser, and a
   documented `go install` path. Targets: linux/amd64, linux/arm64,
   darwin/arm64. The legacy `make install` flow is preserved for development.
   Homebrew tap and code signing remain deferred.
```

**Acceptance Criteria:**

- [ ] PROGRESS.md mentions the install story landing and the supported
      platform matrix.
- [ ] The note clarifies that `make install` remains.

---

## Implementation Order

Build in this order. Each unit's acceptance criteria must pass before moving on.

1. **Unit 1** — `.goreleaser.yaml`. Verify with `goreleaser check` and
   `goreleaser release --snapshot --clean --skip=publish`.
2. **Unit 5** — `.gitignore` add `dist/`. Trivial; do this before snapshot
   builds leave artifacts staged.
3. **Unit 4** — `Makefile release-check`. Wraps Unit 1's commands so the rest
   of the units can be iterated against `make release-check`.
4. **Unit 2** — `.github/workflows/release.yml`. Verify with `actionlint`. End
   to end verification requires pushing a tag (do this in a fork or against a
   throwaway tag like `v0.0.0-test`).
5. **Unit 3** — `scripts/install.sh`. Verify with `shellcheck -s sh` and the
   harness in the Testing section. Cannot test the full happy path without a
   real release artifact (chicken-and-egg with Unit 2). Use a snapshot tarball
   served from a local HTTP server during development.
6. **Unit 6** — `README.md` rewrite. Final, because it documents the
   completed contract from Units 1–3.
7. **Unit 7** — `docs/SPEC.md` Installed-binaries section.
8. **Unit 8** — `docs/VISION.md` softening (one bullet).
9. **Unit 9** — `docs/PROGRESS.md` entry.

Doc edits last so they document final, settled behavior.

## Testing

This feature ships shell + YAML, not Go. The project's existing
`go test ./...` is untouched; the new tests live under `scripts/test/` and
are not wired into `go test` because they don't run Go. They run via
`make test-install` (a new `.PHONY` target — implementer adds it alongside
`release-check`).

### Lint gates (run on every change)

```sh
shellcheck -s sh scripts/install.sh
actionlint .github/workflows/release.yml
goreleaser check
```

### Unit tests for `scripts/install.sh`

`scripts/test/install_test.sh` — POSIX `sh` test harness. Runs the installer
against a fake "release" served from a local HTTP server.

#### Test harness fixture

```sh
# Layout:
#   scripts/test/install_test.sh          # the test runner
#   scripts/test/fixtures/serve.sh        # python -m http.server wrapper
#   scripts/test/fixtures/release/
#     v0.0.0-test/
#       agentbox_0.0.0-test_linux_amd64.tar.gz
#       agentbox_0.0.0-test_linux_arm64.tar.gz
#       agentbox_0.0.0-test_darwin_arm64.tar.gz
#       checksums.txt
#       latest.json   # mock /releases/latest response
```

The test runner builds the fixture archives at startup using the local
`agentbox` and `agentbox-netfilter` binaries (so the snapshot from
`goreleaser release --snapshot` can be reused if available; otherwise a
trivial `echo 'fake' > agentbox; tar -czf …` archive is sufficient — the
installer doesn't execute the binaries).

The runner then exports overrides so the installer points at the local
server instead of GitHub:

```sh
GITHUB_API="http://localhost:$PORT/api"
GITHUB_DL="http://localhost:$PORT/releases/download"
```

These cannot be overridden by env in the script-as-shipped — they're
constants. **Implementation accommodation:** the implementer adds two
non-public env-var overrides, intended for testing only:

```sh
# In install.sh, replace the constants with:
REPO="${AGENTBOX_TEST_REPO:-nklisch/agentbox}"
GITHUB_API="${AGENTBOX_TEST_API:-https://api.github.com/repos/${REPO}}"
GITHUB_DL="${AGENTBOX_TEST_DL:-https://github.com/${REPO}/releases/download}"
```

These are documented in a comment as "test seam, not user-facing" and
aren't mentioned in README.

#### Test cases

Each test is a `test_<name>` shell function; the runner executes them with
fresh `$HOME` and `$AGENTBOX_PREFIX` per test (using `mktemp -d`). Failures
print the command, expected, actual, and exit non-zero.

| Test | Setup | Assert |
| ---- | ----- | ------ |
| `test_happy_path_linux_amd64` | Fake `uname` wrapper returns `Linux x86_64`. Set `AGENTBOX_VERSION=v0.0.0-test`. | Exits 0; `$prefix/agentbox` and `$prefix/agentbox-netfilter` exist with mode 0755. |
| `test_resolves_latest_when_unset` | Server returns `latest.json` with `tag_name: "v0.0.0-test"`. | Exits 0; same files exist. |
| `test_unsupported_os` | Wrapper `uname -s` echoes `FreeBSD`. | Exits 2; prefix empty. |
| `test_unsupported_arch` | Wrapper `uname -m` echoes `mips64`. | Exits 2; prefix empty. |
| `test_intel_mac_blocked` | `uname -s = Darwin, -m = x86_64`. | Exits 2 with "darwin/amd64" in stderr; prefix empty. |
| `test_checksum_mismatch` | Truncate the tarball after fixture build but before serving. | Exits 5; prefix empty. |
| `test_missing_checksum_entry` | Remove the entry for the platform's archive from `checksums.txt`. | Exits 5; prefix empty. |
| `test_unwritable_prefix` | `AGENTBOX_PREFIX=/proc/install` (always read-only). | Exits 6; nothing created under proc. |
| `test_path_append_idempotent` | Empty `~/.zshrc`. Run installer twice. | Exactly one sentinel block in `~/.zshrc`. |
| `test_path_skipped_when_already_on_path` | Pre-populate `PATH` with `$prefix`. | No change to `~/.zshrc`. |
| `test_path_skipped_when_no_path_env` | Set `AGENTBOX_NO_PATH=1`. | No change to `~/.zshrc`. |
| `test_path_fish` | `SHELL=/usr/bin/fish`. | `~/.config/fish/config.fish` gains a sentinel block with `fish_add_path`. |
| `test_path_unknown_shell` | `SHELL=/bin/dash` (not bash/zsh/fish). | Exits 0; no rc file written; "could not detect shell rc" in stderr. |
| `test_quarantine_strip_darwin` | Wrapper `uname -s = Darwin`, mock `xattr` that records its args. | `xattr -d com.apple.quarantine` invoked twice (once per binary). |
| `test_quarantine_skip_linux` | `uname -s = Linux`, mock `xattr` that fails if called. | Mock not invoked; install succeeds. |
| `test_curl_failure_archive` | Stop the HTTP server after the API call returns. | Exits 4 with "download failed". |
| `test_curl_failure_api` | Server's `/api/...` returns 500. | Exits 4 with "could not resolve latest release tag". |
| `test_pinned_version_skips_api` | `AGENTBOX_VERSION=v0.0.0-test`, server's `/api/...` returns 500. | Exits 0 (the API is never queried). |
| `test_install_debug_traces` | `AGENTBOX_INSTALL_DEBUG=1`. | Stderr contains lines starting with `+ ` (set -x output). |

#### Mocking helpers

```sh
# scripts/test/fixtures/uname-wrap.sh
#   Drop-in PATH shim that echoes UNAME_S/UNAME_M before falling through to
#   the real uname for unmatched flags. Used by tests that simulate
#   non-host platforms.

# scripts/test/fixtures/xattr-wrap.sh
#   Records args to a log file under $XATTR_LOG. Always exits 0.
```

Both shims are tiny `sh` scripts that the test runner prepends to `PATH`
before invoking the installer.

### `.goreleaser.yaml` validation

```sh
# In CI, after Unit 1 lands:
goreleaser check
goreleaser release --snapshot --clean --skip=publish
test -f dist/agentbox_*-SNAPSHOT-*_linux_amd64.tar.gz
test -f dist/agentbox_*-SNAPSHOT-*_linux_arm64.tar.gz
test -f dist/agentbox_*-SNAPSHOT-*_darwin_arm64.tar.gz
test ! -f dist/agentbox_*-SNAPSHOT-*_darwin_amd64.tar.gz
test -f dist/checksums.txt
test "$(wc -l < dist/checksums.txt)" -eq 3
# Verify archive layout:
mkdir /tmp/abx-extract && tar -xzf dist/agentbox_*_linux_amd64.tar.gz -C /tmp/abx-extract
test -x /tmp/abx-extract/agentbox
test -x /tmp/abx-extract/agentbox-netfilter
test -f /tmp/abx-extract/LICENSE
test -f /tmp/abx-extract/README.md
test ! -d /tmp/abx-extract/agentbox_*
# Verify version stamp:
/tmp/abx-extract/agentbox --version | grep -q SNAPSHOT
```

### `.github/workflows/release.yml` validation

```sh
actionlint .github/workflows/release.yml
# End-to-end: push a v0.0.0-test tag to a fork, observe a workflow run that
# uploads the same artifacts the snapshot produced (minus the SNAPSHOT
# suffix). This is a manual check; not automated.
```

### Doc tests

```sh
# README links resolve:
grep -oE '\(docs/[^)]+\)' README.md | tr -d '()' | while read p; do
  test -f "$p" || { echo "broken link: $p"; exit 1; }
done

# README mentions all three install options:
grep -q "curl | sh"   README.md
grep -q "release tarball" README.md
grep -q "go install"  README.md
```

## Verification Checklist

After all units land, run:

```sh
# Lint
shellcheck -s sh scripts/install.sh
actionlint .github/workflows/release.yml
goreleaser check

# Build the snapshot
make release-check

# Confirm artifacts
ls dist/agentbox_*_linux_amd64.tar.gz \
   dist/agentbox_*_linux_arm64.tar.gz \
   dist/agentbox_*_darwin_arm64.tar.gz \
   dist/checksums.txt

# Run the installer test harness
make test-install

# (Manual) Push a throwaway tag to a fork and observe the workflow:
git tag v0.0.0-test
git push origin v0.0.0-test
gh run watch
gh release view v0.0.0-test
gh release delete v0.0.0-test --cleanup-tag

# (Manual) Smoke-test the installer against the throwaway tag:
AGENTBOX_VERSION=v0.0.0-test \
  curl -fsSL https://raw.githubusercontent.com/<fork>/agentbox/main/scripts/install.sh | sh
~/.local/bin/agentbox --version
```

## Notes for the implementer

- **Don't add a `make release` target.** The brief explicitly excludes it.
  `make release-check` is a snapshot validator, not a release path.
- **Don't add code signing.** Out of scope for v0.2; checksums-only.
- **Don't add Homebrew formula generation in `.goreleaser.yaml`.** Even though
  goreleaser supports `brews:` blocks, the brief excludes a Homebrew tap.
- **Don't add a `--prerelease` flag to `install.sh`.** Users who want a
  prerelease set `AGENTBOX_VERSION` explicitly.
- **Do mirror the `Makefile`'s LDFLAGS exactly in `.goreleaser.yaml`.**
  Divergence between `make install` and a tarball install means
  `agentbox --version` reports differently for the same source — confusing.
- **Do verify archive layout in CI before tagging the first release.** The
  installer hard-codes filenames; goreleaser config drift would break it
  silently.
