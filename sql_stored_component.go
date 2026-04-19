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

// SqlStoredProcedureName is the header key for the stored procedure name.
const SqlStoredProcedureName = "CamelSqlStoredProcedureName"

// SQLStoredEndpoint represents a configured stored procedure endpoint.
// Full implementation with methods will be in Task 3.
type SQLStoredEndpoint struct {
	uri        string
	name       string
	procedure  string
	outputType SQLOutputType
	transacted bool
	noop       bool
	db         *sql.DB
}

// URI returns the endpoint URI.
func (e *SQLStoredEndpoint) URI() string { return e.uri }

// CreateProducer creates a SQLStoredProducer.
func (e *SQLStoredEndpoint) CreateProducer() (Producer, error) {
	return &SQLStoredProducer{endpoint: e}, nil
}

// CreateConsumer returns an error: sql-stored component is producer-only.
func (e *SQLStoredEndpoint) CreateConsumer(processor Processor) (Consumer, error) {
	return nil, fmt.Errorf("sql-stored component does not support consumers")
}

// SQLStoredComponent manages endpoints for calling stored procedures.
//
// The user registers their *sql.DB via RegisterDataSource() or SetDefaultDataSource()
// before building a route, then references the datasource by name in the URI.
//
// URI Format:
//	sql-stored://datasourceName?procedure=sp_name
//	sql-stored://logical?dataSourceRef=datasourceName&procedure=sp_name
//
// Options:
//	procedure      Name of the stored procedure to call (required)
//	dataSourceRef  Name of a registered datasource (otherwise uses the host from URI)
//	outputType     SelectList (default) or SelectOne
//	transacted     True to wrap the call in a transaction (default: false)
//	noop           True to consume the message without calling the procedure (default: false)
type SQLStoredComponent struct {
	mu                sync.RWMutex
	dataSources       map[string]*sql.DB
	defaultDataSource *sql.DB
}

// NewSQLStoredComponent creates a new SQLStoredComponent instance.
func NewSQLStoredComponent() *SQLStoredComponent {
	return &SQLStoredComponent{dataSources: make(map[string]*sql.DB)}
}

// RegisterDataSource registers a *sql.DB connection under a name.
func (c *SQLStoredComponent) RegisterDataSource(name string, db *sql.DB) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dataSources[name] = db
}

// SetDefaultDataSource sets the default connection to use when no named datasource matches.
func (c *SQLStoredComponent) SetDefaultDataSource(db *sql.DB) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.defaultDataSource = db
}

func (c *SQLStoredComponent) lookup(name string) (*sql.DB, bool) {
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

// CreateEndpoint creates a SQLStoredEndpoint from a URI.
//
// Format:
//	sql-stored://datasourceName?procedure=NAME
//	sql-stored://logical?dataSourceRef=datasourceName&procedure=NAME
//
// Required option:
//	procedure      Name of the stored procedure to call
//
// Optional options:
//	dataSourceRef  Reference to a datasource name (overrides URI host)
//	outputType     SelectList (default) or SelectOne
//	transacted     true to wrap procedure call in transaction (default: false)
//	noop           true to consume message without calling procedure (default: false)
func (c *SQLStoredComponent) CreateEndpoint(uri string) (Endpoint, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("invalid sql-stored URI: %w", err)
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

	procedure := GetConfigValue(u, "procedure")
	if procedure == "" {
		return nil, fmt.Errorf("required option 'procedure' missing in sql-stored URI: %s", uri)
	}

	db, ok := c.lookup(name)
	if !ok {
		return nil, fmt.Errorf("datasource '%s' not found: register it via RegisterDataSource() or SetDefaultDataSource()", name)
	}

	outputType := SQLOutputSelectList
	if v := GetConfigValue(u, "outputType"); v != "" {
		outputType = SQLOutputType(v)
	}

	transacted := false
	if v := GetConfigValue(u, "transacted"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			transacted = b
		}
	}

	noop := false
	if v := GetConfigValue(u, "noop"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			noop = b
		}
	}

	return &SQLStoredEndpoint{
		uri:        uri,
		name:       name,
		procedure:  procedure,
		outputType: outputType,
		transacted: transacted,
		noop:       noop,
		db:         db,
	}, nil
}

// SQLStoredProducer executes stored procedures on SQL databases.
type SQLStoredProducer struct {
	endpoint *SQLStoredEndpoint
}

// Start initializes the producer (noop since db is managed externally).
func (p *SQLStoredProducer) Start(ctx context.Context) error { return nil }

// Stop cleans up the producer (noop since db is managed externally).
func (p *SQLStoredProducer) Stop() error { return nil }

// Send executes the stored procedure with the given exchange.
func (p *SQLStoredProducer) Send(exchange *Exchange) error {
	// Get procedure name from endpoint or header
	procedure := p.endpoint.procedure
	if v, ok := exchange.GetIn().GetHeader(SqlStoredProcedureName); ok {
		if s, ok := v.(string); ok && s != "" {
			procedure = s
		}
	}

	// Interpolate procedure name
	procedure = Interpolate(procedure, exchange)

	// Get context from exchange or create Background
	ctx := exchange.Context
	if ctx == nil {
		ctx = context.Background()
	}

	// Extract parameters from exchange
	params := extractStoredProcedureParameters(exchange)

	// If noop mode: set body with procedure info and return
	if p.endpoint.noop {
		result := map[string]any{
			"procedure":     procedure,
			"parametersIn":  params,
		}
		exchange.GetOut().SetBody(result)
		return nil
	}

	// Execute procedure (transacted or direct)
	if p.endpoint.transacted {
		return p.sendTx(ctx, exchange, procedure, params)
	}
	return p.execProcedure(ctx, exchange, p.endpoint.db, procedure, params)
}

