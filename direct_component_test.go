package gocamel

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDirectComponent(t *testing.T) {
	ctx := context.Background()

	camelCtx := NewCamelContext()
	camelCtx.AddComponent("direct", NewDirectComponent())

	var receivedBody interface{}

	route := camelCtx.CreateRouteBuilder().
		From("direct:start").
		ProcessFunc(func(exchange *Exchange) error {
			receivedBody = exchange.GetIn().GetBody()
			exchange.GetOut().SetBody("Modified: " + receivedBody.(string))
			return nil
		}).
		Build()

	camelCtx.AddRoute(route)
	err := camelCtx.Start()
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

func TestDirectComponentMultipleRoutes(t *testing.T) {
	ctx := context.Background()

	camelCtx := NewCamelContext()
	camelCtx.AddComponent("direct", NewDirectComponent())

	var receivedBody2 interface{}

	// Route 1 (starts from direct:start, goes to direct:next)
	route1 := camelCtx.CreateRouteBuilder().
		From("direct:start").
		ProcessFunc(func(exchange *Exchange) error {
            body := exchange.GetIn().GetBody().(string)
			exchange.GetOut().SetBody(body + " Route1")
			return nil
		}).
		To("direct:next").
		Build()

	// Route 2 (starts from direct:next)
	route2 := camelCtx.CreateRouteBuilder().
		From("direct:next").
		ProcessFunc(func(exchange *Exchange) error {
            body := exchange.GetIn().GetBody().(string)
			receivedBody2 = body + " Route2"
			exchange.GetOut().SetBody(receivedBody2)
			return nil
		}).
		Build()

	camelCtx.AddRoute(route1)
	camelCtx.AddRoute(route2)
	err := camelCtx.Start()
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
