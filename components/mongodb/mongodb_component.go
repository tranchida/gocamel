package mongodb

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	gocamel "gitlab.com/tranchida/gocamel"
)

// Header constants for MongoDB
const (
	CamelMongoDbDatabase    = "CamelMongoDbDatabase"    // Name of the database
	CamelMongoDbCollection  = "CamelMongoDbCollection"  // Name of the collection
	CamelMongoDbOperation   = "CamelMongoDbOperation"   // Operation: find, findOne, insert, insertOne, save, update, remove, count
	CamelMongoDbResultTotal = "CamelMongoDbResultTotal" // Total number of documents
	CamelMongoDbOid         = "CamelMongoDbOid"         // Document ObjectID
	CamelMongoDbCriteria    = "CamelMongoDbCriteria"    // Criteria/filter for search operations
	CamelMongoDbLimit       = "CamelMongoDbLimit"       // Limit for find operations
	CamelMongoDbSkip        = "CamelMongoDbSkip"        // Number of documents to skip
	CamelMongoDbSort        = "CamelMongoDbSort"        // Sort results (e.g. {"field": 1} or {"field": -1})

	CamelMongoDbResultsTruncated = "CamelMongoDbResultsTruncated" // true when find stopped at maxResults
)

// MongoDBConnection represents a shared MongoDB connection.
type MongoDBConnection struct {
	uri      string
	client   *mongo.Client
	database string
}

// MongoDBComponent manages MongoDB clients and creates endpoints.
type MongoDBComponent struct {
	mu          sync.RWMutex
	connections map[string]*MongoDBConnection // key = connection name
	defaultConn *MongoDBConnection
}

// NewMongoDBComponent creates a new MongoDBComponent instance.
func NewMongoDBComponent() *MongoDBComponent {
	return &MongoDBComponent{
		connections: make(map[string]*MongoDBConnection),
	}
}

// RegisterConnection registers a MongoDB connection with a name.
func (c *MongoDBComponent) RegisterConnection(name string, conn *MongoDBConnection) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.connections[name] = conn
}

// SetDefaultConnection sets the default connection to use.
func (c *MongoDBComponent) SetDefaultConnection(conn *MongoDBConnection) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.defaultConn = conn
}

func (c *MongoDBComponent) lookup(name string) (*MongoDBConnection, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if name != "" {
		if conn, ok := c.connections[name]; ok {
			return conn, true
		}
	}
	if c.defaultConn != nil {
		return c.defaultConn, true
	}
	return nil, false
}

