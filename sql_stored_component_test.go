package gocamel

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestDB creates an in-memory SQLite database with a test procedure
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:")
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

	// Create endpoint with one procedure name
	uri := "sql-stored://testdb?procedure=sp_endpoint_procedure&noop=true"
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
	query, args, outIndices := buildCallStatement("sp_no_params", nil)
	assert.Equal(t, "CALL sp_no_params()", query)
	assert.Nil(t, args)
	assert.Nil(t, outIndices)
}

func TestBuildCallStatement_InParams(t *testing.T) {
	params := []StoredProcedureParam{
		{Name: "param1", Direction: ParamDirectionIn, Value: 42},
		{Name: "param2", Direction: ParamDirectionIn, Value: "hello"},
	}
	query, args, outIndices := buildCallStatement("sp_with_params", params)
	assert.Equal(t, "CALL sp_with_params(?, ?)", query)
	assert.Equal(t, []any{42, "hello"}, args)
	assert.Nil(t, outIndices) // No OUT params
}

func TestBuildCallStatement_WithOutParams(t *testing.T) {
	params := []StoredProcedureParam{
		{Name: "inParam", Direction: ParamDirectionIn, Value: "input"},
		{Name: "outParam", Direction: ParamDirectionOut, Value: nil},
		{Name: "inoutParam", Direction: ParamDirectionInOut, Value: 123},
	}
	query, args, outIndices := buildCallStatement("sp_in_out", params)
	assert.Equal(t, "CALL sp_in_out(?, ?, ?)", query)
	assert.Equal(t, []any{"input", nil, 123}, args)
	assert.Equal(t, []int{1}, outIndices) // Only pure OUT param at index 1
}

func TestExtractStoredProcedureParameters_Array(t *testing.T) {
	exchange := NewExchange(context.Background())
	params := []StoredProcedureParam{
		{Name: "id", Direction: ParamDirectionIn, Value: 1},
		{Name: "name", Direction: ParamDirectionIn, Value: "Alice"},
	}
	exchange.GetIn().SetBody(params)

	result := extractStoredProcedureParameters(exchange)
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

	result := extractStoredProcedureParameters(exchange)
	require.Len(t, result, 3)

	// Convert to map for easier assertions
	resultMap := make(map[string]StoredProcedureParam)
	for _, p := range result {
		resultMap[p.Name] = p
	}

	assert.Equal(t, 42, resultMap["userId"].Value)
	assert.Equal(t, "Bob", resultMap["userName"].Value)
	assert.Equal(t, true, resultMap["active"].Value)

	// All should be IN direction
	for _, p := range result {
		assert.Equal(t, ParamDirectionIn, p.Direction)
	}
}

func TestExtractStoredProcedureParameters_Nil(t *testing.T) {
	exchange := NewExchange(context.Background())
	exchange.GetIn().SetBody(nil)

	result := extractStoredProcedureParameters(exchange)
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
