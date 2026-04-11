package gocamel

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMemoryAggregationRepository_AddGetRemove(t *testing.T) {
	repo := NewMemoryAggregationRepository()
	ctx := context.Background()

	// 1. Get non-existent
	exchange, err := repo.Get(ctx, "key1")
	assert.NoError(t, err)
	assert.Nil(t, exchange)

	// 2. Add
	ex1 := NewExchange(ctx)
	ex1.GetIn().SetBody("Test Body")
	err = repo.Add(ctx, "key1", ex1)
	assert.NoError(t, err)

	// 3. Get existing
	exchange, err = repo.Get(ctx, "key1")
	assert.NoError(t, err)
	assert.NotNil(t, exchange)
	assert.Equal(t, "Test Body", exchange.GetIn().GetBody())

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
