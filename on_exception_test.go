package gocamel

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// onExceptionTypedError is a custom struct error matched by type (errors.As).
type onExceptionTypedError struct{ Code int }

func (e *onExceptionTypedError) Error() string { return fmt.Sprintf("typed error %d", e.Code) }

var errOnExceptionSentinel = errors.New("on-exception sentinel")

// startOnExceptionContext starts ctx and stops it when the test ends.
func startOnExceptionContext(t *testing.T, ctx *CamelContext) {
	t.Helper()
	require.NoError(t, ctx.Start())
	t.Cleanup(func() { _ = ctx.Stop() })
}

// sendDirect sends body to a direct: endpoint and returns the exchange and the
// error seen by the sender.
func sendDirect(t *testing.T, ctx *CamelContext, uri string, body any) (*Exchange, error) {
	t.Helper()
	endpoint, err := ctx.CreateEndpoint(uri)
	require.NoError(t, err)
	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)
	require.NoError(t, producer.Start(context.Background()))
	t.Cleanup(func() { _ = producer.Stop() })

	exchange := NewExchange(context.Background())
	exchange.In.SetBody(body)
	return exchange, producer.Send(exchange)
}

// failingN returns a processor that fails with err for the first n calls and
// succeeds afterwards; calls counts every invocation.
func failingN(n int32, err error, calls *atomic.Int32) ProcessorFunc {
	return func(*Exchange) error {
		if calls.Add(1) <= n {
			return err
		}
		return nil
	}
}

func alwaysFail(err error, calls *atomic.Int32) ProcessorFunc {
	return func(*Exchange) error {
		calls.Add(1)
		return err
	}
}

func TestOnException_SentinelMatchRetriesThenRunsHandler(t *testing.T) {
	ctx := NewCamelContext()
	var calls, handled atomic.Int32

	_, err := ctx.CreateRouteBuilder().
		OnException(errOnExceptionSentinel).
		MaximumRedeliveries(3).
		RedeliveryDelay(time.Millisecond).
		ProcessFunc(func(e *Exchange) error {
			handled.Add(1)
			caught, ok := e.GetProperty(CamelExceptionCaught)
			assert.True(t, ok)
			assert.ErrorIs(t, caught.(error), errOnExceptionSentinel)
			counter, _ := e.GetProperty(CamelRedeliveryCounter)
			assert.Equal(t, 3, counter)
			return nil
		}).
		End().
		From("direct:in").
		Process(alwaysFail(fmt.Errorf("wrapped: %w", errOnExceptionSentinel), &calls)).
		Build()
	require.NoError(t, err)
	startOnExceptionContext(t, ctx)

	_, sendErr := sendDirect(t, ctx, "direct:in", "x")
	assert.ErrorIs(t, sendErr, errOnExceptionSentinel, "Handled(false) is the default: the error propagates")
	assert.Equal(t, int32(4), calls.Load(), "1 attempt + 3 redeliveries")
	assert.Equal(t, int32(1), handled.Load())
}

func TestOnException_RedeliverySucceeds_HandlerNotRun(t *testing.T) {
	ctx := NewCamelContext()
	var calls, handled atomic.Int32

	_, err := ctx.CreateRouteBuilder().
		OnException(errOnExceptionSentinel).
		MaximumRedeliveries(3).
		RedeliveryDelay(time.Millisecond).
		ProcessFunc(func(*Exchange) error { handled.Add(1); return nil }).
		End().
		From("direct:in").
		Process(failingN(2, errOnExceptionSentinel, &calls)).
		SetBody("ok").
		Build()
	require.NoError(t, err)
	startOnExceptionContext(t, ctx)

	ex, sendErr := sendDirect(t, ctx, "direct:in", "x")
	require.NoError(t, sendErr)
	assert.Equal(t, int32(3), calls.Load())
	assert.Equal(t, int32(0), handled.Load())
	assert.Equal(t, "ok", ex.GetResponse().GetBody())
}

