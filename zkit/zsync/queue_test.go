package zsync_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/zsync"
)

func TestQueue_Push(t *testing.T) {
	tests := []struct {
		name   string
		items  []string
		closed bool
		want   int
		error  error
	}{
		{
			name:  "push single item",
			items: []string{"test"},
			want:  1,
		},
		{
			name:  "push multiple items",
			items: []string{"a", "b", "c"},
			want:  3,
		},
		{
			name:  "push empty string",
			items: []string{""},
			want:  1,
		},
		{
			name:   "push to closed queue",
			items:  []string{"test"},
			closed: true,
			want:   0,
			error:  zsync.ErrQueueClosed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := zsync.NewQueue[string]()

			if tt.closed {
				q.Close()
			}

			var lastErr error
			for _, item := range tt.items {
				if err := q.Push(item); err != nil {
					lastErr = err
				}
			}

			if !errors.Is(lastErr, tt.error) {
				t.Errorf("Push() error = %v, want %v", lastErr, tt.error)
			}

			if got := q.Len(); got != tt.want {
				t.Errorf("Len() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestQueue_TryPop(t *testing.T) {
	tests := []struct {
		name  string
		setup []string
		want  string
		err   error
	}{
		{
			name:  "pop from single item",
			setup: []string{"test"},
			want:  "test",
		},
		{
			name:  "pop first item from multiple",
			setup: []string{"first", "second", "third"},
			want:  "first",
		},
		{
			name:  "pop from empty queue",
			setup: []string{},
			want:  "",
			err:   zsync.ErrQueueEmpty,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := zsync.NewQueue[string]()

			for _, item := range tt.setup {
				q.Push(item)
			}

			got, err := q.TryPop()
			if !errors.Is(err, tt.err) {
				t.Errorf("TryPop() error = %v, want %v", err, tt.err)
				return
			}

			if got != tt.want {
				t.Errorf("TryPop() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestQueue_Pop(t *testing.T) {
	t.Run("pop from queue with items", func(t *testing.T) {
		q := zsync.NewQueue[string]()
		q.Push("first")
		q.Push("second")

		got, err := q.Pop()
		if err != nil {
			t.Errorf("Pop() error = %v, want nil", err)
			return
		}

		if got != "first" {
			t.Errorf("Pop() = %v, want first", got)
		}

		if q.Len() != 1 {
			t.Errorf("Len() after pop = %v, want 1", q.Len())
		}
	})

	t.Run("pop from closed empty queue", func(t *testing.T) {
		q := zsync.NewQueue[string]()
		q.Close()

		got, err := q.Pop()
		if !errors.Is(err, zsync.ErrQueueClosed) {
			t.Errorf("Pop() error = %v, want %v", err, zsync.ErrQueueClosed)
		}

		if got != "" {
			t.Errorf("Pop() = %v, want empty string", got)
		}
	})
}

func TestQueue_PopContext(t *testing.T) {
	t.Run("pop with context timeout", func(t *testing.T) {
		q := zsync.NewQueue[string]()
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
		defer cancel()

		got, err := q.PopContext(ctx)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("PopContext() error = %v, want %v", err, context.DeadlineExceeded)
		}

		if got != "" {
			t.Errorf("PopContext() = %v, want empty string", got)
		}
	})

	t.Run("pop with context success", func(t *testing.T) {
		q := zsync.NewQueue[string]()
		q.Push("test")

		ctx := t.Context()
		got, err := q.PopContext(ctx)
		if err != nil {
			t.Errorf("PopContext() error = %v, want nil", err)
			return
		}

		if got != "test" {
			t.Errorf("PopContext() = %v, want test", got)
		}
	})
}

func TestQueue_Len(t *testing.T) {
	tests := []struct {
		name  string
		setup []string
		want  int
	}{
		{
			name:  "empty queue",
			setup: []string{},
			want:  0,
		},
		{
			name:  "single item",
			setup: []string{"test"},
			want:  1,
		},
		{
			name:  "multiple items",
			setup: []string{"a", "b", "c"},
			want:  3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := zsync.NewQueue[string]()

			for _, item := range tt.setup {
				q.Push(item)
			}

			if got := q.Len(); got != tt.want {
				t.Errorf("Len() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestQueue_Close(t *testing.T) {
	q := zsync.NewQueue[string]()
	q.Push("test")

	if q.IsClosed() {
		t.Errorf("IsClosed() = true, want false before close")
	}

	q.Close()

	if !q.IsClosed() {
		t.Errorf("IsClosed() = false, want true after close")
	}

	// verify closed queue rejects push
	err := q.Push("another")
	if !errors.Is(err, zsync.ErrQueueClosed) {
		t.Errorf("Push() after close error = %v, want %v", err, zsync.ErrQueueClosed)
	}
}

func TestQueue_FIFO(t *testing.T) {
	q := zsync.NewQueue[int]()

	// fifo order
	for i := 1; i <= 5; i++ {
		q.Push(i)
	}

	// verify fifo ordering
	for i := 1; i <= 5; i++ {
		got, err := q.TryPop()
		if err != nil {
			t.Errorf("TryPop() error = %v, want nil", err)
			continue
		}

		if got != i {
			t.Errorf("TryPop() = %v, want %v", got, i)
		}
	}
}

// concurrent producer/consumer test.
func TestQueue_Concurrent(t *testing.T) {
	q := zsync.NewQueue[int]()

	const (
		numProducers     = 5
		numConsumers     = 3
		itemsPerProducer = 100
	)

	// consumer goroutines
	consumedItems := make([]int, 0, numProducers*itemsPerProducer)
	var (
		wg         sync.WaitGroup
		producerWg sync.WaitGroup
		consumeMu  sync.Mutex
	)

	for range numConsumers {
		wg.Go(func() {
			for {
				item, err := q.Pop()
				if errors.Is(err, zsync.ErrQueueClosed) {
					return // expected when queue closes
				}

				consumeMu.Lock()
				consumedItems = append(consumedItems, item)
				consumeMu.Unlock()
			}
		})
	}

	// producer goroutines
	for i := range numProducers {
		producerWg.Add(1)
		go func(producerID int) {
			defer producerWg.Done()
			for j := range itemsPerProducer {
				item := producerID*itemsPerProducer + j
				q.Push(item)
			}
		}(i)
	}

	// await producers
	producerWg.Wait()

	// signal consumers via close
	q.Close()

	// await consumers
	wg.Wait()

	// ensure all items consumed
	expectedItems := numProducers * itemsPerProducer
	if len(consumedItems) != expectedItems {
		t.Errorf("consumed %v items, want %v", len(consumedItems), expectedItems)
	}
}

func TestQueue_BlockingPop(t *testing.T) {
	q := zsync.NewQueue[string]()

	var wg sync.WaitGroup
	var result string
	var err error

	// blocking consumer
	wg.Go(func() {
		result, err = q.Pop()
	})

	// let consumer block first
	time.Sleep(10 * time.Millisecond)

	// unblock via push
	q.Push("unblock")

	// await unblock
	wg.Wait()

	if err != nil {
		t.Errorf("Pop() error = %v, want nil", err)
	}

	if result != "unblock" {
		t.Errorf("Pop() = %v, want unblock", result)
	}
}

func TestQueue_CloseUnblocksConsumers(t *testing.T) {
	q := zsync.NewQueue[string]()

	var wg sync.WaitGroup
	var err error

	// blocking consumer
	wg.Go(func() {
		_, err = q.Pop()
	})

	// let consumer block first
	time.Sleep(10 * time.Millisecond)

	// unblock via close
	q.Close()

	// await unblock
	wg.Wait()

	if !errors.Is(err, zsync.ErrQueueClosed) {
		t.Errorf("Pop() after close error = %v, want %v", err, zsync.ErrQueueClosed)
	}
}
