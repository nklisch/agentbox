#!/bin/sh
# install_test.sh — Test harness for scripts/install.sh
#
# Runs the installer against a local mock HTTP server so no real GitHub
# traffic is needed. Each test function runs with a fresh $HOME and
# $AGENTBOX_PREFIX. Failures print context and exit non-zero.
#
# Usage:
#   sh scripts/test/install_test.sh
#   make test-install

set -eu

# ---------------------------------------------------------------------------
# Paths
# ---------------------------------------------------------------------------

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
INSTALLER="$REPO_ROOT/scripts/install.sh"
FIXTURES_DIR="$SCRIPT_DIR/fixtures"
RELEASE_DIR="$FIXTURES_DIR/release"

# ---------------------------------------------------------------------------
# Test framework
# ---------------------------------------------------------------------------

PASS=0
FAIL=0
TEST_NAME=""

pass() {
    PASS=$((PASS + 1))
    printf '  ok  %s\n' "$TEST_NAME"
}

fail() {
    FAIL=$((FAIL + 1))
    printf '  FAIL %s: %s\n' "$TEST_NAME" "$*" >&2
}

assert_file_exists() {
    if [ ! -f "$1" ]; then
        fail "expected file to exist: $1"
        return 1
    fi
    return 0
}

assert_file_not_exists() {
    if [ -f "$1" ]; then
        fail "expected file to NOT exist: $1"
        return 1
    fi
    return 0
}

assert_executable() {
    if [ ! -x "$1" ]; then
        fail "expected file to be executable: $1"
        return 1
    fi
    return 0
}

# ---------------------------------------------------------------------------
# Server management
# ---------------------------------------------------------------------------

SERVER_PORT=18080
SERVER_PID=""
SERVER_SCRIPT=""

start_server() {
    PORT="$SERVER_PORT" RELEASE_DIR="$RELEASE_DIR" sh "$FIXTURES_DIR/serve.sh" > /tmp/agentbox-test-server-info.txt 2>&1
    SERVER_PID=$(head -1 /tmp/agentbox-test-server-info.txt)
    SERVER_SCRIPT=$(tail -1 /tmp/agentbox-test-server-info.txt)
    # Wait for server to be ready (up to 3 seconds).
    retries=0
    while ! curl -fs "http://127.0.0.1:${SERVER_PORT}/api/repos/test/releases/latest" >/dev/null 2>&1; do
        retries=$((retries + 1))
        if [ "$retries" -gt 30 ]; then
            printf 'ERROR: mock server did not start in time\n' >&2
            exit 1
        fi
        sleep 0.1 2>/dev/null || true
    done
}

stop_server() {
    if [ -n "$SERVER_PID" ]; then
        kill "$SERVER_PID" 2>/dev/null || true
        wait "$SERVER_PID" 2>/dev/null || true
        SERVER_PID=""
    fi
    if [ -n "$SERVER_SCRIPT" ] && [ -f "$SERVER_SCRIPT" ]; then
        rm -f "$SERVER_SCRIPT"
        SERVER_SCRIPT=""
    fi
}

# ---------------------------------------------------------------------------
# Test environment helpers
# ---------------------------------------------------------------------------

TEST_HOME=""
TEST_PREFIX=""
TEST_STDERR_FILE=""
TEST_BIN_DIR=""

# setup_env — Create a fresh HOME, prefix, and shim-bin dir for one test.
# Sets TEST_HOME, TEST_PREFIX, TEST_STDERR_FILE, TEST_BIN_DIR globals.
setup_env() {
    TEST_HOME=$(mktemp -d /tmp/agentbox-test-home.XXXXXX)
    TEST_PREFIX=$(mktemp -d /tmp/agentbox-test-prefix.XXXXXX)
    TEST_STDERR_FILE=$(mktemp /tmp/agentbox-test-stderr.XXXXXX)
    # Per-test shim bin directory: create fake 'uname' and 'xattr' wrappers here.
    TEST_BIN_DIR=$(mktemp -d /tmp/agentbox-test-bin.XXXXXX)
}

