package gocamel

import "context"

// AggregationRepository définit l'interface pour stocker les échanges en cours d'agrégation.
type AggregationRepository interface {
	// Add ajoute ou met à jour un échange dans le repository.
	Add(ctx context.Context, key string, exchange *Exchange) error

	// Get récupère un échange par sa clé de corrélation. Retourne (nil, nil) s'il n'existe pas.
	Get(ctx context.Context, key string) (*Exchange, error)

	// Remove supprime un échange du repository.
	Remove(ctx context.Context, key string) error
}
