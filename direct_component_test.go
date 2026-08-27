package gocamel

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDirectComponent(t *testing.T) {
	ctx := context.Background()

	camelCtx := NewCamelContext()
	camelCtx.AddComponent("direct", NewDirectComponent())

	var receivedBody interface{}

	route, err := camelCtx.CreateRouteBuilder().
		From("direct:start").
		ProcessFunc(func(exchange *Exchange) error {
			receivedBody = exchange.GetIn().GetBody()
			exchange.GetOut().SetBody("Modified: " + receivedBody.(string))
			return nil
		}).
		Build()
	assert.NoError(t, err)

	camelCtx.AddRoute(route)
	err = camelCtx.Start()
	assert.NoError(t, err)

	endpoint, err := camelCtx.CreateEndpoint("direct:start")
	assert.NoError(t, err)

	producer, err := endpoint.CreateProducer()
	assert.NoError(t, err)

	exchange := NewExchange(ctx)
	exchange.GetIn().SetBody("Hello Direct")

	err = producer.Send(exchange)
	assert.NoError(t, err)

	assert.Equal(t, "Hello Direct", receivedBody)
	assert.Equal(t, "Modified: Hello Direct", exchange.GetOut().GetBody())

	camelCtx.Stop()
}

type lifecycleCountingComponent struct {
	created atomic.Int32
	stopped atomic.Int32
}

func (c *lifecycleCountingComponent) CreateEndpoint(uri string) (Endpoint, error) {
	return &lifecycleCountingEndpoint{uri: uri, component: c}, nil
}

type lifecycleCountingEndpoint struct {
	uri       string
	component *lifecycleCountingComponent
}

func (e *lifecycleCountingEndpoint) URI() string { return e.uri }
func (e *lifecycleCountingEndpoint) CreateProducer() (Producer, error) {
	e.component.created.Add(1)
	return &lifecycleCountingProducer{component: e.component}, nil
}
func (e *lifecycleCountingEndpoint) CreateConsumer(Processor) (Consumer, error) {
	return nil, fmt.Errorf("producer-only endpoint")
}

type lifecycleCountingProducer struct {
	component *lifecycleCountingComponent
}

func (p *lifecycleCountingProducer) Start(context.Context) error { return nil }
func (p *lifecycleCountingProducer) Stop() error {
	p.component.stopped.Add(1)
	return nil
}
func (p *lifecycleCountingProducer) Send(*Exchange) error { return nil }

func TestRoute_StopDisconnectsDirectAndRestartRecreatesLazyProducer(t *testing.T) {
	camelCtx := NewCamelContext()
	camelCtx.AddComponent("direct", NewDirectComponent())
	counting := &lifecycleCountingComponent{}
	camelCtx.AddComponent("counting", counting)

	route, err := camelCtx.CreateRouteBuilder().
		From("direct:lifecycle").
		To("counting:out").
		Build()
	assert.NoError(t, err)
	assert.NoError(t, route.Start(context.Background()))

	endpoint, err := camelCtx.CreateEndpoint("direct:lifecycle")
	assert.NoError(t, err)
	producer, err := endpoint.CreateProducer()
	assert.NoError(t, err)

	assert.NoError(t, producer.Send(NewExchange(context.Background())))
	assert.EqualValues(t, 1, counting.created.Load())

	assert.NoError(t, route.Stop())
	assert.Error(t, producer.Send(NewExchange(context.Background())))
	assert.EqualValues(t, 1, counting.stopped.Load())

	assert.NoError(t, route.Start(context.Background()))
	assert.NoError(t, producer.Send(NewExchange(context.Background())))
	assert.EqualValues(t, 2, counting.created.Load())
	assert.NoError(t, route.Stop())
	assert.EqualValues(t, 2, counting.stopped.Load())
}

func TestDirectComponentMultipleRoutes(t *testing.T) {
	ctx := context.Background()

	camelCtx := NewCamelContext()
	camelCtx.AddComponent("direct", NewDirectComponent())

	var receivedBody2 interface{}

	// Route 1 (starts from direct:start, goes to direct:next)
	route1, err := camelCtx.CreateRouteBuilder().
		From("direct:start").
		ProcessFunc(func(exchange *Exchange) error {
			body := exchange.GetIn().GetBody().(string)
			exchange.GetOut().SetBody(body + " Route1")
			return nil
		}).
		To("direct:next").
		Build()
	assert.NoError(t, err)

	// Route 2 (starts from direct:next)
	route2, err := camelCtx.CreateRouteBuilder().
		From("direct:next").
		ProcessFunc(func(exchange *Exchange) error {
			body := exchange.GetIn().GetBody().(string)
			receivedBody2 = body + " Route2"
			exchange.GetOut().SetBody(receivedBody2)
			return nil
		}).
		Build()
	assert.NoError(t, err)

	camelCtx.AddRoute(route1)
	camelCtx.AddRoute(route2)
	err = camelCtx.Start()
	assert.NoError(t, err)

	endpoint, err := camelCtx.CreateEndpoint("direct:start")
	assert.NoError(t, err)

	producer, err := endpoint.CreateProducer()
	assert.NoError(t, err)

	exchange := NewExchange(ctx)
	exchange.GetIn().SetBody("Start")

	err = producer.Send(exchange)
	assert.NoError(t, err)

	assert.Equal(t, "Start Route1 Route2", receivedBody2)
	assert.Equal(t, "Start Route1 Route2", exchange.GetOut().GetBody())

	camelCtx.Stop()
}

func TestDirectProducer_RecoversProcessorPanic(t *testing.T) {
	camelCtx := NewCamelContext()
	camelCtx.AddComponent("direct", NewDirectComponent())

	_, err := camelCtx.CreateRouteBuilder().
		From("direct:panic").
		ProcessFunc(func(*Exchange) error { panic("boom") }).
		Build()
	require.NoError(t, err)
	require.NoError(t, camelCtx.Start())
	t.Cleanup(func() { _ = camelCtx.Stop() })

	ep, err := camelCtx.CreateEndpoint("direct:panic")
	require.NoError(t, err)
	prod, err := ep.CreateProducer()
	require.NoError(t, err)

	err = prod.Send(NewExchange(context.Background()))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrProcessorPanic)
}
