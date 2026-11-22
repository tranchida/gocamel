package gocamel

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

// newTestSQLDB creates an in-memory SQLite database populated with a few users.
func newTestSQLDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)

	_, err = db.Exec(`
		CREATE TABLE users (
			id    INTEGER PRIMARY KEY,
			name  TEXT NOT NULL,
			email TEXT NOT NULL
		)
	`)
	require.NoError(t, err)

	_, err = db.Exec(`
		INSERT INTO users (id, name, email) VALUES
			(1, 'Alice', 'alice@example.com'),
			(2, 'Bob',   'bob@example.com'),
			(3, 'Carol', 'carol@example.com')
	`)
	require.NoError(t, err)

	t.Cleanup(func() { db.Close() })
	return db
}

func TestSQLComponent_CreateEndpoint_RequiresQuery(t *testing.T) {
	db := newTestSQLDB(t)
	comp := NewSQLComponent()
	comp.RegisterDataSource("testdb", db)

	_, err := comp.CreateEndpoint("sql://testdb")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "query")
}

func TestSQLComponent_CreateEndpoint_UnknownDataSource(t *testing.T) {
	comp := NewSQLComponent()

	_, err := comp.CreateEndpoint("sql://missing?query=" + url.QueryEscape("SELECT 1"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "datasource")
}

func TestSQLProducer_Select(t *testing.T) {
	db := newTestSQLDB(t)
	comp := NewSQLComponent()
	comp.RegisterDataSource("testdb", db)

	uri := "sql://testdb?query=" + url.QueryEscape("SELECT id, name FROM users ORDER BY id")
	endpoint, err := comp.CreateEndpoint(uri)
	require.NoError(t, err)

	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)

	exchange := NewExchange(context.Background())
	require.NoError(t, producer.Send(exchange))

	results, ok := exchange.GetOut().GetBody().([]map[string]any)
	require.True(t, ok, "body should be []map[string]any, got %T", exchange.GetOut().GetBody())
	require.Len(t, results, 3)

	assert.EqualValues(t, 1, results[0]["id"])
	assert.Equal(t, "Alice", results[0]["name"])
	assert.Equal(t, "Bob", results[1]["name"])
	assert.Equal(t, "Carol", results[2]["name"])

	cols, _ := exchange.GetOut().GetHeader(SqlColumnNames)
	assert.Equal(t, []string{"id", "name"}, cols)

	rowCount, _ := exchange.GetOut().GetHeader(SqlRowCount)
	assert.Equal(t, 3, rowCount)
}

func TestSQLProducer_SelectWithParameters(t *testing.T) {
	db := newTestSQLDB(t)
	comp := NewSQLComponent()
	comp.RegisterDataSource("testdb", db)

	uri := "sql://testdb?query=" + url.QueryEscape("SELECT name FROM users WHERE id = ?")
	endpoint, err := comp.CreateEndpoint(uri)
	require.NoError(t, err)

	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)

	exchange := NewExchange(context.Background())
	exchange.GetIn().SetHeader(SqlParameters, []any{2})

	require.NoError(t, producer.Send(exchange))

	results, ok := exchange.GetOut().GetBody().([]map[string]any)
	require.True(t, ok)
	require.Len(t, results, 1)
	assert.Equal(t, "Bob", results[0]["name"])
}

func TestSQLProducer_SelectOne(t *testing.T) {
	db := newTestSQLDB(t)
	comp := NewSQLComponent()
	comp.RegisterDataSource("testdb", db)

	uri := "sql://testdb?outputType=SelectOne&query=" + url.QueryEscape("SELECT id, name, email FROM users WHERE id = ?")
	endpoint, err := comp.CreateEndpoint(uri)
	require.NoError(t, err)

	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)

	exchange := NewExchange(context.Background())
	exchange.GetIn().SetBody([]any{1})

	require.NoError(t, producer.Send(exchange))

	row, ok := exchange.GetOut().GetBody().(map[string]any)
	require.True(t, ok, "body should be map[string]any, got %T", exchange.GetOut().GetBody())
	assert.Equal(t, "Alice", row["name"])
	assert.Equal(t, "alice@example.com", row["email"])
}

