package docstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	mongooptions "go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"

	"github.com/zarldev/zarlmono/zkit/options"
)

const mongoFieldID = "_id"

// MongoDatabase owns a MongoDB client connection.
type MongoDatabase struct {
	client   *mongo.Client
	database *mongo.Database
	config   mongoConfig
}

type mongoConfig struct {
	uri         string
	name        string
	timeout     time.Duration
	maxPoolSize uint64
	minPoolSize uint64
}

// ConnectMongo opens and verifies a MongoDB database connection.
func ConnectMongo(ctx context.Context, opts ...options.Option[MongoDatabase]) (*MongoDatabase, error) {
	db := &MongoDatabase{config: mongoConfig{uri: "mongodb://localhost:27017", name: "docstore", timeout: 10 * time.Second, maxPoolSize: 100, minPoolSize: 10}}
	for _, opt := range opts {
		opt(db)
	}
	client, err := mongo.Connect(ctx, mongooptions.Client().ApplyURI(db.config.uri).SetConnectTimeout(db.config.timeout).SetMaxPoolSize(db.config.maxPoolSize).SetMinPoolSize(db.config.minPoolSize))
	if err != nil {
		return nil, fmt.Errorf("connect MongoDB: %w", err)
	}
	if err := client.Ping(ctx, readpref.Primary()); err != nil {
		if closeErr := client.Disconnect(ctx); closeErr != nil {
			return nil, fmt.Errorf("ping MongoDB: %w; disconnect: %w", err, closeErr)
		}
		return nil, fmt.Errorf("ping MongoDB: %w", err)
	}
	db.client, db.database = client, client.Database(db.config.name)
	return db, nil
}

// WithMongoURI sets the MongoDB connection URI.
func WithMongoURI(uri string) options.Option[MongoDatabase] {
	return func(db *MongoDatabase) { db.config.uri = uri }
}

// WithDatabaseName sets the MongoDB database name.
func WithDatabaseName(name string) options.Option[MongoDatabase] {
	return func(db *MongoDatabase) { db.config.name = name }
}

// WithMongoTimeout sets the MongoDB connection timeout.
func WithMongoTimeout(timeout time.Duration) options.Option[MongoDatabase] {
	return func(db *MongoDatabase) { db.config.timeout = timeout }
}

// WithPoolSize sets the MongoDB connection-pool bounds.
func WithPoolSize(minPool, maxPool uint64) options.Option[MongoDatabase] {
	return func(db *MongoDatabase) { db.config.minPoolSize, db.config.maxPoolSize = minPool, maxPool }
}

// Collection returns a collection from this database.
func (db *MongoDatabase) Collection(name string) *mongo.Collection {
	return db.database.Collection(name)
}

// Close closes the MongoDB client owned by db.
func (db *MongoDatabase) Close(ctx context.Context) error {
	if db.client == nil {
		return nil
	}
	return db.client.Disconnect(ctx)
}

// MongoStore stores independent document snapshots in a MongoDB collection.
type MongoStore[T Value[T]] struct{ collection *mongo.Collection }

// NewMongoStore returns a concrete store backed by collection. The caller owns collection's client.
func NewMongoStore[T Value[T]](collection *mongo.Collection) *MongoStore[T] {
	return &MongoStore[T]{collection: collection}
}

type mongoRecord struct {
	ID    ID       `bson:"_id"`
	Value bson.Raw `bson:"value"`
}

// Create stores record when its identity is not already present.
func (s *MongoStore[T]) Create(ctx context.Context, record Record[T]) (Record[T], error) {
	if err := ensureContext(ctx); err != nil {
		return Record[T]{}, err
	}
	if record.ID == "" {
		return Record[T]{}, ErrInvalidRecord
	}
	record = recordClone(record)
	stored, err := encodeMongoRecord(record)
	if err != nil {
		return Record[T]{}, err
	}
	if _, err := s.collection.InsertOne(ctx, stored); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return Record[T]{}, fmt.Errorf("create %q: %w", record.ID, ErrConflict)
		}
		return Record[T]{}, fmt.Errorf("create %q: %w", record.ID, err)
	}
	return recordClone(record), nil
}

// Read returns an independent snapshot for id.
func (s *MongoStore[T]) Read(ctx context.Context, id ID) (Record[T], error) {
	if err := ensureContext(ctx); err != nil {
		return Record[T]{}, err
	}
	var stored mongoRecord
	if err := s.collection.FindOne(ctx, bson.D{{Key: mongoFieldID, Value: id}}).Decode(&stored); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return Record[T]{}, fmt.Errorf("read %q: %w", id, ErrNotFound)
		}
		return Record[T]{}, fmt.Errorf("read %q: %w", id, err)
	}
	record, err := decodeMongoRecord[T](stored)
	if err != nil {
		return Record[T]{}, fmt.Errorf("read %q: %w", id, err)
	}
	return recordClone(record), nil
}

