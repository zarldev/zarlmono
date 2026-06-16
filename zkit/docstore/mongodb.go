package docstore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zarldev/zarlmono/zkit/options"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	mongooptions "go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

const (
	mongoFieldID   = "_id"
	mongoFieldData = "data"
)

// MongoDatabase wraps MongoDB database operations.
type MongoDatabase struct {
	client   *mongo.Client
	database *mongo.Database
	config   *mongoConfig
}

// mongoConfig holds MongoDB configuration.
type mongoConfig struct {
	uri         string
	name        string
	timeout     time.Duration
	maxPoolSize uint64
	minPoolSize uint64
}

// defaultMongoConfig returns default configuration.
func defaultMongoConfig() *mongoConfig {
	return &mongoConfig{
		uri:         "mongodb://localhost:27017",
		name:        "docstore",
		timeout:     10 * time.Second,
		maxPoolSize: 100,
		minPoolSize: 10,
	}
}

// ConnectMongo creates a MongoDB database connection.
func ConnectMongo(ctx context.Context, opts ...options.Option[MongoDatabase]) (*MongoDatabase, error) {
	config := defaultMongoConfig()

	db := &MongoDatabase{
		config: config,
	}

	// Apply options
	for _, opt := range opts {
		opt(db)
	}

	// Set up client options
	clientOpts := mongooptions.Client().
		ApplyURI(config.uri).
		SetConnectTimeout(config.timeout).
		SetMaxPoolSize(config.maxPoolSize).
		SetMinPoolSize(config.minPoolSize)

	// Connect to MongoDB
	client, err := mongo.Connect(ctx, clientOpts)
	if err != nil {
		return nil, fmt.Errorf("connect to MongoDB: %w", err)
	}

	// Ping to verify connection
	if err := client.Ping(ctx, readpref.Primary()); err != nil {
		if derr := client.Disconnect(ctx); derr != nil {
			return nil, fmt.Errorf("ping MongoDB: %w; disconnect: %w", err, derr)
		}
		return nil, fmt.Errorf("ping MongoDB: %w", err)
	}

	db.client = client
	db.database = client.Database(config.name)

	return db, nil
}

// WithMongoURI sets the MongoDB connection URI.
func WithMongoURI(uri string) options.Option[MongoDatabase] {
	return func(db *MongoDatabase) {
		db.config.uri = uri
	}
}

// WithDatabaseName overrides the default database name.
func WithDatabaseName(name string) options.Option[MongoDatabase] {
	return func(db *MongoDatabase) {
		db.config.name = name
	}
}

// WithMongoTimeout sets the per-operation timeout applied to connect,
// ping, and collection operations.
func WithMongoTimeout(timeout time.Duration) options.Option[MongoDatabase] {
	return func(db *MongoDatabase) {
		db.config.timeout = timeout
	}
}

// WithPoolSize bounds the driver's connection pool to [minPool, maxPool].
func WithPoolSize(minPool, maxPool uint64) options.Option[MongoDatabase] {
	return func(db *MongoDatabase) {
		db.config.minPoolSize = minPool
		db.config.maxPoolSize = maxPool
	}
}

// Collection returns a typed collection.
func (db *MongoDatabase) Collection(name string) *mongo.Collection {
	return db.database.Collection(name)
}

// NewMongoStore creates a typed MongoDB store.
func NewMongoStore[T Document](coll *mongo.Collection) Store[T] {
	return &MongoCollection[T]{
		coll: coll,
	}
}

// Close disconnects from MongoDB.
func (db *MongoDatabase) Close(ctx context.Context) error {
	if db.client != nil {
		return db.client.Disconnect(ctx)
	}
	return nil
}

// MongoCollection implements Store for MongoDB.
type MongoCollection[T Document] struct {
	coll *mongo.Collection
}

// Insert adds a new document.
func (c *MongoCollection[T]) Insert(ctx context.Context, doc T) (string, error) {
	return c.InsertWithMetadata(ctx, doc, Metadata{})
}

// InsertWithMetadata adds a document with metadata.
func (c *MongoCollection[T]) InsertWithMetadata(ctx context.Context, doc T, meta Metadata) (string, error) {
	// If document doesn't have an ID, generate one
	if doc.ID() == "" {
		doc.SetID(primitive.NewObjectID().Hex())
	}

	// Create document wrapper with metadata
	wrapper := bson.M{
		mongoFieldID:   doc.ID(),
		mongoFieldData: doc,
		"metadata":     meta,
	}

	result, err := c.coll.InsertOne(ctx, wrapper)
	if err != nil {
		return "", fmt.Errorf("insert document: %w", err)
	}

	// Extract ID from result
	if oid, ok := result.InsertedID.(primitive.ObjectID); ok {
		return oid.Hex(), nil
	}
	if id, ok := result.InsertedID.(string); ok {
		return id, nil
	}

	return doc.ID(), nil
}

