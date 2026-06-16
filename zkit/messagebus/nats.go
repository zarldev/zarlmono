package messagebus

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/zarldev/zarlmono/zkit/options"
)

// NATSBus implements Bus using NATS.
type NATSBus[T any] struct {
	conn       *nats.Conn
	js         nats.JetStreamContext
	serializer Serializer[T]
	config     *natsConfig
}

// Serializer handles message encoding and decoding for NATS payloads.
type Serializer[T any] interface {
	// Encode converts a typed payload into bytes for publishing.
	Encode(data T) ([]byte, error)
	// Decode converts bytes received from NATS into a typed payload.
	Decode(data []byte) (T, error)
}

// JSONSerializer provides JSON encoding.
type JSONSerializer[T any] struct{}

// Encode marshals data as JSON.
func (s JSONSerializer[T]) Encode(data T) ([]byte, error) {
	return json.Marshal(data)
}

// Decode unmarshals JSON data into T.
func (s JSONSerializer[T]) Decode(data []byte) (T, error) {
	var result T
	err := json.Unmarshal(data, &result)
	return result, err
}

// natsConfig holds NATS configuration.
type natsConfig struct {
	url           string
	maxReconnect  int
	reconnectWait time.Duration
	timeout       time.Duration
	useJetStream  bool
}

// defaultNATSConfig returns default configuration.
func defaultNATSConfig() *natsConfig {
	return &natsConfig{
		url:           "nats://localhost:4222",
		maxReconnect:  10,
		reconnectWait: 2 * time.Second,
		timeout:       30 * time.Second,
		useJetStream:  false,
	}
}

// NewNATSBus creates a NATS bus with optional configuration.
func NewNATSBus[T any](opts ...options.Option[NATSBus[T]]) (Bus[T], error) {
	config := defaultNATSConfig()

	bus := &NATSBus[T]{
		config:     config,
		serializer: JSONSerializer[T]{},
	}

	// Apply options
	for _, opt := range opts {
		opt(bus)
	}

	// Connect to NATS
	natsOpts := []nats.Option{
		nats.MaxReconnects(config.maxReconnect),
		nats.ReconnectWait(config.reconnectWait),
		nats.Timeout(config.timeout),
	}

	conn, err := nats.Connect(config.url, natsOpts...)
	if err != nil {
		return nil, fmt.Errorf("connect to NATS: %w", err)
	}

	bus.conn = conn

	// Set up JetStream if requested
	if config.useJetStream {
		js, err := conn.JetStream()
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("create JetStream context: %w", err)
		}
		bus.js = js
	}

	return bus, nil
}

// WithNATSURL sets the NATS server URL used when connecting.
func WithNATSURL[T any](url string) options.Option[NATSBus[T]] {
	return func(bus *NATSBus[T]) {
		bus.config.url = url
	}
}

// WithJetStream publishes and subscribes through JetStream instead of core NATS.
func WithJetStream[T any]() options.Option[NATSBus[T]] {
	return func(bus *NATSBus[T]) {
		bus.config.useJetStream = true
	}
}

// WithNATSTimeout sets the timeout used for the initial NATS connection.
func WithNATSTimeout[T any](timeout time.Duration) options.Option[NATSBus[T]] {
	return func(bus *NATSBus[T]) {
		bus.config.timeout = timeout
	}
}

// WithMaxReconnect sets the maximum number of NATS reconnect attempts.
func WithMaxReconnect[T any](maxReconnect int) options.Option[NATSBus[T]] {
	return func(bus *NATSBus[T]) {
		bus.config.maxReconnect = maxReconnect
	}
}

// WithSerializer replaces the default JSON serializer for NATS payloads.
func WithSerializer[T any](serializer Serializer[T]) options.Option[NATSBus[T]] {
	return func(bus *NATSBus[T]) {
		bus.serializer = serializer
	}
}

// Publish sends a message to the given subject with no headers.
func (b *NATSBus[T]) Publish(ctx context.Context, subject string, data T) error {
	return b.PublishWithHeaders(ctx, subject, data, Headers{})
}

// PublishWithHeaders encodes data with the bus serializer, stamps an
// RFC3339 "timestamp" header alongside the caller's headers, and sends
// via JetStream when configured (core NATS otherwise). A ctx already
// cancelled on entry short-circuits before encoding.
func (b *NATSBus[T]) PublishWithHeaders(ctx context.Context, subject string, data T, headers Headers) error {
	// Cheap pre-flight: if the caller's ctx is already cancelled,
	// don't bother encoding or sending. Earlier shape accepted ctx
	// purely for signature parity and let cancelled requests through.
	if err := ctx.Err(); err != nil {
		return err
	}

	payload, err := b.serializer.Encode(data)
	if err != nil {
		return fmt.Errorf("encode message: %w", err)
	}

	msg := &nats.Msg{
		Subject: subject,
		Data:    payload,
		Header:  make(nats.Header),
	}

	// Copy headers
	for k, v := range headers {
		msg.Header.Set(k, v)
	}

	// Set timestamp
	msg.Header.Set("timestamp", time.Now().Format(time.RFC3339))

	if b.js != nil {
		_, err = b.js.PublishMsg(msg)
	} else {
		err = b.conn.PublishMsg(msg)
	}

	if err != nil {
		return fmt.Errorf("publish message: %w", err)
	}

	return nil
}

