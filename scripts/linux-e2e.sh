#!/usr/bin/env bash
# Two-machine acceptance check: run "machine B" as a Linux container.
#
# Clones the real index from GitHub, verifies its signature, installs an
# external tool for linux via mise, and proves bwrap sandbox enforcement
# (write + network escape attempts from a hostile tier-0 tool).
#
# Usage: scripts/linux-e2e.sh [owner/index-repo]   (default below)
# Requires: docker (privileged containers, for bwrap), gh (authenticated),
# go via mise. Nothing is pushed anywhere; the container is ephemeral.
set -euo pipefail
cd "$(dirname "$0")/.."

INDEX_REPO="${1:-psomialbert/smith-index}"
CTX="$(mktemp -d)"
trap 'rm -rf "$CTX"' EXIT

GOOS=linux GOARCH=arm64 CGO_ENABLED=0 mise exec -- go build -o "$CTX/smith" ./cmd/smith

cat > "$CTX/inner.sh" <<'INNER'
#!/usr/bin/env bash
set -euo pipefail
SMITH=/ctx/smith
say() { printf '\n== %s ==\n' "$*"; }

say "deps"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq >/dev/null
apt-get install -y -qq git curl ca-certificates bubblewrap python3 >/dev/null 2>&1
curl -fsSL https://mise.run | MISE_QUIET=1 sh >/dev/null 2>&1
export PATH="$HOME/.local/bin:$PATH"

say "clone real index + verify signature"
git clone -q "https://x-access-token:${GH_TOKEN}@github.com/${INDEX_REPO}.git" "$HOME/.toolsmith"
echo "pubkey fingerprint (check out-of-band): $(python3 -c 'import hashlib,sys; print(hashlib.sha256(bytes.fromhex(open(sys.argv[1]).read().strip())).hexdigest()[:16])' "$HOME/.toolsmith/index/smith.pub")"
$SMITH verify

say "external tool for this platform via mise"
$SMITH add rg@15.1.0 2>&1 | grep -v '^mise ' | head -4
mkdir -p /work && cd /work
$SMITH run rg --version | head -1

say "sandbox escape attempts (must all be blocked)"
QD="$HOME/.toolsmith/store/quarantine/evil/0.1.0/bin"
mkdir -p "$QD"
printf '#!/bin/bash\ntouch /root/PWNED 2>/dev/null && echo WRITE-ESCAPED || echo write-blocked\ncurl -sS --max-time 4 https://example.com -o /dev/null 2>/dev/null && echo NET-ESCAPED || echo network-blocked\n' > "$QD/evil"
chmod +x "$QD/evil"
mkdir -p "$HOME/.toolsmith/index/tools/evil/0.1.0"
printf 'name: evil\nversion: 0.1.0\nplatform: any\ntier: 0\norigin: authored\nsource: local\nadded_at: 2026-01-01T00:00:00Z\nartifacts:\n  - path: bin/evil\n    sha256: %s\n' "$(sha256sum "$QD/evil" | cut -d' ' -f1)" > "$HOME/.toolsmith/index/tools/evil/0.1.0/any.yaml"
out=$($SMITH run evil)
echo "$out"
echo "$out" | grep -q ESCAPED && { echo "SANDBOX FAILED"; exit 1; }
test ! -e /root/PWNED

say "ALL LINUX CHECKS PASSED"
INNER
chmod +x "$CTX/inner.sh"

docker run --rm --privileged \
  -v "$CTX:/ctx" \
  -e GH_TOKEN="$(gh auth token)" \
  -e INDEX_REPO="$INDEX_REPO" \
  ubuntu:24.04 bash /ctx/inner.sh
