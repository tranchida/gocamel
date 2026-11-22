package gocamel

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// SqlStoredProcedureName is the header key for the stored procedure name.
const SqlStoredProcedureName = "CamelSqlStoredProcedureName"

// storedProcedureNamePattern is the allowlist for stored procedure identifiers.
// Procedure names are spliced into a CALL statement and cannot be bound as a
// parameter, so they must be a strict identifier (optionally schema-qualified).
var storedProcedureNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]*$`)

// validateStoredProcedureName rejects procedure names that are not a plain
// identifier (optionally schema-qualified with dots). This is the SQL-injection
// boundary for the sql-stored component, where the name is interpolated into
// the CALL statement text.
func validateStoredProcedureName(procedure string) error {
	if procedure == "" || !storedProcedureNamePattern.MatchString(procedure) {
		return fmt.Errorf("invalid stored procedure name: %q", procedure)
	}
	return nil
}

// SQLStoredEndpoint represents a configured stored procedure endpoint.
// Full implementation with methods will be in Task 3.
type SQLStoredEndpoint struct {
	uri                 string
	name                string
	procedure           string
	outputType          SQLOutputType
	transacted          bool
	noop                bool
	allowHeaderOverride bool
	db                  *sql.DB
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
//
//	sql-stored://datasourceName?procedure=sp_name
//	sql-stored://logical?dataSourceRef=datasourceName&procedure=sp_name
//
// Options:
//
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
	if db == nil {
		LogWarn("sql-stored: ignoring nil *sql.DB in RegisterDataSource", "name", name)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dataSources[name] = db
}

// SetDefaultDataSource sets the default connection to use when no named datasource matches.
func (c *SQLStoredComponent) SetDefaultDataSource(db *sql.DB) {
	if db == nil {
		LogWarn("sql-stored: ignoring nil *sql.DB in SetDefaultDataSource")
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.defaultDataSource = db
}

func (c *SQLStoredComponent) lookup(name string) (*sql.DB, bool) {
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

// CreateEndpoint creates a SQLStoredEndpoint from a URI.
//
// Format:
//
//	sql-stored://datasourceName?procedure=NAME
//	sql-stored://logical?dataSourceRef=datasourceName&procedure=NAME
//
// Required option:
//
//	procedure      Name of the stored procedure to call
//
// Optional options:
//
//	dataSourceRef       Reference to a datasource name (overrides URI host)
//	outputType          SelectList (default) or SelectOne
//	transacted          true to wrap procedure call in transaction (default: false)
//	noop                true to consume message without calling procedure (default: false)
//	allowHeaderOverride true to honor the CamelSqlStoredProcedureName header
//	                    (default: false). SECURITY: when enabled the header is
//	                    attacker-controlled if the exchange carries untrusted
//	                    headers (e.g. HTTP), so it is validated against a strict
//	                    identifier allowlist and never interpolated.
func (c *SQLStoredComponent) CreateEndpoint(uri string) (Endpoint, error) {
	u, err := ParseURI(uri)
	if err != nil {
		return nil, fmt.Errorf("invalid sql-stored URI: %w", err)
	}

	// Read SQL options strictly from URI query parameters, never from environment
	// variables, so process env vars cannot hijack procedures or enable allowHeaderOverride.
	q := u.Query()
	name := resolveSQLDataSourceName(u, q)

	procedure := q.Get("procedure")
	if procedure == "" {
		return nil, fmt.Errorf("required option 'procedure' missing in sql-stored URI: %s", uri)
	}
	if err := validateStoredProcedureName(procedure); err != nil {
		return nil, fmt.Errorf("invalid sql-stored URI: %w", err)
	}

	db, ok := c.lookup(name)
	if !ok {
		return nil, fmt.Errorf("datasource '%s' not found: register it via RegisterDataSource() or SetDefaultDataSource()", name)
	}

	outputType, err := parseSQLOutputType(q, "sql-stored")
	if err != nil {
		return nil, err
	}

	transacted, err := parseSQLBoolOption(q, "transacted")
	if err != nil {
		return nil, err
	}

	noop, err := parseSQLBoolOption(q, "noop")
	if err != nil {
		return nil, err
	}

	allowHeaderOverride, err := parseSQLBoolOption(q, "allowHeaderOverride")
	if err != nil {
		return nil, err
	}

	return &SQLStoredEndpoint{
		uri:                 uri,
		name:                name,
		procedure:           procedure,
		outputType:          outputType,
		transacted:          transacted,
		noop:                noop,
		allowHeaderOverride: allowHeaderOverride,
		db:                  db,
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
	if p.endpoint == nil {
		return fmt.Errorf("sql-stored: producer has no configured endpoint")
	}

	// The procedure name defaults to the trusted URI configuration. The header
	// is only honored when the endpoint opted in with allowHeaderOverride, so
	// an untrusted exchange cannot smuggle a procedure name by default.
	procedure := p.endpoint.procedure
	if p.endpoint.allowHeaderOverride {
		if v, ok := exchange.GetIn().GetHeader(SqlStoredProcedureName); ok {
			if s, ok := v.(string); ok && s != "" {
				procedure = s
			}
		}
	}

	// The procedure name is an identifier spliced into the CALL statement text:
	// it is never interpolated with exchange data (which would allow SQL via
	// ${body}/${header.X}), and it is validated against a strict identifier
	// allowlist before use.
	if err := validateStoredProcedureName(procedure); err != nil {
		return err
	}

	// Get context from exchange or create Background
	ctx := exchange.Context
	if ctx == nil {
		ctx = context.Background()
	}

	// Extract and validate parameters from exchange
	params, err := extractStoredProcedureParameters(exchange)
	if err != nil {
		return err
	}
	if err := validateStoredProcedureParams(params); err != nil {
		return err
	}

	// If noop mode: set body with procedure info and return
	if p.endpoint.noop {
		result := map[string]any{
			"procedure":    procedure,
			"parametersIn": params,
		}
		exchange.GetOut().SetBody(result)
		return nil
	}

	if p.endpoint.db == nil {
		return fmt.Errorf("sql-stored: producer has no configured datasource")
	}

	// Execute procedure (transacted or direct)
	if p.endpoint.transacted {
		return p.sendTx(ctx, exchange, procedure, params)
	}
	return p.execProcedure(ctx, exchange, p.endpoint.db, procedure, params)
}

// execProcedure executes the stored procedure against the database.
func (p *SQLStoredProducer) execProcedure(ctx context.Context, exchange *Exchange, db *sql.DB, procedure string, params []StoredProcedureParam) error {
	query, args, err := buildCallStatement(procedure, params)
	if err != nil {
		return err
	}

	allResults, err := runStoredQuery(ctx, db, query, args, p.endpoint.outputType == SQLOutputSelectOne)
	if err != nil {
		return err
	}

	applyStoredProcedureResult(exchange, p.endpoint.outputType, allResults)
	return nil
}

// sendTx executes the stored procedure within a transaction.
func (p *SQLStoredProducer) sendTx(ctx context.Context, exchange *Exchange, procedure string, params []StoredProcedureParam) error {
	query, args, err := buildCallStatement(procedure, params)
	if err != nil {
		return err
	}

	tx, err := p.endpoint.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("error beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	allResults, err := runStoredQuery(ctx, tx, query, args, p.endpoint.outputType == SQLOutputSelectOne)
	if err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("error committing transaction: %w", err)
	}

	applyStoredProcedureResult(exchange, p.endpoint.outputType, allResults)
	return nil
}

func runStoredQuery(ctx context.Context, q sqlQuerier, query string, args []any, selectOne bool) ([]map[string]any, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("error executing stored procedure: %w", err)
	}
	defer rows.Close()

	var allResults []map[string]any
	for {
		cols, err := rows.Columns()
		if err != nil {
			return nil, fmt.Errorf("error getting columns: %w", err)
		}

		for rows.Next() {
			row, err := scanSQLRow(rows, cols)
			if err != nil {
				return nil, fmt.Errorf("error scanning row: %w", err)
			}
			allResults = append(allResults, row)
			if selectOne {
				break
			}
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("error iterating rows: %w", err)
		}

		if selectOne && len(allResults) > 0 {
			break
		}
		if !rows.NextResultSet() {
			break
		}
	}

	return allResults, nil
}

func applyStoredProcedureResult(exchange *Exchange, outputType SQLOutputType, allResults []map[string]any) {
	applySQLOutput(exchange, outputType, allResults)
	exchange.GetOut().SetHeader(SqlRowCount, len(allResults))
}

// extractStoredProcedureParameters extracts parameters from the exchange body.
// Only []StoredProcedureParam is accepted for ordered positional CALL binding;
// unordered map[string]any bodies are rejected to avoid random argument transposition.
func extractStoredProcedureParameters(exchange *Exchange) ([]StoredProcedureParam, error) {
	body := exchange.GetIn().GetBody()
	if body == nil {
		return nil, nil
	}

	if params, ok := body.([]StoredProcedureParam); ok {
		return params, nil
	}

	if _, ok := body.(map[string]any); ok {
		return nil, fmt.Errorf("sql-stored: unordered map[string]any body cannot be used for positional CALL parameters; pass []StoredProcedureParam instead")
	}

	return nil, nil
}

func validateStoredProcedureParams(params []StoredProcedureParam) error {
	for i, param := range params {
		switch param.Direction {
		case ParamDirectionIn:
		case ParamDirectionOut, ParamDirectionInOut:
			return fmt.Errorf("sql-stored: parameter %d (%q) uses unsupported direction %s; OUT/INOUT parameters require driver-specific sql.Out support", i, param.Name, param.Direction)
		default:
			return fmt.Errorf("sql-stored: parameter %d (%q) has unknown direction %d", i, param.Name, param.Direction)
		}
	}
	return nil
}

// buildCallStatement builds the CALL statement for a stored procedure.
// Returns: query string, argument values, or an error if any parameter direction is unsupported.
func buildCallStatement(procedure string, params []StoredProcedureParam) (string, []any, error) {
	if err := validateStoredProcedureParams(params); err != nil {
		return "", nil, err
	}
	if len(params) == 0 {
		return fmt.Sprintf("CALL %s()", procedure), nil, nil
	}

	placeholders := make([]string, len(params))
	args := make([]any, len(params))
	for i, param := range params {
		placeholders[i] = "?"
		args[i] = param.Value
	}

	query := fmt.Sprintf("CALL %s(%s)", procedure, strings.Join(placeholders, ", "))
	return query, args, nil
}
