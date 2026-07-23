#!/usr/bin/env sh
# Install the `smith` binary.
#
#   curl -fsSL https://raw.githubusercontent.com/psomialbert/tool-smith/main/scripts/install.sh | sh
#
# Modes, auto-detected:
#   - inside a clone (cmd/smith present): builds from source via mise+go
#   - otherwise: downloads the release binary over https (no auth needed once the
#     repo is public); falls back to authenticated `gh` if the download 404s
#     (e.g. the repo is still private). The download is checksum-verified against
#     the release's SHA256SUMS when that asset is present.
#
# Env:
#   SMITH_INSTALL_DIR   where to install (default: ~/.local/bin)
#   SMITH_VERSION       release tag to fetch in download mode (default: latest)
#   SMITH_REPO          owner/repo for download mode (default: psomialbert/tool-smith)
#   SMITH_SKILLS_DIR / SKILLS_DIR   where to install the cli-creator skill
set -eu

INSTALL_DIR="${SMITH_INSTALL_DIR:-$HOME/.local/bin}"
# SMITH_SKILLS_DIR (the var the binary itself honors) wins; SKILLS_DIR is
# accepted too so the documented override works from the curl one-liner.
SKILLS_DIR="${SMITH_SKILLS_DIR:-${SKILLS_DIR:-$HOME/.claude/skills}}"
REPO="${SMITH_REPO:-psomialbert/tool-smith}"
VERSION="${SMITH_VERSION:-latest}"

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$os" in
  darwin) os=darwin ;;
  linux) os=linux ;;
  *) echo "smith: unsupported OS: $os (macOS/Linux only)" >&2; exit 1 ;;
esac
arch="$(uname -m)"
case "$arch" in
  arm64|aarch64) arch=arm64 ;;
  x86_64|amd64) arch=amd64 ;;
  *) echo "smith: unsupported arch: $arch" >&2; exit 1 ;;
esac
plat="$os-$arch"

mkdir -p "$INSTALL_DIR"
dest="$INSTALL_DIR/smith"

skill_dst="$SKILLS_DIR/cli-creator/SKILL.md"
mkdir -p "$SKILLS_DIR/cli-creator"

# sha256_of <file> — print the hex sha256, or "" if no tool is available.
sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | cut -d' ' -f1
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | cut -d' ' -f1
  else
    echo ""
  fi
}

# verify_checksum <binary> <sumsfile> <name-in-sums> — hard-fail on mismatch.
verify_checksum() {
  want="$(grep " $3\$" "$2" 2>/dev/null | cut -d' ' -f1 | head -1)"
  if [ -z "$want" ]; then
    echo "smith: no checksum for $3 in SHA256SUMS — refusing to install" >&2
    return 1
  fi
  got="$(sha256_of "$1")"
  if [ -z "$got" ]; then
    echo "smith: no sha256 tool (sha256sum/shasum) to verify the download — refusing to install" >&2
    return 1
  fi
  if [ "$want" != "$got" ]; then
    echo "smith: checksum MISMATCH for $3 (want $want, got $got) — refusing to install" >&2
    return 1
  fi
}

if [ -f "cmd/smith/main.go" ]; then
  echo "smith: building from source ($plat)..."
  if ! command -v mise >/dev/null 2>&1; then
    echo "smith: mise not found — needed to build (https://mise.jdx.dev)" >&2
    exit 1
  fi
  mise exec -- go build -trimpath \
    -ldflags "-X main.version=$(git describe --tags --always 2>/dev/null || echo dev)" \
    -o "$dest" ./cmd/smith
  install -m 0644 skills/cli-creator/SKILL.md "$skill_dst"
else
  echo "smith: downloading release binary ($plat) from $REPO..."
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT INT TERM
  if [ "$VERSION" = latest ]; then
    base="https://github.com/$REPO/releases/latest/download"
  else
    base="https://github.com/$REPO/releases/download/$VERSION"
  fi
  fetch() { # fetch <url> <dest>; nonzero on failure (curl -f fails on 404)
    if command -v curl >/dev/null 2>&1; then
      curl -fsSL "$1" -o "$2"
    elif command -v wget >/dev/null 2>&1; then
      wget -qO "$2" "$1"
    else
      return 127
    fi
  }
  if fetch "$base/smith-$plat" "$tmp/smith-$plat" 2>/dev/null; then
    # Public path: plain https, no auth. Verify against SHA256SUMS if present.
    if fetch "$base/SHA256SUMS" "$tmp/SHA256SUMS" 2>/dev/null; then
      verify_checksum "$tmp/smith-$plat" "$tmp/SHA256SUMS" "smith-$plat" || exit 1
    else
      echo "smith: note: release has no SHA256SUMS asset; integrity rests on TLS only" >&2
    fi
    install -m 0755 "$tmp/smith-$plat" "$dest"
    if fetch "$base/cli-creator-SKILL.md" "$tmp/cli-creator-SKILL.md" 2>/dev/null; then
      install -m 0644 "$tmp/cli-creator-SKILL.md" "$skill_dst"
    fi
  elif command -v gh >/dev/null 2>&1; then
    # Fallback: authenticated gh (repo still private, or no curl/wget).
    tag="$VERSION"
    [ "$tag" = latest ] && tag="$(gh release view --repo "$REPO" --json tagName --jq .tagName)"
    gh release download "$tag" --repo "$REPO" --pattern "smith-$plat" --dir "$tmp" --clobber
    if gh release download "$tag" --repo "$REPO" --pattern "SHA256SUMS" --dir "$tmp" --clobber 2>/dev/null; then
      verify_checksum "$tmp/smith-$plat" "$tmp/SHA256SUMS" "smith-$plat" || exit 1
    else
      echo "smith: note: release has no SHA256SUMS asset; integrity rests on gh/TLS only" >&2
    fi
    install -m 0755 "$tmp/smith-$plat" "$dest"
    if gh release download "$tag" --repo "$REPO" --pattern "cli-creator-SKILL.md" --dir "$tmp" --clobber 2>/dev/null; then
      install -m 0644 "$tmp/cli-creator-SKILL.md" "$skill_dst"
    fi
  else
    echo "smith: download failed — need curl/wget (public releases) or gh (private repo)" >&2
    exit 1
  fi
fi

chmod +x "$dest"
# A wrong-arch or truncated binary must fail the install loudly, not silently
# print "(?)" and exit 0.
if ! ver="$("$dest" version 2>/dev/null)"; then
  echo "smith: installed binary at $dest does not run (wrong arch or corrupt download)" >&2
  rm -f "$dest"
  exit 1
fi
echo "installed: $dest ($ver)"
[ -f "$skill_dst" ] && echo "installed skill: $skill_dst"

# Migrate a machine coming from the retired v1 (personal-index) layout: back up
# ~/.toolsmith cruft and strip dead config keys. No-op on a clean/first install.
"$dest" migrate || true

case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *) echo "note: $INSTALL_DIR is not on your PATH — add: export PATH=\"$INSTALL_DIR:\$PATH\"" ;;
esac
command -v mise >/dev/null 2>&1 || \
  echo "note: mise not found — smith needs it to run (https://mise.jdx.dev)"
echo "next: run \`smith use <repo>\` in a project to adopt a tool; see the README 'Using smith with agents' for the agent workflow + trust rules."
