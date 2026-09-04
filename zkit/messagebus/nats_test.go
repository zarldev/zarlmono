package messagebus_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/zarldev/zarlmono/zkit/messagebus"
)

func TestJSONSerializer(t *testing.T) {
	t.Run("round trip", func(t *testing.T) {
		serializer := messagebus.JSONSerializer[TestEvent]{}
		want := TestEvent{ID: 42, Message: "round trip"}
		payload, err := serializer.Encode(want)
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}
		got, err := serializer.Decode(payload)
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("round trip = %#v, want %#v", got, want)
		}
	})

	t.Run("malformed payload", func(t *testing.T) {
		serializer := messagebus.JSONSerializer[TestEvent]{}
		got, err := serializer.Decode([]byte(`{"id":`))
		if err == nil {
			t.Fatal("Decode malformed payload succeeded")
		}
		if got != (TestEvent{}) {
			t.Fatalf("Decode malformed payload = %#v, want zero value", got)
		}
	})
}

func TestNATSBusContract(t *testing.T) {
	url := requiredIntegrationURL(t, "NATS_URL")
	prefix := fmt.Sprintf("zarlcode.contract.%d", time.Now().UnixNano())
	bus, err := messagebus.NewNATSBus[TestEvent](
		messagebus.WithNATSURL[TestEvent](url),
		messagebus.WithNATSTimeout[TestEvent](5*time.Second),
		messagebus.WithMaxReconnect[TestEvent](0),
	)
	if err != nil {
		t.Fatalf("NewNATSBus with configured NATS_URL: %v", err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			if err := bus.Close(); err != nil {
				t.Errorf("Close: %v", err)
			}
		}
	})

	t.Run("publish subscribe and headers", func(t *testing.T) {
		received := make(chan messagebus.Message[TestEvent], 2)
		sub, err := bus.Subscribe(t.Context(), prefix+".events", func(_ context.Context, msg messagebus.Message[TestEvent]) error {
			received <- msg
			return nil
		})
		if err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		defer func() { _ = sub.Unsubscribe() }()

		plain := TestEvent{ID: 1, Message: "plain"}
		if err := bus.Publish(t.Context(), prefix+".events", plain); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		assertMessage(t, receiveMessage(t, received), prefix+".events", plain, "", "")

		withHeaders := TestEvent{ID: 2, Message: "headers"}
		if err := bus.PublishWithHeaders(t.Context(), prefix+".events", withHeaders, messagebus.Headers{"trace-id": "trace-123"}); err != nil {
			t.Fatalf("PublishWithHeaders: %v", err)
		}
		assertMessage(t, receiveMessage(t, received), prefix+".events", withHeaders, "trace-id", "trace-123")
	})

	t.Run("queue group", func(t *testing.T) {
		deliveries := make(chan int, 8)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		first, err := bus.QueueSubscribe(ctx, prefix+".queue", "workers", func(context.Context, messagebus.Message[TestEvent]) error {
			deliveries <- 1
			return nil
		})
		if err != nil {
			t.Fatalf("first QueueSubscribe: %v", err)
		}
		defer func() { _ = first.Unsubscribe() }()
		second, err := bus.QueueSubscribe(ctx, prefix+".queue", "workers", func(context.Context, messagebus.Message[TestEvent]) error {
			deliveries <- 2
			return nil
		})
		if err != nil {
			t.Fatalf("second QueueSubscribe: %v", err)
		}
		defer func() { _ = second.Unsubscribe() }()

		for i := range 8 {
			if err := bus.Publish(t.Context(), prefix+".queue", TestEvent{ID: i}); err != nil {
				t.Fatalf("Publish queue item %d: %v", i, err)
			}
		}
		counts := map[int]int{}
		for range 8 {
			counts[receiveWithin(t, deliveries)]++
		}
		if counts[1] == 0 || counts[2] == 0 {
			t.Fatalf("queue deliveries = %v, want both subscribers to receive work", counts)
		}
	})

	t.Run("request reply", func(t *testing.T) {
		sub, err := bus.Subscribe(t.Context(), prefix+".request", func(ctx context.Context, msg messagebus.Message[TestEvent]) error {
			replyTo := msg.Headers.Get("reply-to")
			if replyTo == "" {
				return errors.New("request did not expose reply subject")
			}
			return bus.Publish(ctx, replyTo, TestEvent{ID: msg.Data.ID * 2, Message: "reply: " + msg.Data.Message})
		})
		if err != nil {
			t.Fatalf("Subscribe responder: %v", err)
		}
		defer func() { _ = sub.Unsubscribe() }()

		got, err := bus.Request(t.Context(), prefix+".request", TestEvent{ID: 5, Message: "request"}, 2*time.Second)
		if err != nil {
			t.Fatalf("Request: %v", err)
		}
		want := TestEvent{ID: 10, Message: "reply: request"}
		if got != want {
			t.Fatalf("Request reply = %#v, want %#v", got, want)
		}

		started := time.Now()
		if _, err := bus.Request(t.Context(), prefix+".no-responder", TestEvent{}, 100*time.Millisecond); err == nil {
			t.Fatal("Request without responder succeeded")
		}
		if elapsed := time.Since(started); elapsed > time.Second {
			t.Fatalf("Request timeout took %v, want less than 1s", elapsed)
		}
	})

	t.Run("cancellation", func(t *testing.T) {
		canceled, cancel := context.WithCancel(t.Context())
		cancel()
		if err := bus.Publish(canceled, prefix+".canceled", TestEvent{}); !errors.Is(err, context.Canceled) {
			t.Fatalf("Publish canceled error = %v, want context.Canceled", err)
		}
		if _, err := bus.Subscribe(canceled, prefix+".canceled", func(context.Context, messagebus.Message[TestEvent]) error { return nil }); !errors.Is(err, context.Canceled) {
			t.Fatalf("Subscribe canceled error = %v, want context.Canceled", err)
		}
		if _, err := bus.Request(canceled, prefix+".canceled", TestEvent{}, time.Second); !errors.Is(err, context.Canceled) {
			t.Fatalf("Request canceled error = %v, want context.Canceled", err)
		}

		subCtx, subCancel := context.WithCancel(t.Context())
		sub, err := bus.Subscribe(subCtx, prefix+".cancel-sub", func(context.Context, messagebus.Message[TestEvent]) error { return nil })
		if err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		if !sub.IsValid() {
			t.Fatal("new subscription is invalid")
		}
		subCancel()
		waitFor(t, func() bool { return !sub.IsValid() }, "subscription to become invalid after context cancellation")
	})

	t.Run("close", func(t *testing.T) {
		sub, err := bus.Subscribe(t.Context(), prefix+".close", func(context.Context, messagebus.Message[TestEvent]) error { return nil })
		if err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		if err := bus.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		closed = true
		waitFor(t, func() bool { return !sub.IsValid() }, "subscription to become invalid after Close")
		if err := bus.Publish(t.Context(), prefix+".close", TestEvent{}); err == nil {
			t.Error("Publish after Close succeeded")
		}
		if _, err := bus.Subscribe(t.Context(), prefix+".close", func(context.Context, messagebus.Message[TestEvent]) error { return nil }); err == nil {
			t.Error("Subscribe after Close succeeded")
		}
	})
}

