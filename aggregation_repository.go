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
