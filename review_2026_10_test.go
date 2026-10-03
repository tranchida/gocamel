package gocamel

import (
	"context"
	"errors"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regression tests for the 2026-10 code review.

// --- direct: does not leak a sub-route's stop to its caller -----------------

func startDirectCaller(t *testing.T, ctx *CamelContext, reached *atomic.Bool) Producer {
	t.Helper()
	_, err := ctx.CreateRouteBuilder().
		From("direct:caller").
		To("direct:callee").
		ProcessFunc(func(*Exchange) error { reached.Store(true); return nil }).
		Build()
	require.NoError(t, err)
	require.NoError(t, ctx.Start())
	t.Cleanup(func() { _ = ctx.Stop() })
	p, err := ctx.GetProducer("direct:caller")
	require.NoError(t, err)
	return p
}

func TestDirect_FilterInCalledRouteDoesNotStopCaller(t *testing.T) {
	ctx := NewCamelContext()
	_, err := ctx.CreateRouteBuilder().
		From("direct:callee").
		Filter(func(*Exchange) (bool, error) { return false, nil }).
		Build()
	require.NoError(t, err)
	var reached atomic.Bool
	p := startDirectCaller(t, ctx, &reached)

	require.NoError(t, p.Send(NewExchange(context.Background())))
	assert.True(t, reached.Load(), "caller continues after the called route filtered the exchange")
}

type latestStrategy struct{}

func (latestStrategy) Aggregate(_, current *Exchange) *Exchange { return current }

func TestDirect_IncompleteAggregatorInCalledRouteDoesNotStopCaller(t *testing.T) {
	ctx := NewCamelContext()
	agg := NewAggregator(func(*Exchange) string { return "k" }, latestStrategy{}, NewMemoryAggregationRepository()).SetCompletionSize(2)
	_, err := ctx.CreateRouteBuilder().From("direct:callee").Aggregate(agg).Build()
	require.NoError(t, err)
	var reached atomic.Bool
	p := startDirectCaller(t, ctx, &reached)

	require.NoError(t, p.Send(NewExchange(context.Background())))
	assert.True(t, reached.Load())
}

func TestDirect_StopEIPInCalledRouteStopsCaller(t *testing.T) {
	ctx := NewCamelContext()
	_, err := ctx.CreateRouteBuilder().From("direct:callee").Stop().Build()
	require.NoError(t, err)
	var reached atomic.Bool
	p := startDirectCaller(t, ctx, &reached)

	err = p.Send(NewExchange(context.Background()))
	assert.ErrorIs(t, err, ErrStopRouting, "Stop() ends the whole exchange, as in Apache Camel")
	assert.False(t, reached.Load())
}

// --- DeadLetterChannel marks dead-lettered exchanges ------------------------

func TestDeadLetterChannel_MarksExchangeDeadLettered(t *testing.T) {
	ctx := NewCamelContext()
	_, err := ctx.CreateRouteBuilder().From("direct:dlq").ProcessFunc(func(*Exchange) error { return nil }).Build()
	require.NoError(t, err)
	require.NoError(t, ctx.Start())
	t.Cleanup(func() { _ = ctx.Stop() })

	ex := NewExchange(context.Background())
	assert.False(t, IsDeadLettered(ex))
	dlc := NewDeadLetterChannel(ctx, "direct:dlq")
	err = dlc.HandleError(ex, errors.New("boom"))
	require.Error(t, err, "the original error is still returned by default")
	assert.True(t, IsDeadLettered(ex))
}

func TestDeadLetterChannel_FailedDeadLetterSendIsNotMarked(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(context.Background())
	_ = NewDeadLetterChannel(ctx, "direct:missing").HandleError(ex, errors.New("boom"))
	assert.False(t, IsDeadLettered(ex))
}

// --- IsCamelHeader -----------------------------------------------------------

func TestIsCamelHeader(t *testing.T) {
	for _, k := range []string{"CamelFileName", "camelfilename", "CAMELX", "Camel"} {
		assert.True(t, IsCamelHeader(k), k)
	}
	for _, k := range []string{"", "Came", "X-Camel", "Content-Type"} {
		assert.False(t, IsCamelHeader(k), k)
	}
}

// --- ConsumerRedeliveryPolicy --------------------------------------------------

func TestConsumerRedeliveryPolicy_Defaults(t *testing.T) {
	p := NewConsumerRedeliveryPolicy()
	assert.False(t, p.Exhausted(9))
	assert.True(t, p.Exhausted(10), "a poison message is given up on after 10 redeliveries by default")
	assert.False(t, ConsumerRedeliveryPolicy{MaxRedeliveries: -1}.Exhausted(1_000_000), "-1 retries forever")
	assert.Equal(t, 100*time.Millisecond, p.Backoff(0))
	assert.Equal(t, 200*time.Millisecond, p.Backoff(1))
	assert.Equal(t, 30*time.Second, p.Backoff(20), "backoff is capped")
}

func TestConsumerRedeliveryPolicy_ParseURI(t *testing.T) {
	u, err := url.Parse("kafka:t?maxRedeliveries=3&redeliveryDelay=10&maxRedeliveryDelay=50")
	require.NoError(t, err)
	p := ParseConsumerRedeliveryPolicy(u)
	assert.False(t, p.Exhausted(2))
	assert.True(t, p.Exhausted(3))
	assert.Equal(t, 10*time.Millisecond, p.Backoff(0))
	assert.Equal(t, 50*time.Millisecond, p.Backoff(5))

	u, err = url.Parse("kafka:t?maxRedeliveries=0&redeliveryDelay=oops")
	require.NoError(t, err)
	p = ParseConsumerRedeliveryPolicy(u)
	assert.True(t, p.Exhausted(0), "0 gives up on the first failure")
	assert.Equal(t, DefaultConsumerRedeliveryDelay, p.Delay, "invalid value falls back to the default")
}

func TestConsumerRedeliveryPolicy_WaitHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := ConsumerRedeliveryPolicy{Delay: time.Hour, MaxDelay: time.Hour}
	assert.False(t, p.Wait(ctx, 0))
}

