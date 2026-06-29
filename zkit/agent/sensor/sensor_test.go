package sensor_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/sensor"
)

func TestFunc_EmitsOnlyOnChange(t *testing.T) {
	t.Parallel()

	var polls atomic.Int32
	values := []string{"a", "a", "b", "b", "c"}
	s := sensor.NewFunc("test", time.Second, func(context.Context) (sensor.Observation, error) {
		idx := int(polls.Add(1) - 1)
		if idx >= len(values) {
			return sensor.Observation{}, sensor.ErrNoChange
		}
		return sensor.Observation{Value: values[idx]}, nil
	})

	var got []string
	for range values {
		obs, err := s.Poll(t.Context())
		if errors.Is(err, sensor.ErrNoChange) {
			continue
		}
		if err != nil {
			t.Fatalf("poll: %v", err)
		}
		got = append(got, obs.Value)
	}
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d]=%q, want %q", i, got[i], want[i])
		}
	}
}

func TestRunner_FiresHandlerOnChange(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	var tick atomic.Int32
	s := sensor.NewFunc("t", 10*time.Millisecond, func(context.Context) (sensor.Observation, error) {
		n := tick.Add(1)
		if n > 3 {
			return sensor.Observation{}, sensor.ErrNoChange
		}
		return sensor.Observation{Value: "v" + string('0'+n)}, nil
	})

	r := sensor.New()
	r.Register(s)
	var mu sync.Mutex
	var seen []string
	r.OnChange(func(_ context.Context, _ string, o sensor.Observation) {
		mu.Lock()
		seen = append(seen, o.Value)
		mu.Unlock()
	})
	r.Start(ctx)
	t.Cleanup(func() { r.Stop() })

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(seen)
		mu.Unlock()
		if n >= 3 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) < 3 {
		t.Fatalf("handler saw %v, want 3 distinct values", seen)
	}
}

func TestRunner_IsRunningAndRemove(t *testing.T) {
	t.Parallel()

	r := sensor.New()
	s := sensor.NewFunc("k1", time.Second, func(context.Context) (sensor.Observation, error) {
		return sensor.Observation{Value: "v"}, nil
	})
	r.Register(s)

	if !r.IsRunning("k1") {
		t.Error("expected k1 to be running after Register")
	}
	if r.IsRunning("missing") {
		t.Error("missing key should not be running")
	}

	if !r.Remove("k1") {
		t.Error("Remove(k1) returned false; expected true")
	}
	if r.IsRunning("k1") {
		t.Error("k1 should not be running after Remove")
	}
	if r.Remove("k1") {
		t.Error("second Remove(k1) should return false")
	}
}

// fakeReactive is a Reactive that emits one value on Start.
type fakeReactive struct {
	key     string
	value   string
	stopped chan struct{}
}

func (f *fakeReactive) Key() string { return f.key }
func (f *fakeReactive) Start(ctx context.Context, emit func(sensor.Observation)) error {
	emit(sensor.Observation{Value: f.value})
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-f.stopped:
		return nil
	}
}
func (f *fakeReactive) Stop() { close(f.stopped) }

func TestRunner_RegisterReactiveStartsImmediately(t *testing.T) {
	t.Parallel()

	r := sensor.New()
	got := make(chan string, 1)
	r.OnChange(func(_ context.Context, _ string, o sensor.Observation) {
		select {
		case got <- o.Value:
		default:
		}
	})

	rc := &fakeReactive{key: "rk", value: "first", stopped: make(chan struct{})}
	r.RegisterReactive(rc)

	select {
	case v := <-got:
		if v != "first" {
			t.Errorf("got %q, want first", v)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("did not receive emitted value within 500ms")
	}

	if !r.Remove("rk") {
		t.Error("Remove(rk) returned false")
	}
}
