# Running Zarl — operator notes

The [README](../README.md) covers install + quickstart. This page is
the runtime reference: which command does what, how to verify
services, model download recipes, hardware-tier examples, and the
gotchas that aren't obvious from `task --list-all`.

For the architecture (what each layer does, how a request flows
through), see [`architecture.md`](architecture.md).

## Verifying services

```bash
task ps                                                            # compose status
curl -fsS http://localhost:6333/healthz                            # qdrant
curl -fsS http://localhost:8888/ -o /dev/null -w '%{http_code}\n'  # searxng
curl -fsS http://localhost:8081/v1/models                          # llama-server
curl -fsS http://localhost:8080/ -o /dev/null -w '%{http_code}\n'  # zarl
```

Notes:

- Qdrant's compose healthcheck runs `wget`, which the `qdrant/qdrant`
  image no longer ships — `task ps` will show it as **unhealthy** even
  when the service is fine. Verify from the host with the `/healthz`
  probe above.
- Dolt listens on **:3307** (not the default 3306) to avoid collisions;
  DSN is `root:@tcp(localhost:3307)/zarl?parseTime=true`.
- Llama-server boots with the `llm` compose profile. First boot has to
  load the Q3_K_XL weights onto the GPU — give it ~2 minutes before
  the health probe turns green.

## Taskfile overview

| Command               | What it does                                            |
|-----------------------|---------------------------------------------------------|
| `task setup`          | first-time bootstrap (`frontend:install` + `proto`)     |
| `task doctor`         | preflight: toolchain, `.env`, models, services          |
| `task up`             | dolt + qdrant + searxng                                 |
| `task up:llm`         | adds llama-server (GPU)                                 |
| `task down`           | stop all compose services                               |
| `task ps`             | compose status                                          |
| `task logs -- <svc>`  | tail compose logs for a service                         |
| `task proto`          | `buf lint` + generate (Go + TS)                         |
| `task frontend:build` | production Vite build                                   |
| `task frontend:dev`   | Vite dev server on :5173, proxies RPC to :8080          |
| `task build`          | `frontend:build` + `go build -o zarl ./cmd/zarl`        |
| `task run`            | run the existing binary — does **not** rebuild          |
| `task test`           | `go test -race -count=1 ./...`                          |
| `task clean`          | remove binary, frontend `dist/`, `node_modules/`        |

`task run` deliberately does **not** depend on `task build`. Build is
manual — rerun it after Go or frontend changes. There is no hot-reload
target for the backend; restart `task run` after rebuilding.

## Models

zarl needs four model families on disk before it will start. They all
live under one root, `MODELS_DIR` (default `./deploy/models`), with
fixed subpaths matching upstream tarball layouts — unpack each release
in place and you're done.

### STT — sherpa-onnx Whisper

Path: `deploy/models/whisper-small-en/`. Files (the `int8` variants are
smaller and run fine on CPU):

- `small.en-encoder.int8.onnx`
- `small.en-decoder.int8.onnx`
- `small.en-tokens.txt`

