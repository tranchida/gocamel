package gocamel

import (
	"context"
	"fmt"
	"sync"

	"github.com/nats-io/nats.go"
)

// NATS Component constants
const (
	CamelNATSSubject = "CamelNATSSubject"
)

// NATSComponent represents the Camel NATS component.
type NATSComponent struct {
	mu sync.Mutex
}

// NewNATSComponent creates a new NATSComponent instance.
func NewNATSComponent() *NATSComponent {
	return &NATSComponent{}
}

// CreateEndpoint creates a new NATSEndpoint from a URI.
// Example URIs:
//   nats:news?servers=nats://localhost:4222
//   nats:orders
func (c *NATSComponent) CreateEndpoint(uri string) (Endpoint, error) {
	u, err := ParseURI(uri)
	if err != nil {
		return nil, fmt.Errorf("invalid NATS URI: %w", err)
	}

	// SEDA-like subject identification: host + path.
	subject := u.Host + u.Path
	if subject == "" {
		subject = u.Opaque
	}
	if subject == "" {
		subject = GetConfigValue(u, "subject")
	}

	servers := GetConfigValue(u, "servers")
	if servers == "" {
		servers = nats.DefaultURL // "nats://127.0.0.1:4222"
	}

	return &NATSEndpoint{
		uri:     uri,
		servers: servers,
		subject: subject,
	}, nil
}

// NATSEndpoint represents a NATS endpoint.
type NATSEndpoint struct {
	uri      string
	servers  string
	subject  string
	conn     *nats.Conn
	consumer *NATSConsumer
	mu       sync.Mutex
}

// URI returns the endpoint URI.
func (e *NATSEndpoint) URI() string {
	return e.uri
}

// CreateProducer creates a NATSProducer.
func (e *NATSEndpoint) CreateProducer() (Producer, error) {
	return &NATSProducer{endpoint: e}, nil
}

// CreateConsumer creates a NATSConsumer.
func (e *NATSEndpoint) CreateConsumer(processor Processor) (Consumer, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.consumer != nil {
		return nil, fmt.Errorf("multiple consumers for the same NATS endpoint are not allowed: %s", e.uri)
	}

	e.consumer = &NATSConsumer{
		endpoint:  e,
		processor: processor,
	}
	return e.consumer, nil
}

// NATSProducer publishes messages to NATS.
type NATSProducer struct {
	endpoint *NATSEndpoint
}

// Start starts the producer by opening a NATS connection.
func (p *NATSProducer) Start(ctx context.Context) error {
	p.endpoint.mu.Lock()
	defer p.endpoint.mu.Unlock()

	if p.endpoint.conn != nil {
		return nil
	}

	conn, err := nats.Connect(p.endpoint.servers)
	if err != nil {
		return fmt.Errorf("nats producer connect failed: %w", err)
	}

	p.endpoint.conn = conn
	return nil
}

// Stop stops the producer and closes the NATS connection.
func (p *NATSProducer) Stop() error {
	p.endpoint.mu.Lock()
	defer p.endpoint.mu.Unlock()

	if p.endpoint.conn != nil {
		p.endpoint.conn.Close()
		p.endpoint.conn = nil
	}
	return nil
}

// Send publishes the exchange body to NATS.
func (p *NATSProducer) Send(exchange *Exchange) error {
	p.endpoint.mu.Lock()
	conn := p.endpoint.conn
	p.endpoint.mu.Unlock()

	if conn == nil {
		return fmt.Errorf("nats producer is not started or connected")
	}

	// Determine NATS subject
	subject := p.endpoint.subject
	if hSubject, ok := exchange.GetHeaderAsString(CamelNATSSubject); ok {
		subject = hSubject
	}

	if subject == "" {
		return fmt.Errorf("nats: subject is required to publish a message")
	}

	body := exchange.In.GetBody()
	var payload []byte
	switch b := body.(type) {
	case []byte:
		payload = b
	case string:
		payload = []byte(b)
	case nil:
		payload = []byte{}
	default:
		payload = []byte(fmt.Sprintf("%v", b))
	}

	err := conn.Publish(subject, payload)
	if err != nil {
		return fmt.Errorf("nats publish error on subject %q: %w", subject, err)
	}

	return nil
}

// NATSConsumer subscribes to a NATS subject.
type NATSConsumer struct {
	endpoint  *NATSEndpoint
	processor Processor
	sub       *nats.Subscription
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

// Start opens NATS connection, subscribes to subject and handles incoming messages.
func (c *NATSConsumer) Start(ctx context.Context) error {
	c.endpoint.mu.Lock()
	defer c.endpoint.mu.Unlock()

	if c.endpoint.subject == "" {
		return fmt.Errorf("nats consumer: subject is required to subscribe")
	}

	if c.endpoint.conn == nil {
		conn, err := nats.Connect(c.endpoint.servers)
		if err != nil {
			return fmt.Errorf("nats consumer connect failed: %w", err)
		}
		c.endpoint.conn = conn
	}

	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	// Simple subscription
	sub, err := c.endpoint.conn.SubscribeSync(c.endpoint.subject)
	if err != nil {
		cancel()
		return fmt.Errorf("nats subscription failed: %w", err)
	}
	c.sub = sub

	c.wg.Add(1)
	go c.run(ctx)

	return nil
}

// Stop unsubscribes, closes subscription and NATS connection.
func (c *NATSConsumer) Stop() error {
	if c.cancel != nil {
		c.cancel()
	}
	if c.sub != nil {
		_ = c.sub.Unsubscribe()
	}
	c.wg.Wait()

	c.endpoint.mu.Lock()
	defer c.endpoint.mu.Unlock()
	if c.endpoint.conn != nil {
		c.endpoint.conn.Close()
		c.endpoint.conn = nil
	}
	return nil
}

func (c *NATSConsumer) run(ctx context.Context) {
	defer c.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		default:
			// Fetch NATS message with timeout
			msg, err := c.sub.NextMsg(nats.DefaultTimeout)
			if err != nil {
				if err == nats.ErrTimeout {
					continue // loop and check context cancellation
				}
				// Subscription closed or other error
				return
			}

			exchange := NewExchange(ctx)
			exchange.In.SetBody(msg.Data)
			exchange.In.SetHeader(CamelNATSSubject, msg.Subject)

			if err := c.processor.Process(exchange); err != nil {
				logError("nats consumer processing error",
					"uri", c.endpoint.uri,
					"error", err,
					"exchange_id", exchange.ID,
				)
			}
		}
	}
}