// CreateEndpoint creates a MongoDBEndpoint from a URI.
//
// Format:
//
//	mongodb://connectionName?database=mydb&collection=mycoll&operation=find
//	mongodb://?connectionRef=myconn&database=mydb&collection=mycoll
//
// Options:
//
//	database            Name of the database
//	collection          Name of the collection
//	operation           Default operation (find, findOne, insert, insertOne, save, update, remove, count)
//	connectionRef       Name of a registered connection
//	allowDeleteAll      Permit remove with an empty filter (default false)
//	allowUpdateAll      Permit update with an empty filter (default false)
//	allowHeaderOverride true to let the CamelMongoDbDatabase, CamelMongoDbCollection,
//	                    CamelMongoDbOperation and CamelMongoDbCriteria headers replace
//	                    the corresponding URI configuration (default false)
//	maxResults          Maximum documents a find returns (default DefaultMaxResults,
//	                    0 = unlimited). Extra documents are not read: the find is
//	                    truncated, a warning is logged and CamelMongoDbResultsTruncated
//	                    is set to true
func (c *MongoDBComponent) CreateEndpoint(uri string) (gocamel.Endpoint, error) {
	u, err := gocamel.ParseURI(uri)
	if err != nil {
		return nil, fmt.Errorf("invalid mongodb URI: %w", err)
	}

	name := u.Host
	if name == "" && u.Opaque != "" {
		name = u.Opaque
	}
	if path := strings.TrimPrefix(u.Path, "/"); path != "" {
		name = path
	}
	if ref := gocamel.GetConfigValue(u, "connectionRef"); ref != "" {
		name = ref
	}

	conn, ok := c.lookup(name)
	if !ok {
		return nil, fmt.Errorf("connection '%s' not found: register it via RegisterConnection() or SetDefaultConnection()", name)
	}

	dbConfig := gocamel.GetConfigValue(u, "database")
	if dbConfig == "" {
		dbConfig = conn.database
	}

	collConfig := gocamel.GetConfigValue(u, "collection")
	operation := gocamel.GetConfigValue(u, "operation")
	allowDeleteAll := false
	if raw := gocamel.GetConfigValue(u, "allowDeleteAll"); raw != "" {
		allowDeleteAll, err = strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid allowDeleteAll value %q: %w", raw, err)
		}
	}
	allowUpdateAll := false
	if raw := gocamel.GetConfigValue(u, "allowUpdateAll"); raw != "" {
		allowUpdateAll, err = strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid allowUpdateAll value %q: %w", raw, err)
		}
	}

	allowHeaderOverride := false
	if v := gocamel.GetConfigValue(u, "allowHeaderOverride"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			allowHeaderOverride = b
		}
	}

	maxResults := int64(DefaultMaxResults)
	if raw := gocamel.GetConfigValue(u, "maxResults"); raw != "" {
		maxResults, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || maxResults < 0 {
			return nil, fmt.Errorf("invalid maxResults value %q: must be a non-negative integer", raw)
		}
	}

	return &MongoDBEndpoint{
		uri:                 uri,
		maxResults:          maxResults,
		connName:            name,
		dbConfig:            dbConfig,
		collConfig:          collConfig,
		operation:           operation,
		allowDeleteAll:      allowDeleteAll,
		allowUpdateAll:      allowUpdateAll,
		allowHeaderOverride: allowHeaderOverride,
		connection:          conn,
	}, nil
}

// MongoDBEndpoint represents a configured MongoDB endpoint.
type MongoDBEndpoint struct {
	uri                 string
	connName            string
	dbConfig            string
	collConfig          string
	operation           string
	allowDeleteAll      bool
	allowUpdateAll      bool
	allowHeaderOverride bool  // opt-in: CamelMongoDb* headers may override URI settings
	maxResults          int64 // cap on documents returned by find; 0 = unlimited
	connection          *MongoDBConnection
}

// DefaultMaxResults is the default cap on the documents a find returns, so a
// find without a limit on a large collection cannot exhaust memory.
const DefaultMaxResults = 10_000

// URI returns the endpoint URI.
func (e *MongoDBEndpoint) URI() string { return e.uri }

// CreateProducer creates a MongoDBProducer.
func (e *MongoDBEndpoint) CreateProducer() (gocamel.Producer, error) {
	return &MongoDBProducer{endpoint: e}, nil
}

// CreateConsumer returns an error: MongoDB component is producer-only.
func (e *MongoDBEndpoint) CreateConsumer(processor gocamel.Processor) (gocamel.Consumer, error) {
	return nil, fmt.Errorf("the mongodb component does not support consumers")
}

// MongoDBProducer executes MongoDB operations.
// Thread-safe: does not store mutable message-related state.
type MongoDBProducer struct {
	endpoint *MongoDBEndpoint
	client   *mongo.Client
}

// Start starts the producer and establishes the connection if needed.
func (p *MongoDBProducer) Start(ctx context.Context) error {
	p.client = p.endpoint.connection.client
	if p.client == nil {
		return fmt.Errorf("no MongoDB client available for connection '%s'", p.endpoint.connName)
	}
	return nil
}

// Stop stops the producer.
// Note: The MongoDB client is not closed as it is managed by MongoDBConnection.
func (p *MongoDBProducer) Stop() error {
	return nil
}

