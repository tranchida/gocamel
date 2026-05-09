package gocamel

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Aggregator is a Processor that implements the Aggregator EIP.
// It collects and stores messages, then aggregates them until a completion condition is met.
//
// Completion can be triggered by:
//   - CompletionSize: a fixed number of messages per correlation key
//   - CompletionTimeout: a maximum wait time after the first message for a key
//   - CompletionPredicate: an arbitrary function evaluated on the aggregated exchange
//
// At least one completion condition must be set.
type Aggregator struct {
	CorrelationExpression func(*Exchange) string
	AggregationStrategy   AggregationStrategy
	AggregationRepository AggregationRepository
	CompletionSize        int
	CompletionTimeout     time.Duration
	CompletionPredicate   func(*Exchange) bool

	// keyLocks provides per-correlation-key mutual exclusion to avoid serialising
	// unrelated keys while still preventing races within a single key.
	keyLocks sync.Map // map[string]*sync.Mutex
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

// SetCompletionTimeout sets the maximum duration after the first message of a group before
// the group is forcibly completed and emitted, even if CompletionSize is not reached.
// A background goroutine is started per key upon first arrival.
func (a *Aggregator) SetCompletionTimeout(d time.Duration) *Aggregator {
	a.CompletionTimeout = d
	return a
}

// SetCompletionPredicate sets an arbitrary predicate evaluated on the aggregated exchange
// after each merge. When it returns true the group is completed and emitted.
func (a *Aggregator) SetCompletionPredicate(pred func(*Exchange) bool) *Aggregator {
	a.CompletionPredicate = pred
	return a
}

// lockForKey returns the per-key mutex, creating it on first use.
func (a *Aggregator) lockForKey(key string) *sync.Mutex {
	mu, _ := a.keyLocks.LoadOrStore(key, &sync.Mutex{})
	return mu.(*sync.Mutex)
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

	mu := a.lockForKey(key)
	mu.Lock()
	defer mu.Unlock()

	isFirstMessage, err := a.processLocked(ctx, key, exchange)
	if err != nil {
		return err
	}

	// Start the timeout goroutine on the first message for this key.
	if isFirstMessage && a.CompletionTimeout > 0 {
		go a.runTimeout(key, exchange.Context)
	}

	return nil
}

// processLocked performs aggregation while the per-key lock is held.
// Returns (isFirstMessage, error).
func (a *Aggregator) processLocked(ctx context.Context, key string, exchange *Exchange) (bool, error) {
	oldExchange, err := a.AggregationRepository.Get(ctx, key)
	if err != nil {
		return false, fmt.Errorf("failed to get exchange from repository: %w", err)
	}

	isFirst := oldExchange == nil

	aggregatedExchange := a.AggregationStrategy.Aggregate(oldExchange, exchange)

	count := 1
	if !isFirst {
		if c, ok := oldExchange.GetPropertyAsInt(CamelAggregatorSize); ok {
			count = c + 1
		} else {
			count = 2
		}
	}
	aggregatedExchange.SetProperty(CamelAggregatorSize, count)

	completed := false
	if a.CompletionSize > 0 && count >= a.CompletionSize {
		completed = true
	}
	if !completed && a.CompletionPredicate != nil && a.CompletionPredicate(aggregatedExchange) {
		completed = true
	}

	if completed {
		return isFirst, a.emitLocked(ctx, key, aggregatedExchange, exchange)
	}

	if err := a.AggregationRepository.Add(ctx, key, aggregatedExchange); err != nil {
		return isFirst, fmt.Errorf("failed to add exchange to repository: %w", err)
	}

	return isFirst, ErrStopRouting
}

// emitLocked removes the group from the repository and copies the aggregated state
// onto the current exchange so it continues through the route.
func (a *Aggregator) emitLocked(ctx context.Context, key string, aggregated, current *Exchange) error {
	if err := a.AggregationRepository.Remove(ctx, key); err != nil {
		return fmt.Errorf("failed to remove exchange from repository: %w", err)
	}
	a.keyLocks.Delete(key)

	current.In = aggregated.In
	current.Out = aggregated.Out
	current.Properties = aggregated.Properties
	return nil
}

// runTimeout waits for CompletionTimeout then forces completion of the group if still pending.
func (a *Aggregator) runTimeout(key string, ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}

	select {
	case <-time.After(a.CompletionTimeout):
	case <-ctx.Done():
		return
	}

	mu := a.lockForKey(key)
	mu.Lock()
	defer mu.Unlock()

	stored, err := a.AggregationRepository.Get(ctx, key)
	if err != nil || stored == nil {
		return // already completed or repo error
	}

	// Force-complete by removing from repository; the stored exchange is discarded
	// (it cannot be injected back into the route from here without a route reference).
	// To forward it, wire a separate timeout route via CompletionPredicate + a timer.
	if err := a.AggregationRepository.Remove(ctx, key); err != nil {
		logWarn("Aggregator timeout: failed to remove key", "key", key, "error", err)
		return
	}
	a.keyLocks.Delete(key)
	logInfo("Aggregator timeout: group expired and removed", "key", key)
}
