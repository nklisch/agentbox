# Feature: Easy install (no clone required)

## Summary

Today, the only way to get agentbox onto a host is `git clone + make install`. That's
fine for the author but high-friction for "I want to try this on a new laptop." This
feature adds three install paths that don't require cloning the repo:

1. A `curl | sh` one-liner that downloads prebuilt binaries from GitHub Releases,
   verifies checksums, drops them in `~/.local/bin`, and ensures `PATH` is set up.
2. Prebuilt release tarballs (per OS/arch) on GitHub Releases for users who prefer
   to download and extract manually.
3. A documented `go install` path for users who already have Go and don't want a
   tarball at all.

The release artifacts are produced by a tag-triggered GitHub Actions workflow using
goreleaser. The dev workflow (`make install` from a clone) is preserved unchanged.

## Requirements

### `curl | sh` installer

- Hosted at a stable URL: `https://raw.githubusercontent.com/nklisch/agentbox/main/scripts/install.sh`.
  Source lives in `scripts/install.sh` in the repo.
- One-liner shape, documented in README:
  ```sh
  curl -fsSL https://raw.githubusercontent.com/nklisch/agentbox/main/scripts/install.sh | sh
  ```
- **Acceptance:** running the one-liner on a clean Linux x86_64 host with `curl` and
  a libc available leaves `agentbox` and `agentbox-netfilter` executable in
  `~/.local/bin/`, prints the resolved version, and exits 0.
- **OS/arch detection:** the script reads `uname -s` / `uname -m` and maps to one of
  the supported targets (`linux/amd64`, `linux/arm64`, `darwin/arm64`). Unsupported
  combos exit non-zero with a clear "no prebuilt binary for `<os>/<arch>`; build from
  source: …" message.
- **Version selection:**
  - Default: resolve the latest release via the GitHub API
    (`https://api.github.com/repos/nklisch/agentbox/releases/latest`) and install
    that tag.
  - Override: `AGENTBOX_VERSION=v0.2.0 curl … | sh` pins to a specific tag.
- **Install prefix:**
  - Default: `~/.local/bin`.
  - Override: `AGENTBOX_PREFIX=/usr/local/bin curl … | sh`.
  - The script does not `sudo` — if the prefix isn't writable, it exits non-zero
    with a "rerun with sudo or pick a writable prefix" message.
- **Checksum verification:** the script downloads both the platform tarball and the
  release's `checksums.txt`, verifies the SHA-256 with `sha256sum` (or
  `shasum -a 256` on macOS), and aborts on mismatch.
  **Acceptance:** corrupting the downloaded tarball before verification causes the
  installer to exit non-zero without copying any binary into the prefix.
- **PATH handling (auto-append, rustup-style):**
  - If the chosen prefix is already on `$PATH`, do nothing.
  - Otherwise, detect the user's login shell (via `$SHELL`) and append a one-line
    `export PATH=...` to the appropriate rc file (`~/.zshrc`, `~/.bashrc`,
    `~/.config/fish/config.fish`). Each line is wrapped in a sentinel comment
    (`# >>> agentbox installer >>>` / `# <<< agentbox installer <<<`) so the
    uninstaller can find it.
  - Idempotent: re-running the installer does not duplicate the block.
  - **Acceptance:** running the installer twice in a row leaves exactly one
    sentinel block in the shell rc file.
- **Both binaries shipped together.** `agentbox-netfilter` is required for `safe`
  and `allowlist` network modes and its real path is referenced in the sudoers
  suggestion that `agentbox doctor` prints. The installer must place both binaries
  in the same prefix.
- **Idempotency:** re-running the installer with the same `AGENTBOX_VERSION`
  overwrites the binaries and leaves PATH alone.
- **No execution of agentbox itself.** The installer never invokes
  `agentbox doctor` or any other agentbox subcommand — it only copies files and
  edits shell rc. The post-install message tells the user to run `agentbox doctor`.
- **Tracing:** `AGENTBOX_INSTALL_DEBUG=1 curl … | sh` enables `set -x` and prints
  the resolved version, OS/arch, prefix, and download URLs.
- **macOS Gatekeeper:** when `uname -s` is `Darwin`, the installer runs
  `xattr -d com.apple.quarantine "$AGENTBOX_PREFIX/agentbox" "$AGENTBOX_PREFIX/agentbox-netfilter"`
  after copy, ignoring the "no such xattr" exit code (newer macOS versions don't
  always set the attribute). Without this, the first `agentbox` invocation pops
  the "cannot verify developer" system dialog. The user already authenticated to
  download via their own shell, so stripping quarantine is friction-removal, not
  a security regression.
  **Acceptance:** on macOS 15+, `agentbox --version` runs without a Gatekeeper
  dialog immediately after install.
