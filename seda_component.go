package gocamel

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"sync"
	"time"
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
//   - size: The capacity of the internal queue (default 1000).
//   - concurrentConsumers: The number of worker goroutines (default 1).
//   - shutdownTimeout: how long Stop keeps processing the queued exchanges, in
//     milliseconds (default 30000; 0 discards them immediately).
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
	if queueName == "" {
		return nil, fmt.Errorf("SEDA queue name is required in URI: %s", RedactURI(uri))
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if endpoint, exists := c.endpoints[queueName]; exists {
		// Options only apply to the first URI that creates the queue (often a
		// plain "seda:name" producer): say so instead of silently ignoring a
		// consumer's size or concurrentConsumers.
		if u.RawQuery != "" && endpoint.uri != uri {
			LogWarn("seda endpoint already exists; URI parameters ignored",
				"queue", queueName, "existing", RedactURI(endpoint.uri), "requested", RedactURI(uri))
		}
		return endpoint, nil
	}

	size := 1000 // default size
	if sizeStr := GetConfigValue(u, "size"); sizeStr != "" {
		if s, err := strconv.Atoi(sizeStr); err == nil && s >= 0 {
			size = s
		} else {
			LogWarn("seda: invalid size; using default", "value", sizeStr, "default", size)
		}
	}

	concurrentConsumers := 1 // default consumers
	if consumersStr := GetConfigValue(u, "concurrentConsumers"); consumersStr != "" {
		if cc, err := strconv.Atoi(consumersStr); err == nil && cc > 0 {
			concurrentConsumers = cc
		} else {
			LogWarn("seda: invalid concurrentConsumers; using default", "value", consumersStr, "default", concurrentConsumers)
		}
	}

	endpoint := &SedaEndpoint{
		queueEndpointBase: queueEndpointBase{
			uri:             uri,
			queue:           make(chan *Exchange, size),
			shutdownTimeout: parseShutdownTimeout(u, "seda"),
			stopCh:          make(chan struct{}),
		},
		concurrentConsumers: concurrentConsumers,
	}
	c.endpoints[queueName] = endpoint
	return endpoint, nil
}

type queueEndpointBase struct {
	polling         int // guarded by mu; active Poll calls, mutually exclusive with a continuous consumer
	uri             string
	queue           chan *Exchange
	shutdownTimeout time.Duration // bound on draining queued exchanges at Stop
	consumer        Consumer      // active/registered consumer; guarded by mu
	mu              sync.Mutex
	stopCh          chan struct{}  // closed when the consumer stops; initialized in CreateEndpoint
	stopped         bool           // true after Stop until the next Start
	senders         sync.WaitGroup // in-flight Send calls; Add is guarded by mu
}

// URI returns the endpoint URI.
func (e *queueEndpointBase) URI() string {
	return e.uri
}

func (e *queueEndpointBase) beginSend(scheme string) (<-chan struct{}, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.stopped {
		return nil, fmt.Errorf("%s: consumer stopped: %s", scheme, RedactURI(e.uri))
	}
	e.senders.Add(1)
	return e.stopCh, nil
}

func (e *queueEndpointBase) send(scheme string, exchange *Exchange) error {
	// Deep-copy the exchange: the consumer runs on its own goroutine and may
	// mutate the body/headers concurrently with the sender. A shallow Copy()
	// would share mutable bodies ([]byte, map, ...) between the two goroutines
	// — a data race. DeepCopy duplicates the common mutable container types.
	queued := exchange.DeepCopy()

	// A nil exchange context must not panic here; fall back to a blocking
	// enqueue, which is the behaviour of an uncancellable context.
	ctx := exchange.Context
	if ctx == nil {
		ctx = context.Background()
	}

	stopCh, err := e.beginSend(scheme)
	if err != nil {
		return err
	}
	defer e.senders.Done()
	select {
	case e.queue <- queued:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-stopCh:
		return fmt.Errorf("%s: consumer stopped: %s", scheme, RedactURI(e.uri))
	}
}

// SedaEndpoint represents a SEDA endpoint.
type SedaEndpoint struct {
	queueEndpointBase
	concurrentConsumers int
}

// CreateProducer creates a SEDA producer.
func (e *SedaEndpoint) CreateProducer() (Producer, error) {
	return &SedaProducer{endpoint: e}, nil
}

