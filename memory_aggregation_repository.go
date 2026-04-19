package gocamel

import (
	"context"
	"sync"
)

// MemoryAggregationRepository is an in-memory implementation of AggregationRepository.
type MemoryAggregationRepository struct {
	mu    sync.RWMutex
	store map[string]*Exchange
}

// NewMemoryAggregationRepository creates a new MemoryAggregationRepository instance.
func NewMemoryAggregationRepository() *MemoryAggregationRepository {
	return &MemoryAggregationRepository{
		store: make(map[string]*Exchange),
	}
}

// Add adds or updates an exchange in the repository.
func (r *MemoryAggregationRepository) Add(ctx context.Context, key string, exchange *Exchange) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.store[key] = exchange
	return nil
}

// Get retrieves an exchange by its key. Returns nil if it doesn't exist.
func (r *MemoryAggregationRepository) Get(ctx context.Context, key string) (*Exchange, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	exchange, exists := r.store[key]
	if !exists {
		return nil, nil
	}
	return exchange, nil
}

// Remove removes an exchange from the repository.
func (r *MemoryAggregationRepository) Remove(ctx context.Context, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.store, key)
	return nil
}