// getCollection returns the MongoDB collection to use.
// This method is thread-safe as it computes everything locally.
func (p *MongoDBProducer) getCollection(exchange *gocamel.Exchange) (*mongo.Collection, error) {
	// Start with the endpoint values
	dbName := p.endpoint.dbConfig
	if dbName == "" {
		dbName = p.endpoint.connection.database
	}
	collName := p.endpoint.collConfig

	// SECURITY: header overrides are honoured only when the endpoint opts in
	// with allowHeaderOverride=true. Enable it only where headers cannot be
	// influenced by untrusted input: they select which database and
	// collection are accessed.
	if p.endpoint.allowHeaderOverride {
		if v, ok := exchange.GetIn().GetHeader(CamelMongoDbDatabase); ok {
			if s, ok := v.(string); ok && s != "" {
				dbName = s
			}
		}
		if v, ok := exchange.GetIn().GetHeader(CamelMongoDbCollection); ok {
			if s, ok := v.(string); ok && s != "" {
				collName = s
			}
		}
	}

	if dbName == "" {
		return nil, fmt.Errorf("no database configured for MongoDB operation (use 'database' in the URI or the CamelMongoDbDatabase header)")
	}
	if collName == "" {
		return nil, fmt.Errorf("no collection configured for MongoDB operation (use 'collection' in the URI or the CamelMongoDbCollection header)")
	}

	return p.client.Database(dbName).Collection(collName), nil
}

// getOperation returns the operation to execute.
// The CamelMongoDbOperation header is honoured only when the endpoint opts in
// with allowHeaderOverride=true.
func (p *MongoDBProducer) getOperation(exchange *gocamel.Exchange) string {
	if p.endpoint.allowHeaderOverride {
		if v, ok := exchange.GetIn().GetHeader(CamelMongoDbOperation); ok {
			if s, ok := v.(string); ok && s != "" {
				return strings.ToLower(s)
			}
		}
	}
	return strings.ToLower(p.endpoint.operation)
}

// Send executes the MongoDB operation and fills the Out message with results.
func (p *MongoDBProducer) Send(exchange *gocamel.Exchange) error {
	ctx := exchange.Context
	if ctx == nil {
		ctx = context.Background()
	}

	operation := p.getOperation(exchange)
	if operation == "" {
		return fmt.Errorf("no MongoDB operation configured (use 'operation' in the URI or the CamelMongoDbOperation header)")
	}

	coll, err := p.getCollection(exchange)
	if err != nil {
		return err
	}

	switch operation {
	case "find":
		return p.execFind(ctx, exchange, coll)
	case "findone":
		return p.execFindOne(ctx, exchange, coll)
	case "insert":
		return p.execInsert(ctx, exchange, coll)
	case "insertone":
		return p.execInsertOne(ctx, exchange, coll)
	case "save":
		return p.execSave(ctx, exchange, coll)
	case "update":
		return p.execUpdate(ctx, exchange, coll)
	case "remove":
		return p.execRemove(ctx, exchange, coll)
	case "count":
		return p.execCount(ctx, exchange, coll)
	default:
		return fmt.Errorf("unsupported MongoDB operation: %s", operation)
	}
}

// execFind executes a multi-document search (find).
// Supports limit, skip, and sort via the CamelMongoDbLimit, CamelMongoDbSkip, CamelMongoDbSort headers.
func (p *MongoDBProducer) execFind(ctx context.Context, exchange *gocamel.Exchange, coll *mongo.Collection) error {
	filter, err := p.extractFilter(exchange)
	if err != nil {
		return err
	}

	opts := options.Find()

	// Limit support via header
	if v, ok := exchange.GetIn().GetHeader(CamelMongoDbLimit); ok {
		switch limit := v.(type) {
		case int:
			if limit > 0 {
				opts.SetLimit(int64(limit))
			}
		case int64:
			if limit > 0 {
				opts.SetLimit(limit)
			}
		}
	}

	// Skip support via header
	if v, ok := exchange.GetIn().GetHeader(CamelMongoDbSkip); ok {
		switch skip := v.(type) {
		case int:
			if skip > 0 {
				opts.SetSkip(int64(skip))
			}
		case int64:
			if skip > 0 {
				opts.SetSkip(skip)
			}
		}
	}

	// Sort support via header (map[string]interface{} or bson.M)
	if v, ok := exchange.GetIn().GetHeader(CamelMongoDbSort); ok {
		switch sort := v.(type) {
		case map[string]interface{}:
			opts.SetSort(sort)
		case bson.M:
			opts.SetSort(sort)
		}
	}

	// Ask the server for one document past the cap, enough to detect
	// truncation without fetching the rest of the collection.
	maxResults := p.endpoint.maxResults
	if maxResults > 0 && (opts.Limit == nil || *opts.Limit > maxResults) {
		opts.SetLimit(maxResults + 1)
	}

	cursor, err := coll.Find(ctx, filter, opts)
	if err != nil {
		return fmt.Errorf("find error: %w", err)
	}
	defer cursor.Close(ctx)

	results, truncated, err := drainCursor(ctx, cursor, maxResults)
	if err != nil {
		return err
	}
	if truncated {
		gocamel.LogWarn("mongodb: find results truncated at maxResults",
			"endpoint", p.endpoint.uri, "maxResults", maxResults)
	}

	exchange.GetOut().SetHeader(CamelMongoDbResultTotal, len(results))
	exchange.GetOut().SetHeader(CamelMongoDbResultsTruncated, truncated)
	exchange.GetOut().SetBody(results)
	return nil
}

