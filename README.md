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

**Status:** M1.5 — chat polish: regenerate-as-swipes with left/right
navigation (alternate greetings included), in-place message editing, a
default persona (name + description) that flows into prompts, and a
chat list with multiple chats per character, resume, and delete. On top
of card import (PNG/JSON, V1/V2/V3) with a characters screen, and three
providers (Ollama native, OpenAI-compatible, Anthropic) with mid-chat
switching. Ollama management/onboarding and dev mode land in later M1
milestones; see `docs/masque-dev-spec-m1.md` for the full build order
(not checked into this repo).

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

## Testing and linting

```sh
make test   # go test ./...
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
