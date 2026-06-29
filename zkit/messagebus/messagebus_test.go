package messagebus_test

import (
	"context"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/messagebus"
)

// TestEvent represents a test message.
type TestEvent struct {
	ID      int    `json:"id"`
	Message string `json:"message"`
}

// Test implementations.
func TestBusImplementations(t *testing.T) {
	tests := []struct {
		name    string
		busFunc func() messagebus.Bus[TestEvent]
	}{
		{
			name: "memory",
			busFunc: func() messagebus.Bus[TestEvent] {
				return messagebus.NewMemoryBus[TestEvent]()
			},
		},
		{
			name: "memory_synchronous",
			busFunc: func() messagebus.Bus[TestEvent] {
				return messagebus.NewMemoryBus(
					messagebus.WithSynchronous[TestEvent](),
				)
			},
		},
		{
			name: "memory_large_buffer",
			busFunc: func() messagebus.Bus[TestEvent] {
				return messagebus.NewMemoryBus(
					messagebus.WithBufferSize[TestEvent](1000),
				)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bus := tt.busFunc()
			defer bus.Close()

			testBasicPubSub(t, bus)
			testHeaders(t, bus)
			testMultipleSubscribers(t, bus)
			testQueueSubscriptions(t, bus)
			testRequestReply(t, bus)
		})
	}
}

func testBasicPubSub(t *testing.T, bus messagebus.Bus[TestEvent]) {
	ctx := t.Context()
	received := make(chan TestEvent, 1)

	// Subscribe
	handler := func(ctx context.Context, msg messagebus.Message[TestEvent]) error {
		received <- msg.Data
		return nil
	}

	sub, err := bus.Subscribe(ctx, "test.basic", handler)
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}
	defer sub.Unsubscribe()

	// Publish
	event := TestEvent{ID: 1, Message: "hello"}
	if err := bus.Publish(ctx, "test.basic", event); err != nil {
		t.Fatalf("Publish failed: %v", err)
	}

	// Verify
	select {
	case got := <-received:
		if got.ID != event.ID || got.Message != event.Message {
			t.Errorf("Expected %+v, got %+v", event, got)
		}
	case <-time.After(time.Second):
		t.Fatal("Message not received")
	}
}

func testHeaders(t *testing.T, bus messagebus.Bus[TestEvent]) {
	ctx := t.Context()
	received := make(chan messagebus.Message[TestEvent], 1)

	// Subscribe
	handler := func(ctx context.Context, msg messagebus.Message[TestEvent]) error {
		received <- msg
		return nil
	}

	sub, err := bus.Subscribe(ctx, "test.headers", handler)
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}
	defer sub.Unsubscribe()

	// Publish with headers
	event := TestEvent{ID: 2, Message: "with headers"}
	headers := messagebus.Headers{
		"user-id": "123",
		"source":  "test",
	}

	if err := bus.PublishWithHeaders(ctx, "test.headers", event, headers); err != nil {
		t.Fatalf("Publish failed: %v", err)
	}

	// Verify
	select {
	case got := <-received:
		if got.Data.ID != event.ID {
			t.Errorf("Expected ID %d, got %d", event.ID, got.Data.ID)
		}
		if got.Headers.Get("user-id") != "123" {
			t.Errorf("Expected user-id header to be '123', got '%s'", got.Headers.Get("user-id"))
		}
		if got.Headers.Get("source") != "test" {
			t.Errorf("Expected source header to be 'test', got '%s'", got.Headers.Get("source"))
		}
		if got.Subject != "test.headers" {
			t.Errorf("Expected subject 'test.headers', got '%s'", got.Subject)
		}
		if got.Timestamp.IsZero() {
			t.Error("Expected timestamp to be set")
		}
	case <-time.After(time.Second):
		t.Fatal("Message not received")
	}
}