func TestOnException_TypeMatchWithErrorsAs(t *testing.T) {
	ctx := NewCamelContext()
	var handled atomic.Int32
	var seenCode atomic.Int32

	_, err := ctx.CreateRouteBuilder().
		OnException(ErrorType[*onExceptionTypedError]()).
		Handled(true).
		ProcessFunc(func(e *Exchange) error {
			handled.Add(1)
			caught, _ := e.GetProperty(CamelExceptionCaught)
			var typed *onExceptionTypedError
			if assert.ErrorAs(t, caught.(error), &typed) {
				seenCode.Store(int32(typed.Code))
			}
			return nil
		}).
		End().
		From("direct:in").
		ProcessFunc(func(*Exchange) error {
			return fmt.Errorf("context: %w", &onExceptionTypedError{Code: 42})
		}).
		Build()
	require.NoError(t, err)
	startOnExceptionContext(t, ctx)

	_, sendErr := sendDirect(t, ctx, "direct:in", "x")
	assert.NoError(t, sendErr)
	assert.Equal(t, int32(1), handled.Load())
	assert.Equal(t, int32(42), seenCode.Load())
}

func TestOnException_PointerToErrorPointerTarget(t *testing.T) {
	// new(*T) is the errors.As target convention and is accepted as-is.
	ctx := NewCamelContext()
	var handled atomic.Int32

	_, err := ctx.CreateRouteBuilder().
		OnException(new(*onExceptionTypedError)).
		Handled(true).
		ProcessFunc(func(*Exchange) error { handled.Add(1); return nil }).
		End().
		From("direct:in").
		ProcessFunc(func(*Exchange) error { return &onExceptionTypedError{Code: 1} }).
		Build()
	require.NoError(t, err)
	startOnExceptionContext(t, ctx)

	_, sendErr := sendDirect(t, ctx, "direct:in", "x")
	assert.NoError(t, sendErr)
	assert.Equal(t, int32(1), handled.Load())
}

func TestOnException_FirstMatchingClauseWins(t *testing.T) {
	ctx := NewCamelContext()
	var first, second atomic.Int32

	_, err := ctx.CreateRouteBuilder().
		OnException(ErrorType[*onExceptionTypedError]()).
		Handled(true).
		ProcessFunc(func(*Exchange) error { first.Add(1); return nil }).
		End().
		OnException(errOnExceptionSentinel).
		Handled(true).
		ProcessFunc(func(*Exchange) error { second.Add(1); return nil }).
		End().
		From("direct:in").
		ProcessFunc(func(*Exchange) error {
			// Matches both clauses: the first declared one must win.
			return errors.Join(&onExceptionTypedError{Code: 7}, errOnExceptionSentinel)
		}).
		Build()
	require.NoError(t, err)
	startOnExceptionContext(t, ctx)

	_, sendErr := sendDirect(t, ctx, "direct:in", "x")
	assert.NoError(t, sendErr)
	assert.Equal(t, int32(1), first.Load())
	assert.Equal(t, int32(0), second.Load())
}

func TestOnException_SeveralTargetsInOneClause(t *testing.T) {
	ctx := NewCamelContext()
	var handled atomic.Int32
	other := errors.New("other")

	_, err := ctx.CreateRouteBuilder().
		OnException(errOnExceptionSentinel, other).
		Handled(true).
		ProcessFunc(func(*Exchange) error { handled.Add(1); return nil }).
		End().
		From("direct:in").
		ProcessFunc(func(*Exchange) error { return other }).
		Build()
	require.NoError(t, err)
	startOnExceptionContext(t, ctx)

	_, sendErr := sendDirect(t, ctx, "direct:in", "x")
	assert.NoError(t, sendErr)
	assert.Equal(t, int32(1), handled.Load())
}

func TestOnException_HandledStopsRoutingWithoutError(t *testing.T) {
	ctx := NewCamelContext()
	var after atomic.Int32

	_, err := ctx.CreateRouteBuilder().
		OnException(errOnExceptionSentinel).
		Handled(true).
		SetBody("recovered").
		End().
		From("direct:in").
		ProcessFunc(func(*Exchange) error { return errOnExceptionSentinel }).
		ProcessFunc(func(*Exchange) error { after.Add(1); return nil }).
		Build()
	require.NoError(t, err)
	startOnExceptionContext(t, ctx)

	ex, sendErr := sendDirect(t, ctx, "direct:in", "x")
	assert.NoError(t, sendErr, "a handled error must not reach the sender")
	assert.Equal(t, int32(0), after.Load(), "routing stops after the handler")
	assert.Equal(t, "recovered", ex.GetResponse().GetBody())
	assert.NoError(t, ex.Error)
}

func TestOnException_HandledReturnsStopRoutingFromRoute(t *testing.T) {
	ctx := NewCamelContext()
	route, err := ctx.CreateRouteBuilder().
		OnException(errOnExceptionSentinel).
		Handled(true).
		End().
		From("direct:in").
		ProcessFunc(func(*Exchange) error { return errOnExceptionSentinel }).
		Build()
	require.NoError(t, err)

	routeErr := route.Process(NewExchange(context.Background()))
	assert.ErrorIs(t, routeErr, ErrStopRouting)
	assert.ErrorIs(t, routeErr, errOnExceptionSentinel, "the original error stays inspectable")
}

