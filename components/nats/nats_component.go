package nats

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"

	gocamel "github.com/tranchida/gocamel"
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
	connUsers           int
	consumer            *NATSConsumer
	mu                  sync.Mutex
}

// closeConn decrements the active user count and closes the endpoint's shared
// NATS connection when the last user releases it.
func (e *NATSEndpoint) closeConn() {
	e.mu.Lock()
	if e.connUsers > 0 {
		e.connUsers--
	}
	if e.connUsers > 0 || e.conn == nil {
		e.mu.Unlock()
		return
	}
	conn := e.conn
	e.conn = nil
	e.mu.Unlock()

	conn.Close()
}

// URI returns the endpoint URI with sensitive parameters masked.
func (e *NATSEndpoint) URI() string {
	return gocamel.RedactURI(e.uri)
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
		return nil, fmt.Errorf("multiple consumers for the same NATS endpoint are not allowed: %s", e.URI())
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
	mu       sync.Mutex
	started  bool
}

// Start starts the producer by opening a NATS connection.
func (p *NATSProducer) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.started {
		return nil
	}

	p.endpoint.mu.Lock()
	defer p.endpoint.mu.Unlock()

	if p.endpoint.conn == nil || p.endpoint.conn.IsClosed() {
		conn, err := nats.Connect(p.endpoint.servers)
		if err != nil {
			return fmt.Errorf("nats producer connect failed: %w", err)
		}
		p.endpoint.conn = conn
	}

	p.endpoint.connUsers++
	p.started = true
	return nil
}

