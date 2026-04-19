package gocamel

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ErrStopRouting is a special error used to stop
// routing of a message without it being considered a failure.
// Used notably by the Aggregator EIP.
var ErrStopRouting = errors.New("stop routing")

// Processor définit l'interface pour le traitement des messages
type Processor interface {
	Process(exchange *Exchange) error
}

// ProcessorContainer définit l'interface pour les objets pouvant contenir des processeurs.
// Cela permet de supporter l'imbrication des EIP comme le Splitter.
type ProcessorContainer interface {
	AddProcessor(processor Processor)
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
	consumer    Consumer
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
func (r *Route) AddProcessor(processor Processor) {
	r.processors = append(r.processors, processor)
}

// ProcessFunc ajoute une fonction de traitement à la route
func (r *Route) ProcessFunc(f func(*Exchange) error) *Route {
	r.AddProcessor(ProcessorFunc(f))
	return r
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
	r.consumer = consumer

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

	if r.consumer != nil {
		if err := r.consumer.Stop(); err != nil {
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
			if errors.Is(err, ErrStopRouting) {
				return err // Bubble up
			}
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

// SetBody définit le corps du message de sortie
func (r *Route) SetBody(body interface{}) *Route {
	return r.ProcessFunc(func(exchange *Exchange) error {
		exchange.GetOut().SetBody(body)
		return nil
	})
}

// SetHeader définit un en-tête du message de sortie
func (r *Route) SetHeader(key string, value interface{}) *Route {
	return r.ProcessFunc(func(exchange *Exchange) error {
		exchange.GetOut().SetHeader(key, value)
		return nil
	})
}

// To ajoute un ou plusieurs endpoints de destination à la route.
// Si plusieurs URIs sont fournies, un Multicast est créé automatiquement.
func (r *Route) To(uris ...string) *Route {
	if len(uris) == 0 {
		return r
	}
	if len(uris) == 1 {
		r.AddProcessor(createToProcessor(r.context, uris[0]))
	} else {
		m := NewMulticast()
		for _, uri := range uris {
			m.AddProcessor(createToProcessor(r.context, uri))
		}
		r.AddProcessor(m)
	}
	return r
}

// ToD ajoute un ou plusieurs processeurs dynamiques d'envoi à une URI calculée à chaque échange.
// Si plusieurs templates sont fournis, un Multicast est créé automatiquement.
func (r *Route) ToD(uriTemplates ...string) *Route {
	if len(uriTemplates) == 0 {
		return r
	}
	if len(uriTemplates) == 1 {
		r.AddProcessor(createToDProcessor(r.context, uriTemplates[0]))
	} else {
		m := NewMulticast()
		for _, uriTemplate := range uriTemplates {
			m.AddProcessor(createToDProcessor(r.context, uriTemplate))
		}
		r.AddProcessor(m)
	}
	return r
}

func createToProcessor(context *CamelContext, uri string) Processor {
	var (
		once     sync.Once
		producer Producer
		initErr  error
	)
	return ProcessorFunc(func(exchange *Exchange) error {
		once.Do(func() {
			endpoint, err := context.CreateEndpoint(uri)
			if err != nil {
				initErr = err
				return
			}
			p, err := endpoint.CreateProducer()
			if err != nil {
				initErr = err
				return
			}
			if err := p.Start(exchange.Context); err != nil {
				initErr = err
				return
			}
			producer = p
		})
		if initErr != nil {
			return initErr
		}

		// Propagation de la sortie vers l'entrée si une modification a eu lieu
		if outBody := exchange.GetOut().GetBody(); outBody != nil {
			exchange.GetIn().SetBody(outBody)
		}
		for k, v := range exchange.GetOut().GetHeaders() {
			exchange.GetIn().SetHeader(k, v)
		}

		return producer.Send(exchange)
	})
}

func createToDProcessor(context *CamelContext, uriTemplate string) Processor {
	return ProcessorFunc(func(exchange *Exchange) error {
		// Résolution de l'URI dynamique
		uri := Interpolate(uriTemplate, exchange)

		// Création de l'endpoint et du producer à chaque fois (pour ToD)
		// TODO: Optimiser avec un cache de producers si nécessaire
		endpoint, err := context.CreateEndpoint(uri)
		if err != nil {
			return fmt.Errorf("toD dynamic endpoint creation error: %w", err)
		}

		producer, err := endpoint.CreateProducer()
		if err != nil {
			return fmt.Errorf("toD producer creation error: %w", err)
		}

		if err := producer.Start(exchange.Context); err != nil {
			return fmt.Errorf("toD producer start error: %w", err)
		}
		// On s'assure que le producer est arrêté à la fin (ou on laisse le GC s'en charger si l'endpoint est éphémère ?)
		// Normalement dans Camel, les producteurs dynamiques sont mis en cache.
		// Si on ne met pas en cache, on devrait probablement arrêter le producteur après l'envoi.
		defer producer.Stop()

		// Propagation de la sortie vers l'entrée si une modification a eu lieu
		if outBody := exchange.GetOut().GetBody(); outBody != nil {
			exchange.GetIn().SetBody(outBody)
		}
		for k, v := range exchange.GetOut().GetHeaders() {
			exchange.GetIn().SetHeader(k, v)
		}

		return producer.Send(exchange)
	})
}
