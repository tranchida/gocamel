package gocamel

import "context"

// ClaimCheckRepository defines the interface for storing and retrieving
// message data for the Claim Check EIP pattern.
type ClaimCheckRepository interface {
	// Add stores the data and returns a unique key.
	Add(ctx context.Context, data any) (string, error)

	// Set stores the data with a specific key.
	Set(ctx context.Context, key string, data any) error

	// Get retrieves the data for the given key.
	Get(ctx context.Context, key string) (any, error)

	// Remove removes the data for the given key and returns it.
	Remove(ctx context.Context, key string) (any, error)
}

// ClaimCheckLookupRepository is an optional extension of ClaimCheckRepository
// that distinguishes a missing key from a key whose stored payload is nil.
type ClaimCheckLookupRepository interface {
	ClaimCheckRepository
	Lookup(ctx context.Context, key string) (data any, exists bool, err error)
	Pop(ctx context.Context, key string) (data any, exists bool, err error)
}
