#!/usr/bin/env bash
# Self-test for greet. Invoked by `smith register` as: selftest.sh <tool>
# inside the sandbox (no network, writes only to CWD/TMP). Exit 0 = pass.
#
# A passing self-test is NECESSARY for Tier 0 registration but NOT SUFFICIENT
# for trust — promotion review still reads the source (spec §7).
set -euo pipefail
TOOL="$1"

fail() { echo "SELFTEST FAIL: $*" >&2; exit 1; }

# --- agent-friendly contract checks (keep these) ---
"$TOOL" --help >/dev/null 2>&1 || fail "--help must exit 0"
"$TOOL" --help 2>&1 | grep -qi "usage" || fail "--help must describe usage"
"$TOOL" --version >/dev/null 2>&1 || fail "--version must exit 0"
if "$TOOL" --definitely-not-a-real-flag >/dev/null 2>&1; then
  fail "unknown flags must be rejected with a nonzero exit"
fi
out=$("$TOOL" --json) || fail "--json invocation errored"
printf '%s' "$out" | python3 -c 'import json,sys; json.loads(sys.stdin.read())' \
  || fail "--json output is not valid JSON"

# --- operation-specific assertions (REQUIRED: replace the placeholder) ---
# TODO: assert the tool actually performs its operation correctly, e.g.:
#   result=$(echo 'input' | "$TOOL" --json some-arg)
#   [[ $(printf '%s' "$result" | python3 -c '...extract field...') == expected ]] \
#     || fail "wrong answer for known input"

echo "selftest: ok"
