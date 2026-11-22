package gocamel

import (
	"context"
	"errors"
	"math"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockDLQEndpoint captures exchanges sent to it for verification
type mockDLQEndpoint struct {
	received []*Exchange
	mu       sync.Mutex
	uri      string
}

func (e *mockDLQEndpoint) URI() string { return e.uri }
func (e *mockDLQEndpoint) CreateConsumer(processor Processor) (Consumer, error) {
	return nil, errors.New("mock DLQ: consumer not supported")
}

func (e *mockDLQEndpoint) CreateProducer() (Producer, error) {
	return &mockDLQProducer{endpoint: e}, nil
}

type mockDLQProducer struct {
	endpoint *mockDLQEndpoint
}

func (p *mockDLQProducer) Start(ctx context.Context) error { return nil }
func (p *mockDLQProducer) Stop() error                     { return nil }
func (p *mockDLQProducer) Send(exchange *Exchange) error {
	p.endpoint.mu.Lock()
	p.endpoint.received = append(p.endpoint.received, exchange)
	p.endpoint.mu.Unlock()
	return nil
}

// mockDLQComponent implements the Component interface for dead letter queue testing
type mockDLQComponent struct {
	endpoint *mockDLQEndpoint
}

func (c *mockDLQComponent) CreateEndpoint(uri string) (Endpoint, error) {
	return c.endpoint, nil
}

func newMockDLQComponent() (*mockDLQComponent, *mockDLQEndpoint) {
	ep := &mockDLQEndpoint{uri: "mockdlq:errors"}
	return &mockDLQComponent{endpoint: ep}, ep
}

func TestDeadLetterChannel_SendsFailedExchange(t *testing.T) {
	comp, dlq := newMockDLQComponent()

	ctx := NewCamelContext()
	ctx.GetComponentRegistry().RegisterComponent("mockdlq", comp)

	dlc := NewDeadLetterChannel(ctx, "mockdlq:errors")
	assert.NotNil(t, dlc)

	exchange := NewExchange(context.Background())
	exchange.GetIn().SetBody("test body")
	originalErr := errors.New("processor failed")

	err := dlc.HandleError(exchange, originalErr)

	// Should return the original error after DLQ processing
	assert.ErrorIs(t, err, originalErr)

	// Verify exchange was captured by DLQ
	dlq.mu.Lock()
	assert.Len(t, dlq.received, 1)
	dlq.mu.Unlock()

	if len(dlq.received) > 0 {
		received := dlq.received[0]
		assert.Equal(t, "test body", received.GetIn().GetBody())

		caught, ok := received.GetProperty(CamelExceptionCaught)
		assert.True(t, ok)
		assert.Equal(t, originalErr, caught)
	}
}

func TestDeadLetterChannel_Redelivery(t *testing.T) {
	comp, dlq := newMockDLQComponent()

	ctx := NewCamelContext()
	ctx.GetComponentRegistry().RegisterComponent("mockdlq", comp)

	dlc := NewDeadLetterChannel(ctx, "mockdlq:errors",
		MaxRedeliveries(2),
		RedeliveryDelay(10), // nanoseconds for fast tests
	)
	assert.NotNil(t, dlc)

	exchange := NewExchange(context.Background())

	// First attempt: should retry (returns nil = retry signal)
	err := dlc.HandleError(exchange, errors.New("attempt 1"))
	assert.Nil(t, err, "first failure should trigger retry (nil = retry)")

	// Second attempt: should retry again
	err = dlc.HandleError(exchange, errors.New("attempt 2"))
	assert.Nil(t, err, "second failure should trigger retry")

	// Third attempt: max redeliveries reached (2), should go to DLQ
	err = dlc.HandleError(exchange, errors.New("attempt 3"))
	assert.NotNil(t, err, "third failure should go to DLQ")

	dlq.mu.Lock()
	assert.Len(t, dlq.received, 1, "only one exchange should reach DLQ after max redeliveries")
	dlq.mu.Unlock()

	if len(dlq.received) > 0 {
		received := dlq.received[0]
		counter, _ := received.GetProperty(CamelRedeliveryCounter)
		assert.Equal(t, 2, counter, "redelivery counter should be 2")
	}
}

func TestDeadLetterChannel_ErrorPropertiesOnExchange(t *testing.T) {
	comp, dlq := newMockDLQComponent()

	ctx := NewCamelContext()
	ctx.GetComponentRegistry().RegisterComponent("mockdlq", comp)

	dlc := NewDeadLetterChannel(ctx, "mockdlq:errors")
	originalErr := errors.New("simulated crash")

	exchange := NewExchange(context.Background())
	exchange.GetIn().SetHeader("CustomHeader", "custom-value")

	dlc.HandleError(exchange, originalErr)

	dlq.mu.Lock()
	assert.Len(t, dlq.received, 1)
	dlq.mu.Unlock()

	received := dlq.received[0]

	caught, ok := received.GetProperty(CamelExceptionCaught)
	assert.True(t, ok)
	assert.Equal(t, originalErr, caught)

	assert.Equal(t, originalErr, received.Error)

	hdr, ok := received.GetIn().GetHeader("CustomHeader")
	assert.True(t, ok)
	assert.Equal(t, "custom-value", hdr)

	redelivered, ok := received.GetIn().GetHeader("CamelRedelivered")
	assert.True(t, ok)
	assert.Equal(t, false, redelivered)

	exceptionMsg, ok := received.GetIn().GetHeader("CamelExceptionMessage")
	assert.True(t, ok)
	assert.Equal(t, originalErr.Error(), exceptionMsg)
}

func TestNoErrorHandler_BubblesUp(t *testing.T) {
	handler := &NoErrorHandler{}
	exchange := NewExchange(context.Background())
	originalErr := errors.New("something failed")

	err := handler.HandleError(exchange, originalErr)
	assert.ErrorIs(t, err, originalErr)
}

func TestRouteBuilder_DeadLetterChannelDSL(t *testing.T) {
	comp, dlq := newMockDLQComponent()

	ctx := NewCamelContext()
	ctx.GetComponentRegistry().RegisterComponent("mockdlq", comp)
	ctx.AddComponent("direct", NewDirectComponent())

	route, err := ctx.CreateRouteBuilder().
		From("direct:start").
		DeadLetterChannel("mockdlq:errors", MaxRedeliveries(0)).
		ProcessFunc(func(exchange *Exchange) error {
			return errors.New("boom")
		}).
		Build()

	assert.NoError(t, err)
	assert.NotNil(t, route)
	assert.NotNil(t, route.GetErrorHandler())

	exchange := NewExchange(context.Background())
	exchange.GetIn().SetBody("test")

	processErr := route.Process(exchange)
	assert.NotNil(t, processErr)

	dlq.mu.Lock()
	assert.Len(t, dlq.received, 1, "failed exchange should be sent to DLQ")
	dlq.mu.Unlock()
}

func TestDeadLetterChannel_RedeliveryReplaysFailedProcessorAndContinues(t *testing.T) {
	comp, dlq := newMockDLQComponent()

	ctx := NewCamelContext()
	ctx.GetComponentRegistry().RegisterComponent("mockdlq", comp)
	ctx.AddComponent("direct", NewDirectComponent())

	var attempts int
	var nextRan bool
	route, err := ctx.CreateRouteBuilder().
		From("direct:start").
		DeadLetterChannel("mockdlq:errors", MaxRedeliveries(1), RedeliveryDelay(0)).
		ProcessFunc(func(exchange *Exchange) error {
			attempts++
			if attempts == 1 {
				return errors.New("transient failure")
			}
			return nil
		}).
		ProcessFunc(func(exchange *Exchange) error {
			nextRan = true
			return nil
		}).
		Build()

	assert.NoError(t, err)

	exchange := NewExchange(context.Background())
	processErr := route.Process(exchange)

	assert.NoError(t, processErr)
	assert.Equal(t, 2, attempts, "failed processor should be replayed once")
	assert.True(t, nextRan, "route should continue after successful redelivery")

	dlq.mu.Lock()
	assert.Len(t, dlq.received, 0, "successful redelivery should not send to DLQ")
	dlq.mu.Unlock()
}

func TestDeadLetterChannel_RetryStateCleanedAfterSuccessfulRouteRedelivery(t *testing.T) {
	comp, _ := newMockDLQComponent()

	ctx := NewCamelContext()
	ctx.GetComponentRegistry().RegisterComponent("mockdlq", comp)
	ctx.AddComponent("direct", NewDirectComponent())

	var attempts int
	route, err := ctx.CreateRouteBuilder().
		From("direct:start").
		DeadLetterChannel("mockdlq:errors", MaxRedeliveries(1), RedeliveryDelay(0)).
		ProcessFunc(func(exchange *Exchange) error {
			attempts++
			if attempts == 1 {
				return errors.New("transient failure")
			}
			return nil
		}).
		Build()

	assert.NoError(t, err)

	exchange := NewExchange(context.Background())
	processErr := route.Process(exchange)
	assert.NoError(t, processErr)

	dlc, ok := route.GetErrorHandler().(*DeadLetterChannel)
	assert.True(t, ok)
	dlc.mu.Lock()
	assert.Len(t, dlc.retryAttempts, 0, "retry state should be cleaned without requiring Exchange.Done")
	dlc.mu.Unlock()
}

func TestGetErrorHandler_ReturnsNilForNewRoute(t *testing.T) {
	route := NewRoute()
	assert.Nil(t, route.GetErrorHandler(), "new route should have no error handler")
}

func TestSetErrorHandler_ReturnsCorrectHandler(t *testing.T) {
	route := NewRoute()
	handler := &NoErrorHandler{}
	route.SetErrorHandler(handler)
	assert.Equal(t, handler, route.GetErrorHandler(), "should return the set handler")
}

func TestRouteBuilder_ErrorHandlerDSL(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())

	handler := &NoErrorHandler{}
	route, err := ctx.CreateRouteBuilder().
		From("direct:test").
		ErrorHandler(handler).
		ProcessFunc(func(exchange *Exchange) error { return nil }).
		Build()

	assert.NoError(t, err)
	assert.NotNil(t, route)
	assert.Equal(t, handler, route.GetErrorHandler())
}