// --- Route.Stop stops nested stoppable processors ----------------------------

func TestRouteStop_WaitsForWireTapNestedInContainer(t *testing.T) {
	ctx := NewCamelContext()
	tapStarted := make(chan struct{})
	release := make(chan struct{})
	var tapFinished atomic.Bool
	_, err := ctx.CreateRouteBuilder().From("direct:slowtap").ProcessFunc(func(*Exchange) error {
		close(tapStarted)
		<-release
		tapFinished.Store(true)
		return nil
	}).Build()
	require.NoError(t, err)

	route := ctx.CreateRoute().From("direct:nested-tap")
	m := NewMulticast()
	m.AddProcessor(NewWireTap(ctx, "direct:slowtap"))
	route.AddProcessor(m)
	require.NoError(t, ctx.Start())
	t.Cleanup(func() { _ = ctx.Stop() })

	p, err := ctx.GetProducer("direct:nested-tap")
	require.NoError(t, err)
	require.NoError(t, p.Send(NewExchange(context.Background())))
	<-tapStarted

	stopped := make(chan error, 1)
	go func() { stopped <- route.Stop() }()
	assert.Never(t, func() bool { return len(stopped) > 0 }, 100*time.Millisecond, 10*time.Millisecond,
		"Route.Stop must wait for the in-flight tap of a nested WireTap")
	close(release)
	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Route.Stop did not return")
	}
	assert.True(t, tapFinished.Load())
}

func TestRouteStop_CancelsTimerOfNestedResequencer(t *testing.T) {
	ctx := NewCamelContext()
	var delivered atomic.Int32
	r := NewResequencer(func(e *Exchange) (int64, error) {
		seq, _ := e.GetHeaderAsInt("seq")
		return int64(seq), nil
	}, 0, 30*time.Millisecond)
	r.SetProcessor(ProcessorFunc(func(*Exchange) error { delivered.Add(1); return nil }))
	m := NewMulticast()
	m.AddProcessor(r)
	route := ctx.CreateRoute().From("direct:nested-reseq")
	route.AddProcessor(m)
	require.NoError(t, ctx.Start())
	t.Cleanup(func() { _ = ctx.Stop() })

	p, err := ctx.GetProducer("direct:nested-reseq")
	require.NoError(t, err)
	ex := NewExchange(context.Background())
	ex.SetHeader("seq", 1) // gap: waits for seq 0 until the timeout
	require.NoError(t, p.Send(ex))
	require.NoError(t, route.Stop())

	assert.Never(t, func() bool { return delivered.Load() > 0 }, 150*time.Millisecond, 10*time.Millisecond,
		"the timer of a resequencer nested in a container must not fire after Route.Stop")
}

// --- Resequencer.Stop waits for an in-flight timeout delivery -----------------

func TestRouteStop_WaitsForResequencerTimeoutDelivery(t *testing.T) {
	ctx := NewCamelContext()
	_, err := ctx.CreateRouteBuilder().From("direct:reseq-sink").ProcessFunc(func(*Exchange) error { return nil }).Build()
	require.NoError(t, err)

	deliveryStarted := make(chan struct{})
	release := make(chan struct{})
	route, err := ctx.CreateRouteBuilder().
		From("direct:reseq-timeout").
		Resequence(func(e *Exchange) (int64, error) {
			seq, _ := e.GetHeaderAsInt("seq")
			return int64(seq), nil
		}, 0).
		Timeout(20 * time.Millisecond).
		ProcessFunc(func(*Exchange) error {
			close(deliveryStarted)
			<-release
			return nil
		}).
		To("direct:reseq-sink").
		End().
		Build()
	require.NoError(t, err)
	require.NoError(t, ctx.Start())
	t.Cleanup(func() { _ = ctx.Stop() })

	p, err := ctx.GetProducer("direct:reseq-timeout")
	require.NoError(t, err)
	ex := NewExchange(context.Background())
	ex.SetHeader("seq", 1)
	require.NoError(t, p.Send(ex))
	<-deliveryStarted // the timeout skipped seq 0 and is delivering seq 1

	stopped := make(chan error, 1)
	go func() { stopped <- route.Stop() }()
	assert.Never(t, func() bool { return len(stopped) > 0 }, 100*time.Millisecond, 10*time.Millisecond,
		"Route.Stop must wait for the timeout delivery before stopping producers")
	close(release)
	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Route.Stop did not return")
	}

	// Previously the delivery reached To() after its producer was stopped,
	// recreating a producer that nothing would ever stop.
	for _, lp := range route.lazyProducers {
		lp.mu.Lock()
		leaked := lp.p != nil
		lp.mu.Unlock()
		assert.False(t, leaked, "producer for %s recreated after Route.Stop", lp.uri)
	}
}
