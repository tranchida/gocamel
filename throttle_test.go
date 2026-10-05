package gocamel

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestThrottler_RateLimit(t *testing.T) {
	// Allow 3 messages per 200ms window.
	throttler := NewThrottler(3, 200*time.Millisecond)

	var count atomic.Int64
	start := time.Now()

	// Send 6 messages concurrently.
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ex := NewExchange(context.Background())
			_ = throttler.Process(ex)
			count.Add(1)
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	assert.Equal(t, int64(6), count.Load())
	// 6 messages at 3/200ms means at least 2 windows → ≥200ms total.
	assert.GreaterOrEqual(t, elapsed, 150*time.Millisecond,
		"throttler should have delayed the second window of messages")
}

func TestThrottler_WithinLimit_NoDelay(t *testing.T) {
	throttler := NewThrottler(10, time.Second)

	start := time.Now()
	for i := 0; i < 5; i++ {
		ex := NewExchange(context.Background())
		require.NoError(t, throttler.Process(ex))
	}
	// 5 messages within a 10-msg/s limit — should complete very quickly.
	assert.Less(t, time.Since(start), 50*time.Millisecond)
}

func TestThrottler_InvalidMaxRequests(t *testing.T) {
	throttler := NewThrottler(0, time.Second)
	ex := NewExchange(context.Background())

	err := throttler.Process(ex)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "maxRequests")
}

func TestThrottler_InvalidTimePeriod(t *testing.T) {
	throttler := NewThrottler(1, 0)
	ex := NewExchange(context.Background())

	err := throttler.Process(ex)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "timePeriod")
}

func TestThrottler_ContextCancelledWhileWaiting(t *testing.T) {
	throttler := NewThrottler(1, time.Second)
	require.NoError(t, throttler.Process(NewExchange(context.Background())))

	ctx, cancel := context.WithCancel(context.Background())
	ex := NewExchange(ctx)

	go func() {
		// time.Sleep is intentional: cancel while the throttler is waiting.
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := throttler.Process(ex)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "context cancelled")
	assert.Less(t, time.Since(start), 500*time.Millisecond)
}

func TestRouteBuilder_Throttle(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())

	var count atomic.Int64
	_, err := ctx.CreateRouteBuilder().
		From("direct:throttled").
		Throttle(2, 100*time.Millisecond).
		ProcessFunc(func(e *Exchange) error {
			count.Add(1)
			return nil
		}).
		Build()
	require.NoError(t, err)

	require.NoError(t, ctx.Start())
	defer ctx.Stop()

	send := func() {
		ex := NewExchange(context.Background())
		p, _ := ctx.CreateEndpoint("direct:throttled")
		prod, _ := p.CreateProducer()
		prod.Start(context.Background())
		prod.Send(ex)
		prod.Stop()
	}

	// 4 sequential sends — expect at least 2 windows.
	start := time.Now()
	for i := 0; i < 4; i++ {
		send()
	}

	assert.Equal(t, int64(4), count.Load())
	assert.GreaterOrEqual(t, time.Since(start), 80*time.Millisecond)
}
