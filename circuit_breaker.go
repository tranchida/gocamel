package gocamel

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// CircuitBreakerState represents the state of the circuit breaker.
type CircuitBreakerState int

const (
	StateClosed CircuitBreakerState = iota
	StateOpen
	StateHalfOpen
)

var (
	ErrCircuitOpen = errors.New("circuit breaker is open")
)

// CircuitBreaker implements the Circuit Breaker EIP.
type CircuitBreaker struct {
	state             CircuitBreakerState
	failureThreshold  int
	failureCount      int
	successThreshold  int
	successCount      int
	openTimeout       time.Duration
	lastOpenTime      time.Time
	lastFailureTime   time.Time // last failure while Closed; idle gaps reset the count
	halfOpenProbe     bool      // true while a half-open trial request is in flight
	mu                sync.RWMutex
	processor         Processor
	fallbackProcessor Processor
}

// NewCircuitBreaker creates a new CircuitBreaker processor.
func NewCircuitBreaker() *CircuitBreaker {
	return &CircuitBreaker{
		state:            StateClosed,
		failureThreshold: 5,
		successThreshold: 1,
		openTimeout:      30 * time.Second,
	}
}

// SetProcessor sets the main processor to be executed when the circuit is not open.
func (cb *CircuitBreaker) SetProcessor(p Processor) {
	cb.processor = p
}

// SetFallbackProcessor sets the fallback processor to be executed when the circuit is open
// or when the main processor fails.
func (cb *CircuitBreaker) SetFallbackProcessor(p Processor) {
	cb.fallbackProcessor = p
}

// SetFailureThreshold sets the number of failures before opening the circuit.
func (cb *CircuitBreaker) SetFailureThreshold(threshold int) {
	cb.failureThreshold = threshold
}

// SetSuccessThreshold sets the number of successes in Half-Open state before closing the circuit.
func (cb *CircuitBreaker) SetSuccessThreshold(threshold int) {
	cb.successThreshold = threshold
}

// SetOpenTimeout sets the duration to wait in Open state before moving to Half-Open.
func (cb *CircuitBreaker) SetOpenTimeout(timeout time.Duration) {
	cb.openTimeout = timeout
}

// childProcessors exposes the nested processors so the route can wire
// completion timeouts for aggregators nested inside this circuit breaker.
func (cb *CircuitBreaker) childProcessors() []Processor {
	var procs []Processor
	if cb.processor != nil {
		procs = append(procs, cb.processor)
	}
	if cb.fallbackProcessor != nil {
		procs = append(procs, cb.fallbackProcessor)
	}
	return procs
}

// Process implements the Processor interface.
func (cb *CircuitBreaker) Process(exchange *Exchange) error {
	// Guard the zero-configuration case: NewCircuitBreaker() leaves the main
	// processor unset, and calling through it would panic.
	if cb.processor == nil {
		return fmt.Errorf("circuit breaker: no processor configured (call SetProcessor)")
	}

	cb.mu.Lock()
	// Check if we should transition from Open to Half-Open
	if cb.state == StateOpen && time.Since(cb.lastOpenTime) > cb.openTimeout {
		cb.state = StateHalfOpen
		cb.successCount = 0
		cb.halfOpenProbe = false
	}
	state := cb.state
	var fallback Processor

	// In Half-Open only ONE trial request may probe the downstream at a time;
	// concurrent requests are diverted to the fallback as if the circuit were open.
	if state == StateHalfOpen {
		if cb.halfOpenProbe {
			fallback = cb.fallbackProcessor
			cb.mu.Unlock()
			return cb.doFallback(exchange, ErrCircuitOpen, fallback)
		}
		cb.halfOpenProbe = true
	} else if state == StateOpen {
		// Circuit is open and the timeout has not elapsed: divert to the
		// fallback. fallbackProcessor is read here while still holding the
		// write lock so there is no TOCTOU window between Unlock and a
		// separate RLock.
		fallback = cb.fallbackProcessor
		cb.mu.Unlock()
		return cb.doFallback(exchange, ErrCircuitOpen, fallback)
	}
	cb.mu.Unlock()

	// Try to execute the main processor. safeProcess turns a panic in the
	// protected processor into an error so the post-call bookkeeping below
	// (which clears halfOpenProbe) always runs — otherwise a panic would leave
	// the breaker stuck in Half-Open diverting every request to the fallback.
	err := safeProcess(func() error { return cb.processor.Process(exchange) })

	cb.mu.Lock()
	if state == StateHalfOpen {
		cb.halfOpenProbe = false
	}
	var fb Processor
	if err != nil {
		cb.onFailure()
		fb = cb.fallbackProcessor
	} else {
		cb.onSuccess()
	}
	cb.mu.Unlock()

	if err != nil {
		return cb.doFallback(exchange, err, fb)
	}
	return nil
}

func (cb *CircuitBreaker) onFailure() {
	now := time.Now()
	// Idle reset: failures older than OpenTimeout do not count toward the
	// threshold, so a burst of errors years ago cannot open the circuit today.
	if cb.state == StateClosed && !cb.lastFailureTime.IsZero() && now.Sub(cb.lastFailureTime) > cb.openTimeout {
		cb.failureCount = 0
	}
	cb.lastFailureTime = now
	cb.failureCount++
	if cb.state == StateClosed {
		if cb.failureCount >= cb.failureThreshold {
			cb.openCircuit()
		}
	} else if cb.state == StateHalfOpen {
		cb.openCircuit()
	}
}