func TestSQLProducer_SelectOne_NoRows(t *testing.T) {
	db := newTestSQLDB(t)
	comp := NewSQLComponent()
	comp.RegisterDataSource("testdb", db)

	uri := "sql://testdb?outputType=SelectOne&query=" + url.QueryEscape("SELECT id FROM users WHERE id = ?")
	endpoint, err := comp.CreateEndpoint(uri)
	require.NoError(t, err)

	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)

	exchange := NewExchange(context.Background())
	exchange.GetIn().SetHeader(SqlParameters, []any{999})

	require.NoError(t, producer.Send(exchange))
	assert.Nil(t, exchange.GetOut().GetBody())
}

func TestSQLProducer_Insert(t *testing.T) {
	db := newTestSQLDB(t)
	comp := NewSQLComponent()
	comp.RegisterDataSource("testdb", db)

	uri := "sql://testdb?query=" + url.QueryEscape("INSERT INTO users (id, name, email) VALUES (?, ?, ?)")
	endpoint, err := comp.CreateEndpoint(uri)
	require.NoError(t, err)

	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)

	exchange := NewExchange(context.Background())
	exchange.GetIn().SetHeader(SqlParameters, []any{4, "Dave", "dave@example.com"})

	require.NoError(t, producer.Send(exchange))

	affected, ok := exchange.GetOut().GetBody().(int64)
	require.True(t, ok, "body should be int64, got %T", exchange.GetOut().GetBody())
	assert.EqualValues(t, 1, affected)

	// Verify persistence.
	var name string
	require.NoError(t, db.QueryRow("SELECT name FROM users WHERE id = 4").Scan(&name))
	assert.Equal(t, "Dave", name)
}

func TestSQLProducer_Update(t *testing.T) {
	db := newTestSQLDB(t)
	comp := NewSQLComponent()
	comp.RegisterDataSource("testdb", db)

	uri := "sql://testdb?query=" + url.QueryEscape("UPDATE users SET email = ? WHERE id = ?")
	endpoint, err := comp.CreateEndpoint(uri)
	require.NoError(t, err)

	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)

	exchange := NewExchange(context.Background())
	exchange.GetIn().SetHeader(SqlParameters, []any{"alice+new@example.com", 1})

	require.NoError(t, producer.Send(exchange))
	assert.EqualValues(t, 1, exchange.GetOut().GetBody())

	var email string
	require.NoError(t, db.QueryRow("SELECT email FROM users WHERE id = 1").Scan(&email))
	assert.Equal(t, "alice+new@example.com", email)
}

func TestSQLProducer_Delete(t *testing.T) {
	db := newTestSQLDB(t)
	comp := NewSQLComponent()
	comp.RegisterDataSource("testdb", db)

	uri := "sql://testdb?query=" + url.QueryEscape("DELETE FROM users WHERE id = ?")
	endpoint, err := comp.CreateEndpoint(uri)
	require.NoError(t, err)

	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)

	exchange := NewExchange(context.Background())
	exchange.GetIn().SetHeader(SqlParameters, []any{3})

	require.NoError(t, producer.Send(exchange))
	assert.EqualValues(t, 1, exchange.GetOut().GetBody())

	var count int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count))
	assert.Equal(t, 2, count)
}

func TestSQLProducer_HeaderOverridesQuery(t *testing.T) {
	db := newTestSQLDB(t)
	comp := NewSQLComponent()
	comp.RegisterDataSource("testdb", db)

	uri := "sql://testdb?allowHeaderOverride=true&query=" + url.QueryEscape("SELECT 1")
	endpoint, err := comp.CreateEndpoint(uri)
	require.NoError(t, err)

	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)

	exchange := NewExchange(context.Background())
	exchange.GetIn().SetHeader(SqlQuery, "SELECT name FROM users WHERE id = ?")
	exchange.GetIn().SetHeader(SqlParameters, []any{1})

	require.NoError(t, producer.Send(exchange))

	results, ok := exchange.GetOut().GetBody().([]map[string]any)
	require.True(t, ok)
	require.Len(t, results, 1)
	assert.Equal(t, "Alice", results[0]["name"])
}

// TestSQLProducer_IgnoresHeaderOverrideByDefault pins the secure default: the
// CamelSqlQuery header must NOT replace the configured statement unless the
// endpoint opted in with allowHeaderOverride=true. Without this, any component
// that maps external metadata onto headers hands an attacker full control of
// the SQL statement.
func TestSQLProducer_IgnoresHeaderOverrideByDefault(t *testing.T) {
	db := newTestSQLDB(t)
	comp := NewSQLComponent()
	comp.RegisterDataSource("testdb", db)

	uri := "sql://testdb?query=" + url.QueryEscape("SELECT name FROM users WHERE id = 1")
	endpoint, err := comp.CreateEndpoint(uri)
	require.NoError(t, err)
	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)

	exchange := NewExchange(context.Background())
	exchange.GetIn().SetHeader(SqlQuery, "SELECT name FROM users WHERE id = 2")
	require.NoError(t, producer.Send(exchange))

	results, ok := exchange.GetOut().GetBody().([]map[string]any)
	require.True(t, ok)
	require.Len(t, results, 1)
	// The configured query ran, not the one from the header.
	assert.Equal(t, "Alice", results[0]["name"])
}