# teardown_env — Remove per-test tmpfiles.
teardown_env() {
    rm -rf "$TEST_HOME" "$TEST_PREFIX" "$TEST_STDERR_FILE" "$TEST_BIN_DIR" 2>/dev/null || true
}

# make_uname_shim <os> <arch>
#   Create a 'uname' shim in TEST_BIN_DIR that reports the given OS and arch.
make_uname_shim() {
    SHIM_OS=$1; SHIM_ARCH=$2
    cat > "$TEST_BIN_DIR/uname" <<SHIMEOF
#!/bin/sh
case "\$1" in
    -s) printf '%s\n' "$SHIM_OS" ;;
    -m) printf '%s\n' "$SHIM_ARCH" ;;
    *)  /usr/bin/uname "\$@" ;;
esac
SHIMEOF
    chmod 0755 "$TEST_BIN_DIR/uname"
}

# make_xattr_shim <log_file>
#   Create an 'xattr' shim in TEST_BIN_DIR that logs its args.
make_xattr_shim() {
    SHIM_LOG=$1
    cat > "$TEST_BIN_DIR/xattr" <<SHIMEOF
#!/bin/sh
printf '%s\n' "\$*" >> "$SHIM_LOG"
exit 0
SHIMEOF
    chmod 0755 "$TEST_BIN_DIR/xattr"
}

# make_xattr_fail_shim
#   Create an 'xattr' shim that fails if called.
make_xattr_fail_shim() {
    cat > "$TEST_BIN_DIR/xattr" <<'SHIMEOF'
#!/bin/sh
printf '[xattr-fail-shim] xattr should not have been called: %s\n' "$*" >&2
exit 1
SHIMEOF
    chmod 0755 "$TEST_BIN_DIR/xattr"
}

# base_env — Echo the standard env var assignments that wire installer to
# the local test server. Used with `env` in individual tests.
base_env() {
    printf '%s\n' \
        "HOME=$TEST_HOME" \
        "AGENTBOX_PREFIX=$TEST_PREFIX" \
        "AGENTBOX_TEST_API=http://127.0.0.1:${SERVER_PORT}/api/repos/test" \
        "AGENTBOX_TEST_DL=http://127.0.0.1:${SERVER_PORT}/releases/download" \
        "AGENTBOX_VERSION=v0.0.0-test" \
        "AGENTBOX_NO_PATH=1" \
        "SHELL=/usr/bin/zsh"
}

# ---------------------------------------------------------------------------
# Individual test cases
# ---------------------------------------------------------------------------

test_happy_path_linux_amd64() {
    TEST_NAME="test_happy_path_linux_amd64"
    setup_env
    make_uname_shim Linux x86_64
    exit_code=0
    env \
        HOME="$TEST_HOME" \
        AGENTBOX_PREFIX="$TEST_PREFIX" \
        AGENTBOX_TEST_API="http://127.0.0.1:${SERVER_PORT}/api/repos/test" \
        AGENTBOX_TEST_DL="http://127.0.0.1:${SERVER_PORT}/releases/download" \
        AGENTBOX_VERSION=v0.0.0-test \
        AGENTBOX_NO_PATH=1 \
        SHELL=/usr/bin/zsh \
        PATH="$TEST_BIN_DIR:$PATH" \
        sh "$INSTALLER" >"$TEST_STDERR_FILE" 2>&1 || exit_code=$?
    if [ "$exit_code" != "0" ]; then
        fail "expected exit 0, got $exit_code; stderr: $(cat "$TEST_STDERR_FILE")"
        teardown_env; return
    fi
    assert_file_exists "$TEST_PREFIX/agentbox"       || { teardown_env; return; }
    assert_file_exists "$TEST_PREFIX/agentbox-netfilter" || { teardown_env; return; }
    assert_executable  "$TEST_PREFIX/agentbox"       || { teardown_env; return; }
    assert_executable  "$TEST_PREFIX/agentbox-netfilter" || { teardown_env; return; }
    teardown_env
    pass
}

