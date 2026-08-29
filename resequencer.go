package gocamel

import (
	"errors"
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
	delivering bool   // one goroutine at a time delivers, without holding mu
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
	r.capacity = n
	return r
}

// SetProcessor sets the processor to be executed when a message is ready.
func (r *Resequencer) SetProcessor(p Processor) {
	r.processor = p
}

// childProcessors exposes the nested processor so the route can wire
// completion timeouts for aggregators nested inside this resequencer.
func (r *Resequencer) childProcessors() []Processor {
	if r.processor != nil {
		return []Processor{r.processor}
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
	seq, err := r.expression(exchange)
	if err != nil {
		return fmt.Errorf("resequencer: failed to evaluate expression: %w", err)
	}

	r.mu.Lock()

	if seq < r.nextSeq {
		r.mu.Unlock()
		LogWarn("resequencer: received outdated sequence number", "seq", seq, "expected", r.nextSeq)
		return ErrStopRouting
	}

	for _, e := range r.buffer {
		if e.seq == seq {
			r.mu.Unlock()
			LogWarn("resequencer: duplicate sequence number received", "seq", seq)
			return ErrStopRouting
		}
	}

	if r.capacity > 0 && len(r.buffer) >= r.capacity {
		r.mu.Unlock()
		return fmt.Errorf("resequencer: buffer full (%d); dropping seq %d", r.capacity, seq)
	}

	r.buffer = append(r.buffer, &resequencerEntry{
		seq:      seq,
		exchange: exchange.Copy(),
	})
	sort.Slice(r.buffer, func(i, j int) bool {
		return r.buffer[i].seq < r.buffer[j].seq
	})

	var ready []*resequencerEntry
	if !r.delivering {
		ready = r.takeReady()
		r.delivering = len(ready) > 0
	}
	r.updateTimer()
	r.mu.Unlock()

	if len(ready) == 0 {
		return ErrStopRouting
	}
	return r.deliver(ready)
}

func (r *Resequencer) takeReady() []*resequencerEntry {
	var ready []*resequencerEntry
	for len(r.buffer) > 0 && r.buffer[0].seq == r.nextSeq {
		ready = append(ready, r.buffer[0])
		r.buffer = r.buffer[1:]
		r.nextSeq++
	}
	return ready
}

func (r *Resequencer) deliver(ready []*resequencerEntry) error {
	var firstErr error
	for {
		for _, entry := range ready {
			if r.processor == nil {
				continue
			}
			if err := safeProcess(func() error { return r.processor.Process(entry.exchange) }); err != nil {
				if firstErr == nil {
					firstErr = err
				}
			}
		}
		r.mu.Lock()
		ready = r.takeReady()
		if len(ready) == 0 {
			r.delivering = false
			r.updateTimer()
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

	if len(r.buffer) > 0 && r.buffer[0].seq > r.nextSeq {
		r.timerGen++
		gen := r.timerGen
		r.timer = time.AfterFunc(r.timeout, func() { r.onTimeout(gen) })
	}
}

func (r *Resequencer) onTimeout(gen uint64) {
	r.mu.Lock()
	if gen != r.timerGen {
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
	r.delivering = len(ready) > 0
	r.updateTimer()
	r.mu.Unlock()

	if len(ready) == 0 {
		return
	}
	if err := r.deliver(ready); err != nil && !errors.Is(err, ErrStopRouting) {
		LogError("resequencer: downstream processing failed during timeout drain", "error", err)
	}
}

// Stop cancels the pending completion timer so no callback fires after the
// route has stopped (and its downstream producers are gone). The timer
// generation is advanced so an already-in-flight callback observes a mismatch
// and returns without delivering. Buffered exchanges that have not yet reached
// their sequence are intentionally not flushed.
func (r *Resequencer) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.timerGen++
	if r.timer != nil {
		r.timer.Stop()
		r.timer = nil
	}
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
			b.err = fmt.Errorf("resequencer: failed to parse simple expression: %w", err)
			return &ResequencerDefinition{parent: b}
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
		b.err = fmt.Errorf("resequencer: unsupported expression type %T", expression)
		return &ResequencerDefinition{parent: b}
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
