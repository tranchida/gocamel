package gocamel

import (
	"context"
	"errors"
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
	keyLocks sync.Map // map[string]*keyState

	// lifecycleMu coordinates timeout goroutines with Route.Stop. A configured
	// timeout resumes at the processor immediately following this Aggregator.
	lifecycleMu       sync.Mutex
	timeoutContext    context.Context
	timeoutCancel     context.CancelFunc
	timeoutProcessor  Processor
	timeoutConfigured bool
	timeoutActive     bool
	timeoutWG         sync.WaitGroup
}

// keyState holds the per-key mutex and a generation counter.
// gen is incremented each time a new group starts; timeout goroutines compare
// against it to avoid expiring a group that was replaced by a newer one.
type keyState struct {
	mu  sync.Mutex
	gen uint64
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

// lockForKey returns the per-key state, creating it on first use, and locks it.
//
// The load/lock pair is retried until the state is still the one registered for
// the key after the lock is acquired. Removing a keyState on completion
// (emitLocked, runTimeout) would otherwise let one goroutine wait on a retired
// state while another creates a fresh one, leaving two goroutines operating on
// the same correlation key under two different mutexes — and therefore racing
// on the aggregation repository.
func (a *Aggregator) lockForKey(key string) *keyState {
	for {
		st, _ := a.keyLocks.LoadOrStore(key, &keyState{})
		state := st.(*keyState)
		state.mu.Lock()

		current, ok := a.keyLocks.Load(key)
		if ok && current.(*keyState) == state {
			return state
		}
		// The state was retired while we waited; drop it and take the new one.
		state.mu.Unlock()
	}
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

	st := a.lockForKey(key) // returns with st.mu held
	defer st.mu.Unlock()

	isFirstMessage, err := a.processLocked(ctx, key, exchange, st)

	// Start the timeout goroutine on the first message for this key.
	if isFirstMessage && a.CompletionTimeout > 0 && errors.Is(err, ErrStopRouting) {
		a.startTimeout(key, ctx, st.gen, st)
	}
	return err
}

// processLocked performs aggregation while the per-key lock is held.
// Returns (isFirstMessage, error).
func (a *Aggregator) processLocked(ctx context.Context, key string, exchange *Exchange, st *keyState) (bool, error) {
	oldExchange, err := a.AggregationRepository.Get(ctx, key)
	if err != nil {
		return false, fmt.Errorf("failed to get exchange from repository: %w", err)
	}

	isFirst := oldExchange == nil
	if isFirst {
		st.gen++ // new group started; invalidates any pending timeout for the previous group
	}

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
		return isFirst, a.emitLocked(ctx, key, aggregatedExchange, exchange, st)
	}

	if err := a.AggregationRepository.Add(ctx, key, aggregatedExchange); err != nil {
		return isFirst, fmt.Errorf("failed to add exchange to repository: %w", err)
	}

	return isFirst, ErrStopRouting
}

// emitLocked removes the group from the repository and copies the aggregated state
// onto the current exchange so it continues through the route.
func (a *Aggregator) emitLocked(ctx context.Context, key string, aggregated, current *Exchange, st *keyState) error {
	if err := a.AggregationRepository.Remove(ctx, key); err != nil {
		return fmt.Errorf("failed to remove exchange from repository: %w", err)
	}
	st.gen++ // invalidate the timeout belonging to the completed group
	a.keyLocks.CompareAndDelete(key, st)

	publishAggregatedResult(current, aggregated)
	return nil
}

// configureTimeout associates timeout completions with the remainder of a
// running route. It is intentionally internal: the route owns this lifecycle.
func (a *Aggregator) configureTimeout(ctx context.Context, processor Processor) {
	if ctx == nil {
		ctx = context.Background()
	}

	a.lifecycleMu.Lock()
	a.timeoutContext, a.timeoutCancel = context.WithCancel(ctx)
	a.timeoutProcessor = processor
	a.timeoutConfigured = true
	a.timeoutActive = true
	a.lifecycleMu.Unlock()
}

func (a *Aggregator) startTimeout(key string, fallback context.Context, gen uint64, st *keyState) {
	a.lifecycleMu.Lock()
	if a.timeoutConfigured && !a.timeoutActive {
		a.lifecycleMu.Unlock()
		return
	}
	ctx := a.timeoutContext
	processor := a.timeoutProcessor
	if ctx == nil {
		ctx = fallback
	}
	if ctx == nil {
		ctx = context.Background()
	}
	a.timeoutWG.Add(1)
	a.lifecycleMu.Unlock()

	go func() {
		defer a.timeoutWG.Done()
		a.runTimeout(key, ctx, gen, st, processor)
	}()
}

// Stop cancels pending completion timeouts and waits for callbacks that have
// already started. A later Route.Start configures the Aggregator again.
func (a *Aggregator) Stop() error {
	a.lifecycleMu.Lock()
	if a.timeoutConfigured {
		a.timeoutActive = false
	}
	cancel := a.timeoutCancel
	a.timeoutCancel = nil
	a.timeoutContext = nil
	a.timeoutProcessor = nil
	a.lifecycleMu.Unlock()

	if cancel != nil {
		cancel()
	}
	a.timeoutWG.Wait()
	return nil
}

// runTimeout waits for CompletionTimeout then forces completion of the group if still pending.
// gen is matched against st.gen to ensure we only expire the group we were started for;
// if a newer group has started with the same key in the meantime, we bail out.
func (a *Aggregator) runTimeout(key string, ctx context.Context, gen uint64, st *keyState, processor Processor) {
	if ctx == nil {
		ctx = context.Background()
	}

	timer := time.NewTimer(a.CompletionTimeout)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		return
	}

	st.mu.Lock()

	if st.gen != gen {
		st.mu.Unlock()
		return // a newer group has started for this key; leave it alone
	}

	stored, err := a.AggregationRepository.Get(ctx, key)
	if err != nil {
		st.mu.Unlock()
		LogWarn("Aggregator timeout: failed to get key", "key", key, "error", err)
		return
	}
	if stored == nil {
		st.mu.Unlock()
		return // already completed
	}

	if err := a.AggregationRepository.Remove(ctx, key); err != nil {
		st.mu.Unlock()
		LogWarn("Aggregator timeout: failed to remove key", "key", key, "error", err)
		return
	}
	st.gen++
	a.keyLocks.CompareAndDelete(key, st)
	st.mu.Unlock()

	// The originating exchange may come from a short-lived request context
	// (for example an HTTP consumer). Timeout completion belongs to the route
	// lifecycle, so downstream work must use the timeout's route context.
	stored.Context = ctx
	if processor != nil {
		err := safeProcess(func() error { return processor.Process(stored) })
		if err != nil && !errors.Is(err, ErrStopRouting) {
			LogError("Aggregator timeout: downstream processing failed",
				"key", key,
				"exchange_id", stored.ID,
				"error", err,
			)
		}
	}
	LogDebug("Aggregator timeout: group completed",
		"key", key,
		"exchange_id", stored.ID,
		"size", stored.GetPropertyOrDefault(CamelAggregatorSize, 0),
	)
}
