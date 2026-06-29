package messagebus_test

import (
	"context"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/messagebus"
)

func BenchmarkMemoryBus(b *testing.B) {
	b.Run("Publish", func(b *testing.B) {
		bus := messagebus.NewMemoryBus[TestEvent]()
		defer bus.Close()

		ctx := b.Context()
		event := TestEvent{ID: 1, Message: "benchmark"}

		b.ResetTimer()
		for b.Loop() {
			bus.Publish(ctx, "bench.publish", event)
		}
	})

	b.Run("PublishWithHeaders", func(b *testing.B) {
		bus := messagebus.NewMemoryBus[TestEvent]()
		defer bus.Close()

		ctx := b.Context()
		event := TestEvent{ID: 1, Message: "benchmark"}
		headers := messagebus.Headers{
			"user-id": "123",
			"source":  "benchmark",
		}

		b.ResetTimer()
		for b.Loop() {
			bus.PublishWithHeaders(ctx, "bench.headers", event, headers)
		}
	})

	b.Run("Subscribe", func(b *testing.B) {
		bus := messagebus.NewMemoryBus[TestEvent]()
		defer bus.Close()

		ctx := b.Context()
		handler := func(ctx context.Context, msg messagebus.Message[TestEvent]) error {
			return nil
		}

		b.ResetTimer()
		for b.Loop() {
			sub, _ := bus.Subscribe(ctx, "bench.subscribe", handler)
			sub.Unsubscribe()
		}
	})

	b.Run("PubSubThroughput", func(b *testing.B) {
		bus := messagebus.NewMemoryBus(
			messagebus.WithBufferSize[TestEvent](10000),
		)
		defer bus.Close()

		ctx := b.Context()
		received := 0
		handler := func(ctx context.Context, msg messagebus.Message[TestEvent]) error {
			received++
			return nil
		}

		sub, _ := bus.Subscribe(ctx, "bench.throughput", handler)
		defer sub.Unsubscribe()

		event := TestEvent{ID: 1, Message: "throughput"}

		b.ResetTimer()
		for b.Loop() {
			bus.Publish(ctx, "bench.throughput", event)
		}

		// Wait for messages to be processed
		time.Sleep(10 * time.Millisecond)
	})

	b.Run("RequestReply", func(b *testing.B) {
		bus := messagebus.NewMemoryBus[TestEvent]()
		defer bus.Close()

		ctx := b.Context()

		// Set up responder
		responder := func(ctx context.Context, msg messagebus.Message[TestEvent]) error {
			reply := TestEvent{ID: msg.Data.ID, Message: "reply"}
			replySubject := msg.Headers.Get("reply-to")
			if replySubject != "" {
				return bus.Publish(ctx, replySubject, reply)
			}
			return nil
		}

		sub, _ := bus.Subscribe(ctx, "bench.request", responder)
		defer sub.Unsubscribe()

		request := TestEvent{ID: 1, Message: "request"}

		b.ResetTimer()
		for b.Loop() {
			bus.Request(ctx, "bench.request", request, time.Second)
		}
	})
}

func BenchmarkMemoryBusSynchronous(b *testing.B) {
	bus := messagebus.NewMemoryBus(
		messagebus.WithSynchronous[TestEvent](),
	)
	defer bus.Close()

	b.Run("PublishSynchronous", func(b *testing.B) {
		ctx := b.Context()
		event := TestEvent{ID: 1, Message: "sync benchmark"}

		// Add subscriber to make publish do work
		handler := func(ctx context.Context, msg messagebus.Message[TestEvent]) error {
			return nil
		}
		sub, _ := bus.Subscribe(ctx, "bench.sync", handler)
		defer sub.Unsubscribe()

		b.ResetTimer()
		for b.Loop() {
			bus.Publish(ctx, "bench.sync", event)
		}
	})
}

func BenchmarkHeaders(b *testing.B) {
	headers := messagebus.Headers{
		"key1": "value1",
		"key2": "value2",
		"key3": "value3",
	}

	b.Run("Get", func(b *testing.B) {
		for b.Loop() {
			_ = headers.Get("key2")
		}
	})

	b.Run("Set", func(b *testing.B) {
		for b.Loop() {
			headers.Set("benchmark", "value")
		}
	})

	b.Run("Has", func(b *testing.B) {
		for b.Loop() {
			_ = headers.Has("key2")
		}
	})

	b.Run("Clone", func(b *testing.B) {
		for b.Loop() {
			_ = headers.Clone()
		}
	})
}

// Benchmark pattern matching through publish/subscribe.
func BenchmarkPatternMatching(b *testing.B) {
	bus := messagebus.NewMemoryBus[TestEvent]()
	defer bus.Close()

	ctx := b.Context()
	event := TestEvent{ID: 1, Message: "pattern"}

	// Set up wildcard subscribers
	handler := func(ctx context.Context, msg messagebus.Message[TestEvent]) error {
		return nil
	}

	sub1, _ := bus.Subscribe(ctx, "user.*", handler)
	defer sub1.Unsubscribe()

	sub2, _ := bus.Subscribe(ctx, "order.>", handler)
	defer sub2.Unsubscribe()

	subjects := []string{
		"user.login",
		"user.logout",
		"order.created",
		"order.updated",
		"system.alert",
	}

	b.ResetTimer()
	for b.Loop() {
		for _, subject := range subjects {
			bus.Publish(ctx, subject, event)
		}
	}
}

// Benchmark concurrent access.
func BenchmarkConcurrentAccess(b *testing.B) {
	bus := messagebus.NewMemoryBus(
		messagebus.WithBufferSize[TestEvent](1000),
	)
	defer bus.Close()

	ctx := b.Context()
	event := TestEvent{ID: 1, Message: "concurrent"}

	// Set up subscriber
	handler := func(ctx context.Context, msg messagebus.Message[TestEvent]) error {
		return nil
	}
	sub, _ := bus.Subscribe(ctx, "bench.concurrent", handler)
	defer sub.Unsubscribe()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			bus.Publish(ctx, "bench.concurrent", event)
		}
	})
}
