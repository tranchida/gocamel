package gocamel

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToD(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())

	receivedBody := ""
	ctx.CreateRouteBuilder().
		From("direct:start").
		ToD("direct:${header.target}").
		Build()

	ctx.CreateRouteBuilder().
		From("direct:a").
		ProcessFunc(func(exchange *Exchange) error {
			receivedBody = exchange.GetIn().GetBody().(string)
			return nil
		}).
		Build()

	ctx.CreateRouteBuilder().
		From("direct:b").
		ProcessFunc(func(exchange *Exchange) error {
			receivedBody = exchange.GetIn().GetBody().(string)
			return nil
		}).
		Build()

	err := ctx.Start()
	require.NoError(t, err)
	defer ctx.Stop()

	// Test 1: Send to 'a'
	exchange1 := NewExchange(context.Background())
	exchange1.In.SetBody("Hello A")
	exchange1.In.SetHeader("target", "a")
	
	endpoint, err := ctx.CreateEndpoint("direct:start")
	require.NoError(t, err)
	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)
	
	err = producer.Start(context.Background())
	require.NoError(t, err)
	
	err = producer.Send(exchange1)
	assert.NoError(t, err)
	assert.Equal(t, "Hello A", receivedBody)

	// Test 2: Send to 'b'
	exchange2 := NewExchange(context.Background())
	exchange2.In.SetBody("Hello B")
	exchange2.In.SetHeader("target", "b")
	
	err = producer.Send(exchange2)
	assert.NoError(t, err)
	assert.Equal(t, "Hello B", receivedBody)
}

func TestToDWithPropertyAndBody(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())

	receivedURI := ""
	ctx.CreateRouteBuilder().
		From("direct:start").
		ToD("direct:${property.prefix}-${body}").
		Build()

	// Intercepter toutes les routes direct pour voir laquelle est appelée
	ctx.CreateRouteBuilder().
		From("direct:msg-hello").
		ProcessFunc(func(exchange *Exchange) error {
			receivedURI = "direct:msg-hello"
			return nil
		}).
		Build()

	err := ctx.Start()
	require.NoError(t, err)
	defer ctx.Stop()

	exchange := NewExchange(context.Background())
	exchange.In.SetBody("hello")
	exchange.SetProperty("prefix", "msg")
	
	endpoint, err := ctx.CreateEndpoint("direct:start")
	require.NoError(t, err)
	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)
	
	err = producer.Start(context.Background())
	require.NoError(t, err)
	
	err = producer.Send(exchange)
	assert.NoError(t, err)
	assert.Equal(t, "direct:msg-hello", receivedURI)
}

func TestToDInSplit(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())

	var receivedBodies []string
	ctx.CreateRouteBuilder().
		From("direct:start").
		Split(func(e *Exchange) (any, error) {
			return []string{"a", "b"}, nil
		}).
		ToD("direct:${body}").
		End()

	ctx.CreateRouteBuilder().
		From("direct:a").
		ProcessFunc(func(exchange *Exchange) error {
			receivedBodies = append(receivedBodies, "received-a")
			return nil
		}).
		Build()

	ctx.CreateRouteBuilder().
		From("direct:b").
		ProcessFunc(func(exchange *Exchange) error {
			receivedBodies = append(receivedBodies, "received-b")
			return nil
		}).
		Build()

	err := ctx.Start()
	require.NoError(t, err)
	defer ctx.Stop()

	exchange := NewExchange(context.Background())
	exchange.In.SetBody("start")
	
	endpoint, err := ctx.CreateEndpoint("direct:start")
	require.NoError(t, err)
	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)
	
	err = producer.Start(context.Background())
	require.NoError(t, err)
	
	err = producer.Send(exchange)
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{"received-a", "received-b"}, receivedBodies)
}