test_resolves_latest_when_unset() {
    TEST_NAME="test_resolves_latest_when_unset"
    setup_env
    make_uname_shim Linux x86_64
    # Do NOT set AGENTBOX_VERSION — let the installer query the mock API.
    exit_code=0
    env \
        HOME="$TEST_HOME" \
        AGENTBOX_PREFIX="$TEST_PREFIX" \
        AGENTBOX_TEST_API="http://127.0.0.1:${SERVER_PORT}/api/repos/test" \
        AGENTBOX_TEST_DL="http://127.0.0.1:${SERVER_PORT}/releases/download" \
        AGENTBOX_NO_PATH=1 \
        SHELL=/usr/bin/zsh \
        PATH="$TEST_BIN_DIR:$PATH" \
        sh "$INSTALLER" >"$TEST_STDERR_FILE" 2>&1 || exit_code=$?
    if [ "$exit_code" != "0" ]; then
        fail "expected exit 0, got $exit_code; stderr: $(cat "$TEST_STDERR_FILE")"
        teardown_env; return
    fi
    assert_file_exists "$TEST_PREFIX/agentbox"           || { teardown_env; return; }
    assert_file_exists "$TEST_PREFIX/agentbox-netfilter" || { teardown_env; return; }
    teardown_env
    pass
}

test_unsupported_os() {
    TEST_NAME="test_unsupported_os"
    setup_env
    make_uname_shim FreeBSD amd64
    exit_code=0
    env \
        HOME="$TEST_HOME" \
        AGENTBOX_PREFIX="$TEST_PREFIX" \
        AGENTBOX_TEST_API="http://127.0.0.1:${SERVER_PORT}/api/repos/test" \
        AGENTBOX_TEST_DL="http://127.0.0.1:${SERVER_PORT}/releases/download" \
        AGENTBOX_VERSION=v0.0.0-test \
        AGENTBOX_NO_PATH=1 \
        SHELL=/usr/bin/zsh \
        PATH="$TEST_BIN_DIR:$PATH" \
        sh "$INSTALLER" >"$TEST_STDERR_FILE" 2>&1 || exit_code=$?
    if [ "$exit_code" != "2" ]; then
        fail "expected exit 2, got $exit_code; stderr: $(cat "$TEST_STDERR_FILE")"
        teardown_env; return
    fi
    assert_file_not_exists "$TEST_PREFIX/agentbox" || { teardown_env; return; }
    teardown_env
    pass
}

test_intel_mac_blocked() {
    TEST_NAME="test_intel_mac_blocked"
    setup_env
    make_uname_shim Darwin x86_64
    exit_code=0
    env \
        HOME="$TEST_HOME" \
        AGENTBOX_PREFIX="$TEST_PREFIX" \
        AGENTBOX_TEST_API="http://127.0.0.1:${SERVER_PORT}/api/repos/test" \
        AGENTBOX_TEST_DL="http://127.0.0.1:${SERVER_PORT}/releases/download" \
        AGENTBOX_VERSION=v0.0.0-test \
        AGENTBOX_NO_PATH=1 \
        SHELL=/usr/bin/zsh \
        PATH="$TEST_BIN_DIR:$PATH" \
        sh "$INSTALLER" >"$TEST_STDERR_FILE" 2>&1 || exit_code=$?
    if [ "$exit_code" != "2" ]; then
        fail "expected exit 2, got $exit_code"
        teardown_env; return
    fi
    if ! grep -q "darwin/amd64" "$TEST_STDERR_FILE"; then
        fail "expected 'darwin/amd64' in stderr; got: $(cat "$TEST_STDERR_FILE")"
        teardown_env; return
    fi
    assert_file_not_exists "$TEST_PREFIX/agentbox" || { teardown_env; return; }
    teardown_env
    pass
}