func testMultipleSubscribers(t *testing.T, bus messagebus.Bus[TestEvent]) {
	ctx := t.Context()
	received1 := make(chan TestEvent, 1)
	received2 := make(chan TestEvent, 1)

	// Subscribe with two handlers
	handler1 := func(ctx context.Context, msg messagebus.Message[TestEvent]) error {
		received1 <- msg.Data
		return nil
	}
	handler2 := func(ctx context.Context, msg messagebus.Message[TestEvent]) error {
		received2 <- msg.Data
		return nil
	}

	sub1, err := bus.Subscribe(ctx, "test.multi", handler1)
	if err != nil {
		t.Fatalf("Subscribe 1 failed: %v", err)
	}
	defer sub1.Unsubscribe()

	sub2, err := bus.Subscribe(ctx, "test.multi", handler2)
	if err != nil {
		t.Fatalf("Subscribe 2 failed: %v", err)
	}
	defer sub2.Unsubscribe()

	// Publish
	event := TestEvent{ID: 3, Message: "multi"}
	if err := bus.Publish(ctx, "test.multi", event); err != nil {
		t.Fatalf("Publish failed: %v", err)
	}

	// Both should receive
	timeout := time.After(time.Second)

	select {
	case <-received1:
	case <-timeout:
		t.Fatal("Handler 1 did not receive message")
	}

	select {
	case <-received2:
	case <-timeout:
		t.Fatal("Handler 2 did not receive message")
	}
}

func testQueueSubscriptions(t *testing.T, bus messagebus.Bus[TestEvent]) {
	ctx := t.Context()
	received1 := make(chan TestEvent, 10)
	received2 := make(chan TestEvent, 10)

	// Queue subscribers (round-robin)
	handler1 := func(ctx context.Context, msg messagebus.Message[TestEvent]) error {
		received1 <- msg.Data
		return nil
	}
	handler2 := func(ctx context.Context, msg messagebus.Message[TestEvent]) error {
		received2 <- msg.Data
		return nil
	}

	sub1, err := bus.QueueSubscribe(ctx, "test.queue", "workers", handler1)
	if err != nil {
		t.Fatalf("QueueSubscribe 1 failed: %v", err)
	}
	defer sub1.Unsubscribe()

	sub2, err := bus.QueueSubscribe(ctx, "test.queue", "workers", handler2)
	if err != nil {
		t.Fatalf("QueueSubscribe 2 failed: %v", err)
	}
	defer sub2.Unsubscribe()

	// Publish multiple messages
	for i := range 4 {
		event := TestEvent{ID: i, Message: "queue"}
		if err := bus.Publish(ctx, "test.queue", event); err != nil {
			t.Fatalf("Publish %d failed: %v", i, err)
		}
	}

	// Wait and count messages received by each handler
	time.Sleep(100 * time.Millisecond)

	count1 := len(received1)
	count2 := len(received2)
	total := count1 + count2

	// For memory bus, all messages go to all subscribers in queue group
	// For NATS, it would be round-robin
	if total < 4 {
		t.Errorf("Expected at least 4 messages total, got %d (handler1: %d, handler2: %d)",
			total, count1, count2)
	}
}

func testRequestReply(t *testing.T, bus messagebus.Bus[TestEvent]) {
	ctx := t.Context()

	// Set up responder
	responder := func(ctx context.Context, msg messagebus.Message[TestEvent]) error {
		// Echo back with modified message
		reply := TestEvent{
			ID:      msg.Data.ID * 2,
			Message: "reply: " + msg.Data.Message,
		}

		replySubject := msg.Headers.Get("reply-to")
		if replySubject != "" {
			return bus.Publish(ctx, replySubject, reply)
		}
		return nil
	}

	sub, err := bus.Subscribe(ctx, "test.request", responder)
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}
	defer sub.Unsubscribe()

	// Make request
	request := TestEvent{ID: 5, Message: "request"}
	reply, err := bus.Request(ctx, "test.request", request, time.Second)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	// Verify reply
	if reply.ID != 10 { // 5 * 2
		t.Errorf("Expected reply ID 10, got %d", reply.ID)
	}
	if reply.Message != "reply: request" {
		t.Errorf("Expected reply message 'reply: request', got '%s'", reply.Message)
	}
}