// CreateConsumer creates a SEDA consumer.
func (e *SedaEndpoint) CreateConsumer(processor Processor) (Consumer, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.consumer != nil || e.polling > 0 {
		return nil, fmt.Errorf("multiple consumers for the same SEDA endpoint are not allowed: %s", RedactURI(e.uri))
	}

	c := &SedaConsumer{
		endpoint: e,
		queueConsumerBase: queueConsumerBase{
			base:      &e.queueEndpointBase,
			processor: processor,
		},
	}
	e.consumer = c
	return c, nil
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
// It creates a deep copy of the exchange so the queued exchange is fully
// independent of the caller's (see queueEndpointBase.send).
func (p *SedaProducer) Send(exchange *Exchange) error {
	return p.endpoint.send("seda", exchange)
}

// acquireConsumerTransition serializes Start and Stop transitions without
// holding mu while waiting for an in-progress transition.
func acquireConsumerTransition(mu *sync.Mutex, transition *chan struct{}) {
	for {
		mu.Lock()
		if *transition != nil {
			done := *transition
			mu.Unlock()
			<-done
			continue
		}
		*transition = make(chan struct{})
		mu.Unlock()
		return
	}
}

// releaseConsumerTransition marks the active Start/Stop transition complete and
// wakes any waiting callers.
func releaseConsumerTransition(mu *sync.Mutex, transition *chan struct{}) {
	mu.Lock()
	close(*transition)
	*transition = nil
	mu.Unlock()
}

// isClosedChan reports whether ch is non-nil and already closed.
func isClosedChan(ch <-chan struct{}) bool {
	if ch == nil {
		return false
	}
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// ensureOpenStopCh returns ch if it is non-nil and still open, or allocates a
// fresh open channel when ch is nil or closed. Must be called under the owning mutex.
func ensureOpenStopCh(ch chan struct{}) chan struct{} {
	if ch == nil || isClosedChan(ch) {
		return make(chan struct{})
	}
	return ch
}

// closeStopCh closes ch if it is non-nil and not yet closed. Must be called
// under the owning mutex.
func closeStopCh(ch chan struct{}) {
	if ch != nil && !isClosedChan(ch) {
		close(ch)
	}
}

type queueConsumerBase struct {
	base       *queueEndpointBase
	processor  Processor
	mu         sync.Mutex
	transition chan struct{}
	started    bool
	cancel     context.CancelFunc
	drain      chan struct{} // closed by Stop: finish the queued exchanges, then exit
	wg         sync.WaitGroup
}

func (c *queueConsumerBase) start(ctx context.Context, owner Consumer, scheme, endpointKind string, workers int, errMsg string) error {
	if ctx == nil {
		ctx = context.Background()
	}

	acquireConsumerTransition(&c.mu, &c.transition)
	defer releaseConsumerTransition(&c.mu, &c.transition)

	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return fmt.Errorf("%s consumer is already running: %s", scheme, RedactURI(c.base.uri))
	}
	c.mu.Unlock()

	c.base.mu.Lock()
	if (c.base.consumer != nil && c.base.consumer != owner) || c.base.polling > 0 {
		c.base.mu.Unlock()
		return fmt.Errorf("multiple consumers for the same %s endpoint are not allowed: %s", endpointKind, RedactURI(c.base.uri))
	}
	c.base.consumer = owner
	c.base.stopped = false
	c.base.stopCh = ensureOpenStopCh(c.base.stopCh)
	c.base.mu.Unlock()

	runCtx, cancel := context.WithCancel(ctx)
	drain := make(chan struct{})

	c.mu.Lock()
	c.cancel = cancel
	c.drain = drain
	c.started = true
	for i := 0; i < workers; i++ {
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			runQueueWorker(runCtx, drain, c.base.queue, c.processor, errMsg, RedactURI(c.base.uri))
		}()
	}
	c.mu.Unlock()

	return nil
}

func (c *queueConsumerBase) stop(owner Consumer, scheme string) error {
	acquireConsumerTransition(&c.mu, &c.transition)
	defer releaseConsumerTransition(&c.mu, &c.transition)

	c.mu.Lock()
	started := c.started
	drain := c.drain
	cancel := c.cancel
	c.mu.Unlock()

	c.base.mu.Lock()
	owned := c.base.consumer == owner
	if !started && !owned {
		c.base.mu.Unlock()
		return nil
	}
	c.base.stopped = true
	closeStopCh(c.base.stopCh)
	c.base.mu.Unlock()

	// Every Send that passed beginSend either enqueued or observed stopCh.
	// Wait for them before the workers/drain touch the queue, so no exchange
	// can be enqueued after the final drain.
	c.base.senders.Wait()

	if started {
		stopQueueWorkers(drain, &c.wg, cancel, c.base.shutdownTimeout)
	}

	c.mu.Lock()
	c.drain = nil
	c.cancel = nil
	c.started = false
	c.mu.Unlock()

	c.base.mu.Lock()
	if c.base.consumer == owner {
		c.base.consumer = nil
	}
	c.base.mu.Unlock()

	drainExchangeQueue(c.base.queue, fmt.Errorf("%s: discarded because the consumer stopped", scheme))
	return nil
}

