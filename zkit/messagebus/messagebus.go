package messagebus

import (
	"context"
	"maps"
	"time"
)

// Headers is a semantic type for message metadata.
type Headers map[string]string

// Get retrieves a header value.
func (h Headers) Get(key string) string {
	return h[key]
}

// Set adds or updates a header value.
func (h Headers) Set(key, value string) {
	h[key] = value
}

// Has checks if a header exists.
func (h Headers) Has(key string) bool {
	_, ok := h[key]
	return ok
}

// Clone creates a copy of headers.
func (h Headers) Clone() Headers {
	clone := make(Headers, len(h))
	maps.Copy(clone, h)
	return clone
}

// Message represents a typed message.
type Message[T any] struct {
	Subject   string
	Data      T
	Headers   Headers
	Timestamp time.Time
}

// Handler processes messages.
type Handler[T any] func(ctx context.Context, msg Message[T]) error

// Subscription represents an active subscription.
type Subscription interface {
	// Unsubscribe closes the subscription
	Unsubscribe() error
	// IsValid returns true if subscription is active
	IsValid() bool
}

// Bus provides typed pub/sub operations.
type Bus[T any] interface {
	// Publish sends a message to a subject
	Publish(ctx context.Context, subject string, data T) error

	// PublishWithHeaders sends a message with custom headers
	PublishWithHeaders(ctx context.Context, subject string, data T, headers Headers) error

	// Subscribe creates a subscription to a subject
	Subscribe(ctx context.Context, subject string, handler Handler[T]) (Subscription, error)

	// QueueSubscribe creates a queue group subscription
	QueueSubscribe(ctx context.Context, subject string, queue string, handler Handler[T]) (Subscription, error)

	// Request sends a request and waits for a reply
	Request(ctx context.Context, subject string, data T, timeout time.Duration) (T, error)

	// Close shuts down the bus
	Close() error
}