// documentCursor is the subset of *mongo.Cursor used by drainCursor.
type documentCursor interface {
	Next(ctx context.Context) bool
	Decode(val interface{}) error
	Err() error
}

// drainCursor decodes at most maxResults documents (maxResults <= 0:
// unlimited) and reports whether more were available.
func drainCursor(ctx context.Context, cursor documentCursor, maxResults int64) ([]map[string]interface{}, bool, error) {
	results := []map[string]interface{}{}
	for cursor.Next(ctx) {
		if maxResults > 0 && int64(len(results)) >= maxResults {
			return results, true, nil
		}
		var doc map[string]interface{}
		if err := cursor.Decode(&doc); err != nil {
			return nil, false, fmt.Errorf("result decoding error: %w", err)
		}
		results = append(results, doc)
	}
	if err := cursor.Err(); err != nil {
		return nil, false, fmt.Errorf("result decoding error: %w", err)
	}
	return results, false, nil
}

// execFindOne executes a single-document search (findOne).
func (p *MongoDBProducer) execFindOne(ctx context.Context, exchange *gocamel.Exchange, coll *mongo.Collection) error {
	filter, err := p.extractFilter(exchange)
	if err != nil {
		return err
	}

	opts := options.FindOne()

	var result map[string]interface{}
	err = coll.FindOne(ctx, filter, opts).Decode(&result)
	if err == mongo.ErrNoDocuments {
		exchange.GetOut().SetBody(nil)
		return nil
	}
	if err != nil {
		return fmt.Errorf("findOne error: %w", err)
	}

	exchange.GetOut().SetBody(result)
	return nil
}

// execInsert executes an insertMany.
func (p *MongoDBProducer) execInsert(ctx context.Context, exchange *gocamel.Exchange, coll *mongo.Collection) error {
	body := exchange.GetIn().GetBody()
	docs, ok := body.([]interface{})
	if !ok {
		// Try converting []map[string]interface{}
		if maps, ok := body.([]map[string]interface{}); ok {
			docs = make([]interface{}, len(maps))
			for i, m := range maps {
				docs[i] = m
			}
		} else {
			return fmt.Errorf("insert: body must be []interface{} or []map[string]interface{}, got %T", body)
		}
	}

	if len(docs) == 0 {
		exchange.GetOut().SetHeader(CamelMongoDbResultTotal, 0)
		exchange.GetOut().SetBody([]interface{}{})
		return nil
	}

	result, err := coll.InsertMany(ctx, docs)
	if err != nil {
		return fmt.Errorf("insertMany error: %w", err)
	}

	exchange.GetOut().SetHeader(CamelMongoDbResultTotal, len(result.InsertedIDs))
	exchange.GetOut().SetBody(result.InsertedIDs)
	return nil
}