func TestRouteBuilder_NoErrorHandler_ErrorBubblesUp(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())

	route, err := ctx.CreateRouteBuilder().
		From("direct:test").
		ProcessFunc(func(exchange *Exchange) error {
			return errors.New("simulated error")
		}).
		Build()

	assert.NoError(t, err)
	assert.Nil(t, route.GetErrorHandler(), "no error handler configured")

	exchange := NewExchange(context.Background())
	processErr := route.Process(exchange)
	assert.Error(t, processErr)
	assert.Contains(t, processErr.Error(), "simulated error")
}

func TestDeadLetterChannel_ExponentialBackoff(t *testing.T) {
	ctx := NewCamelContext()
	dlc := NewDeadLetterChannel(ctx, "log:test",
		MaxRedeliveries(3),
		ExponentialBackoff(2.0),
	)

	assert.NotNil(t, dlc)
	assert.True(t, dlc.useExponentialBackoff)
	assert.Equal(t, 2.0, dlc.backoffMultiplier)
	assert.Equal(t, 3, dlc.maxRedeliveries)
}

func TestDeadLetterChannel_MaxRedeliveriesZero(t *testing.T) {
	comp, dlq := newMockDLQComponent()
	ctx := NewCamelContext()
	ctx.GetComponentRegistry().RegisterComponent("mockdlq", comp)

	dlc := NewDeadLetterChannel(ctx, "mockdlq:errors", MaxRedeliveries(0))
	assert.NotNil(t, dlc)
	assert.Equal(t, 0, dlc.maxRedeliveries)

	exchange := NewExchange(context.Background())
	originalErr := errors.New("immediate DLQ test")

	err := dlc.HandleError(exchange, originalErr)
	assert.ErrorIs(t, err, originalErr)

	dlq.mu.Lock()
	assert.Len(t, dlq.received, 1, "should go directly to DLQ without retries")
	dlq.mu.Unlock()
}

