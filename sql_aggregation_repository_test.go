package gocamel

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
)

func TestSQLAggregationRepository_AddGetRemove(t *testing.T) {
	// Create an in-memory SQLite database
	db, err := sql.Open("sqlite3", ":memory:")
	assert.NoError(t, err)
	defer db.Close()

	opts := SQLAggregationOptions{
		TableName:      "test_aggregations",
		UseDollarParam: false,
	}
	repo := NewSQLAggregationRepository(db, opts)
	ctx := context.Background()

	err = repo.InitDB(ctx)
	assert.NoError(t, err)

	// 1. Get non-existent
	exchange, err := repo.Get(ctx, "key1")
	assert.NoError(t, err)
	assert.Nil(t, exchange)

	// 2. Add
	ex1 := NewExchange(ctx)
	ex1.GetIn().SetBody("Test Body")
	ex1.GetIn().SetHeader("Header1", "Value1")
	ex1.SetProperty("Prop1", "PropValue1")

	// Resetting time just to avoid unmarshal minor differences in formatting if they occur
	now := time.Now().Truncate(time.Second)
	ex1.Created = now
	ex1.Modified = now

	err = repo.Add(ctx, "key1", ex1)
	assert.NoError(t, err)

	// 3. Get existing
	exchange, err = repo.Get(ctx, "key1")
	assert.NoError(t, err)
	assert.NotNil(t, exchange)

	assert.Equal(t, "Test Body", exchange.GetIn().GetBody())

	header, exists := exchange.GetHeader("Header1")
	assert.True(t, exists)
	assert.Equal(t, "Value1", header)

	prop, exists := exchange.GetProperty("Prop1")
	assert.True(t, exists)
	assert.Equal(t, "PropValue1", prop)

	// 4. Update existing
	ex2 := NewExchange(ctx)
	ex2.GetIn().SetBody("Updated Body")
	err = repo.Add(ctx, "key1", ex2)
	assert.NoError(t, err)

	exchange, err = repo.Get(ctx, "key1")
	assert.NoError(t, err)
	assert.NotNil(t, exchange)
	assert.Equal(t, "Updated Body", exchange.GetIn().GetBody())

	// 5. Remove
	err = repo.Remove(ctx, "key1")
	assert.NoError(t, err)

	exchange, err = repo.Get(ctx, "key1")
	assert.NoError(t, err)
	assert.Nil(t, exchange)
}
