package gocamel

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
)

// En-têtes SQL posés ou consommés sur les Exchange.
// Correspondent aux en-têtes du composant Apache Camel SQL.
const (
	SqlQuery       = "CamelSqlQuery"       // Surcharge de la requête configurée dans l'URI
	SqlSelect      = "CamelSqlQuery"       // Alias: surcharge de la requête (body utilisable comme paramètres)
	SqlParameters  = "CamelSqlParameters"  // Paramètres positionnels ([]any)
	SqlRowCount    = "CamelSqlRowCount"    // Nombre de lignes affectées ou retournées
	SqlColumnNames = "CamelSqlColumnNames" // Noms des colonnes retournées pour un SELECT
)

// SQLOutputType contrôle la forme du body retourné par un SELECT.
type SQLOutputType string

const (
	SQLOutputSelectList SQLOutputType = "SelectList" // []map[string]any (défaut)
	SQLOutputSelectOne  SQLOutputType = "SelectOne"  // map[string]any (première ligne)
)

// SQLComponent gère les endpoints SQL et les datasources partagées.
//
// L'utilisateur enregistre ses *sql.DB via RegisterDataSource() ou SetDefaultDataSource()
// avant de construire une route, puis référence la datasource par son nom dans l'URI.
type SQLComponent struct {
	mu                sync.RWMutex
	dataSources       map[string]*sql.DB
	defaultDataSource *sql.DB
}

// NewSQLComponent crée une nouvelle instance de SQLComponent.
func NewSQLComponent() *SQLComponent {
	return &SQLComponent{dataSources: make(map[string]*sql.DB)}
}

// RegisterDataSource enregistre une connexion *sql.DB sous un nom.
func (c *SQLComponent) RegisterDataSource(name string, db *sql.DB) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dataSources[name] = db
}

// SetDefaultDataSource définit la connexion utilisée quand aucune datasource nommée ne correspond.
func (c *SQLComponent) SetDefaultDataSource(db *sql.DB) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.defaultDataSource = db
}

func (c *SQLComponent) lookup(name string) (*sql.DB, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if name != "" {
		if db, ok := c.dataSources[name]; ok {
			return db, true
		}
	}
	if c.defaultDataSource != nil {
		return c.defaultDataSource, true
	}
	return nil, false
}

// CreateEndpoint crée un SQLEndpoint depuis une URI.
//
// Format:
//
//	sql://datasourceName?query=SELECT+*+FROM+users+WHERE+id=?
//	sql://logical?dataSourceRef=datasourceName&query=...
//
// Options:
//
//	query          Requête SQL (obligatoire)
//	dataSourceRef  Nom d'une datasource enregistrée (sinon on utilise le host de l'URI)
//	batch          Active le mode batch : paramètres = [][]any (défaut: false)
//	outputType     SelectList (défaut) ou SelectOne
//	transacted     true pour englober la requête dans une transaction (défaut: false)
func (c *SQLComponent) CreateEndpoint(uri string) (Endpoint, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("URI sql invalide: %w", err)
	}

	name := u.Host
	if name == "" && u.Opaque != "" {
		name = u.Opaque
	}
	if path := strings.TrimPrefix(u.Path, "/"); path != "" {
		name = path
	}
	if ref := GetConfigValue(u, "dataSourceRef"); ref != "" {
		name = ref
	}

	query := GetConfigValue(u, "query")
	if query == "" {
		return nil, fmt.Errorf("option 'query' requise dans l'URI sql: %s", uri)
	}

	db, ok := c.lookup(name)
	if !ok {
		return nil, fmt.Errorf("datasource '%s' non trouvée: enregistrez-la via RegisterDataSource() ou SetDefaultDataSource()", name)
	}

	outputType := SQLOutputSelectList
	if v := GetConfigValue(u, "outputType"); v != "" {
		outputType = SQLOutputType(v)
	}

	batch := false
	if v := GetConfigValue(u, "batch"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			batch = b
		}
	}

	transacted := false
	if v := GetConfigValue(u, "transacted"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			transacted = b
		}
	}

	return &SQLEndpoint{
		uri:        uri,
		name:       name,
		query:      query,
		outputType: outputType,
		batch:      batch,
		transacted: transacted,
		db:         db,
	}, nil
}

// SQLEndpoint représente un endpoint SQL configuré.
type SQLEndpoint struct {
	uri        string
	name       string
	query      string
	outputType SQLOutputType
	batch      bool
	transacted bool
	db         *sql.DB
}

// URI retourne l'URI de l'endpoint.
func (e *SQLEndpoint) URI() string { return e.uri }

// CreateProducer crée un SQLProducer.
func (e *SQLEndpoint) CreateProducer() (Producer, error) {
	return &SQLProducer{endpoint: e}, nil
}

// CreateConsumer retourne une erreur : le composant SQL est producer-only pour l'instant.
func (e *SQLEndpoint) CreateConsumer(processor Processor) (Consumer, error) {
	return nil, fmt.Errorf("le composant sql ne supporte pas les consommateurs")
}

// SQLProducer exécute la requête configurée sur l'Exchange.
type SQLProducer struct {
	endpoint *SQLEndpoint
}

// Start ne fait rien : la connexion est gérée par l'utilisateur.
func (p *SQLProducer) Start(ctx context.Context) error { return nil }

