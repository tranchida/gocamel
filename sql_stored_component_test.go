package gocamel

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

// newTestDB creates an in-memory SQLite database with a test procedure
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)

	// Create a test table
	_, err = db.Exec(`
		CREATE TABLE users (
			id    INTEGER PRIMARY KEY,
			name  TEXT NOT NULL,
			email TEXT NOT NULL
		)
	`)
	require.NoError(t, err)

	// Insert test data
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

func TestSQLStoredComponent_CreateEndpoint_RequiresProcedure(t *testing.T) {
	db := newTestDB(t)
	comp := NewSQLStoredComponent()
	comp.RegisterDataSource("testdb", db)

	// Try to create endpoint without procedure parameter
	_, err := comp.CreateEndpoint("sql-stored://testdb")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "procedure")
	assert.Contains(t, err.Error(), "missing")
}

func TestSQLStoredComponent_CreateEndpoint_UnknownDataSource(t *testing.T) {
	comp := NewSQLStoredComponent()

	// Try to create endpoint with unregistered datasource
	_, err := comp.CreateEndpoint("sql-stored://missing?procedure=sp_test")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "datasource")
	assert.Contains(t, err.Error(), "not found")
}

func TestSQLStoredComponent_BasicEndpoint(t *testing.T) {
	db := newTestDB(t)
	comp := NewSQLStoredComponent()
	comp.RegisterDataSource("testdb", db)

	uri := "sql-stored://testdb?procedure=sp_get_user"
	endpoint, err := comp.CreateEndpoint(uri)
	require.NoError(t, err)

	// Verify it's the correct type
	storedEndpoint, ok := endpoint.(*SQLStoredEndpoint)
	require.True(t, ok)

	// Verify endpoint properties
	assert.Equal(t, uri, storedEndpoint.URI())
	assert.Equal(t, "testdb", storedEndpoint.name)
	assert.Equal(t, "sp_get_user", storedEndpoint.procedure)
	assert.Equal(t, SQLOutputSelectList, storedEndpoint.outputType)
	assert.False(t, storedEndpoint.transacted)
	assert.False(t, storedEndpoint.noop)
}

func TestSQLStoredProducer_NoOpMode(t *testing.T) {
	db := newTestDB(t)
	comp := NewSQLStoredComponent()
	comp.RegisterDataSource("testdb", db)

	uri := "sql-stored://testdb?procedure=sp_test&noop=true"
	endpoint, err := comp.CreateEndpoint(uri)
	require.NoError(t, err)

	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)

	// Create exchange with parameters
	exchange := NewExchange(context.Background())
	exchange.GetIn().SetBody([]StoredProcedureParam{
		{Name: "userId", Direction: ParamDirectionIn, Value: 42},
	})

	err = producer.Send(exchange)
	require.NoError(t, err)

	// In noop mode, body should contain procedure info and parameters
	result, ok := exchange.GetOut().GetBody().(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "sp_test", result["procedure"])
	assert.NotNil(t, result["parametersIn"])
}

func TestSQLStoredProducer_ProcedureHeaderOverride(t *testing.T) {
	db := newTestDB(t)
	comp := NewSQLStoredComponent()
	comp.RegisterDataSource("testdb", db)

	// Create endpoint with one procedure name and header override enabled
	uri := "sql-stored://testdb?procedure=sp_endpoint_procedure&noop=true&allowHeaderOverride=true"
	endpoint, err := comp.CreateEndpoint(uri)
	require.NoError(t, err)

	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)

	// Create exchange with different procedure name in header
	exchange := NewExchange(context.Background())
	exchange.GetIn().SetHeader(SqlStoredProcedureName, "sp_header_procedure")
	exchange.GetIn().SetBody([]StoredProcedureParam{
		{Name: "param1", Direction: ParamDirectionIn, Value: "value1"},
	})

	err = producer.Send(exchange)
	require.NoError(t, err)

	// Result should use header procedure name, not endpoint
	result, ok := exchange.GetOut().GetBody().(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "sp_header_procedure", result["procedure"])
}

func TestSQLStoredProducer_HeaderIgnoredWithoutOptIn(t *testing.T) {
	db := newTestDB(t)
	comp := NewSQLStoredComponent()
	comp.RegisterDataSource("testdb", db)

	// allowHeaderOverride defaults to false: the header must be ignored.
	uri := "sql-stored://testdb?procedure=sp_endpoint_procedure&noop=true"
	endpoint, err := comp.CreateEndpoint(uri)
	require.NoError(t, err)

	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)

	exchange := NewExchange(context.Background())
	exchange.GetIn().SetHeader(SqlStoredProcedureName, "sp_attacker")
	err = producer.Send(exchange)
	require.NoError(t, err)

	result, ok := exchange.GetOut().GetBody().(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "sp_endpoint_procedure", result["procedure"])
}

