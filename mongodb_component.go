package gocamel

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Headers constants pour MongoDB
const (
	CamelMongoDbDatabase    = "CamelMongoDbDatabase"    // Nom de la base de données
	CamelMongoDbCollection  = "CamelMongoDbCollection"  // Nom de la collection
	CamelMongoDbOperation   = "CamelMongoDbOperation"   // Opération: find, findOne, insert, insertOne, save, update, remove, count
	CamelMongoDbResultTotal = "CamelMongoDbResultTotal" // Nombre total de documents
	CamelMongoDbOid         = "CamelMongoDbOid"         // ObjectID du document
	CamelMongoDbCriteria    = "CamelMongoDbCriteria"    // Critères/filtre pour les opérations de recherche
	CamelMongoDbLimit       = "CamelMongoDbLimit"       // Limite pour les opérations find
	CamelMongoDbSkip        = "CamelMongoDbSkip"        // Nombre de documents à sauter
	CamelMongoDbSort        = "CamelMongoDbSort"        // Tri des résultats (ex: {"field": 1} ou {"field": -1})
)

// MongoDBConnection représente une connexion MongoDB partagée.
type MongoDBConnection struct {
	uri      string
	client   *mongo.Client
	database string
}

// MongoDBComponent gère les clients MongoDB et crée des endpoints.
type MongoDBComponent struct {
	mu          sync.RWMutex
	connections map[string]*MongoDBConnection // key = connection name
	defaultConn *MongoDBConnection
}

// NewMongoDBComponent crée une nouvelle instance de MongoDBComponent.
func NewMongoDBComponent() *MongoDBComponent {
	return &MongoDBComponent{
		connections: make(map[string]*MongoDBConnection),
	}
}

// RegisterConnection enregistre une connexion MongoDB sous un nom.
func (c *MongoDBComponent) RegisterConnection(name string, conn *MongoDBConnection) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.connections[name] = conn
}

// SetDefaultConnection définit la connexion utilisée par défaut.
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

// CreateEndpoint crée un MongoDBEndpoint depuis une URI.
//
// Format:
//
//	mongodb://connectionName?database=mydb&collection=mycoll&operation=find
//	mongodb://?connectionRef=myconn&database=mydb&collection=mycoll
//
// Options:
//
//	database       Nom de la base de données
//	collection     Nom de la collection
//	operation      Opération par défaut (find, findOne, insert, insertOne, save, update, remove, count)
//	connectionRef  Nom d'une connexion enregistrée
func (c *MongoDBComponent) CreateEndpoint(uri string) (Endpoint, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("URI mongodb invalide: %w", err)
	}

	name := u.Host
	if name == "" && u.Opaque != "" {
		name = u.Opaque
	}
	if path := strings.TrimPrefix(u.Path, "/"); path != "" {
		name = path
	}
	if ref := GetConfigValue(u, "connectionRef"); ref != "" {
		name = ref
	}

	conn, ok := c.lookup(name)
	if !ok {
		return nil, fmt.Errorf("connection '%s' non trouvée: enregistrez-la via RegisterConnection() ou SetDefaultConnection()", name)
	}

	dbConfig := GetConfigValue(u, "database")
	if dbConfig == "" {
		dbConfig = conn.database
	}

	collConfig := GetConfigValue(u, "collection")
	operation := GetConfigValue(u, "operation")

	return &MongoDBEndpoint{
		uri:        uri,
		connName:   name,
		dbConfig:   dbConfig,
		collConfig: collConfig,
		operation:  operation,
		connection: conn,
	}, nil
}

// MongoDBEndpoint représente un endpoint MongoDB configuré.
type MongoDBEndpoint struct {
	uri        string
	connName   string
	dbConfig   string
	collConfig string
	operation  string
	connection *MongoDBConnection
}

// URI retourne l'URI de l'endpoint.
func (e *MongoDBEndpoint) URI() string { return e.uri }

// CreateProducer crée un MongoDBProducer.
func (e *MongoDBEndpoint) CreateProducer() (Producer, error) {
	return &MongoDBProducer{endpoint: e}, nil
}

