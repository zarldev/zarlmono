package checkpoint_test

import (
	"errors"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/checkpoint"
)

func TestMemoryStoreCopiesAndLists(t *testing.T) {
	st := checkpoint.NewMemoryStore()
	cp := checkpoint.Checkpoint{ID: "a", RunID: "run", State: map[string]any{"n": 1}, CreatedAt: time.Unix(1, 0)}
	if err := st.Save(t.Context(), cp); err != nil {
		t.Fatal(err)
	}
	cp.State["n"] = 2
	got, err := st.Load(t.Context(), "a")
	if err != nil {
		t.Fatal(err)
	}
	if got.State["n"] != 1 {
		t.Fatalf("state aliased: %#v", got.State)
	}
	list, err := st.List(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "a" {
		t.Fatalf("list = %#v", list)
	}
}

func TestMemoryStoreLoadNotFound(t *testing.T) {
	_, err := checkpoint.NewMemoryStore().Load(t.Context(), "missing")
	if !errors.Is(err, checkpoint.ErrNotFound) {
		t.Fatalf("Load() error = %v, want ErrNotFound", err)
	}
}
