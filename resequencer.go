package gocamel

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"
)

// Resequencer implements the Resequencer EIP (Stream mode).
// It reorders messages based on a sequence number or timestamp extracted via an expression.
// DefaultResequencerCapacity is the default maximum number of out-of-order
// exchanges retained by a Resequencer. SetCapacity(0) disables the bound.
const DefaultResequencerCapacity = 10000

type Resequencer struct {
	expression func(*Exchange) (int64, error)
	timeout    time.Duration
	nextSeq    int64
	capacity   int
	buffer     []*resequencerEntry
	mu         sync.Mutex
	processor  Processor
	timer      *time.Timer
	timerGen   uint64 // incremented each time a new timer is created; guards against stale callbacks
	// lifecycleCtx is the context of the running route (set by Route.Start,
	// guarded by mu). Buffered exchanges are delivered under it.
	lifecycleCtx context.Context
	delivering   bool // one goroutine at a time delivers, without holding mu
	stopped      bool // set by Stop: no timer is armed until the next Process
	stopping     bool // set while Stop is waiting for in-flight delivery
	// timeoutWG tracks active downstream delivery (synchronous or timeout), so
	// Stop can wait for it before the route stops its producers.
	timeoutWG sync.WaitGroup
}

type resequencerEntry struct {
	seq      int64
	exchange *Exchange
}

// NewResequencer creates a new Resequencer processor.
func NewResequencer(expression func(*Exchange) (int64, error), nextSeq int64, timeout time.Duration) *Resequencer {
	return &Resequencer{
		expression: expression,
		nextSeq:    nextSeq,
		timeout:    timeout,
		capacity:   DefaultResequencerCapacity,
		buffer:     make([]*resequencerEntry, 0),
	}
}

// SetCapacity bounds how many out-of-order exchanges the resequencer retains.
// 0 means unlimited (not recommended).
func (r *Resequencer) SetCapacity(n int) *Resequencer {
	r.mu.Lock()
	r.capacity = n
	r.mu.Unlock()
	return r
}

// SetProcessor sets the processor to be executed when a message is ready.
func (r *Resequencer) SetProcessor(p Processor) {
	r.mu.Lock()
	r.processor = p
	r.mu.Unlock()
}

// Validate implements the Validator interface for build-time validation.
func (r *Resequencer) Validate() error {
	if r == nil {
		return fmt.Errorf("resequencer: processor is nil")
	}
	if r.expression == nil {
		return fmt.Errorf("resequencer: expression must not be nil")
	}
	return nil
}

// childProcessors exposes the nested processor so the route can wire
// completion timeouts for aggregators nested inside this resequencer.
func (r *Resequencer) childProcessors() []Processor {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	proc := r.processor
	r.mu.Unlock()
	if proc != nil {
		return []Processor{proc}
	}
	return nil
}

// Process implements the Processor interface.
//
// The incoming exchange is copied into the buffer and delivered downstream
// (in order) via the internal pipeline; the ORIGINAL exchange never continues
// past this processor, so Process returns ErrStopRouting for consumed
// messages. Buffer mutations are serialized under mu; delivery runs without
// that lock so a downstream processor that routes back into this Resequencer
// can enqueue without deadlocking. A delivering flag ensures only one
// goroutine emits at a time, preserving order.
func (r *Resequencer) Process(exchange *Exchange) error {
	if err := r.Validate(); err != nil {
		return err
	}
	if exchange == nil {
		return fmt.Errorf("resequencer: exchange must not be nil")
	}
	seq, err := r.expression(exchange)
	if err != nil {
		return fmt.Errorf("resequencer: failed to evaluate expression: %w", err)
	}

	r.mu.Lock()
	if r.stopping {
		r.mu.Unlock()
		return ErrStopRouting
	}
	// An exchange only arrives while the route runs: a Process after Stop
	// means the route was restarted.
	r.stopped = false

	if seq < r.nextSeq {
		expected := r.nextSeq
		r.mu.Unlock()
		LogWarn("resequencer: received outdated sequence number", "seq", seq, "expected", expected)
		return ErrStopRouting
	}

	idx := sort.Search(len(r.buffer), func(i int) bool {
		return r.buffer[i].seq >= seq
	})
	if idx < len(r.buffer) && r.buffer[idx].seq == seq {
		r.mu.Unlock()
		LogWarn("resequencer: duplicate sequence number received", "seq", seq)
		return ErrStopRouting
	}

	// Allow seq == r.nextSeq even when buffer is at capacity because it
	// immediately triggers delivery and drains the head of the buffer.
	if r.capacity > 0 && len(r.buffer) >= r.capacity && seq != r.nextSeq {
		capLimit := r.capacity
		r.mu.Unlock()
		return fmt.Errorf("resequencer: buffer full (%d); dropping seq %d", capLimit, seq)
	}

	// A buffered exchange is delivered later, by another message or by the
	// timeout timer, when the context of the request that brought it (an HTTP
	// request, say) is typically done. Like the Aggregator and SEDA, delivery
	// runs under the route's lifecycle instead.
	buffered := exchange.DeepCopy()
	buffered.Context = r.deliveryContext(exchange.Context)
	r.buffer = append(r.buffer, nil)
	copy(r.buffer[idx+1:], r.buffer[idx:])
	r.buffer[idx] = &resequencerEntry{
		seq:      seq,
		exchange: buffered,
	}

	var ready []*resequencerEntry
	if !r.delivering {
		ready = r.takeReady()
		if len(ready) > 0 {
			r.delivering = true
			r.timeoutWG.Add(1)
		}
	}
	r.updateTimer()
	r.mu.Unlock()

	if len(ready) == 0 {
		return ErrStopRouting
	}
	return r.deliver(ready, &seq)
}

