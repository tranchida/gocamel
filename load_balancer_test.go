package gocamel

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadBalancer_RoundRobin(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())

	var callsA, callsB int

	_, err := ctx.CreateRouteBuilder().
		From("direct:start").
		LoadBalance().RoundRobin().
		To("direct:a").
		To("direct:b").
		End().
		Build()
	require.NoError(t, err)

	_, err = ctx.CreateRouteBuilder().
		From("direct:a").
		ProcessFunc(func(e *Exchange) error {
			callsA++
			return nil
		}).
		Build()
	require.NoError(t, err)

	_, err = ctx.CreateRouteBuilder().
		From("direct:b").
		ProcessFunc(func(e *Exchange) error {
			callsB++
			return nil
		}).
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

	for i := 0; i < 5; i++ {
		send()
	}

	// 5 calls: A, B, A, B, A
	assert.Equal(t, 3, callsA)
	assert.Equal(t, 2, callsB)
}

func TestLoadBalancer_Random(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())

	var callsA, callsB int

	_, err := ctx.CreateRouteBuilder().
		From("direct:start").
		LoadBalance().Random().
		To("direct:a").
		To("direct:b").
		End().
		Build()
	require.NoError(t, err)

	_, err = ctx.CreateRouteBuilder().
		From("direct:a").
		ProcessFunc(func(e *Exchange) error {
			callsA++
			return nil
		}).
		Build()
	require.NoError(t, err)

	_, err = ctx.CreateRouteBuilder().
		From("direct:b").
		ProcessFunc(func(e *Exchange) error {
			callsB++
			return nil
		}).
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

	for i := 0; i < 20; i++ {
		send()
	}

	assert.True(t, callsA > 0, "A should have been called")
	assert.True(t, callsB > 0, "B should have been called")
	assert.Equal(t, 20, callsA+callsB)
}