func TestNATSBusJetStream(t *testing.T) {
	url := requiredIntegrationURL(t, "NATS_URL")
	admin, err := nats.Connect(url, nats.Timeout(5*time.Second), nats.MaxReconnects(0))
	if err != nil {
		t.Fatalf("connect to configured NATS_URL for JetStream setup: %v", err)
	}
	t.Cleanup(admin.Close)
	js, err := admin.JetStream()
	if err != nil {
		t.Fatalf("create JetStream setup context: %v", err)
	}
	if _, err := js.AccountInfo(); err != nil {
		if errors.Is(err, nats.ErrNoResponders) {
			t.Skipf("NATS_URL is reachable but JetStream is not enabled: %v", err)
		}
		t.Fatalf("query JetStream account: %v", err)
	}

	suffix := time.Now().UnixNano()
	stream := fmt.Sprintf("ZARLCODE_CONTRACT_%d", suffix)
	subject := fmt.Sprintf("zarlcode.jetstream.contract.%d", suffix)
	if _, err := js.AddStream(&nats.StreamConfig{Name: stream, Subjects: []string{subject}, Storage: nats.MemoryStorage}); err != nil {
		t.Fatalf("AddStream: %v", err)
	}
	t.Cleanup(func() {
		if err := js.DeleteStream(stream); err != nil && !errors.Is(err, nats.ErrStreamNotFound) {
			t.Errorf("DeleteStream: %v", err)
		}
	})

	bus, err := messagebus.NewNATSBus[TestEvent](
		messagebus.WithNATSURL[TestEvent](url),
		messagebus.WithNATSTimeout[TestEvent](5*time.Second),
		messagebus.WithMaxReconnect[TestEvent](0),
		messagebus.WithJetStream[TestEvent](),
	)
	if err != nil {
		t.Fatalf("NewNATSBus with JetStream: %v", err)
	}
	defer func() {
		if err := bus.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	received := make(chan messagebus.Message[TestEvent], 1)
	sub, err := bus.Subscribe(t.Context(), subject, func(_ context.Context, msg messagebus.Message[TestEvent]) error {
		received <- msg
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer func() { _ = sub.Unsubscribe() }()
	want := TestEvent{ID: 7, Message: "jetstream"}
	if err := bus.PublishWithHeaders(t.Context(), subject, want, messagebus.Headers{"mode": "jetstream"}); err != nil {
		t.Fatalf("PublishWithHeaders: %v", err)
	}
	assertMessage(t, receiveMessage(t, received), subject, want, "mode", "jetstream")
}

func requiredIntegrationURL(t *testing.T, name string) string {
	t.Helper()
	value, ok := os.LookupEnv(name)
	if !ok {
		t.Skipf("%s is not set; skipping integration contract", name)
	}
	if value == "" {
		t.Fatalf("%s is set but empty", name)
	}
	return value
}

func receiveMessage(t *testing.T, messages <-chan messagebus.Message[TestEvent]) messagebus.Message[TestEvent] {
	t.Helper()
	return receiveWithin(t, messages)
}

func receiveWithin[T any](t *testing.T, values <-chan T) T {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(3 * time.Second):
		var zero T
		t.Fatal("timed out waiting for message")
		return zero
	}
}

func assertMessage(t *testing.T, got messagebus.Message[TestEvent], wantSubject string, wantData TestEvent, header, wantHeader string) {
	t.Helper()
	if got.Subject != wantSubject {
		t.Errorf("subject = %q, want %q", got.Subject, wantSubject)
	}
	if got.Data != wantData {
		t.Errorf("data = %#v, want %#v", got.Data, wantData)
	}
	if header != "" && got.Headers.Get(header) != wantHeader {
		t.Errorf("header %q = %q, want %q", header, got.Headers.Get(header), wantHeader)
	}
	if got.Timestamp.IsZero() {
		t.Error("timestamp is zero")
	}
}

func waitFor(t *testing.T, condition func() bool, description string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", description)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
