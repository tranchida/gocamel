package gocamel

import (
	"context"
)

// WireTap is a Processor that implements the Wire Tap EIP.
// It sends a deep copy of the exchange asynchronously to a specified
// endpoint without blocking the main route.
//
// The tap exchange uses an independent context (context.Background())
// so that it is not cancelled when the main route completes.
// Errors during the tap are logged but not propagated to the main route.
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
	// Deep copy to isolate the tap from the main route
	tapExchange := exchange.Copy()
	// Use an independent context so the tap is not tied to the main route lifecycle
	tapExchange.Context = context.Background()

	go func() {
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

// sendTap creates a producer for the tap endpoint and sends the exchange.
func (w *WireTap) sendTap(exchange *Exchange) error {
	endpoint, err := w.context.CreateEndpoint(w.uri)
	if err != nil {
		return err
	}

	producer, err := endpoint.CreateProducer()
	if err != nil {
		return err
	}

	if err := producer.Start(exchange.Context); err != nil {
		return err
	}
	defer producer.Stop()

	exchange.PropagateOutToIn()
	return producer.Send(exchange)
}
