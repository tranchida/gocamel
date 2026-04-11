package gocamel

import (
	"context"
	"fmt"
	"net/url"
	"sync"
)

// DirectComponent represents the direct component
type DirectComponent struct {
	endpoints map[string]*DirectEndpoint
	mu        sync.Mutex
}

// NewDirectComponent creates a new instance of DirectComponent
func NewDirectComponent() *DirectComponent {
	return &DirectComponent{
		endpoints: make(map[string]*DirectEndpoint),
	}
}

// CreateEndpoint creates a new direct endpoint
func (c *DirectComponent) CreateEndpoint(uri string) (Endpoint, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if endpoint, exists := c.endpoints[uri]; exists {
		return endpoint, nil
	}

	if _, err := url.Parse(uri); err != nil {
		return nil, fmt.Errorf("invalid direct URI: %v", err)
	}

	endpoint := &DirectEndpoint{
		uri: uri,
	}
	c.endpoints[uri] = endpoint
	return endpoint, nil
}

// DirectEndpoint represents a direct endpoint
type DirectEndpoint struct {
	uri      string
	consumer *DirectConsumer
	mu       sync.RWMutex
}

// URI returns the endpoint URI
func (e *DirectEndpoint) URI() string {
	return e.uri
}

// CreateProducer creates a direct producer
func (e *DirectEndpoint) CreateProducer() (Producer, error) {
	return &DirectProducer{
		endpoint: e,
	}, nil
}

// CreateConsumer creates a direct consumer
func (e *DirectEndpoint) CreateConsumer(processor Processor) (Consumer, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.consumer != nil {
		return nil, fmt.Errorf("multiple consumers for the same direct endpoint are not allowed: %s", e.uri)
	}
	e.consumer = &DirectConsumer{
		endpoint:  e,
		processor: processor,
	}
	return e.consumer, nil
}

// DirectProducer represents a direct producer
type DirectProducer struct {
	endpoint *DirectEndpoint
}

// Start starts the producer
func (p *DirectProducer) Start(ctx context.Context) error {
	return nil
}

// Stop stops the producer
func (p *DirectProducer) Stop() error {
	return nil
}

// Send sends an exchange to the direct consumer synchronously
func (p *DirectProducer) Send(exchange *Exchange) error {
	p.endpoint.mu.RLock()
	consumer := p.endpoint.consumer
	p.endpoint.mu.RUnlock()

	if consumer == nil {
		return fmt.Errorf("no consumers available on endpoint: %s", p.endpoint.uri)
	}
	return consumer.processor.Process(exchange)
}

// DirectConsumer represents a direct consumer
type DirectConsumer struct {
	endpoint  *DirectEndpoint
	processor Processor
}

// Start starts the consumer
func (c *DirectConsumer) Start(ctx context.Context) error {
	return nil
}

// Stop stops the consumer
func (c *DirectConsumer) Stop() error {
	return nil
}
