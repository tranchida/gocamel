package gocamel

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

// DefaultMaxConcurrency bounds how many parts of a parallel Split or
// recipients of a parallel RecipientList are processed at the same time
// (Apache Camel's default thread pool size). Both expand at runtime from
// message content, so an unbounded fan-out would start one goroutine per
// element, e.g. a million for a million-line file.
const DefaultMaxConcurrency = 10

// effectiveMaxConcurrency returns n, or DefaultMaxConcurrency when n <= 0.
func effectiveMaxConcurrency(n int) int {
	if n <= 0 {
		return DefaultMaxConcurrency
	}
	return n
}

// processFanOut owns the lifecycle of every branch. Parallel workers only run
// processors; aggregation and completion run in input order after they join.
// This keeps user strategies and callbacks from racing over aggregated state.
// In parallel mode at most maxConcurrency branches run at the same time;
// maxConcurrency <= 0 runs every branch in its own goroutine.
func processFanOut(branches []*Exchange, parallel bool, maxConcurrency int, process func(int, *Exchange) error, strategy AggregationStrategy) (*Exchange, error) {
	results := make([]error, len(branches))
	if parallel {
		workers := len(branches)
		if maxConcurrency > 0 && maxConcurrency < workers {
			workers = maxConcurrency
		}
		var next atomic.Int64
		var wg sync.WaitGroup
		for range workers {
			wg.Go(func() {
				for {
					i := int(next.Add(1) - 1)
					if i >= len(branches) {
						return
					}
					results[i] = safeProcess(func() error { return process(i, branches[i]) })
				}
			})
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
