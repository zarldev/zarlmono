#!/usr/bin/env bash
set -euo pipefail

if ! command -v tmux >/dev/null 2>&1; then
  echo "tui smoke: tmux is required" >&2
  exit 2
fi

timeout_seconds=${ZARLCODE_TUI_SMOKE_TIMEOUT:-30}
if ! [[ "$timeout_seconds" =~ ^[1-9][0-9]*$ ]]; then
  echo "tui smoke: ZARLCODE_TUI_SMOKE_TIMEOUT must be a positive integer" >&2
  exit 2
fi

tmp=$(mktemp -d)
socket="zarlcode-smoke-$$"
session=smoke
cleanup() {
  tmux -L "$socket" kill-server 2>/dev/null || true
  rm -rf "$tmp"
}
trap cleanup EXIT INT TERM

strip_ansi() {
  perl -pe 's/\e\[[0-9;?]*[ -\/]*[@-~]//g'
}

capture() {
  local name=$1
  tmux -L "$socket" capture-pane -p -e -t "$session:0.0" | strip_ansi >"$tmp/$name"
}

wait_for_text() {
  local name=$1
  local want=$2
  local deadline=$((SECONDS + timeout_seconds))
  while (( SECONDS < deadline )); do
    if ! tmux -L "$socket" has-session -t "$session" 2>/dev/null; then
      echo "tui smoke: session exited while waiting for $want" >&2
      return 1
    fi
    capture "$name"
    if grep -Fq "$want" "$tmp/$name"; then
      return 0
    fi
    sleep 0.2
  done
  echo "tui smoke: timed out waiting for $want" >&2
  cat "$tmp/$name" >&2 || true
  return 1
}

binary=${ZARLCODE_TUI_SMOKE_BINARY:-}
if [ -z "$binary" ]; then
  binary="$tmp/zarlcode"
  go build -C zarlcode -o "$binary" ./cmd
fi
binary=$(realpath "$binary")
mkdir "$tmp/workspace" "$tmp/home"
if [ -n "${ZARLCODE_TUI_SMOKE_HOME:-}" ]; then
  smoke_home=$(realpath "$ZARLCODE_TUI_SMOKE_HOME")
else
  smoke_home="$tmp/home"
fi

tmux -L "$socket" -f /dev/null new-session -d -x 80 -y 24 -c "$tmp/workspace" -s "$session" \
  "HOME='$smoke_home' TERM=xterm-256color '$binary'"

wait_for_text startup "ctrl+g keys"
grep -Fq "workspace" "$tmp/startup"

tmux -L "$socket" resize-window -t "$session" -x 60 -y 16
tmux -L "$socket" send-keys -t "$session" C-g
wait_for_text help "[keys]"
grep -Fq "ctrl+g / esc close" "$tmp/help"

tmux -L "$socket" send-keys -t "$session" Escape
sleep 0.2
tmux -L "$socket" send-keys -t "$session" C-c
wait_for_text quit "[quit]"
grep -Fq "quit ƶarl/code?" "$tmp/quit"
tmux -L "$socket" send-keys -t "$session" Enter

exit_deadline=$((SECONDS + timeout_seconds))
while tmux -L "$socket" has-session -t "$session" 2>/dev/null; do
  if (( SECONDS >= exit_deadline )); then
    echo "tui smoke: session did not exit after quit confirmation" >&2
    capture stuck || true
    cat "$tmp/stuck" >&2 || true
    exit 1
  fi
  sleep 0.2
done

echo "tui smoke: startup, resize, help, quit, and shutdown passed"