func TestOnException_ContinuedRunsNextProcessor(t *testing.T) {
	ctx := NewCamelContext()
	var handled, after atomic.Int32

	_, err := ctx.CreateRouteBuilder().
		OnException(errOnExceptionSentinel).
		Continued(true).
		ProcessFunc(func(*Exchange) error { handled.Add(1); return nil }).
		End().
		From("direct:in").
		SetBody("before").
		ProcessFunc(func(*Exchange) error { return errOnExceptionSentinel }).
		ProcessFunc(func(e *Exchange) error {
			after.Add(1)
			e.GetOut().SetBody(fmt.Sprint(e.GetIn().GetBody(), "+after"))
			return nil
		}).
		Build()
	require.NoError(t, err)
	startOnExceptionContext(t, ctx)

	ex, sendErr := sendDirect(t, ctx, "direct:in", "x")
	require.NoError(t, sendErr)
	assert.Equal(t, int32(1), handled.Load(), "the clause's processors run before continuing (Camel semantics)")
	assert.Equal(t, int32(1), after.Load())
	assert.Equal(t, "before+after", ex.GetResponse().GetBody())
	assert.NoError(t, ex.Error)
}

func TestOnException_NoMatchUsesDefaultErrorHandler(t *testing.T) {
	comp, dlq := newMockDLQComponent()
	ctx := NewCamelContext()
	ctx.GetComponentRegistry().RegisterComponent("mockdlq", comp)
	var handled atomic.Int32
	unmatched := errors.New("unmatched")

	_, err := ctx.CreateRouteBuilder().
		OnException(errOnExceptionSentinel).
		Handled(true).
		ProcessFunc(func(*Exchange) error { handled.Add(1); return nil }).
		End().
		From("direct:in").
		DeadLetterChannel("mockdlq:errors").
		ProcessFunc(func(*Exchange) error { return unmatched }).
		Build()
	require.NoError(t, err)
	startOnExceptionContext(t, ctx)

	_, sendErr := sendDirect(t, ctx, "direct:in", "x")
	assert.ErrorIs(t, sendErr, unmatched)
	assert.Equal(t, int32(0), handled.Load())
	dlq.mu.Lock()
	assert.Len(t, dlq.received, 1, "the DeadLetterChannel still handles unmatched errors")
	dlq.mu.Unlock()
}

func TestOnException_MatchBypassesDefaultErrorHandler(t *testing.T) {
	comp, dlq := newMockDLQComponent()
	ctx := NewCamelContext()
	ctx.GetComponentRegistry().RegisterComponent("mockdlq", comp)

	_, err := ctx.CreateRouteBuilder().
		OnException(errOnExceptionSentinel).
		End().
		From("direct:in").
		DeadLetterChannel("mockdlq:errors").
		ProcessFunc(func(*Exchange) error { return errOnExceptionSentinel }).
		Build()
	require.NoError(t, err)
	startOnExceptionContext(t, ctx)

	_, sendErr := sendDirect(t, ctx, "direct:in", "x")
	assert.ErrorIs(t, sendErr, errOnExceptionSentinel)
	dlq.mu.Lock()
	assert.Empty(t, dlq.received, "a matched clause replaces the route's ErrorHandler for that error")
	dlq.mu.Unlock()
}

func TestOnException_ZeroRedeliveriesRunsHandlerImmediately(t *testing.T) {
	ctx := NewCamelContext()
	var calls, handled atomic.Int32

	_, err := ctx.CreateRouteBuilder().
		OnException(errOnExceptionSentinel).
		MaximumRedeliveries(0).
		Handled(true).
		ProcessFunc(func(*Exchange) error { handled.Add(1); return nil }).
		End().
		From("direct:in").
		Process(alwaysFail(errOnExceptionSentinel, &calls)).
		Build()
	require.NoError(t, err)
	startOnExceptionContext(t, ctx)

	_, sendErr := sendDirect(t, ctx, "direct:in", "x")
	assert.NoError(t, sendErr)
	assert.Equal(t, int32(1), calls.Load(), "no redelivery")
	assert.Equal(t, int32(1), handled.Load())
}