// TestSQLProducer_DoesNotInterpolateQuery verifies that ${...} placeholders in
// a SQL query are NOT substituted with Exchange data. Interpolating headers or
// body into the raw query string would defeat parameterised queries and enable
// SQL injection. Dynamic values must be bound via the SqlParameters header.
func TestSQLProducer_DoesNotInterpolateQuery(t *testing.T) {
	db := newTestSQLDB(t)
	comp := NewSQLComponent()
	comp.RegisterDataSource("testdb", db)

	uri := "sql://testdb?query=" + url.QueryEscape("SELECT name FROM ${header.table} WHERE id = ?")
	endpoint, err := comp.CreateEndpoint(uri)
	require.NoError(t, err)

	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)

	exchange := NewExchange(context.Background())
	exchange.GetIn().SetHeader("table", "users")
	exchange.GetIn().SetHeader(SqlParameters, []any{1})

	err = producer.Send(exchange)
	require.Error(t, err, "query with ${header.table} must NOT be interpolated; the driver should reject the literal $ token")
	assert.Contains(t, err.Error(), "$", "the literal ${header.table} should be passed to the driver as-is")
}

func TestSQLComponent_DefaultDataSource(t *testing.T) {
	db := newTestSQLDB(t)
	comp := NewSQLComponent()
	comp.SetDefaultDataSource(db)

	uri := "sql://any?query=" + url.QueryEscape("SELECT COUNT(*) AS n FROM users")
	endpoint, err := comp.CreateEndpoint(uri)
	require.NoError(t, err)

	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)

	exchange := NewExchange(context.Background())
	require.NoError(t, producer.Send(exchange))

	results, ok := exchange.GetOut().GetBody().([]map[string]any)
	require.True(t, ok)
	require.Len(t, results, 1)
	assert.EqualValues(t, 3, results[0]["n"])
}

func TestSQLProducer_Transacted(t *testing.T) {
	db := newTestSQLDB(t)
	comp := NewSQLComponent()
	comp.RegisterDataSource("testdb", db)

	uri := "sql://testdb?transacted=true&query=" + url.QueryEscape("INSERT INTO users (id, name, email) VALUES (?, ?, ?)")
	endpoint, err := comp.CreateEndpoint(uri)
	require.NoError(t, err)

	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)

	exchange := NewExchange(context.Background())
	exchange.GetIn().SetHeader(SqlParameters, []any{10, "Eve", "eve@example.com"})
	require.NoError(t, producer.Send(exchange))

	var name string
	require.NoError(t, db.QueryRow("SELECT name FROM users WHERE id = 10").Scan(&name))
	assert.Equal(t, "Eve", name)

	// A second insertion with the same key should return an error (implicit rollback).
	exchange = NewExchange(context.Background())
	exchange.GetIn().SetHeader(SqlParameters, []any{10, "Eve2", "eve2@example.com"})
	err = producer.Send(exchange)
	assert.Error(t, err)

	var stored string
	require.NoError(t, db.QueryRow("SELECT name FROM users WHERE id = 10").Scan(&stored))
	assert.Equal(t, "Eve", stored, "failed transaction should not modify existing row")
}

func TestSQLProducer_Batch(t *testing.T) {
	db := newTestSQLDB(t)
	comp := NewSQLComponent()
	comp.RegisterDataSource("testdb", db)

	uri := "sql://testdb?batch=true&query=" + url.QueryEscape("INSERT INTO users (id, name, email) VALUES (?, ?, ?)")
	endpoint, err := comp.CreateEndpoint(uri)
	require.NoError(t, err)

	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)

	exchange := NewExchange(context.Background())
	exchange.GetIn().SetBody([][]any{
		{20, "Frank", "frank@example.com"},
		{21, "Grace", "grace@example.com"},
	})
	require.NoError(t, producer.Send(exchange))

	assert.EqualValues(t, 2, exchange.GetOut().GetBody())

	var count int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM users WHERE id IN (20, 21)").Scan(&count))
	assert.Equal(t, 2, count)
}