test_checksum_mismatch() {
    TEST_NAME="test_checksum_mismatch"
    setup_env

    # Create a corrupted release dir with a wrong checksum for linux_amd64.
    corrupted_release=$(mktemp -d /tmp/agentbox-test-corrupted.XXXXXX)
    mkdir -p "$corrupted_release/v0.0.0-test"
    cp "$RELEASE_DIR/v0.0.0-test/"*.tar.gz "$corrupted_release/v0.0.0-test/"
    cp "$RELEASE_DIR/v0.0.0-test/latest.json" "$corrupted_release/v0.0.0-test/"

    # Write a checksums.txt with a wrong hash for linux_amd64.
    printf 'deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef  agentbox_0.0.0-test_linux_amd64.tar.gz\n' \
        > "$corrupted_release/v0.0.0-test/checksums.txt"
    # Keep correct hashes for the other platforms.
    grep -v "linux_amd64" "$RELEASE_DIR/v0.0.0-test/checksums.txt" \
        >> "$corrupted_release/v0.0.0-test/checksums.txt"

    # Start a second server on a different port.
    CORRUPTED_PORT=18081
    PORT="$CORRUPTED_PORT" RELEASE_DIR="$corrupted_release" \
        sh "$FIXTURES_DIR/serve.sh" > /tmp/agentbox-test-corrupt-server.txt 2>&1
    C_PID=$(head -1 /tmp/agentbox-test-corrupt-server.txt)
    C_SCRIPT=$(tail -1 /tmp/agentbox-test-corrupt-server.txt)

    retries=0
    while ! curl -fs "http://127.0.0.1:${CORRUPTED_PORT}/api/repos/test/releases/latest" >/dev/null 2>&1; do
        retries=$((retries + 1))
        if [ "$retries" -gt 30 ]; then break; fi
        sleep 0.1 2>/dev/null || true
    done

    make_uname_shim Linux x86_64
    exit_code=0
    env \
        HOME="$TEST_HOME" \
        AGENTBOX_PREFIX="$TEST_PREFIX" \
        AGENTBOX_TEST_API="http://127.0.0.1:${CORRUPTED_PORT}/api/repos/test" \
        AGENTBOX_TEST_DL="http://127.0.0.1:${CORRUPTED_PORT}/releases/download" \
        AGENTBOX_VERSION=v0.0.0-test \
        AGENTBOX_NO_PATH=1 \
        SHELL=/usr/bin/zsh \
        PATH="$TEST_BIN_DIR:$PATH" \
        sh "$INSTALLER" >"$TEST_STDERR_FILE" 2>&1 || exit_code=$?

    kill "$C_PID" 2>/dev/null || true
    wait "$C_PID" 2>/dev/null || true
    rm -f "$C_SCRIPT" 2>/dev/null || true
    rm -rf "$corrupted_release"

    if [ "$exit_code" != "5" ]; then
        fail "expected exit 5, got $exit_code; stderr: $(cat "$TEST_STDERR_FILE")"
        teardown_env; return
    fi
    assert_file_not_exists "$TEST_PREFIX/agentbox" || { teardown_env; return; }
    teardown_env
    pass
}

test_path_append_idempotent() {
    TEST_NAME="test_path_append_idempotent"
    setup_env

    make_uname_shim Linux x86_64

    # Strip TEST_PREFIX from PATH to ensure the installer tries to add it.
    SAFE_PATH=$(printf '%s' "$PATH" | tr ':' '\n' | grep -v "^$TEST_PREFIX$" | tr '\n' ':' | sed 's/:$//')

    # Create an empty ~/.zshrc.
    touch "$TEST_HOME/.zshrc"

    # Run the installer twice.
    i=1
    while [ "$i" -le 2 ]; do
        exit_code=0
        env \
            HOME="$TEST_HOME" \
            AGENTBOX_PREFIX="$TEST_PREFIX" \
            AGENTBOX_TEST_API="http://127.0.0.1:${SERVER_PORT}/api/repos/test" \
            AGENTBOX_TEST_DL="http://127.0.0.1:${SERVER_PORT}/releases/download" \
            AGENTBOX_VERSION=v0.0.0-test \
            SHELL=/usr/bin/zsh \
            PATH="$TEST_BIN_DIR:$SAFE_PATH" \
            sh "$INSTALLER" >"$TEST_STDERR_FILE" 2>&1 || exit_code=$?
        if [ "$exit_code" != "0" ]; then
            fail "run $i failed with exit $exit_code; stderr: $(cat "$TEST_STDERR_FILE")"
            teardown_env; return
        fi
        i=$((i + 1))
    done

    # Exactly one sentinel block in ~/.zshrc.
    sentinel_count=$(grep -cF "# >>> agentbox installer >>>" "$TEST_HOME/.zshrc" 2>/dev/null || printf '0')
    if [ "$sentinel_count" != "1" ]; then
        fail "expected 1 sentinel block, got $sentinel_count"
        teardown_env; return
    fi
    teardown_env
    pass
}

