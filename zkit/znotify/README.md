# `zkit/znotify`

Session-keyed pub/sub with offline queueing. A subscriber identified
by a session ID receives notifications targeted at that session
immediately; a session with no live subscriber accumulates
notifications in a per-session queue that drains on the next subscribe.

Not agent-specific: any session-keyed delivery problem (web push,
SSE fan-out) can use it.

## Quick start

```go
store := notify.NewNotificationStore()

// Live consumer subscribes — auto-unsubscribes when ctx is done.
ch := store.Subscribe(ctx, "session-A")

go func() {
    for n := range ch {
        // handle the notification
    }
}()

// Producer pushes a notification targeted at session-A.
store.Push(notify.Notification{
    SessionID: "session-A",
    Content:   "task complete",
})

// Or fan out to every active session.
store.Broadcast(notify.Notification{
    Content: "system maintenance in 5 minutes",
})
```

## Subscribe lifetimes

`Subscribe(ctx, sessionID)` ties the subscription to ctx — when ctx
is cancelled, the subscription is removed and the channel is closed
via `context.AfterFunc`. Callers that want explicit lifecycle pass
`context.Background()` and call `Unsubscribe` themselves.

```go
ch := store.Subscribe(ctx, "alice")
defer store.Unsubscribe("alice", ch)  // explicit; redundant when ctx cancels
```

## Offline delivery

A push to a session with no live subscriber is queued. When a
subscriber later attaches, it can `Drain` the session's queue to
replay missed notifications:

```go
store.Push(notify.Notification{SessionID: "alice", Content: "missed me"})
// ...alice subscribes later...
ch := store.Subscribe(ctx, "alice")
queued := store.Drain("alice")  // returns the buffered notifications
```

This makes the store usable for clients that disconnect and reconnect
(TUI sessions, web frontends), where a notification produced during
the disconnected window should still surface.

## Buffer size

Channels returned by `Subscribe` have a small fixed buffer (16) by
default. Slow readers silently drop pushed messages — back-pressure
without panic. Configure with the option:

```go
store := notify.NewNotificationStore(
    notify.WithSubscriberBuffer(64),
)
```

## Key types

- [`Notification`] — payload struct with `SessionID`, `ToolName`, `Content`, `Broadcast`.
- [`NotificationStore`] — the pub/sub. Subscribe / Unsubscribe / Push / Broadcast / Drain.
- [`WithSubscriberBuffer`] — option to override the default channel buffer.

See [`AGENTS.md`](AGENTS.md) for design notes (channel close
discipline, broadcast subtleties, why dropped notifications are silent).

[`Notification`]: https://pkg.go.dev/github.com/zarldev/zarlmono/zkit/znotify#Notification
[`NotificationStore`]: https://pkg.go.dev/github.com/zarldev/zarlmono/zkit/znotify#NotificationStore
[`WithSubscriberBuffer`]: https://pkg.go.dev/github.com/zarldev/zarlmono/zkit/znotify#WithSubscriberBuffer
