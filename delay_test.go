package gocamel

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDelayer_FixedDelay(t *testing.T) {
	d := NewDelayer(50 * time.Millisecond)
	ex := NewExchange(context.Background())

	start := time.Now()
	require.NoError(t, d.Process(ex))
	assert.GreaterOrEqual(t, time.Since(start), 40*time.Millisecond)
}

func TestDelayer_ZeroDelay(t *testing.T) {
	d := NewDelayer(0)
	ex := NewExchange(context.Background())
	start := time.Now()
	require.NoError(t, d.Process(ex))
	assert.Less(t, time.Since(start), 10*time.Millisecond)
}

func TestDelayer_ContextCancelled(t *testing.T) {
	d := NewDelayer(5 * time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	ex := NewExchange(ctx)

	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := d.Process(ex)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "context cancelled")
	assert.Less(t, time.Since(start), 500*time.Millisecond)
}

func TestDelayerFunc_Dynamic(t *testing.T) {
	d := NewDelayerFunc(func(e *Exchange) (time.Duration, error) {
		ms, ok := e.GetHeaderAsInt("X-Delay-Ms")
		if !ok {
			return 0, nil
		}
		return time.Duration(ms) * time.Millisecond, nil
	})

	ex := NewExchange(context.Background())
	ex.In.SetHeader("X-Delay-Ms", 60)

	start := time.Now()
	require.NoError(t, d.Process(ex))
	assert.GreaterOrEqual(t, time.Since(start), 50*time.Millisecond)
}

func TestDelayerFunc_NilReturnsError(t *testing.T) {
	d := NewDelayerFunc(nil)
	ex := NewExchange(context.Background())

	err := d.Process(ex)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "delay function is nil")
}

func TestRouteBuilder_DelayFuncNilBuildError(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())

	route, err := ctx.CreateRouteBuilder().
		From("direct:delayed").
		DelayFunc(nil).
		Build()

	assert.Nil(t, route)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DelayFunc")
}

func TestRouteBuilder_Delay(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())

	var elapsed time.Duration
	var start time.Time

	_, err := ctx.CreateRouteBuilder().
		From("direct:delayed").
		ProcessFunc(func(e *Exchange) error {
			start = time.Now()
			return nil
		}).
		Delay(60 * time.Millisecond).
		ProcessFunc(func(e *Exchange) error {
			elapsed = time.Since(start)
			return nil
		}).
		Build()
	require.NoError(t, err)

	require.NoError(t, ctx.Start())
	defer ctx.Stop()

	ex := NewExchange(context.Background())
	p, _ := ctx.CreateEndpoint("direct:delayed")
	prod, _ := p.CreateProducer()
	prod.Start(context.Background())
	prod.Send(ex)
	prod.Stop()

	assert.GreaterOrEqual(t, elapsed, 50*time.Millisecond)
}