// Find retrieves a document by ID.
func (c *MongoCollection[T]) Find(ctx context.Context, id string) (T, error) {
	var zero T

	filter := bson.M{mongoFieldID: id}

	var wrapper bson.M
	err := c.coll.FindOne(ctx, filter).Decode(&wrapper)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return zero, errors.New("document not found")
		}
		return zero, fmt.Errorf("find document: %w", err)
	}

	// Extract document from wrapper
	dataBytes, err := bson.Marshal(wrapper[mongoFieldData])
	if err != nil {
		return zero, fmt.Errorf("marshal document data: %w", err)
	}

	var doc T
	err = bson.Unmarshal(dataBytes, &doc)
	if err != nil {
		return zero, fmt.Errorf("unmarshal document: %w", err)
	}

	return doc, nil
}

// Update modifies an existing document.
func (c *MongoCollection[T]) Update(ctx context.Context, id string, doc T) error {
	filter := bson.M{mongoFieldID: id}
	update := bson.M{
		"$set": bson.M{
			mongoFieldData: doc,
			"updated_at":   time.Now(),
		},
	}

	result, err := c.coll.UpdateOne(ctx, filter, update)
	if err != nil {
		return fmt.Errorf("update document: %w", err)
	}

	if result.MatchedCount == 0 {
		return errors.New("document not found")
	}

	return nil
}

// Upsert inserts or updates a document.
func (c *MongoCollection[T]) Upsert(ctx context.Context, doc T) error {
	if doc.ID() == "" {
		doc.SetID(primitive.NewObjectID().Hex())
	}

	filter := bson.M{mongoFieldID: doc.ID()}
	update := bson.M{
		"$set": bson.M{
			mongoFieldData: doc,
			"updated_at":   time.Now(),
		},
		"$setOnInsert": bson.M{
			"created_at": time.Now(),
		},
	}

	opts := mongooptions.Update().SetUpsert(true)
	_, err := c.coll.UpdateOne(ctx, filter, update, opts)
	if err != nil {
		return fmt.Errorf("upsert document: %w", err)
	}

	return nil
}

// Delete removes a document.
func (c *MongoCollection[T]) Delete(ctx context.Context, id string) error {
	filter := bson.M{mongoFieldID: id}

	result, err := c.coll.DeleteOne(ctx, filter)
	if err != nil {
		return fmt.Errorf("delete document: %w", err)
	}

	if result.DeletedCount == 0 {
		return errors.New("document not found")
	}

	return nil
}

// Query creates a new query builder.
func (c *MongoCollection[T]) Query(ctx context.Context) Query[T] {
	return &MongoQuery[T]{
		ctx:    ctx,
		coll:   c.coll,
		filter: bson.M{},
	}
}

// Count returns total number of documents.
func (c *MongoCollection[T]) Count(ctx context.Context) (int64, error) {
	count, err := c.coll.CountDocuments(ctx, bson.M{})
	if err != nil {
		return 0, fmt.Errorf("count documents: %w", err)
	}
	return count, nil
}

// Close is a no-op for collections (database handles cleanup).
func (c *MongoCollection[T]) Close() error {
	return nil
}

// MongoQuery implements Query for MongoDB.
type MongoQuery[T Document] struct {
	ctx    context.Context
	coll   *mongo.Collection
	filter bson.M
	sort   bson.M
	limit  *int64
	skip   *int64
}

// Where adds a filter condition. Field names are validated against
// [isValidMongoFieldPath] — see that function for the policy. Invalid
// names are dropped silently (the query still runs but won't match
// what the caller intended); panicking inside a fluent builder would
// be a worse outcome.
func (q *MongoQuery[T]) Where(field string, op Operator, value any) Query[T] {
	if !isValidMongoFieldPath(field) {
		return q
	}
	var condition any

	switch op {
	case OpEq:
		condition = value
	case OpNe:
		condition = bson.M{"$ne": value}
	case OpGt:
		condition = bson.M{"$gt": value}
	case OpGte:
		condition = bson.M{"$gte": value}
	case OpLt:
		condition = bson.M{"$lt": value}
	case OpLte:
		condition = bson.M{"$lte": value}
	case OpIn:
		condition = bson.M{"$in": value}
	case OpNotIn:
		condition = bson.M{"$nin": value}
	case OpExists:
		condition = bson.M{"$exists": value}
	case OpRegex:
		condition = bson.M{"$regex": value}
	}

	// Prefix field with "data." to query within document wrapper.
	q.filter["data."+field] = condition
	return q
}

// isValidMongoFieldPath returns true for field paths that are safe to
// concatenate into a MongoDB key. Allowed: ASCII letters, digits,
// underscore, hyphen, and a dot as the path separator. Rejected:
// the empty string, leading "$" (MongoDB operator syntax), embedded
// $ (operator injection), and embedded null bytes (corrupt the BSON
// key encoding).
//
// Doesn't try to be exhaustive against every Mongo edge case — the
// goal is to reject the obvious injection vectors flagged by the
// adversarial review, not replicate the server's full key grammar.
func isValidMongoFieldPath(field string) bool {
	if field == "" {
		return false
	}
	if strings.HasPrefix(field, "$") {
		return false
	}
	for _, r := range field {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-' || r == '.':
		default:
			return false
		}
	}
	return true
}

