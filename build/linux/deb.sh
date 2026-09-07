#!/usr/bin/env bash
# Package build/bin/masque as a .deb that depends on the distro's
# webkit2gtk-4.1 (Ubuntu 22.04+, Debian 12+). No bundling: apt provides GTK
# and WebKit, so the package is small and shares security updates.
#
# Usage: build/linux/deb.sh [output-dir]   (VERSION env optional)
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
OUT="${1:-$ROOT/build/bin}"
VERSION="${VERSION:-$(jq -r '.info.productVersion' "$ROOT/wails.json")}"
BIN="$ROOT/build/bin/masque"
[ -x "$BIN" ] || { echo "missing $BIN — run 'make build' first" >&2; exit 1; }

case "$(uname -m)" in
  x86_64) DEB_ARCH=amd64 ;;
  aarch64) DEB_ARCH=arm64 ;;
  *) echo "unsupported arch $(uname -m)" >&2; exit 1 ;;
esac

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
PKG="$WORK/masque"

install -Dm755 "$BIN" "$PKG/usr/bin/masque"
install -Dm644 "$ROOT/build/linux/masque.desktop" "$PKG/usr/share/applications/masque.desktop"
install -Dm644 "$ROOT/build/linux/masque.png" "$PKG/usr/share/icons/hicolor/512x512/apps/masque.png"
install -Dm644 /dev/stdin "$PKG/usr/share/doc/masque/copyright" <<'DOC'
Masque — local-first AI roleplay frontend
Copyright (c) 2026 Patrick Barrett
DOC

INSTALLED_KB=$(du -sk "$PKG" | cut -f1)
mkdir -p "$PKG/DEBIAN"
cat > "$PKG/DEBIAN/control" <<CTRL
Package: masque
Version: $VERSION
Section: utils
Priority: optional
Architecture: $DEB_ARCH
Maintainer: Patrick Barrett <pbarrett520@yahoo.com>
Installed-Size: $INSTALLED_KB
Depends: libwebkit2gtk-4.1-0, libgtk-3-0 | libgtk-3-0t64
Homepage: https://github.com/pbarrett520/masque
Description: Local-first AI roleplay frontend
 Masque is a desktop client for AI roleplay. Chats and characters stay on
 your machine; inference comes from a local Ollama instance or any
 OpenAI-compatible or Anthropic endpoint.
CTRL

mkdir -p "$OUT"
DEB="$OUT/masque_${VERSION}_${DEB_ARCH}.deb"
dpkg-deb --build --root-owner-group "$PKG" "$DEB" >/dev/null
echo "built $DEB"
