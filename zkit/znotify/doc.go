// Package znotify provides session-keyed notifications with live pub/sub and
// bounded offline queueing.
//
// It is useful for long-running agent shells and service frontends that need to
// fan messages to active subscribers while preserving a small backlog for
// sessions that reconnect later. Delivery is best-effort, not a durable message
// queue.
package znotify