// start records the context of the route that runs this resequencer, clears
// stopped state, and re-arms the completion timer for any buffered entries.
// Called by Route.Start.
func (r *Resequencer) start(ctx context.Context) {
	r.mu.Lock()
	r.lifecycleCtx = ctx
	r.stopped = false
	var ready []*resequencerEntry
	if !r.delivering {
		ready = r.takeReady()
		if len(ready) > 0 {
			r.delivering = true
			r.timeoutWG.Add(1)
		}
	}
	r.updateTimer()
	r.mu.Unlock()
	if len(ready) > 0 {
		go func() {
			_ = r.deliver(ready, nil)
		}()
	}
}

// deliveryContext returns the context a buffered exchange is delivered with:
// the route's lifecycle context, or, for a Resequencer used outside a route,
// the exchange's own context detached from its cancellation. Caller holds mu.
func (r *Resequencer) deliveryContext(exchangeCtx context.Context) context.Context {
	if r.lifecycleCtx != nil {
		return r.lifecycleCtx
	}
	if exchangeCtx == nil {
		return context.Background()
	}
	return context.WithoutCancel(exchangeCtx)
}

func (r *Resequencer) takeReady() []*resequencerEntry {
	if r.stopped {
		return nil
	}
	n := 0
	for n < len(r.buffer) && r.buffer[n].seq == r.nextSeq {
		r.nextSeq++
		n++
	}
	if n == 0 {
		return nil
	}
	ready := make([]*resequencerEntry, n)
	for i := 0; i < n; i++ {
		entry := r.buffer[i]
		// Refresh context at delivery time so entries buffered before a
		// Stop/Start cycle deliver under the current live route context.
		entry.exchange.Context = r.deliveryContext(entry.exchange.Context)
		ready[i] = entry
		r.buffer[i] = nil
	}
	if n == len(r.buffer) {
		r.buffer = r.buffer[:0]
	} else {
		rem := len(r.buffer) - n
		copy(r.buffer, r.buffer[n:])
		for i := rem; i < len(r.buffer); i++ {
			r.buffer[i] = nil
		}
		r.buffer = r.buffer[:rem]
	}
	return ready
}

// deliver runs the ready exchanges downstream, in order, until none is left.
//
// own is the sequence number of the exchange whose Process call is doing the
// delivery (nil for a timeout). Only that exchange's failure is returned: the
// other exchanges of the batch belong to earlier callers that already returned,
// so reporting their failure here would fail (and redeliver) the wrong message.
// They are logged instead.
func (r *Resequencer) deliver(ready []*resequencerEntry, own *int64) error {
	done := false
	defer func() {
		if !done {
			r.timeoutWG.Done()
		}
	}()
	var firstErr error
	for {
		r.mu.Lock()
		proc := r.processor
		r.mu.Unlock()
		for _, entry := range ready {
			if proc == nil {
				_ = CompleteExchange(entry.exchange, nil)
				continue
			}
			err := safeProcess(func() error { return proc.Process(entry.exchange) })
			if err := CompleteExchange(entry.exchange, err); err != nil {
				if own != nil && entry.seq == *own {
					firstErr = err
				} else {
					LogError("resequencer: downstream processing of a buffered exchange failed",
						"seq", entry.seq, "exchange_id", entry.exchange.ID, "error", err)
				}
			}
		}
		r.mu.Lock()
		ready = r.takeReady()
		if len(ready) == 0 {
			r.delivering = false
			r.updateTimer()
			done = true
			r.timeoutWG.Done()
			r.mu.Unlock()
			break
		}
		r.mu.Unlock()
	}
	if firstErr != nil {
		return firstErr
	}
	return ErrStopRouting
}

func (r *Resequencer) updateTimer() {
	if r.timer != nil {
		r.timer.Stop()
		r.timer = nil
	}

	if !r.stopped && len(r.buffer) > 0 && r.buffer[0].seq > r.nextSeq {
		r.timerGen++
		gen := r.timerGen
		r.timer = time.AfterFunc(r.timeout, func() { r.onTimeout(gen) })
	}
}

