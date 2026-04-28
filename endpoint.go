package gocamel

import (
	"context"
)

// Endpoint represents an endpoint in a route
type Endpoint interface {
	// URI retourne l'URI de l'endpoint
	URI() string
	// CreateProducer crée un producteur pour cet endpoint
	CreateProducer() (Producer, error)
	// CreateConsumer crée un consommateur pour cet endpoint
	CreateConsumer(processor Processor) (Consumer, error)
}

// Producer représente un producteur de messages
type Producer interface {
	// Start démarre le producteur
	Start(ctx context.Context) error
	// Stop arrête le producteur
	Stop() error
	// Send envoie un message
	Send(exchange *Exchange) error
}

// Consumer représente un consommateur de messages
type Consumer interface {
	// Start démarre le consommateur
	Start(ctx context.Context) error
	// Stop arrête le consommateur
	Stop() error
}

// Component représente un composant qui peut créer des endpoints
type Component interface {
	// CreateEndpoint crée un nouvel endpoint à partir d'une URI
	CreateEndpoint(uri string) (Endpoint, error)
}