func TestSQLEndpoint_ConsumerNotSupported(t *testing.T) {
	db := newTestSQLDB(t)
	comp := NewSQLComponent()
	comp.RegisterDataSource("testdb", db)

	uri := "sql://testdb?query=" + url.QueryEscape("SELECT 1")
	endpoint, err := comp.CreateEndpoint(uri)
	require.NoError(t, err)

	_, err = endpoint.CreateConsumer(nil)
	assert.Error(t, err)
}

func TestSQLProducer_RejectsStackedStatementWithOneSemicolon(t *testing.T) {
	db := newTestSQLDB(t)
	comp := NewSQLComponent()
	comp.RegisterDataSource("testdb", db)

	uri := "sql://testdb?allowHeaderOverride=true&query=" + url.QueryEscape("SELECT 1")
	endpoint, err := comp.CreateEndpoint(uri)
	require.NoError(t, err)
	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)

	exchange := NewExchange(context.Background())
	exchange.SetHeader(SqlQuery, "SELECT 1; DROP TABLE users")
	err = producer.Send(exchange)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stacked SQL statements")

	var count int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count))
}

func TestValidateSQLQuery_AllowsQuotedAndTrailingSemicolons(t *testing.T) {
	assert.NoError(t, validateSQLQuery("SELECT ';' AS value;   "))
	assert.NoError(t, validateSQLQuery(`SELECT ";" AS value`))
	assert.NoError(t, validateSQLQuery("SELECT `;` AS value"))
	assert.NoError(t, validateSQLQuery("SELECT `col;name` FROM users;"))
	assert.NoError(t, validateSQLQuery("SELECT 1; -- trailing comment"))
	assert.NoError(t, validateSQLQuery("SELECT 1; /* trailing ; comment */"))
	assert.NoError(t, validateSQLQuery("SELECT 1 /* embedded ; comment */;"))
	assert.Error(t, validateSQLQuery("SELECT 1; SELECT 2"))
	assert.Error(t, validateSQLQuery("SELECT 1; /* comment */ DROP TABLE users"))
	assert.Error(t, validateSQLQuery("SELECT `col` FROM users; DROP TABLE users"))
}

// sqlStubState tracks calls and configures behavior for the in-memory driver.Driver stub.
type sqlStubState struct {
	cols              []string
	rows              [][]driver.Value
	commitErrFn       func() error
	execErr           error
	rowsAffectedPanic any
	nextPanic         any
	nextCalls         atomic.Int32
	rowsClosed        atomic.Bool
	stmtClosed        atomic.Bool
	commitCalls       atomic.Int32
	rollbackCalls     atomic.Int32
	lastExecRows      int64
}

func newStubSQLDB(t *testing.T, state *sqlStubState) *sql.DB {
	t.Helper()
	db := sql.OpenDB(&sqlStubConnector{state: state})
	t.Cleanup(func() { _ = db.Close() })
	return db
}

type sqlStubConnector struct{ state *sqlStubState }

func (c *sqlStubConnector) Connect(context.Context) (driver.Conn, error) {
	return &sqlStubConn{state: c.state}, nil
}
func (c *sqlStubConnector) Driver() driver.Driver { return sqlStubDriver{} }

type sqlStubDriver struct{}

func (sqlStubDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("unused")
}

type sqlStubConn struct{ state *sqlStubState }

func (c *sqlStubConn) Prepare(string) (driver.Stmt, error) { return &sqlStubStmt{state: c.state}, nil }
func (c *sqlStubConn) Close() error                        { return nil }
func (c *sqlStubConn) Begin() (driver.Tx, error)           { return &sqlStubTx{state: c.state}, nil }
func (c *sqlStubConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return &sqlStubTx{state: c.state}, nil
}
func (c *sqlStubConn) QueryContext(_ context.Context, _ string, _ []driver.NamedValue) (driver.Rows, error) {
	return &sqlStubRows{state: c.state}, nil
}
func (c *sqlStubConn) ExecContext(_ context.Context, _ string, _ []driver.NamedValue) (driver.Result, error) {
	return driver.RowsAffected(c.state.lastExecRows), nil
}

type sqlStubStmt struct{ state *sqlStubState }