// CreateConsumer retourne une erreur : le composant MongoDB est producer-only.
func (e *MongoDBEndpoint) CreateConsumer(processor Processor) (Consumer, error) {
	return nil, fmt.Errorf("le composant mongodb ne supporte pas les consommateurs")
}

// MongoDBProducer exécute les opérations MongoDB.
// Thread-safe: ne stocke pas d'état mutable lié aux messages.
type MongoDBProducer struct {
	endpoint *MongoDBEndpoint
	client   *mongo.Client
}

// Start démarre le producteur et établit la connexion si nécessaire.
func (p *MongoDBProducer) Start(ctx context.Context) error {
	p.client = p.endpoint.connection.client
	if p.client == nil {
		return fmt.Errorf("aucun client MongoDB disponible pour la connexion '%s'", p.endpoint.connName)
	}
	return nil
}

// Stop arrête le producteur.
// Note: Le client MongoDB n'est pas fermé car il est géré par MongoDBConnection.
func (p *MongoDBProducer) Stop() error {
	return nil
}

// getCollection retourne la collection MongoDB à utiliser.
// Cette méthode est thread-safe car elle calcule tout localement.
func (p *MongoDBProducer) getCollection(exchange *Exchange) (*mongo.Collection, error) {
	// Commence par les valeurs de l'endpoint
	dbName := p.endpoint.dbConfig
	if dbName == "" {
		dbName = p.endpoint.connection.database
	}
	collName := p.endpoint.collConfig

	// Override via headers si présents
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

	if dbName == "" {
		return nil, fmt.Errorf("aucune base de données configurée pour l'opération MongoDB (utilisez 'database' dans l'URI ou le header CamelMongoDbDatabase)")
	}
	if collName == "" {
		return nil, fmt.Errorf("aucune collection configurée pour l'opération MongoDB (utilisez 'collection' dans l'URI ou le header CamelMongoDbCollection)")
	}

	return p.client.Database(dbName).Collection(collName), nil
}

// getOperation retourne l'opération à exécuter.
func (p *MongoDBProducer) getOperation(exchange *Exchange) string {
	// Priorité au header
	if v, ok := exchange.GetIn().GetHeader(CamelMongoDbOperation); ok {
		if s, ok := v.(string); ok && s != "" {
			return strings.ToLower(s)
		}
	}
	return strings.ToLower(p.endpoint.operation)
}

// Send exécute l'opération MongoDB et remplit le message Out avec les résultats.
func (p *MongoDBProducer) Send(exchange *Exchange) error {
	ctx := exchange.Context
	if ctx == nil {
		ctx = context.Background()
	}

	operation := p.getOperation(exchange)
	if operation == "" {
		return fmt.Errorf("aucune opération MongoDB configurée (utilisez 'operation' dans l'URI ou le header CamelMongoDbOperation)")
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
		return fmt.Errorf("opération MongoDB non supportée: %s", operation)
	}
}

// execFind exécute une recherche multiple (find).
// Supporte limit, skip, sort via headers CamelMongoDbLimit, CamelMongoDbSkip, CamelMongoDbSort.
func (p *MongoDBProducer) execFind(ctx context.Context, exchange *Exchange, coll *mongo.Collection) error {
	filter, err := p.extractFilter(exchange)
	if err != nil {
		return err
	}

	opts := options.Find()

	// Support pour limit via header
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

	// Support pour skip via header
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

	// Support pour sort via header (map[string]interface{} ou bson.M)
	if v, ok := exchange.GetIn().GetHeader(CamelMongoDbSort); ok {
		switch sort := v.(type) {
		case map[string]interface{}:
			opts.SetSort(sort)
		case bson.M:
			opts.SetSort(sort)
		}
	}

	cursor, err := coll.Find(ctx, filter, opts)
	if err != nil {
		return fmt.Errorf("erreur lors du find: %w", err)
	}
	defer cursor.Close(ctx)

	var results []map[string]interface{}
	if err := cursor.All(ctx, &results); err != nil {
		return fmt.Errorf("erreur lors du décodage des résultats: %w", err)
	}

	exchange.GetOut().SetHeader(CamelMongoDbResultTotal, len(results))
	exchange.GetOut().SetBody(results)
	return nil
}

