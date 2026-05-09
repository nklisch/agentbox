#!/usr/bin/env bash
# net-diag.sh — diagnose connectivity for an agentbox.
#
# Subcommands:
#   tail              Stream CoreDNS sidecar query log (DNS visibility).
#   probe [endpoint]  Curl common Claude Code plugin endpoints from inside the box.
#                     With no endpoint, runs the full preset list.
#   drops             Show iptables DROP counters and ipset size for direct-IP block.
#   watch [seconds]   Print a periodic snapshot (DNS tail head + probe + drops). Default 30s.
#
# The project is auto-detected from $PWD via `agentbox ls --json`. Override with
# AGENTBOX_PROJECT=<id>.

set -euo pipefail

PROJECT_ID="${AGENTBOX_PROJECT:-}"
RUNTIME="${AGENTBOX_RUNTIME:-podman}"

ENDPOINTS_DEFAULT=(
  "https://api.anthropic.com/"
  "https://mcp-proxy.anthropic.com/"
  "https://statsig.anthropic.com/"
  "https://registry.npmjs.org/"
  "https://github.com/"
  "https://api.github.com/"
  "https://raw.githubusercontent.com/"
  "https://objects.githubusercontent.com/"
  "https://skills.sh/"
)

die() { printf '%s\n' "$*" >&2; exit 1; }
note() { printf '%s\n' "$*" >&2; }

resolve_project() {
  if [[ -n "$PROJECT_ID" ]]; then return; fi
  command -v agentbox >/dev/null || die "agentbox not on PATH"
  command -v jq >/dev/null || die "jq not on PATH (needed to parse agentbox ls --json)"
  local cwd; cwd="$(pwd)"
  PROJECT_ID="$(agentbox ls --json 2>/dev/null \
    | jq -rs --arg cwd "$cwd" '.[] | select(.cwd==$cwd and .role=="box") | .project_id' \
    | head -n1)"
  if [[ -z "$PROJECT_ID" ]]; then
    PROJECT_ID="$(agentbox ls --json 2>/dev/null \
      | jq -rs '.[] | select(.role=="box" and .status=="running") | .project_id' \
      | head -n1)"
  fi
  [[ -n "$PROJECT_ID" ]] || die "no running box found for $cwd; pass AGENTBOX_PROJECT=<id>"
}

coredns_container() { printf 'agentbox-coredns-%s\n' "$PROJECT_ID"; }
box_container()     { printf 'agentbox-%s\n'         "$PROJECT_ID"; }
ipset_name()        { printf 'abx-%s-a\n'            "$PROJECT_ID"; }

cmd_tail() {
  resolve_project
  local c; c="$(coredns_container)"
  note "==> tailing $c (Ctrl-C to stop). Filtering NXDOMAIN/REFUSED to top of feed."
  "$RUNTIME" logs -f --tail 0 "$c" 2>&1 \
    | awk '
        /NXDOMAIN|REFUSED|SERVFAIL/ { printf "\033[31m%s\033[0m\n", $0; next }
        /NOERROR/                   { print; next }
                                    { print }
      '
}

probe_one() {
  local url="$1"
  local box; box="$(box_container)"
  local out
  if ! out=$("$RUNTIME" exec "$box" curl -sS -o /dev/null \
      --connect-timeout 5 --max-time 10 \
      -w '%{http_code} %{time_total}s name=%{remote_ip} family=%{http_version}' \
      "$url" 2>&1); then
    printf '  \033[31mFAIL\033[0m %-55s %s\n' "$url" "$out"
    return 1
  fi
  printf '  \033[32m OK \033[0m %-55s %s\n' "$url" "$out"
}

probe_one_v4() {
  local url="$1"
  local box; box="$(box_container)"
  local out
  if ! out=$("$RUNTIME" exec "$box" curl -sS -4 -o /dev/null \
      --connect-timeout 5 --max-time 10 \
      -w '%{http_code} %{time_total}s ip=%{remote_ip}' \
      "$url" 2>&1); then
    printf '  v4 \033[31mFAIL\033[0m %-50s %s\n' "$url" "$out"
    return 1
  fi
  printf '  v4 \033[32m OK \033[0m %-50s %s\n' "$url" "$out"
}

probe_one_v6() {
  local url="$1"
  local box; box="$(box_container)"
  local out
  if ! out=$("$RUNTIME" exec "$box" curl -sS -6 -o /dev/null \
      --connect-timeout 5 --max-time 10 \
      -w '%{http_code} %{time_total}s ip=%{remote_ip}' \
      "$url" 2>&1); then
    printf '  v6 \033[31mFAIL\033[0m %-50s %s\n' "$url" "$out"
    return 1
  fi
  printf '  v6 \033[32m OK \033[0m %-50s %s\n' "$url" "$out"
}

cmd_probe() {
  resolve_project
  local box; box="$(box_container)"
  note "==> probing from $box (project=$PROJECT_ID)"
  local targets=()
  if [[ $# -gt 0 ]]; then targets=("$@"); else targets=("${ENDPOINTS_DEFAULT[@]}"); fi
  local v4_fail=0 v6_fail=0
  for url in "${targets[@]}"; do
    probe_one_v4 "$url" || v4_fail=$((v4_fail+1))
    probe_one_v6 "$url" || v6_fail=$((v6_fail+1))
  done
  echo
  note "summary: v4 failures=$v4_fail  v6 failures=$v6_fail  (of ${#targets[@]} endpoints)"
  if (( v6_fail > 0 && v4_fail == 0 )); then
    note "hint: only IPv6 is failing — likely the ipset is v4-only and v6 traffic"
    note "      either bypasses or isn't covered. Try AGENTBOX_DISABLE_IPV6 inside the box,"
    note "      or set network.safe.block_direct_ip=false to confirm."
  fi
}

cmd_drops() {
  resolve_project
  local set_name; set_name="$(ipset_name)"
  echo "==> ipset $set_name"
  if sudo -n ipset list "$set_name" 2>/dev/null | head -20; then :; else
    note "(could not read ipset; needs sudo or ipset isn't set up for this project)"
  fi
  echo
  echo "==> iptables FORWARD rule (DROP counters for $set_name)"
  sudo -n iptables -L FORWARD -v -n 2>/dev/null \
    | awk -v s="$set_name" 'NR<=2 || $0 ~ s' \
    || note "(could not read iptables)"
  echo
  echo "==> ipset entry count: $(sudo -n ipset list "$set_name" 2>/dev/null | grep -c '^[0-9]') ipv4 entries"
}

cmd_watch() {
  resolve_project
  local interval="${1:-30}"
  local c; c="$(coredns_container)"
  note "==> watch every ${interval}s. Ctrl-C to stop."
  while true; do
    printf '\n=== %s ===\n' "$(date '+%H:%M:%S')"
    echo "-- recent DNS (last 8) --"
    "$RUNTIME" logs --tail 8 "$c" 2>&1 | sed 's/^/  /'
    echo "-- probes --"
    cmd_probe || true
    echo "-- drops --"
    cmd_drops || true
    sleep "$interval"
  done
}

main() {
  local sub="${1:-}"; shift || true
  case "$sub" in
    tail)   cmd_tail   "$@" ;;
    probe)  cmd_probe  "$@" ;;
    drops)  cmd_drops  "$@" ;;
    watch)  cmd_watch  "$@" ;;
    -h|--help|help|"")
      sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//'
      ;;
    *) die "unknown subcommand: $sub (try: tail | probe | drops | watch)" ;;
  esac
}

main "$@"