func TestNoErrorHandler_ImplementsInterface(t *testing.T) {
	var handler ErrorHandler = &NoErrorHandler{}
	assert.NotNil(t, handler)
}

func TestExponentialBackoffDelay_CapsOverflow(t *testing.T) {
	d := exponentialBackoffDelay(time.Second, 10, 100)
	assert.Equal(t, maxRedeliveryDelay, d)
}

type failingSendComponent struct{}

func (failingSendComponent) CreateEndpoint(uri string) (Endpoint, error) {
	return failingSendEndpoint{uri: uri}, nil
}

type failingSendEndpoint struct{ uri string }

func (e failingSendEndpoint) URI() string { return e.uri }
func (e failingSendEndpoint) CreateConsumer(Processor) (Consumer, error) {
	return nil, errors.New("unsupported")
}
func (e failingSendEndpoint) CreateProducer() (Producer, error) { return failingSendProducer{}, nil }

type failingSendProducer struct{}

func (failingSendProducer) Start(context.Context) error { return nil }
func (failingSendProducer) Stop() error                 { return nil }
func (failingSendProducer) Send(*Exchange) error        { return errors.New("dlq sink down") }

func TestDeadLetterChannel_SkipsAlreadyDeadLetteredAndNoSyncLeakAndClearsOut(t *testing.T) {
	t.Run("skips already dead-lettered exchange from inner route", func(t *testing.T) {
		comp, dlq := newMockDLQComponent()
		ctx := NewCamelContext()
		ctx.GetComponentRegistry().RegisterComponent("mockdlq", comp)

		_, err := ctx.CreateRouteBuilder().
			From("direct:inner").
			DeadLetterChannel("mockdlq:errors", DeadLetterHandled(false)).
			ProcessFunc(func(*Exchange) error { return errors.New("inner failure") }).
			Build()
		require.NoError(t, err)

		_, err = ctx.CreateRouteBuilder().
			From("direct:outer").
			DeadLetterChannel("mockdlq:errors", MaxRedeliveries(2), RedeliveryDelay(time.Millisecond)).
			To("direct:inner").
			Build()
		require.NoError(t, err)

		require.NoError(t, ctx.Start())
		t.Cleanup(func() { _ = ctx.Stop() })

		prod, err := ctx.GetProducer("direct:outer")
		require.NoError(t, err)
		err = prod.Send(NewExchange(context.Background()))
		require.Error(t, err)

		dlq.mu.Lock()
		count := len(dlq.received)
		dlq.mu.Unlock()
		assert.Equal(t, 1, count, "already dead-lettered exchange must not be retried or dead-lettered again")
	})

	t.Run("no synchronized map leak across repeated DirectProducer retries", func(t *testing.T) {
		comp, _ := newMockDLQComponent()
		ctx := NewCamelContext()
		ctx.GetComponentRegistry().RegisterComponent("mockdlq", comp)

		var step1Calls, step2Calls atomic.Int32
		route, err := ctx.CreateRouteBuilder().
			From("direct:retry-leak").
			DeadLetterChannel("mockdlq:errors", MaxRedeliveries(2), RedeliveryDelay(0)).
			ProcessFunc(func(e *Exchange) error {
				if step1Calls.Add(1)%2 == 1 {
					return errors.New("transient step 1")
				}
				return nil
			}).
			ProcessFunc(func(e *Exchange) error {
				if step2Calls.Add(1)%2 == 1 {
					return errors.New("transient step 2")
				}
				return nil
			}).
			Build()
		require.NoError(t, err)
		require.NoError(t, ctx.Start())
		t.Cleanup(func() { _ = ctx.Stop() })

		prod, err := ctx.GetProducer("direct:retry-leak")
		require.NoError(t, err)

		var lastEx *Exchange
		for i := 0; i < 20; i++ {
			ex := NewExchange(context.Background())
			require.NoError(t, prod.Send(ex))
			lastEx = ex
		}

		dlc := route.GetErrorHandler().(*DeadLetterChannel)
		dlc.mu.Lock()
		retryLen := len(dlc.retryAttempts)
		syncField := reflect.ValueOf(dlc).Elem().FieldByName("synchronized")
		syncLen := 0
		if syncField.IsValid() {
			syncLen = syncField.Len()
		}
		dlc.mu.Unlock()

		assert.Equal(t, 0, retryLen)
		assert.Equal(t, 0, syncLen, "DeadLetterChannel must not retain synchronized entries across DirectProducer retries")
		assert.LessOrEqual(t, len(lastEx.synchronizations), 1, "repeated failures on the same exchange must not register duplicate synchronizations")
	})

	t.Run("redacts deadLetterURI in property and errors and wraps original error", func(t *testing.T) {
		comp, _ := newMockDLQComponent()
		ctx := NewCamelContext()
		ctx.GetComponentRegistry().RegisterComponent("mockdlq", comp)
		ctx.GetComponentRegistry().RegisterComponent("faildlq", failingSendComponent{})
		origErr := errors.New("original failure")

		// 1. Property redaction on successful DLQ delivery
		secretDLQURI := "mockdlq://admin:s3cr3t@host/errors?password=topsecret&token=tok123"
		dlc := NewDeadLetterChannel(ctx, secretDLQURI)
		ex := NewExchange(context.Background())
		err := dlc.HandleError(ex, origErr)
		assert.ErrorIs(t, err, origErr)
		propVal, ok := ex.GetPropertyAsString("CamelDeadLetterChannel")
		require.True(t, ok)
		assert.NotContains(t, propVal, "s3cr3t")
		assert.NotContains(t, propVal, "topsecret")
		assert.NotContains(t, propVal, "tok123")
		assert.Contains(t, propVal, "xxxxx")

		// 2. Producer acquisition error redaction + %w wrapping
		unknownURI := "missingcomp://admin:s3cr3t@host/errors?password=topsecret"
		err = NewDeadLetterChannel(ctx, unknownURI).HandleError(NewExchange(context.Background()), origErr)
		require.Error(t, err)
		assert.ErrorIs(t, err, origErr)
		assert.NotContains(t, err.Error(), "s3cr3t")
		assert.NotContains(t, err.Error(), "topsecret")
		assert.Contains(t, err.Error(), "xxxxx")

		// 3. DLQ send error redaction + %w wrapping
		failURI := "faildlq://admin:s3cr3t@host/errors?password=topsecret"
		err = NewDeadLetterChannel(ctx, failURI).HandleError(NewExchange(context.Background()), origErr)
		require.Error(t, err)
		assert.ErrorIs(t, err, origErr)
		assert.NotContains(t, err.Error(), "s3cr3t")
		assert.NotContains(t, err.Error(), "topsecret")
		assert.Contains(t, err.Error(), "xxxxx")

		// 4. Cancelled during redelivery backoff wraps original error
		cancelCtx, cancel := context.WithCancel(context.Background())
		cancel()
		abortErr := NewDeadLetterChannel(ctx, "mockdlq:errors",
			MaxRedeliveries(1), RedeliveryDelay(time.Hour),
		).HandleError(NewExchange(cancelCtx), origErr)
		require.Error(t, abortErr)
		assert.ErrorIs(t, abortErr, origErr)
		assert.ErrorIs(t, abortErr, context.Canceled)
	})

	t.Run("clears failed Out and delivers with WithoutCancel on cancelled context", func(t *testing.T) {
		ctx := NewCamelContext()
		var dlqCount atomic.Int32
		var dlqInBody, dlqOutBody any
		var dlqHasBadHeader bool
		var dlqCtxErr error

		_, err := ctx.CreateRouteBuilder().
			From("direct:dlq-ctx").
			ProcessFunc(func(e *Exchange) error {
				if e.Context != nil && e.Context.Err() != nil {
					return e.Context.Err()
				}
				dlqCount.Add(1)
				dlqCtxErr = e.Context.Err()
				dlqInBody = e.GetIn().GetBody()
				dlqOutBody = e.GetOut().GetBody()
				_, dlqHasBadHeader = e.GetIn().GetHeader("BadHeader")
				return nil
			}).
			Build()
		require.NoError(t, err)

		_, err = ctx.CreateRouteBuilder().
			From("direct:main-fail").
			DeadLetterChannel("direct:dlq-ctx", MaxRedeliveries(0)).
			ProcessFunc(func(e *Exchange) error {
				e.GetOut().SetBody("partial-err-out")
				e.GetOut().SetHeader("BadHeader", "bad")
				return errors.New("boom")
			}).
			Build()
		require.NoError(t, err)

		require.NoError(t, ctx.Start())
		t.Cleanup(func() { _ = ctx.Stop() })

		exCtx, cancel := context.WithCancel(context.Background())
		cancel()
		ex := NewExchange(exCtx)
		ex.GetIn().SetBody("original-in")

		prod, err := ctx.GetProducer("direct:main-fail")
		require.NoError(t, err)
		sendErr := prod.Send(ex)
		require.Error(t, sendErr)

		assert.Equal(t, int32(1), dlqCount.Load(), "cancelled exchange context must still deliver to DLQ via WithoutCancel")
		assert.NoError(t, dlqCtxErr)
		assert.Equal(t, "original-in", dlqInBody)
		assert.Nil(t, dlqOutBody, "failed Out must be cleared before DLQ send")
		assert.False(t, dlqHasBadHeader)
		assert.True(t, IsDeadLettered(ex))
		assert.ErrorIs(t, ex.Context.Err(), context.Canceled, "original exchange context must be preserved after DLQ send")
	})
}

func TestDeadLetterChannel_ExponentialBackoffGuardsInvalidMultiplier(t *testing.T) {
	base := 10 * time.Millisecond
	for _, mult := range []float64{0, -2, math.NaN(), math.Inf(1), math.Inf(-1)} {
		d := exponentialBackoffDelay(base, mult, 3)
		assert.Equal(t, base, d, "multiplier %v should fall back to base delay", mult)
	}
	assert.Less(t, exponentialBackoffDelay(base, 0.5, 100), maxRedeliveryDelay)

	ctx := NewCamelContext()
	dlc := NewDeadLetterChannel(ctx, "log:test", ExponentialBackoff(math.NaN()))
	assert.Equal(t, 2.0, dlc.backoffMultiplier)
}