// execFindOne exécute une recherche unique (findOne).
func (p *MongoDBProducer) execFindOne(ctx context.Context, exchange *Exchange, coll *mongo.Collection) error {
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
		return fmt.Errorf("erreur lors du findOne: %w", err)
	}

	exchange.GetOut().SetBody(result)
	return nil
}

// execInsert exécute un insertMany.
func (p *MongoDBProducer) execInsert(ctx context.Context, exchange *Exchange, coll *mongo.Collection) error {
	body := exchange.GetIn().GetBody()
	docs, ok := body.([]interface{})
	if !ok {
		// Essaye de convertir []map[string]interface{}
		if maps, ok := body.([]map[string]interface{}); ok {
			docs = make([]interface{}, len(maps))
			for i, m := range maps {
				docs[i] = m
			}
		} else {
			return fmt.Errorf("insert: le body doit être []interface{} ou []map[string]interface{}, reçu %T", body)
		}
	}

	if len(docs) == 0 {
		exchange.GetOut().SetHeader(CamelMongoDbResultTotal, 0)
		exchange.GetOut().SetBody([]interface{}{})
		return nil
	}

	result, err := coll.InsertMany(ctx, docs)
	if err != nil {
		return fmt.Errorf("erreur lors de l'insertMany: %w", err)
	}

	exchange.GetOut().SetHeader(CamelMongoDbResultTotal, len(result.InsertedIDs))
	exchange.GetOut().SetBody(result.InsertedIDs)
	return nil
}

// execInsertOne exécute un insertOne.
func (p *MongoDBProducer) execInsertOne(ctx context.Context, exchange *Exchange, coll *mongo.Collection) error {
	body := exchange.GetIn().GetBody()
	doc, ok := body.(map[string]interface{})
	if !ok {
		return fmt.Errorf("insertOne: le body doit être map[string]interface{}, reçu %T", body)
	}

	result, err := coll.InsertOne(ctx, doc)
	if err != nil {
		return fmt.Errorf("erreur lors de l'insertOne: %w", err)
	}

	exchange.GetOut().SetHeader(CamelMongoDbOid, result.InsertedID)
	exchange.GetOut().SetBody(result.InsertedID)
	return nil
}

// execSave exécute un replaceOne avec upsert=true (save/upsert).
// Si le document possède un _id, utilise replaceOne avec upsert.
// Sinon, utilise insertOne pour créer un nouveau document.
func (p *MongoDBProducer) execSave(ctx context.Context, exchange *Exchange, coll *mongo.Collection) error {
	body := exchange.GetIn().GetBody()
	doc, ok := body.(map[string]interface{})
	if !ok {
		return fmt.Errorf("save: le body doit être map[string]interface{}, reçu %T", body)
	}

	// Extrait le _id pour le filtre
	if id, ok := doc["_id"]; ok && id != nil {
		filter := bson.M{"_id": id}
		delete(doc, "_id")

		opts := options.Replace().SetUpsert(true)
		result, err := coll.ReplaceOne(ctx, filter, doc, opts)
		if err != nil {
			return fmt.Errorf("erreur lors du save (replaceOne): %w", err)
		}

		exchange.GetOut().SetHeader(CamelMongoDbResultTotal, result.ModifiedCount+result.UpsertedCount)
		if result.UpsertedID != nil {
			exchange.GetOut().SetHeader(CamelMongoDbOid, result.UpsertedID)
		}
		exchange.GetOut().SetBody(result)
		return nil
	}

	// Pas d'_id: c'est un nouveau document, insère-le
	result, err := coll.InsertOne(ctx, doc)
	if err != nil {
		return fmt.Errorf("erreur lors du save (insertOne): %w", err)
	}

	exchange.GetOut().SetHeader(CamelMongoDbResultTotal, 1)
	exchange.GetOut().SetHeader(CamelMongoDbOid, result.InsertedID)
	exchange.GetOut().SetBody(result.InsertedID)
	return nil
}

