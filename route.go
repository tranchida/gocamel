package gocamel

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime/debug"
	"sync"
	"time"
)

// ErrStopRouting is a special error used to stop
// routing of a message without it being considered a failure.
// Used notably by the Aggregator EIP.
var ErrStopRouting = errors.New("stop routing")

// ErrProcessorPanic is wrapped by the error produced when a processor panics.
// Routes can detect it with errors.Is.
var ErrProcessorPanic = errors.New("processor panic")

// safeProcess runs fn and converts a panic into an error instead of letting it
// unwind the goroutine.
//
// Processors run arbitrary user code. When that code is invoked from a
// library-owned goroutine (SEDA worker, parallel Multicast/RecipientList
// branch, Aggregator completion timeout, WireTap), an unrecovered panic takes
// down the whole process rather than failing the one exchange responsible for
// it. Recovering here turns it into an ordinary route error that the
// configured ErrorHandler can act on. The stack is logged before it is lost.
// ProcessSafely runs processor.Process(exchange), converting a panic into an
// error wrapping ErrProcessorPanic. Consumers that invoke a processor from a
// goroutine they own should call this instead of Process directly, so that a
// panic in user code fails the exchange rather than the whole process.
func ProcessSafely(processor Processor, exchange *Exchange) error {
	return safeProcess(func() error { return processor.Process(exchange) })
}

func safeProcess(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			LogError("processor panic recovered", "panic", r, "stack", string(stack))
			err = fmt.Errorf("%w: %v", ErrProcessorPanic, r)
		}
	}()
	return fn()
}

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
	starting       bool         // guarded by startLock
	stopping       bool         // guarded by startLock
	startLock      sync.RWMutex // Protects started, starting and stopping.
	err            error        // Accumulated during construction, surfaced at Start().
	errorHandler   ErrorHandler // Error handler for this route (nil means propagate).
	interceptors   []RouteInterceptor
	process        func(*Exchange) error // doProcess wrapped by the interceptor chain.
	messageHistory bool                  // Whether Message History is enabled for this route.
	nodeIDMu       sync.RWMutex          // Guards nodeIDCache.
	nodeIDCache    []string              // Memoised Message History labels, indexed by processor position.
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
//
// Creation is single-flighted per lazyProducer (the mutex) but Send runs
// without the lock. The producer is route-scoped: Route.Stop() tears it down
// so a later Start recreates it, unlike ToD which uses the context cache.
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
	prod, err := lp.producer()
	if err != nil {
		return err
	}
	propagateOutToInAndReset(exchange)
	return prod.Send(exchange)
}