func TestSQLStoredProducer_RejectsInvalidProcedureName(t *testing.T) {
	db := newTestDB(t)
	comp := NewSQLStoredComponent()
	comp.RegisterDataSource("testdb", db)

	// Even with header override enabled, a malicious name must be rejected.
	uri := "sql-stored://testdb?procedure=sp_test&noop=true&allowHeaderOverride=true"
	endpoint, err := comp.CreateEndpoint(uri)
	require.NoError(t, err)

	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)

	exchange := NewExchange(context.Background())
	exchange.GetIn().SetHeader(SqlStoredProcedureName, "sp_test; DROP TABLE users; --")
	err = producer.Send(exchange)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid stored procedure name")
}

func TestSQLStoredEndpoint_ConsumerNotSupported(t *testing.T) {
	db := newTestDB(t)
	comp := NewSQLStoredComponent()
	comp.RegisterDataSource("testdb", db)

	uri := "sql-stored://testdb?procedure=sp_test"
	endpoint, err := comp.CreateEndpoint(uri)
	require.NoError(t, err)

	// Consumer should return error
	_, err = endpoint.CreateConsumer(nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "consumer")
	assert.Contains(t, err.Error(), "not support")
}

func TestBuildCallStatement_NoParams(t *testing.T) {
	query, args, err := buildCallStatement("sp_no_params", nil)
	require.NoError(t, err)
	assert.Equal(t, "CALL sp_no_params()", query)
	assert.Nil(t, args)
}

func TestBuildCallStatement_InParams(t *testing.T) {
	params := []StoredProcedureParam{
		{Name: "param1", Direction: ParamDirectionIn, Value: 42},
		{Name: "param2", Direction: ParamDirectionIn, Value: "hello"},
	}
	query, args, err := buildCallStatement("sp_with_params", params)
	require.NoError(t, err)
	assert.Equal(t, "CALL sp_with_params(?, ?)", query)
	assert.Equal(t, []any{42, "hello"}, args)
}

func TestBuildCallStatement_WithOutParams(t *testing.T) {
	params := []StoredProcedureParam{
		{Name: "inParam", Direction: ParamDirectionIn, Value: "input"},
		{Name: "outParam", Direction: ParamDirectionOut, Value: nil},
		{Name: "inoutParam", Direction: ParamDirectionInOut, Value: 123},
	}
	_, _, err := buildCallStatement("sp_in_out", params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported")
}

func TestExtractStoredProcedureParameters_Array(t *testing.T) {
	exchange := NewExchange(context.Background())
	params := []StoredProcedureParam{
		{Name: "id", Direction: ParamDirectionIn, Value: 1},
		{Name: "name", Direction: ParamDirectionIn, Value: "Alice"},
	}
	exchange.GetIn().SetBody(params)

	result, err := extractStoredProcedureParameters(exchange)
	require.NoError(t, err)
	require.Len(t, result, 2)
	assert.Equal(t, "id", result[0].Name)
	assert.Equal(t, 1, result[0].Value)
	assert.Equal(t, "name", result[1].Name)
	assert.Equal(t, "Alice", result[1].Value)
}

func TestExtractStoredProcedureParameters_Map(t *testing.T) {
	exchange := NewExchange(context.Background())
	bodyMap := map[string]any{
		"userId":   42,
		"userName": "Bob",
		"active":   true,
	}
	exchange.GetIn().SetBody(bodyMap)

	result, err := extractStoredProcedureParameters(exchange)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "[]StoredProcedureParam")
}

func TestExtractStoredProcedureParameters_Nil(t *testing.T) {
	exchange := NewExchange(context.Background())
	exchange.GetIn().SetBody(nil)

	result, err := extractStoredProcedureParameters(exchange)
	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestSQLStoredComponent_DefaultDataSource(t *testing.T) {
	db := newTestDB(t)
	comp := NewSQLStoredComponent()
	comp.SetDefaultDataSource(db)

	// Use a datasource name that wasn't explicitly registered
	// Should fall back to default datasource
	uri := "sql-stored://anyname?procedure=sp_test&noop=true"
	endpoint, err := comp.CreateEndpoint(uri)
	require.NoError(t, err)

	// Verify endpoint was created
	storedEndpoint, ok := endpoint.(*SQLStoredEndpoint)
	require.True(t, ok)
	assert.Equal(t, "anyname", storedEndpoint.name)

	// Verify it works by creating producer
	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)

	exchange := NewExchange(context.Background())
	exchange.GetIn().SetBody(nil)
	err = producer.Send(exchange)
	require.NoError(t, err)

	// In noop mode, should get result
	result, ok := exchange.GetOut().GetBody().(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "sp_test", result["procedure"])
}

