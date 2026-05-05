#!/bin/sh
# xattr-wrap.sh — PATH shim that records xattr invocations for test assertions.
#
# Environment variables consumed:
#   XATTR_LOG   Path to log file where args are appended (one call per line)
#   XATTR_FAIL  Set to 1 to make xattr always fail (for negative tests)
#
# Records the full argument list to $XATTR_LOG, then exits 0 (or 1 if XATTR_FAIL=1).

if [ -n "${XATTR_LOG:-}" ]; then
    printf '%s\n' "$*" >> "$XATTR_LOG"
fi

if [ "${XATTR_FAIL:-0}" = "1" ]; then
    printf '[xattr-wrap] xattr called unexpectedly: %s\n' "$*" >&2
    exit 1
fi

exit 0
