#!/bin/sh
# make_fixtures.sh — Build fixture archives for the install_test.sh harness.
#
# Creates fake agentbox + agentbox-netfilter binaries (shell scripts that
# print a version string) and packages them into tarballs matching the
# goreleaser naming convention.
#
# Output: scripts/test/fixtures/release/v0.0.0-test/
#   agentbox_0.0.0-test_linux_amd64.tar.gz
#   agentbox_0.0.0-test_linux_arm64.tar.gz
#   agentbox_0.0.0-test_darwin_arm64.tar.gz
#   checksums.txt
#   latest.json

set -eu

VERSION="v0.0.0-test"
BARE="0.0.0-test"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
RELEASE_DIR="${SCRIPT_DIR}/release/${VERSION}"

mkdir -p "$RELEASE_DIR"

# Build a tiny fake binary that responds to --version.
make_fake_bin() {
    name=$1
    path=$2
    cat > "$path" <<'BINEOF'
#!/bin/sh
if [ "${1:-}" = "--version" ]; then
    printf '0.0.0-test (commit abc1234, built 1970-01-01T00:00:00Z)\n'
    exit 0
fi
printf 'fake %s: %s\n' "$(basename "$0")" "$*"
BINEOF
    chmod 0755 "$path"
}

# Build tarballs for each platform.
for platform in linux_amd64 linux_arm64 darwin_arm64; do
    archive_name="agentbox_${BARE}_${platform}.tar.gz"
    archive_path="${RELEASE_DIR}/${archive_name}"

    # Skip if already built and not stale.
    if [ -f "$archive_path" ]; then
        continue
    fi

    work=$(mktemp -d)
    make_fake_bin agentbox            "$work/agentbox"
    make_fake_bin agentbox-netfilter  "$work/agentbox-netfilter"

    # Include stub LICENSE and README so the archive layout matches production.
    printf 'MIT\n' > "$work/LICENSE"
    printf '# agentbox\n' > "$work/README.md"

    tar -czf "$archive_path" -C "$work" agentbox agentbox-netfilter LICENSE README.md
    rm -rf "$work"
done

# Build a checksums.txt from the real sha256 of each archive.
(
    cd "$RELEASE_DIR"
    sha256sum agentbox_${BARE}_linux_amd64.tar.gz \
              agentbox_${BARE}_linux_arm64.tar.gz \
              agentbox_${BARE}_darwin_arm64.tar.gz \
    > checksums.txt
)

# Build a minimal GitHub "releases/latest" API mock response.
cat > "${RELEASE_DIR}/latest.json" <<JSONEOF
{
  "tag_name": "${VERSION}",
  "name": "agentbox ${VERSION}",
  "prerelease": false,
  "draft": false
}
JSONEOF

printf 'fixtures built in %s\n' "$RELEASE_DIR" >&2