// execUpdate exécute un updateMany.
// Attend soit:
// - Un body de type map[string]interface{} avec "filter" et "update"
// - Un body avec le document "update" et le filtre dans le header CamelMongoDbCriteria
func (p *MongoDBProducer) execUpdate(ctx context.Context, exchange *Exchange, coll *mongo.Collection) error {
	body := exchange.GetIn().GetBody()

	// Récupère le filtre - priorité au header, sinon dans le body
	var filter interface{}
	if crit, ok := exchange.GetIn().GetHeader(CamelMongoDbCriteria); ok {
		filter = crit
	}

	// Récupère l'update depuis le body
	var update interface{}

	switch v := body.(type) {
	case map[string]interface{}:
		// Vérifie si le body contient "filter" et "update"
		if rawFilter, exists := v["filter"]; exists {
			filter = rawFilter
		}
		if rawUpdate, exists := v["update"]; exists {
			update = rawUpdate
		}
		// Si pas de "update" clé, utilise le body entier
		if update == nil {
			update = v
		}
	case bson.M:
		// Même logique pour bson.M
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
		// Utilise le body tel quel comme update
		update = v
	}

	// Vérifications
	if filter == nil {
		filter = bson.M{}
	}
	if update == nil {
		return fmt.Errorf("update: aucun document d'update trouvé (utilisez le body ou CamelMongoDbCriteria pour le filtre)")
	}

	result, err := coll.UpdateMany(ctx, filter, update)
	if err != nil {
		return fmt.Errorf("erreur lors de l'updateMany: %w", err)
	}

	exchange.GetOut().SetHeader(CamelMongoDbResultTotal, result.ModifiedCount)
	exchange.GetOut().SetBody(result.ModifiedCount)
	return nil
}

// execRemove exécute un deleteMany.
func (p *MongoDBProducer) execRemove(ctx context.Context, exchange *Exchange, coll *mongo.Collection) error {
	// Sécurité : refuser les filtres avec une erreur explicite au lieu de supprimer tout
	filter, err := p.extractFilter(exchange)
	if err != nil {
		return fmt.Errorf("remove: filtre invalide: %w", err)
	}

	result, err := coll.DeleteMany(ctx, filter)
	if err != nil {
		return fmt.Errorf("erreur lors du deleteMany: %w", err)
	}

	exchange.GetOut().SetHeader(CamelMongoDbResultTotal, result.DeletedCount)
	exchange.GetOut().SetBody(result.DeletedCount)
	return nil
}

// execCount exécute un countDocuments.
func (p *MongoDBProducer) execCount(ctx context.Context, exchange *Exchange, coll *mongo.Collection) error {
	filter, err := p.extractFilter(exchange)
	if err != nil {
		return err
	}

	count, err := coll.CountDocuments(ctx, filter)
	if err != nil {
		return fmt.Errorf("erreur lors du countDocuments: %w", err)
	}

	exchange.GetOut().SetHeader(CamelMongoDbResultTotal, count)
	exchange.GetOut().SetBody(count)
	return nil
}

// extractFilter extrait le filtre depuis le body de l'Exchange.
// Priority au header CamelMongoDbCriteria s'il existe, sinon utilise le body.
// Si le body est nil ou string vide, retourne bson.M{} (tous les documents).
// Si le body est une string JSON invalide ou un type non supporté, retourne une erreur.
func (p *MongoDBProducer) extractFilter(exchange *Exchange) (interface{}, error) {
	// Priorité au header de critère s'il existe
	if crit, ok := exchange.GetIn().GetHeader(CamelMongoDbCriteria); ok {
		return crit, nil
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
		// Essaie de parser la string comme JSON BSON
		var res bson.M
		if err := bson.UnmarshalExtJSON([]byte(v), true, &res); err != nil {
			return nil, fmt.Errorf("impossible de parser le filtre JSON: %w", err)
		}
		return res, nil
	default:
		return nil, fmt.Errorf("type de filtre non supporté: %T", v)
	}
}

// CreateMongoDBConnection crée une nouvelle connexion MongoDB.
// Cette fonction doit être appelée par l'utilisateur avant d'utiliser le composant.
//
// Exemple:
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