func (s *sqlStubStmt) Close() error {
	s.state.stmtClosed.Store(true)
	return nil
}
func (s *sqlStubStmt) NumInput() int { return -1 }
func (s *sqlStubStmt) Exec([]driver.Value) (driver.Result, error) {
	if s.state.execErr != nil {
		return nil, s.state.execErr
	}
	return &sqlStubResult{state: s.state}, nil
}

type sqlStubResult struct{ state *sqlStubState }

func (r *sqlStubResult) LastInsertId() (int64, error) { return 0, nil }
func (r *sqlStubResult) RowsAffected() (int64, error) {
	if r.state.rowsAffectedPanic != nil {
		panic(r.state.rowsAffectedPanic)
	}
	return r.state.lastExecRows, nil
}
func (s *sqlStubStmt) Query([]driver.Value) (driver.Rows, error) {
	return &sqlStubRows{state: s.state}, nil
}

type sqlStubTx struct{ state *sqlStubState }

func (tx *sqlStubTx) Commit() error {
	tx.state.commitCalls.Add(1)
	if tx.state.commitErrFn != nil {
		return tx.state.commitErrFn()
	}
	return nil
}
func (tx *sqlStubTx) Rollback() error {
	tx.state.rollbackCalls.Add(1)
	return nil
}

type sqlStubRows struct {
	state *sqlStubState
	idx   int
}

func (r *sqlStubRows) Columns() []string { return r.state.cols }
func (r *sqlStubRows) Close() error {
	r.state.rowsClosed.Store(true)
	return nil
}
func (r *sqlStubRows) Next(dest []driver.Value) error {
	r.state.nextCalls.Add(1)
	if r.state.nextPanic != nil {
		panic(r.state.nextPanic)
	}
	if r.idx >= len(r.state.rows) {
		return io.EOF
	}
	copy(dest, r.state.rows[r.idx])
	r.idx++
	return nil
}

func TestSQL_SelectOneZeroRowsClearsInBodyAndStopsAfterFirstRow(t *testing.T) {
	t.Run("zero rows clears In.Body across 2-step route and GetResponse", func(t *testing.T) {
		db := newTestSQLDB(t)
		camel := NewCamelContext()
		sqlComp := NewSQLComponent()
		sqlComp.RegisterDataSource("testdb", db)
		camel.AddComponent("sql", sqlComp)

		var downstreamBody any
		route, err := camel.CreateRouteBuilder().
			From("direct:findUser").
			To("sql://testdb?outputType=SelectOne&query=" + url.QueryEscape("SELECT id, name FROM users WHERE id = ?")).
			ProcessFunc(func(ex *Exchange) error {
				downstreamBody = ex.GetIn().GetBody()
				return nil
			}).
			Build()
		require.NoError(t, err)
		camel.AddRoute(route)

		require.NoError(t, camel.Start())
		t.Cleanup(func() { _ = camel.Stop() })

		ep, err := camel.CreateEndpoint("direct:findUser")
		require.NoError(t, err)
		prod, err := ep.CreateProducer()
		require.NoError(t, err)

		ex := NewExchange(context.Background())
		ex.GetIn().SetBody([]any{999})
		require.NoError(t, prod.Send(ex))

		assert.Nil(t, downstreamBody, "downstream step must observe In.Body == nil when SelectOne matches 0 rows")
		assert.Nil(t, ex.GetResponse().GetBody(), "GetResponse().GetBody() must be nil when SelectOne matches 0 rows")
	})

	t.Run("stops scanning after first row", func(t *testing.T) {
		state := &sqlStubState{
			cols: []string{"id", "name"},
			rows: [][]driver.Value{
				{int64(1), "Alice"},
				{int64(2), "Bob"},
				{int64(3), "Carol"},
				{int64(4), "Dave"},
			},
		}
		db := newStubSQLDB(t, state)
		comp := NewSQLComponent()
		comp.RegisterDataSource("testdb", db)

		ep, err := comp.CreateEndpoint("sql://testdb?outputType=SelectOne&query=" + url.QueryEscape("SELECT id, name FROM users"))
		require.NoError(t, err)
		prod, err := ep.CreateProducer()
		require.NoError(t, err)

		ex := NewExchange(context.Background())
		require.NoError(t, prod.Send(ex))

		row, ok := ex.GetOut().GetBody().(map[string]any)
		require.True(t, ok, "expected map[string]any, got %T", ex.GetOut().GetBody())
		assert.Equal(t, int64(1), row["id"])
		assert.Equal(t, "Alice", row["name"])
		assert.Equal(t, int32(1), state.nextCalls.Load(), "SelectOne must stop after scanning the first row")
		assert.True(t, state.rowsClosed.Load(), "rows must be closed")
		rc, _ := ex.GetOut().GetHeader(SqlRowCount)
		assert.Equal(t, 1, rc)
	})
}

