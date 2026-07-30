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
# A pre-existing AGENTS.md with hand-written content: the *-md agent targets only
# fire when the file already exists, so without this the emission path below is
# never exercised and CI would pass while agent discovery was broken.
printf '# proj\n\nHand-written guidance; must survive verbatim.\n' > "$proj/AGENTS.md"
( cd "$proj" && "$SM" use "$repo@v0.1.0" && git add -A && git status --short )

say "'merge the PR' then smith install -> greet on PATH for the project"
( cd "$proj" && gitq commit -qm "adopt greet@v0.1.0" && "$SM" install && mise exec -- sh -c 'greet alice && greet --json bob' )

say "agent discovery: install wrote the managed block into the existing AGENTS.md"
grep -q '<!-- smith:begin' "$proj/AGENTS.md" || { echo "BUG: no smith block in AGENTS.md"; exit 1; }
grep -q 'greet' "$proj/AGENTS.md"            || { echo "BUG: greet missing from the block"; exit 1; }
grep -q 'Hand-written guidance; must survive verbatim.' "$proj/AGENTS.md" \
  || { echo "BUG: install clobbered hand-written content"; exit 1; }
echo "block present, hand-written content intact"

say "emission is idempotent: a second install leaves AGENTS.md byte-identical"
# AGENTS.md is git-tracked, so a non-idempotent emitter would dirty the tree on
# every install. Compare bytes rather than trusting the tree state.
cp "$proj/AGENTS.md" "$work/agents-before.md"
( cd "$proj" && "$SM" install >/dev/null )
cmp -s "$work/agents-before.md" "$proj/AGENTS.md" \
  || { echo "BUG: second install changed AGENTS.md"; diff "$work/agents-before.md" "$proj/AGENTS.md" || true; exit 1; }
echo "byte-identical"

say "smith doctor reports the agent block as healthy"
( cd "$proj" && "$SM" doctor --project --json | grep -q '"agents-block"' || { echo "BUG: no agents-block finding"; exit 1; } )
( cd "$proj" && "$SM" doctor --project --json | python3 -c '
import json,sys
fs=[f for f in json.load(sys.stdin)["findings"] if f["check"]=="agents-block"]
assert len(fs)==1, fs
assert fs[0]["severity"]=="ok", fs[0]
print("agents-block:", fs[0]["severity"], "-", fs[0]["message"])
' )

say "teammate clones the project + installs -> same tool"
git clone -q "$proj" "$work/teammate"
( cd "$work/teammate" && "$SM" install >/dev/null && mise exec -- sh -c 'greet teammate' )

say "integrity: tampering vendored source makes install refuse"
( cd "$proj" && echo evil >> .smith/tools/greet/greet && ( "$SM" install && echo "BUG: install accepted tampered source" && exit 1 || echo "refused (expected)" ) )

say "ADOPT E2E PASSED"
