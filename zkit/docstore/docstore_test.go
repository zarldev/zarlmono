package docstore_test

import (
	"context"
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zkit/docstore"
)

type note struct {
	Title string
	Tags  []string
}

func (n note) Clone() note { n.Tags = append([]string(nil), n.Tags...); return n }

type store interface {
	Create(context.Context, docstore.Record[note]) (docstore.Record[note], error)
	Read(context.Context, docstore.ID) (docstore.Record[note], error)
	Replace(context.Context, docstore.Record[note]) (docstore.Record[note], error)
	Put(context.Context, docstore.Record[note]) (docstore.Record[note], error)
	Delete(context.Context, docstore.ID) error
	List(context.Context, docstore.Page) ([]docstore.Record[note], error)
	Count(context.Context) (int, error)
}

func TestMemoryStoreContract(t *testing.T) { testStore(t, docstore.NewMemoryStore[note]()) }

func testStore(t *testing.T, store store) {
	t.Helper()
	ctx := t.Context()
	first := docstore.Record[note]{ID: "b", Value: note{Title: "before", Tags: []string{"one"}}}
	created, err := store.Create(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	first.Value.Tags[0] = "caller-mutated"
	created.Value.Tags[0] = "returned-mutated"
	read, err := store.Read(ctx, "b")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := read.Value.Tags[0], "one"; got != want {
		t.Fatalf("stored tag = %q, want %q", got, want)
	}
	read.Value.Tags[0] = "read-mutated"
	read, err = store.Read(ctx, "b")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := read.Value.Tags[0], "one"; got != want {
		t.Fatalf("read leaked alias: %q", got)
	}
	if _, err := store.Create(ctx, first); !errors.Is(err, docstore.ErrConflict) {
		t.Fatalf("duplicate error = %v", err)
	}
	if _, err := store.Replace(ctx, docstore.Record[note]{ID: "missing", Value: note{}}); !errors.Is(err, docstore.ErrNotFound) {
		t.Fatalf("replace missing = %v", err)
	}
	if err := store.Delete(ctx, "missing"); !errors.Is(err, docstore.ErrNotFound) {
		t.Fatalf("delete missing = %v", err)
	}
	if _, err := store.Create(ctx, docstore.Record[note]{ID: "a", Value: note{Title: "a"}}); err != nil {
		t.Fatal(err)
	}
	list, err := store.List(ctx, docstore.Page{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "a" {
		t.Fatalf("list = %#v", list)
	}
	if _, err := store.List(ctx, docstore.Page{Offset: -1}); !errors.Is(err, docstore.ErrInvalidRecord) {
		t.Fatalf("bad page error = %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.Count(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled count = %v", err)
	}
	if err := store.Delete(ctx, "b"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(ctx, "b"); !errors.Is(err, docstore.ErrNotFound) {
		t.Fatalf("read deleted = %v", err)
	}
}
