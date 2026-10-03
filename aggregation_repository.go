package gocamel

import "context"

// AggregationRepository defines the interface for storing exchanges being aggregated.
type AggregationRepository interface {
	// Add adds or updates an exchange in the repository.
	Add(ctx context.Context, key string, exchange *Exchange) error

	// Get retrieves an exchange by its correlation key. Returns (nil, nil) if it doesn't exist.
	Get(ctx context.Context, key string) (*Exchange, error)

	// Remove removes an exchange from the repository.
	Remove(ctx context.Context, key string) error
}

// ListableAggregationRepository is an AggregationRepository that can list the
// correlation keys of the groups it holds. When a route with a completion
// timeout starts, the Aggregator uses it to re-arm the timeout of groups left
// pending by a previous run (a restarted route, or a persistent repository
// after a process restart); otherwise such groups could only complete by size
// or predicate. MemoryAggregationRepository and SQLAggregationRepository
// implement it.
type ListableAggregationRepository interface {
	AggregationRepository
	// Keys returns the correlation keys of the stored groups.
	Keys(ctx context.Context) ([]string, error)
}