- **No GitHub authentication.** The installer never reads `GITHUB_TOKEN`. The
  unauthenticated GitHub API quota (60 req/h per IP) is sufficient for this
  tool's install cadence. Users who hit the cap can pin `AGENTBOX_VERSION` —
  setting it skips the `releases/latest` lookup entirely and only the tarball
  download (which goes to `github.com`, not `api.github.com`) remains.
- **Uninstall hint:** the post-install message documents the inverse:
  `rm "$AGENTBOX_PREFIX/agentbox" "$AGENTBOX_PREFIX/agentbox-netfilter"`,
  plus `sed -i` for the sentinel-wrapped block in shell rc.

### Prebuilt release tarballs (GitHub Releases)

- **Targets:** `linux/amd64`, `linux/arm64`, `darwin/arm64`. (Intel Mac is
  intentionally skipped — README explicitly calls out Apple Silicon.)
- **Archive layout:** each `.tar.gz` contains, at the top level:
  ```
  agentbox
  agentbox-netfilter
  LICENSE
  README.md
  ```
  No nested directory. Extraction with `tar -xzf` into a chosen prefix Just Works.
- **Naming:** `agentbox_<version>_<os>_<arch>.tar.gz`. Example:
  `agentbox_0.2.0_linux_amd64.tar.gz`.
- **Checksums:** a single `checksums.txt` in the release containing one
  `sha256  filename` line per archive.
- **Static binaries:** built with `CGO_ENABLED=0`, `-trimpath`, and the existing
  `LDFLAGS` from the `Makefile` (so `agentbox --version` reports the tag, commit,
  and date).
- **Acceptance:** `tar -tzf agentbox_<v>_linux_amd64.tar.gz` lists exactly the four
  files above and `file agentbox` reports `statically linked`.
- **Reproducibility:** the workflow records the goreleaser version in the release
  notes so a build can be repeated by hand.

### `go install` path

- Documented in README as the third install option.
  ```sh
  go install github.com/nklisch/agentbox/cmd/agentbox@latest
  go install github.com/nklisch/agentbox/cmd/agentbox-netfilter@latest
  ```
- **Acceptance:** on a host with Go ≥ 1.25, `go install ...@latest` succeeds and
  the resulting `$GOPATH/bin/agentbox --version` reports a non-empty version
  string. (Version may be `(devel)` because `go install` doesn't run our LDFLAGS;
  call this out in the README.)
- No code changes are required for this path beyond the existing `cmd/agentbox`
  and `cmd/agentbox-netfilter` entry points.

### Release pipeline (GitHub Actions + goreleaser)

- File: `.github/workflows/release.yml`.
- **Trigger:** `push` of a tag matching `v*` (e.g. `v0.2.0`).
- **Job:** single Linux runner (ubuntu-latest). Cross-compiles all three target
  triples via `GOOS`/`GOARCH`. No CGO, so no per-OS runners needed.
- **Tooling:** `goreleaser/goreleaser-action` with a checked-in `.goreleaser.yaml`.
- **Permissions:** `contents: write` for uploading release assets via the
  workflow's `GITHUB_TOKEN`. No PATs.
- **Side effect:** the workflow creates the GitHub Release object if it doesn't
  exist (or updates a manually-created one), uploads the three tarballs and
  `checksums.txt`, and writes a release-notes block.
- **Release notes:** autogenerated by goreleaser from `git log` between the
  previous tag and the new tag. No hand-edited `CHANGELOG.md`. The
  `.goreleaser.yaml` `changelog` block uses `use: github` (or `git`) with sane
  default exclusions (chore-scope commits, merges) so the rendered notes stay
  readable.
- **Acceptance:** pushing `v0.2.0-test` to a fork triggers the workflow, which
  produces three tarballs, a `checksums.txt`, and a Release object with all four
  artifacts attached, all within ~2 minutes.
- **No PR / branch builds.** Build verification on PRs is a separate concern and
  out of scope here.
- **Fallback:** a documented `make release VERSION=v0.2.0` target is **not** part
  of this feature. If the workflow is broken, the dev rebuilds locally and uploads
  with `gh release upload`.

### Documentation

- **README.md:** the `## Install` section is rewritten so the first option is
  `curl | sh`, the second is "download a tarball from Releases", the third is
  `go install`, and "build from source" moves to a "Building from source" appendix.
  The opening copy says "no clone needed for the common path."
- **VISION.md:** the "Not for distribution. No marketing, no onboarding, no
  Homebrew tap (yet)" line is softened — the prebuilt-binary path is the easy
  install story, not a distribution push. Wording is design's call; the bullet
  doesn't have to disappear, just stop contradicting the new install paths.
