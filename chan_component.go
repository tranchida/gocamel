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
	endpoints map[string]*ChanEndpoint
	mu        sync.Mutex
}

// NewChanComponent creates a new instance of ChanComponent.
func NewChanComponent() *ChanComponent {
	return &ChanComponent{
		endpoints: make(map[string]*ChanEndpoint),
	}
}

// CreateEndpoint creates a new ChanEndpoint from a URI.
// Supported parameters:
//   - bufferSize: The capacity of the internal channel (default 100).
//   - shutdownTimeout: how long Stop keeps processing the queued exchanges, in
//     milliseconds (default 30000; 0 discards them immediately).
//
// Endpoints are cached by channel name so that all producers and consumers of
// the same channel share ONE endpoint instance — this is what makes the
// single-consumer guard in CreateConsumer effective. URI parameters are only
// honoured on first creation; subsequent lookups with different parameters
// log a warning and reuse the existing endpoint.
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
		return nil, fmt.Errorf("channel name is required in URI: %s", RedactURI(uri))
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if endpoint, exists := c.endpoints[channelName]; exists {
		if u.RawQuery != "" && endpoint.uri != uri {
			LogWarn("chan endpoint already exists; URI parameters ignored",
				"channel", channelName, "existing", RedactURI(endpoint.uri), "requested", RedactURI(uri))
		}
		return endpoint, nil
	}

	bufferSize := 100 // default buffer size
	if bufStr := GetConfigValue(u, "bufferSize"); bufStr != "" {
		if b, err := strconv.Atoi(bufStr); err == nil && b >= 0 {
			bufferSize = b
		} else {
			LogWarn("chan: invalid bufferSize; using default", "value", bufStr, "default", bufferSize)
		}
	}

	endpoint := &ChanEndpoint{
		queueEndpointBase: queueEndpointBase{
			uri:             uri,
			queue:           make(chan *Exchange, bufferSize),
			shutdownTimeout: parseShutdownTimeout(u, "chan"),
			stopCh:          make(chan struct{}),
		},
	}
	c.endpoints[channelName] = endpoint
	return endpoint, nil
}

// ChanEndpoint represents a Go channel endpoint.
type ChanEndpoint struct {
	queueEndpointBase
}

// CreateProducer creates a ChanProducer.
func (e *ChanEndpoint) CreateProducer() (Producer, error) {
	return &ChanProducer{endpoint: e}, nil
}

// CreateConsumer creates a ChanConsumer.
func (e *ChanEndpoint) CreateConsumer(processor Processor) (Consumer, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.consumer != nil || e.polling > 0 {
		return nil, fmt.Errorf("multiple consumers for the same channel endpoint are not allowed: %s", RedactURI(e.uri))
	}

	c := &ChanConsumer{
		endpoint: e,
		queueConsumerBase: queueConsumerBase{
			base:      &e.queueEndpointBase,
			processor: processor,
		},
	}
	e.consumer = c
	return c, nil
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
	return p.endpoint.send("chan", exchange)
}

// ChanConsumer reads exchanges from the channel and passes them to the route.
type ChanConsumer struct {
	queueConsumerBase
	endpoint *ChanEndpoint
}

// Start starts the channel polling worker.
func (c *ChanConsumer) Start(ctx context.Context) error {
	return c.queueConsumerBase.start(ctx, c, "chan", "channel", 1, "chan consumer processing error")
}

// Stop rejects new sends and unblocks producers waiting on a full queue, then
// lets the worker finish the exchanges already queued before returning. If
// the parent context is cancelled first, leftover exchanges are discarded as
// failures so they cannot leak after shutdown (see SedaConsumer.Stop).
func (c *ChanConsumer) Stop() error {
	return c.queueConsumerBase.stop(c, "chan")
}
