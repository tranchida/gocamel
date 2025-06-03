package gocamel

import (
	"context"
	"fmt"
	"sync"
)

// Processor définit l'interface pour le traitement des messages
type Processor interface {
	Process(exchange *Exchange) error
}

// ProcessorFunc est un type de fonction qui implémente l'interface Processor
type ProcessorFunc func(*Exchange) error

// Process implémente l'interface Processor pour ProcessorFunc
func (f ProcessorFunc) Process(exchange *Exchange) error {
	return f(exchange)
}

// Route représente une route dans le système
type Route struct {
	ID          string
	Description string
	Group       string
	context     *CamelContext
	from        Endpoint
	processors  []Processor
	started     bool
	startLock   sync.Mutex
}

// NewRoute crée une nouvelle instance de Route
func NewRoute() *Route {
	return &Route{
		processors: make([]Processor, 0),
	}
}

// From définit l'endpoint source de la route
func (r *Route) From(uri string) *Route {
	endpoint, err := r.context.CreateEndpoint(uri)
	if err != nil {
		panic(fmt.Sprintf("erreur lors de la création de l'endpoint: %v", err))
	}
	r.from = endpoint
	return r
}

// AddProcessor ajoute un processeur à la route
func (r *Route) AddProcessor(processor Processor) *Route {
	r.processors = append(r.processors, processor)
	return r
}

// ProcessFunc ajoute une fonction de traitement à la route
func (r *Route) ProcessFunc(f func(*Exchange) error) *Route {
	return r.AddProcessor(ProcessorFunc(f))
}

// Process implémente l'interface Processor
func (r *Route) Process(exchange *Exchange) error {
	for _, processor := range r.processors {
		if err := processor.Process(exchange); err != nil {
			return err
		}
	}
	return nil
}

// Start démarre la route
func (r *Route) Start(ctx context.Context) error {
	r.startLock.Lock()
	defer r.startLock.Unlock()

	if r.started {
		return fmt.Errorf("la route est déjà démarrée")
	}

	if r.from == nil {
		return fmt.Errorf("aucun endpoint source défini")
	}

	consumer, err := r.from.CreateConsumer(r)
	if err != nil {
		return fmt.Errorf("erreur lors de la création du consommateur: %v", err)
	}

	if err := consumer.Start(ctx); err != nil {
		return fmt.Errorf("erreur lors du démarrage du consommateur: %v", err)
	}

	r.started = true
	return nil
}

// Stop arrête la route
func (r *Route) Stop() error {
	r.startLock.Lock()
	defer r.startLock.Unlock()

	if !r.started {
		return nil
	}

	if r.from != nil {
		consumer, err := r.from.CreateConsumer(r)
		if err != nil {
			return fmt.Errorf("erreur lors de la création du consommateur: %v", err)
		}

		if err := consumer.Stop(); err != nil {
			return fmt.Errorf("erreur lors de l'arrêt du consommateur: %v", err)
		}
	}

	r.started = false
	return nil
}

// IsStarted vérifie si la route est démarrée
func (r *Route) IsStarted() bool {
	r.startLock.Lock()
	defer r.startLock.Unlock()
	return r.started
}

// SetID définit l'ID de la route
func (r *Route) SetID(id string) *Route {
	r.ID = id
	return r
}

// SetDescription définit la description de la route
func (r *Route) SetDescription(description string) *Route {
	r.Description = description
	return r
}

// SetGroup définit le groupe de la route
func (r *Route) SetGroup(group string) *Route {
	r.Group = group
	return r
}

// GetContext récupère le contexte Camel associé à la route
func (r *Route) GetContext() *CamelContext {
	return r.context
}

// processorFunc est une implémentation de l'interface Processor
type processorFunc func(*Exchange) error

func (f processorFunc) Process(exchange *Exchange) error {
	return f(exchange)
}

// routeProcessor est un processeur qui gère le traitement complet d'une route
type routeProcessor struct {
	processors []Processor
	to         []string
	registry   *Registry
}

// Process exécute tous les processeurs de la route et envoie le message aux destinations
func (p *routeProcessor) Process(exchange *Exchange) error {
	// Exécution des processeurs
	for _, processor := range p.processors {
		if err := processor.Process(exchange); err != nil {
			return fmt.Errorf("erreur lors du traitement: %v", err)
		}
	}

	// Envoi aux destinations
	for _, toURI := range p.to {
		endpoint, err := p.registry.CreateEndpoint(toURI)
		if err != nil {
			return fmt.Errorf("erreur lors de la création de l'endpoint de destination: %v", err)
		}

		producer, err := endpoint.CreateProducer()
		if err != nil {
			return fmt.Errorf("erreur lors de la création du producteur: %v", err)
		}

		if err := producer.Send(exchange); err != nil {
			return fmt.Errorf("erreur lors de l'envoi du message: %v", err)
		}
	}

	return nil
}
