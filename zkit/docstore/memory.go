package docstore

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zarldev/zarlmono/zkit/options"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

var memoryIDCounter atomic.Uint64

// MemoryStore implements Store using in-memory storage.
//
// Aliasing caveat: when T is a pointer or contains pointer fields,
// Find / query results return the SAME object held by the store —
// callers that mutate the returned value mutate the stored state
// without going through Update. This is fine for short-lived /
// read-only tests but a footgun in tests that exercise mutation.
// The portable fix is to add a Clone() method to the [Document]
// interface; consumers wanting safety today can json-round-trip
// their result or stage edits through Update explicitly.
//
// Context handling: queryDelay sleeps and condition scans honour
// ctx cancellation — earlier shape ignored ctx entirely. Find()
// returns ctx.Err() immediately when ctx is already done.
type MemoryStore[T Document] struct {
	mu        sync.RWMutex
	documents map[string]T
	metadata  map[string]Metadata
	config    *memoryConfig
}

// memoryConfig holds memory store configuration.
type memoryConfig struct {
	queryDelay time.Duration
	indexes    map[string]bool
}

// defaultMemoryConfig returns default configuration.
func defaultMemoryConfig() *memoryConfig {
	return &memoryConfig{
		queryDelay: 0,
		indexes:    make(map[string]bool),
	}
}

// NewMemoryStore creates an in-memory store for testing.
func NewMemoryStore[T Document](opts ...options.Option[MemoryStore[T]]) Store[T] {
	config := defaultMemoryConfig()

	store := &MemoryStore[T]{
		documents: make(map[string]T),
		metadata:  make(map[string]Metadata),
		config:    config,
	}

	// Apply options
	for _, opt := range opts {
		opt(store)
	}

	return store
}

// WithQueryDelay sets the delay before queries are executed.
func WithQueryDelay[T Document](delay time.Duration) options.Option[MemoryStore[T]] {
	return func(store *MemoryStore[T]) {
		store.config.queryDelay = delay
	}
}

// WithIndexes declares the fields the in-memory store treats as indexed,
// mirroring a real backend's index set so query-shape tests stay honest.
func WithIndexes[T Document](fields ...string) options.Option[MemoryStore[T]] {
	return func(store *MemoryStore[T]) {
		for _, field := range fields {
			store.config.indexes[field] = true
		}
	}
}

// Insert adds a new document.
func (s *MemoryStore[T]) Insert(ctx context.Context, doc T) (string, error) {
	return s.InsertWithMetadata(ctx, doc, Metadata{})
}

// InsertWithMetadata adds a document with metadata.
func (s *MemoryStore[T]) InsertWithMetadata(ctx context.Context, doc T, meta Metadata) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Generate ID if not set
	if doc.ID() == "" {
		doc.SetID(generateID())
	}

	id := doc.ID()

	// Check if document already exists
	if _, exists := s.documents[id]; exists {
		return "", fmt.Errorf("document with ID %s already exists", id)
	}

	s.documents[id] = doc
	s.metadata[id] = meta.Clone()

	return id, nil
}

// Find retrieves a document by ID. Returns ctx.Err() if ctx is
// already cancelled. queryDelay is honoured via a cancellable
// timer rather than time.Sleep so a long delay can't outlive a
// short ctx.
func (s *MemoryStore[T]) Find(ctx context.Context, id string) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}

	s.mu.RLock()
	doc, exists := s.documents[id]
	s.mu.RUnlock()
	if !exists {
		return zero, errors.New("document not found")
	}

	// Delay is for testing slow-store behaviour. Use a cancellable
	// wait outside the lock — sleeping under RLock starves writers,
	// and time.Sleep doesn't honour ctx.
	if s.config.queryDelay > 0 {
		t := time.NewTimer(s.config.queryDelay)
		defer t.Stop()
		select {
		case <-t.C:
		case <-ctx.Done():
			return zero, ctx.Err()
		}
	}

	return doc, nil
}