test_path_skipped_when_no_path_env() {
    TEST_NAME="test_path_skipped_when_no_path_env"
    setup_env
    make_uname_shim Linux x86_64

    touch "$TEST_HOME/.zshrc"

    exit_code=0
    env \
        HOME="$TEST_HOME" \
        AGENTBOX_PREFIX="$TEST_PREFIX" \
        AGENTBOX_TEST_API="http://127.0.0.1:${SERVER_PORT}/api/repos/test" \
        AGENTBOX_TEST_DL="http://127.0.0.1:${SERVER_PORT}/releases/download" \
        AGENTBOX_VERSION=v0.0.0-test \
        AGENTBOX_NO_PATH=1 \
        SHELL=/usr/bin/zsh \
        PATH="$TEST_BIN_DIR:$PATH" \
        sh "$INSTALLER" >"$TEST_STDERR_FILE" 2>&1 || exit_code=$?
    if [ "$exit_code" != "0" ]; then
        fail "expected exit 0, got $exit_code; stderr: $(cat "$TEST_STDERR_FILE")"
        teardown_env; return
    fi
    if grep -qF "# >>> agentbox installer >>>" "$TEST_HOME/.zshrc" 2>/dev/null; then
        fail "sentinel block should not appear when AGENTBOX_NO_PATH=1"
        teardown_env; return
    fi
    teardown_env
    pass
}

test_quarantine_strip_darwin() {
    TEST_NAME="test_quarantine_strip_darwin"
    setup_env
    make_uname_shim Darwin arm64
    XATTR_LOG=$(mktemp /tmp/agentbox-test-xattr.XXXXXX)
    make_xattr_shim "$XATTR_LOG"

    exit_code=0
    env \
        HOME="$TEST_HOME" \
        AGENTBOX_PREFIX="$TEST_PREFIX" \
        AGENTBOX_TEST_API="http://127.0.0.1:${SERVER_PORT}/api/repos/test" \
        AGENTBOX_TEST_DL="http://127.0.0.1:${SERVER_PORT}/releases/download" \
        AGENTBOX_VERSION=v0.0.0-test \
        AGENTBOX_NO_PATH=1 \
        SHELL=/usr/bin/zsh \
        PATH="$TEST_BIN_DIR:$PATH" \
        sh "$INSTALLER" >"$TEST_STDERR_FILE" 2>&1 || exit_code=$?
    if [ "$exit_code" != "0" ]; then
        fail "expected exit 0, got $exit_code; stderr: $(cat "$TEST_STDERR_FILE")"
        rm -f "$XATTR_LOG"; teardown_env; return
    fi

    # Verify xattr was called at least twice (once per binary).
    xattr_calls=$(wc -l < "$XATTR_LOG" 2>/dev/null | tr -d ' ')
    if [ "${xattr_calls:-0}" -lt 2 ]; then
        fail "expected at least 2 xattr calls, got ${xattr_calls:-0}; log: $(cat "$XATTR_LOG")"
        rm -f "$XATTR_LOG"; teardown_env; return
    fi
    if ! grep -q "com.apple.quarantine" "$XATTR_LOG"; then
        fail "expected 'com.apple.quarantine' in xattr log"
        rm -f "$XATTR_LOG"; teardown_env; return
    fi

    rm -f "$XATTR_LOG"
    teardown_env
    pass
}

