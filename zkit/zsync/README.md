The `zkit/zsync` directory contains a set of thread-safe, generic data structures for Go.

- **`Map[K, V]`**: A generic, thread-safe map implementation that uses a `sync.RWMutex` for concurrent access. The zero value is ready to use, so it can be embedded by value without a constructor. Offers standard map operations like `Set`, `Get`, `Delete`, `Len`, `Keys`, `Clear`, and `LoadOrStore` (generic equivalent of `sync.Map.LoadOrStore` — useful for per-key dispensers like mutex pools or atomic counters).

- **`Queue[T]`**: A generic, thread-safe, unbounded FIFO queue built with a slice and a `sync.Cond` for signaling. It features a blocking `Pop` method, a context-aware `PopContext` for cancellable operations, and a non-blocking `TryPop`. The queue can be closed to prevent new items from being added while allowing remaining items to be drained.

- **`Set[T]`**: A generic, thread-safe set built on top of the `zsync.Map`. It provides typical set operations such as `Add`, `Contains`, `Remove`, `Len`, `Values`, and `Clear`, along with methods to retrieve the set's values in a sorted order.

The package also defines a common set of errors (`ErrNotFound`, `ErrQueueClosed`, `ErrQueueEmpty`) in the `zsync.go` file. Each data structure is accompanied by its own set of tests and benchmarks to ensure correctness and performance.
