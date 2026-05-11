package gocamel

import (
	"context"
	"errors"
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
