package gocamel

import (
	"fmt"
	"sync"
	"time"
)

// Throttler implements the Throttle EIP.
// It limits the rate of exchange processing to at most MaxRequests per TimePeriod.
// Exchanges that exceed the rate are delayed (not dropped).
//
// The implementation is a token bucket of capacity MaxRequests that refills at
// MaxRequests/TimePeriod. Unlike a fixed window, it does not allow a 2× burst
// at a window boundary.
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

	mu     sync.Mutex
	tokens float64
	last   time.Time
}

// NewThrottler creates a Throttler that allows at most maxRequests per timePeriod.
func NewThrottler(maxRequests int64, timePeriod time.Duration) *Throttler {
	return &Throttler{
		maxRequests: maxRequests,
		timePeriod:  timePeriod,
		tokens:      float64(maxRequests),
		last:        time.Now(),
	}
}

// Process implements the Processor interface.
// If no token is available, the call blocks until one refills.
func (t *Throttler) Process(exchange *Exchange) error {
	if t.maxRequests <= 0 {
		return fmt.Errorf("throttle: maxRequests must be greater than 0")
	}
	if t.timePeriod <= 0 {
		return fmt.Errorf("throttle: timePeriod must be greater than 0")
	}

	rate := float64(t.maxRequests) / t.timePeriod.Seconds()
	capacity := float64(t.maxRequests)

	for {
		t.mu.Lock()
		now := time.Now()
		elapsed := now.Sub(t.last).Seconds()
		t.tokens += elapsed * rate
		if t.tokens > capacity {
			t.tokens = capacity
		}
		t.last = now

		if t.tokens >= 1 {
			t.tokens--
			t.mu.Unlock()
			return nil
		}

		need := 1 - t.tokens
		wait := time.Duration(need / rate * float64(time.Second))
		t.mu.Unlock()

		if wait < time.Millisecond {
			wait = time.Millisecond
		}
		if err := sleepWithContext(exchange, wait); err != nil {
			return fmt.Errorf("throttle: %w", err)
		}
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
