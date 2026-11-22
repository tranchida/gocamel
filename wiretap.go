package gocamel

import (
	"context"
	"fmt"
	"sync"
)

// WireTap is a Processor that implements the Wire Tap EIP.
// It sends a deep copy of the exchange asynchronously to a specified
// endpoint without blocking the main route.
//
// Each tap exchange runs under a cancellable child of the route lifecycle
// context (so in-flight taps outlive short-lived request contexts, yet are
// cancelled cleanly when the route stops). Concurrent taps are bounded by
// DefaultMaxConcurrency (or SetMaxConcurrency). Errors during the tap are
// logged but not propagated to the main route.
//
// The producer for the tap endpoint is fetched from the CamelContext
// producer cache (created once, reused for every tap, stopped with the
// context). Route.Stop() calls Stop(), which cancels in-flight taps and waits
// for them to finish so none are lost or leaked during shutdown.
//
// Example DSL usage:
//
//	route, err := ctx.CreateRouteBuilder().
//	    From("direct:start").
//	    ProcessFunc(myProcessor).
//	    WireTap("direct:audit").
//	    To("direct:result").
//	    Build()
type WireTap struct {
	context        *CamelContext
	uri            string
	maxConcurrency int
	mu             sync.RWMutex
	stopped        bool
	ctx            context.Context
	cancel         context.CancelFunc
	sem            chan struct{}
	wg             sync.WaitGroup
}

// NewWireTap creates a new WireTap processor that will tap a copy
// of each exchange to the given URI endpoint.
func NewWireTap(camelContext *CamelContext, uri string) *WireTap {
	return &WireTap{
		context:        camelContext,
		uri:            uri,
		maxConcurrency: DefaultMaxConcurrency,
	}
}

// SetMaxConcurrency bounds the number of concurrent tap deliveries
// (n <= 0 restores DefaultMaxConcurrency).
func (w *WireTap) SetMaxConcurrency(n int) *WireTap {
	w.mu.Lock()
	w.maxConcurrency = effectiveMaxConcurrency(n)
	w.sem = nil
	w.mu.Unlock()
	return w
}

func (w *WireTap) baseContext() context.Context {
	if w.context != nil {
		if ctx := w.context.GetContext(); ctx != nil {
			return ctx
		}
	}
	return context.Background()
}

func (w *WireTap) ensureContextLocked() context.Context {
	if w.ctx == nil {
		w.ctx, w.cancel = context.WithCancel(w.baseContext())
	}
	return w.ctx
}

func (w *WireTap) ensureSemLocked() chan struct{} {
	if w.sem == nil {
		w.sem = make(chan struct{}, effectiveMaxConcurrency(w.maxConcurrency))
	}
	return w.sem
}

// Process implements the Processor interface.
// It creates a deep copy of the exchange, spawns a bounded goroutine to send
// the copy to the tap endpoint, and returns immediately so the main
// route continues without waiting.
func (w *WireTap) Process(exchange *Exchange) error {
	if w == nil || exchange == nil {
		return nil
	}

	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return nil
	}
	tapCtx := w.ensureContextLocked()
	sem := w.ensureSemLocked()
	w.mu.Unlock()

	exCtx := exchange.Context
	if exCtx == nil {
		exCtx = context.Background()
	}

	select {
	case sem <- struct{}{}:
	case <-tapCtx.Done():
		return nil
	case <-exCtx.Done():
		return nil
	}

	handOff := false
	defer func() {
		if !handOff {
			<-sem
		}
	}()

	// Deep copy before wg.Add(1) so the tap is isolated from the main route and
	// a panic in DeepCopy cannot leave wg stranded.
	tapExchange := exchange.DeepCopy()
	tapExchange.Context = tapCtx

	w.mu.RLock()
	if w.stopped || w.ctx != tapCtx {
		w.mu.RUnlock()
		return nil
	}
	w.wg.Add(1)
	w.mu.RUnlock()

	handOff = true
	go func() {
		defer w.wg.Done()
		defer func() { <-sem }()
		err := safeProcess(func() error { return w.sendTap(tapExchange) })
		if err := CompleteExchange(tapExchange, err); err != nil {
			LogWarn("WireTap: tap delivery failed",
				"uri", RedactURI(w.uri),
				"error", err,
			)
			return
		}

		LogDebug("WireTap: tap delivered successfully",
			"uri", RedactURI(w.uri),
		)
	}()

	return nil
}

// Start resets the stopped state when a wiretap is started directly.
func (w *WireTap) Start() {
	w.start(w.baseContext())
}

// start attaches the route lifecycle context and resets the stopped state.
func (w *WireTap) start(ctx context.Context) {
	if ctx == nil {
		ctx = w.baseContext()
	}
	w.mu.Lock()
	if w.cancel != nil {
		w.cancel()
	}
	w.stopped = false
	w.ctx, w.cancel = context.WithCancel(ctx)
	w.sem = make(chan struct{}, effectiveMaxConcurrency(w.maxConcurrency))
	w.mu.Unlock()
}

// Stop rejects new taps, cancels in-flight tap contexts, and waits for all
// in-flight taps to complete. Called by Route.Stop().
func (w *WireTap) Stop() error {
	w.mu.Lock()
	w.stopped = true
	cancel := w.cancel
	w.cancel = nil
	w.ctx = nil
	w.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	w.wg.Wait()
	return nil
}

// sendTap sends the exchange to the tap endpoint via the shared producer cache.
func (w *WireTap) sendTap(exchange *Exchange) error {
	if w.context == nil {
		return fmt.Errorf("wiretap: CamelContext must not be nil")
	}
	propagateOutToInAndReset(exchange)
	return w.context.sendToURI(w.uri, exchange)
}