func TestSQL_EnvCannotEnableAllowHeaderOverride(t *testing.T) {
	t.Setenv("allowHeaderOverride", "true")
	t.Setenv("SQL_ALLOWHEADEROVERRIDE", "true")
	t.Setenv("query", "SELECT name FROM users WHERE id = 2")
	t.Setenv("SQL_QUERY", "SELECT name FROM users WHERE id = 2")

	db := newTestSQLDB(t)
	comp := NewSQLComponent()
	comp.RegisterDataSource("testdb", db)

	uri := "sql://testdb?query=" + url.QueryEscape("SELECT name FROM users WHERE id = 1")
	ep, err := comp.CreateEndpoint(uri)
	require.NoError(t, err)
	prod, err := ep.CreateProducer()
	require.NoError(t, err)

	ex := NewExchange(context.Background())
	ex.GetIn().SetHeader(SqlQuery, "SELECT name FROM users WHERE id = 2")
	require.NoError(t, prod.Send(ex))

	results, ok := ex.GetOut().GetBody().([]map[string]any)
	require.True(t, ok)
	require.Len(t, results, 1)
	assert.Equal(t, "Alice", results[0]["name"], "env vars must not override URI query or enable allowHeaderOverride")
}

func TestSQL_ReturnsRowsIgnoresStringLiterals(t *testing.T) {
	db := newTestSQLDB(t)
	comp := NewSQLComponent()
	comp.RegisterDataSource("testdb", db)

	uri := "sql://testdb?query=" + url.QueryEscape("UPDATE users SET name = 'customer returning item' WHERE id = ?")
	ep, err := comp.CreateEndpoint(uri)
	require.NoError(t, err)
	prod, err := ep.CreateProducer()
	require.NoError(t, err)

	ex := NewExchange(context.Background())
	ex.GetIn().SetBody([]any{1})
	require.NoError(t, prod.Send(ex))

	assert.Equal(t, int64(1), ex.GetOut().GetBody(), "UPDATE with 'returning' in string literal must execute as write")

	var updated string
	require.NoError(t, db.QueryRow("SELECT name FROM users WHERE id = 1").Scan(&updated))
	assert.Equal(t, "customer returning item", updated)

	writes := []string{
		"UPDATE t SET note = 'customer returning item' WHERE id = ?",
		"INSERT INTO t (note) VALUES ('it''s returning')",
		"UPDATE t SET note = '/* returning */' WHERE id = ?",
		"UPDATE t SET note = '-- returning' WHERE id = ?",
		`UPDATE t SET note = "returning" WHERE id = ?`,
		"UPDATE t SET `returning` = 1 WHERE id = ?",
		"UPDATE t SET [returning] = 1 WHERE id = ?",
		"UPDATE t SET note = $$returning$$ WHERE id = ?",
		"UPDATE t SET note = $tag$returning$tag$ WHERE id = ?",
		"UPDATE t SET x = $$'$$ WHERE note = 'returning'",
		`UPDATE t SET note = E'it\'s returning' WHERE id = ?`,
		"UPDATE t SET x = 1 -- returning count",
		"UPDATE t SET x = 1 /* returning count */",
		"UPDATE t SET x = returning_flag WHERE id = ?",
		"UPDATE t SET returning = 1 WHERE id = ?",
		"UPDATE t SET x = t.returning WHERE id = ?",
	}
	for _, q := range writes {
		assert.Falsef(t, returnsRows(q), "%q must be classified as write", q)
	}

	reads := []string{
		"INSERT INTO t VALUES (1) /* comment */ RETURNING id",
		"INSERT INTO t VALUES (1) -- comment\nRETURNING id",
		"INSERT INTO t VALUES (1) /* ' */ RETURNING id",
		"UPDATE t SET x = $$'$$ RETURNING id",
		"DELETE FROM t WHERE id = 1 RETURNING *",
		"UPDATE t SET x = 'a' RETURNING(id)",
		"-- comment\n( /* c */ SELECT 1 )",
	}
	for _, q := range reads {
		assert.Truef(t, returnsRows(q), "%q must be classified as row-returning", q)
	}
}

