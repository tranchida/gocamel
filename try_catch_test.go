package gocamel

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTryCatchFinally_Success(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())

	var tryCalled, catchCalled, finallyCalled bool

	_, err := ctx.CreateRouteBuilder().
		From("direct:start").
		DoTry().
		ProcessFunc(func(e *Exchange) error {
			tryCalled = true
			return nil
		}).
		DoCatch("").
		ProcessFunc(func(e *Exchange) error {
			catchCalled = true
			return nil
		}).
		DoFinally().
		ProcessFunc(func(e *Exchange) error {
			finallyCalled = true
			return nil
		}).
		End().
		Build()
	require.NoError(t, err)

	require.NoError(t, ctx.Start())
	defer ctx.Stop()

	ex := NewExchange(context.Background())
	p, _ := ctx.CreateEndpoint("direct:start")
	prod, _ := p.CreateProducer()
	prod.Start(context.Background())
	prod.Send(ex)
	prod.Stop()

	assert.True(t, tryCalled)
	assert.False(t, catchCalled)
	assert.True(t, finallyCalled)
}

func TestTryCatchFinally_Catch(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())

	var tryCalled, catchCalled, finallyCalled bool
	var caughtError error

	_, err := ctx.CreateRouteBuilder().
		From("direct:start").
		DoTry().
		ProcessFunc(func(e *Exchange) error {
			tryCalled = true
			return errors.New("boom")
		}).
		DoCatch("").
		ProcessFunc(func(e *Exchange) error {
			catchCalled = true
			val, _ := e.GetProperty(CamelExceptionCaught)
			caughtError = val.(error)
			return nil
		}).
		DoFinally().
		ProcessFunc(func(e *Exchange) error {
			finallyCalled = true
			return nil
		}).
		End().
		Build()
	require.NoError(t, err)

	require.NoError(t, ctx.Start())
	defer ctx.Stop()

	ex := NewExchange(context.Background())
	p, _ := ctx.CreateEndpoint("direct:start")
	prod, _ := p.CreateProducer()
	prod.Start(context.Background())
	err = prod.Send(ex)
	prod.Stop()

	// Error should be nil because it was caught
	assert.NoError(t, err)
	assert.True(t, tryCalled)
	assert.True(t, catchCalled)
	assert.True(t, finallyCalled)
	assert.Equal(t, "boom", caughtError.Error())
}

func TestTryCatchFinally_Nested(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())

	var finally1, finally2 bool

	// We use a separate RouteBuilder for the inner part to avoid chaining issues with our current DSL
	// In a real scenario, this is how users might handle complex nesting if the DSL is limited.
	_, err := ctx.CreateRouteBuilder().
		From("direct:start").
		DoTry().
		ProcessFunc(func(e *Exchange) error {
			// Inner try block
			inner := NewTryProcessor()
			inner.SetTryBlock(ProcessorFunc(func(ex *Exchange) error {
				return errors.New("inner")
			}))
			inner.SetFinallyBlock(ProcessorFunc(func(ex *Exchange) error {
				finally1 = true
				return nil
			}))
			return inner.Process(e)
		}).
		DoCatch("inner").
		ProcessFunc(func(e *Exchange) error {
			return nil
		}).
		DoFinally().
		ProcessFunc(func(e *Exchange) error {
			finally2 = true
			return nil
		}).
		End().
		Build()
	require.NoError(t, err)

	require.NoError(t, ctx.Start())
	defer ctx.Stop()

	ex := NewExchange(context.Background())
	p, _ := ctx.CreateEndpoint("direct:start")
	prod, _ := p.CreateProducer()
	prod.Start(context.Background())
	err = prod.Send(ex)
	prod.Stop()

	assert.NoError(t, err)
	assert.True(t, finally1)
	assert.True(t, finally2)
}

func TestTryCatch_IgnoresStopRouting(t *testing.T) {
	ctx := NewCamelContext()
	caught := false
	finallyExecuted := false

	_, err := ctx.CreateRouteBuilder().
		From("direct:stop-test").
		DoTry().
		ProcessFunc(func(e *Exchange) error {
			return ErrStopRouting
		}).
		DoCatch("").
		ProcessFunc(func(e *Exchange) error {
			caught = true
			return nil
		}).
		DoFinally().
		ProcessFunc(func(e *Exchange) error {
			finallyExecuted = true
			return nil
		}).
		End().
		Build()
	require.NoError(t, err)
	require.NoError(t, ctx.Start())
	defer ctx.Stop()

	prod, err := ctx.GetProducer("direct:stop-test")
	require.NoError(t, err)

	ex := NewExchange(context.Background())
	err = prod.Send(ex)

	// The stop ends the called route without failing the direct: caller.
	assert.NoError(t, err)
	assert.False(t, caught, "ErrStopRouting should not be intercepted by catch")
	assert.True(t, finallyExecuted, "finally block should execute on ErrStopRouting")
}

