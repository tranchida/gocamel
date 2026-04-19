package gocamel

import (
	"context"
	"fmt"
	"sync"
)

// Aggregator is a Processor that implements the Aggregator EIP.
// It collects and stores messages, then aggregates them until a completion condition is met.
type Aggregator struct {
	CorrelationExpression func(*Exchange) string
	AggregationStrategy   AggregationStrategy
	AggregationRepository AggregationRepository
	CompletionSize        int

	// mu protects concurrent access to the processing of the same correlation key
	// For a distributed system, a distributed lock would be needed.
	mu sync.Mutex
}

// NewAggregator creates a new Aggregator processor.
func NewAggregator(
	correlationExpr func(*Exchange) string,
	strategy AggregationStrategy,
	repo AggregationRepository,
) *Aggregator {
	return &Aggregator{
		CorrelationExpression: correlationExpr,
		AggregationStrategy:   strategy,
		AggregationRepository: repo,
	}
}

// SetCompletionSize sets the number of messages required to complete the aggregation.
func (a *Aggregator) SetCompletionSize(size int) *Aggregator {
	a.CompletionSize = size
	return a
}

// Process handles the arrival of a new exchange.
func (a *Aggregator) Process(exchange *Exchange) error {
	ctx := exchange.Context
	if ctx == nil {
		ctx = context.Background()
	}

	key := a.CorrelationExpression(exchange)
	if key == "" {
		return fmt.Errorf("correlation key evaluated to empty string")
	}

	// Simplification: a global lock for the aggregator to avoid race conditions
	// when accessing the repository concurrently. In a real implementation, one could
	// have a lock per correlation key or rely on DB transactions.
	a.mu.Lock()
	defer a.mu.Unlock()

	// Retrieve the old exchange
	oldExchange, err := a.AggregationRepository.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("failed to get exchange from repository: %w", err)
	}

	// Apply the aggregation strategy
	aggregatedExchange := a.AggregationStrategy.Aggregate(oldExchange, exchange)

	// Manage the count for completion
	count := 1
	if oldExchange != nil {
		if c, ok := oldExchange.GetPropertyAsInt("CamelAggregatorSize"); ok {
			count = c + 1
		} else {
			count = 2
		}
	}
	aggregatedExchange.SetProperty("CamelAggregatorSize", count)

	if a.CompletionSize > 0 && count >= a.CompletionSize {
		// Completed: remove from repository
		if err := a.AggregationRepository.Remove(ctx, key); err != nil {
			return fmt.Errorf("failed to remove exchange from repository: %w", err)
		}

		// The current exchange that continues in the route becomes the aggregated exchange
		exchange.In = aggregatedExchange.In
		exchange.Out = aggregatedExchange.Out
		exchange.Properties = aggregatedExchange.Properties

		// Let the exchange continue normally in the route
		return nil
	}

	// Not completed: save to repository and stop routing for THIS exchange
	if err := a.AggregationRepository.Add(ctx, key, aggregatedExchange); err != nil {
		return fmt.Errorf("failed to add exchange to repository: %w", err)
	}

	return ErrStopRouting
}
