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
	if exchange != nil {
		r.store[key] = exchange.DeepCopy()
	} else {
		r.store[key] = nil
	}
	return nil
}

// Get retrieves an exchange by its key. Returns nil if it doesn't exist.
func (r *MemoryAggregationRepository) Get(ctx context.Context, key string) (*Exchange, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	exchange, exists := r.store[key]
	if !exists || exchange == nil {
		return nil, nil
	}
	return exchange.DeepCopy(), nil
}

// Keys returns the correlation keys of the stored groups.
func (r *MemoryAggregationRepository) Keys(ctx context.Context) ([]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	keys := make([]string, 0, len(r.store))
	for key := range r.store {
		keys = append(keys, key)
	}
	return keys, nil
}

// Remove removes an exchange from the repository.
func (r *MemoryAggregationRepository) Remove(ctx context.Context, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.store, key)
	return nil
}
