#!/bin/sh
# uname-wrap.sh — PATH shim that intercepts uname -s and uname -m calls.
#
# Environment variables consumed:
#   UNAME_S   Value to return for `uname -s` (OS name)
#   UNAME_M   Value to return for `uname -m` (machine/arch)
#
# Any other uname flags fall through to the real uname.

real_uname=$(command -p uname 2>/dev/null || /usr/bin/uname)

case "$1" in
    -s)
        if [ -n "${UNAME_S:-}" ]; then
            printf '%s\n' "$UNAME_S"
            exit 0
        fi
        ;;
    -m)
        if [ -n "${UNAME_M:-}" ]; then
            printf '%s\n' "$UNAME_M"
            exit 0
        fi
        ;;
esac

exec "$real_uname" "$@"
