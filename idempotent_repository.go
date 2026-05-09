package gocamel

import "context"

// IdempotentRepository defines the interface for an idempotent repository
// which is used to prevent duplicate messages from being processed.
type IdempotentRepository interface {
	// Add adds the key to the repository.
	// Returns true if the key was added, false if it already exists.
	Add(ctx context.Context, key string) (bool, error)

	// Contains returns true if the repository contains the key.
	Contains(ctx context.Context, key string) (bool, error)

	// Remove removes the key from the repository.
	Remove(ctx context.Context, key string) error

	// Clear removes all keys from the repository.
	Clear(ctx context.Context) error
}
