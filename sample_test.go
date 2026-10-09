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

func TestSampler_Every(t *testing.T) {
	s := NewSamplerEvery(3)

	var passed []int
	for i := 0; i < 9; i++ {
		err := s.Process(NewExchange(context.Background()))
		if err == nil {
			passed = append(passed, i)
			continue
		}
		assert.ErrorIs(t, err, ErrStopRouting)
	}

	assert.Equal(t, []int{0, 3, 6}, passed)
}

func TestSampler_Period(t *testing.T) {
	s := NewSamplerPeriod(100 * time.Millisecond)

	require.NoError(t, s.Process(NewExchange(context.Background())))
	assert.ErrorIs(t, s.Process(NewExchange(context.Background())), ErrStopRouting)
	assert.ErrorIs(t, s.Process(NewExchange(context.Background())), ErrStopRouting)

	assert.Eventually(t, func() bool {
		return s.Process(NewExchange(context.Background())) == nil
	}, 2*time.Second, 10*time.Millisecond)
}

func TestSampler_InvalidArguments(t *testing.T) {
	assert.ErrorContains(t, NewSamplerEvery(0).Validate(), "count")
	assert.ErrorContains(t, NewSamplerEvery(-1).Validate(), "count")
	assert.ErrorContains(t, NewSamplerPeriod(0).Validate(), "period")
	assert.ErrorContains(t, NewSamplerPeriod(-time.Second).Validate(), "period")
	assert.NoError(t, NewSamplerEvery(1).Validate())
	assert.NoError(t, NewSamplerPeriod(time.Second).Validate())

	// Process refuses to run with invalid settings rather than passing everything.
	err := NewSamplerEvery(0).Process(NewExchange(context.Background()))
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrStopRouting)
}

func TestSampler_ConcurrentCount(t *testing.T) {
	s := NewSamplerEvery(10)

	var passed atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s.Process(NewExchange(context.Background())) == nil {
				passed.Add(1)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int64(10), passed.Load())
}

func TestRouteBuilder_SampleEvery(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())

	var received atomic.Int64
	_, err := ctx.CreateRouteBuilder().
		From("direct:sensor").
		SampleEvery(5).
		ProcessFunc(func(*Exchange) error {
			received.Add(1)
			return nil
		}).
		Build()
	require.NoError(t, err)

	require.NoError(t, ctx.Start())
	t.Cleanup(func() { _ = ctx.Stop() })

	endpoint, err := ctx.CreateEndpoint("direct:sensor")
	require.NoError(t, err)
	prod, err := endpoint.CreateProducer()
	require.NoError(t, err)
	require.NoError(t, prod.Start(context.Background()))
	t.Cleanup(func() { _ = prod.Stop() })

	for i := 0; i < 20; i++ {
		ex := NewExchange(context.Background())
		ex.In.SetBody(i)
		_ = prod.Send(ex)
	}

	assert.Equal(t, int64(4), received.Load())
}

func TestRouteBuilder_SampleInvalid(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())

	route, err := ctx.CreateRouteBuilder().
		From("direct:sensor").
		Sample(0).
		Build()
	require.NoError(t, err)
	assert.ErrorContains(t, route.Validate(), "period")
}
