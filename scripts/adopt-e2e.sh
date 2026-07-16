#!/usr/bin/env bash
# End-to-end proof of the v2 adoption model, entirely with local git repos
# (no GitHub needed): a tool repo is adopted into a project as a reviewable
# commit, then realized onto the project's PATH — and a teammate who clones
# the project gets the same tool.
#
# Usage: scripts/adopt-e2e.sh   (run from the repo root; needs mise + a sandbox)
set -euo pipefail
cd "$(dirname "$0")/.."
SM="$PWD/smith"
mise exec -- go build -o "$SM" ./cmd/smith

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
export SMITH_HOME="$work/home"
export SMITH_SKILLS_DIR="$work/skills"   # isolate: don't touch the real ~/.claude/skills
say() { printf '\n== %s ==\n' "$*"; }
gitq() { git -c user.email=t@t -c user.name=t "$@"; }

say "author greet, then make it its own tool repo (tagged, topic smith-tool)"
repo="$work/greet"
( cd "$work" && "$SM" new greet --runtime bash --desc "print a greeting" >/dev/null 2>&1 )
python3 - "$repo/greet" <<'PY'
import re,sys
p=sys.argv[1]; s=open(p).read()
s=re.sub(r'main\(\) \{.*?\n\}', 'main() {\n  local who="${args[0]:-world}"\n  emit "{\\"hello\\":\\"$who\\"}" "hello, $who"\n}', s, flags=re.S)
open(p,'w').write(s)
PY
python3 - "$repo/selftest.sh" <<'PY'
import sys
p=sys.argv[1]; s=open(p).read(); m="# --- operation-specific assertions"
open(p,'w').write(s[:s.index(m)]+m+'\n[[ "$("$TOOL" --json alice)" == \'{"hello":"alice"}\' ]] || fail "wrong greeting"\n\necho "selftest: ok"\n')
PY
( cd "$repo" && git init -q && gitq add -A && gitq commit -qm "greet v0.1.0" && git tag v0.1.0 )

say "in a project: smith use (self-test sandboxed, vendor, pin) — a reviewable diff"
proj="$work/proj"; mkdir -p "$proj"; ( cd "$proj" && git init -q )
( cd "$proj" && "$SM" use "$repo@v0.1.0" && git add -A && git status --short )

say "'merge the PR' then smith install -> greet on PATH for the project"
( cd "$proj" && gitq commit -qm "adopt greet@v0.1.0" && "$SM" install && mise exec -- sh -c 'greet alice && greet --json bob' )

say "teammate clones the project + installs -> same tool"
git clone -q "$proj" "$work/teammate"
( cd "$work/teammate" && "$SM" install >/dev/null && mise exec -- sh -c 'greet teammate' )

say "integrity: tampering vendored source makes install refuse"
( cd "$proj" && echo evil >> .smith/tools/greet/greet && ( "$SM" install && echo "BUG: install accepted tampered source" && exit 1 || echo "refused (expected)" ) )

say "ADOPT E2E PASSED"
