package gocamel

import (
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"
)

// Resequencer implements the Resequencer EIP (Stream mode).
// It reorders messages based on a sequence number or timestamp extracted via an expression.
type Resequencer struct {
	expression func(*Exchange) (int64, error)
	timeout    time.Duration
	nextSeq    int64
	buffer     []*resequencerEntry
	mu         sync.Mutex
	processor  Processor
	timer      *time.Timer
	timerGen   uint64 // incremented each time a new timer is created; guards against stale callbacks
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
		buffer:     make([]*resequencerEntry, 0),
	}
}

// SetProcessor sets the processor to be executed when a message is ready.
func (r *Resequencer) SetProcessor(p Processor) {
	r.processor = p
}

// Process implements the Processor interface.
//
// The incoming exchange is copied into the buffer and delivered downstream
// (in order) via the internal pipeline; the ORIGINAL exchange never continues
// past this processor, so Process returns ErrStopRouting for consumed
// messages. Delivery happens under the internal mutex: ordering requires
// serialized delivery, so downstream processors must not route back into the
// same Resequencer (that would deadlock).
func (r *Resequencer) Process(exchange *Exchange) error {
	seq, err := r.expression(exchange)
	if err != nil {
		return fmt.Errorf("resequencer: failed to evaluate expression: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if seq < r.nextSeq {
		LogWarn("resequencer: received outdated sequence number", "seq", seq, "expected", r.nextSeq)
		return ErrStopRouting
	}

	// Check for duplicates
	for _, e := range r.buffer {
		if e.seq == seq {
			LogWarn("resequencer: duplicate sequence number received", "seq", seq)
			return ErrStopRouting
		}
	}

	entry := &resequencerEntry{
		seq:      seq,
		exchange: exchange.Copy(),
	}

	r.buffer = append(r.buffer, entry)
	sort.Slice(r.buffer, func(i, j int) bool {
		return r.buffer[i].seq < r.buffer[j].seq
	})

	err = r.drain()
	r.updateTimer()
	if err != nil {
		return err
	}
	return ErrStopRouting
}

func (r *Resequencer) drain() error {
	for len(r.buffer) > 0 && r.buffer[0].seq == r.nextSeq {
		entry := r.buffer[0]
		r.buffer = r.buffer[1:]
		r.nextSeq++

		if err := r.processor.Process(entry.exchange); err != nil {
			return err
		}
	}
	return nil
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
	defer r.mu.Unlock()

	// A newer timer was created since this callback was scheduled; ignore.
	if gen != r.timerGen {
		return
	}

	if len(r.buffer) > 0 && r.buffer[0].seq > r.nextSeq {
		LogWarn("resequencer: timeout reached", "expected", r.nextSeq, "skipping to", r.buffer[0].seq)
		r.nextSeq = r.buffer[0].seq
		if err := r.drain(); err != nil {
			// There is no caller to propagate to on the timeout path; surface
			// the failure in the logs instead of dropping it silently.
			LogError("resequencer: downstream processing failed during timeout drain", "error", err)
		}
		r.updateTimer()
	}
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

// End terminates the Resequencer block and returns to the parent builder.
func (d *ResequencerDefinition) End() *RouteBuilder {
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
