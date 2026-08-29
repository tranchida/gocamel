package nats

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/nats-io/nats.go"

	gocamel "gitlab.com/tranchida/gocamel"
)

// NATS Component constants
const (
	CamelNATSSubject = "CamelNATSSubject"
)

// NATSComponent represents the Camel NATS component.
type NATSComponent struct{}

// NewNATSComponent creates a new NATSComponent instance.
func NewNATSComponent() *NATSComponent {
	return &NATSComponent{}
}

// CreateEndpoint creates a new NATSEndpoint from a URI.
// Example URIs:
//
//	nats:news?servers=nats://localhost:4222
//	nats:orders
//
// Options:
//
//	subject             Default subject (otherwise taken from host + path)
//	servers             Comma-separated NATS server URLs (default: nats.DefaultURL)
//	allowHeaderOverride true to let the CamelNATSSubject header replace the
//	                    configured subject (default: false — see NATSProducer.Send)
func (c *NATSComponent) CreateEndpoint(uri string) (gocamel.Endpoint, error) {
	u, err := gocamel.ParseURI(uri)
	if err != nil {
		return nil, fmt.Errorf("invalid NATS URI: %w", err)
	}

	// SEDA-like subject identification: host + path.
	subject := u.Host + u.Path
	if subject == "" {
		subject = u.Opaque
	}
	if subject == "" {
		subject = gocamel.GetConfigValue(u, "subject")
	}

	servers := gocamel.GetConfigValue(u, "servers")
	if servers == "" {
		servers = nats.DefaultURL // "nats://127.0.0.1:4222"
	}

	allowHeaderOverride := false
	if v := gocamel.GetConfigValue(u, "allowHeaderOverride"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			allowHeaderOverride = b
		}
	}

	return &NATSEndpoint{
		uri:                 uri,
		servers:             servers,
		subject:             subject,
		allowHeaderOverride: allowHeaderOverride,
	}, nil
}

// NATSEndpoint represents a NATS endpoint.
type NATSEndpoint struct {
	uri                 string
	servers             string
	subject             string
	allowHeaderOverride bool // opt-in: the CamelNATSSubject header may override the subject
	conn                *nats.Conn
	consumer            *NATSConsumer
	mu                  sync.Mutex
}

// closeConn closes the endpoint's shared NATS connection exactly once.
//
// The producer and the consumer created from the same endpoint share one
// connection, and both call Stop() on it (via Route.Stop and
// CamelContext.Stop). Closing twice — or racing a Close against a Send that
// already grabbed the conn reference — is a use-after-close. sync.Once makes
// the shutdown idempotent; the mutex serialises the close against connection
// establishment and Send's conn snapshot.
func (e *NATSEndpoint) closeConn() {
	e.mu.Lock()
	conn := e.conn
	e.conn = nil
	e.mu.Unlock()

	if conn != nil {
		conn.Close()
	}
}

// URI returns the endpoint URI.
func (e *NATSEndpoint) URI() string {
	return e.uri
}

// CreateProducer creates a NATSProducer.
func (e *NATSEndpoint) CreateProducer() (gocamel.Producer, error) {
	return &NATSProducer{endpoint: e}, nil
}

// CreateConsumer creates a NATSConsumer.
func (e *NATSEndpoint) CreateConsumer(processor gocamel.Processor) (gocamel.Consumer, error) {
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

// Stop stops the producer by closing the shared NATS connection.
func (p *NATSProducer) Stop() error {
	p.endpoint.closeConn()
	return nil
}

// Send publishes the exchange body to NATS.
func (p *NATSProducer) Send(exchange *gocamel.Exchange) error {
	p.endpoint.mu.Lock()
	conn := p.endpoint.conn
	p.endpoint.mu.Unlock()

	if conn == nil {
		return fmt.Errorf("nats producer is not started or connected")
	}

	// Determine NATS subject. The CamelNATSSubject header replaces the
	// configured subject wholesale, so it is honoured only when the endpoint
	// opts in with allowHeaderOverride=true. Enable it only where headers
	// cannot be influenced by untrusted input: any component that maps
	// external metadata onto headers would otherwise give an attacker control
	// of the publish target.
	subject := p.endpoint.subject
	if p.endpoint.allowHeaderOverride {
		if hSubject, ok := exchange.GetHeaderAsString(CamelNATSSubject); ok {
			// Lightweight validation: NATS subjects must be non-empty tokens
			// separated by dots and cannot contain spaces.
			if hSubject == "" || strings.Contains(hSubject, " ") {
				return fmt.Errorf("nats: invalid subject %q in CamelNATSSubject header: must be non-empty and contain no spaces", hSubject)
			}
			subject = hSubject
		}
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
	processor gocamel.Processor
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

// Stop unsubscribes and releases the endpoint's shared NATS connection. The
// connection is closed via closeConn, which is safe when the producer is
// still active on the same endpoint: nats.Conn is safe for concurrent use,
// and the mutex prevents a Close from racing a subsequent Send's snapshot.
func (c *NATSConsumer) Stop() error {
	if c.cancel != nil {
		c.cancel()
	}
	if c.sub != nil {
		_ = c.sub.Unsubscribe()
	}
	c.wg.Wait()

	c.endpoint.closeConn()

	c.endpoint.mu.Lock()
	if c.endpoint.consumer == c {
		c.endpoint.consumer = nil
	}
	c.endpoint.mu.Unlock()
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

			exchange := gocamel.NewExchange(ctx)
			exchange.In.SetBody(msg.Data)
			exchange.In.SetHeader(CamelNATSSubject, msg.Subject)

			if err := gocamel.CompleteExchange(exchange, gocamel.ProcessSafely(c.processor, exchange)); err != nil {
				gocamel.LogError("nats consumer processing error",
					"uri", c.endpoint.uri,
					"error", err,
					"exchange_id", exchange.ID,
				)
			}
		}
	}
}
