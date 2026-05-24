package gocamel

import (
	"context"
	"fmt"
	"strconv"
	"sync"
)

// ChanComponent represents the native Go channel component.
// It allows message exchanges to flow asynchronously through standard Go channels.
type ChanComponent struct {
	channels map[string]chan *Exchange
	mu       sync.Mutex
}

// NewChanComponent creates a new instance of ChanComponent.
func NewChanComponent() *ChanComponent {
	return &ChanComponent{
		channels: make(map[string]chan *Exchange),
	}
}

// CreateEndpoint creates a new ChanEndpoint from a URI.
// Supported parameters:
// - bufferSize: The capacity of the internal channel (default 100).
func (c *ChanComponent) CreateEndpoint(uri string) (Endpoint, error) {
	u, err := ParseURI(uri)
	if err != nil {
		return nil, fmt.Errorf("invalid channel URI: %w", err)
	}

	channelName := u.Host + u.Path
	if channelName == "" {
		channelName = u.Opaque
	}
	if channelName == "" {
		return nil, fmt.Errorf("channel name is required in URI: %s", uri)
	}

	bufferSize := 100 // default buffer size
	if bufStr := GetConfigValue(u, "bufferSize"); bufStr != "" {
		if b, err := strconv.Atoi(bufStr); err == nil {
			bufferSize = b
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	ch, exists := c.channels[channelName]
	if !exists {
		ch = make(chan *Exchange, bufferSize)
		c.channels[channelName] = ch
	}

	return &ChanEndpoint{
		uri:   uri,
		queue: ch,
	}, nil
}

// ChanEndpoint represents a Go channel endpoint.
type ChanEndpoint struct {
	uri      string
	queue    chan *Exchange
	consumer *ChanConsumer
	mu       sync.Mutex
}

// URI returns the endpoint URI.
func (e *ChanEndpoint) URI() string {
	return e.uri
}

// CreateProducer creates a ChanProducer.
func (e *ChanEndpoint) CreateProducer() (Producer, error) {
	return &ChanProducer{endpoint: e}, nil
}

// CreateConsumer creates a ChanConsumer.
func (e *ChanEndpoint) CreateConsumer(processor Processor) (Consumer, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.consumer != nil {
		return nil, fmt.Errorf("multiple consumers for the same channel endpoint are not allowed: %s", e.uri)
	}

	e.consumer = &ChanConsumer{
		endpoint:  e,
		processor: processor,
	}
	return e.consumer, nil
}

// ChanProducer writes exchanges to the channel.
type ChanProducer struct {
	endpoint *ChanEndpoint
}

// Start starts the producer.
func (p *ChanProducer) Start(ctx context.Context) error {
	return nil
}

// Stop stops the producer.
func (p *ChanProducer) Stop() error {
	return nil
}

// Send writes the exchange to the Go channel.
func (p *ChanProducer) Send(exchange *Exchange) error {
	ctx := exchange.Context
	if ctx == nil {
		ctx = context.Background()
	}

	// We copy the exchange to decouple the sender from downstream updates
	copy := exchange.Copy()

	select {
	case p.endpoint.queue <- copy:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ChanConsumer reads exchanges from the channel and passes them to the route.
type ChanConsumer struct {
	endpoint  *ChanEndpoint
	processor Processor
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

// Start starts the channel polling worker.
func (c *ChanConsumer) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	c.wg.Add(1)
	go c.run(ctx)

	return nil
}

// Stop stops the consumer worker.
func (c *ChanConsumer) Stop() error {
	if c.cancel != nil {
		c.cancel()
	}
	c.wg.Wait()
	return nil
}

func (c *ChanConsumer) run(ctx context.Context) {
	defer c.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case exchange, ok := <-c.endpoint.queue:
			if !ok {
				return
			}
			if err := c.processor.Process(exchange); err != nil {
				logError("chan consumer processing error",
					"uri", c.endpoint.uri,
					"error", err,
					"exchange_id", exchange.ID,
				)
			}
		}
	}
}