// Replace atomically replaces the value for an existing record.
func (s *MongoStore[T]) Replace(ctx context.Context, record Record[T]) (Record[T], error) {
	if err := ensureContext(ctx); err != nil {
		return Record[T]{}, err
	}
	if record.ID == "" {
		return Record[T]{}, ErrInvalidRecord
	}
	record = recordClone(record)
	stored, err := encodeMongoRecord(record)
	if err != nil {
		return Record[T]{}, err
	}
	result, err := s.collection.ReplaceOne(ctx, bson.D{{Key: mongoFieldID, Value: record.ID}}, stored)
	if err != nil {
		return Record[T]{}, fmt.Errorf("replace %q: %w", record.ID, err)
	}
	if result.MatchedCount == 0 {
		return Record[T]{}, fmt.Errorf("replace %q: %w", record.ID, ErrNotFound)
	}
	return recordClone(record), nil
}

// Put creates or replaces record and returns the stored snapshot.
func (s *MongoStore[T]) Put(ctx context.Context, record Record[T]) (Record[T], error) {
	if err := ensureContext(ctx); err != nil {
		return Record[T]{}, err
	}
	if record.ID == "" {
		return Record[T]{}, ErrInvalidRecord
	}
	record = recordClone(record)
	stored, err := encodeMongoRecord(record)
	if err != nil {
		return Record[T]{}, err
	}
	if _, err := s.collection.ReplaceOne(ctx, bson.D{{Key: mongoFieldID, Value: record.ID}}, stored, mongooptions.Replace().SetUpsert(true)); err != nil {
		return Record[T]{}, fmt.Errorf("put %q: %w", record.ID, err)
	}
	return recordClone(record), nil
}

// Delete removes id.
func (s *MongoStore[T]) Delete(ctx context.Context, id ID) error {
	if err := ensureContext(ctx); err != nil {
		return err
	}
	result, err := s.collection.DeleteOne(ctx, bson.D{{Key: mongoFieldID, Value: id}})
	if err != nil {
		return fmt.Errorf("delete %q: %w", id, err)
	}
	if result.DeletedCount == 0 {
		return fmt.Errorf("delete %q: %w", id, ErrNotFound)
	}
	return nil
}

// List returns ID-ordered independent snapshots within page.
func (s *MongoStore[T]) List(ctx context.Context, page Page) ([]Record[T], error) {
	if err := ensureContext(ctx); err != nil {
		return nil, err
	}
	if !page.Valid() {
		return nil, ErrInvalidRecord
	}
	opts := mongooptions.Find().SetSort(bson.D{{Key: mongoFieldID, Value: 1}}).SetSkip(int64(page.Offset))
	if page.Limit > 0 {
		opts.SetLimit(int64(page.Limit))
	}
	cursor, err := s.collection.Find(ctx, bson.D{}, opts)
	if err != nil {
		return nil, fmt.Errorf("list documents: %w", err)
	}
	defer cursor.Close(ctx)
	var records []Record[T]
	for cursor.Next(ctx) {
		var stored mongoRecord
		if err := cursor.Decode(&stored); err != nil {
			return nil, fmt.Errorf("decode document: %w", err)
		}
		record, err := decodeMongoRecord[T](stored)
		if err != nil {
			return nil, fmt.Errorf("decode document: %w", err)
		}
		records = append(records, recordClone(record))
	}
	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("list documents: %w", err)
	}
	return records, nil
}

// Count reports the number of records.
func (s *MongoStore[T]) Count(ctx context.Context) (int, error) {
	if err := ensureContext(ctx); err != nil {
		return 0, err
	}
	count, err := s.collection.CountDocuments(ctx, bson.D{})
	if err != nil {
		return 0, fmt.Errorf("count documents: %w", err)
	}
	return int(count), nil
}
func encodeMongoRecord[T Value[T]](record Record[T]) (mongoRecord, error) {
	value, err := bson.Marshal(record.Value)
	if err != nil {
		return mongoRecord{}, fmt.Errorf("encode document: %w", err)
	}
	return mongoRecord{ID: record.ID, Value: value}, nil
}
func decodeMongoRecord[T Value[T]](stored mongoRecord) (Record[T], error) {
	var value T
	if err := bson.Unmarshal(stored.Value, &value); err != nil {
		return Record[T]{}, err
	}
	return Record[T]{ID: stored.ID, Value: value}, nil
}
