#!/usr/bin/env bash
set -euo pipefail

: "${OPENAI_API_KEY:?set OPENAI_API_KEY first}"

export LLM_PROVIDER="${LLM_PROVIDER:-openai}"
export LLM_MODEL="${LLM_MODEL:-gpt-4o-mini}"

exec go run ./examples/computer_use \
  -chrome "${CHROME_BIN:-/usr/bin/chromium-browser}" \
  -provider "$LLM_PROVIDER" \
  -model "$LLM_MODEL" \
  -headless=false \
  -pause "${PAUSE:-30s}" \
  "$@"