// Update modifies an existing document.
func (s *MemoryStore[T]) Update(ctx context.Context, id string, doc T) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.documents[id]; !exists {
		return errors.New("document not found")
	}

	// Ensure ID is set correctly
	doc.SetID(id)
	s.documents[id] = doc

	return nil
}

// Upsert inserts or updates a document.
func (s *MemoryStore[T]) Upsert(ctx context.Context, doc T) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Generate ID if not set
	if doc.ID() == "" {
		doc.SetID(generateID())
	}

	id := doc.ID()
	s.documents[id] = doc

	// Preserve existing metadata or create new
	if _, exists := s.metadata[id]; !exists {
		s.metadata[id] = make(Metadata)
	}

	return nil
}

// Delete removes a document.
func (s *MemoryStore[T]) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.documents[id]; !exists {
		return errors.New("document not found")
	}

	delete(s.documents, id)
	delete(s.metadata, id)

	return nil
}

// Query creates a new query builder.
func (s *MemoryStore[T]) Query(ctx context.Context) Query[T] {
	return &MemoryQuery[T]{
		ctx:   ctx,
		store: s,
	}
}

// Count returns total number of documents.
func (s *MemoryStore[T]) Count(ctx context.Context) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return int64(len(s.documents)), nil
}

// Close is a no-op for memory store.
func (s *MemoryStore[T]) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.documents = make(map[string]T)
	s.metadata = make(map[string]Metadata)

	return nil
}

// MemoryQuery implements Query for memory store.
type MemoryQuery[T Document] struct {
	ctx        context.Context
	store      *MemoryStore[T]
	conditions []queryCondition
	sortField  string
	sortDesc   bool
	limit      *int
	skip       *int
}

// queryCondition represents a filter condition.
type queryCondition struct {
	field string
	op    Operator
	value any
}

// Where adds a filter condition.
func (q *MemoryQuery[T]) Where(field string, op Operator, value any) Query[T] {
	q.conditions = append(q.conditions, queryCondition{
		field: field,
		op:    op,
		value: value,
	})
	return q
}

// WhereIn adds an "in" filter condition.
func (q *MemoryQuery[T]) WhereIn(field string, values []any) Query[T] {
	return q.Where(field, OpIn, values)
}

// OrderBy adds sorting.
func (q *MemoryQuery[T]) OrderBy(field string, desc bool) Query[T] {
	q.sortField = field
	q.sortDesc = desc
	return q
}

// Limit sets maximum results.
func (q *MemoryQuery[T]) Limit(n int) Query[T] {
	q.limit = &n
	return q
}

// Skip sets offset for results.
func (q *MemoryQuery[T]) Skip(n int) Query[T] {
	q.skip = &n
	return q
}

// Find executes query and returns all matching documents. Returns
// ctx.Err() if the query's context is cancelled before the scan
// completes. queryDelay sleeps outside the lock and is cancellable
// — earlier shape blocked writers under RLock for the whole delay
// and ignored ctx.
func (q *MemoryQuery[T]) Find() ([]T, error) {
	if err := q.ctx.Err(); err != nil {
		return nil, err
	}

	// Delay first (outside the lock + ctx-aware) so a slow-store
	// simulation doesn't pin writers.
	if q.store.config.queryDelay > 0 {
		t := time.NewTimer(q.store.config.queryDelay)
		defer t.Stop()
		select {
		case <-t.C:
		case <-q.ctx.Done():
			return nil, q.ctx.Err()
		}
	}

	q.store.mu.RLock()
	defer q.store.mu.RUnlock()

	var results []T

	// Filter documents
	for _, doc := range q.store.documents {
		if err := q.ctx.Err(); err != nil {
			return nil, err
		}
		if q.matchesConditions(doc) {
			results = append(results, doc)
		}
	}

	// Sort results
	if q.sortField != "" {
		q.sortResults(results)
	}

	// Apply skip and limit
	start := 0
	if q.skip != nil {
		start = *q.skip
		if start >= len(results) {
			return []T{}, nil
		}
	}

	end := len(results)
	if q.limit != nil && start+*q.limit < len(results) {
		end = start + *q.limit
	}

	if start > 0 || end < len(results) {
		results = results[start:end]
	}

	return results, nil
}

