package docstore_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/docstore"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type note struct {
	Title     string             `bson:"title"`
	Tags      []string           `bson:"tags"`
	CreatedAt time.Time          `bson:"created_at"`
	Token     primitive.ObjectID `bson:"token"`
	Blob      []byte             `bson:"blob"`
	Scores    map[string]int     `bson:"scores"`
}

func (n note) Clone() note {
	n.Tags = append([]string(nil), n.Tags...)
	n.Blob = append([]byte(nil), n.Blob...)
	n.Scores = cloneMap(n.Scores)
	return n
}

func cloneMap[K comparable, V any](src map[K]V) map[K]V {
	if src == nil {
		return nil
	}
	dst := make(map[K]V, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

type store interface {
	Create(context.Context, docstore.Record[note]) (docstore.Record[note], error)
	Read(context.Context, docstore.ID) (docstore.Record[note], error)
	Replace(context.Context, docstore.Record[note]) (docstore.Record[note], error)
	Put(context.Context, docstore.Record[note]) (docstore.Record[note], error)
	Delete(context.Context, docstore.ID) error
	List(context.Context, docstore.Page) ([]docstore.Record[note], error)
	Count(context.Context) (int, error)
}

func TestMemoryStoreContract(t *testing.T) {
	testStoreContract(t, docstore.NewMemoryStore[note]())
}

func TestMongoStoreContract(t *testing.T) {
	uri, ok := os.LookupEnv("MONGODB_URI")
	if !ok {
		t.Skip("MONGODB_URI is not set; skipping MongoStore contract")
	}
	if uri == "" {
		t.Fatal("MONGODB_URI is set but empty")
	}

	connectCtx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	databaseName := fmt.Sprintf("zarlcode_docstore_contract_%d", time.Now().UnixNano())
	database, err := docstore.ConnectMongo(
		connectCtx,
		docstore.WithMongoURI(uri),
		docstore.WithDatabaseName(databaseName),
		docstore.WithPoolSize(0, 4),
	)
	if err != nil {
		t.Fatalf("ConnectMongo with configured MONGODB_URI: %v", err)
	}
	closed := false
	t.Cleanup(func() {
		if closed {
			return
		}
		ctx, cleanupCancel := context.WithTimeout(context.WithoutCancel(t.Context()), 10*time.Second)
		defer cleanupCancel()
		if err := database.Collection("records").Drop(ctx); err != nil {
			t.Errorf("drop MongoDB contract collection: %v", err)
		}
		if err := database.Close(ctx); err != nil {
			t.Errorf("close MongoDB contract database: %v", err)
		}
	})

	mongoStore := docstore.NewMongoStore[note](database.Collection("records"))
	testStoreContract(t, mongoStore)

	closeCtx, closeCancel := context.WithTimeout(t.Context(), 10*time.Second)
	if err := database.Collection("records").Drop(closeCtx); err != nil {
		closeCancel()
		t.Fatalf("drop MongoDB contract collection: %v", err)
	}
	if err := database.Close(closeCtx); err != nil {
		closeCancel()
		t.Fatalf("Close: %v", err)
	}
	closeCancel()
	closed = true
	if _, err := mongoStore.Count(t.Context()); err == nil {
		t.Error("Count after Close succeeded, want error")
	}
}

func testStoreContract(t *testing.T, store store) {
	t.Helper()
	ctx := t.Context()
	first := docstore.Record[note]{
		ID: "b",
		Value: note{
			Title:     "before",
			Tags:      []string{"one", "two"},
			CreatedAt: time.UnixMilli(1_704_067_200_123).UTC(),
			Token:     primitive.NewObjectID(),
			Blob:      []byte{0, 1, 2, 255},
			Scores:    map[string]int{"one": 1, "two": 2},
		},
	}
	created, err := store.Create(ctx, first)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	firstSnapshot := first.Value.Clone()
	first.Value.Tags[0] = "caller-mutated"
	first.Value.Blob[0] = 9
	first.Value.Scores["one"] = 9
	created.Value.Tags[0] = "returned-mutated"
	created.Value.Blob[0] = 8
	created.Value.Scores["one"] = 8

	read, err := store.Read(ctx, "b")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !reflect.DeepEqual(read.Value, firstSnapshot) {
		t.Fatalf("Read BSON fidelity = %#v, want %#v", read.Value, firstSnapshot)
	}
	read.Value.Tags[0] = "read-mutated"
	read.Value.Blob[0] = 7
	read.Value.Scores["one"] = 7
	read, err = store.Read(ctx, "b")
	if err != nil {
		t.Fatalf("second Read: %v", err)
	}
	if !reflect.DeepEqual(read.Value, firstSnapshot) {
		t.Fatalf("Read leaked mutable alias: %#v", read.Value)
	}

	if _, err := store.Create(ctx, first); !errors.Is(err, docstore.ErrConflict) {
		t.Fatalf("duplicate Create error = %v, want ErrConflict", err)
	}
	if _, err := store.Replace(ctx, docstore.Record[note]{ID: "missing", Value: note{}}); !errors.Is(err, docstore.ErrNotFound) {
		t.Fatalf("Replace missing error = %v, want ErrNotFound", err)
	}
	if err := store.Delete(ctx, "missing"); !errors.Is(err, docstore.ErrNotFound) {
		t.Fatalf("Delete missing error = %v, want ErrNotFound", err)
	}
	for name, operation := range map[string]func() error{
		"Create":  func() error { _, err := store.Create(ctx, docstore.Record[note]{}); return err },
		"Replace": func() error { _, err := store.Replace(ctx, docstore.Record[note]{}); return err },
		"Put":     func() error { _, err := store.Put(ctx, docstore.Record[note]{}); return err },
	} {
		if err := operation(); !errors.Is(err, docstore.ErrInvalidRecord) {
			t.Errorf("%s empty ID error = %v, want ErrInvalidRecord", name, err)
		}
	}

	replaced := docstore.Record[note]{ID: "b", Value: note{Title: "replaced", Tags: []string{"new"}}}
	if _, err := store.Replace(ctx, replaced); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	assertReadTitle(t, ctx, store, "b", "replaced")
	if _, err := store.Put(ctx, docstore.Record[note]{ID: "b", Value: note{Title: "updated"}}); err != nil {
		t.Fatalf("Put existing: %v", err)
	}
	if _, err := store.Put(ctx, docstore.Record[note]{ID: "c", Value: note{Title: "created by put"}}); err != nil {
		t.Fatalf("Put new: %v", err)
	}
	if _, err := store.Create(ctx, docstore.Record[note]{ID: "a", Value: note{Title: "created"}}); err != nil {
		t.Fatalf("Create a: %v", err)
	}

	count, err := store.Count(ctx)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 3 {
		t.Fatalf("Count = %d, want 3", count)
	}
	assertListIDs(t, ctx, store, docstore.Page{}, "a", "b", "c")
	assertListIDs(t, ctx, store, docstore.Page{Offset: 1, Limit: 1}, "b")
	assertListIDs(t, ctx, store, docstore.Page{Offset: 2}, "c")
	assertListIDs(t, ctx, store, docstore.Page{Offset: 20})
	for _, page := range []docstore.Page{{Offset: -1}, {Limit: -1}} {
		if _, err := store.List(ctx, page); !errors.Is(err, docstore.ErrInvalidRecord) {
			t.Errorf("List(%+v) error = %v, want ErrInvalidRecord", page, err)
		}
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	canceledOperations := map[string]func() error{
		"Create":  func() error { _, err := store.Create(canceled, docstore.Record[note]{ID: "cancel-create"}); return err },
		"Read":    func() error { _, err := store.Read(canceled, "a"); return err },
		"Replace": func() error { _, err := store.Replace(canceled, docstore.Record[note]{ID: "a"}); return err },
		"Put":     func() error { _, err := store.Put(canceled, docstore.Record[note]{ID: "cancel-put"}); return err },
		"Delete":  func() error { return store.Delete(canceled, "a") },
		"List":    func() error { _, err := store.List(canceled, docstore.Page{}); return err },
		"Count":   func() error { _, err := store.Count(canceled); return err },
	}
	for name, operation := range canceledOperations {
		if err := operation(); !errors.Is(err, context.Canceled) {
			t.Errorf("%s canceled error = %v, want context.Canceled", name, err)
		}
	}

	if err := store.Delete(ctx, "b"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Read(ctx, "b"); !errors.Is(err, docstore.ErrNotFound) {
		t.Fatalf("Read deleted error = %v, want ErrNotFound", err)
	}
}

func assertReadTitle(t *testing.T, ctx context.Context, store store, id docstore.ID, want string) {
	t.Helper()
	record, err := store.Read(ctx, id)
	if err != nil {
		t.Fatalf("Read(%q): %v", id, err)
	}
	if record.Value.Title != want {
		t.Fatalf("Read(%q) title = %q, want %q", id, record.Value.Title, want)
	}
}

func assertListIDs(t *testing.T, ctx context.Context, store store, page docstore.Page, want ...docstore.ID) {
	t.Helper()
	records, err := store.List(ctx, page)
	if err != nil {
		t.Fatalf("List(%+v): %v", page, err)
	}
	got := make([]docstore.ID, len(records))
	for i, record := range records {
		got[i] = record.ID
	}
	if !slices.Equal(got, want) {
		t.Fatalf("List(%+v) IDs = %v, want %v", page, got, want)
	}
}