func (lp *lazyProducer) producer() (Producer, error) {
	lp.mu.Lock()
	defer lp.mu.Unlock()
	if lp.p != nil {
		return lp.p, nil
	}

	endpoint, err := lp.context.CreateEndpoint(lp.uri)
	if err != nil {
		return nil, err
	}
	p, err := endpoint.CreateProducer()
	if err != nil {
		return nil, err
	}
	// Start with the CamelContext's lifecycle context, not the exchange's
	// (which may be nil or a short-lived request context). The producer is
	// a route-level resource stopped by Route.Stop, so it should observe the
	// context shutdown like the producers in ProducerCache do.
	if err := p.Start(lp.context.GetContext()); err != nil {
		return nil, err
	}
	lp.p = p
	return p, nil
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
// Errors are stored and surfaced at Build() / Start() time.
func (r *Route) From(uri string) *Route {
	// A Route built with NewRoute() has no CamelContext and cannot resolve an
	// endpoint. Record the error instead of dereferencing a nil context:
	// use CamelContext.CreateRoute() (or a RouteBuilder) to obtain a usable Route.
	if r.context == nil {
		r.err = fmt.Errorf("route has no CamelContext: build it with CamelContext.CreateRoute() instead of NewRoute()")
		return r
	}

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
		propagateOutToInAndReset(exchange)

		// Named stepStart, not start: shadowing the loop's start parameter
		// here made the function hard to read for no benefit.
		var stepStart time.Time
		if r.messageHistory {
			stepStart = time.Now()
		}

		// A panicking processor is turned into an ordinary error so the
		// configured ErrorHandler (dead letter, redelivery) can act on it
		// instead of the panic unwinding the consumer goroutine.
		err := safeProcess(func() error { return processor.Process(exchange) })

		if r.messageHistory {
			elapsed := time.Since(stepStart)
			entry := MessageHistoryEntry{
				NodeID:    r.nodeIDFor(i, processor),
				RouteID:   r.ID,
				Timestamp: stepStart,
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

// nodeIDFor returns the Message History label for the processor at index i:
// its ID() when it exposes one, otherwise its Go type name.
//
// reflect.TypeOf(...).String() was called for every processor of every message
// while Message History was enabled. The type name never changes for a given
// pipeline slot, so it is resolved once and memoised by position — Processor
// itself cannot key a map, since ProcessorFunc is a func type and func values
// are not hashable.
func (r *Route) nodeIDFor(i int, processor Processor) string {
	if named, ok := processor.(interface{ ID() string }); ok {
		return named.ID() // may legitimately change; never cached
	}

	r.nodeIDMu.RLock()
	cache := r.nodeIDCache
	if i < len(cache) && cache[i] != "" {
		id := cache[i]
		r.nodeIDMu.RUnlock()
		return id
	}
	r.nodeIDMu.RUnlock()

	id := reflect.TypeOf(processor).String()

	r.nodeIDMu.Lock()
	if len(r.nodeIDCache) < len(r.processors) {
		grown := make([]string, len(r.processors))
		copy(grown, r.nodeIDCache)
		r.nodeIDCache = grown
	}
	if i < len(r.nodeIDCache) {
		r.nodeIDCache[i] = id
	}
	r.nodeIDMu.Unlock()
	return id
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
// All problems are returned via errors.Join. Start does NOT call Validate
// automatically — that would change the timing of existing error reporting
// and could turn previously latent issues into startup failures.
func (r *Route) Validate() error {
	var errs []error
	if r.err != nil {
		errs = append(errs, r.err)
	}
	if r.from == nil {
		errs = append(errs, fmt.Errorf("no source endpoint defined"))
	}
	for i, p := range r.processors {
		v, ok := p.(Validator)
		if !ok {
			continue
		}
		if err := v.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("processor[%d]: %w", i, err))
		}
	}
	return errors.Join(errs...)
}

// Start starts the route.
func (r *Route) Start(ctx context.Context) (err error) {
	r.startLock.Lock()
	if r.started || r.starting || r.stopping {
		r.startLock.Unlock()
		return fmt.Errorf("route is already started or changing lifecycle state")
	}
	r.starting = true
	r.startLock.Unlock()
	defer func() {
		r.startLock.Lock()
		r.starting = false
		r.started = err == nil
		r.startLock.Unlock()
	}()
	if r.err != nil {
		return r.err
	}
	if r.from == nil {
		return fmt.Errorf("no source endpoint defined")
	}
	consumer, err := r.from.CreateConsumer(r)
	if err != nil {
		return fmt.Errorf("error creating consumer: %w", err)
	}
	r.consumer = consumer
	r.configureAggregatorTimeouts(ctx)
	if err := consumer.Start(ctx); err != nil {
		r.consumer = nil
		return errors.Join(fmt.Errorf("error starting consumer: %w", err), consumer.Stop(), r.stopAggregatorTimeouts())
	}
	return nil
}

// Stop stops the route. All resources (consumer, producers, stoppable
// processors) are stopped even if some fail; errors are collected and
// returned combined so one failing producer cannot leak the others.
func (r *Route) Stop() error {
	r.startLock.Lock()
	if r.starting || r.stopping {
		r.startLock.Unlock()
		return fmt.Errorf("route is changing lifecycle state")
	}
	if !r.started {
		r.startLock.Unlock()
		return nil
	}
	r.stopping = true
	r.startLock.Unlock()
	defer func() {
		r.startLock.Lock()
		r.started = false
		r.stopping = false
		r.startLock.Unlock()
	}()
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

	return errors.Join(errs...)
}

func (r *Route) configureAggregatorTimeouts(ctx context.Context) {
	for i, processor := range r.processors {
		r.wireAggregatorTimeouts(ctx, processor, i)
	}
}

// childProcessorContainer is implemented by processors that nest other
// processors (Pipeline, Multicast, Splitter, Loop, LoadBalancer, Choice). It
// lets the route walk nested containers so completion timeouts are wired for
// aggregators that are not top-level route processors (e.g. an aggregator
// inside a Split).
type childProcessorContainer interface {
	childProcessors() []Processor
}

// wireAggregatorTimeouts wires the completion timeout for an aggregator found
// at topIndex in the route's top-level pipeline, then recurses into any nested
// container. A nested aggregator resumes at the route level just past the
// top-level container that holds it, so a group that times out is emitted
// downstream rather than silently dropped.
func (r *Route) wireAggregatorTimeouts(ctx context.Context, processor Processor, topIndex int) {
	if aggregator, ok := processor.(*Aggregator); ok {
		if aggregator.CompletionTimeout <= 0 {
			return
		}
		next := topIndex + 1
		aggregator.configureTimeout(ctx, ProcessorFunc(func(exchange *Exchange) error {
			return r.processFrom(exchange, next)
		}))
		return
	}

	container, ok := processor.(childProcessorContainer)
	if !ok {
		return
	}
	for _, child := range container.childProcessors() {
		r.wireAggregatorTimeouts(ctx, child, topIndex)
	}
}

func (r *Route) stopAggregatorTimeouts() error {
	var errs []error
	r.forEachAggregator(func(aggregator *Aggregator) {
		if err := aggregator.Stop(); err != nil {
			errs = append(errs, err)
		}
	})
	return errors.Join(errs...)
}

// forEachAggregator visits every aggregator in the route, including those
// nested inside containers, so that Stop() joins their timeout goroutines.
func (r *Route) forEachAggregator(visit func(*Aggregator)) {
	for _, processor := range r.processors {
		r.walkAggregators(processor, visit)
	}
}

func (r *Route) walkAggregators(processor Processor, visit func(*Aggregator)) {
	if aggregator, ok := processor.(*Aggregator); ok {
		visit(aggregator)
		return
	}
	if container, ok := processor.(childProcessorContainer); ok {
		for _, child := range container.childProcessors() {
			r.walkAggregators(child, visit)
		}
	}
}

func (r *Route) IsStarted() bool {
	r.startLock.RLock()
	defer r.startLock.RUnlock()
	return r.started
}

func (r *Route) SetID(id string) *Route {
	if r.context != nil {
		r.context.reassignRouteID(r, id)
		return r
	}
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