test_unwritable_prefix() {
    TEST_NAME="test_unwritable_prefix"
    setup_env
    make_uname_shim Linux x86_64

    exit_code=0
    env \
        HOME="$TEST_HOME" \
        AGENTBOX_PREFIX=/proc/agentbox-test-install \
        AGENTBOX_TEST_API="http://127.0.0.1:${SERVER_PORT}/api/repos/test" \
        AGENTBOX_TEST_DL="http://127.0.0.1:${SERVER_PORT}/releases/download" \
        AGENTBOX_VERSION=v0.0.0-test \
        AGENTBOX_NO_PATH=1 \
        SHELL=/usr/bin/zsh \
        PATH="$TEST_BIN_DIR:$PATH" \
        sh "$INSTALLER" >"$TEST_STDERR_FILE" 2>&1 || exit_code=$?
    if [ "$exit_code" != "6" ]; then
        fail "expected exit 6, got $exit_code; stderr: $(cat "$TEST_STDERR_FILE")"
        teardown_env; return
    fi
    teardown_env
    pass
}

# ---------------------------------------------------------------------------
# TODO: remaining 11 test cases from the design
#
# These cover additional code paths. Scaffold left here to guide future
# implementation. Each should follow the same pattern: setup_env, run with
# env, assert exit code and filesystem state, teardown_env.
#
# test_unsupported_arch
#   UNAME_M=mips64 -> assert exit 2
#
# test_missing_checksum_entry
#   Serve a checksums.txt with no entry for linux_amd64 -> assert exit 5
#
# test_path_skipped_when_already_on_path
#   PATH includes TEST_PREFIX -> assert no change to ~/.zshrc
#
# test_path_fish
#   SHELL=/usr/bin/fish -> assert ~/.config/fish/config.fish gains
#   sentinel block with 'fish_add_path' line
#
# test_path_unknown_shell
#   SHELL=/bin/dash (not bash/zsh/fish) -> assert exit 0, no rc written,
#   "could not detect shell rc" in stderr
#
# test_quarantine_skip_linux
#   UNAME_S=Linux, XATTR_FAIL=1, PATH includes xattr-wrap.sh before
#   system dirs -> assert exit 0 and xattr not called
#
# test_curl_failure_archive
#   Stop HTTP server after API call but before download -> assert exit 4
#
# test_curl_failure_api
#   AGENTBOX_TEST_API points to /dev/null URL (500 response) -> assert
#   exit 4 with "could not resolve" in stderr
#
# test_pinned_version_skips_api
#   AGENTBOX_VERSION=v0.0.0-test with API returning 500 -> assert exit 0
#
# test_install_debug_traces
#   AGENTBOX_INSTALL_DEBUG=1 -> assert stderr contains '+ ' lines
#
# test_missing_tool
#   PATH without 'tar' -> assert exit 3 with "missing required tool" in stderr
# ---------------------------------------------------------------------------

# ---------------------------------------------------------------------------
# Runner
# ---------------------------------------------------------------------------

run_all_tests() {
    printf 'agentbox install.sh test harness\n'
    printf '=================================\n'

    # Build fixtures if not already built.
    sh "$FIXTURES_DIR/make_fixtures.sh" 2>/dev/null

    # Start mock HTTP server.
    start_server

    # Run each implemented test case.
    test_happy_path_linux_amd64
    test_resolves_latest_when_unset
    test_unsupported_os
    test_intel_mac_blocked
    test_checksum_mismatch
    test_path_append_idempotent
    test_path_skipped_when_no_path_env
    test_quarantine_strip_darwin
    test_unwritable_prefix

    # Teardown server.
    stop_server

    printf '\n'
    printf 'Results: %d passed, %d failed\n' "$PASS" "$FAIL"
    if [ "$FAIL" -gt 0 ]; then
        exit 1
    fi
}

run_all_tests