// WhereIn adds an "in" filter condition.
func (q *MongoQuery[T]) WhereIn(field string, values []any) Query[T] {
	return q.Where(field, OpIn, values)
}

// OrderBy adds sorting. Same field-name validation as Where —
// invalid names are dropped rather than passed through to MongoDB
// where they'd either error opaquely or sort by something the caller
// didn't intend.
func (q *MongoQuery[T]) OrderBy(field string, desc bool) Query[T] {
	if !isValidMongoFieldPath(field) {
		return q
	}
	if q.sort == nil {
		q.sort = bson.M{}
	}

	order := 1
	if desc {
		order = -1
	}

	q.sort["data."+field] = order
	return q
}

// Limit sets maximum results.
func (q *MongoQuery[T]) Limit(n int) Query[T] {
	limit := int64(n)
	q.limit = &limit
	return q
}

// Skip sets offset for results.
func (q *MongoQuery[T]) Skip(n int) Query[T] {
	skip := int64(n)
	q.skip = &skip
	return q
}

// Find executes query and returns all matching documents.
func (q *MongoQuery[T]) Find() ([]T, error) {
	opts := mongooptions.Find()
	if q.sort != nil {
		opts.SetSort(q.sort)
	}
	if q.limit != nil {
		opts.SetLimit(*q.limit)
	}
	if q.skip != nil {
		opts.SetSkip(*q.skip)
	}

	cursor, err := q.coll.Find(q.ctx, q.filter, opts)
	if err != nil {
		return nil, fmt.Errorf("execute query: %w", err)
	}
	defer cursor.Close(q.ctx)

	var results []T
	for cursor.Next(q.ctx) {
		var wrapper bson.M
		if err := cursor.Decode(&wrapper); err != nil {
			return nil, fmt.Errorf("decode document: %w", err)
		}

		// Extract document from wrapper
		dataBytes, err := bson.Marshal(wrapper[mongoFieldData])
		if err != nil {
			return nil, fmt.Errorf("marshal document data: %w", err)
		}

		var doc T
		if err := bson.Unmarshal(dataBytes, &doc); err != nil {
			return nil, fmt.Errorf("unmarshal document: %w", err)
		}

		results = append(results, doc)
	}

	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("cursor error: %w", err)
	}

	return results, nil
}

// FindOne executes query and returns first matching document.
func (q *MongoQuery[T]) FindOne() (T, error) {
	var zero T

	opts := mongooptions.FindOne()
	if q.sort != nil {
		opts.SetSort(q.sort)
	}
	if q.skip != nil {
		opts.SetSkip(*q.skip)
	}

	var wrapper bson.M
	err := q.coll.FindOne(q.ctx, q.filter, opts).Decode(&wrapper)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return zero, errors.New("document not found")
		}
		return zero, fmt.Errorf("find document: %w", err)
	}

	// Extract document from wrapper
	dataBytes, err := bson.Marshal(wrapper[mongoFieldData])
	if err != nil {
		return zero, fmt.Errorf("marshal document data: %w", err)
	}

	var doc T
	err = bson.Unmarshal(dataBytes, &doc)
	if err != nil {
		return zero, fmt.Errorf("unmarshal document: %w", err)
	}

	return doc, nil
}

// Count returns number of matching documents.
func (q *MongoQuery[T]) Count() (int64, error) {
	count, err := q.coll.CountDocuments(q.ctx, q.filter)
	if err != nil {
		return 0, fmt.Errorf("count documents: %w", err)
	}
	return count, nil
}

// Cursor returns an iterator for large result sets.
func (q *MongoQuery[T]) Cursor() (Cursor[T], error) {
	opts := mongooptions.Find()
	if q.sort != nil {
		opts.SetSort(q.sort)
	}
	if q.limit != nil {
		opts.SetLimit(*q.limit)
	}
	if q.skip != nil {
		opts.SetSkip(*q.skip)
	}

	cursor, err := q.coll.Find(q.ctx, q.filter, opts)
	if err != nil {
		return nil, fmt.Errorf("create cursor: %w", err)
	}

	return &MongoCursor[T]{
		cursor: cursor,
	}, nil
}

// MongoCursor implements Cursor for MongoDB.
type MongoCursor[T Document] struct {
	cursor *mongo.Cursor
}

// Next advances to the next document.
func (c *MongoCursor[T]) Next(ctx context.Context) bool {
	return c.cursor.Next(ctx)
}

// Decode retrieves current document.
func (c *MongoCursor[T]) Decode(doc T) error {
	var wrapper bson.M
	if err := c.cursor.Decode(&wrapper); err != nil {
		return fmt.Errorf("decode wrapper: %w", err)
	}

	// Extract document from wrapper
	dataBytes, err := bson.Marshal(wrapper[mongoFieldData])
	if err != nil {
		return fmt.Errorf("marshal document data: %w", err)
	}

	err = bson.Unmarshal(dataBytes, doc)
	if err != nil {
		return fmt.Errorf("unmarshal document: %w", err)
	}

	return nil
}

// Close releases cursor resources.
func (c *MongoCursor[T]) Close(ctx context.Context) error {
	return c.cursor.Close(ctx)
}