// SedaConsumer represents a SEDA consumer.
type SedaConsumer struct {
	queueConsumerBase
	endpoint *SedaEndpoint
}

// Start starts the SEDA consumer workers.
func (c *SedaConsumer) Start(ctx context.Context) error {
	return c.queueConsumerBase.start(ctx, c, "seda", "SEDA", c.endpoint.concurrentConsumers, "SEDA consumer processing error")
}

// Stop rejects new sends and unblocks producers waiting on a full queue, then
// lets the workers finish the exchanges already queued (graceful shutdown)
// before returning. If the parent context is cancelled first (GracefulStop,
// or its timeout), the workers exit immediately and the leftover exchanges are
// discarded as failures so they cannot leak after shutdown.
func (c *SedaConsumer) Stop() error {
	return c.queueConsumerBase.stop(c, "seda")
}

// drainExchangeQueue discards leftover exchanges after a consumer stops so they
// cannot sit in the buffer forever. Synchronizations still fire (as a failure).
func drainExchangeQueue(queue chan *Exchange, reason error) {
	for {
		select {
		case ex, ok := <-queue:
			if !ok {
				return
			}
			if ex != nil {
				_ = CompleteExchange(ex, reason)
			}
		default:
			return
		}
	}
}

// DefaultQueueShutdownTimeout bounds how long Stop waits for a seda or chan
// consumer to finish the exchanges already queued before cancelling them.
const DefaultQueueShutdownTimeout = 30 * time.Second

// parseShutdownTimeout reads the shutdownTimeout URI option (milliseconds).
func parseShutdownTimeout(u *url.URL, scheme string) time.Duration {
	timeout := DefaultQueueShutdownTimeout
	if v := GetConfigValue(u, "shutdownTimeout"); v != "" {
		if ms, err := strconv.Atoi(v); err == nil && ms >= 0 {
			timeout = time.Duration(ms) * time.Millisecond
		} else {
			LogWarn(scheme+": invalid shutdownTimeout; using default", "value", v, "default", timeout)
		}
	}
	return timeout
}

// stopQueueWorkers asks the workers to finish the queued exchanges (drain) and
// waits for them for at most timeout, then cancels ctx so in-flight exchanges
// observe the shutdown, and waits for the workers to return.
func stopQueueWorkers(drain chan struct{}, wg *sync.WaitGroup, cancel context.CancelFunc, timeout time.Duration) {
	if timeout <= 0 {
		if cancel != nil {
			cancel()
		}
		if drain != nil {
			close(drain)
		}
		wg.Wait()
		return
	}
	if drain != nil {
		close(drain)
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
	if cancel != nil {
		cancel()
	}
	<-done
}

// runQueueWorker is one worker of a queue-backed consumer (seda, chan). It
// processes exchanges until ctx is cancelled, or until drain is closed and the
// queue is empty.
func runQueueWorker(ctx context.Context, drain <-chan struct{}, queue chan *Exchange, processor Processor, errMsg, uri string) {
	handle := func(exchange *Exchange) {
		// The exchange still carries the sender's context, which is typically
		// done by now (an HTTP request that returned right after enqueuing).
		// The asynchronous leg runs under the consumer's lifecycle instead.
		exchange.Context = ctx
		err := safeProcess(func() error { return processor.Process(exchange) })
		if err := CompleteExchange(exchange, err); err != nil {
			LogError(errMsg, "uri", uri, "error", err, "exchange_id", exchange.ID)
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		select {
		case <-ctx.Done():
			return
		case <-drain:
			for {
				if ctx.Err() != nil {
					return
				}
				select {
				case exchange, ok := <-queue:
					if !ok {
						return
					}
					handle(exchange)
				default:
					return
				}
			}
		case exchange, ok := <-queue:
			if !ok {
				return
			}
			handle(exchange)
		}
	}
}
