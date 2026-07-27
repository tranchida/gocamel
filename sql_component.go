package gocamel

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// SQL headers set or consumed on Exchange objects.
// These correspond to the headers of the Apache Camel SQL component.
const (
	// SqlQuery overrides the query configured in the URI. Honoured ONLY when
	// the endpoint sets allowHeaderOverride=true — see SQLProducer.Send.
	SqlQuery = "CamelSqlQuery"
	// SqlSelect is a deprecated alias of SqlQuery, kept for source
	// compatibility. Both names resolve to the same header.
	SqlSelect      = SqlQuery
	SqlParameters  = "CamelSqlParameters"  // Positional parameters ([]any)
	SqlRowCount    = "CamelSqlRowCount"    // Number of rows affected or returned
	SqlColumnNames = "CamelSqlColumnNames" // Column names returned by a SELECT
)

// SQLOutputType controls the shape of the body returned by a SELECT.
type SQLOutputType string

const (
	SQLOutputSelectList SQLOutputType = "SelectList" // []map[string]any (default)
	SQLOutputSelectOne  SQLOutputType = "SelectOne"  // map[string]any (first row)
)

// SQLComponent manages SQL endpoints and shared datasources.
//
// The user registers their *sql.DB via RegisterDataSource() or SetDefaultDataSource()
// before building a route, then references the datasource by its name in the URI.
type SQLComponent struct {
	mu                sync.RWMutex
	dataSources       map[string]*sql.DB
	defaultDataSource *sql.DB
}

// NewSQLComponent creates a new SQLComponent instance.
func NewSQLComponent() *SQLComponent {
	return &SQLComponent{dataSources: make(map[string]*sql.DB)}
}

// RegisterDataSource registers a *sql.DB connection under a name.
func (c *SQLComponent) RegisterDataSource(name string, db *sql.DB) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dataSources[name] = db
}

// SetDefaultDataSource sets the connection used when no named datasource matches.
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

// CreateEndpoint creates an SQLEndpoint from a URI.
//
// Format:
//
//	sql://datasourceName?query=SELECT+*+FROM+users+WHERE+id=?
//	sql://logical?dataSourceRef=datasourceName&query=...
//
// Options:
//
//	query               SQL query (required)
//	dataSourceRef       Name of a registered datasource (otherwise uses the host of the URI)
//	batch               Enables batch mode: parameters = [][]any (default: false)
//	outputType          SelectList (default) or SelectOne
//	transacted          true to wrap the query in a transaction (default: false)
//	allowHeaderOverride true to let the CamelSqlQuery header replace the
//	                    configured query (default: false — see SQLProducer.Send)
func (c *SQLComponent) CreateEndpoint(uri string) (Endpoint, error) {
	u, err := ParseURI(uri)
	if err != nil {
		return nil, fmt.Errorf("invalid sql URI: %w", err)
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
		return nil, fmt.Errorf("required option 'query' missing in sql URI: %s", uri)
	}

	db, ok := c.lookup(name)
	if !ok {
		return nil, fmt.Errorf("datasource '%s' not found: register it via RegisterDataSource() or SetDefaultDataSource()", name)
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

	allowHeaderOverride := false
	if v := GetConfigValue(u, "allowHeaderOverride"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			allowHeaderOverride = b
		}
	}

	return &SQLEndpoint{
		uri:                 uri,
		name:                name,
		query:               query,
		outputType:          outputType,
		batch:               batch,
		transacted:          transacted,
		allowHeaderOverride: allowHeaderOverride,
		db:                  db,
	}, nil
}

// SQLEndpoint represents a configured SQL endpoint.
type SQLEndpoint struct {
	uri                 string
	name                string
	query               string
	outputType          SQLOutputType
	batch               bool
	transacted          bool
	allowHeaderOverride bool // opt-in: the CamelSqlQuery header may replace the query
	db                  *sql.DB
}

// URI returns the endpoint URI.
func (e *SQLEndpoint) URI() string { return e.uri }

// CreateProducer creates a SQLProducer.
func (e *SQLEndpoint) CreateProducer() (Producer, error) {
	return &SQLProducer{endpoint: e}, nil
}

// CreateConsumer returns an error: the sql component is producer-only for now.
func (e *SQLEndpoint) CreateConsumer(processor Processor) (Consumer, error) {
	return nil, fmt.Errorf("the sql component does not support consumers")
}

// SQLProducer executes the configured query on the Exchange.
type SQLProducer struct {
	endpoint *SQLEndpoint
}

// Start does nothing: the connection is managed by the user.
func (p *SQLProducer) Start(ctx context.Context) error { return nil }

// Stop does nothing: the connection is managed by the user.
func (p *SQLProducer) Stop() error { return nil }

