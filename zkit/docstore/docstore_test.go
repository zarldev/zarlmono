package docstore_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/docstore"
)

// TestDocument represents a test document.
type TestDocument struct {
	DocID   string    `json:"id"      bson:"_id,omitempty"`
	Name    string    `json:"name"    bson:"name"`
	Age     int       `json:"age"     bson:"age"`
	Email   string    `json:"email"   bson:"email"`
	Tags    []string  `json:"tags"    bson:"tags"`
	Active  bool      `json:"active"  bson:"active"`
	Created time.Time `json:"created" bson:"created"`
}

func (d *TestDocument) ID() string {
	return d.DocID
}

func (d *TestDocument) SetID(id string) {
	d.DocID = id
}

// Test implementations.
func TestStoreImplementations(t *testing.T) {
	tests := []struct {
		name      string
		storeFunc func() docstore.Store[*TestDocument]
	}{
		{
			name: "memory",
			storeFunc: func() docstore.Store[*TestDocument] {
				return docstore.NewMemoryStore[*TestDocument]()
			},
		},
		{
			name: "memory_with_delay",
			storeFunc: func() docstore.Store[*TestDocument] {
				return docstore.NewMemoryStore(
					docstore.WithQueryDelay[*TestDocument](10 * time.Millisecond),
				)
			},
		},
		{
			name: "memory_with_indexes",
			storeFunc: func() docstore.Store[*TestDocument] {
				return docstore.NewMemoryStore(
					docstore.WithIndexes[*TestDocument]("name", "email"),
				)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Run("BasicCRUD", func(t *testing.T) {
				store := tt.storeFunc()
				defer store.Close()
				testBasicCRUD(t, store)
			})

			t.Run("Metadata", func(t *testing.T) {
				store := tt.storeFunc()
				defer store.Close()
				testMetadata(t, store)
			})

			t.Run("Queries", func(t *testing.T) {
				store := tt.storeFunc()
				defer store.Close()
				testQueries(t, store)
			})

			t.Run("Sorting", func(t *testing.T) {
				store := tt.storeFunc()
				defer store.Close()
				testSorting(t, store)
			})

			t.Run("Pagination", func(t *testing.T) {
				store := tt.storeFunc()
				defer store.Close()
				testPagination(t, store)
			})

			t.Run("Cursor", func(t *testing.T) {
				store := tt.storeFunc()
				defer store.Close()
				testCursor(t, store)
			})
		})
	}
}

func testBasicCRUD(t *testing.T, store docstore.Store[*TestDocument]) {
	ctx := t.Context()

	// Test Insert
	doc := &TestDocument{
		Name:    "John Doe",
		Age:     30,
		Email:   "john@example.com",
		Tags:    []string{"developer", "go"},
		Active:  true,
		Created: time.Now(),
	}

	id, err := store.Insert(ctx, doc)
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	if id == "" {
		t.Fatal("Expected non-empty ID")
	}

	if doc.ID() != id {
		t.Errorf("Expected document ID to be set to %s, got %s", id, doc.ID())
	}

	// Test Find
	retrieved, err := store.Find(ctx, id)
	if err != nil {
		t.Fatalf("Find failed: %v", err)
	}

	if retrieved.Name != doc.Name {
		t.Errorf("Expected name %s, got %s", doc.Name, retrieved.Name)
	}
	if retrieved.Age != doc.Age {
		t.Errorf("Expected age %d, got %d", doc.Age, retrieved.Age)
	}

	// Test Update
	retrieved.Age = 31
	err = store.Update(ctx, id, retrieved)
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	updated, err := store.Find(ctx, id)
	if err != nil {
		t.Fatalf("Find after update failed: %v", err)
	}

	if updated.Age != 31 {
		t.Errorf("Expected updated age 31, got %d", updated.Age)
	}

	// Test Upsert (update existing)
	updated.Name = "John Smith"
	err = store.Upsert(ctx, updated)
	if err != nil {
		t.Fatalf("Upsert failed: %v", err)
	}

	upserted, err := store.Find(ctx, id)
	if err != nil {
		t.Fatalf("Find after upsert failed: %v", err)
	}

	if upserted.Name != "John Smith" {
		t.Errorf("Expected upserted name 'John Smith', got %s", upserted.Name)
	}

	// Test Delete
	err = store.Delete(ctx, id)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	_, err = store.Find(ctx, id)
	if err == nil {
		t.Error("Expected error when finding deleted document")
	}
}

func testMetadata(t *testing.T, store docstore.Store[*TestDocument]) {
	ctx := t.Context()

	doc := &TestDocument{
		Name:  "Jane Doe",
		Email: "jane@example.com",
	}

	meta := docstore.Metadata{
		"author":     "test",
		"version":    1,
		"important":  true,
		"created_by": "admin",
	}

	id, err := store.InsertWithMetadata(ctx, doc, meta)
	if err != nil {
		t.Fatalf("InsertWithMetadata failed: %v", err)
	}

	// For memory store, we can't directly access metadata
	// but the document should be inserted correctly
	retrieved, err := store.Find(ctx, id)
	if err != nil {
		t.Fatalf("Find failed: %v", err)
	}

	if retrieved.Name != doc.Name {
		t.Errorf("Expected name %s, got %s", doc.Name, retrieved.Name)
	}
}