func TestSQL_SendTxDoesNotMutateOutBeforeCommitFail(t *testing.T) {
	t.Run("failed commit preserves In.Body and does not mutate Out on SELECT/SelectOne", func(t *testing.T) {
		var attempt atomic.Int32
		state := &sqlStubState{
			cols: []string{"id"},
			rows: nil, // 0 rows
			commitErrFn: func() error {
				if attempt.Add(1) == 1 {
					return errors.New("simulated commit failure")
				}
				return nil
			},
		}
		db := newStubSQLDB(t, state)
		comp := NewSQLComponent()
		comp.RegisterDataSource("testdb", db)

		ep, err := comp.CreateEndpoint("sql://testdb?transacted=true&outputType=SelectOne&query=" + url.QueryEscape("SELECT id FROM users WHERE id = ?"))
		require.NoError(t, err)
		prod, err := ep.CreateProducer()
		require.NoError(t, err)

		ex := NewExchange(context.Background())
		ex.GetIn().SetBody([]any{999})
		err = prod.Send(ex)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "commit")

		assert.Nil(t, ex.GetOut().GetBody(), "Out.Body must remain untouched after Commit failure")
		assert.Empty(t, ex.GetOut().GetHeaders(), "Out.Headers must remain untouched after Commit failure")
		assert.Equal(t, []any{999}, ex.GetIn().GetBody(), "In.Body must be preserved for retry after Commit failure")

		// Retry succeeds on second attempt using preserved In.Body.
		require.NoError(t, prod.Send(ex))
		assert.Nil(t, ex.GetIn().GetBody())
	})

	t.Run("failed SQLite deferred FK commit does not mutate Out", func(t *testing.T) {
		db, err := sql.Open("sqlite", ":memory:")
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })

		_, err = db.Exec(`
			PRAGMA foreign_keys = ON;
			CREATE TABLE parents (id INTEGER PRIMARY KEY);
			CREATE TABLE children (
				id INTEGER PRIMARY KEY,
				parent_id INTEGER REFERENCES parents(id) DEFERRABLE INITIALLY DEFERRED
			);
		`)
		require.NoError(t, err)

		comp := NewSQLComponent()
		comp.RegisterDataSource("testdb", db)
		ep, err := comp.CreateEndpoint("sql://testdb?transacted=true&query=" + url.QueryEscape("INSERT INTO children (id, parent_id) VALUES (?, ?)"))
		require.NoError(t, err)
		prod, err := ep.CreateProducer()
		require.NoError(t, err)

		ex := NewExchange(context.Background())
		ex.GetIn().SetBody([]any{1, 999})
		err = prod.Send(ex)
		require.Error(t, err)
		assert.Nil(t, ex.GetOut().GetBody(), "Out.Body must remain untouched after Commit failure")
		assert.Empty(t, ex.GetOut().GetHeaders(), "Out.Headers must remain untouched after Commit failure")
		assert.Equal(t, []any{1, 999}, ex.GetIn().GetBody(), "In.Body must be preserved after Commit failure")
	})
}

func TestSQL_SendTxRollsBackOnPanic(t *testing.T) {
	state := &sqlStubState{
		cols:      []string{"id"},
		rows:      [][]driver.Value{{int64(1)}},
		nextPanic: "boom inside scan",
	}
	db := newStubSQLDB(t, state)
	comp := NewSQLComponent()
	comp.RegisterDataSource("testdb", db)

	ep, err := comp.CreateEndpoint("sql://testdb?transacted=true&query=" + url.QueryEscape("SELECT id FROM users"))
	require.NoError(t, err)
	prod, err := ep.CreateProducer()
	require.NoError(t, err)

	ex := NewExchange(context.Background())
	assert.Panics(t, func() {
		_ = prod.Send(ex)
	})
	assert.True(t, state.rowsClosed.Load(), "rows must be closed on panic")
	assert.GreaterOrEqual(t, state.rollbackCalls.Load(), int32(1), "transaction must be rolled back on panic")
}

func TestSQL_BatchCommitFailurePreservesInBody(t *testing.T) {
	state := &sqlStubState{
		lastExecRows: 1,
		commitErrFn:  func() error { return errors.New("batch commit failed") },
	}
	db := newStubSQLDB(t, state)
	comp := NewSQLComponent()
	comp.RegisterDataSource("testdb", db)

	ep, err := comp.CreateEndpoint("sql://testdb?batch=true&query=" + url.QueryEscape("INSERT INTO users (id) VALUES (?)"))
	require.NoError(t, err)
	prod, err := ep.CreateProducer()
	require.NoError(t, err)

	payload := [][]any{{1}, {2}}
	ex := NewExchange(context.Background())
	ex.GetIn().SetBody(payload)

	err = prod.Send(ex)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "commit")
	assert.Nil(t, ex.GetOut().GetBody())
	assert.Empty(t, ex.GetOut().GetHeaders())
	assert.Equal(t, payload, ex.GetIn().GetBody())
	assert.Equal(t, int32(1), state.commitCalls.Load())
	assert.True(t, state.stmtClosed.Load())
}

