package gocamel

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"reflect"
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
	if db == nil {
		LogWarn("sql: ignoring nil *sql.DB in RegisterDataSource", "name", name)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dataSources[name] = db
}

// SetDefaultDataSource sets the connection used when no named datasource matches.
func (c *SQLComponent) SetDefaultDataSource(db *sql.DB) {
	if db == nil {
		LogWarn("sql: ignoring nil *sql.DB in SetDefaultDataSource")
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.defaultDataSource = db
}

func (c *SQLComponent) lookup(name string) (*sql.DB, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if name != "" {
		if db, ok := c.dataSources[name]; ok && db != nil {
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

	// Read SQL options strictly from URI query parameters, never from environment
	// variables, so process env vars cannot inject queries or enable allowHeaderOverride.
	q := u.Query()
	name := resolveSQLDataSourceName(u, q)

	query := q.Get("query")
	if query == "" {
		return nil, fmt.Errorf("required option 'query' missing in sql URI: %s", uri)
	}
	if err := validateSQLQuery(query); err != nil {
		return nil, fmt.Errorf("invalid sql URI: %w", err)
	}

	db, ok := c.lookup(name)
	if !ok {
		return nil, fmt.Errorf("datasource '%s' not found: register it via RegisterDataSource() or SetDefaultDataSource()", name)
	}

	outputType, err := parseSQLOutputType(q, "sql")
	if err != nil {
		return nil, err
	}

	batch, err := parseSQLBoolOption(q, "batch")
	if err != nil {
		return nil, err
	}

	transacted, err := parseSQLBoolOption(q, "transacted")
	if err != nil {
		return nil, err
	}

	allowHeaderOverride, err := parseSQLBoolOption(q, "allowHeaderOverride")
	if err != nil {
		return nil, err
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

func resolveSQLDataSourceName(u *url.URL, q url.Values) string {
	name := u.Host
	if name == "" && u.Opaque != "" {
		name = u.Opaque
	}
	if path := strings.TrimPrefix(u.Path, "/"); path != "" {
		name = path
	}
	if ref := q.Get("dataSourceRef"); ref != "" {
		name = ref
	}
	return name
}

func parseSQLOutputType(q url.Values, scheme string) (SQLOutputType, error) {
	outputType := SQLOutputSelectList
	if v := q.Get("outputType"); v != "" {
		outputType = SQLOutputType(v)
		if outputType != SQLOutputSelectList && outputType != SQLOutputSelectOne {
			return "", fmt.Errorf("invalid outputType %q in %s URI (supported: %s, %s)", v, scheme, SQLOutputSelectList, SQLOutputSelectOne)
		}
	}
	return outputType, nil
}

func parseSQLBoolOption(q url.Values, key string) (bool, error) {
	v := q.Get(key)
	if v == "" {
		return false, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("invalid boolean value %q for option %q: %w", v, key, err)
	}
	return b, nil
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
	if p.endpoint == nil || p.endpoint.db == nil {
		return fmt.Errorf("sql: producer has no configured datasource")
	}

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
	defer func() { _ = tx.Rollback() }()

	if isSelect {
		cols, results, err := p.runSelect(ctx, tx, query, params)
		if err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("transaction commit error: %w", err)
		}
		applySelectResult(exchange, p.endpoint.outputType, cols, results)
		return nil
	}

	affected, err := p.runWrite(ctx, tx, query, params)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("transaction commit error: %w", err)
	}
	applyWriteResult(exchange, affected)
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
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		return fmt.Errorf("batch prepare error: %w", err)
	}
	defer stmt.Close()

	var total int64
	for i, params := range batch {
		result, err := stmt.ExecContext(ctx, params...)
		if err != nil {
			return fmt.Errorf("batch error at row %d: %w", i, err)
		}
		if n, err := result.RowsAffected(); err == nil {
			total += n
		}
	}

	if err := stmt.Close(); err != nil {
		return fmt.Errorf("batch statement close error: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("batch commit error: %w", err)
	}

	applyWriteResult(exchange, total)
	return nil
}

type sqlQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

type sqlExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func (p *SQLProducer) execSelect(ctx context.Context, exchange *Exchange, q sqlQuerier, query string, params []any) error {
	cols, results, err := p.runSelect(ctx, q, query, params)
	if err != nil {
		return err
	}
	applySelectResult(exchange, p.endpoint.outputType, cols, results)
	return nil
}

func (p *SQLProducer) runSelect(ctx context.Context, q sqlQuerier, query string, params []any) ([]string, []map[string]any, error) {
	rows, err := q.QueryContext(ctx, query, params...)
	if err != nil {
		return nil, nil, fmt.Errorf("SELECT execution error: %w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, nil, fmt.Errorf("column retrieval error: %w", err)
	}

	selectOne := p.endpoint.outputType == SQLOutputSelectOne
	var results []map[string]any
	for rows.Next() {
		row, err := scanSQLRow(rows, cols)
		if err != nil {
			return nil, nil, fmt.Errorf("row scan error: %w", err)
		}
		results = append(results, row)
		if selectOne {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("row iteration error: %w", err)
	}

	return cols, results, nil
}

func scanSQLRow(rows *sql.Rows, cols []string) (map[string]any, error) {
	values := make([]any, len(cols))
	pointers := make([]any, len(cols))
	for i := range values {
		pointers[i] = &values[i]
	}
	if err := rows.Scan(pointers...); err != nil {
		return nil, err
	}
	row := make(map[string]any, len(cols))
	for i, c := range cols {
		row[c] = normalizeSQLValue(values[i])
	}
	return row, nil
}

func applySelectResult(exchange *Exchange, outputType SQLOutputType, cols []string, results []map[string]any) {
	exchange.GetOut().SetHeader(SqlColumnNames, cols)
	exchange.GetOut().SetHeader(SqlRowCount, len(results))
	applySQLOutput(exchange, outputType, results)
}

func applySQLOutput(exchange *Exchange, outputType SQLOutputType, results []map[string]any) {
	switch outputType {
	case SQLOutputSelectOne:
		if len(results) > 0 {
			exchange.GetOut().SetBody(results[0])
		} else {
			exchange.GetOut().SetBody(nil)
			// Clear In.Body so downstream steps and GetResponse() do not retain
			// query input parameters when Out.Body is nil (HasOut() == false).
			exchange.GetIn().SetBody(nil)
		}
	default:
		exchange.GetOut().SetBody(results)
	}
}

func (p *SQLProducer) execWrite(ctx context.Context, exchange *Exchange, e sqlExecer, query string, params []any) error {
	affected, err := p.runWrite(ctx, e, query, params)
	if err != nil {
		return err
	}
	applyWriteResult(exchange, affected)
	return nil
}

func (p *SQLProducer) runWrite(ctx context.Context, e sqlExecer, query string, params []any) (int64, error) {
	result, err := e.ExecContext(ctx, query, params...)
	if err != nil {
		return 0, fmt.Errorf("SQL execution error: %w", err)
	}
	affected, _ := result.RowsAffected()
	return affected, nil
}

func applyWriteResult(exchange *Exchange, affected int64) {
	exchange.GetOut().SetHeader(SqlRowCount, affected)
	exchange.GetOut().SetBody(affected)
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

type sqlTokenKind int

const (
	sqlTokenWord sqlTokenKind = iota
	sqlTokenLParen
	sqlTokenEq
	sqlTokenDot
	sqlTokenOther
)

// returnsRows reports whether the statement should be run through
// QueryContext (result set) rather than ExecContext (affected rows).
//
// It uses a lexical scanner that skips SQL string literals, quoted identifiers,
// dollar-quoted strings, and '--' / '/* ... */' comments so keywords inside data
// or comments (such as 'customer returning item') do not misclassify writes.
func returnsRows(query string) bool {
	pos := 0
	firstKeywordChecked := false
	prevKind := sqlTokenOther
	sawReturning := false

	for {
		kind, word, nextPos, ok := nextSQLToken(query, pos)
		if !ok {
			break
		}
		pos = nextPos

		if !firstKeywordChecked {
			if kind == sqlTokenLParen {
				prevKind = kind
				continue
			}
			firstKeywordChecked = true
			if kind == sqlTokenWord && rowReturningKeywords[word] {
				return true
			}
		}

		if sawReturning {
			// "SET returning = 1" assigns a column named "returning", not a RETURNING clause.
			if kind != sqlTokenEq {
				return true
			}
			sawReturning = false
		}

		if kind == sqlTokenWord && word == "RETURNING" && prevKind != sqlTokenDot {
			sawReturning = true
		}

		prevKind = kind
	}

	return sawReturning
}

func nextSQLToken(query string, pos int) (sqlTokenKind, string, int, bool) {
	i := pos
	for i < len(query) {
		c := query[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v':
			i++
		case c == '-' && i+1 < len(query) && query[i+1] == '-':
			i += 2
			for i < len(query) && query[i] != '\n' && query[i] != '\r' {
				i++
			}
		case c == '/' && i+1 < len(query) && query[i+1] == '*':
			if endRel := strings.Index(query[i+2:], "*/"); endRel >= 0 {
				i += 2 + endRel + 2
			} else {
				return sqlTokenOther, "", len(query), false
			}
		case c == '\'':
			return sqlTokenOther, "", skipQuotedLiteral(query, i, '\'', false), true
		case c == '"':
			return sqlTokenOther, "", skipQuotedLiteral(query, i, '"', false), true
		case c == '`':
			return sqlTokenOther, "", skipQuotedLiteral(query, i, '`', false), true
		case (c == 'E' || c == 'e') && i+1 < len(query) && query[i+1] == '\'':
			// PostgreSQL C-style escape string literal (E'...')
			return sqlTokenOther, "", skipQuotedLiteral(query, i+1, '\'', true), true
		case c == '[':
			if end := findSQLClosingBracket(query, i); end > i {
				return sqlTokenOther, "", end, true
			}
			return sqlTokenOther, "", i + 1, true
		case c == '$':
			if tag, ok := matchDollarQuoteTag(query[i:]); ok {
				if endRel := strings.Index(query[i+len(tag):], tag); endRel >= 0 {
					return sqlTokenOther, "", i + len(tag) + endRel + len(tag), true
				}
			}
			return sqlTokenOther, "", i + 1, true
		case c == '(':
			return sqlTokenLParen, "(", i + 1, true
		case c == '=':
			return sqlTokenEq, "=", i + 1, true
		case c == '.':
			return sqlTokenDot, ".", i + 1, true
		case isSQLIdentStart(c):
			start := i
			i++
			for i < len(query) && isSQLIdentCont(query[i]) {
				i++
			}
			return sqlTokenWord, strings.ToUpper(query[start:i]), i, true
		case c >= '0' && c <= '9':
			i++
			for i < len(query) && (isSQLIdentCont(query[i])) {
				i++
			}
			return sqlTokenOther, "", i, true
		default:
			return sqlTokenOther, "", i + 1, true
		}
	}
	return sqlTokenOther, "", len(query), false
}

func skipQuotedLiteral(s string, start int, quote byte, backslashEscapes bool) int {
	i := start + 1
	for i < len(s) {
		c := s[i]
		if backslashEscapes && c == '\\' {
			if i+1 < len(s) {
				i += 2
			} else {
				i++
			}
			continue
		}
		if c == quote {
			if i+1 < len(s) && s[i+1] == quote {
				i += 2
				continue
			}
			return i + 1
		}
		i++
	}
	return len(s)
}

func findSQLClosingBracket(s string, start int) int {
	i := start + 1
	for i < len(s) {
		if s[i] == ']' {
			if i+1 < len(s) && s[i+1] == ']' {
				i += 2
				continue
			}
			return i + 1
		}
		i++
	}
	return 0
}

func isSQLIdentStart(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
}

func isSQLIdentCont(c byte) bool {
	return isSQLIdentStart(c) || (c >= '0' && c <= '9')
}

// extractSQLParameters retrieves positional parameters from the Exchange.
// Priority: SqlParameters header first, then the body if it is a []any.
func extractSQLParameters(exchange *Exchange) []any {
	if v, ok := exchange.GetIn().GetHeader(SqlParameters); ok {
		if s := toAnySlice(v); s != nil {
			return s
		}
	}
	if body := exchange.GetIn().GetBody(); body != nil {
		if s := toAnySlice(body); s != nil {
			return s
		}
	}
	return nil
}

func toAnySlice(v any) []any {
	if v == nil {
		return nil
	}
	if s, ok := v.([]any); ok {
		return s
	}
	if _, ok := v.([]byte); ok {
		return nil
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Slice {
		n := rv.Len()
		res := make([]any, n)
		for i := 0; i < n; i++ {
			res[i] = rv.Index(i).Interface()
		}
		return res
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
