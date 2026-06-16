package zsync_test

import (
	"strconv"
	"sync"
	"testing"

	"github.com/zarldev/zarlmono/zkit/zsync"
)

func BenchmarkMap_Set(b *testing.B) {
	m := zsync.NewMap[string, int]()

	b.ResetTimer()
	for i := range b.N {
		m.Set("key"+strconv.Itoa(i), i)
	}
}

func BenchmarkMap_Get(b *testing.B) {
	m := zsync.NewMap[string, int]()

	// warmup
	for i := range 1000 {
		m.Set("key"+strconv.Itoa(i), i)
	}

	b.ResetTimer()
	for i := range b.N {
		m.Get("key" + strconv.Itoa(i%1000))
	}
}

func BenchmarkMap_Delete(b *testing.B) {
	m := zsync.NewMap[string, int]()

	// warmup
	for i := range b.N {
		m.Set("key"+strconv.Itoa(i), i)
	}

	b.ResetTimer()
	for i := range b.N {
		m.Delete("key" + strconv.Itoa(i))
	}
}

func BenchmarkMap_SetGet_Mixed(b *testing.B) {
	m := zsync.NewMap[string, int]()

	b.ResetTimer()
	for i := range b.N {
		if i%2 == 0 {
			m.Set("key"+strconv.Itoa(i), i)
		} else {
			m.Get("key" + strconv.Itoa(i-1))
		}
	}
}

func BenchmarkMap_Concurrent(b *testing.B) {
	m := zsync.NewMap[string, int]()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := "key" + strconv.Itoa(i)
			switch i % 3 {
			case 0:
				m.Set(key, i)
			case 1:
				m.Get(key)
			default:
				m.Delete(key)
			}
			i++
		}
	})
}

func BenchmarkMap_Keys(b *testing.B) {
	m := zsync.NewMap[string, int]()

	// warmup
	for i := range 1000 {
		m.Set("key"+strconv.Itoa(i), i)
	}

	b.ResetTimer()
	for range b.N {
		m.Keys()
	}
}

func BenchmarkMap_Clear(b *testing.B) {
	for range b.N {
		m := zsync.NewMap[string, int]()

		// warmup
		for i := range 1000 {
			m.Set("key"+strconv.Itoa(i), i)
		}

		b.StartTimer()
		m.Clear()
		b.StopTimer()
	}
}

// comparison with stdlib map + mutex.
type stdMap struct {
	mu sync.RWMutex
	m  map[string]int
}

func newStdMap() *stdMap {
	return &stdMap{
		m: make(map[string]int),
	}
}

func (sm *stdMap) Set(key string, value int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.m[key] = value
}

func (sm *stdMap) Get(key string) (int, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	val, ok := sm.m[key]
	return val, ok
}

func BenchmarkStdMap_Set(b *testing.B) {
	m := newStdMap()

	b.ResetTimer()
	for i := range b.N {
		m.Set("key"+strconv.Itoa(i), i)
	}
}

func BenchmarkStdMap_Get(b *testing.B) {
	m := newStdMap()

	// warmup
	for i := range 1000 {
		m.Set("key"+strconv.Itoa(i), i)
	}

	b.ResetTimer()
	for i := range b.N {
		m.Get("key" + strconv.Itoa(i%1000))
	}
}
