package gocamel

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"
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
		return nil, fmt.Errorf("channel name is required in URI: %s", uri)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if endpoint, exists := c.endpoints[channelName]; exists {
		if endpoint.uri != uri {
			LogWarn("chan endpoint already exists; URI parameters ignored",
				"channel", channelName, "existing", endpoint.uri, "requested", uri)
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
		uri:             uri,
		queue:           make(chan *Exchange, bufferSize),
		shutdownTimeout: parseShutdownTimeout(u, "chan"),
	}
	c.endpoints[channelName] = endpoint
	return endpoint, nil
}

// ChanEndpoint represents a Go channel endpoint.
type ChanEndpoint struct {
	polling  bool // guarded by mu; mutually exclusive with a continuous consumer
	uri      string
	queue    chan *Exchange
	consumer *ChanConsumer
	// shutdownTimeout bounds draining queued exchanges at Stop.
	shutdownTimeout time.Duration
	mu              sync.Mutex
	stopCh          chan struct{} // closed when the consumer stops; nil until first Start
	stopped         bool          // true after Stop until the next Start
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

	if e.consumer != nil || e.polling {
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

	// Deep-copy the exchange: the consumer runs on its own goroutine and may
	// mutate the body/headers concurrently with the sender. A shallow Copy()
	// would share mutable bodies ([]byte, map, ...) between the two goroutines
	// — a data race. DeepCopy duplicates the common mutable container types.
	copy := exchange.DeepCopy()

	stopCh, err := p.endpoint.beginSend()
	if err != nil {
		return err
	}
	select {
	case p.endpoint.queue <- copy:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-stopCh:
		return fmt.Errorf("chan: consumer stopped: %s", p.endpoint.uri)
	}
}

func (e *ChanEndpoint) beginSend() (<-chan struct{}, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.stopped {
		return nil, fmt.Errorf("chan: consumer stopped: %s", e.uri)
	}
	return e.stopCh, nil
}

// ChanConsumer reads exchanges from the channel and passes them to the route.
type ChanConsumer struct {
	endpoint  *ChanEndpoint
	processor Processor
	cancel    context.CancelFunc
	drain     chan struct{} // closed by Stop: finish the queued exchanges, then exit
	wg        sync.WaitGroup
}

// Start starts the channel polling worker.
func (c *ChanConsumer) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	drain := make(chan struct{})
	c.drain = drain

	c.endpoint.mu.Lock()
	c.endpoint.stopped = false
	c.endpoint.stopCh = make(chan struct{})
	c.endpoint.mu.Unlock()

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		runQueueWorker(ctx, drain, c.endpoint.queue, c.processor, "chan consumer processing error", c.endpoint.uri)
	}()

	return nil
}

// Stop rejects new sends and unblocks producers waiting on a full queue, then
// lets the worker finish the exchanges already queued before returning. If
// the parent context is cancelled first, leftover exchanges are discarded as
// failures so they cannot leak after shutdown (see SedaConsumer.Stop).
func (c *ChanConsumer) Stop() error {
	c.endpoint.mu.Lock()
	c.endpoint.stopped = true
	if c.endpoint.stopCh != nil {
		select {
		case <-c.endpoint.stopCh:
		default:
			close(c.endpoint.stopCh)
		}
	}
	c.endpoint.mu.Unlock()

	stopQueueWorkers(c.drain, &c.wg, c.cancel, c.endpoint.shutdownTimeout)
	c.drain = nil

	c.endpoint.mu.Lock()
	if c.endpoint.consumer == c {
		c.endpoint.consumer = nil
	}
	c.endpoint.mu.Unlock()

	drainExchangeQueue(c.endpoint.queue, fmt.Errorf("chan: discarded because the consumer stopped"))
	return nil
}
