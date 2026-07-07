# Computer-use Wikipedia quiz

This example demonstrates the universal computer-use flow with a visible browser:

1. Fetch random Wikipedia summaries with `zhttp`.
2. Ask an LLM to generate plausible distractors for each summary.
3. Serve a local multiple-choice quiz.
4. Use `computer_observe` to read the browser UI.
5. Ask the LLM to choose one of the observed answer buttons.
6. Use `computer_act` to click the selected answer.

The example intentionally drives the typed tool registry directly instead of a full agent loop so the observe → LLM → act workflow is easy to follow.

## Requirements

- Chrome or Chromium.
- Network access to Wikipedia.
- An LLM backend configured through the usual zkit environment variables.

For OpenAI:

```sh
export OPENAI_API_KEY=...
export LLM_PROVIDER=openai
export LLM_MODEL=gpt-4o-mini
```

Do not commit API keys. If a key is pasted into a terminal transcript or chat, revoke and rotate it.

## Run

From the repository root:

```sh
./examples/computer_use/run.sh
```

The helper script defaults to:

- `CHROME_BIN=/usr/bin/chromium-browser`
- `LLM_PROVIDER=openai`
- `LLM_MODEL=gpt-4o-mini`
- visible browser mode (`-headless=false`)
- `PAUSE=30s` so the browser remains visible after completion

Override values as needed:

```sh
CHROME_BIN=/snap/bin/chromium PAUSE=2m ./examples/computer_use/run.sh
```

Or run the Go program directly:

```sh
go run ./examples/computer_use \
  -chrome /usr/bin/chromium-browser \
  -provider openai \
  -model gpt-4o-mini \
  -headless=false \
  -pause 30s
```

## Logging

The example configures `zkit/zlog` for structured console logs. Debug logs show fetched summaries, generated quiz questions, LLM requests, and raw LLM responses.

Set the normal zlog/log-level environment used by your shell profile if you want debug output. At info level you should still see the main flow:

```text
fetching random wikipedia summaries
building quiz questions
navigating to quiz
question answered
quiz complete
```

## Files

- `main.go` — CLI flags and top-level orchestration.
- `harness.go` — browser session, tool registration, and observe → LLM → act loop.
- `llm.go` — LLM provider setup, answer selection, and distractor generation.
- `wiki.go` — Wikipedia fetching and quiz construction.
- `server.go` — local quiz web page.
- `tools.go` — typed tool-call helper.