// execInsertOne executes an insertOne.
func (p *MongoDBProducer) execInsertOne(ctx context.Context, exchange *gocamel.Exchange, coll *mongo.Collection) error {
	body := exchange.GetIn().GetBody()
	doc, ok := body.(map[string]interface{})
	if !ok {
		return fmt.Errorf("insertOne: body must be map[string]interface{}, got %T", body)
	}

	result, err := coll.InsertOne(ctx, doc)
	if err != nil {
		return fmt.Errorf("insertOne error: %w", err)
	}

	exchange.GetOut().SetHeader(CamelMongoDbOid, result.InsertedID)
	exchange.GetOut().SetBody(result.InsertedID)
	return nil
}

// execSave executes a replaceOne with upsert=true (save/upsert).
// If the document has an _id, use replaceOne with upsert.
// Otherwise, use insertOne to create a new document.
func (p *MongoDBProducer) execSave(ctx context.Context, exchange *gocamel.Exchange, coll *mongo.Collection) error {
	body := exchange.GetIn().GetBody()
	doc, ok := body.(map[string]interface{})
	if !ok {
		return fmt.Errorf("save: body must be map[string]interface{}, got %T", body)
	}

	// Extract _id for the filter
	if id, ok := doc["_id"]; ok && id != nil {
		filter := bson.M{"_id": id}

		opts := options.Replace().SetUpsert(true)
		result, err := coll.ReplaceOne(ctx, filter, doc, opts)
		if err != nil {
			return fmt.Errorf("save (replaceOne) error: %w", err)
		}

		exchange.GetOut().SetHeader(CamelMongoDbResultTotal, result.ModifiedCount+result.UpsertedCount)
		if result.UpsertedID != nil {
			exchange.GetOut().SetHeader(CamelMongoDbOid, result.UpsertedID)
		}
		exchange.GetOut().SetBody(result)
		return nil
	}

	// No _id: it is a new document, insert it
	result, err := coll.InsertOne(ctx, doc)
	if err != nil {
		return fmt.Errorf("save (insertOne) error: %w", err)
	}

	exchange.GetOut().SetHeader(CamelMongoDbResultTotal, 1)
	exchange.GetOut().SetHeader(CamelMongoDbOid, result.InsertedID)
	exchange.GetOut().SetBody(result.InsertedID)
	return nil
}

// execUpdate executes an updateMany.
// Expects either:
// - A body of type map[string]interface{} with "filter" and "update" keys
// - A body with the update document and the filter in the CamelMongoDbCriteria header
func (p *MongoDBProducer) execUpdate(ctx context.Context, exchange *gocamel.Exchange, coll *mongo.Collection) error {
	body := exchange.GetIn().GetBody()

	// Get the filter — header takes priority (with allowHeaderOverride), then body
	var filter interface{}
	if p.endpoint.allowHeaderOverride {
		if crit, ok := exchange.GetIn().GetHeader(CamelMongoDbCriteria); ok {
			filter = crit
		}
	}

	// Get the update from the body
	var update interface{}

	switch v := body.(type) {
	case map[string]interface{}:
		// Check if body contains "filter" and "update"
		if rawFilter, exists := v["filter"]; exists {
			filter = rawFilter
		}
		if rawUpdate, exists := v["update"]; exists {
			update = rawUpdate
		}
		// If no "update" key, use the entire body
		if update == nil {
			update = v
		}
	case bson.M:
		// Same logic for bson.M
		if rawFilter, exists := v["filter"]; exists {
			filter = rawFilter
		}
		if rawUpdate, exists := v["update"]; exists {
			update = rawUpdate
		}
		if update == nil {
			update = v
		}
	default:
		// Use the body as-is for the update
		update = v
	}

	// Validation
	if isEmptyMongoFilter(filter) {
		if !p.endpoint.allowUpdateAll {
			return fmt.Errorf("update: empty filter rejected; set allowUpdateAll=true on the endpoint to update all documents")
		}
		if filter == nil {
			filter = bson.M{}
		}
	}
	if update == nil {
		return fmt.Errorf("update: no update document found (use the body or CamelMongoDbCriteria for the filter)")
	}

	result, err := coll.UpdateMany(ctx, filter, update)
	if err != nil {
		return fmt.Errorf("updateMany error: %w", err)
	}

	exchange.GetOut().SetHeader(CamelMongoDbResultTotal, result.ModifiedCount)
	exchange.GetOut().SetBody(result.ModifiedCount)
	return nil
}