func TestTryCatch_PanicHandled(t *testing.T) {
	ctx := NewCamelContext()
	caught := false
	finallyExecuted := false

	_, err := ctx.CreateRouteBuilder().
		From("direct:panic-test").
		DoTry().
		ProcessFunc(func(e *Exchange) error {
			panic("something went terribly wrong")
		}).
		DoCatch("").
		ProcessFunc(func(e *Exchange) error {
			caught = true
			return nil
		}).
		DoFinally().
		ProcessFunc(func(e *Exchange) error {
			finallyExecuted = true
			return nil
		}).
		End().
		Build()
	require.NoError(t, err)
	require.NoError(t, ctx.Start())
	defer ctx.Stop()

	prod, err := ctx.GetProducer("direct:panic-test")
	require.NoError(t, err)

	ex := NewExchange(context.Background())
	err = prod.Send(ex)

	assert.NoError(t, err)
	assert.True(t, caught, "panic in try block should be caught by catch clause")
	assert.True(t, finallyExecuted, "finally block should execute after caught panic")
}

func TestTryCatch_ClearsOutAndFinallyDoesNotSwallowError(t *testing.T) {
	t.Run("clears Out before DoCatch", func(t *testing.T) {
		ctx := NewCamelContext()
		var catchInBody, catchOutBody any
		var hasLeakedHeader bool

		_, err := ctx.CreateRouteBuilder().
			From("direct:catch-target").
			ProcessFunc(func(e *Exchange) error {
				catchInBody = e.GetIn().GetBody()
				catchOutBody = e.GetOut().GetBody()
				_, hasLeakedHeader = e.GetIn().GetHeader("X-Partial")
				return nil
			}).
			Build()
		require.NoError(t, err)

		_, err = ctx.CreateRouteBuilder().
			From("direct:try-clear-out").
			DoTry().
			ProcessFunc(func(e *Exchange) error {
				e.GetOut().SetBody("partial-garbage")
				e.GetOut().SetHeader("X-Partial", "bad")
				return errors.New("try boom")
			}).
			DoCatch("try boom").
			To("direct:catch-target").
			End().
			Build()
		require.NoError(t, err)
		require.NoError(t, ctx.Start())
		t.Cleanup(func() { _ = ctx.Stop() })

		ex := NewExchange(context.Background())
		ex.GetIn().SetBody("original")
		prod, err := ctx.GetProducer("direct:try-clear-out")
		require.NoError(t, err)
		require.NoError(t, prod.Send(ex))

		assert.Equal(t, "original", catchInBody)
		assert.Nil(t, catchOutBody)
		assert.False(t, hasLeakedHeader)
	})

	t.Run("nil catch clause processor does not panic", func(t *testing.T) {
		tp := NewTryProcessor()
		tp.SetTryBlock(ProcessorFunc(func(*Exchange) error { return errors.New("boom") }))
		tp.AddCatchClause("boom", nil)

		err := tp.Process(NewExchange(context.Background()))
		assert.NoError(t, err)
	})

	t.Run("finally ErrStopRouting does not swallow real error", func(t *testing.T) {
		tryErr := errors.New("uncaught try error")
		tp := NewTryProcessor()
		tp.SetTryBlock(ProcessorFunc(func(*Exchange) error { return tryErr }))
		tp.SetFinallyBlock(ProcessorFunc(func(*Exchange) error { return ErrStopRouting }))

		err := tp.Process(NewExchange(context.Background()))
		require.Error(t, err)
		assert.ErrorIs(t, err, tryErr)
		assert.NotErrorIs(t, err, ErrStopRouting)
	})

	t.Run("finally real error joins with try or catch error", func(t *testing.T) {
		tryErr := errors.New("uncaught try error")
		finErr := errors.New("finally error")
		tp := NewTryProcessor()
		tp.SetTryBlock(ProcessorFunc(func(*Exchange) error { return tryErr }))
		tp.SetFinallyBlock(ProcessorFunc(func(*Exchange) error { return finErr }))

		err := tp.Process(NewExchange(context.Background()))
		require.Error(t, err)
		assert.ErrorIs(t, err, tryErr)
		assert.ErrorIs(t, err, finErr)
	})

	t.Run("nested TryDefinition DoTry DSL exits cleanly to parent route builder", func(t *testing.T) {
		ctx := NewCamelContext()
		var order []string

		route, err := ctx.CreateRouteBuilder().
			From("direct:nested-try").
			DoTry().
			ProcessFunc(func(*Exchange) error {
				order = append(order, "outer-before")
				return nil
			}).
			DoTry().
			ProcessFunc(func(*Exchange) error {
				order = append(order, "inner-try")
				return errors.New("inner-err")
			}).
			DoCatch("inner-err").
			ProcessFunc(func(*Exchange) error {
				order = append(order, "inner-catch")
				return nil
			}).
			End().
			ProcessFunc(func(*Exchange) error {
				order = append(order, "after-outer")
				return nil
			}).
			Build()
		require.NoError(t, err)
		assert.Len(t, route.processors, 2, "after-outer must be on the main route outside outer DoTry")

		require.NoError(t, ctx.Start())
		t.Cleanup(func() { _ = ctx.Stop() })
		prod, err := ctx.GetProducer("direct:nested-try")
		require.NoError(t, err)
		require.NoError(t, prod.Send(NewExchange(context.Background())))
		assert.Equal(t, []string{"outer-before", "inner-try", "inner-catch", "after-outer"}, order)

		// Invalid nested configuration must propagate to Build() and detach the route.
		ctxBad := NewCamelContext()
		_, err = ctxBad.CreateRouteBuilder().
			From("direct:nested-bad").
			DoTry().
			DoTry().
			ProcessFunc(nil).
			End().
			Build()
		assert.Error(t, err)
		assert.Empty(t, ctxBad.GetRoutes())
	})
}

