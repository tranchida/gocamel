package gocamel

import (
	"fmt"
	"sync"
	"time"
)

// Throttler implements the Throttle EIP.
// It limits the rate of exchange processing to at most MaxRequests per TimePeriod.
// Exchanges that exceed the rate are delayed (not dropped) until the current window resets.
//
// The implementation uses a fixed-window counter: the window resets every TimePeriod.
// For smooth rate limiting across windows, use a smaller TimePeriod (e.g. 100ms with
// MaxRequests=10 instead of 1000ms with MaxRequests=100).
//
// Example DSL usage:
//
//	// Max 10 messages per second
//	route, _ := ctx.CreateRouteBuilder().
//	    From("direct:start").
//	    Throttle(10, time.Second).
//	    To("direct:downstream").
//	    Build()
type Throttler struct {
	maxRequests int64
	timePeriod  time.Duration

	mu          sync.Mutex
	count       int64
	windowStart time.Time
}

// NewThrottler creates a Throttler that allows at most maxRequests per timePeriod.
func NewThrottler(maxRequests int64, timePeriod time.Duration) *Throttler {
	return &Throttler{
		maxRequests: maxRequests,
		timePeriod:  timePeriod,
		windowStart: time.Now(),
	}
}

// Process implements the Processor interface.
// If the rate limit is reached, the call blocks until the current window expires.
func (t *Throttler) Process(exchange *Exchange) error {
	if t.maxRequests <= 0 {
		return fmt.Errorf("throttle: maxRequests must be greater than 0")
	}
	if t.timePeriod <= 0 {
		return fmt.Errorf("throttle: timePeriod must be greater than 0")
	}

	for {
		t.mu.Lock()
		now := time.Now()

		// Reset window if expired.
		if now.Sub(t.windowStart) >= t.timePeriod {
			t.windowStart = now
			t.count = 0
		}

		if t.count < t.maxRequests {
			t.count++
			t.mu.Unlock()
			return nil
		}

		// Window is full — compute how long until it resets.
		waitUntil := t.windowStart.Add(t.timePeriod)
		t.mu.Unlock()

		sleepFor := time.Until(waitUntil)
		if sleepFor > 0 {
			if err := sleepWithContext(exchange, sleepFor); err != nil {
				return fmt.Errorf("throttle: %w", err)
			}
		}
		// Loop to recheck (another goroutine may have consumed the reset window).
	}
}

// sleepWithContext sleeps for d, aborting early if the exchange context is
// cancelled. Shared by the Throttle and DeadLetterChannel redelivery delays.
func sleepWithContext(exchange *Exchange, d time.Duration) error {
	ctx := exchange.Context
	if ctx == nil {
		time.Sleep(d)
		return nil
	}

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("context cancelled: %w", ctx.Err())
	}
}
