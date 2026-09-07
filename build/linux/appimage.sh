#!/usr/bin/env bash
# Bundle build/bin/masque into a self-contained AppImage.
#
# WebKitGTK is bundled along with GTK (via linuxdeploy-plugin-gtk), so the
# result runs on distros that ship neither webkit2gtk-4.0 nor -4.1. Build on
# the oldest glibc you intend to support (CI uses ubuntu-22.04).
#
# Needs: pkg-config, file, the GTK3/librsvg dev packages (the gtk plugin
# reads their .pc files), the gtk/gdk-pixbuf query tools, and the
# webkit2gtk-4.1 runtime the binary was linked against. On Debian/Ubuntu:
#   libgtk-3-dev librsvg2-dev libgtk-3-bin libgdk-pixbuf2.0-bin libglib2.0-bin
# --deploy-deps-only makes linuxdeploy resolve the helpers' libraries and
# give them an rpath back to usr/lib.
#
# Usage: build/linux/appimage.sh [output-dir]   (VERSION env optional)
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
OUT="${1:-$ROOT/build/bin}"
mkdir -p "$OUT"; OUT="$(cd "$OUT" && pwd)"  # absolute: scripts cd around
VERSION="${VERSION:-$(jq -r '.info.productVersion' "$ROOT/wails.json")}"
ARCH="$(uname -m)"
BIN="$ROOT/build/bin/masque"
[ -x "$BIN" ] || { echo "missing $BIN — run 'make build' first" >&2; exit 1; }

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
APPDIR="$WORK/AppDir"
mkdir -p "$APPDIR"

# Nested AppImages (linuxdeploy, appimagetool) must not need FUSE.
export APPIMAGE_EXTRACT_AND_RUN=1
# Tell the gtk plugin which toolkit to deploy; skip the auto-detect noise.
export DEPLOY_GTK_VERSION=3

TOOLS="$ROOT/build/linux/.tools"
mkdir -p "$TOOLS"
fetch() { [ -s "$TOOLS/$1" ] || curl -sSL -4 -o "$TOOLS/$1" "$2"; chmod +x "$TOOLS/$1"; }
fetch linuxdeploy-$ARCH.AppImage \
  "https://github.com/linuxdeploy/linuxdeploy/releases/download/continuous/linuxdeploy-$ARCH.AppImage"
# Tauri's fork of the gtk plugin knows about WebKitGTK (patches its
# compiled-in /usr paths to be relative, see the cwd hook below).
fetch linuxdeploy-plugin-gtk.sh \
  "https://raw.githubusercontent.com/tauri-apps/linuxdeploy-plugin-gtk/master/linuxdeploy-plugin-gtk.sh"
# Run linuxdeploy from an extracted tree so it needs no FUSE; the nested
# appimagetool honours the env var above.
if [ ! -x "$TOOLS/linuxdeploy/AppRun" ]; then
  (cd "$TOOLS" && rm -rf squashfs-root linuxdeploy \
    && "./linuxdeploy-$ARCH.AppImage" --appimage-extract >/dev/null \
    && mv squashfs-root linuxdeploy)
fi
LINUXDEPLOY="$TOOLS/linuxdeploy/AppRun"

# WebKit runs its web/network/GPU work in helper processes it locates by a
# compiled-in absolute path. Copy them (and the injected bundle) into the
# AppDir at the same relative path; the gtk plugin rewrites "/usr" to "././"
# inside libwebkit, so with cwd=$APPDIR/usr they resolve inside the bundle.
WEBKIT_WEBPROCESS="$(find /usr/lib /usr/lib64 /usr/libexec -path '*webkit2gtk-4.1*' -name WebKitWebProcess -print -quit 2>/dev/null || true)"
WEBKIT_LIBEXEC="$(dirname "${WEBKIT_WEBPROCESS:-/nonexistent/x}")"
[ -d "$WEBKIT_LIBEXEC" ] || { echo "cannot find webkit2gtk-4.1 helper processes (WebKitWebProcess)" >&2; exit 1; }
for helper in WebKitNetworkProcess WebKitWebProcess WebKitGPUProcess; do
  [ -x "$WEBKIT_LIBEXEC/$helper" ] || continue
  install -D "$WEBKIT_LIBEXEC/$helper" "$APPDIR$WEBKIT_LIBEXEC/$helper"
done
install -D "$WEBKIT_LIBEXEC/injected-bundle/libwebkit2gtkinjectedbundle.so" \
  "$APPDIR$WEBKIT_LIBEXEC/injected-bundle/libwebkit2gtkinjectedbundle.so"

# AppRun hook: linuxdeploy's wrapper sources every file in apprun-hooks
# before exec'ing the binary, so a cd here sets the app's cwd.
mkdir -p "$APPDIR/apprun-hooks"
cat > "$APPDIR/apprun-hooks/zz-masque-webkit.sh" <<'HOOK'
# Masque: WebKitGTK's helper-process paths were rewritten to be relative to
# usr/ (see build/linux/appimage.sh); make that the working directory.
cd "$(dirname "$(readlink -f "$0")")/usr"
HOOK

# 512x512 copy of build/appicon.png (linuxdeploy rejects 1024).
ICON="$ROOT/build/linux/masque.png"

(cd "$WORK" && PATH="$TOOLS:$PATH" "$LINUXDEPLOY" \
  --appdir "$APPDIR" \
  --executable "$BIN" \
  --desktop-file "$ROOT/build/linux/masque.desktop" \
  --icon-file "$ICON" \
  --deploy-deps-only "$APPDIR$WEBKIT_LIBEXEC" \
  --plugin gtk)

mkdir -p "$OUT"
(cd "$WORK" && OUTPUT="$OUT/Masque-$VERSION-$ARCH.AppImage" \
  "$LINUXDEPLOY" --appdir "$APPDIR" --output appimage)
echo "built $OUT/Masque-$VERSION-$ARCH.AppImage"
