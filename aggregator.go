package gocamel

import (
	"context"
	"fmt"
	"sync"
)

// Aggregator est un Processor qui implémente l'EIP Aggregator.
// Il collecte et stocke les messages, puis les agrège jusqu'à ce qu'une condition de complétion soit remplie.
type Aggregator struct {
	CorrelationExpression func(*Exchange) string
	AggregationStrategy   AggregationStrategy
	AggregationRepository AggregationRepository
	CompletionSize        int

	// mu protège l'accès concurrent au traitement d'une même clé de corrélation
	// Pour un système distribué, il faudrait un lock distribué.
	mu sync.Mutex
}

// NewAggregator crée un nouveau processeur Aggregator.
func NewAggregator(
	correlationExpr func(*Exchange) string,
	strategy AggregationStrategy,
	repo AggregationRepository,
) *Aggregator {
	return &Aggregator{
		CorrelationExpression: correlationExpr,
		AggregationStrategy:   strategy,
		AggregationRepository: repo,
	}
}

// SetCompletionSize définit le nombre de messages requis pour compléter l'agrégation.
func (a *Aggregator) SetCompletionSize(size int) *Aggregator {
	a.CompletionSize = size
	return a
}

// Process gère l'arrivée d'un nouvel échange.
func (a *Aggregator) Process(exchange *Exchange) error {
	ctx := exchange.Context
	if ctx == nil {
		ctx = context.Background()
	}

	key := a.CorrelationExpression(exchange)
	if key == "" {
		return fmt.Errorf("correlation key evaluated to empty string")
	}

	// Simplification : un lock global pour l'aggregator afin d'éviter les conditions de course
	// lors de l'accès au repository concurrent. Dans une vraie implémentation, on pourrait
	// avoir un lock par clé de corrélation ou s'appuyer sur les transactions du DB.
	a.mu.Lock()
	defer a.mu.Unlock()

	// Récupérer l'ancien échange
	oldExchange, err := a.AggregationRepository.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("failed to get exchange from repository: %w", err)
	}

	// Appliquer la stratégie d'agrégation
	aggregatedExchange := a.AggregationStrategy.Aggregate(oldExchange, exchange)

	// Gérer le compte pour la complétion
	count := 1
	if oldExchange != nil {
		if c, ok := oldExchange.GetPropertyAsInt("CamelAggregatorSize"); ok {
			count = c + 1
		} else {
			count = 2
		}
	}
	aggregatedExchange.SetProperty("CamelAggregatorSize", count)

	// Vérifier la condition de complétion
	if a.CompletionSize > 0 && count >= a.CompletionSize {
		// Complété : supprimer du repository
		if err := a.AggregationRepository.Remove(ctx, key); err != nil {
			return fmt.Errorf("failed to remove exchange from repository: %w", err)
		}

		// L'échange actuel qui continue dans la route devient l'échange agrégé
		exchange.In = aggregatedExchange.In
		exchange.Out = aggregatedExchange.Out
		exchange.Properties = aggregatedExchange.Properties

		// On laisse l'échange continuer normalement dans la route
		return nil
	}

	// Non complété : sauvegarder dans le repository et stopper le routage de CET échange
	if err := a.AggregationRepository.Add(ctx, key, aggregatedExchange); err != nil {
		return fmt.Errorf("failed to add exchange to repository: %w", err)
	}

	return ErrStopRouting
}