func TestHeaders(t *testing.T) {
	headers := messagebus.Headers{
		"key1": "value1",
		"key2": "value2",
	}

	// Test Get
	if got := headers.Get("key1"); got != "value1" {
		t.Errorf("Expected 'value1', got '%s'", got)
	}
	if got := headers.Get("missing"); got != "" {
		t.Errorf("Expected empty string for missing key, got '%s'", got)
	}

	// Test Set
	headers.Set("key3", "value3")
	if got := headers.Get("key3"); got != "value3" {
		t.Errorf("Expected 'value3', got '%s'", got)
	}

	// Test Has
	if !headers.Has("key1") {
		t.Error("Expected Has('key1') to be true")
	}
	if headers.Has("missing") {
		t.Error("Expected Has('missing') to be false")
	}

	// Test Clone
	clone := headers.Clone()
	clone.Set("key1", "modified")

	if headers.Get("key1") == "modified" {
		t.Error("Clone modified original headers")
	}
	if clone.Get("key1") != "modified" {
		t.Error("Clone was not properly modified")
	}
}

func TestSubscriptionLifecycle(t *testing.T) {
	bus := messagebus.NewMemoryBus[TestEvent]()
	defer bus.Close()

	ctx := t.Context()
	received := make(chan TestEvent, 1)

	handler := func(ctx context.Context, msg messagebus.Message[TestEvent]) error {
		received <- msg.Data
		return nil
	}

	// Subscribe
	sub, err := bus.Subscribe(ctx, "test.lifecycle", handler)
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	if !sub.IsValid() {
		t.Error("Expected subscription to be valid")
	}

	// Unsubscribe
	if err := sub.Unsubscribe(); err != nil {
		t.Fatalf("Unsubscribe failed: %v", err)
	}

	if sub.IsValid() {
		t.Error("Expected subscription to be invalid after unsubscribe")
	}

	// Publish after unsubscribe should not be received
	event := TestEvent{ID: 99, Message: "should not receive"}
	if err := bus.Publish(ctx, "test.lifecycle", event); err != nil {
		t.Fatalf("Publish failed: %v", err)
	}

	// Should not receive anything
	select {
	case <-received:
		t.Error("Received message after unsubscribe")
	case <-time.After(100 * time.Millisecond):
		// Expected - no message received
	}
}

// TestSyncHandler_ReentrantSubscribeDoesNotDeadlock guards the
// MemoryBus sync-mode reentrancy fix. A synchronous handler that
// itself calls Subscribe / Unsubscribe / Close must not deadlock —
// earlier shape held the bus RLock through handler invocation so
// any reentrant writer-lock acquisition would hang forever.
func TestSyncHandler_ReentrantSubscribeDoesNotDeadlock(t *testing.T) {
	t.Parallel()
	bus := messagebus.NewMemoryBus[TestEvent](
		messagebus.WithSynchronous[TestEvent](),
	)
	defer bus.Close()
	ctx := t.Context()

	gotNested := make(chan struct{}, 1)
	primary := func(ctx context.Context, _ messagebus.Message[TestEvent]) error {
		// Reentrant Subscribe — needs the writer lock. Earlier shape
		// would deadlock here because Publish was holding the reader
		// lock until handler returned.
		_, err := bus.Subscribe(ctx, "test.nested", func(context.Context, messagebus.Message[TestEvent]) error {
			gotNested <- struct{}{}
			return nil
		})
		return err
	}
	if _, err := bus.Subscribe(ctx, "test.primary", primary); err != nil {
		t.Fatalf("Subscribe primary: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- bus.Publish(ctx, "test.primary", TestEvent{ID: 1})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Publish: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Publish deadlocked — reentrant Subscribe was unable to acquire writer lock")
	}

	// Verify the nested subscription is wired and reachable.
	if err := bus.Publish(ctx, "test.nested", TestEvent{ID: 2}); err != nil {
		t.Fatalf("Publish nested: %v", err)
	}
	select {
	case <-gotNested:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("nested handler never fired")
	}
}

func TestBusClose(t *testing.T) {
	bus := messagebus.NewMemoryBus[TestEvent]()

	ctx := t.Context()

	// Subscribe before close
	handler := func(ctx context.Context, msg messagebus.Message[TestEvent]) error {
		return nil
	}

	_, err := bus.Subscribe(ctx, "test.close", handler)
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	// Close bus
	if err := bus.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Operations after close should fail
	event := TestEvent{ID: 100, Message: "after close"}
	if err := bus.Publish(ctx, "test.close", event); err == nil {
		t.Error("Expected publish to fail after close")
	}

	if _, err := bus.Subscribe(ctx, "test.close2", handler); err == nil {
		t.Error("Expected subscribe to fail after close")
	}
}
