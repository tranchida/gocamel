package gocamel

import (
	"context"
	"sync"
)

// WireTap is a Processor that implements the Wire Tap EIP.
// It sends a deep copy of the exchange asynchronously to a specified
// endpoint without blocking the main route.
//
// The tap exchange uses an independent context (context.Background())
// so that it is not cancelled when the main route completes.
// Errors during the tap are logged but not propagated to the main route.
//
// The producer for the tap endpoint is fetched from the CamelContext
// producer cache (created once, reused for every tap, stopped with the
// context). Route.Stop() calls Stop(), which waits for in-flight taps to
// finish so none are lost during shutdown.
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
	context *CamelContext
	uri     string
	wg      sync.WaitGroup
}

// NewWireTap creates a new WireTap processor that will tap a copy
// of each exchange to the given URI endpoint.
func NewWireTap(camelContext *CamelContext, uri string) *WireTap {
	return &WireTap{
		context: camelContext,
		uri:     uri,
	}
}

// Process implements the Processor interface.
// It creates a deep copy of the exchange, spawns a goroutine to send
// the copy to the tap endpoint, and returns immediately so the main
// route continues without waiting.
func (w *WireTap) Process(exchange *Exchange) error {
	// Deep copy to isolate the tap from the main route: the tap goroutine runs
	// concurrently with the rest of the route, so shared mutable bodies would race.
	tapExchange := exchange.DeepCopy()
	// Use an independent context so the tap is not tied to the main route lifecycle
	tapExchange.Context = context.Background()

	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		if err := w.sendTap(tapExchange); err != nil {
			logWarn("WireTap: tap delivery failed",
				"uri", w.uri,
				"error", err,
			)
			return
		}

		logDebug("WireTap: tap delivered successfully",
			"uri", w.uri,
		)
	}()

	return nil
}

// Stop waits for all in-flight taps to complete. Called by Route.Stop().
func (w *WireTap) Stop() error {
	w.wg.Wait()
	return nil
}

// sendTap sends the exchange to the tap endpoint via the shared producer cache.
func (w *WireTap) sendTap(exchange *Exchange) error {
	producer, err := w.context.GetProducer(w.uri)
	if err != nil {
		return err
	}

	exchange.PropagateOutToIn()
	return producer.Send(exchange)
}
