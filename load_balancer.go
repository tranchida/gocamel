package gocamel

import (
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
)

// LoadBalancerStrategy defines the interface for load balancing strategies.
type LoadBalancerStrategy interface {
	// Select selects one processor from the given slice.
	Select(processors []Processor) Processor
}

// RoundRobinStrategy selects processors in a circular order.
type RoundRobinStrategy struct {
	counter int64
}

func (s *RoundRobinStrategy) Select(processors []Processor) Processor {
	if len(processors) == 0 {
		return nil
	}
	idx := atomic.AddInt64(&s.counter, 1) - 1
	return processors[idx%int64(len(processors))]
}

// RandomStrategy selects a processor randomly.
type RandomStrategy struct{}

func (s *RandomStrategy) Select(processors []Processor) Processor {
	if len(processors) == 0 {
		return nil
	}
	// Random load distribution — not security-sensitive, so math/rand is
	// adequate and faster than crypto/rand (gosec G404).
	return processors[rand.Intn(len(processors))] // #nosec G404
}

// LoadBalancerProcessor implements the Load Balancer EIP.
type LoadBalancerProcessor struct {
	strategy   LoadBalancerStrategy
	processors []Processor
	mu         sync.RWMutex
}

// NewLoadBalancerProcessor creates a new LoadBalancerProcessor with the given strategy.
func NewLoadBalancerProcessor(strategy LoadBalancerStrategy) *LoadBalancerProcessor {
	return &LoadBalancerProcessor{
		strategy:   strategy,
		processors: make([]Processor, 0),
	}
}

// AddProcessor adds a processor to the load balancer.
func (p *LoadBalancerProcessor) AddProcessor(processor Processor) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.processors = append(p.processors, processor)
}

// Process implements the Processor interface.
func (p *LoadBalancerProcessor) Process(exchange *Exchange) error {
	p.mu.RLock()
	processors := p.processors
	strategy := p.strategy
	p.mu.RUnlock()

	if len(processors) == 0 {
		return fmt.Errorf("load balancer: no processors available")
	}

	selected := strategy.Select(processors)
	if selected == nil {
		return fmt.Errorf("load balancer: strategy failed to select a processor")
	}

	return selected.Process(exchange)
}

// LoadBalancerDefinition provides a fluent API for configuring a Load Balancer.
type LoadBalancerDefinition struct {
	parent       *RouteBuilder
	loadBalancer *LoadBalancerProcessor
}

// RoundRobin sets the strategy to Round Robin.
func (d *LoadBalancerDefinition) RoundRobin() *LoadBalancerDefinition {
	d.loadBalancer.mu.Lock()
	d.loadBalancer.strategy = &RoundRobinStrategy{}
	d.loadBalancer.mu.Unlock()
	return d
}

// Random sets the strategy to Random.
func (d *LoadBalancerDefinition) Random() *LoadBalancerDefinition {
	d.loadBalancer.mu.Lock()
	d.loadBalancer.strategy = &RandomStrategy{}
	d.loadBalancer.mu.Unlock()
	return d
}

// Custom sets a custom load balancing strategy.
func (d *LoadBalancerDefinition) Custom(strategy LoadBalancerStrategy) *LoadBalancerDefinition {
	d.loadBalancer.mu.Lock()
	d.loadBalancer.strategy = strategy
	d.loadBalancer.mu.Unlock()
	return d
}

// To adds one or more destination endpoints to the load balancer.
func (d *LoadBalancerDefinition) To(uris ...string) *LoadBalancerDefinition {
	for _, uri := range uris {
		d.loadBalancer.AddProcessor(newLazyProducer(d.parent.context, uri))
	}
	return d
}

// End terminates the LoadBalancer block and returns to the parent builder.
func (d *LoadBalancerDefinition) End() *RouteBuilder {
	return d.parent
}

// LoadBalance starts a Load Balancer EIP block.
func (b *RouteBuilder) LoadBalance() *LoadBalancerDefinition {
	lb := NewLoadBalancerProcessor(&RoundRobinStrategy{}) // Default to Round Robin
	b.container.AddProcessor(lb)
	return &LoadBalancerDefinition{
		parent:       b,
		loadBalancer: lb,
	}
}
