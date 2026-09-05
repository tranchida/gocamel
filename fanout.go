package gocamel

import (
	"errors"
	"fmt"
	"sync"
)

// processFanOut owns the lifecycle of every branch. Parallel workers only run
// processors; aggregation and completion run in input order after they join.
// This keeps user strategies and callbacks from racing over aggregated state.
func processFanOut(branches []*Exchange, parallel bool, process func(int, *Exchange) error, strategy AggregationStrategy) (*Exchange, error) {
	results := make([]error, len(branches))
	if parallel {
		var wg sync.WaitGroup
		for i, branch := range branches {
			wg.Go(func() { results[i] = safeProcess(func() error { return process(i, branch) }) })
		}
		wg.Wait()
	}
	var aggregated *Exchange
	var firstErr error
	for i, branch := range branches {
		if !parallel {
			results[i] = safeProcess(func() error { return process(i, branch) })
		}
		err := results[i]
		if errors.Is(err, ErrStopRouting) {
			err = nil
		}
		if err == nil && strategy != nil {
			err = safeProcess(func() error {
				aggregated = strategy.Aggregate(aggregated, branch)
				return nil
			})
		}
		completionErr := safeProcess(func() error { branch.Done(err); return nil })
		err = errors.Join(err, completionErr)
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("branch %d: %w", i, err)
		}
		if err != nil && !parallel {
			break
		}
	}
	return aggregated, firstErr
}