// Stop stops the producer by releasing its reference to the shared NATS connection.
func (p *NATSProducer) Stop() error {
	p.mu.Lock()
	if !p.started {
		p.mu.Unlock()
		return nil
	}
	p.started = false
	p.mu.Unlock()

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

// Bounds of the backoff between attempts to recover from a receive error.
const (
	minRetryDelay = 100 * time.Millisecond
	maxRetryDelay = 5 * time.Second
)

// NATSConsumer subscribes to a NATS subject.
type NATSConsumer struct {
	endpoint  *NATSEndpoint
	processor gocamel.Processor
	subLock   sync.Mutex // Protects sub, replaced by run on resubscription
	sub       *nats.Subscription
	mu        sync.Mutex
	started   bool
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

func (c *NATSConsumer) subscription() *nats.Subscription {
	c.subLock.Lock()
	defer c.subLock.Unlock()
	return c.sub
}

// Start opens NATS connection, subscribes to subject and handles incoming messages.
func (c *NATSConsumer) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started {
		return nil
	}

	c.endpoint.mu.Lock()
	defer c.endpoint.mu.Unlock()

	if c.endpoint.subject == "" {
		return fmt.Errorf("nats consumer: subject is required to subscribe")
	}

	createdConn := false
	if c.endpoint.conn == nil || c.endpoint.conn.IsClosed() {
		conn, err := nats.Connect(c.endpoint.servers)
		if err != nil {
			return fmt.Errorf("nats consumer connect failed: %w", err)
		}
		c.endpoint.conn = conn
		createdConn = true
	}

	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	// Simple subscription
	sub, err := c.endpoint.conn.SubscribeSync(c.endpoint.subject)
	if err != nil {
		cancel()
		if createdConn && c.endpoint.connUsers == 0 {
			c.endpoint.conn.Close()
			c.endpoint.conn = nil
		}
		return fmt.Errorf("nats subscription failed: %w", err)
	}
	c.subLock.Lock()
	c.sub = sub
	c.subLock.Unlock()
	c.endpoint.connUsers++
	c.started = true

	c.wg.Add(1)
	go c.run(ctx)

	return nil
}

// Stop unsubscribes and releases the endpoint's shared NATS connection. The
// connection is closed via closeConn when the last user releases it.
func (c *NATSConsumer) Stop() error {
	c.mu.Lock()
	wasStarted := c.started
	c.started = false
	c.mu.Unlock()

	if c.cancel != nil {
		c.cancel()
	}
	// Unsubscribing wakes run up from NextMsg instead of waiting for its timeout.
	if sub := c.subscription(); sub != nil {
		_ = sub.Unsubscribe()
	}
	c.wg.Wait()

	if wasStarted {
		c.endpoint.closeConn()
	}

	c.endpoint.mu.Lock()
	if c.endpoint.consumer == c {
		c.endpoint.consumer = nil
	}
	c.endpoint.mu.Unlock()
	return nil
}

func (c *NATSConsumer) run(ctx context.Context) {
	defer c.wg.Done()
	// A subscription created by a resubscription racing Stop is released here.
	defer func() {
		if sub := c.subscription(); sub != nil {
			_ = sub.Unsubscribe()
		}
	}()

	failures := 0
	for ctx.Err() == nil {
		// Fetch NATS message with timeout
		msg, err := c.subscription().NextMsg(nats.DefaultTimeout)
		if err != nil {
			if errors.Is(err, nats.ErrTimeout) {
				continue // loop and check context cancellation
			}
			if ctx.Err() != nil {
				return
			}
			// Connection closed, subscription invalidated, slow consumer...:
			// returning would leave the route started with no consumer.
			delay := retryDelay(failures)
			failures++
			gocamel.LogWarn("nats receive failed; retrying",
				"uri", c.endpoint.URI(), "retry_in", delay, "error", err)
			if !waitForRetry(ctx, delay) {
				return
			}
			if err := c.resubscribe(); err != nil {
				gocamel.LogWarn("nats resubscription failed; retrying",
					"uri", c.endpoint.URI(), "error", err)
			}
			continue
		}
		failures = 0

		exchange := gocamel.NewExchange(ctx)
		exchange.In.SetBody(msg.Data)
		exchange.In.SetHeader(CamelNATSSubject, msg.Subject)

		if err := gocamel.CompleteExchange(exchange, gocamel.ProcessSafely(c.processor, exchange)); err != nil {
			gocamel.LogError("nats consumer processing error",
				"uri", c.endpoint.URI(),
				"error", err,
				"exchange_id", exchange.ID,
			)
		}
	}
}

// resubscribe replaces an invalid subscription, reconnecting first when the
// shared connection is closed. A still-valid subscription is kept: the error
// was transient (e.g. slow consumer) and the next NextMsg may succeed.
func (c *NATSConsumer) resubscribe() error {
	if c.subscription().IsValid() {
		return nil
	}

	conn, err := c.liveConn()
	if err != nil {
		return err
	}
	sub, err := conn.SubscribeSync(c.endpoint.subject)
	if err != nil {
		return fmt.Errorf("nats subscription to %q failed: %w", c.endpoint.subject, err)
	}

	c.subLock.Lock()
	c.sub = sub
	c.subLock.Unlock()
	return nil
}

// liveConn returns the endpoint's shared connection, replacing it when it is
// closed. The connection is established outside the endpoint lock so that a
// slow broker does not block the producer or Stop.
func (c *NATSConsumer) liveConn() (*nats.Conn, error) {
	e := c.endpoint
	e.mu.Lock()
	conn := e.conn
	e.mu.Unlock()
	if conn != nil && !conn.IsClosed() {
		return conn, nil
	}

	newConn, err := nats.Connect(e.servers)
	if err != nil {
		return nil, fmt.Errorf("nats consumer reconnect failed: %w", err)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.connUsers == 0 {
		newConn.Close()
		return nil, errors.New("nats endpoint has no active users")
	}
	if e.conn != nil && e.conn != conn && !e.conn.IsClosed() {
		// The producer reconnected concurrently: share its connection.
		newConn.Close()
		return e.conn, nil
	}
	e.conn = newConn
	return newConn, nil
}

// retryDelay returns the exponential backoff before the given retry attempt.
func retryDelay(attempt int) time.Duration {
	delay := minRetryDelay
	for i := 0; i < attempt && delay < maxRetryDelay; i++ {
		delay *= 2
	}
	return min(delay, maxRetryDelay)
}

// waitForRetry sleeps for delay and reports false if ctx ended first.
func waitForRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
