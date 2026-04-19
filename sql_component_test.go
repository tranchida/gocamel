package gocamel

import (
	"context"
	"database/sql"
	"net/url"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestSQLDB crée une base SQLite en mémoire peuplée de quelques utilisateurs.
func newTestSQLDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:")
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

	// Vérifie la persistance.
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

	uri := "sql://testdb?query=" + url.QueryEscape("SELECT 1")
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

func TestSQLProducer_InterpolateHeader(t *testing.T) {
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

	require.NoError(t, producer.Send(exchange))

	results, ok := exchange.GetOut().GetBody().([]map[string]any)
	require.True(t, ok)
	require.Len(t, results, 1)
	assert.Equal(t, "Alice", results[0]["name"])
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

	// Une deuxième insertion avec la même clé doit remonter une erreur (rollback implicite).
	exchange = NewExchange(context.Background())
	exchange.GetIn().SetHeader(SqlParameters, []any{10, "Eve2", "eve2@example.com"})
	err = producer.Send(exchange)
	assert.Error(t, err)

	var stored string
	require.NoError(t, db.QueryRow("SELECT name FROM users WHERE id = 10").Scan(&stored))
	assert.Equal(t, "Eve", stored, "la transaction en échec ne doit pas modifier la ligne existante")
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