// Subscribe registers handler for subject (JetStream when configured,
// core NATS otherwise) and ties the subscription's lifetime to ctx: a
// context.AfterFunc auto-unsubscribes on cancellation, and the handler
// sees that same ctx so it learns when its subscription is torn down.
func (b *NATSBus[T]) Subscribe(ctx context.Context, subject string, handler Handler[T]) (Subscription, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	wrappedHandler := b.wrapHandler(ctx, subject, handler)

	var sub *nats.Subscription
	var err error

	if b.js != nil {
		sub, err = b.js.Subscribe(subject, wrappedHandler)
	} else {
		sub, err = b.conn.Subscribe(subject, wrappedHandler)
	}

	if err != nil {
		return nil, fmt.Errorf("subscribe to %s: %w", subject, err)
	}

	// Auto-unsubscribe when ctx cancels — Subscribe used to take ctx
	// purely for signature parity, leaving the subscription alive
	// past the caller's intended lifetime. context.AfterFunc fires
	// the cleanup once and is a no-op if Unsubscribe was called
	// explicitly first.
	context.AfterFunc(ctx, func() { _ = sub.Unsubscribe() })

	return &natsSubscription{sub: sub}, nil
}

// QueueSubscribe is Subscribe with a NATS queue group: members of the
// same queue share subject traffic so each message lands on exactly one
// of them. Lifetime follows ctx via the same auto-unsubscribe hook.
func (b *NATSBus[T]) QueueSubscribe(
	ctx context.Context,
	subject string,
	queue string,
	handler Handler[T],
) (Subscription, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	wrappedHandler := b.wrapHandler(ctx, subject, handler)

	var sub *nats.Subscription
	var err error

	if b.js != nil {
		sub, err = b.js.QueueSubscribe(subject, queue, wrappedHandler)
	} else {
		sub, err = b.conn.QueueSubscribe(subject, queue, wrappedHandler)
	}

	if err != nil {
		return nil, fmt.Errorf("queue subscribe to %s: %w", subject, err)
	}

	context.AfterFunc(ctx, func() { _ = sub.Unsubscribe() })

	return &natsSubscription{sub: sub}, nil
}

// Request publishes data on subject with a reply subject and blocks
// until either the reply arrives, the timeout elapses, or ctx is
// cancelled — whichever fires first.
//
// Earlier shape ignored the timeout argument entirely (only ctx was
// honoured). The caller-facing contract on [Bus.Request] documents
// the timeout as authoritative, so we now compose: if timeout > 0
// AND ctx has no nearer deadline, we derive a ctx-with-timeout. If
// ctx is already nearer, ctx wins. Both interpretations satisfy
// "Request returns within at most min(ctx-deadline, timeout)".
func (b *NATSBus[T]) Request(ctx context.Context, subject string, data T, timeout time.Duration) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}

	payload, err := b.serializer.Encode(data)
	if err != nil {
		return zero, fmt.Errorf("encode request: %w", err)
	}

	// Derive a timeout-bound ctx when the caller asked for one AND
	// no nearer ctx deadline already exists.
	if timeout > 0 {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > timeout {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
	}

	reply, err := b.conn.RequestWithContext(ctx, subject, payload)
	if err != nil {
		return zero, fmt.Errorf("request to %s: %w", subject, err)
	}

	result, err := b.serializer.Decode(reply.Data)
	if err != nil {
		return zero, fmt.Errorf("decode reply: %w", err)
	}

	return result, nil
}

// Close closes the underlying NATS connection, which terminates every
// subscription with it. Always returns nil — nats.Conn.Close does not
// report errors.
func (b *NATSBus[T]) Close() error {
	b.conn.Close()
	return nil
}

// wrapHandler builds the nats.MsgHandler that bridges raw NATS
// messages onto the typed bus Handler. The subscription's ctx is
// captured so handlers see the same lifetime that drives auto-
// unsubscribe — earlier shape used context.Background() and a
// ctx-aware handler had no way to know when its subscription was
// supposed to be torn down.
//
// Decode and handler errors used to be silently dropped (the
// // log error comments in the original were placeholders). They
// now go to slog at Warn — visible without breaking message
// processing on the next delivery.
func (b *NATSBus[T]) wrapHandler(subCtx context.Context, subject string, handler Handler[T]) nats.MsgHandler {
	return func(msg *nats.Msg) {
		// If the subscription's ctx has been cancelled, skip
		// delivery. context.AfterFunc will unsubscribe shortly; this
		// gates the small window between cancellation and the
		// unsubscribe taking effect.
		if subCtx.Err() != nil {
			return
		}

		data, err := b.serializer.Decode(msg.Data)
		if err != nil {
			slog.WarnContext(subCtx, "messagebus/nats: decode failed — message dropped",
				"subject", msg.Subject,
				"err", err)
			return
		}

		headers := make(Headers)
		for k, v := range msg.Header {
			if len(v) > 0 {
				headers[k] = v[0]
			}
		}

		var timestamp time.Time
		if ts := headers.Get("timestamp"); ts != "" {
			timestamp, _ = time.Parse(time.RFC3339, ts)
		}
		if timestamp.IsZero() {
			timestamp = time.Now()
		}

		busMsg := Message[T]{
			Subject:   msg.Subject,
			Data:      data,
			Headers:   headers,
			Timestamp: timestamp,
		}

		if err := handler(subCtx, busMsg); err != nil {
			slog.WarnContext(subCtx, "messagebus/nats: handler returned error",
				"subject", msg.Subject,
				"err", err)
		}
	}
}

// natsSubscription wraps NATS subscription.
type natsSubscription struct {
	sub *nats.Subscription
}

func (s *natsSubscription) Unsubscribe() error {
	return s.sub.Unsubscribe()
}

func (s *natsSubscription) IsValid() bool {
	return s.sub.IsValid()
}
