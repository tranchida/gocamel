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

// Processor defines the interface for message processing.
type Processor interface {
	Process(exchange *Exchange) error
}

// ProcessorContainer defines the interface for objects that can hold processors.
// This enables nesting of EIPs such as the Splitter.
type ProcessorContainer interface {
	AddProcessor(processor Processor)
}

// ProcessorFunc is a function type that implements the Processor interface.
type ProcessorFunc func(*Exchange) error

// Process implements the Processor interface for ProcessorFunc.
func (f ProcessorFunc) Process(exchange *Exchange) error {
	return f(exchange)
}

// Route represents a Camel route: a pipeline of Processors fed by a single Consumer.
// Routes are built via RouteBuilder (fluent DSL) and managed by CamelContext.
type Route struct {
	ID           string // Unique identifier for this route.
	Description  string // Human-readable description of the route purpose.
	Group        string // Logical group this route belongs to.
	Transacted   bool   // Whether the route uses transactional semantics.
	context      *CamelContext
	from         Endpoint
	consumer     Consumer
	processors   []Processor
	started      bool
	startLock    sync.RWMutex // Protects started state for concurrent access.
	err          error        // Accumulated during construction, surfaced at Start().
	errorHandler ErrorHandler // Error handler for this route (nil means propagate).
	metrics      bool         // Whether Prometheus metrics are enabled for this route.
	tracing      bool         // Whether OpenTelemetry tracing is enabled for this route.
}

// NewRoute creates a new Route instance.
func NewRoute() *Route {
	return &Route{
		processors: make([]Processor, 0),
	}
}

// SetTransacted sets whether the route uses transactional semantics.
func (r *Route) SetTransacted(transacted bool) *Route {
	r.Transacted = transacted
	return r
}

// From sets the source endpoint of the route.
// Errors are stored and surfaced at Start() time.
func (r *Route) From(uri string) *Route {
	endpoint, err := r.context.CreateEndpoint(uri)
	if err != nil {
		r.err = fmt.Errorf("error creating endpoint: %w", err)
		return r
	}
	r.from = endpoint
	return r
}

// AddProcessor adds a processor to the route.
func (r *Route) AddProcessor(processor Processor) {
	r.processors = append(r.processors, processor)
}

// ProcessFunc adds a processing function to the route.
func (r *Route) ProcessFunc(f func(*Exchange) error) *Route {
	r.AddProcessor(ProcessorFunc(f))
	return r
}

// Process implements the Processor interface.
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
	for i := 0; i < len(r.processors); {
		processor := r.processors[i]
		if err := processor.Process(exchange); err != nil {
			if errors.Is(err, ErrStopRouting) {
				return err // StopRouting is a control signal, not a failure
			}
			// Delegate to error handler if configured
			if r.errorHandler != nil {
				if handleErr := r.errorHandler.HandleError(exchange, err); handleErr != nil {
					return handleErr
				}
				// A nil handler result means the failed processor should be retried.
				continue
			}
			return err
		}
		r.resetErrorHandlerState(exchange)
		i++
	}
	return nil
}

func (r *Route) resetErrorHandlerState(exchange *Exchange) {
	if dlc, ok := r.errorHandler.(*DeadLetterChannel); ok {
		dlc.ResetRetryState(exchange.ID)
	}
}

// Start starts the route.
func (r *Route) Start(ctx context.Context) error {
	r.startLock.Lock()
	defer r.startLock.Unlock()

	if r.started {
		return fmt.Errorf("route is already started")
	}

	if r.err != nil {
		return r.err
	}

	if r.from == nil {
		return fmt.Errorf("no source endpoint defined")
	}

	consumer, err := r.from.CreateConsumer(r)
	if err != nil {
		return fmt.Errorf("error creating consumer: %v", err)
	}
	r.consumer = consumer

	if err := consumer.Start(ctx); err != nil {
		return fmt.Errorf("error starting consumer: %v", err)
	}

	r.started = true
	return nil
}

// Stop stops the route.
func (r *Route) Stop() error {
	r.startLock.Lock()
	defer r.startLock.Unlock()

	if !r.started {
		return nil
	}

	if r.consumer != nil {
		if err := r.consumer.Stop(); err != nil {
			return fmt.Errorf("error stopping consumer: %v", err)
		}
	}

	r.started = false
	return nil
}

// IsStarted returns true if the route is started (uses RLock).
func (r *Route) IsStarted() bool {
	r.startLock.RLock()
	defer r.startLock.RUnlock()
	return r.started
}

// SetID sets the route ID.
func (r *Route) SetID(id string) *Route {
	r.ID = id
	return r
}

// SetDescription sets the route description.
func (r *Route) SetDescription(description string) *Route {
	r.Description = description
	return r
}

// SetGroup sets the route group.
func (r *Route) SetGroup(group string) *Route {
	r.Group = group
	return r
}

// GetContext returns the CamelContext associated with the route.
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

// EnableMetrics enables Prometheus metrics collection for this route.
func (r *Route) EnableMetrics() *Route {
	r.metrics = true
	return r
}

// SetBody sets the output message body.
func (r *Route) SetBody(body interface{}) *Route {
	return r.ProcessFunc(func(exchange *Exchange) error {
		exchange.GetOut().SetBody(body)
		return nil
	})
}

// SetHeader sets a header on the output message.
func (r *Route) SetHeader(key string, value interface{}) *Route {
	return r.ProcessFunc(func(exchange *Exchange) error {
		exchange.GetOut().SetHeader(key, value)
		return nil
	})
}

// Marshal adds a dataformat marshal step to the route.
func (r *Route) Marshal(dataFormat DataFormat) *Route {
	r.AddProcessor(&MarshalProcessor{DataFormat: dataFormat})
	return r
}

// Unmarshal adds a dataformat unmarshal step to the route.
func (r *Route) Unmarshal(dataFormat DataFormat) *Route {
	r.AddProcessor(&UnmarshalProcessor{DataFormat: dataFormat})
	return r
}

// MarshalRSS converts an RSSFeed body to RSS XML.
func (r *Route) MarshalRSS() *Route {
	return r.Marshal(NewRSSDataFormat())
}

// UnmarshalRSS converts RSS XML into an RSSFeed body.
func (r *Route) UnmarshalRSS() *Route {
	return r.Unmarshal(NewRSSDataFormat())
}

// To adds one or more destination endpoints to the route.
// If multiple URIs are provided, a Multicast is created automatically.
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

// ToD adds one or more dynamic send processors that resolve the URI for each exchange.
// If multiple templates are provided, a Multicast is created automatically.
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
		// Resolve the dynamic URI
		uri := Interpolate(uriTemplate, exchange)

		// Create the endpoint and producer on each exchange (required for ToD).
		// TODO: Optimise with a producer cache if needed.
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
		// Ensure the producer is stopped when done (in Camel, dynamic producers are normally cached;
		// without caching we stop the producer after each send).
		defer producer.Stop()

		exchange.PropagateOutToIn()
		return producer.Send(exchange)
	})
}
