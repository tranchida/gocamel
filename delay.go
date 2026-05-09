package gocamel

import (
	"fmt"
	"time"
)

// Delayer implements the Delay EIP.
// It pauses processing of each exchange for a duration determined either by a
// fixed value or a dynamic expression evaluated per exchange.
//
// The delay is applied synchronously — the calling goroutine sleeps.
// Context cancellation interrupts the sleep and returns an error.
//
// Example DSL usage:
//
//	// Fixed delay
//	route, _ := ctx.CreateRouteBuilder().
//	    From("direct:start").
//	    Delay(500 * time.Millisecond).
//	    To("direct:next").
//	    Build()
//
//	// Dynamic delay from a header value
//	route, _ := ctx.CreateRouteBuilder().
//	    From("direct:start").
//	    DelayFunc(func(e *gocamel.Exchange) (time.Duration, error) {
//	        ms, ok := e.GetHeaderAsInt("X-Delay-Ms")
//	        if !ok {
//	            return 0, nil
//	        }
//	        return time.Duration(ms) * time.Millisecond, nil
//	    }).
//	    To("direct:next").
//	    Build()
type Delayer struct {
	delayFn func(*Exchange) (time.Duration, error)
}

// NewDelayer creates a Delayer with a fixed duration.
func NewDelayer(d time.Duration) *Delayer {
	return &Delayer{delayFn: func(_ *Exchange) (time.Duration, error) { return d, nil }}
}

// NewDelayerFunc creates a Delayer whose duration is computed per exchange.
func NewDelayerFunc(fn func(*Exchange) (time.Duration, error)) *Delayer {
	return &Delayer{delayFn: fn}
}

// Process implements the Processor interface.
// It sleeps for the computed duration, respecting context cancellation.
func (d *Delayer) Process(exchange *Exchange) error {
	duration, err := d.delayFn(exchange)
	if err != nil {
		return fmt.Errorf("delay: expression error: %w", err)
	}
	if duration <= 0 {
		return nil
	}

	ctx := exchange.Context
	if ctx == nil {
		time.Sleep(duration)
		return nil
	}

	select {
	case <-time.After(duration):
		return nil
	case <-ctx.Done():
		return fmt.Errorf("delay: context cancelled: %w", ctx.Err())
	}
}
