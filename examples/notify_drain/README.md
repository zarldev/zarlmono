# Notification subscription and offline drain

A deterministic example of the two session delivery paths in `znotify.NotificationStore`:

- a `Push` with no subscriber is retained for a later `Drain`;
- after `Subscribe`, a `Push` is delivered immediately on the live channel;
- `Unsubscribe` explicitly ends the subscription and closes its store-owned channel;
- `Drain` returns and clears the earlier offline notification.

There are no goroutines in the example. The main function owns the subscription and explicitly unsubscribes before exit. It makes no network or LLM calls.

Run from the repository root:

```sh
go run -C examples ./notify_drain
```

Verify:

```sh
go test -C examples ./notify_drain
```
