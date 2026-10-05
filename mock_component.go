package gocamel

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// MockComponent provides "mock:name" endpoints for testing routes. A mock
// endpoint is producer-only: every exchange sent to it is recorded and
// completes normally, so tests can assert on what a route produced.
//
// Endpoints are cached by name (query parameters are ignored), so
// "mock:out" resolved by a route and by CamelContext.GetMockEndpoint is the
// same *MockEndpoint.
type MockComponent struct {
	mu        sync.Mutex // Protects endpoints.
	endpoints map[string]*MockEndpoint
}

// NewMockComponent creates a new MockComponent.
func NewMockComponent() *MockComponent {
	return &MockComponent{endpoints: make(map[string]*MockEndpoint)}
}

// CreateEndpoint returns the mock endpoint for the URI's name, creating it on
// first use.
func (c *MockComponent) CreateEndpoint(uri string) (Endpoint, error) {
	u, err := ParseURI(uri)
	if err != nil {
		return nil, fmt.Errorf("invalid mock URI: %w", err)
	}
	name := u.Opaque
	if name == "" {
		name = u.Host + u.Path
	}
	if name == "" {
		return nil, fmt.Errorf("invalid mock URI %q: missing endpoint name", uri)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if endpoint, ok := c.endpoints[name]; ok {
		return endpoint, nil
	}
	endpoint := &MockEndpoint{uri: "mock:" + name, changed: make(chan struct{})}
	c.endpoints[name] = endpoint
	return endpoint, nil
}

// GetMockEndpoint returns the mock endpoint registered under the "mock"
// scheme. uri may be a full URI ("mock:out") or a bare name ("out"). The
// endpoint is created if no route has resolved it yet, so tests can set
// expectations before Start.
func (c *CamelContext) GetMockEndpoint(uri string) (*MockEndpoint, error) {
	if !strings.HasPrefix(uri, "mock:") {
		uri = "mock:" + uri
	}
	endpoint, err := c.CreateEndpoint(uri)
	if err != nil {
		return nil, err
	}
	mock, ok := endpoint.(*MockEndpoint)
	if !ok {
		return nil, fmt.Errorf("endpoint %s is not a mock endpoint (%T)", uri, endpoint)
	}
	return mock, nil
}

// MockEndpoint records the exchanges sent to it and supports a minimum
// message count expectation. All methods are safe for concurrent use.
//
// Each exchange is recorded as a DeepCopy taken at send time (keeping the
// original exchange ID), so later mutation of the live exchange — including
// of map/slice bodies and headers — does not change the snapshot. Values of
// other reference types are shared, as documented on Message.DeepCopy.
type MockEndpoint struct {
	uri      string
	mu       sync.Mutex // Protects received, expected and changed.
	received []*Exchange
	expected int
	// changed is closed and replaced whenever received changes, waking every
	// Wait call without polling.
	changed chan struct{}
}

// URI returns the endpoint URI ("mock:name").
func (e *MockEndpoint) URI() string { return e.uri }

// CreateProducer creates a producer that records exchanges on this endpoint.
func (e *MockEndpoint) CreateProducer() (Producer, error) {
	return &MockProducer{endpoint: e}, nil
}

// CreateConsumer returns an error: mock endpoints are producer-only.
func (e *MockEndpoint) CreateConsumer(processor Processor) (Consumer, error) {
	return nil, fmt.Errorf("the mock component does not support consumers")
}

func (e *MockEndpoint) record(exchange *Exchange) {
	snapshot := exchange.DeepCopy()
	snapshot.ID = exchange.ID

	e.mu.Lock()
	e.received = append(e.received, snapshot)
	close(e.changed)
	e.changed = make(chan struct{})
	e.mu.Unlock()
}

// MessageCount returns the number of exchanges received so far.
func (e *MockEndpoint) MessageCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.received)
}

// Message returns the snapshot of the i-th received exchange (0-based, in
// arrival order), or nil if i is out of range. Treat it as read-only.
func (e *MockEndpoint) Message(i int) *Exchange {
	e.mu.Lock()
	defer e.mu.Unlock()
	if i < 0 || i >= len(e.received) {
		return nil
	}
	return e.received[i]
}

// Body returns the In body of the i-th received exchange, or nil if i is out
// of range.
func (e *MockEndpoint) Body(i int) any {
	exchange := e.Message(i)
	if exchange == nil {
		return nil
	}
	return exchange.In.Body
}

// Header returns an In header of the i-th received exchange. ok is false if
// the header is absent or i is out of range.
func (e *MockEndpoint) Header(i int, name string) (any, bool) {
	exchange := e.Message(i)
	if exchange == nil {
		return nil, false
	}
	return exchange.In.GetHeader(name)
}

// ExpectedMessageCount sets the expectation checked by Wait: at least n
// exchanges must have been received. Receiving more than n still satisfies
// it (Wait does not detect surplus messages; assert MessageCount for an exact
// count). The expectation stays in force until changed or Reset. n <= 0
// clears it.
func (e *MockEndpoint) ExpectedMessageCount(n int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.expected = max(n, 0)
}

// Wait blocks until the expectation set by ExpectedMessageCount is met or
// timeout elapses. It returns immediately when no expectation is set. On
// timeout the error reports the expected and actual counts.
func (e *MockEndpoint) Wait(timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		e.mu.Lock()
		expected, actual, changed := e.expected, len(e.received), e.changed
		e.mu.Unlock()
		if actual >= expected {
			return nil
		}
		select {
		case <-changed:
		case <-timer.C:
			return fmt.Errorf("mock endpoint %s: expected at least %d messages but received %d within %s", e.uri, expected, actual, timeout)
		}
	}
}

// Reset clears the received exchanges and the expectation. Endpoints are
// cached per CamelContext, so reuse across tests sharing a context needs it.
func (e *MockEndpoint) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.received = nil
	e.expected = 0
	close(e.changed)
	e.changed = make(chan struct{})
}

// MockProducer records every exchange it receives on its MockEndpoint.
type MockProducer struct {
	endpoint *MockEndpoint
}

// Start starts the producer.
func (p *MockProducer) Start(ctx context.Context) error { return nil }

// Stop stops the producer.
func (p *MockProducer) Stop() error { return nil }

// Send records the exchange and completes it normally, leaving it unchanged.
func (p *MockProducer) Send(exchange *Exchange) error {
	p.endpoint.record(exchange)
	return nil
}