func TestOnException_DeadLetteredExchangeDoesNotTrigger(t *testing.T) {
	comp, dlq := newMockDLQComponent()
	ctx := NewCamelContext()
	ctx.GetComponentRegistry().RegisterComponent("mockdlq", comp)
	var handled atomic.Int32

	// The inner route dead-letters the failure and propagates the original
	// error (DeadLetterHandled(false)) back across the direct: boundary.
	_, err := ctx.CreateRouteBuilder().
		From("direct:inner").
		DeadLetterChannel("mockdlq:errors").
		ProcessFunc(func(*Exchange) error { return errOnExceptionSentinel }).
		Build()
	require.NoError(t, err)

	// The outer route has a matching clause, but the exchange has already
	// been dead-lettered: handling it again would duplicate the recovery.
	_, err = ctx.CreateRouteBuilder().
		OnException(errOnExceptionSentinel).
		Handled(true).
		ProcessFunc(func(*Exchange) error { handled.Add(1); return nil }).
		End().
		From("direct:outer").
		To("direct:inner").
		Build()
	require.NoError(t, err)
	startOnExceptionContext(t, ctx)

	ex, sendErr := sendDirect(t, ctx, "direct:outer", "x")
	assert.ErrorIs(t, sendErr, errOnExceptionSentinel, "the error propagates as without OnException")
	assert.True(t, IsDeadLettered(ex))
	assert.Equal(t, int32(0), handled.Load())
	dlq.mu.Lock()
	assert.Len(t, dlq.received, 1)
	dlq.mu.Unlock()
}

func TestOnException_RecoveryEndpointDirect(t *testing.T) {
	ctx := NewCamelContext()
	mock, err := ctx.GetMockEndpoint("mock:recovered")
	require.NoError(t, err)

	_, err = ctx.CreateRouteBuilder().
		From("direct:recovery").
		SetHeader("recovered", true).
		To("mock:recovered").
		Build()
	require.NoError(t, err)

	_, err = ctx.CreateRouteBuilder().
		OnException(errOnExceptionSentinel).
		Handled(true).
		To("direct:recovery").
		End().
		From("direct:in").
		ProcessFunc(func(*Exchange) error { return errOnExceptionSentinel }).
		Build()
	require.NoError(t, err)
	startOnExceptionContext(t, ctx)

	_, sendErr := sendDirect(t, ctx, "direct:in", "payload")
	assert.NoError(t, sendErr)
	require.Equal(t, 1, mock.MessageCount())
	assert.Equal(t, "payload", mock.Body(0))
	v, ok := mock.Header(0, "recovered")
	assert.True(t, ok)
	assert.Equal(t, true, v)
}

func TestOnException_HandlerFailurePropagates(t *testing.T) {
	ctx := NewCamelContext()
	handlerErr := errors.New("handler broke")

	_, err := ctx.CreateRouteBuilder().
		OnException(errOnExceptionSentinel).
		Handled(true).
		ProcessFunc(func(*Exchange) error { return handlerErr }).
		End().
		From("direct:in").
		ProcessFunc(func(*Exchange) error { return errOnExceptionSentinel }).
		Build()
	require.NoError(t, err)
	startOnExceptionContext(t, ctx)

	_, sendErr := sendDirect(t, ctx, "direct:in", "x")
	assert.ErrorIs(t, sendErr, handlerErr)
	assert.ErrorIs(t, sendErr, errOnExceptionSentinel)
	assert.NotErrorIs(t, sendErr, ErrStopRouting)
}

func TestOnException_RedeliveryAbortsOnContextCancel(t *testing.T) {
	ctx := NewCamelContext()
	var calls atomic.Int32
	route, err := ctx.CreateRouteBuilder().
		OnException(errOnExceptionSentinel).
		MaximumRedeliveries(5).
		RedeliveryDelay(time.Hour).
		End().
		From("direct:in").
		Process(alwaysFail(errOnExceptionSentinel, &calls)).
		Build()
	require.NoError(t, err)

	exCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- route.Process(NewExchange(exCtx)) }()
	assert.Eventually(t, func() bool { return calls.Load() == 1 }, time.Second, time.Millisecond)
	cancel()

	select {
	case routeErr := <-done:
		assert.ErrorIs(t, routeErr, context.Canceled)
		assert.ErrorIs(t, routeErr, errOnExceptionSentinel)
	case <-time.After(5 * time.Second):
		t.Fatal("redelivery did not observe context cancellation")
	}
}