func testQueries(t *testing.T, store docstore.Store[*TestDocument]) {
	ctx := t.Context()

	// Insert test documents
	docs := []*TestDocument{
		{Name: "Alice", Age: 25, Email: "alice@example.com", Active: true},
		{Name: "Bob", Age: 30, Email: "bob@example.com", Active: true},
		{Name: "Charlie", Age: 35, Email: "charlie@example.com", Active: false},
		{Name: "Diana", Age: 28, Email: "diana@example.com", Active: true},
	}

	for _, doc := range docs {
		_, err := store.Insert(ctx, doc)
		if err != nil {
			t.Fatalf("Insert failed: %v", err)
		}
	}

	// Test simple equality query
	results, err := store.Query(ctx).Where("name", docstore.OpEq, "Alice").Find()
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Expected 1 result, got %d", len(results))
	}
	if len(results) > 0 && results[0].Name != "Alice" {
		t.Errorf("Expected Alice, got %s", results[0].Name)
	}

	// Test range query
	results, err = store.Query(ctx).Where("age", docstore.OpGt, 28).Find()
	if err != nil {
		t.Fatalf("Range query failed: %v", err)
	}

	if len(results) != 2 { // Bob and Charlie
		t.Errorf("Expected 2 results for age > 28, got %d", len(results))
	}

	// Test boolean query
	results, err = store.Query(ctx).Where("active", docstore.OpEq, true).Find()
	if err != nil {
		t.Fatalf("Boolean query failed: %v", err)
	}

	if len(results) != 3 { // Alice, Bob, Diana
		t.Errorf("Expected 3 active users, got %d", len(results))
	}

	// Test IN query
	results, err = store.Query(ctx).WhereIn("name", []any{"Alice", "Bob"}).Find()
	if err != nil {
		t.Fatalf("IN query failed: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("Expected 2 results for IN query, got %d", len(results))
	}

	// Test FindOne
	result, err := store.Query(ctx).Where("age", docstore.OpEq, 30).FindOne()
	if err != nil {
		t.Fatalf("FindOne failed: %v", err)
	}

	if result.Name != "Bob" {
		t.Errorf("Expected Bob, got %s", result.Name)
	}

	// Test Count
	count, err := store.Query(ctx).Where("active", docstore.OpEq, true).Count()
	if err != nil {
		t.Fatalf("Count failed: %v", err)
	}

	if count != 3 {
		t.Errorf("Expected count 3, got %d", count)
	}
}

func testSorting(t *testing.T, store docstore.Store[*TestDocument]) {
	ctx := t.Context()

	// Insert documents with different ages
	docs := []*TestDocument{
		{Name: "User1", Age: 30},
		{Name: "User2", Age: 20},
		{Name: "User3", Age: 40},
		{Name: "User4", Age: 25},
	}

	for _, doc := range docs {
		_, err := store.Insert(ctx, doc)
		if err != nil {
			t.Fatalf("Insert failed: %v", err)
		}
	}

	// Test ascending sort
	results, err := store.Query(ctx).OrderBy("age", false).Find()
	if err != nil {
		t.Fatalf("Sort query failed: %v", err)
	}

	if len(results) >= 2 {
		if results[0].Age > results[1].Age {
			t.Error("Results not sorted in ascending order")
		}
	}

	// Test descending sort
	results, err = store.Query(ctx).OrderBy("age", true).Find()
	if err != nil {
		t.Fatalf("Sort query failed: %v", err)
	}

	if len(results) >= 2 {
		if results[0].Age < results[1].Age {
			t.Error("Results not sorted in descending order")
		}
	}
}

