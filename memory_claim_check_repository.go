package gocamel

import (
	"container/list"
	"context"
	"sync"
)

// DefaultMemoryClaimCheckRepositorySize is the default maximum number of
// claim check entries retained before least-recently-used entries are evicted.
const DefaultMemoryClaimCheckRepositorySize = 10_000

type claimCheckEntry struct {
	key  string
	data any
}

// MemoryClaimCheckRepository is a bounded, thread-safe in-memory implementation
// of ClaimCheckRepository and ClaimCheckLookupRepository.
type MemoryClaimCheckRepository struct {
	mu      sync.Mutex
	store   map[string]*list.Element
	order   *list.List // front = most recently used, back = least recently used
	maxSize int
}

// NewMemoryClaimCheckRepository creates a new MemoryClaimCheckRepository bounded
// by DefaultMemoryClaimCheckRepositorySize.
func NewMemoryClaimCheckRepository() *MemoryClaimCheckRepository {
	return NewMemoryClaimCheckRepositoryWithCapacity(DefaultMemoryClaimCheckRepositorySize)
}

// NewMemoryClaimCheckRepositoryWithCapacity creates a new MemoryClaimCheckRepository
// with the given maximum capacity (n <= 0 means unbounded).
func NewMemoryClaimCheckRepositoryWithCapacity(maxSize int) *MemoryClaimCheckRepository {
	if maxSize < 0 {
		maxSize = 0
	}
	return &MemoryClaimCheckRepository{
		store:   make(map[string]*list.Element),
		order:   list.New(),
		maxSize: maxSize,
	}
}

// SetMaxSize updates the maximum number of entries retained (n <= 0 disables
// the bound) and evicts least-recently-used entries above the new limit.
func (r *MemoryClaimCheckRepository) SetMaxSize(n int) *MemoryClaimCheckRepository {
	if n < 0 {
		n = 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.maxSize = n
	r.evictOverflowLocked(n)
	return r
}

func (r *MemoryClaimCheckRepository) evictOverflowLocked(limit int) {
	if limit <= 0 || r.order == nil {
		return
	}
	for len(r.store) > limit && r.order.Len() > 0 {
		back := r.order.Back()
		r.order.Remove(back)
		entry := back.Value.(*claimCheckEntry)
		delete(r.store, entry.key)
	}
}

func (r *MemoryClaimCheckRepository) putLocked(key string, copied any) {
	if r.store == nil {
		r.store = make(map[string]*list.Element)
	}
	if r.order == nil {
		r.order = list.New()
	}
	if elem, exists := r.store[key]; exists {
		elem.Value.(*claimCheckEntry).data = copied
		r.order.MoveToFront(elem)
		return
	}
	if r.maxSize > 0 {
		r.evictOverflowLocked(r.maxSize - 1)
	}
	elem := r.order.PushFront(&claimCheckEntry{key: key, data: copied})
	r.store[key] = elem
}

// Add stores the data under a freshly generated UUID key and returns that key.
func (r *MemoryClaimCheckRepository) Add(ctx context.Context, data any) (string, error) {
	key := generateUUID()
	copied := deepCopyValue(data)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.putLocked(key, copied)
	return key, nil
}

// Set stores the data with a specific key.
func (r *MemoryClaimCheckRepository) Set(ctx context.Context, key string, data any) error {
	copied := deepCopyValue(data)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.putLocked(key, copied)
	return nil
}

// Lookup retrieves the data for key and reports whether key existed.
func (r *MemoryClaimCheckRepository) Lookup(ctx context.Context, key string) (any, bool, error) {
	r.mu.Lock()
	elem, exists := r.store[key]
	if !exists {
		r.mu.Unlock()
		return nil, false, nil
	}
	if r.order != nil {
		r.order.MoveToFront(elem)
	}
	data := elem.Value.(*claimCheckEntry).data
	r.mu.Unlock()
	return deepCopyValue(data), true, nil
}

// Get retrieves the data for the given key.
func (r *MemoryClaimCheckRepository) Get(ctx context.Context, key string) (any, error) {
	data, _, err := r.Lookup(ctx, key)
	return data, err
}

// Pop removes the data for key, returning it and reporting whether key existed.
func (r *MemoryClaimCheckRepository) Pop(ctx context.Context, key string) (any, bool, error) {
	r.mu.Lock()
	elem, exists := r.store[key]
	if !exists {
		r.mu.Unlock()
		return nil, false, nil
	}
	if r.order != nil {
		r.order.Remove(elem)
	}
	delete(r.store, key)
	data := elem.Value.(*claimCheckEntry).data
	r.mu.Unlock()
	return deepCopyValue(data), true, nil
}

// Remove removes the data for the given key and returns it.
func (r *MemoryClaimCheckRepository) Remove(ctx context.Context, key string) (any, error) {
	data, _, err := r.Pop(ctx, key)
	return data, err
}
