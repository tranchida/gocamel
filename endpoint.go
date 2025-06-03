package gocamel

import (
	"context"
	"fmt"
	"net/url"
)

// Endpoint représente un point de terminaison dans une route
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

// Registry gère l'enregistrement des composants
type Registry struct {
	components map[string]Component
}

// NewRegistry crée une nouvelle instance de Registry
func NewRegistry() *Registry {
	return &Registry{
		components: make(map[string]Component),
	}
}

// RegisterComponent enregistre un nouveau composant
func (r *Registry) RegisterComponent(scheme string, component Component) {
	r.components[scheme] = component
}

// CreateEndpoint crée un endpoint à partir d'une URI
func (r *Registry) CreateEndpoint(uri string) (Endpoint, error) {
	parsedURL, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("URI invalide: %v", err)
	}

	component, exists := r.components[parsedURL.Scheme]
	if !exists {
		return nil, fmt.Errorf("aucun composant trouvé pour le schéma: %s", parsedURL.Scheme)
	}

	return component.CreateEndpoint(uri)
}
