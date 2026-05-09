package gocamel

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
)

// MemoryClaimCheckRepository is an in-memory implementation of ClaimCheckRepository.
type MemoryClaimCheckRepository struct {
	mu      sync.RWMutex
	store   map[string]any
	counter atomic.Uint64
}

// NewMemoryClaimCheckRepository creates a new MemoryClaimCheckRepository instance.
func NewMemoryClaimCheckRepository() *MemoryClaimCheckRepository {
	return &MemoryClaimCheckRepository{
		store: make(map[string]any),
	}
}

// Add stores the data and returns a unique key.
func (r *MemoryClaimCheckRepository) Add(ctx context.Context, data any) (string, error) {
	key := fmt.Sprintf("cc-%d", r.counter.Add(1))
	r.mu.Lock()
	defer r.mu.Unlock()
	r.store[key] = data
	return key, nil
}

// Set stores the data with a specific key.
func (r *MemoryClaimCheckRepository) Set(ctx context.Context, key string, data any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.store[key] = data
	return nil
}

// Get retrieves the data for the given key.
func (r *MemoryClaimCheckRepository) Get(ctx context.Context, key string) (any, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	data, exists := r.store[key]
	if !exists {
		return nil, nil
	}
	return data, nil
}

// Remove removes the data for the given key and returns it.
func (r *MemoryClaimCheckRepository) Remove(ctx context.Context, key string) (any, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	data, exists := r.store[key]
	if !exists {
		return nil, nil
	}
	delete(r.store, key)
	return data, nil
}