type storedStubState struct {
	resultSets    [][][]driver.Value
	cols          []string
	commitErrFn   func() error
	nextPanic     any
	nextCalls     atomic.Int32
	rowsClosed    atomic.Bool
	rollbackCalls atomic.Int32
}

func newStoredStubDB(t *testing.T, state *storedStubState) *sql.DB {
	t.Helper()
	db := sql.OpenDB(&storedStubConnector{state: state})
	t.Cleanup(func() { _ = db.Close() })
	return db
}

type storedStubConnector struct{ state *storedStubState }

func (c *storedStubConnector) Connect(context.Context) (driver.Conn, error) {
	return &storedStubConn{state: c.state}, nil
}
func (c *storedStubConnector) Driver() driver.Driver { return storedStubDriver{} }

type storedStubDriver struct{}

func (storedStubDriver) Open(string) (driver.Conn, error) { return nil, errors.New("unused") }

type storedStubConn struct{ state *storedStubState }

func (c *storedStubConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unused")
}
func (c *storedStubConn) Close() error              { return nil }
func (c *storedStubConn) Begin() (driver.Tx, error) { return &storedStubTx{state: c.state}, nil }
func (c *storedStubConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return &storedStubTx{state: c.state}, nil
}
func (c *storedStubConn) QueryContext(_ context.Context, _ string, _ []driver.NamedValue) (driver.Rows, error) {
	return &storedStubRows{state: c.state}, nil
}

type storedStubTx struct{ state *storedStubState }

func (tx *storedStubTx) Commit() error {
	if tx.state.commitErrFn != nil {
		return tx.state.commitErrFn()
	}
	return nil
}
func (tx *storedStubTx) Rollback() error {
	tx.state.rollbackCalls.Add(1)
	return nil
}

type storedStubRows struct {
	state *storedStubState
	rsIdx int
	row   int
}

func (r *storedStubRows) Columns() []string { return r.state.cols }
func (r *storedStubRows) Close() error {
	r.state.rowsClosed.Store(true)
	return nil
}
func (r *storedStubRows) Next(dest []driver.Value) error {
	r.state.nextCalls.Add(1)
	if r.state.nextPanic != nil {
		panic(r.state.nextPanic)
	}
	if r.rsIdx >= len(r.state.resultSets) {
		return io.EOF
	}
	rs := r.state.resultSets[r.rsIdx]
	if r.row >= len(rs) {
		return io.EOF
	}
	copy(dest, rs[r.row])
	r.row++
	return nil
}
func (r *storedStubRows) HasNextResultSet() bool {
	return r.rsIdx+1 < len(r.state.resultSets)
}
func (r *storedStubRows) NextResultSet() error {
	if r.rsIdx+1 >= len(r.state.resultSets) {
		return io.EOF
	}
	r.rsIdx++
	r.row = 0
	return nil
}

func TestSQLStored_RejectsUnorderedMapForPositionalCall(t *testing.T) {
	t.Run("rejects map[string]any with clear error recommending []StoredProcedureParam", func(t *testing.T) {
		db := newTestDB(t)
		comp := NewSQLStoredComponent()
		comp.RegisterDataSource("testdb", db)

		ep, err := comp.CreateEndpoint("sql-stored://testdb?procedure=sp_add&noop=true")
		require.NoError(t, err)
		prod, err := ep.CreateProducer()
		require.NoError(t, err)

		ex := NewExchange(context.Background())
		ex.GetIn().SetBody(map[string]any{"a": 1, "b": 2})

		err = prod.Send(ex)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "[]StoredProcedureParam")
	})

	t.Run("rejects OUT and INOUT parameters with explicit unsupported error", func(t *testing.T) {
		db := newStoredStubDB(t, &storedStubState{cols: []string{"ok"}})
		comp := NewSQLStoredComponent()
		comp.RegisterDataSource("testdb", db)

		ep, err := comp.CreateEndpoint("sql-stored://testdb?procedure=sp_out")
		require.NoError(t, err)
		prod, err := ep.CreateProducer()
		require.NoError(t, err)

		for _, dir := range []StoredProcedureParamDirection{ParamDirectionOut, ParamDirectionInOut} {
			ex := NewExchange(context.Background())
			ex.GetIn().SetBody([]StoredProcedureParam{
				{Name: "p", Direction: dir, Value: 1},
			})
			err = prod.Send(ex)
			require.Error(t, err, "direction %s must not silently drop outputs", dir)
			assert.Contains(t, err.Error(), "unsupported")
		}
	})
}

