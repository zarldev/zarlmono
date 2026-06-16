package zsync_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/zarldev/zarlmono/zkit/zsync"
)

// original implementation for comparison.
type ZQueueOriginal[T any] struct {
	mu     sync.Mutex
	cond   *sync.Cond
	items  []T
	closed bool
}

func NewZQueueOriginal[T any]() *ZQueueOriginal[T] {
	q := &ZQueueOriginal[T]{
		items: make([]T, 0),
	}
	q.cond = sync.NewCond(&q.mu)
	return q
}

func (q *ZQueueOriginal[T]) Push(item T) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.closed {
		return zsync.ErrQueueClosed
	}

	q.items = append(q.items, item)
	q.cond.Signal()
	return nil
}

func (q *ZQueueOriginal[T]) PopOriginal() (T, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	for len(q.items) == 0 && !q.closed {
		q.cond.Wait()
	}

	if len(q.items) == 0 && q.closed {
		var zero T
		return zero, zsync.ErrQueueClosed
	}

	item := q.items[0]
	q.items = q.items[1:]
	return item, nil
}

func (q *ZQueueOriginal[T]) TryPop() (T, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.items) == 0 {
		var zero T
		return zero, zsync.ErrQueueEmpty
	}

	item := q.items[0]
	q.items = q.items[1:]
	return item, nil
}

func (q *ZQueueOriginal[T]) Close() error {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.closed = true
	q.cond.Broadcast()
	return nil
}

func (q *ZQueueOriginal[T]) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

// benchmark single threaded push/pop.
func BenchmarkQueue_PushPop_New(b *testing.B) {
	q := zsync.NewQueue[int]()

	b.ResetTimer()
	for i := range b.N {
		q.Push(i)
		q.TryPop()
	}
}

func BenchmarkQueue_PushPop_Original(b *testing.B) {
	q := NewZQueueOriginal[int]()

	b.ResetTimer()
	for i := range b.N {
		q.Push(i)
		q.TryPop()
	}
}

// benchmark with items already in queue.
func BenchmarkQueue_PopWithItems_New(b *testing.B) {
	q := zsync.NewQueue[int]()
	// warmup queue
	for i := range 1000 {
		q.Push(i)
	}

	b.ResetTimer()
	for i := range b.N {
		q.Push(i + 1000)
		q.TryPop()
	}
}

func BenchmarkQueue_PopWithItems_Original(b *testing.B) {
	q := NewZQueueOriginal[int]()
	// warmup queue
	for i := range 1000 {
		q.Push(i)
	}

	b.ResetTimer()
	for i := range b.N {
		q.Push(i + 1000)
		q.TryPop()
	}
}

// benchmark concurrent push/pop.
func BenchmarkQueue_Concurrent_New(b *testing.B) {
	q := zsync.NewQueue[int]()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%2 == 0 {
				q.Push(i)
			} else {
				q.TryPop()
			}
			i++
		}
	})
}

func BenchmarkQueue_Concurrent_Original(b *testing.B) {
	q := NewZQueueOriginal[int]()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%2 == 0 {
				q.Push(i)
			} else {
				q.TryPop()
			}
			i++
		}
	})
}

// benchmark blocking pop scenario.
func BenchmarkQueue_BlockingPop_New(b *testing.B) {
	q := zsync.NewQueue[int]()
	var wg sync.WaitGroup

	// producer goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range b.N {
			q.Push(i)
		}
		q.Close()
	}()

	// consumer goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			_, err := q.Pop()
			if errors.Is(err, zsync.ErrQueueClosed) {
				break
			}
		}
	}()

	wg.Wait()
}

func BenchmarkQueue_BlockingPop_Original(b *testing.B) {
	q := NewZQueueOriginal[int]()
	var wg sync.WaitGroup

	// producer goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range b.N {
			q.Push(i)
		}
		q.Close()
	}()

	// consumer goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			_, err := q.PopOriginal()
			if errors.Is(err, zsync.ErrQueueClosed) {
				break
			}
		}
	}()

	wg.Wait()
}

// benchmark push to closed queue (error path).
func BenchmarkQueue_PushClosed_New(b *testing.B) {
	q := zsync.NewQueue[int]()
	q.Close()

	b.ResetTimer()
	for i := range b.N {
		q.Push(i)
	}
}

func BenchmarkQueue_PushClosed_Original(b *testing.B) {
	q := NewZQueueOriginal[int]()
	q.Close()

	b.ResetTimer()
	for i := range b.N {
		q.Push(i)
	}
}
