package gocamel

import (
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
// Full implementation will be in Task 4.
func (e *SQLStoredEndpoint) CreateProducer() (Producer, error) {
	return nil, fmt.Errorf("SQLStoredEndpoint.CreateProducer not yet implemented")
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
