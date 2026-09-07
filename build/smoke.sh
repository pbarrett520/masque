#!/usr/bin/env bash
# Launch a GUI binary, make sure it is still alive after a grace period,
# then stop it. A stand-in for "it opens on a clean machine" in CI.
# Usage: build/smoke.sh [-t seconds] <command> [args...]
set -uo pipefail
GRACE=10
if [ "${1:-}" = "-t" ]; then GRACE="$2"; shift 2; fi
[ $# -ge 1 ] || { echo "usage: $0 [-t secs] <command> [args...]" >&2; exit 2; }

# Kill a process and its descendants (AppImage runtime -> app -> WebKit helpers).
killtree() {
  local c
  for c in $(pgrep -P "$1" 2>/dev/null); do killtree "$c"; done
  kill "$1" 2>/dev/null || true
}

LOG="$(mktemp)"
"$@" >"$LOG" 2>&1 &
PID=$!
sleep "$GRACE"
if kill -0 "$PID" 2>/dev/null; then
  echo "smoke: '$*' still running after ${GRACE}s — OK"
  killtree "$PID"
  sleep 1
  kill -9 "$PID" 2>/dev/null || true
  wait "$PID" 2>/dev/null
  echo "--- output ---"; cat "$LOG"; rm -f "$LOG"
  exit 0
fi
wait "$PID"; CODE=$?
echo "smoke: '$*' exited early with code $CODE — FAIL"
echo "--- output ---"; cat "$LOG"
if [ -n "${GITHUB_ACTIONS:-}" ]; then
  # Surface the tail of the output as check-run annotations (readable via
  # the public API without a token, unlike the raw job log).
  echo "::error::smoke: '$*' exited with code $CODE"
  tail -n 8 "$LOG" | while IFS= read -r line; do echo "::error::${line:0:900}"; done
fi
rm -f "$LOG"
exit 1
