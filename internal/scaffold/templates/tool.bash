#!/usr/bin/env bash
# {{NAME}} — {{DESC}}
# Agent-friendly contract: --json machine output, --quiet, non-interactive,
# errors to stderr, exit codes: 0 ok / 1 runtime error / 2 usage error.
set -euo pipefail

VERSION="{{VERSION}}"
JSON=0
QUIET=0
args=()

usage() {
  cat <<USAGE
{{NAME}} $VERSION — {{DESC}}

Usage: {{NAME}} [--json] [--quiet] [--version] [--help] [args...]

Flags:
  --json      machine-readable JSON on stdout (the machine path — never prose)
  --quiet     suppress non-essential human output
  --version   print version and exit
  --help      print this help and exit

Exit codes: 0 ok, 1 runtime error, 2 usage error
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --json) JSON=1 ;;
    --quiet) QUIET=1 ;;
    --version) echo "$VERSION"; exit 0 ;;
    --help|-h) usage; exit 0 ;;
    --*) echo "{{NAME}}: unknown flag: $1" >&2; exit 2 ;;
    *) args+=("$1") ;;
  esac
  shift
done

# emit <json> <human>: single choke point for output so the machine path
# stays structured.
emit() {
  if [[ $JSON -eq 1 ]]; then
    printf '%s\n' "$1"
  elif [[ $QUIET -eq 0 ]]; then
    printf '%s\n' "$2"
  fi
}

main() {
  # TODO: implement {{NAME}}. Replace this placeholder.
  local n=0
  [[ ${args+x} ]] && n=${#args[@]}
  emit "{\"ok\":true,\"args\":$n}" "ok ($n args)"
}

main