// execRemove executes a deleteMany.
func (p *MongoDBProducer) execRemove(ctx context.Context, exchange *gocamel.Exchange, coll *mongo.Collection) error {
	// Safety: reject invalid filters with an explicit error rather than deleting everything
	filter, err := p.extractFilter(exchange)
	if err != nil {
		return fmt.Errorf("remove: invalid filter: %w", err)
	}
	if isEmptyMongoFilter(filter) && !p.endpoint.allowDeleteAll {
		return fmt.Errorf("remove: empty filter rejected; set allowDeleteAll=true on the endpoint to delete all documents")
	}

	result, err := coll.DeleteMany(ctx, filter)
	if err != nil {
		return fmt.Errorf("deleteMany error: %w", err)
	}

	exchange.GetOut().SetHeader(CamelMongoDbResultTotal, result.DeletedCount)
	exchange.GetOut().SetBody(result.DeletedCount)
	return nil
}

func isEmptyMongoFilter(filter interface{}) bool {
	if filter == nil {
		return true
	}
	switch v := filter.(type) {
	case map[string]interface{}:
		return len(v) == 0
	case bson.M:
		return len(v) == 0
	case bson.D:
		return len(v) == 0
	case string:
		return strings.TrimSpace(v) == ""
	default:
		return false
	}
}

// execCount executes a countDocuments.
func (p *MongoDBProducer) execCount(ctx context.Context, exchange *gocamel.Exchange, coll *mongo.Collection) error {
	filter, err := p.extractFilter(exchange)
	if err != nil {
		return err
	}

	count, err := coll.CountDocuments(ctx, filter)
	if err != nil {
		return fmt.Errorf("countDocuments error: %w", err)
	}

	exchange.GetOut().SetHeader(CamelMongoDbResultTotal, count)
	exchange.GetOut().SetBody(count)
	return nil
}

// extractFilter extracts the filter from the Exchange body.
// The CamelMongoDbCriteria header takes priority when the endpoint opts in
// with allowHeaderOverride=true; the body is used as fallback.
// If the body is nil or an empty string, bson.M{} (all documents) is returned.
// If the body is an invalid JSON string or an unsupported type, an error is returned.
func (p *MongoDBProducer) extractFilter(exchange *gocamel.Exchange) (interface{}, error) {
	// Criteria header takes priority (opt-in via allowHeaderOverride)
	if p.endpoint.allowHeaderOverride {
		if crit, ok := exchange.GetIn().GetHeader(CamelMongoDbCriteria); ok {
			return crit, nil
		}
	}

	body := exchange.GetIn().GetBody()
	if body == nil {
		return bson.M{}, nil
	}

	switch v := body.(type) {
	case map[string]interface{}:
		return v, nil
	case bson.M:
		return v, nil
	case bson.D:
		return v, nil
	case string:
		if v == "" {
			return bson.M{}, nil
		}
		// Try parsing the string as BSON JSON
		var res bson.M
		if err := bson.UnmarshalExtJSON([]byte(v), true, &res); err != nil {
			return nil, fmt.Errorf("cannot parse JSON filter: %w", err)
		}
		return res, nil
	default:
		return nil, fmt.Errorf("unsupported filter type: %T", v)
	}
}

// CreateMongoDBConnection creates a new MongoDB connection.
// This function must be called by the user before using the component.
//
// Example:
//
//	ctx := context.Background()
//	client, err := mongo.Connect(ctx, options.Client().ApplyURI("mongodb://localhost:27017"))
//	if err != nil { ... }
//	conn := &MongoDBConnection{
//		uri:      "mongodb://localhost:27017",
//		client:   client,
//		database: "mydb",
//	}
func CreateMongoDBConnection(client *mongo.Client, uri, database string) *MongoDBConnection {
	return &MongoDBConnection{
		uri:      uri,
		client:   client,
		database: database,
	}
}
