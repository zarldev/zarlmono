package zsync_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/zarldev/zarlmono/zkit/zsync"
)

func TestMap_Set(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value int
	}{
		{
			name:  "set string key with int value",
			key:   "test",
			value: 42,
		},
		{
			name:  "set empty string key",
			key:   "",
			value: 0,
		},
		{
			name:  "overwrite existing key",
			key:   "existing",
			value: 100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := zsync.NewMap[string, int]()
			m.Set(tt.key, tt.value)

			got, err := m.Get(tt.key)
			if err != nil {
				t.Errorf("Get() error = %v, want nil", err)
				return
			}
			if got != tt.value {
				t.Errorf("Get() = %v, want %v", got, tt.value)
			}
		})
	}
}

func TestMap_Get(t *testing.T) {
	tests := []struct {
		name  string
		setup map[string]int
		key   string
		want  int
		error error
	}{
		{
			name:  "get existing key",
			setup: map[string]int{"test": 42},
			key:   "test",
			want:  42,
		},
		{
			name:  "get non-existent key",
			setup: map[string]int{},
			key:   "missing",
			want:  0,
			error: zsync.ErrNotFound,
		},
		{
			name:  "get from empty map",
			setup: nil,
			key:   "any",
			want:  0,
			error: zsync.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := zsync.NewMap[string, int]()

			// setup
			for k, v := range tt.setup {
				m.Set(k, v)
			}

			got, err := m.Get(tt.key)
			if !errors.Is(err, tt.error) {
				t.Errorf("Get() error = %v, want %v", err, tt.error)
				return
			}
			if got != tt.want {
				t.Errorf("Get() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMap_Delete(t *testing.T) {
	tests := []struct {
		name   string
		setup  map[string]int
		key    string
		want   bool
		length int
	}{
		{
			name:   "delete existing key",
			setup:  map[string]int{"test": 42, "other": 1},
			key:    "test",
			want:   true,
			length: 1,
		},
		{
			name:   "delete non-existent key",
			setup:  map[string]int{"test": 42},
			key:    "missing",
			want:   false,
			length: 1,
		},
		{
			name:   "delete from empty map",
			setup:  nil,
			key:    "any",
			want:   false,
			length: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := zsync.NewMap[string, int]()

			// setup
			for k, v := range tt.setup {
				m.Set(k, v)
			}

			got := m.Delete(tt.key)
			if got != tt.want {
				t.Errorf("Delete() = %v, want %v", got, tt.want)
			}

			if m.Len() != tt.length {
				t.Errorf("Len() after delete = %v, want %v", m.Len(), tt.length)
			}
		})
	}
}

func TestMap_Len(t *testing.T) {
	tests := []struct {
		name  string
		setup map[string]int
		want  int
	}{
		{
			name:  "empty map",
			setup: nil,
			want:  0,
		},
		{
			name:  "single item",
			setup: map[string]int{"test": 42},
			want:  1,
		},
		{
			name:  "multiple items",
			setup: map[string]int{"a": 1, "b": 2, "c": 3},
			want:  3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := zsync.NewMap[string, int]()

			for k, v := range tt.setup {
				m.Set(k, v)
			}

			if got := m.Len(); got != tt.want {
				t.Errorf("Len() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMap_Keys(t *testing.T) {
	tests := []struct {
		name  string
		setup map[string]int
		want  []string
	}{
		{
			name:  "empty map",
			setup: nil,
			want:  []string{},
		},
		{
			name:  "single key",
			setup: map[string]int{"test": 42},
			want:  []string{"test"},
		},
		{
			name:  "multiple keys",
			setup: map[string]int{"a": 1, "b": 2},
			want:  []string{"a", "b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := zsync.NewMap[string, int]()

			for k, v := range tt.setup {
				m.Set(k, v)
			}

			got := m.Keys()
			if len(got) != len(tt.want) {
				t.Errorf("Keys() length = %v, want %v", len(got), len(tt.want))
				return
			}

			// unordered comparison
			gotMap := make(map[string]bool)
			for _, k := range got {
				gotMap[k] = true
			}

			for _, wantKey := range tt.want {
				if !gotMap[wantKey] {
					t.Errorf("Keys() missing key %v", wantKey)
				}
			}
		})
	}
}

func TestMap_Clear(t *testing.T) {
	m := zsync.NewMap[string, int]()
	m.Set("a", 1)
	m.Set("b", 2)

	if m.Len() != 2 {
		t.Errorf("Len() before clear = %v, want 2", m.Len())
	}

	m.Clear()

	if m.Len() != 0 {
		t.Errorf("Len() after clear = %v, want 0", m.Len())
	}

	_, err := m.Get("a")
	if !errors.Is(err, zsync.ErrNotFound) {
		t.Errorf("Get() after clear error = %v, want %v", err, zsync.ErrNotFound)
	}
}

func TestMap_LoadOrStore(t *testing.T) {
	m := zsync.NewMap[string, int]()

	// First call stores and returns the value.
	got, loaded := m.LoadOrStore("k", 1)
	if loaded {
		t.Errorf("first LoadOrStore: loaded=true, want false")
	}
	if got != 1 {
		t.Errorf("first LoadOrStore: got %d, want 1", got)
	}

	// Second call returns the existing value, not the supplied one.
	got, loaded = m.LoadOrStore("k", 99)
	if !loaded {
		t.Errorf("second LoadOrStore: loaded=false, want true")
	}
	if got != 1 {
		t.Errorf("second LoadOrStore: got %d, want existing 1", got)
	}

	// Underlying value visible to Get is still the first one stored.
	if v, err := m.Get("k"); err != nil || v != 1 {
		t.Errorf("Get after LoadOrStore: got (%d, %v), want (1, nil)", v, err)
	}
}

// Exactly one of N racing LoadOrStore calls wins; every other call
// receives the winner's value. The contract this test guards is what
// per-key mutex/counter dispensers (workspace pathLockMap,
// repeatcap.Counter) rely on — if it broke, two goroutines could
// each get their own mutex for the same key and the locking would
// silently fail.
func TestMap_LoadOrStore_RaceOneWinner(t *testing.T) {
	m := zsync.NewMap[string, *int]()
	const n = 64
	var wg sync.WaitGroup
	results := make([]*int, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v := i
			got, _ := m.LoadOrStore("k", &v)
			results[i] = got
		}(i)
	}
	wg.Wait()
	// Every goroutine must observe the same winning pointer.
	winner := results[0]
	for i, r := range results {
		if r != winner {
			t.Errorf("goroutine %d saw a different pointer than the winner", i)
		}
	}
}

func TestMap_ZeroValueUsable(t *testing.T) {
	// var m zsync.Map[...] — no NewMap, no embedding helper. Should
	// behave the same as a constructed one. Reads on the empty map
	// return ErrNotFound; first Set lazily allocates the backing
	// map; subsequent operations work normally.
	var m zsync.Map[string, int]

	if _, err := m.Get("missing"); !errors.Is(err, zsync.ErrNotFound) {
		t.Errorf("zero-value Get: err=%v, want ErrNotFound", err)
	}
	if m.Len() != 0 {
		t.Errorf("zero-value Len: %d, want 0", m.Len())
	}
	if got := m.Delete("missing"); got {
		t.Errorf("zero-value Delete: got true, want false")
	}
	if len(m.Keys()) != 0 {
		t.Errorf("zero-value Keys: len %d, want 0", len(m.Keys()))
	}

	m.Set("k", 42)
	if v, err := m.Get("k"); err != nil || v != 42 {
		t.Errorf("Set after zero-value: got (%d, %v), want (42, nil)", v, err)
	}

	// LoadOrStore also works without prior Set.
	var m2 zsync.Map[string, int]
	if v, loaded := m2.LoadOrStore("k", 7); loaded || v != 7 {
		t.Errorf("zero-value LoadOrStore: got (%d, loaded=%v), want (7, false)", v, loaded)
	}
}

// concurrent access test.
func TestMap_Concurrent(t *testing.T) {
	m := zsync.NewMap[int, string]()

	const numGoroutines = 100
	const numOperations = 1000

	var wg sync.WaitGroup

	// concurrent writes
	for i := range numGoroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := range numOperations {
				key := id*numOperations + j
				m.Set(key, "value")
			}
		}(i)
	}

	// concurrent reads
	for i := range numGoroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := range numOperations {
				key := id*numOperations + j
				m.Get(key) // ignore result, just testing for races
			}
		}(i)
	}

	wg.Wait()

	expectedLen := numGoroutines * numOperations
	if m.Len() != expectedLen {
		t.Errorf("Len() after concurrent operations = %v, want %v", m.Len(), expectedLen)
	}
}
