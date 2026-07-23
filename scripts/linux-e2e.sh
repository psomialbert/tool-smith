#!/usr/bin/env bash
# Linux acceptance check for the v2 adoption model, inside an Ubuntu container:
# run the adopt -> install -> run flow, and prove bwrap sandbox enforcement — a
# hostile tool whose self-test tries to write outside CWD/TMP or reach the
# network must be blocked, so `smith use` only succeeds when the sandbox holds.
#
# Usage: scripts/linux-e2e.sh   (from the repo root)
# Requires: docker (privileged, for bwrap) and go via mise. Nothing is pushed
# anywhere; the container is ephemeral.
set -euo pipefail
cd "$(dirname "$0")/.."

CTX="$(mktemp -d)"
trap 'rm -rf "$CTX"' EXIT

# Build the linux binary for the container's architecture (docker uses the host
# arch by default, so match it — no hardcoded GOARCH).
case "$(uname -m)" in
  arm64|aarch64) GOARCH=arm64 ;;
  x86_64|amd64)  GOARCH=amd64 ;;
  *) echo "unsupported host arch: $(uname -m)" >&2; exit 1 ;;
esac
GOOS=linux GOARCH="$GOARCH" CGO_ENABLED=0 mise exec -- go build -o "$CTX/smith" ./cmd/smith

cat > "$CTX/inner.sh" <<'INNER'
#!/usr/bin/env bash
set -euo pipefail
SMITH=/ctx/smith
say() { printf '\n== %s ==\n' "$*"; }
gitq() { git -c user.email=t@t -c user.name=t "$@"; }

say "deps"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq >/dev/null
apt-get install -y -qq git curl ca-certificates bubblewrap python3 >/dev/null 2>&1
curl -fsSL https://mise.run | MISE_QUIET=1 sh >/dev/null 2>&1
export PATH="$HOME/.local/bin:$PATH"
eval "$(mise activate bash)" || true

export SMITH_HOME="$HOME/.smith-home"
export SMITH_SKILLS_DIR="$HOME/.smith-skills"
work="$(mktemp -d)"

say "author a greet tool repo (bash), tag v0.1.0"
cd "$work"
"$SMITH" new greet --runtime bash --desc "print a greeting" >/dev/null
python3 - greet/greet <<'PY'
import re,sys
p=sys.argv[1]; s=open(p).read()
s=re.sub(r'main\(\) \{.*?\n\}', 'main() {\n  local who="${args[0]:-world}"\n  emit "{\\"hello\\":\\"$who\\"}" "hello, $who"\n}', s, flags=re.S)
open(p,'w').write(s)
PY
python3 - greet/selftest.sh <<'PY'
import sys
p=sys.argv[1]; s=open(p).read(); m="# --- operation-specific assertions"
open(p,'w').write(s[:s.index(m)]+m+'\n[[ "$("$TOOL" --json alice)" == \'{"hello":"alice"}\' ]] || fail "wrong greeting"\n\necho "selftest: ok"\n')
PY
( cd greet && git init -q && gitq add -A && gitq commit -qm "greet v0.1.0" && git tag v0.1.0 )

say "adopt into a project (sandboxed self-test, vendor, pin) + install -> on PATH"
proj="$work/proj"; mkdir -p "$proj"; ( cd "$proj" && git init -q )
cd "$proj"
"$SMITH" use "$work/greet@v0.1.0"
gitq add -A && gitq commit -qm "adopt greet@v0.1.0"
"$SMITH" install
mise exec -- sh -c 'greet alice && greet --json bob'

say "sandbox enforcement: a hostile tool's self-test must NOT be able to escape"
evil="$work/evil"
"$SMITH" new evil --runtime bash --desc "hostile probe" >/dev/null
# The self-test passes iff the sandbox BLOCKS both escape attempts; if either
# succeeds it exits nonzero, so `smith use` failing here == the sandbox is broken.
cat > "$evil/selftest.sh" <<'ST'
#!/usr/bin/env bash
set -uo pipefail
if touch /root/PWNED 2>/dev/null; then echo "WRITE-ESCAPED" >&2; rm -f /root/PWNED; exit 1; fi
if curl -sS --max-time 4 https://example.com -o /dev/null 2>/dev/null; then echo "NET-ESCAPED" >&2; exit 1; fi
echo "selftest: ok (sandbox blocked write + network)"
ST
chmod +x "$evil/selftest.sh"
( cd "$evil" && git init -q && gitq add -A && gitq commit -qm "evil v0.1.0" && git tag v0.1.0 )
proj2="$work/proj2"; mkdir -p "$proj2"; ( cd "$proj2" && git init -q )
cd "$proj2"
if ! "$SMITH" use "$work/evil@v0.1.0"; then
  echo "SANDBOX FAILED: hostile self-test escaped the sandbox"; exit 1
fi
test ! -e /root/PWNED

say "ALL LINUX CHECKS PASSED"
INNER
chmod +x "$CTX/inner.sh"

docker run --rm --privileged \
  -v "$CTX:/ctx" \
  ubuntu:24.04 bash /ctx/inner.sh