func TestSQLStored_SelectOneZeroRowsClearsInBodyAndStopsAfterFirstRow(t *testing.T) {
	t.Run("zero rows clears In.Body across 2-step route and GetResponse", func(t *testing.T) {
		db := newStoredStubDB(t, &storedStubState{
			cols:       []string{"id", "name"},
			resultSets: [][][]driver.Value{{}},
		})
		camel := NewCamelContext()
		comp := NewSQLStoredComponent()
		comp.RegisterDataSource("testdb", db)
		camel.AddComponent("sql-stored", comp)

		var downstreamBody any
		route, err := camel.CreateRouteBuilder().
			From("direct:callSP").
			To("sql-stored://testdb?procedure=sp_find&outputType=SelectOne").
			ProcessFunc(func(ex *Exchange) error {
				downstreamBody = ex.GetIn().GetBody()
				return nil
			}).
			Build()
		require.NoError(t, err)
		camel.AddRoute(route)

		require.NoError(t, camel.Start())
		t.Cleanup(func() { _ = camel.Stop() })

		ep, err := camel.CreateEndpoint("direct:callSP")
		require.NoError(t, err)
		prod, err := ep.CreateProducer()
		require.NoError(t, err)

		ex := NewExchange(context.Background())
		ex.GetIn().SetBody([]StoredProcedureParam{
			{Name: "id", Direction: ParamDirectionIn, Value: 999},
		})
		require.NoError(t, prod.Send(ex))

		assert.Nil(t, downstreamBody, "downstream step must observe In.Body == nil on 0-row SelectOne")
		assert.Nil(t, ex.GetResponse().GetBody(), "GetResponse().GetBody() must be nil on 0-row SelectOne")
	})

	t.Run("stops scanning after first row", func(t *testing.T) {
		state := &storedStubState{
			cols: []string{"id", "name"},
			resultSets: [][][]driver.Value{
				{
					{int64(1), "Alice"},
					{int64(2), "Bob"},
					{int64(3), "Carol"},
				},
			},
		}
		db := newStoredStubDB(t, state)
		comp := NewSQLStoredComponent()
		comp.RegisterDataSource("testdb", db)

		ep, err := comp.CreateEndpoint("sql-stored://testdb?procedure=sp_list&outputType=SelectOne")
		require.NoError(t, err)
		prod, err := ep.CreateProducer()
		require.NoError(t, err)

		ex := NewExchange(context.Background())
		require.NoError(t, prod.Send(ex))

		row, ok := ex.GetOut().GetBody().(map[string]any)
		require.True(t, ok)
		assert.Equal(t, int64(1), row["id"])
		assert.Equal(t, int32(1), state.nextCalls.Load(), "SelectOne must stop after first row")
		assert.True(t, state.rowsClosed.Load(), "rows must be closed")
	})
}

func TestSQLStored_EnvCannotEnableAllowHeaderOverride(t *testing.T) {
	t.Setenv("allowHeaderOverride", "true")
	t.Setenv("SQL_STORED_ALLOWHEADEROVERRIDE", "true")
	t.Setenv("procedure", "sp_from_env")
	t.Setenv("SQL_STORED_PROCEDURE", "sp_from_env")

	db := newTestDB(t)
	comp := NewSQLStoredComponent()
	comp.RegisterDataSource("testdb", db)

	ep, err := comp.CreateEndpoint("sql-stored://testdb?procedure=sp_endpoint_procedure&noop=true")
	require.NoError(t, err)
	prod, err := ep.CreateProducer()
	require.NoError(t, err)

	ex := NewExchange(context.Background())
	ex.GetIn().SetHeader(SqlStoredProcedureName, "sp_attacker")
	require.NoError(t, prod.Send(ex))

	result, ok := ex.GetOut().GetBody().(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "sp_endpoint_procedure", result["procedure"], "env vars must not override URI procedure or enable allowHeaderOverride")
}

