package gocamel

import (
	"context"
	"sync"
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

func TestLoadBalancer_ValidatorNilChecksAndConcurrentChildProcessors(t *testing.T) {
	t.Run("implements Validator and guards nil strategy/processors", func(t *testing.T) {
		var typedNilStrategy LoadBalancerStrategy = (*RoundRobinStrategy)(nil)
		var typedNilProc Processor = ProcessorFunc(nil)

		empty := NewLoadBalancerProcessor(&RoundRobinStrategy{})
		v, ok := any(empty).(Validator)
		require.True(t, ok, "LoadBalancerProcessor must implement Validator")
		require.Error(t, v.Validate())

		nilStrat := NewLoadBalancerProcessor(nil)
		nilStrat.AddProcessor(ProcessorFunc(func(*Exchange) error { return nil }))
		require.Error(t, any(nilStrat).(Validator).Validate())
		require.NotPanics(t, func() {
			require.Error(t, nilStrat.Process(NewExchange(context.Background())))
		})

		typedNilStrat := NewLoadBalancerProcessor(typedNilStrategy)
		typedNilStrat.AddProcessor(ProcessorFunc(func(*Exchange) error { return nil }))
		require.Error(t, any(typedNilStrat).(Validator).Validate())
		require.NotPanics(t, func() {
			require.Error(t, typedNilStrat.Process(NewExchange(context.Background())))
		})

		withNilProc := NewLoadBalancerProcessor(&RoundRobinStrategy{})
		withNilProc.AddProcessor(typedNilProc)
		require.Error(t, any(withNilProc).(Validator).Validate())
		require.NotPanics(t, func() {
			require.Error(t, withNilProc.Process(NewExchange(context.Background())))
		})
	})

	t.Run("concurrent AddProcessor, childProcessors, and Process under race", func(t *testing.T) {
		lb := NewLoadBalancerProcessor(&RoundRobinStrategy{})
		lb.AddProcessor(ProcessorFunc(func(*Exchange) error { return nil }))

		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Add(3)
			go func() {
				defer wg.Done()
				for j := 0; j < 25; j++ {
					lb.AddProcessor(ProcessorFunc(func(*Exchange) error { return nil }))
				}
			}()
			go func() {
				defer wg.Done()
				for j := 0; j < 25; j++ {
					_ = lb.childProcessors()
				}
			}()
			go func() {
				defer wg.Done()
				for j := 0; j < 25; j++ {
					_ = lb.Process(NewExchange(context.Background()))
				}
			}()
		}
		wg.Wait()
	})
}
