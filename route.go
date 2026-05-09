package gocamel

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
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

// Route represents a Camel route: a pipeline of Processors fed by a single Consumer.
// Routes are built via RouteBuilder (fluent DSL) and managed by CamelContext.
type Route struct {
	ID           string     // Unique identifier for this route.
	Description  string     // Human-readable description of the route purpose.
	Group        string     // Logical group this route belongs to.
	Transacted   bool       // Whether the route uses transactional semantics.
	context      *CamelContext
	from         Endpoint
	consumer     Consumer
	processors   []Processor
	started      bool
	startLock    sync.RWMutex                       // Protects started state for concurrent access.
	err          error                              // Accumulated during construction, surfaced at Start().
	errorHandler ErrorHandler                        // Error handler for this route (nil means propagate).
	metrics      bool                               // Whether Prometheus metrics are enabled for this route.
	tracing      bool                               // Whether OpenTelemetry tracing is enabled for this route.
}

// NewRoute crée une nouvelle instance de Route
func NewRoute() *Route {
	return &Route{
		processors: make([]Processor, 0),
	}
}

// SetTransacted définit si la route est transactionnelle
func (r *Route) SetTransacted(transacted bool) *Route {
	r.Transacted = transacted
	return r
}

// From définit l'endpoint source de la route.
// Errors are stored and surfaced at Start() time.
func (r *Route) From(uri string) *Route {
	endpoint, err := r.context.CreateEndpoint(uri)
	if err != nil {
		r.err = fmt.Errorf("erreur lors de la création de l'endpoint: %w", err)
		return r
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

// Process implémente l'interface Processor.
// Errors are delegated to the configured ErrorHandler if one is set.
func (r *Route) Process(exchange *Exchange) error {
	if r.metrics && r.tracing {
		return r.metricsProcess(exchange, func(e *Exchange) error {
			return r.traceProcess(e)
		})
	}
	if r.metrics {
		return r.metricsProcess(exchange, r.doProcess)
	}
	if r.tracing {
		return r.traceProcess(exchange)
	}
	return r.doProcess(exchange)
}

func (r *Route) metricsProcess(exchange *Exchange, next func(*Exchange) error) error {
	start := time.Now()
	exchangesInFlight.WithLabelValues(r.ID).Inc()
	defer exchangesInFlight.WithLabelValues(r.ID).Dec()

	err := next(exchange)

	duration := time.Since(start).Seconds()
	exchangesDuration.WithLabelValues(r.ID).Observe(duration)

	status := "success"
	if err != nil && err != ErrStopRouting {
		status = "failure"
	}
	exchangesTotal.WithLabelValues(r.ID, status).Inc()
	return err
}

func (r *Route) doProcess(exchange *Exchange) error {
	for _, processor := range r.processors {
		if err := processor.Process(exchange); err != nil {
			if errors.Is(err, ErrStopRouting) {
				return err // StopRouting is a control signal, not a failure
			}
			// Delegate to error handler if configured
			if r.errorHandler != nil {
				return r.errorHandler.HandleError(exchange, err)
			}
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

	if r.err != nil {
		return r.err
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

// IsStarted vérifie si la route est démarrée (utilise RLock).
func (r *Route) IsStarted() bool {
	r.startLock.RLock()
	defer r.startLock.RUnlock()
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

// SetErrorHandler configures the error handler for this route.
func (r *Route) SetErrorHandler(handler ErrorHandler) *Route {
	r.errorHandler = handler
	return r
}

// GetErrorHandler returns the route's error handler (may be nil if none configured).
func (r *Route) GetErrorHandler() ErrorHandler {
	return r.errorHandler
}

// EnableMetrics active la collecte des métriques Prometheus pour cette route
func (r *Route) EnableMetrics() *Route {
	r.metrics = true
	return r
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

		exchange.PropagateOutToIn()
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

		exchange.PropagateOutToIn()
		return producer.Send(exchange)
	})
}
