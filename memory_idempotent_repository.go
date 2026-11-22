package gocamel

import (
	"container/list"
	"context"
	"sync"
)

// DefaultMemoryIdempotentRepositorySize is the default maximum number of keys
// retained by MemoryIdempotentRepository before oldest entries are evicted.
const DefaultMemoryIdempotentRepositorySize = 100_000

// MemoryIdempotentRepository is a bounded, thread-safe in-memory implementation
// of IdempotentRepository.
type MemoryIdempotentRepository struct {
	mu      sync.RWMutex
	store   map[string]*list.Element
	order   *list.List // front = newest/most recently seen, back = oldest
	maxSize int
}

// NewMemoryIdempotentRepository creates a new MemoryIdempotentRepository bounded
// by DefaultMemoryIdempotentRepositorySize.
func NewMemoryIdempotentRepository() *MemoryIdempotentRepository {
	return NewMemoryIdempotentRepositoryWithCapacity(DefaultMemoryIdempotentRepositorySize)
}

// NewMemoryIdempotentRepositoryWithCapacity creates a new MemoryIdempotentRepository
// with the given maximum capacity (n <= 0 means unbounded).
func NewMemoryIdempotentRepositoryWithCapacity(maxSize int) *MemoryIdempotentRepository {
	if maxSize < 0 {
		maxSize = 0
	}
	return &MemoryIdempotentRepository{
		store:   make(map[string]*list.Element),
		order:   list.New(),
		maxSize: maxSize,
	}
}

// SetMaxSize updates the maximum number of keys retained (n <= 0 disables the
// bound) and evicts oldest entries above the new limit.
func (r *MemoryIdempotentRepository) SetMaxSize(n int) *MemoryIdempotentRepository {
	if n < 0 {
		n = 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.maxSize = n
	r.evictOverflowLocked(n)
	return r
}

func (r *MemoryIdempotentRepository) evictOverflowLocked(limit int) {
	if limit <= 0 || r.order == nil {
		return
	}
	for len(r.store) > limit && r.order.Len() > 0 {
		back := r.order.Back()
		r.order.Remove(back)
		delete(r.store, back.Value.(string))
	}
}

// Add adds the key to the repository. Returns true if added, false if already exists.
func (r *MemoryIdempotentRepository) Add(ctx context.Context, key string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.store == nil {
		r.store = make(map[string]*list.Element)
	}
	if r.order == nil {
		r.order = list.New()
	}
	if elem, exists := r.store[key]; exists {
		r.order.MoveToFront(elem)
		return false, nil
	}
	if r.maxSize > 0 {
		r.evictOverflowLocked(r.maxSize - 1)
	}
	elem := r.order.PushFront(key)
	r.store[key] = elem
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
	if elem, exists := r.store[key]; exists {
		if r.order != nil {
			r.order.Remove(elem)
		}
		delete(r.store, key)
	}
	return nil
}

// Clear removes all keys from the repository.
func (r *MemoryIdempotentRepository) Clear(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.store = make(map[string]*list.Element)
	r.order = list.New()
	return nil
}
