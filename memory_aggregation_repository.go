package gocamel

import (
	"context"
	"sync"
)

// MemoryAggregationRepository est une implémentation en mémoire de AggregationRepository.
type MemoryAggregationRepository struct {
	mu    sync.RWMutex
	store map[string]*Exchange
}

// NewMemoryAggregationRepository crée une nouvelle instance de MemoryAggregationRepository.
func NewMemoryAggregationRepository() *MemoryAggregationRepository {
	return &MemoryAggregationRepository{
		store: make(map[string]*Exchange),
	}
}

// Add ajoute ou met à jour un échange dans le repository.
func (r *MemoryAggregationRepository) Add(ctx context.Context, key string, exchange *Exchange) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.store[key] = exchange
	return nil
}

// Get récupère un échange par sa clé. Retourne nil s'il n'existe pas.
func (r *MemoryAggregationRepository) Get(ctx context.Context, key string) (*Exchange, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	exchange, exists := r.store[key]
	if !exists {
		return nil, nil
	}
	return exchange, nil
}

// Remove supprime un échange du repository.
func (r *MemoryAggregationRepository) Remove(ctx context.Context, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.store, key)
	return nil
}