// FindOne executes query and returns first matching document.
func (q *MemoryQuery[T]) FindOne() (T, error) {
	var zero T

	results, err := q.Limit(1).Find()
	if err != nil {
		return zero, err
	}

	if len(results) == 0 {
		return zero, errors.New("document not found")
	}

	return results[0], nil
}

// Count returns number of matching documents.
func (q *MemoryQuery[T]) Count() (int64, error) {
	q.store.mu.RLock()
	defer q.store.mu.RUnlock()

	count := int64(0)
	for _, doc := range q.store.documents {
		if q.matchesConditions(doc) {
			count++
		}
	}

	return count, nil
}

// Cursor returns an iterator for large result sets.
func (q *MemoryQuery[T]) Cursor() (Cursor[T], error) {
	results, err := q.Find()
	if err != nil {
		return nil, err
	}

	return &MemoryCursor[T]{
		results: results,
		index:   -1,
	}, nil
}

// Helper methods.
func (q *MemoryQuery[T]) matchesConditions(doc T) bool {
	docValue := reflect.ValueOf(doc)
	if docValue.Kind() == reflect.Pointer {
		docValue = docValue.Elem()
	}

	for _, condition := range q.conditions {
		if !q.matchesCondition(docValue, condition) {
			return false
		}
	}

	return true
}

func (q *MemoryQuery[T]) matchesCondition(docValue reflect.Value, condition queryCondition) bool {
	fieldValue := q.getFieldValue(docValue, condition.field)
	if !fieldValue.IsValid() {
		return false
	}

	switch condition.op {
	case OpEq:
		return q.compareValues(fieldValue, condition.value) == 0
	case OpNe:
		return q.compareValues(fieldValue, condition.value) != 0
	case OpGt:
		return q.compareValues(fieldValue, condition.value) > 0
	case OpGte:
		return q.compareValues(fieldValue, condition.value) >= 0
	case OpLt:
		return q.compareValues(fieldValue, condition.value) < 0
	case OpLte:
		return q.compareValues(fieldValue, condition.value) <= 0
	case OpIn:
		if values, ok := condition.value.([]any); ok {
			for _, v := range values {
				if q.compareValues(fieldValue, v) == 0 {
					return true
				}
			}
		}
		return false
	case OpNotIn:
		if values, ok := condition.value.([]any); ok {
			for _, v := range values {
				if q.compareValues(fieldValue, v) == 0 {
					return false
				}
			}
			return true
		}
		return false
	case OpExists:
		exists, _ := condition.value.(bool)
		return fieldValue.IsValid() == exists
	case OpRegex:
		if pattern, ok := condition.value.(string); ok && fieldValue.Kind() == reflect.String {
			regex, err := regexp.Compile(pattern)
			if err != nil {
				return false
			}
			return regex.MatchString(fieldValue.String())
		}
		return false
	}

	return false
}

func (q *MemoryQuery[T]) getFieldValue(docValue reflect.Value, field string) reflect.Value {
	parts := strings.Split(field, ".")
	current := docValue

	for _, part := range parts {
		if current.Kind() == reflect.Pointer {
			current = current.Elem()
		}

		if current.Kind() != reflect.Struct {
			return reflect.Value{}
		}

		current = current.FieldByName(cases.Title(language.Und).String(part))
		if !current.IsValid() {
			return reflect.Value{}
		}
	}

	return current
}

