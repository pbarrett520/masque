# Masque

A local-first AI roleplay frontend: one desktop app that gets a nontechnical
user from install to a good roleplay conversation in minutes, while staying
open enough for a power user to reach full control (sampler settings, prompt
inspection, model management) when they want it.

Chats, characters, and memory live entirely on your machine. Masque doesn't
run any inference itself — it's a client for inference you already have:
point it at a local [Ollama](https://ollama.com) instance (or any
OpenAI-compatible / Anthropic endpoint) or paste a cloud API key. No
accounts, no telemetry, no server component.

Built with [Wails v2](https://wails.io) (Go backend, native webview) and a
React/TypeScript/Tailwind/shadcn-ui frontend, backed by an embedded SQLite
database (`modernc.org/sqlite`, pure Go — no CGo from our own code).

**Status:** M1.8 — packaging. All M1 features are in (chat with swipes,
edit, regenerate and personas; PNG/JSON card import; Ollama, OpenAI-compatible
and Anthropic providers; Ollama model manager with first-run onboarding; dev
mode with a context inspector, sampler panel and request log). The
remaining M1 work is producing and clean-machine-testing unsigned installers
for Linux, Windows and macOS; see `docs/masque-dev-spec-m1.md` for the full
build order (not checked into this repo).

## Prerequisites

- Go 1.23+
- Node.js + npm (for the frontend)
- [Wails CLI v2](https://wails.io/docs/gettingstarted/installation)
- Linux only: `webkit2gtk` development headers (`webkit2gtk-4.1` or
  `webkit2gtk-4.0`) — Wails renders through WebKitGTK on Linux. Windows
  (WebView2) and macOS (WKWebView) need nothing extra.

## Running it

```sh
make dev
```

Runs `wails dev` with hot reload for both the Go backend and the frontend.
On Linux, the Makefile automatically adds `-tags webkit2_41` if your distro
only has `webkit2gtk-4.1` installed.

The first run generates `frontend/wailsjs/` (the Wails JS/TS bindings) and
`frontend/dist/`; both are gitignored build output. A bare `npm run build`
in `frontend/` will fail until `wails dev` or `wails build` has generated
`frontend/wailsjs/` at least once.

## Building

```sh
make build
```

Produces a production binary at `build/bin/masque`.

### Packaging

Installers are unsigned for now (code signing is deferred until public
release). Linux packages can be built locally; Windows and macOS need their
own OS, so CI builds those.

```sh
make appimage   # build/bin/Masque-<version>-x86_64.AppImage (bundles GTK + WebKitGTK)
make deb        # build/bin/masque_<version>_amd64.deb (depends on libwebkit2gtk-4.1-0)
```

`build/linux/appimage.sh` downloads linuxdeploy and Tauri's fork of
linuxdeploy-plugin-gtk into `build/linux/.tools/` (gitignored) on first use.
Build the AppImage on the oldest glibc you want to support; it copies
WebKit's helper processes into the bundle and pins the working directory to
`usr/` so the library's rewritten paths resolve. By AppImage convention it
still expects what any graphical desktop has (fontconfig, harfbuzz, Mesa
GL/EGL/GLES, fonts) but no WebKitGTK. The .deb is a plain `dpkg-deb`
layout, no bundling.

`build/darwin/dmg.sh` wraps `build/bin/masque.app` (from
`wails build -platform darwin/universal`) in a drag-to-Applications dmg and
ad-hoc signs it. Windows uses Wails' NSIS support
(`wails build -platform windows/amd64 -nsis`; needs `makensis` on PATH).

### CI

- `.github/workflows/ci.yml` runs Go tests, golangci-lint and a full
  `wails build` (which type-checks the frontend) on every push to `main` and
  every pull request.
- `.github/workflows/release.yml` builds all three platforms, smoke-launches
  each result on a clean runner (the AppImage on an Ubuntu 24.04 box with
  WebKitGTK removed, the .deb via `apt install`, the NSIS installer with `/S`,
  the dmg mounted and its app launched), and on a `vX.Y.Z` tag opens a
  **draft** GitHub release with the artifacts and a `SHA256SUMS.txt`. Trigger
  it by hand from the Actions tab for a dry run; the version is taken from
  the tag (`0.0.0` for manual runs) and stamped into `wails.json`.

To cut a release: bump `info.productVersion` in `wails.json`, commit, then
`git tag vX.Y.Z && git push origin vX.Y.Z`, and publish the draft once the
smoke jobs are green.

## Testing and linting

```sh
make test   # go test ./... (with -tags webkit2_41 where needed)
make lint   # golangci-lint run (config: .golangci.yml)
```

## Other useful commands

```sh
make clean  # remove build/bin, frontend/dist contents, frontend/wailsjs
```

## Project layout

```
app.go, main.go          Wails app entry point, service wiring
internal/
  card/                   Card parsing/export (PNG chunks, V1/V2/V3 JSON)
  character/              Characters library service (bound to frontend)
  chat/                   Chat orchestration service (bound to frontend)
  datadir/                Platform data directory resolution
  prompt/                 Prompt assembly + token budgeting
  provider/               Provider interface; ollama/, openai/, anthropic/
  settings/               Key/value settings service (bound to frontend)
  store/                  SQLite access layer + embedded migrations
frontend/
  src/                    React/TypeScript app
  src/components/ui/      shadcn/ui components (new-york style)
  src/screens/            Chat and Settings screens
```

All SQLite access goes through `internal/store`. Migrations are embedded SQL
files in `internal/store/migrations/`, applied in order and tracked via
`PRAGMA user_version` — a shipped migration is never edited, only added to.

The app's SQLite database (`masque.db`) lives in the platform data
directory: `$XDG_DATA_HOME/masque` (or `~/.local/share/masque`) on Linux,
`~/Library/Application Support/Masque` on macOS, `%APPDATA%/Masque` on
Windows.