// execProcedure executes the stored procedure against the database.
func (p *SQLStoredProducer) execProcedure(ctx context.Context, exchange *Exchange, db *sql.DB, procedure string, params []StoredProcedureParam) error {
	// Build CALL statement
	query, args, outIndices := buildCallStatement(procedure, params)

	// Execute the stored procedure
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("error executing stored procedure: %w", err)
	}
	defer rows.Close()

	// Process results (support multiple result sets)
	var allResults []map[string]any
	var resultCount int

	for {
		cols, err := rows.Columns()
		if err != nil {
			return fmt.Errorf("error getting columns: %w", err)
		}

		for rows.Next() {
			values := make([]any, len(cols))
			pointers := make([]any, len(cols))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				return fmt.Errorf("error scanning row: %w", err)
			}
			row := make(map[string]any, len(cols))
			for i, c := range cols {
				row[c] = normalizeSQLValue(values[i])
			}
			allResults = append(allResults, row)
		}
		resultCount++

		// Check if there are more result sets
		if !rows.NextResultSet() {
			break
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating rows: %w", err)
	}

	// Handle OUT parameters
	if len(outIndices) > 0 {
		// For simplicity, we return row count and set body based on outputType
		// Full OUT parameter support would require sql.Out with specific drivers
	}

	// Set output body based on outputType
	switch p.endpoint.outputType {
	case SQLOutputSelectOne:
		if len(allResults) > 0 {
			exchange.GetOut().SetBody(allResults[0])
		} else {
			exchange.GetOut().SetBody(nil)
		}
	default:
		exchange.GetOut().SetBody(allResults)
	}

	// Set SqlRowCount header
	exchange.GetOut().SetHeader(SqlRowCount, len(allResults))

	return nil
}

// sendTx executes the stored procedure within a transaction.
func (p *SQLStoredProducer) sendTx(ctx context.Context, exchange *Exchange, procedure string, params []StoredProcedureParam) error {
	tx, err := p.endpoint.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("error beginning transaction: %w", err)
	}

	// Build CALL statement
	query, args, outIndices := buildCallStatement(procedure, params)

	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("error executing stored procedure: %w", err)
	}

	// Process results
	var allResults []map[string]any
	for {
		cols, err := rows.Columns()
		if err != nil {
			_ = rows.Close()
			_ = tx.Rollback()
			return fmt.Errorf("error getting columns: %w", err)
		}

		for rows.Next() {
			values := make([]any, len(cols))
			pointers := make([]any, len(cols))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				_ = rows.Close()
				_ = tx.Rollback()
				return fmt.Errorf("error scanning row: %w", err)
			}
			row := make(map[string]any, len(cols))
			for i, c := range cols {
				row[c] = normalizeSQLValue(values[i])
			}
			allResults = append(allResults, row)
		}

		if !rows.NextResultSet() {
			break
		}
	}
	rows.Close()

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("error committing transaction: %w", err)
	}

	// Handle OUT parameters
	if len(outIndices) > 0 {
		// OUT parameter handling would be implemented here
	}

	// Set output body based on outputType
	switch p.endpoint.outputType {
	case SQLOutputSelectOne:
		if len(allResults) > 0 {
			exchange.GetOut().SetBody(allResults[0])
		} else {
			exchange.GetOut().SetBody(nil)
		}
	default:
		exchange.GetOut().SetBody(allResults)
	}

	// Set SqlRowCount header
	exchange.GetOut().SetHeader(SqlRowCount, len(allResults))

	return nil
}

// extractStoredProcedureParameters extracts parameters from the exchange body.
// Priority 1: body is []StoredProcedureParam
// Priority 2: body is map[string]any -> convert to []StoredProcedureParam with Direction=ParamDirectionIn
func extractStoredProcedureParameters(exchange *Exchange) []StoredProcedureParam {
	body := exchange.GetIn().GetBody()
	if body == nil {
		return nil
	}

	// Priority 1: body is []StoredProcedureParam
	if params, ok := body.([]StoredProcedureParam); ok {
		return params
	}

	// Priority 2: body is map[string]any
	if m, ok := body.(map[string]any); ok {
		params := make([]StoredProcedureParam, 0, len(m))
		for name, value := range m {
			params = append(params, StoredProcedureParam{
				Name:      name,
				Direction: ParamDirectionIn,
				Value:     value,
			})
		}
		return params
	}

	return nil
}

// buildCallStatement builds the CALL statement for a stored procedure.
// Returns: query string, argument values, indices of OUT parameters for retrieval
func buildCallStatement(procedure string, params []StoredProcedureParam) (string, []any, []int) {
	if len(params) == 0 {
		return fmt.Sprintf("CALL %s()", procedure), nil, nil
	}

	placeholders := make([]string, len(params))
	args := make([]any, 0, len(params))
	var outIndices []int

	for i, param := range params {
		switch param.Direction {
		case ParamDirectionOut, ParamDirectionInOut:
			// For OUT/INOUT parameters, use ? placeholder
			// The actual OUT value retrieval is driver-specific
			placeholders[i] = "?"
			if param.Direction == ParamDirectionOut {
				outIndices = append(outIndices, i)
			}
			args = append(args, param.Value)
		default: // ParamDirectionIn
			placeholders[i] = "?"
			args = append(args, param.Value)
		}
	}

	query := fmt.Sprintf("CALL %s(%s)", procedure, strings.Join(placeholders, ", "))
	return query, args, outIndices
}