func TestSQL_BatchRollsBackOnErrorAndPanic(t *testing.T) {
	t.Run("error rolls back and preserves In.Body", func(t *testing.T) {
		state := &sqlStubState{
			execErr: errors.New("constraint violation"),
		}
		db := newStubSQLDB(t, state)
		comp := NewSQLComponent()
		comp.RegisterDataSource("testdb", db)

		ep, err := comp.CreateEndpoint("sql://testdb?batch=true&query=" + url.QueryEscape("INSERT INTO users (id) VALUES (?)"))
		require.NoError(t, err)
		prod, err := ep.CreateProducer()
		require.NoError(t, err)

		payload := [][]any{{1}}
		ex := NewExchange(context.Background())
		ex.GetIn().SetBody(payload)

		err = prod.Send(ex)
		require.Error(t, err)
		assert.Nil(t, ex.GetOut().GetBody())
		assert.Empty(t, ex.GetOut().GetHeaders())
		assert.Equal(t, payload, ex.GetIn().GetBody())
		assert.True(t, state.stmtClosed.Load(), "prepared statement must be closed on error")
		assert.GreaterOrEqual(t, state.rollbackCalls.Load(), int32(1), "batch transaction must roll back on error")
	})

	t.Run("panic in RowsAffected rolls back and closes statement", func(t *testing.T) {
		state := &sqlStubState{
			rowsAffectedPanic: "boom inside batch result",
		}
		db := newStubSQLDB(t, state)
		comp := NewSQLComponent()
		comp.RegisterDataSource("testdb", db)

		ep, err := comp.CreateEndpoint("sql://testdb?batch=true&query=" + url.QueryEscape("INSERT INTO users (id) VALUES (?)"))
		require.NoError(t, err)
		prod, err := ep.CreateProducer()
		require.NoError(t, err)

		ex := NewExchange(context.Background())
		ex.GetIn().SetBody([][]any{{1}})

		assert.Panics(t, func() {
			_ = prod.Send(ex)
		})
		assert.True(t, state.stmtClosed.Load(), "prepared statement must be closed on panic")
		assert.GreaterOrEqual(t, state.rollbackCalls.Load(), int32(1), "batch transaction must roll back on panic")
	})
}

func TestSQLComponent_RegisterDataSource_RejectsNilDB(t *testing.T) {
	comp := NewSQLComponent()
	comp.RegisterDataSource("nildb", nil)

	_, err := comp.CreateEndpoint("sql://nildb?query=" + url.QueryEscape("SELECT 1"))
	require.Error(t, err, "nil *sql.DB must not be accepted as a valid datasource")
}

func TestSQLComponent_CreateEndpoint_EagerValidation(t *testing.T) {
	db := newTestSQLDB(t)
	comp := NewSQLComponent()
	comp.RegisterDataSource("testdb", db)

	_, err := comp.CreateEndpoint("sql://testdb?outputType=Bogus&query=" + url.QueryEscape("SELECT 1"))
	assert.Error(t, err, "invalid outputType must fail in CreateEndpoint")

	_, err = comp.CreateEndpoint("sql://testdb?transacted=maybe&query=" + url.QueryEscape("SELECT 1"))
	assert.Error(t, err, "invalid boolean transacted must fail in CreateEndpoint")

	_, err = comp.CreateEndpoint("sql://testdb?batch=nope&query=" + url.QueryEscape("INSERT INTO users VALUES (1,'a','b')"))
	assert.Error(t, err, "invalid boolean batch must fail in CreateEndpoint")

	_, err = comp.CreateEndpoint("sql://testdb?allowHeaderOverride=yes&query=" + url.QueryEscape("SELECT 1"))
	assert.Error(t, err, "invalid boolean allowHeaderOverride must fail in CreateEndpoint")

	_, err = comp.CreateEndpoint("sql://testdb?query=" + url.QueryEscape("SELECT 1; DROP TABLE users"))
	assert.Error(t, err, "stacked SQL query must fail eagerly in CreateEndpoint")
}
