package gocamel

import (
	"context"
	"errors"
	"fmt"
	"reflect"
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

// Validator is an optional interface that processors can implement to support
// build-time validation. Route.Validate() walks the pipeline and aggregates
// validation errors so misconfigured Simple expressions, endpoint URIs, or
// other late-resolving bugs surface before Start() instead of on the first
// matching message.
type Validator interface {
	Validate() error
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
	ID             string // Unique identifier for this route.
	Description    string // Human-readable description of the route purpose.
	Group          string // Logical group this route belongs to.
	Transacted     bool   // Whether the route uses transactional semantics.
	context        *CamelContext
	from           Endpoint
	consumer       Consumer
	processors     []Processor
	lazyProducers  []*lazyProducer // static To() producers; stopped at Route.Stop().
	started        bool
	startLock      sync.RWMutex // Protects started state for concurrent access.
	err            error        // Accumulated during construction, surfaced at Start().
	errorHandler   ErrorHandler // Error handler for this route (nil means propagate).
	interceptors   []RouteInterceptor
	process        func(*Exchange) error // doProcess wrapped by the interceptor chain.
	messageHistory bool                  // Whether Message History is enabled for this route.
}

// RouteInterceptor wraps the processing of every exchange flowing through a
// route. Interceptors are the extension point for cross-cutting concerns such
// as metrics or tracing (see the observability/prometheus and
// observability/otel modules). The route is passed so interceptors can read
// its ID and metadata; next runs the rest of the chain.
type RouteInterceptor func(route *Route, next func(*Exchange) error) func(*Exchange) error

// Intercept adds an interceptor around the route's processing. Interceptors
// run in registration order: the first added is the outermost. Like the rest
// of the route DSL, Intercept must be called before the route starts.
func (r *Route) Intercept(interceptor RouteInterceptor) *Route {
	r.interceptors = append(r.interceptors, interceptor)
	chain := r.doProcess
	for i := len(r.interceptors) - 1; i >= 0; i-- {
		chain = r.interceptors[i](r, chain)
	}
	r.process = chain
	return r
}

// lazyProducer is a Processor that initialises its underlying Producer on
// first use and exposes Stop() for cleanup when the route stops. A failed
// initialisation is NOT cached: the next exchange retries, so a transient
// connection error does not poison the producer for the lifetime of the route.
type lazyProducer struct {
	context *CamelContext
	uri     string
	mu      sync.Mutex
	p       Producer
}

func newLazyProducer(ctx *CamelContext, uri string) *lazyProducer {
	return &lazyProducer{context: ctx, uri: uri}
}

func (lp *lazyProducer) Process(exchange *Exchange) error {
	lp.mu.Lock()
	prod := lp.p
	if prod == nil {
		endpoint, err := lp.context.CreateEndpoint(lp.uri)
		if err != nil {
			lp.mu.Unlock()
			return err
		}
		p, err := endpoint.CreateProducer()
		if err != nil {
			lp.mu.Unlock()
			return err
		}
		if err := p.Start(exchange.Context); err != nil {
			lp.mu.Unlock()
			return err
		}
		lp.p = p
		prod = p
	}
	lp.mu.Unlock()

	exchange.PropagateOutToIn()
	return prod.Send(exchange)
}

func (lp *lazyProducer) Stop() error {
	lp.mu.Lock()
	prod := lp.p
	lp.p = nil
	lp.mu.Unlock()
	if prod != nil {
		return prod.Stop()
	}
	return nil
}

func NewRoute() *Route {
	return &Route{
		processors:    make([]Processor, 0),
		lazyProducers: make([]*lazyProducer, 0),
	}
}

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

func (r *Route) AddProcessor(processor Processor) {
	r.processors = append(r.processors, processor)
}

func (r *Route) ProcessFunc(f func(*Exchange) error) *Route {
	r.AddProcessor(ProcessorFunc(f))
	return r
}

// Process implements the Processor interface.
// Errors are delegated to the configured ErrorHandler if one is set.
func (r *Route) Process(exchange *Exchange) error {
	if r.process != nil {
		return r.process(exchange)
	}
	return r.doProcess(exchange)
}

func (r *Route) EnableMessageHistory() *Route {
	r.messageHistory = true
	return r
}

func (r *Route) doProcess(exchange *Exchange) error {
	return r.processFrom(exchange, 0)
}

// processFrom runs the route pipeline starting at start. Aggregators use this
// to resume routing after a completion timeout without re-running processors
// that already handled the exchange.
func (r *Route) processFrom(exchange *Exchange, start int) error {
	for i := start; i < len(r.processors); {
		processor := r.processors[i]

		var start time.Time
		if r.messageHistory {
			start = time.Now()
		}

		err := processor.Process(exchange)

		if r.messageHistory {
			elapsed := time.Since(start)
			nodeID := reflect.TypeOf(processor).String()
			if named, ok := processor.(interface{ ID() string }); ok {
				nodeID = named.ID()
			}
			entry := MessageHistoryEntry{
				NodeID:    nodeID,
				RouteID:   r.ID,
				Timestamp: start,
				Elapsed:   elapsed,
			}
			history := GetMessageHistory(exchange)
			history = append(history, entry)
			exchange.SetProperty(CamelMessageHistory, history)
		}

		if err != nil {
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
	if rr, ok := r.errorHandler.(RetryResettable); ok {
		rr.ResetRetryState(exchange.ID)
	}
}

// Validate reports problems with the route detected at build time:
//   - the accumulated construction error from From() (e.g. an unknown
//     component scheme),
//   - the absence of a source endpoint,
//   - validation errors from any processor implementing the Validator
//     interface (e.g. Choice when-clause expressions that fail to parse).
//
// Call Validate before Start in tests or CI to fail fast on broken routes.
// Start does NOT call Validate automatically — that would change the timing
// of existing error reporting and could turn previously latent issues into
// startup failures.
func (r *Route) Validate() error {
	if r.err != nil {
		return r.err
	}
	if r.from == nil {
		return fmt.Errorf("no source endpoint defined")
	}
	for i, p := range r.processors {
		v, ok := p.(Validator)
		if !ok {
			continue
		}
		if err := v.Validate(); err != nil {
			return fmt.Errorf("processor[%d]: %w", i, err)
		}
	}
	return nil
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
	r.configureAggregatorTimeouts(ctx)

	if err := consumer.Start(ctx); err != nil {
		r.stopAggregatorTimeouts()
		r.consumer = nil
		if stopErr := consumer.Stop(); stopErr != nil {
			return fmt.Errorf("error starting consumer: %v; cleanup failed: %v", err, stopErr)
		}
		return fmt.Errorf("error starting consumer: %v", err)
	}

	r.started = true
	return nil
}

// Stop stops the route. All resources (consumer, producers, stoppable
// processors) are stopped even if some fail; errors are collected and
// returned combined so one failing producer cannot leak the others.
func (r *Route) Stop() error {
	r.startLock.Lock()
	defer r.startLock.Unlock()

	if !r.started {
		return nil
	}

	var errs []error

	if r.consumer != nil {
		if err := r.consumer.Stop(); err != nil {
			errs = append(errs, fmt.Errorf("error stopping consumer: %w", err))
		}
		r.consumer = nil
	}

	// Aggregator timeouts can resume the downstream pipeline asynchronously.
	// Cancel and join them before stopping producers they may still call.
	if err := r.stopAggregatorTimeouts(); err != nil {
		errs = append(errs, err)
	}

	for _, lp := range r.lazyProducers {
		if err := lp.Stop(); err != nil {
			errs = append(errs, fmt.Errorf("error stopping producer for %s: %w", lp.uri, err))
		}
	}

	for _, p := range r.processors {
		if _, isAggregator := p.(*Aggregator); isAggregator {
			continue // already stopped before producers above
		}
		if stoppable, ok := p.(interface{ Stop() error }); ok {
			if err := stoppable.Stop(); err != nil {
				errs = append(errs, err)
			}
		}
	}

	r.started = false
	return errors.Join(errs...)
}

func (r *Route) configureAggregatorTimeouts(ctx context.Context) {
	for i, processor := range r.processors {
		aggregator, ok := processor.(*Aggregator)
		if !ok || aggregator.CompletionTimeout <= 0 {
			continue
		}
		next := i + 1
		aggregator.configureTimeout(ctx, ProcessorFunc(func(exchange *Exchange) error {
			return r.processFrom(exchange, next)
		}))
	}
}

func (r *Route) stopAggregatorTimeouts() error {
	var errs []error
	for _, processor := range r.processors {
		if aggregator, ok := processor.(*Aggregator); ok {
			if err := aggregator.Stop(); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (r *Route) IsStarted() bool {
	r.startLock.RLock()
	defer r.startLock.RUnlock()
	return r.started
}

func (r *Route) SetID(id string) *Route {
	r.ID = id
	return r
}

func (r *Route) SetDescription(description string) *Route {
	r.Description = description
	return r
}

func (r *Route) SetGroup(group string) *Route {
	r.Group = group
	return r
}

func (r *Route) GetContext() *CamelContext {
	return r.context
}

func (r *Route) SetErrorHandler(handler ErrorHandler) *Route {
	r.errorHandler = handler
	return r
}

// GetErrorHandler returns the route's error handler (may be nil if none configured).
func (r *Route) GetErrorHandler() ErrorHandler {
	return r.errorHandler
}

func (r *Route) SetBody(body interface{}) *Route {
	r.AddProcessor(setBodyProcessor(body))
	return r
}

func (r *Route) SetHeader(key string, value interface{}) *Route {
	r.AddProcessor(setHeaderProcessor(key, value))
	return r
}

func (r *Route) Marshal(dataFormat DataFormat) *Route {
	r.AddProcessor(&MarshalProcessor{DataFormat: dataFormat})
	return r
}

func (r *Route) Unmarshal(dataFormat DataFormat) *Route {
	r.AddProcessor(&UnmarshalProcessor{DataFormat: dataFormat})
	return r
}

// To adds one or more destination endpoints to the route.
// If multiple URIs are provided, a Multicast is created automatically.
func (r *Route) To(uris ...string) *Route {
	if len(uris) == 0 {
		return r
	}
	if len(uris) == 1 {
		lp := newLazyProducer(r.context, uris[0])
		r.lazyProducers = append(r.lazyProducers, lp)
		r.AddProcessor(lp)
	} else {
		m := NewMulticast()
		for _, uri := range uris {
			lp := newLazyProducer(r.context, uri)
			r.lazyProducers = append(r.lazyProducers, lp)
			m.AddProcessor(lp)
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

type toDProcessor struct {
	context     *CamelContext
	uriTemplate string
}

func createToDProcessor(context *CamelContext, uriTemplate string) Processor {
	return &toDProcessor{
		context:     context,
		uriTemplate: uriTemplate,
	}
}

func (t *toDProcessor) Process(exchange *Exchange) error {
	// Resolve the dynamic URI
	uri := Interpolate(t.uriTemplate, exchange)

	// Fetch started producer from the global cache
	producer, err := t.context.GetProducer(uri)
	if err != nil {
		return fmt.Errorf("toD dynamic producer error: %w", err)
	}

	exchange.PropagateOutToIn()
	return producer.Send(exchange)
}

func (t *toDProcessor) Stop() error {
	// Managed globally by CamelContext.Stop()
	return nil
}