func (cb *CircuitBreaker) onSuccess() {
	if cb.state == StateHalfOpen {
		cb.successCount++
		if cb.successCount >= cb.successThreshold {
			cb.closeCircuit()
		}
	} else if cb.state == StateClosed {
		cb.failureCount = 0
	}
}

func (cb *CircuitBreaker) openCircuit() {
	cb.state = StateOpen
	cb.lastOpenTime = time.Now()
	cb.failureCount = 0
}

func (cb *CircuitBreaker) closeCircuit() {
	cb.state = StateClosed
	cb.failureCount = 0
	cb.successCount = 0
}

// doFallback calls the fallback processor if set, otherwise returns err.
// Must be called without holding cb.mu to avoid deadlocks if the fallback
// re-enters the circuit breaker.
func (cb *CircuitBreaker) doFallback(exchange *Exchange, err error, fb Processor) error {
	if fb != nil {
		exchange.SetProperty("CamelCircuitBreakerError", err)
		return safeProcess(func() error { return fb.Process(exchange) })
	}
	return err
}

// CircuitBreakerDefinition provides a fluent API for configuring a Circuit Breaker.
type CircuitBreakerDefinition struct {
	parent         *RouteBuilder
	circuitBreaker *CircuitBreaker
	mainBuilder    *RouteBuilder
}

// FailureThreshold sets the failure threshold.
func (d *CircuitBreakerDefinition) FailureThreshold(threshold int) *CircuitBreakerDefinition {
	d.circuitBreaker.SetFailureThreshold(threshold)
	return d
}

// SuccessThreshold sets the success threshold.
func (d *CircuitBreakerDefinition) SuccessThreshold(threshold int) *CircuitBreakerDefinition {
	d.circuitBreaker.SetSuccessThreshold(threshold)
	return d
}

// OpenTimeout sets the open timeout.
func (d *CircuitBreakerDefinition) OpenTimeout(timeout time.Duration) *CircuitBreakerDefinition {
	d.circuitBreaker.SetOpenTimeout(timeout)
	return d
}

// OnFallback begins a Fallback block.
func (d *CircuitBreakerDefinition) OnFallback() *FallbackDefinition {
	p := NewPipeline()
	d.circuitBreaker.SetFallbackProcessor(p)

	return &FallbackDefinition{
		routeBuilder: &RouteBuilder{
			context:   d.parent.context,
			route:     d.parent.route,
			container: p,
		},
		parent: d,
	}
}

// End terminates the CircuitBreaker block and returns to the parent builder.
func (d *CircuitBreakerDefinition) End() *RouteBuilder {
	return d.parent
}

// FallbackDefinition is a fluent sub-DSL for configuring the Fallback of a Circuit Breaker.
type FallbackDefinition struct {
	routeBuilder *RouteBuilder
	parent       *CircuitBreakerDefinition
}

// Process adds a processor to the fallback and stays in the fallback context.
func (d *FallbackDefinition) Process(processor Processor) *FallbackDefinition {
	d.routeBuilder.Process(processor)
	return d
}

// ProcessFunc adds a processing function to the fallback and stays in the fallback context.
func (d *FallbackDefinition) ProcessFunc(f func(*Exchange) error) *FallbackDefinition {
	d.routeBuilder.ProcessFunc(f)
	return d
}

// To adds one or more destination endpoints to the fallback and stays in the fallback context.
func (d *FallbackDefinition) To(uris ...string) *FallbackDefinition {
	d.routeBuilder.To(uris...)
	return d
}

// End terminates the Fallback block and returns to the CircuitBreakerDefinition.
func (d *FallbackDefinition) End() *CircuitBreakerDefinition {
	return d.parent
}

// CircuitBreaker starts a Circuit Breaker EIP block.
func (b *RouteBuilder) CircuitBreaker() *CircuitBreakerDefinition {
	cb := NewCircuitBreaker()

	// We need a pipeline to hold the processors that are protected by the circuit breaker
	p := NewPipeline()
	cb.SetProcessor(p)

	b.container.AddProcessor(cb)

	return &CircuitBreakerDefinition{
		parent:         b,
		circuitBreaker: cb,
		mainBuilder: &RouteBuilder{
			context:   b.context,
			route:     b.route,
			container: p,
		},
	}
}

// Process adds a processor to the circuit breaker and stays in the circuit breaker context.
func (d *CircuitBreakerDefinition) Process(processor Processor) *CircuitBreakerDefinition {
	d.mainBuilder.Process(processor)
	return d
}

// ProcessFunc adds a processing function to the circuit breaker and stays in the circuit breaker context.
func (d *CircuitBreakerDefinition) ProcessFunc(f func(*Exchange) error) *CircuitBreakerDefinition {
	d.mainBuilder.ProcessFunc(f)
	return d
}

// To adds one or more destination endpoints to the circuit breaker and stays in the circuit breaker context.
func (d *CircuitBreakerDefinition) To(uris ...string) *CircuitBreakerDefinition {
	d.mainBuilder.To(uris...)
	return d
}
