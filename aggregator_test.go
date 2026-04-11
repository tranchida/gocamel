package gocamel

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// StringConcatStrategy est une stratégie d'agrégation simple qui concatène les corps de messages.
type StringConcatStrategy struct{}

func (s *StringConcatStrategy) Aggregate(oldExchange, newExchange *Exchange) *Exchange {
	if oldExchange == nil {
		return newExchange
	}

	oldBody := ""
	if b, ok := oldExchange.GetIn().GetBody().(string); ok {
		oldBody = b
	}

	newBody := ""
	if b, ok := newExchange.GetIn().GetBody().(string); ok {
		newBody = b
	}

	oldExchange.GetIn().SetBody(oldBody + "," + newBody)
	return oldExchange
}

func TestAggregator_CompletionSize(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryAggregationRepository()
	strategy := &StringConcatStrategy{}

	correlationExpr := func(exchange *Exchange) string {
		if key, ok := exchange.GetHeader("group"); ok {
			return fmt.Sprintf("%v", key)
		}
		return "default"
	}

	aggregator := NewAggregator(correlationExpr, strategy, repo).SetCompletionSize(3)

	// Exchange 1
	ex1 := NewExchange(ctx)
	ex1.GetIn().SetHeader("group", "A")
	ex1.GetIn().SetBody("msg1")

	err := aggregator.Process(ex1)
	assert.ErrorIs(t, err, ErrStopRouting, "First message should stop routing")

	// Verify in repo
	savedEx, err := repo.Get(ctx, "A")
	assert.NoError(t, err)
	assert.NotNil(t, savedEx)
	assert.Equal(t, "msg1", savedEx.GetIn().GetBody())

	// Exchange 2
	ex2 := NewExchange(ctx)
	ex2.GetIn().SetHeader("group", "A")
	ex2.GetIn().SetBody("msg2")

	err = aggregator.Process(ex2)
	assert.ErrorIs(t, err, ErrStopRouting, "Second message should stop routing")

	// Verify in repo
	savedEx, err = repo.Get(ctx, "A")
	assert.NoError(t, err)
	assert.NotNil(t, savedEx)
	assert.Equal(t, "msg1,msg2", savedEx.GetIn().GetBody())

	// Exchange 3 (completes)
	ex3 := NewExchange(ctx)
	ex3.GetIn().SetHeader("group", "A")
	ex3.GetIn().SetBody("msg3")

	err = aggregator.Process(ex3)
	assert.NoError(t, err, "Third message should complete aggregation and continue routing")

	// Verify that ex3 now contains the aggregated result
	assert.Equal(t, "msg1,msg2,msg3", ex3.GetIn().GetBody())

	// Verify repo is empty for this key
	savedEx, err = repo.Get(ctx, "A")
	assert.NoError(t, err)
	assert.Nil(t, savedEx, "Repository should be cleared after completion")
}