func TestOnException_BuildValidation(t *testing.T) {
	var nilTyped *onExceptionTypedError
	tests := []struct {
		name    string
		targets []any
	}{
		{"no target", nil},
		{"nil target", []any{nil}},
		{"typed nil error", []any{nilTyped}},
		{"not an error", []any{"boom"}},
		{"pointer to non-error", []any{new(int)}},
		{"nil among valid", []any{errOnExceptionSentinel, nil}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := NewCamelContext()
			_, err := ctx.CreateRouteBuilder().
				OnException(tc.targets...).
				End().
				From("direct:in").
				Build()
			assert.Error(t, err)
		})
	}
}

func TestOnException_NegativeRedeliveriesRejected(t *testing.T) {
	ctx := NewCamelContext()
	_, err := ctx.CreateRouteBuilder().
		OnException(errOnExceptionSentinel).
		MaximumRedeliveries(-1).
		End().
		From("direct:in").
		Build()
	assert.Error(t, err)
}

func TestOnException_BackoffIsExponentialAndCapped(t *testing.T) {
	c := &onExceptionClause{
		redeliveryDelay:     10 * time.Millisecond,
		backOffMultiplier:   2,
		maxRedeliveryDelay:  35 * time.Millisecond,
		maximumRedeliveries: 5,
	}
	assert.Equal(t, 10*time.Millisecond, c.delay(0))
	assert.Equal(t, 20*time.Millisecond, c.delay(1))
	assert.Equal(t, 35*time.Millisecond, c.delay(2), "capped by MaximumRedeliveryDelay")

	c.backOffMultiplier = 1
	assert.Equal(t, 10*time.Millisecond, c.delay(3), "a multiplier of 1 is a fixed delay")
}

func TestOnException_ValueTypeErrorMatch(t *testing.T) {
	// A value-receiver error type is matched by passing a pointer to it,
	// like an errors.As target.
	ctx := NewCamelContext()
	var handled atomic.Int32
	_, err := ctx.CreateRouteBuilder().
		OnException(&onExceptionValueError{}).
		Handled(true).
		ProcessFunc(func(*Exchange) error { handled.Add(1); return nil }).
		End().
		From("direct:in").
		ProcessFunc(func(*Exchange) error { return fmt.Errorf("w: %w", onExceptionValueError{msg: "v"}) }).
		Build()
	require.NoError(t, err)
	startOnExceptionContext(t, ctx)

	_, sendErr := sendDirect(t, ctx, "direct:in", "x")
	assert.NoError(t, sendErr)
	assert.Equal(t, int32(1), handled.Load())
}

type onExceptionValueError struct{ msg string }

func (e onExceptionValueError) Error() string { return e.msg }

func TestOnException_PanicMatchedByErrProcessorPanic(t *testing.T) {
	ctx := NewCamelContext()
	var handled atomic.Int32
	_, err := ctx.CreateRouteBuilder().
		OnException(ErrProcessorPanic).
		Handled(true).
		ProcessFunc(func(*Exchange) error { handled.Add(1); return nil }).
		End().
		From("direct:in").
		ProcessFunc(func(*Exchange) error { panic("boom") }).
		Build()
	require.NoError(t, err)
	startOnExceptionContext(t, ctx)

	_, sendErr := sendDirect(t, ctx, "direct:in", "x")
	assert.NoError(t, sendErr)
	assert.Equal(t, int32(1), handled.Load())
}

func TestOnException_ConcurrentExchanges(t *testing.T) {
	ctx := NewCamelContext()
	var calls, handled atomic.Int32
	_, err := ctx.CreateRouteBuilder().
		OnException(errOnExceptionSentinel).
		MaximumRedeliveries(2).
		RedeliveryDelay(time.Microsecond).
		Handled(true).
		ProcessFunc(func(*Exchange) error { handled.Add(1); return nil }).
		End().
		From("direct:in").
		Process(alwaysFail(errOnExceptionSentinel, &calls)).
		Build()
	require.NoError(t, err)
	startOnExceptionContext(t, ctx)

	endpoint, err := ctx.CreateEndpoint("direct:in")
	require.NoError(t, err)
	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)
	require.NoError(t, producer.Start(context.Background()))
	t.Cleanup(func() { _ = producer.Stop() })

	const n = 20
	errs := make(chan error, n)
	for range n {
		go func() { errs <- producer.Send(NewExchange(context.Background())) }()
	}
	for range n {
		assert.NoError(t, <-errs)
	}
	assert.Equal(t, int32(n*3), calls.Load())
	assert.Equal(t, int32(n), handled.Load())
}