- **SPEC.md `## Installed binaries`:** add a one-paragraph note that the canonical
  install paths are `curl | sh` and prebuilt tarballs, with `make install`
  preserved for development work.
- **PROGRESS.md:** add a short entry under known issues / future work flipping the
  "first run is `pull` not `build` (v0.3+)" note to a sibling that says the binary
  install story landed in this version.

## Scope

**In scope:**
- `scripts/install.sh` (curl | sh installer with checksum verification, version/prefix
  overrides, idempotent shell-rc PATH editing).
- `.goreleaser.yaml` for the three target triples with the archive layout above.
- `.github/workflows/release.yml` triggering goreleaser on `v*` tag pushes.
- README rewrite of the install section.
- VISION.md / SPEC.md / PROGRESS.md adjustments.
- A short post-install message that tells the user to run `agentbox doctor`.

**Out of scope:**
- **Homebrew tap.** Considered and dropped from this feature; revisit later if the
  user base grows beyond the author.
- **Distro packages** (apt/yum/AUR/COPR/nixpkgs).
- **`agentbox self-update`.** Users rerun the installer or `go install`.
- **Code signing** (cosign / minisign / GPG). Checksums-only for v0.2.
- **Windows builds.** Out per VISION.md.
- **Intel Mac (`darwin/amd64`).** Skipped per the OS/arch decision.
- **CI for non-tag builds** (PR smoke tests, lint, etc.). Separate concern.
- **Sudoers automation** for the `safe`/`allowlist` network modes. Existing
  `agentbox doctor` flow is unchanged.
- **Container-runtime install** (podman/docker). Still the user's responsibility;
  the installer doesn't try to install runtimes.
- **Removing `make install`.** It stays for the dev workflow.

## Technical Context

- **Existing code touched:**
  - `Makefile` — no changes required, but the `LDFLAGS` block defines the
    canonical version-injection contract that `.goreleaser.yaml` must mirror.
  - `cmd/agentbox/` and `cmd/agentbox-netfilter/` — entry points, unchanged.
  - `internal/version/` — already exposes `Version`, `Commit`, `Date`. goreleaser
    populates these via `-X` flags; the `make install` flow already does the
    same so behavior is identical.
  - `internal/doctor/` — the sudoers-suggestion code resolves the real path of
    `agentbox-netfilter` via `os.Executable()` and `filepath.EvalSymlinks`; this
    keeps working regardless of install method.
  - `README.md`, `docs/VISION.md`, `docs/SPEC.md`, `docs/PROGRESS.md` — doc
    updates only.

- **New artifacts:**
  - `scripts/install.sh` (POSIX `sh`, no bashisms — runs under dash, bash, zsh).
  - `.goreleaser.yaml`.
  - `.github/workflows/release.yml`.

- **Dependencies:**
  - `goreleaser/goreleaser-action@v6` (or current major) for the workflow.
  - `actions/checkout@v4` and `actions/setup-go@v5` (or current majors).
  - The installer relies only on POSIX `sh`, `curl`, `tar`, `uname`, `mkdir`,
    `install`, and one of `sha256sum` / `shasum`. No `jq` — parse the GitHub
    "latest release" JSON with `grep`/`sed` for the `tag_name` field. (This is
    the standard rustup/nvm tactic and survives without extra deps.)

- **Constraints:**
  - **Stable URL.** The README one-liner pins `main` for `install.sh`, so the
    script must stay backward-compatible: never break `AGENTBOX_VERSION` or
    `AGENTBOX_PREFIX` semantics, and always be parseable by older copies in user
    shells.
  - **No CGO.** All targets are pure Go; goreleaser must set `CGO_ENABLED=0`.
  - **No internet at install-decision time** beyond GitHub. The installer talks
    only to `api.github.com` and `github.com`; nothing else.
  - **No execution of downloaded binaries** by the installer itself. Trust the
    checksum, copy the file, exit. The user runs `agentbox doctor` next.
  - **Single-user calibration.** Per VISION.md, this is not a marketing push;
    decisions favor the author's workflow. The `curl | sh` UX is calibrated for
    the author's own laptop reprovisions, not a v1 onboarding funnel.

## Open Questions

None blocking. Items deferred to v0.3+ and called out in scope rather than as
open questions:

- **Cosign / minisign signatures.** Checksums-only for v0.2; signatures are the
  natural follow-on if the project grows past one user.
- **`--prerelease` flag in the installer.** v0.2 ships stable-only via
  `releases/latest`; users can target prerelease tags by setting
  `AGENTBOX_VERSION` explicitly.
- **Pinning the `curl | sh` URL to a `release` branch (or tag) instead of
  `main`.** Sticking with `main` for v0.2; revisit if installer churn ever
  forces an explicit release-channel branch.
- **Tag for the release that ships this:** chosen at release time, not in
  design.
