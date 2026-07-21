#!/usr/bin/env sh
# Install the `smith` binary.
#
# Two modes, auto-detected:
#   - inside a clone (cmd/smith present): builds from source via mise+go
#   - otherwise: downloads the release binary for this platform with `gh`
#     (needs gh authenticated with access to the repo)
#
# Env:
#   SMITH_INSTALL_DIR   where to install (default: ~/.local/bin)
#   SMITH_VERSION       release tag to fetch in download mode (default: latest)
#   SMITH_REPO          owner/repo for download mode (default: psomialbert/tool-smith)
set -eu

INSTALL_DIR="${SMITH_INSTALL_DIR:-$HOME/.local/bin}"
SKILLS_DIR="${SMITH_SKILLS_DIR:-$HOME/.claude/skills}"
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
  if ! command -v gh >/dev/null 2>&1; then
    echo "smith: gh not found — needed to download from a private repo, or run this from a clone" >&2
    exit 1
  fi
  tag="$VERSION"
  [ "$tag" = latest ] && tag="$(gh release view --repo "$REPO" --json tagName --jq .tagName)"
  tmp="$(mktemp -d)"
  gh release download "$tag" --repo "$REPO" --pattern "smith-$plat" --dir "$tmp"
  install -m 0755 "$tmp/smith-$plat" "$dest"
  # cli-creator authoring skill (best-effort; older releases may not carry it)
  if gh release download "$tag" --repo "$REPO" --pattern "cli-creator-SKILL.md" --dir "$tmp" 2>/dev/null; then
    install -m 0644 "$tmp/cli-creator-SKILL.md" "$skill_dst"
  fi
  rm -rf "$tmp"
fi

chmod +x "$dest"
echo "installed: $dest ($("$dest" version 2>/dev/null || echo '?'))"
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
