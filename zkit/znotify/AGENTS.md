# AGENTS.md — `zkit/znotify`

Notes for editors. See [`zkit/agent/runner/AGENTS.md`](../agent/runner/AGENTS.md) for the runner-side concurrency story and [`zkit/agent/mcp/AGENTS.md`](../agent/mcp/AGENTS.md) for the adapter that pushes notifications onto the runner's inject queue.

## What this package does

`NotificationStore` is a session-keyed pub/sub with offline delivery. Live subscribers receive notifications immediately on a buffered channel; sessions with no active subscriber accumulate notifications in a per-session queue for later retrieval. Broadcasts fan out to every active subscriber.

## Three delivery paths

- **`Push`** — deliver to session X. With a live subscriber, send immediately (drop if the buffer is full); with none, queue for `Drain`. The default.
- **`Broadcast`** — every active subscriber across every session sees this. For lifecycle events (restarts, permission changes) that belong to no one session. Also queues a copy on every known session's pending list for context on their next `Drain`.
- **`Drain`** — return all pending notifications for a session and clear the queue. A reconnecting subscriber pulls what it missed.

If you want a fourth, reconsider whether it's a real need or a special case of these three.

## Best-effort live delivery is the contract

`Push` to a live subscriber whose channel buffer is full **silently drops** — no error, no log. A slow consumer shouldn't back-pressure the producer indefinitely; offline queueing is the durability layer. If you need guaranteed delivery, persist the notification yourself — this is not an at-least-once queue.

`Subscribe` returns a buffered channel (default 16, `WithSubscriberBuffer`): small enough to back-pressure slow consumers, large enough to absorb normal bursts. Each session's offline queue is capped (default 1024, `WithPendingPerSession`); at the cap, the oldest entry is dropped so the newest lands, preventing unbounded growth for sessions that never reconnect.

## Concurrency model

One mutex covers both the offline queues and the live-subscriber registry — one lock, no AB/BA deadlock. The producer side of every channel is owned by the store: external code must never send to a channel returned by `Subscribe`, since `Unsubscribe` closes it under that same lock.

`Subscribe` takes a context so a subscription can be tied to a request, session, or worker lifetime without an explicit `Unsubscribe`; the implementation uses `context.AfterFunc`, firing once on cancellation and a no-op on the happy path. Callers preferring explicit lifecycle pass `context.Background()` and call `Unsubscribe` (idempotent, so the redundant case is harmless).

## Things to never do

- **Don't add cross-session ordering guarantees.** Two pushes from different goroutines have no guaranteed arrival order; sequence on the producer side if you need it.
- **Don't add filtering or routing rules.** The store routes by session ID and fans out broadcasts. Everything else is consumer policy.
- **Don't bake in transport assumptions.** The store yields a channel and expects the subscriber to drain it; whether that forwards to WebSocket, SSE, or stdout is the subscriber's business.
