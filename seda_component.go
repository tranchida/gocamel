package gocamel

import (
	"context"
	"fmt"
	"strconv"
	"sync"
)

// SedaComponent represents the SEDA (Staged Event-Driven Architecture) component.
// It uses internal queues (Go channels) to decouple producers and consumers.
type SedaComponent struct {
	endpoints map[string]*SedaEndpoint
	mu        sync.Mutex
}

// NewSedaComponent creates a new instance of SedaComponent.
func NewSedaComponent() *SedaComponent {
	return &SedaComponent{
		endpoints: make(map[string]*SedaEndpoint),
	}
}

// CreateEndpoint creates a new SEDA endpoint from a URI.
// Supported parameters:
// - size: The capacity of the internal queue (default 1000).
// - concurrentConsumers: The number of worker goroutines (default 1).
func (c *SedaComponent) CreateEndpoint(uri string) (Endpoint, error) {
	u, err := ParseURI(uri)
	if err != nil {
		return nil, fmt.Errorf("invalid SEDA URI: %v", err)
	}

	// SEDA endpoints are identified by their queue name (host + path).
	queueName := u.Host + u.Path
	if queueName == "" {
		queueName = u.Opaque
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if endpoint, exists := c.endpoints[queueName]; exists {
		return endpoint, nil
	}

	size := 1000 // default size
	if sizeStr := GetConfigValue(u, "size"); sizeStr != "" {
		if s, err := strconv.Atoi(sizeStr); err == nil && s >= 0 {
			size = s
		} else {
			logWarn("seda: invalid size; using default", "value", sizeStr, "default", size)
		}
	}

	concurrentConsumers := 1 // default consumers
	if consumersStr := GetConfigValue(u, "concurrentConsumers"); consumersStr != "" {
		if cc, err := strconv.Atoi(consumersStr); err == nil && cc > 0 {
			concurrentConsumers = cc
		} else {
			logWarn("seda: invalid concurrentConsumers; using default", "value", consumersStr, "default", concurrentConsumers)
		}
	}

	endpoint := &SedaEndpoint{
		uri:                 uri,
		queue:               make(chan *Exchange, size),
		concurrentConsumers: concurrentConsumers,
	}
	c.endpoints[queueName] = endpoint
	return endpoint, nil
}

// SedaEndpoint represents a SEDA endpoint.
type SedaEndpoint struct {
	uri                 string
	queue               chan *Exchange
	concurrentConsumers int
	consumer            *SedaConsumer
	mu                  sync.Mutex
}

// URI returns the endpoint URI.
func (e *SedaEndpoint) URI() string {
	return e.uri
}

// CreateProducer creates a SEDA producer.
func (e *SedaEndpoint) CreateProducer() (Producer, error) {
	return &SedaProducer{endpoint: e}, nil
}

// CreateConsumer creates a SEDA consumer.
func (e *SedaEndpoint) CreateConsumer(processor Processor) (Consumer, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.consumer != nil {
		return nil, fmt.Errorf("multiple consumers for the same SEDA endpoint are not allowed: %s", e.uri)
	}

	e.consumer = &SedaConsumer{
		endpoint:  e,
		processor: processor,
	}
	return e.consumer, nil
}

// SedaProducer represents a SEDA producer.
type SedaProducer struct {
	endpoint *SedaEndpoint
}

// Start starts the producer.
func (p *SedaProducer) Start(ctx context.Context) error {
	return nil
}

// Stop stops the producer.
func (p *SedaProducer) Stop() error {
	return nil
}

// Send asynchronously sends an exchange to the SEDA queue.
// It creates a copy of the exchange to ensure decoupling.
func (p *SedaProducer) Send(exchange *Exchange) error {
	copy := exchange.Copy()

	select {
	case p.endpoint.queue <- copy:
		return nil
	case <-exchange.Context.Done():
		return exchange.Context.Err()
	}
}

// SedaConsumer represents a SEDA consumer.
type SedaConsumer struct {
	endpoint  *SedaEndpoint
	processor Processor
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

// Start starts the SEDA consumer workers.
func (c *SedaConsumer) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	for i := 0; i < c.endpoint.concurrentConsumers; i++ {
		c.wg.Add(1)
		go c.run(ctx)
	}

	return nil
}

// Stop stops all SEDA consumer workers and waits for them to finish.
func (c *SedaConsumer) Stop() error {
	if c.cancel != nil {
		c.cancel()
	}
	c.wg.Wait()
	return nil
}

func (c *SedaConsumer) run(ctx context.Context) {
	defer c.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case exchange, ok := <-c.endpoint.queue:
			if !ok {
				return
			}
			if err := completeExchange(exchange, c.processor.Process(exchange)); err != nil {
				logError("SEDA consumer processing error",
					"uri", c.endpoint.uri,
					"error", err,
					"exchange_id", exchange.ID,
				)
			}
		}
	}
}