func (r *Resequencer) onTimeout(gen uint64) {
	r.mu.Lock()
	if gen != r.timerGen || r.stopped {
		r.mu.Unlock()
		return
	}
	if len(r.buffer) == 0 || r.buffer[0].seq <= r.nextSeq {
		r.mu.Unlock()
		return
	}
	LogWarn("resequencer: timeout reached", "expected", r.nextSeq, "skipping to", r.buffer[0].seq)
	r.nextSeq = r.buffer[0].seq
	if r.delivering {
		// The in-flight deliverer will pick the skipped group up on its next takeReady.
		r.updateTimer()
		r.mu.Unlock()
		return
	}
	ready := r.takeReady()
	if len(ready) > 0 {
		r.delivering = true
		r.timeoutWG.Add(1)
	}
	r.updateTimer()
	r.mu.Unlock()

	if len(ready) == 0 {
		return
	}
	_ = r.deliver(ready, nil) // failures are logged per exchange
}

// Stop cancels the pending completion timer so no callback fires after the
// route has stopped (and its downstream producers are gone). The timer
// generation is advanced so a callback that has not started yet observes a
// mismatch and returns without delivering, and Stop waits for any delivery that
// is already in flight downstream. Buffered exchanges that have not yet
// reached their sequence are intentionally not flushed.
func (r *Resequencer) Stop() error {
	r.mu.Lock()
	if r.stopping {
		r.mu.Unlock()
		r.timeoutWG.Wait()
		return nil
	}
	r.stopped = true
	r.stopping = true
	r.timerGen++
	if r.timer != nil {
		r.timer.Stop()
		r.timer = nil
	}
	r.mu.Unlock()
	r.timeoutWG.Wait()
	r.mu.Lock()
	r.stopping = false
	r.mu.Unlock()
	return nil
}

// ResequencerDefinition provides a fluent API for configuring a Resequencer.
type ResequencerDefinition struct {
	parent      *RouteBuilder
	resequencer *Resequencer
	mainBuilder *RouteBuilder
}

// Timeout sets the timeout duration.
func (d *ResequencerDefinition) Timeout(timeout time.Duration) *ResequencerDefinition {
	d.resequencer.timeout = timeout
	return d
}

// Capacity sets the maximum number of out-of-order exchanges retained.
func (d *ResequencerDefinition) Capacity(n int) *ResequencerDefinition {
	d.resequencer.SetCapacity(n)
	return d
}

func (d *ResequencerDefinition) End() *RouteBuilder {
	d.parent.adoptErr(d.mainBuilder)
	return d.parent
}

// Resequence starts a Resequencer EIP block.
func (b *RouteBuilder) Resequence(expression any, startSeq int64) *ResequencerDefinition {
	var exprFn func(*Exchange) (int64, error)

	switch expr := expression.(type) {
	case func(*Exchange) (int64, error):
		exprFn = expr
	case string:
		template, err := ParseSimpleTemplate(expr)
		if err != nil {
			b.recordErr(fmt.Errorf("resequencer: failed to parse simple expression: %w", err))
			return &ResequencerDefinition{parent: b, resequencer: NewResequencer(nil, startSeq, time.Second), mainBuilder: &RouteBuilder{context: b.context, route: b.route, container: NewPipeline()}}
		}
		exprFn = func(e *Exchange) (int64, error) {
			val, ok := template.EvaluateAsInt(e)
			if !ok {
				// Fallback to evaluating as string and parsing
				s, err := template.EvaluateAsString(e)
				if err != nil {
					return 0, err
				}
				i, err := strconv.ParseInt(s, 10, 64)
				if err != nil {
					return 0, fmt.Errorf("resequencer: failed to parse sequence as int: %s", s)
				}
				return i, nil
			}
			return int64(val), nil
		}
	default:
		b.recordErr(fmt.Errorf("resequencer: unsupported expression type %T", expression))
		return &ResequencerDefinition{parent: b, resequencer: NewResequencer(nil, startSeq, time.Second), mainBuilder: &RouteBuilder{context: b.context, route: b.route, container: NewPipeline()}}
	}

	r := NewResequencer(exprFn, startSeq, 1*time.Second)
	p := NewPipeline()
	r.SetProcessor(p)

	b.container.AddProcessor(r)

	return &ResequencerDefinition{
		parent:      b,
		resequencer: r,
		mainBuilder: &RouteBuilder{
			context:   b.context,
			route:     b.route,
			container: p,
		},
	}
}

// Process adds a processor to the resequencer and stays in the resequencer context.
func (d *ResequencerDefinition) Process(processor Processor) *ResequencerDefinition {
	d.mainBuilder.Process(processor)
	return d
}

// ProcessFunc adds a processing function to the resequencer and stays in the resequencer context.
func (d *ResequencerDefinition) ProcessFunc(f func(*Exchange) error) *ResequencerDefinition {
	d.mainBuilder.ProcessFunc(f)
	return d
}

// To adds one or more destination endpoints to the resequencer and stays in the resequencer context.
func (d *ResequencerDefinition) To(uris ...string) *ResequencerDefinition {
	d.mainBuilder.To(uris...)
	return d
}
