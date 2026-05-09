package gocamel

import (
	"context"
	"sync"
)

// MemoryIdempotentRepository is an in-memory implementation of IdempotentRepository.
type MemoryIdempotentRepository struct {
	mu    sync.RWMutex
	store map[string]struct{}
}

// NewMemoryIdempotentRepository creates a new MemoryIdempotentRepository instance.
func NewMemoryIdempotentRepository() *MemoryIdempotentRepository {
	return &MemoryIdempotentRepository{
		store: make(map[string]struct{}),
	}
}

// Add adds the key to the repository. Returns true if added, false if already exists.
func (r *MemoryIdempotentRepository) Add(ctx context.Context, key string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.store[key]; exists {
		return false, nil
	}
	r.store[key] = struct{}{}
	return true, nil
}

// Contains returns true if the repository contains the key.
func (r *MemoryIdempotentRepository) Contains(ctx context.Context, key string) (bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, exists := r.store[key]
	return exists, nil
}

// Remove removes the key from the repository.
func (r *MemoryIdempotentRepository) Remove(ctx context.Context, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.store, key)
	return nil
}

// Clear removes all keys from the repository.
func (r *MemoryIdempotentRepository) Clear(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.store = make(map[string]struct{})
	return nil
}