// Stop ne fait rien : la connexion est gérée par l'utilisateur.
func (p *SQLProducer) Stop() error { return nil }

// Send exécute la requête SQL et remplit le message Out avec les résultats.
//
// Pour un SELECT : Out.Body = []map[string]any (ou map[string]any avec outputType=SelectOne).
// Pour INSERT/UPDATE/DELETE : Out.Body = nombre de lignes affectées (int64).
func (p *SQLProducer) Send(exchange *Exchange) error {
	query := p.endpoint.query
	if v, ok := exchange.GetIn().GetHeader(SqlQuery); ok {
		if s, ok := v.(string); ok && s != "" {
			query = s
		}
	}
	query = Interpolate(query, exchange)

	ctx := exchange.Context
	if ctx == nil {
		ctx = context.Background()
	}

	if p.endpoint.batch {
		return p.execBatch(ctx, exchange, query)
	}

	params := extractSQLParameters(exchange)
	isSelect := strings.HasPrefix(strings.TrimSpace(strings.ToUpper(query)), "SELECT")

	if p.endpoint.transacted {
		return p.sendTx(ctx, exchange, query, params, isSelect)
	}

	if isSelect {
		return p.execSelect(ctx, exchange, p.endpoint.db, query, params)
	}
	return p.execWrite(ctx, exchange, p.endpoint.db, query, params)
}

func (p *SQLProducer) sendTx(ctx context.Context, exchange *Exchange, query string, params []any, isSelect bool) error {
	tx, err := p.endpoint.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("erreur début transaction: %w", err)
	}

	var execErr error
	if isSelect {
		execErr = p.execSelect(ctx, exchange, tx, query, params)
	} else {
		execErr = p.execWrite(ctx, exchange, tx, query, params)
	}

	if execErr != nil {
		_ = tx.Rollback()
		return execErr
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("erreur commit transaction: %w", err)
	}
	return nil
}

// execBatch exécute la même requête pour chaque jeu de paramètres fourni via le body
// (attendu sous forme [][]any).
func (p *SQLProducer) execBatch(ctx context.Context, exchange *Exchange, query string) error {
	body := exchange.GetIn().GetBody()
	batch, ok := body.([][]any)
	if !ok {
		return fmt.Errorf("mode batch: le body doit être [][]any, reçu %T", body)
	}

	tx, err := p.endpoint.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("erreur début transaction batch: %w", err)
	}
	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("erreur préparation batch: %w", err)
	}
	defer stmt.Close()

	var total int64
	for i, params := range batch {
		result, err := stmt.ExecContext(ctx, params...)
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("erreur batch ligne %d: %w", i, err)
		}
		if n, err := result.RowsAffected(); err == nil {
			total += n
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("erreur commit batch: %w", err)
	}

	exchange.GetOut().SetHeader(SqlRowCount, total)
	exchange.GetOut().SetBody(total)
	return nil
}

type sqlQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

type sqlExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func (p *SQLProducer) execSelect(ctx context.Context, exchange *Exchange, q sqlQuerier, query string, params []any) error {
	rows, err := q.QueryContext(ctx, query, params...)
	if err != nil {
		return fmt.Errorf("erreur lors de l'exécution du SELECT: %w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return fmt.Errorf("erreur récupération colonnes: %w", err)
	}

	var results []map[string]any
	for rows.Next() {
		values := make([]any, len(cols))
		pointers := make([]any, len(cols))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return fmt.Errorf("erreur scan ligne: %w", err)
		}
		row := make(map[string]any, len(cols))
		for i, c := range cols {
			row[c] = normalizeSQLValue(values[i])
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("erreur itération lignes: %w", err)
	}

	exchange.GetOut().SetHeader(SqlColumnNames, cols)
	exchange.GetOut().SetHeader(SqlRowCount, len(results))

	switch p.endpoint.outputType {
	case SQLOutputSelectOne:
		if len(results) > 0 {
			exchange.GetOut().SetBody(results[0])
		} else {
			exchange.GetOut().SetBody(nil)
		}
	default:
		exchange.GetOut().SetBody(results)
	}
	return nil
}

func (p *SQLProducer) execWrite(ctx context.Context, exchange *Exchange, e sqlExecer, query string, params []any) error {
	result, err := e.ExecContext(ctx, query, params...)
	if err != nil {
		return fmt.Errorf("erreur lors de l'exécution SQL: %w", err)
	}
	affected, _ := result.RowsAffected()
	exchange.GetOut().SetHeader(SqlRowCount, affected)
	exchange.GetOut().SetBody(affected)
	return nil
}

// extractSQLParameters récupère les paramètres positionnels depuis l'Exchange.
// Priorité : header SqlParameters, puis body s'il s'agit d'un []any.
func extractSQLParameters(exchange *Exchange) []any {
	if v, ok := exchange.GetIn().GetHeader(SqlParameters); ok {
		if s, ok := v.([]any); ok {
			return s
		}
	}
	if body := exchange.GetIn().GetBody(); body != nil {
		if s, ok := body.([]any); ok {
			return s
		}
	}
	return nil
}

// normalizeSQLValue convertit les []byte (TEXT renvoyé en binaire par certains drivers)
// en string pour faciliter l'usage côté processeurs.
func normalizeSQLValue(v any) any {
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return v
}
