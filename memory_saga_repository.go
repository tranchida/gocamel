package gocamel

import (
	"context"
	"sort"
	"sync"
	"time"
)

// MemorySagaRepository is an in-memory implementation of SagaRepository. It is
// the SagaProcessor default: state survives a route or context restart within
// the same process, but not a process crash. Use SQLSagaRepository for that.
type MemorySagaRepository struct {
	mu    sync.RWMutex // Protects store.
	store map[string]SagaState
}

// NewMemorySagaRepository creates a new MemorySagaRepository instance.
func NewMemorySagaRepository() *MemorySagaRepository {
	return &MemorySagaRepository{
		store: make(map[string]SagaState),
	}
}

// Save stores a copy of the saga state.
func (r *MemorySagaRepository) Save(ctx context.Context, sagaID string, state SagaState) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.store[sagaID] = state.clone()
	return nil
}

// Load returns a copy of the saga state, or ErrSagaNotFound.
func (r *MemorySagaRepository) Load(ctx context.Context, sagaID string) (SagaState, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	state, ok := r.store[sagaID]
	if !ok {
		return SagaState{}, ErrSagaNotFound
	}
	return state.clone(), nil
}

// ListIncomplete returns up to DefaultSagaListLimit running or compensating
// saga IDs, oldest first.
func (r *MemorySagaRepository) ListIncomplete(ctx context.Context) ([]string, error) {
	type entry struct {
		id      string
		updated time.Time
	}
	r.mu.RLock()
	pending := make([]entry, 0)
	for id, state := range r.store {
		if state.Status.isIncomplete() {
			pending = append(pending, entry{id: id, updated: state.UpdatedAt})
		}
	}
	r.mu.RUnlock()

	sort.Slice(pending, func(i, j int) bool {
		if !pending[i].updated.Equal(pending[j].updated) {
			return pending[i].updated.Before(pending[j].updated)
		}
		return pending[i].id < pending[j].id
	})
	if len(pending) > DefaultSagaListLimit {
		pending = pending[:DefaultSagaListLimit]
	}
	ids := make([]string, len(pending))
	for i, e := range pending {
		ids[i] = e.id
	}
	return ids, nil
}

// Remove deletes the saga state.
func (r *MemorySagaRepository) Remove(ctx context.Context, sagaID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.store, sagaID)
	return nil
}
