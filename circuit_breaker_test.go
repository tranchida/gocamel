package gocamel

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCircuitBreaker_Basic(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())

	var mainCalled, fallbackCalled int
	shouldFail := false

	_, err := ctx.CreateRouteBuilder().
		From("direct:start").
		CircuitBreaker().
		FailureThreshold(2).
		OpenTimeout(100 * time.Millisecond).
		ProcessFunc(func(e *Exchange) error {
			mainCalled++
			if shouldFail {
				return errors.New("boom")
			}
			return nil
		}).
		OnFallback().
		ProcessFunc(func(e *Exchange) error {
			fallbackCalled++
			return nil
		}).
		End().
		End().
		Build()
	require.NoError(t, err)

	require.NoError(t, ctx.Start())
	defer ctx.Stop()

	send := func() {
		ex := NewExchange(context.Background())
		p, _ := ctx.CreateEndpoint("direct:start")
		prod, _ := p.CreateProducer()
		prod.Start(context.Background())
		prod.Send(ex)
		prod.Stop()
	}

	// 1. Success
	send()
	assert.Equal(t, 1, mainCalled)
	assert.Equal(t, 0, fallbackCalled)

	// 2. Fail twice to open circuit
	shouldFail = true
	send() // failure 1
	send() // failure 2 -> opens
	assert.Equal(t, 3, mainCalled)
	assert.Equal(t, 2, fallbackCalled)

	// 3. Circuit is open, should call fallback directly
	send()
	assert.Equal(t, 3, mainCalled) // No increase
	assert.Equal(t, 3, fallbackCalled)

	// 4. Wait for timeout to move to Half-Open
	// time.Sleep is intentional: Half-Open is evaluated lazily on the next call,
	// so the open timeout must elapse before sending (no observable state to poll).
	time.Sleep(150 * time.Millisecond)
	shouldFail = false
	send() // success -> closes
	assert.Equal(t, 4, mainCalled)
	assert.Equal(t, 3, fallbackCalled)

	// 5. Back to closed
	send()
	assert.Equal(t, 5, mainCalled)
	assert.Equal(t, 3, fallbackCalled)
}

func TestCircuitBreaker_HalfOpenFailure(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())

	var mainCalled int
	shouldFail := true

	cb := NewCircuitBreaker()
	cb.SetFailureThreshold(1)
	cb.SetOpenTimeout(50 * time.Millisecond)

	pipeline := NewPipeline()
	pipeline.AddProcessor(ProcessorFunc(func(e *Exchange) error {
		mainCalled++
		if shouldFail {
			return errors.New("boom")
		}
		return nil
	}))
	cb.SetProcessor(pipeline)

	require.NoError(t, ctx.Start())
	defer ctx.Stop()

	// 1. Fail to open
	ex := NewExchange(context.Background())
	err := cb.Process(ex)
	assert.Error(t, err)
	assert.Equal(t, 1, mainCalled)

	// 2. Wait for Half-Open
	// time.Sleep is intentional: the open timeout must elapse before the next call.
	time.Sleep(60 * time.Millisecond)

	// 3. Fail in Half-Open -> should open again immediately
	err = cb.Process(ex)
	assert.Error(t, err)
	assert.Equal(t, 2, mainCalled)

	// 4. Circuit should be open again
	err = cb.Process(ex)
	assert.ErrorIs(t, err, ErrCircuitOpen)
	assert.Equal(t, 2, mainCalled)
}

func TestCircuitBreaker_FallbackPanicRecovered(t *testing.T) {
	cb := NewCircuitBreaker()
	cb.SetFailureThreshold(1)
	cb.SetProcessor(ProcessorFunc(func(*Exchange) error { return errors.New("fail") }))
	cb.SetFallbackProcessor(ProcessorFunc(func(*Exchange) error { panic("fb") }))

	err := cb.Process(NewExchange(context.Background()))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrProcessorPanic)
}

func TestCircuitBreaker_IdleFailuresDoNotOpen(t *testing.T) {
	cb := NewCircuitBreaker()
	cb.SetFailureThreshold(2)
	cb.SetOpenTimeout(40 * time.Millisecond)
	cb.SetProcessor(ProcessorFunc(func(*Exchange) error { return errors.New("x") }))

	require.Error(t, cb.Process(NewExchange(context.Background())))
	// time.Sleep is intentional: the test needs an idle gap longer than the timeout.
	time.Sleep(60 * time.Millisecond)
	require.Error(t, cb.Process(NewExchange(context.Background())))
	// Count reset after idle gap, so the circuit is still closed.
	require.Error(t, cb.Process(NewExchange(context.Background())))
	assert.ErrorIs(t, cb.Process(NewExchange(context.Background())), ErrCircuitOpen)
}