func (q *MemoryQuery[T]) compareValues(fieldValue reflect.Value, targetValue any) int {
	if !fieldValue.IsValid() {
		return -1
	}

	// Convert to comparable types
	fieldInterface := fieldValue.Interface()

	// Simple comparison for common types
	switch fv := fieldInterface.(type) {
	case string:
		if tv, ok := targetValue.(string); ok {
			if fv < tv {
				return -1
			} else if fv > tv {
				return 1
			}
			return 0
		}
	case int, int8, int16, int32, int64:
		fieldInt := reflect.ValueOf(fv).Int()
		if tv := reflect.ValueOf(targetValue); tv.Kind() >= reflect.Int && tv.Kind() <= reflect.Int64 {
			targetInt := tv.Int()
			if fieldInt < targetInt {
				return -1
			} else if fieldInt > targetInt {
				return 1
			}
			return 0
		}
	}

	// Fallback to string comparison
	fieldStr := fmt.Sprintf("%v", fieldInterface)
	targetStr := fmt.Sprintf("%v", targetValue)

	if fieldStr < targetStr {
		return -1
	} else if fieldStr > targetStr {
		return 1
	}
	return 0
}

func (q *MemoryQuery[T]) sortResults(results []T) {
	// Simple bubble sort for small datasets
	for i := range len(results) - 1 {
		for j := range len(results) - i - 1 {
			docValue1 := reflect.ValueOf(results[j])
			docValue2 := reflect.ValueOf(results[j+1])

			if docValue1.Kind() == reflect.Pointer {
				docValue1 = docValue1.Elem()
			}
			if docValue2.Kind() == reflect.Pointer {
				docValue2 = docValue2.Elem()
			}

			fieldValue1 := q.getFieldValue(docValue1, q.sortField)
			fieldValue2 := q.getFieldValue(docValue2, q.sortField)

			// Invalid sort fields (typo / missing struct field) used
			// to panic on .Interface(). Treat both-invalid as equal
			// and one-invalid as "the valid value sorts first" so the
			// query still completes and returns a reasonable order
			// instead of crashing the process.
			if !fieldValue1.IsValid() && !fieldValue2.IsValid() {
				continue
			}
			if !fieldValue2.IsValid() {
				// fieldValue1 is valid; leave order unchanged.
				continue
			}
			if !fieldValue1.IsValid() {
				// fieldValue2 is valid; force swap so it bubbles up.
				results[j], results[j+1] = results[j+1], results[j]
				continue
			}

			comparison := q.compareValues(fieldValue1, fieldValue2.Interface())
			shouldSwap := comparison > 0
			if q.sortDesc {
				shouldSwap = comparison < 0
			}

			if shouldSwap {
				results[j], results[j+1] = results[j+1], results[j]
			}
		}
	}
}

// MemoryCursor implements Cursor for memory store.
type MemoryCursor[T Document] struct {
	results []T
	index   int
}

// Next advances to the next document.
func (c *MemoryCursor[T]) Next(ctx context.Context) bool {
	c.index++
	return c.index < len(c.results)
}

// Decode retrieves current document.
func (c *MemoryCursor[T]) Decode(doc T) error {
	if c.index < 0 || c.index >= len(c.results) {
		return errors.New("cursor out of bounds")
	}

	// Copy the document
	sourceValue := reflect.ValueOf(c.results[c.index])
	targetValue := reflect.ValueOf(doc)

	if targetValue.Kind() == reflect.Pointer && sourceValue.Kind() == reflect.Pointer {
		targetValue.Elem().Set(sourceValue.Elem())
	} else {
		return errors.New("cannot copy document")
	}

	return nil
}

// Close releases cursor resources.
func (c *MemoryCursor[T]) Close(ctx context.Context) error {
	c.results = nil
	return nil
}

// Helper function to generate unique IDs.
func generateID() string {
	return fmt.Sprintf("mem_%d_%d", time.Now().UnixNano(), memoryIDCounter.Add(1))
}
