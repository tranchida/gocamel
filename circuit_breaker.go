package gocamel

import (
	"errors"
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

// Process implements the Processor interface.
func (cb *CircuitBreaker) Process(exchange *Exchange) error {
	cb.mu.Lock()
	
	// Check if we should transition from Open to Half-Open
	if cb.state == StateOpen && time.Since(cb.lastOpenTime) > cb.openTimeout {
		cb.state = StateHalfOpen
		cb.successCount = 0
	}

	state := cb.state
	cb.mu.Unlock()

	if state == StateOpen {
		return cb.handleFallback(exchange, ErrCircuitOpen)
	}

	// Try to execute the main processor
	err := cb.processor.Process(exchange)

	cb.mu.Lock()
	defer cb.mu.Unlock()

	if err != nil {
		cb.onFailure()
		return cb.handleFallback(exchange, err)
	}

	cb.onSuccess()
	return nil
}

func (cb *CircuitBreaker) onFailure() {
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

func (cb *CircuitBreaker) handleFallback(exchange *Exchange, err error) error {
	if cb.fallbackProcessor != nil {
		// If there's a fallback, we process it and don't return the error
		// We might want to store the error in a property
		exchange.SetProperty("CamelCircuitBreakerError", err)
		return cb.fallbackProcessor.Process(exchange)
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