func TestCircuitBreaker_ErrStopRoutingIsNotFailure(t *testing.T) {
	cb := NewCircuitBreaker()
	cb.SetFailureThreshold(1)
	cb.SetProcessor(ProcessorFunc(func(*Exchange) error { return ErrStopRouting }))

	err := cb.Process(NewExchange(context.Background()))
	require.ErrorIs(t, err, ErrStopRouting)
	err = cb.Process(NewExchange(context.Background()))
	require.ErrorIs(t, err, ErrStopRouting)
	assert.NotErrorIs(t, err, ErrCircuitOpen)
}

func TestCircuitBreaker_OpenTimeoutValidationAndClearsOutAndGenerationABA(t *testing.T) {
	t.Run("DSL rejects non-positive thresholds and OpenTimeout", func(t *testing.T) {
		cases := []struct {
			name string
			cfg  func(*CircuitBreakerDefinition) *CircuitBreakerDefinition
		}{
			{"FailureThreshold 0", func(d *CircuitBreakerDefinition) *CircuitBreakerDefinition { return d.FailureThreshold(0) }},
			{"FailureThreshold -1", func(d *CircuitBreakerDefinition) *CircuitBreakerDefinition { return d.FailureThreshold(-1) }},
			{"SuccessThreshold 0", func(d *CircuitBreakerDefinition) *CircuitBreakerDefinition { return d.SuccessThreshold(0) }},
			{"SuccessThreshold -1", func(d *CircuitBreakerDefinition) *CircuitBreakerDefinition { return d.SuccessThreshold(-1) }},
			{"OpenTimeout 0", func(d *CircuitBreakerDefinition) *CircuitBreakerDefinition { return d.OpenTimeout(0) }},
			{"OpenTimeout -1s", func(d *CircuitBreakerDefinition) *CircuitBreakerDefinition { return d.OpenTimeout(-time.Second) }},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				ctx := NewCamelContext()
				d := ctx.CreateRouteBuilder().From("direct:cb-bad").CircuitBreaker()
				_, err := tc.cfg(d).End().Build()
				assert.Error(t, err)
				assert.Empty(t, ctx.GetRoutes())
			})
		}
	})

	t.Run("Validator rejects invalid CircuitBreaker including nested", func(t *testing.T) {
		cb := NewCircuitBreaker()
		cb.SetOpenTimeout(0)
		cb.SetProcessor(ProcessorFunc(func(*Exchange) error { return nil }))
		v, ok := any(cb).(Validator)
		require.True(t, ok, "CircuitBreaker must implement Validator")
		assert.Error(t, v.Validate())

		cb2 := NewCircuitBreaker()
		cb2.SetFailureThreshold(0)
		cb2.SetProcessor(ProcessorFunc(func(*Exchange) error { return nil }))
		v2, ok := any(cb2).(Validator)
		require.True(t, ok)
		assert.Error(t, v2.Validate())

		cb3 := NewCircuitBreaker()
		cb3.SetSuccessThreshold(0)
		cb3.SetProcessor(ProcessorFunc(func(*Exchange) error { return nil }))
		v3, ok := any(cb3).(Validator)
		require.True(t, ok)
		assert.Error(t, v3.Validate())

		// Nested inside Choice: Route.Validate() walks childProcessorContainer.
		ctx := NewCamelContext()
		route, err := ctx.CreateRouteBuilder().
			From("direct:nested-cb").
			Choice().
			When("${body} == 'a'").
			Process(cb).
			EndChoice().
			Build()
		require.NoError(t, err)
		assert.Error(t, route.Validate())
	})

	t.Run("clears failed Out before fallback", func(t *testing.T) {
		ctx := NewCamelContext()
		var fbInBody, fbOutBody any
		var fbHasBadHeader bool

		_, err := ctx.CreateRouteBuilder().
			From("direct:cb-fb").
			ProcessFunc(func(e *Exchange) error {
				fbInBody = e.GetIn().GetBody()
				fbOutBody = e.GetOut().GetBody()
				_, fbHasBadHeader = e.GetIn().GetHeader("BadHeader")
				return nil
			}).
			Build()
		require.NoError(t, err)

		_, err = ctx.CreateRouteBuilder().
			From("direct:cb-main").
			CircuitBreaker().
			FailureThreshold(1).
			OpenTimeout(time.Second).
			ProcessFunc(func(e *Exchange) error {
				e.GetOut().SetBody("partial-err")
				e.GetOut().SetHeader("BadHeader", "bad")
				return errors.New("downstream boom")
			}).
			OnFallback().
			To("direct:cb-fb").
			End().
			End().
			Build()
		require.NoError(t, err)
		require.NoError(t, ctx.Start())
		t.Cleanup(func() { _ = ctx.Stop() })

		ex := NewExchange(context.Background())
		ex.GetIn().SetBody("original")
		prod, err := ctx.GetProducer("direct:cb-main")
		require.NoError(t, err)
		require.NoError(t, prod.Send(ex))

		assert.Equal(t, "original", fbInBody)
		assert.Nil(t, fbOutBody)
		assert.False(t, fbHasBadHeader)
	})

	t.Run("generation ABA safety isolates stale failures and successes", func(t *testing.T) {
		cb := NewCircuitBreaker()
		cb.SetFailureThreshold(2)
		cb.SetSuccessThreshold(1)
		cb.SetOpenTimeout(15 * time.Millisecond)

		staleEntered := make(chan struct{})
		releaseStale := make(chan struct{})
		var mode atomic.Int32 // 0 = fail immediately, 1 = succeed immediately

		cb.SetProcessor(ProcessorFunc(func(e *Exchange) error {
			if tag, _ := e.GetHeaderAsString("tag"); tag == "stale" {
				close(staleEntered)
				<-releaseStale
				if mode.Load() == 2 {
					return nil
				}
				return errors.New("stale failure from gen 0")
			}
			if mode.Load() == 0 {
				return errors.New("fail")
			}
			return nil
		}))

		// 1. Start stale request in StateClosed (gen 0).
		staleErrCh := make(chan error, 1)
		go func() {
			ex := NewExchange(context.Background())
			ex.SetHeader("tag", "stale")
			staleErrCh <- cb.Process(ex)
		}()
		<-staleEntered

		// 2. Open the circuit in gen 0 (2 failures -> StateOpen, gen 1).
		require.Error(t, cb.Process(NewExchange(context.Background())))
		require.Error(t, cb.Process(NewExchange(context.Background())))
		require.ErrorIs(t, cb.Process(NewExchange(context.Background())), ErrCircuitOpen)

		// 3. Wait for OpenTimeout and close the circuit again (StateClosed, gen 3 -> ABA!).
		time.Sleep(25 * time.Millisecond)
		mode.Store(1)
		require.NoError(t, cb.Process(NewExchange(context.Background())))

		// 4. Accumulate 1 failure in gen 3 (below threshold 2, circuit stays Closed).
		mode.Store(0)
		require.Error(t, cb.Process(NewExchange(context.Background())))

		// 5. Release the stale failure from gen 0: must NOT push gen 3 to failureCount=2 and open it!
		close(releaseStale)
		require.Error(t, <-staleErrCh)

		cb.mu.RLock()
		state := cb.state
		failCount := cb.failureCount
		cb.mu.RUnlock()
		assert.Equal(t, StateClosed, state, "stale failure from prior Closed generation must not reopen current Closed circuit")
		assert.Equal(t, 1, failCount, "stale failure must not increment current generation failureCount")

		// Also verify stale success cannot clear gen 3's failureCount.
		cb2 := NewCircuitBreaker()
		cb2.SetFailureThreshold(2)
		cb2.SetSuccessThreshold(1)
		cb2.SetOpenTimeout(15 * time.Millisecond)
		stale2Entered := make(chan struct{})
		releaseStale2 := make(chan struct{})
		var mode2 atomic.Int32
		cb2.SetProcessor(ProcessorFunc(func(e *Exchange) error {
			if tag, _ := e.GetHeaderAsString("tag"); tag == "stale" {
				close(stale2Entered)
				<-releaseStale2
				return nil // stale success from gen 0
			}
			if mode2.Load() == 0 {
				return errors.New("fail")
			}
			return nil
		}))
		stale2Done := make(chan error, 1)
		go func() {
			ex := NewExchange(context.Background())
			ex.SetHeader("tag", "stale")
			stale2Done <- cb2.Process(ex)
		}()
		<-stale2Entered
		require.Error(t, cb2.Process(NewExchange(context.Background())))
		require.Error(t, cb2.Process(NewExchange(context.Background())))
		time.Sleep(25 * time.Millisecond)
		mode2.Store(1)
		require.NoError(t, cb2.Process(NewExchange(context.Background())))
		mode2.Store(0)
		require.Error(t, cb2.Process(NewExchange(context.Background()))) // 1 failure in gen 3
		close(releaseStale2)
		require.NoError(t, <-stale2Done)

		cb2.mu.RLock()
		failCount2 := cb2.failureCount
		cb2.mu.RUnlock()
		assert.Equal(t, 1, failCount2, "stale success from prior Closed generation must not reset current generation failureCount")
	})

	t.Run("concurrent setters are race-free", func(t *testing.T) {
		cb := NewCircuitBreaker()
		cb.SetProcessor(ProcessorFunc(func(*Exchange) error { return nil }))
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 20; j++ {
					cb.SetFailureThreshold(3)
					cb.SetSuccessThreshold(2)
					cb.SetOpenTimeout(50 * time.Millisecond)
					cb.SetFallbackProcessor(ProcessorFunc(func(*Exchange) error { return nil }))
					_ = cb.Process(NewExchange(context.Background()))
				}
			}()
		}
		wg.Wait()
	})
}

