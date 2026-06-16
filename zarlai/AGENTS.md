# AGENTS.md

Agent/contributor guidance for the zarlai module. The LLM layer delegates to the shared `zkit/ai/llm` providers; see the repo-root [AGENTS.md](../AGENTS.md) for the workspace overview.

## What zarlai is

A local, multimodal conversational assistant: speech-to-text (Whisper/Moonshine via sherpa-onnx), LLM inference (any OpenAI-compatible endpoint — llama.cpp, vLLM, OpenRouter, Anthropic, …), text-to-speech (Kokoro via sherpa-onnx), and face recognition (dlib/go-face). A single Go binary with an embedded React frontend. Its tool-calling system controls Home Assistant, searches the web, manages per-person memory, and runs autonomous background tasks.

## Build & dev commands

Uses **Task** (`Taskfile.yml`):

```bash
task setup          # first-time bootstrap (frontend:install + proto)
task proto          # buf lint + buf generate (proto → Go + TypeScript)
task up             # docker compose up -d (dolt, qdrant, searxng)
task up:llm         # also brings up llama-server (GPU profile, :8081)
task frontend:dev   # Vite dev server on :5173, proxies RPC to :8080
task build          # single binary with embedded frontend
task run            # ./zarl with .env loaded (does NOT rebuild)
task test           # Go tests with -race
```

Backend rebuilds are manual: edit Go → `task build` → restart `task run`. No backend hot-reload. Single test: `go test -race -run TestName ./path/...`. After editing `repository/queries/*.sql` or `migrations/`: `cd repository && sqlc generate`.

## Architecture

Single-binary deployment: a Go backend serves the ConnectRPC API and the embedded React SPA.

- **`cmd/zarl/`** — entry point, dependency wiring, HTTP/2 server (h2c).
- **`service/`** — business logic: LlamaCppClient (primary LLM via an OpenAI-compatible endpoint), OllamaClient (embeddings), Transcriber, KokoroSynthesizer, FaceService, Session, NotificationStore, the tool selector.
- **`repository/`** — sqlc-generated data access against Dolt (MySQL-compatible, :3307).
- **`transport/grpc/`** — ConnectRPC handlers (ZarlServer, AdminServer).
- **`proto/zarl/v1/`** — protobuf definitions; the source of truth for API types.
- **`frontend/`** — React 19 + Vite + Tailwind v4 + ConnectRPC web client.

Each layer owns its types; map between layers at the boundaries.

**Code generation:** `buf generate` outputs Go to `transport/grpc/gen/` and TypeScript to `frontend/src/gen/`; sqlc generates `repository/gen/` from `repository/queries/*.sql`.

### Tool system

All tools implement `zkit/ai/tools.Tool` and register in a registry, organised by location: `tools/` (task management, time, gesture, chart rendering), `tools/homeassistant/`, `tools/memory/` (per-person memory in Qdrant), `tools/searxng/`, `tools/wiki/`, `tools/timer/`, `tools/spotify/`, and `tools/code/` (file-mutating tools from `zkit/ai/tools/code`). Provider config (Spotify, Home Assistant, MCP servers) lives in the `tool_providers` table, managed via `transport/grpc/admin_tools.go`.

Tools are selected per turn by `service.ToolSelector`: the user's latest message is embedded against each tool's description (cached, invalidated on Registry version change), and the top-N cosine-ranked tools plus a small always-on core (`current_time`, `gesture`, `render_chart`, `remember`, `recall`) ship on the Chat request. This keeps per-turn tool-spec overhead small on a 50+ tool roster.

### Background task runner

`taskrunner/runner.go` runs autonomous LLM loops in the background:

- Shares the llama-server with the conversation path but uses a dedicated `*openai.Client` + `*http.Client` so the two request streams don't serialize in Go. Server-side concurrency comes from llama.cpp's `-np 2` (two parallel slots).
- Iterates up to `max_iterations` (default 20), calling tools and accumulating findings.
- `start_task` / `schedule_task` are excluded from the runner's own tool set to prevent recursive spawning.
- The conversation lock yields to real-time conversation when active.
- Findings are embedded into Qdrant (`task_findings`); per-iteration notifications push to the frontend via `NotificationStore`.
- `taskrunner/scheduler.go` handles cron recurrence via `robfig/cron/v3`.

**Profiles** determine a task's personality (model override, prompt prefix, iteration count) and its tool gate. Code-defined skeletons (`default`, `researcher`, `coder`) live in `taskrunner/builtin_profiles.go`; the per-profile tool gate (named tools + provider opt-ins) is `taskrunner.BuiltinToolGates`. Resolution composes `zkit/agent/profile`'s registry (persona + execution settings, with operator overrides merged at runtime) with the registry-level tool gate.

### Sensors

`zkit/agent/sensor` (wrapped by `zarlai/sensor`) runs periodic observers that broadcast when an observation changes, giving the assistant ambient awareness. The `Sensor` interface returns `ErrNoChange` when unchanged; the runner owns one goroutine per sensor (keep handlers fast). Concrete sensors: Home Assistant state, MCP pushes, time-of-day, tool-wrapping, Spotify now-playing.

## Environment variables

Defaults match `cmd/zarl/config.go`; `.env.example` is the canonical reference and `docs/running.md` has the full table. Required: `CHAT_URL` (OpenAI-compatible /v1 endpoint), `CHAT_MODEL`. Common optional: `PORT` (8080), `TASK_CHAT_URL`/`TASK_CHAT_MODEL` (default to the chat ones), `EMBED_URL` (`http://localhost:11434/v1`), `MODELS_DIR` (`./deploy/models`), `DOLT_DSN`.

## Backing services (docker-compose)

Dolt (:3307, MySQL-compatible), Qdrant (:6333, vectors for memory/wiki/findings), SearXNG (:8888, the `web_search` backend).