func testPagination(t *testing.T, store docstore.Store[*TestDocument]) {
	ctx := t.Context()

	// Insert multiple documents
	for i := range 10 {
		doc := &TestDocument{
			Name: fmt.Sprintf("User%d", i),
			Age:  20 + i,
		}
		_, err := store.Insert(ctx, doc)
		if err != nil {
			t.Fatalf("Insert failed: %v", err)
		}
	}

	// Test limit
	results, err := store.Query(ctx).Limit(3).Find()
	if err != nil {
		t.Fatalf("Limit query failed: %v", err)
	}

	if len(results) != 3 {
		t.Errorf("Expected 3 results with limit, got %d", len(results))
	}

	// Test skip
	results, err = store.Query(ctx).Skip(5).Find()
	if err != nil {
		t.Fatalf("Skip query failed: %v", err)
	}

	if len(results) != 5 { // Should get remaining 5 documents
		t.Errorf("Expected 5 results with skip, got %d", len(results))
	}

	// Test skip + limit
	results, err = store.Query(ctx).Skip(3).Limit(2).Find()
	if err != nil {
		t.Fatalf("Skip+Limit query failed: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("Expected 2 results with skip+limit, got %d", len(results))
	}
}

func testCursor(t *testing.T, store docstore.Store[*TestDocument]) {
	ctx := t.Context()

	// Insert documents
	docs := []*TestDocument{
		{Name: "Doc1", Age: 1},
		{Name: "Doc2", Age: 2},
		{Name: "Doc3", Age: 3},
	}

	for _, doc := range docs {
		_, err := store.Insert(ctx, doc)
		if err != nil {
			t.Fatalf("Insert failed: %v", err)
		}
	}

	// Test cursor iteration
	cursor, err := store.Query(ctx).Cursor()
	if err != nil {
		t.Fatalf("Cursor creation failed: %v", err)
	}
	defer cursor.Close(ctx)

	count := 0
	for cursor.Next(ctx) {
		var doc TestDocument
		err := cursor.Decode(&doc)
		if err != nil {
			t.Fatalf("Cursor decode failed: %v", err)
		}

		if doc.Name == "" {
			t.Error("Expected non-empty document name")
		}

		count++
	}

	if count != 3 {
		t.Errorf("Expected to iterate over 3 documents, got %d", count)
	}
}

func TestMetadata(t *testing.T) {
	meta := docstore.Metadata{
		"string_key": "value",
		"int_key":    42,
		"bool_key":   true,
	}

	// Test GetString
	if got := meta.GetString("string_key"); got != "value" {
		t.Errorf("Expected 'value', got '%s'", got)
	}
	if got := meta.GetString("missing"); got != "" {
		t.Errorf("Expected empty string for missing key, got '%s'", got)
	}

	// Test GetInt
	if got := meta.GetInt("int_key"); got != 42 {
		t.Errorf("Expected 42, got %d", got)
	}
	if got := meta.GetInt("missing"); got != 0 {
		t.Errorf("Expected 0 for missing key, got %d", got)
	}

	// Test GetBool
	if got := meta.GetBool("bool_key"); !got {
		t.Error("Expected true, got false")
	}
	if got := meta.GetBool("missing"); got {
		t.Error("Expected false for missing key, got true")
	}

	// Test Set
	meta.Set("new_key", "new_value")
	if got := meta.GetString("new_key"); got != "new_value" {
		t.Errorf("Expected 'new_value', got '%s'", got)
	}

	// Test Has
	if !meta.Has("string_key") {
		t.Error("Expected Has('string_key') to be true")
	}
	if meta.Has("missing") {
		t.Error("Expected Has('missing') to be false")
	}

	// Test Clone
	clone := meta.Clone()
	clone.Set("string_key", "modified")

	if meta.GetString("string_key") == "modified" {
		t.Error("Clone modified original metadata")
	}
	if clone.GetString("string_key") != "modified" {
		t.Error("Clone was not properly modified")
	}
}

func TestStoreCount(t *testing.T) {
	store := docstore.NewMemoryStore[*TestDocument]()
	defer store.Close()

	ctx := t.Context()

	// Initially empty
	count, err := store.Count(ctx)
	if err != nil {
		t.Fatalf("Count failed: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected count 0, got %d", count)
	}

	// Insert documents
	for i := range 5 {
		doc := &TestDocument{Name: fmt.Sprintf("Doc%d", i)}
		_, err := store.Insert(ctx, doc)
		if err != nil {
			t.Fatalf("Insert failed: %v", err)
		}
	}

	// Check count
	count, err = store.Count(ctx)
	if err != nil {
		t.Fatalf("Count failed: %v", err)
	}
	if count != 5 {
		t.Errorf("Expected count 5, got %d", count)
	}
}

func TestStoreUpsertNew(t *testing.T) {
	store := docstore.NewMemoryStore[*TestDocument]()
	defer store.Close()

	ctx := t.Context()

	// Upsert new document (should insert)
	doc := &TestDocument{
		Name:  "New Doc",
		Email: "new@example.com",
	}

	err := store.Upsert(ctx, doc)
	if err != nil {
		t.Fatalf("Upsert failed: %v", err)
	}

	if doc.ID() == "" {
		t.Error("Expected ID to be set after upsert")
	}

	// Verify document exists
	retrieved, err := store.Find(ctx, doc.ID())
	if err != nil {
		t.Fatalf("Find after upsert failed: %v", err)
	}

	if retrieved.Name != doc.Name {
		t.Errorf("Expected name %s, got %s", doc.Name, retrieved.Name)
	}
}

func TestErrorCases(t *testing.T) {
	store := docstore.NewMemoryStore[*TestDocument]()
	defer store.Close()

	ctx := t.Context()

	// Test find non-existent document
	_, err := store.Find(ctx, "nonexistent")
	if err == nil {
		t.Error("Expected error when finding non-existent document")
	}

	// Test update non-existent document
	doc := &TestDocument{Name: "Test"}
	err = store.Update(ctx, "nonexistent", doc)
	if err == nil {
		t.Error("Expected error when updating non-existent document")
	}

	// Test delete non-existent document
	err = store.Delete(ctx, "nonexistent")
	if err == nil {
		t.Error("Expected error when deleting non-existent document")
	}

	// Test FindOne with no results
	_, err = store.Query(ctx).Where("name", docstore.OpEq, "nonexistent").FindOne()
	if err == nil {
		t.Error("Expected error when FindOne returns no results")
	}
}