func TestSQLStored_SendTxDoesNotMutateOutBeforeCommitFail(t *testing.T) {
	var attempt atomic.Int32
	state := &storedStubState{
		cols:       []string{"id"},
		resultSets: [][][]driver.Value{{{int64(42)}}},
		commitErrFn: func() error {
			if attempt.Add(1) == 1 {
				return errors.New("simulated commit failure")
			}
			return nil
		},
	}
	db := newStoredStubDB(t, state)
	comp := NewSQLStoredComponent()
	comp.RegisterDataSource("testdb", db)

	ep, err := comp.CreateEndpoint("sql-stored://testdb?transacted=true&procedure=sp_tx")
	require.NoError(t, err)
	prod, err := ep.CreateProducer()
	require.NoError(t, err)

	params := []StoredProcedureParam{{Name: "id", Direction: ParamDirectionIn, Value: 7}}
	ex := NewExchange(context.Background())
	ex.GetIn().SetBody(params)

	err = prod.Send(ex)
	require.Error(t, err)
	assert.Nil(t, ex.GetOut().GetBody(), "Out.Body must remain untouched after Commit failure")
	assert.Empty(t, ex.GetOut().GetHeaders(), "Out.Headers must remain untouched after Commit failure")
	assert.Equal(t, params, ex.GetIn().GetBody(), "In.Body must be preserved for retry")

	require.NoError(t, prod.Send(ex))
	rows, ok := ex.GetOut().GetBody().([]map[string]any)
	require.True(t, ok)
	require.Len(t, rows, 1)
	assert.Equal(t, int64(42), rows[0]["id"])
}

func TestSQLStored_SendTxSelectOneZeroRowsCommitFailPreservesInBody(t *testing.T) {
	state := &storedStubState{
		cols:        []string{"id"},
		resultSets:  [][][]driver.Value{{}},
		commitErrFn: func() error { return errors.New("commit failed") },
	}
	db := newStoredStubDB(t, state)
	comp := NewSQLStoredComponent()
	comp.RegisterDataSource("testdb", db)

	ep, err := comp.CreateEndpoint("sql-stored://testdb?transacted=true&outputType=SelectOne&procedure=sp_tx_empty")
	require.NoError(t, err)
	prod, err := ep.CreateProducer()
	require.NoError(t, err)

	params := []StoredProcedureParam{{Name: "id", Direction: ParamDirectionIn, Value: 999}}
	ex := NewExchange(context.Background())
	ex.GetIn().SetBody(params)

	err = prod.Send(ex)
	require.Error(t, err)
	assert.Nil(t, ex.GetOut().GetBody())
	assert.Empty(t, ex.GetOut().GetHeaders())
	assert.Equal(t, params, ex.GetIn().GetBody(), "In.Body must not be cleared if tx.Commit fails")
}

func TestSQLStored_SendTxRollsBackOnPanic(t *testing.T) {
	state := &storedStubState{
		cols:       []string{"id"},
		resultSets: [][][]driver.Value{{{int64(1)}}},
		nextPanic:  "boom inside stored scan",
	}
	db := newStoredStubDB(t, state)
	comp := NewSQLStoredComponent()
	comp.RegisterDataSource("testdb", db)

	ep, err := comp.CreateEndpoint("sql-stored://testdb?transacted=true&procedure=sp_panic")
	require.NoError(t, err)
	prod, err := ep.CreateProducer()
	require.NoError(t, err)

	ex := NewExchange(context.Background())
	assert.Panics(t, func() {
		_ = prod.Send(ex)
	})
	assert.True(t, state.rowsClosed.Load(), "rows must be closed on panic")
	assert.GreaterOrEqual(t, state.rollbackCalls.Load(), int32(1), "transaction must roll back on panic")
}

func TestSQLStoredComponent_RegisterDataSource_RejectsNilDB(t *testing.T) {
	comp := NewSQLStoredComponent()
	comp.RegisterDataSource("nildb", nil)

	_, err := comp.CreateEndpoint("sql-stored://nildb?procedure=sp_test")
	require.Error(t, err, "nil *sql.DB must not be accepted as a valid datasource")
}

func TestSQLStoredComponent_CreateEndpoint_EagerValidation(t *testing.T) {
	db := newTestDB(t)
	comp := NewSQLStoredComponent()
	comp.RegisterDataSource("testdb", db)

	_, err := comp.CreateEndpoint("sql-stored://testdb?procedure=sp_test&outputType=Bogus")
	assert.Error(t, err, "invalid outputType must fail in CreateEndpoint")

	_, err = comp.CreateEndpoint("sql-stored://testdb?procedure=sp_test&transacted=maybe")
	assert.Error(t, err, "invalid boolean transacted must fail in CreateEndpoint")

	_, err = comp.CreateEndpoint("sql-stored://testdb?procedure=sp_test&noop=nah")
	assert.Error(t, err, "invalid boolean noop must fail in CreateEndpoint")

	_, err = comp.CreateEndpoint("sql-stored://testdb?procedure=sp_test&allowHeaderOverride=yes")
	assert.Error(t, err, "invalid boolean allowHeaderOverride must fail in CreateEndpoint")
}
