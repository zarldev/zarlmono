package sensor_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
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
	synctest.Test(t, func(t *testing.T) {
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
		seen := make(chan string, 3)
		r.OnChange(func(_ context.Context, _ string, o sensor.Observation) { seen <- o.Value })
		r.Start(ctx)
		t.Cleanup(r.Stop)

		for range 3 {
			time.Sleep(100 * time.Millisecond)
		}
		synctest.Wait()
		for _, want := range []string{"v1", "v2", "v3"} {
			if got := <-seen; got != want {
				t.Errorf("handler value = %q, want %q", got, want)
			}
		}
	})
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
	r := sensor.New()
	got := make(chan string, 1)
	r.OnChange(func(_ context.Context, _ string, o sensor.Observation) { got <- o.Value })

	rc := &fakeReactive{key: "rk", value: "first", stopped: make(chan struct{})}
	if err := r.RegisterReactive(rc); err != nil {
		t.Fatalf("RegisterReactive: %v", err)
	}
	if got := <-got; got != "first" {
		t.Errorf("got %q, want first", got)
	}
	if !r.Remove("rk") {
		t.Error("Remove(rk) returned false")
	}
}
