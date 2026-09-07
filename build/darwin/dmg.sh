#!/usr/bin/env bash
# Wrap build/bin/masque.app in a drag-to-Applications dmg. Ad-hoc signed
# (required to launch on Apple Silicon at all); Developer ID signing and
# notarization are deferred until public release, so first launch needs
# right-click > Open, or `xattr -cr /Applications/Masque.app`.
#
# Usage: build/darwin/dmg.sh [output-dir]   (VERSION env optional)
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
OUT="${1:-$ROOT/build/bin}"
mkdir -p "$OUT"; OUT="$(cd "$OUT" && pwd)"  # absolute: scripts cd around
VERSION="${VERSION:-$(/usr/bin/plutil -extract info.productVersion raw -o - "$ROOT/wails.json" 2>/dev/null || jq -r '.info.productVersion' "$ROOT/wails.json")}"
SRC="$ROOT/build/bin/masque.app"
[ -d "$SRC" ] || { echo "missing $SRC — run 'wails build -platform darwin/universal' first" >&2; exit 1; }

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
STAGE="$WORK/dmg"
mkdir -p "$STAGE"
cp -R "$SRC" "$STAGE/Masque.app"
codesign --force --deep --sign - "$STAGE/Masque.app"
ln -s /Applications "$STAGE/Applications"

mkdir -p "$OUT"
DMG="$OUT/Masque-$VERSION-macos-universal.dmg"
rm -f "$DMG"
hdiutil create -volname "Masque" -srcfolder "$STAGE" -ov -format UDZO -quiet "$DMG"
echo "built $DMG"