// Send executes the SQL query and fills the Out message with the results.
//
// For a SELECT: Out.Body = []map[string]any (or map[string]any with outputType=SelectOne).
// For INSERT/UPDATE/DELETE: Out.Body = number of affected rows (int64).
func (p *SQLProducer) Send(exchange *Exchange) error {
	// SECURITY: the query is trusted configuration. It is never interpolated
	// with Exchange data — interpolating ${header.X} into the SQL string would
	// defeat parameterised queries and enable SQL injection. Bind dynamic
	// values via the CamelSqlParameters header (positional '?' placeholders)
	// or via the body when it is a []any.
	//
	// The CamelSqlQuery header replaces the statement wholesale, so it is
	// honoured only when the endpoint opts in with allowHeaderOverride=true.
	// Enable it only where headers cannot be influenced by untrusted input:
	// any component that maps external metadata onto headers would otherwise
	// hand an attacker full control of the statement.
	query := p.endpoint.query
	if p.endpoint.allowHeaderOverride {
		if v, ok := exchange.GetIn().GetHeader(SqlQuery); ok {
			if s, ok := v.(string); ok && s != "" {
				query = s
			}
		}
	}

	ctx := exchange.Context
	if ctx == nil {
		ctx = context.Background()
	}

	if err := validateSQLQuery(query); err != nil {
		return fmt.Errorf("sql: query validation failed: %w", err)
	}

	if p.endpoint.batch {
		return p.execBatch(ctx, exchange, query)
	}

	params := extractSQLParameters(exchange)
	isSelect := returnsRows(query)

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
		return fmt.Errorf("transaction begin error: %w", err)
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
		return fmt.Errorf("transaction commit error: %w", err)
	}
	return nil
}

// execBatch executes the same query for each set of parameters provided via the body
// (expected as [][]any).
func (p *SQLProducer) execBatch(ctx context.Context, exchange *Exchange, query string) error {
	body := exchange.GetIn().GetBody()
	batch, ok := body.([][]any)
	if !ok {
		return fmt.Errorf("batch mode: body must be [][]any, got %T", body)
	}

	tx, err := p.endpoint.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("batch transaction begin error: %w", err)
	}
	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("batch prepare error: %w", err)
	}
	defer stmt.Close()

	var total int64
	for i, params := range batch {
		result, err := stmt.ExecContext(ctx, params...)
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("batch error at row %d: %w", i, err)
		}
		if n, err := result.RowsAffected(); err == nil {
			total += n
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("batch commit error: %w", err)
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
		return fmt.Errorf("SELECT execution error: %w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return fmt.Errorf("column retrieval error: %w", err)
	}

	var results []map[string]any
	for rows.Next() {
		values := make([]any, len(cols))
		pointers := make([]any, len(cols))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return fmt.Errorf("row scan error: %w", err)
		}
		row := make(map[string]any, len(cols))
		for i, c := range cols {
			row[c] = normalizeSQLValue(values[i])
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("row iteration error: %w", err)
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
		return fmt.Errorf("SQL execution error: %w", err)
	}
	affected, _ := result.RowsAffected()
	exchange.GetOut().SetHeader(SqlRowCount, affected)
	exchange.GetOut().SetBody(affected)
	return nil
}

// rowReturningKeywords are the leading keywords of statements that produce a
// result set rather than an affected-row count.
var rowReturningKeywords = map[string]bool{
	"SELECT":   true,
	"WITH":     true, // common table expression, e.g. WITH t AS (...) SELECT ...
	"VALUES":   true,
	"TABLE":    true,
	"SHOW":     true,
	"EXPLAIN":  true,
	"DESCRIBE": true,
	"DESC":     true,
	"PRAGMA":   true,
}

// returnsRows reports whether the statement should be run through
// QueryContext (result set) rather than ExecContext (affected rows).
//
// Checking only for a "SELECT" prefix misclassified any statement introduced
// by a comment, a leading parenthesis, or a CTE — a "WITH ... SELECT" query
// was executed as a write and its result set silently discarded. A statement
// carrying a RETURNING clause (PostgreSQL, SQLite) also produces rows.
func returnsRows(query string) bool {
	s := stripLeadingSQLNoise(query)

	upper := strings.ToUpper(s)
	word := upper
	if i := strings.IndexFunc(upper, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '(' || r == ';'
	}); i >= 0 {
		word = upper[:i]
	}
	if rowReturningKeywords[word] {
		return true
	}

	// A data-modifying statement with RETURNING yields rows too.
	return regexpReturning.MatchString(upper)
}

// regexpReturning matches a RETURNING clause as a standalone keyword.
var regexpReturning = regexp.MustCompile(`(^|[\s)])RETURNING[\s(]`)

// stripLeadingSQLNoise removes leading whitespace, line/block comments and
// opening parentheses so the first significant keyword can be inspected.
func stripLeadingSQLNoise(query string) string {
	s := strings.TrimSpace(query)
	for {
		switch {
		case strings.HasPrefix(s, "--"):
			if i := strings.IndexByte(s, '\n'); i >= 0 {
				s = strings.TrimSpace(s[i+1:])
				continue
			}
			return ""
		case strings.HasPrefix(s, "/*"):
			if i := strings.Index(s, "*/"); i >= 0 {
				s = strings.TrimSpace(s[i+2:])
				continue
			}
			return ""
		case strings.HasPrefix(s, "("):
			s = strings.TrimSpace(s[1:])
			continue
		default:
			return s
		}
	}
}

// extractSQLParameters retrieves positional parameters from the Exchange.
// Priority: SqlParameters header first, then the body if it is a []any.
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

// normalizeSQLValue converts []byte (TEXT returned as binary by some drivers)
// to string for easier use in processors.
func normalizeSQLValue(v any) any {
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return v
}
