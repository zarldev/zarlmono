---
title: zarlai
description: A local multimodal assistant built on zkit — speech, vision, home automation, and autonomous background tasks.
---

zarlai is a local, multimodal conversational assistant. Like zarlcode, it
is a real product in this repo and exercises the same zkit interfaces in a
very different shape.

## What it does

- Speech-to-text via sherpa-onnx (Whisper / Moonshine)
- Text-to-speech via sherpa-onnx (Kokoro)
- LLM inference through any OpenAI-compatible endpoint
- Face recognition via dlib / go-face
- Tool control for Home Assistant, Spotify, timers, memory, and web search
- Autonomous background tasks with a cron scheduler
- Per-person memory and wiki embeddings in Qdrant

## Key zkit packages it uses

| Package | Role |
|---|---|
| `zkit/agent/runner` | The streaming agent loop |
| `zkit/ai/llm` | Provider abstraction for chat and task models |
| `zkit/ai/tools` | Tool registry, dynamic registration, MCP bridging |
| `zkit/agent/sensor` | Periodic observers for ambient awareness |
| `zkit/agent/taskrunner` | Background autonomous task loops |
| `zkit/notify` / `zkit/messagebus` | Notifications and pub/sub |

## Where to find it

The source lives at [`zarlai/`](https://github.com/zarldev/zarlmono/tree/main/zarlai).

See `zarlai/AGENTS.md` for build instructions — it has CGO dependencies
(dlib, sherpa-onnx) and its own CI job.