Bundled as `sherpa-onnx-whisper-small.en.tar.bz2` on the
[k2-fsa/sherpa-onnx releases page](https://github.com/k2-fsa/sherpa-onnx/releases).
Extract into `deploy/models/whisper-small-en/`.

### TTS — sherpa-onnx Kokoro

Path: `deploy/models/kokoro-en-v0_19/`. Bundled as
`kokoro-en-v0_19.tar.bz2` on the same
[sherpa-onnx releases page](https://github.com/k2-fsa/sherpa-onnx/releases).
Extract there and you'll get `model.onnx`, `voices.bin`, `tokens.txt`,
`lexicon-us-en.txt`, `lexicon-gb-en.txt`.

### Face recognition — dlib

Path: `deploy/models/dlib/`. Both files come from
[dlib.net/files](http://dlib.net/files/) as `.bz2`; decompress with
`bunzip2`:

- [`shape_predictor_5_face_landmarks.dat.bz2`](http://dlib.net/files/shape_predictor_5_face_landmarks.dat.bz2)
- [`dlib_face_recognition_resnet_model_v1.dat.bz2`](http://dlib.net/files/dlib_face_recognition_resnet_model_v1.dat.bz2)

### LLM — GGUF weights

Path: `${MODELS_DIR}/` (mounted into the `llama-server` container at
`/models`). The default `deploy/docker-compose.yml` `command:`
references two specific files — change either one and you need to
update the compose file accordingly:

| File | What it is |
|---|---|
| `Qwen3.6-35B-A3B-UD-Q3_K_XL.gguf` | Main weights — Unsloth's "Dynamic" Q3_K_XL quant of Qwen3.6-35B-A3B |
| `mmproj-F16.gguf` | Multimodal projector — lets the model see camera frames |

Sources:

- **Unsloth's GGUF mirrors** (recommended for the default config) —
  [huggingface.co/unsloth](https://huggingface.co/unsloth) hosts the
  `Qwen3.6-35B-A3B-GGUF` repo with the exact filenames docker-compose
  expects. The Unsloth blog post linked from
  `deploy/docker-compose.yml` documents the recommended sampler
  settings: [unsloth.ai/docs/models/qwen3.6](https://unsloth.ai/docs/models/qwen3.6).
- **Official Qwen weights** —
  [huggingface.co/Qwen](https://huggingface.co/Qwen) hosts the
  canonical full-precision releases. Convert/quantise yourself with
  `llama.cpp`'s `convert_hf_to_gguf.py` if you don't want to rely on a
  third-party quant.
- **Smaller models for smaller VRAM** — see *Hardware alternatives*
  below.

## Hardware alternatives

The default config targets a single 24 GB NVIDIA GPU running
Qwen3.6-35B locally. **You do not need that hardware** — zarl talks to
any OpenAI-compatible endpoint:

- **Mac / Linux without an NVIDIA GPU** — install
  [Ollama](https://ollama.com/), pull a smaller chat model, and point
  zarl at Ollama's OpenAI-compatible endpoint:

  ```bash
  ollama pull qwen2.5:7b-instruct
  ollama pull nomic-embed-text
  # in .env:
  CHAT_URL=http://localhost:11434/v1
  CHAT_MODEL=qwen2.5:7b-instruct
  ```

  Skip `task up:llm` — Ollama replaces the llama-server container.

- **Smaller NVIDIA GPU (8–16 GB)** — keep llama-server but pick a
  smaller GGUF (e.g. `qwen2.5-7b-instruct-q4_k_m.gguf`) and adjust the
  `command:` list in `deploy/docker-compose.yml` (`-c` context size,
  remove `--cache-type-{k,v} q8_0` if needed).

- **Hosted endpoint (zero local compute)** — point at any provider
  exposing the OpenAI shape:

  ```bash
  # .env example for OpenRouter (works the same for Groq, Together,
  # Fireworks):
  CHAT_URL=https://openrouter.ai/api/v1
  CHAT_MODEL=anthropic/claude-sonnet-4
  OPENAI_API_KEY=sk-or-...
  ```

  Embeddings still need Ollama (or any OpenAI-compatible embeddings
  endpoint with a small change to `service/embedder.go`).

The face-recognition + STT + TTS paths run locally via sherpa-onnx +
dlib regardless of where the LLM lives — those don't need a GPU.

## Environment variables (full)

The README only documents the essentials. Full set:

| Variable | Default | Purpose |
|---|---|---|
| `PORT` | `8080` | HTTP port for zarl |
| `CHAT_URL` | — | OpenAI-compatible endpoint for the main LLM |
| `CHAT_MODEL` | — | Model name passed to llama-server |
| `TASK_CHAT_URL` | = `CHAT_URL` | Separate endpoint for the taskrunner |
| `TASK_CHAT_MODEL` | = `CHAT_MODEL` | Separate model for the taskrunner |
| `EMBED_URL` | `http://localhost:11434/v1` | OpenAI-compatible /v1 embeddings endpoint |
| `EMBED_MODEL` | `nomic-embed-text` | Embeddings model |
| `MODELS_DIR` | `./deploy/models` | Single root for STT, TTS, dlib, GGUFs (subpaths fixed) |
| `DOLT_DSN` | `root:@tcp(localhost:3307)/zarl?parseTime=true` | Database DSN — `parseTime=true` is required by the driver |

Tool providers (Home Assistant, Obsidian, Spotify, MCP servers, etc.)
are configured at runtime in `/admin → Tools`, not via environment.

## Shutdown

```bash
# backend: ctrl+c on the `task run` terminal
task down                  # stop compose services
```
