package gocamel

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIdempotentConsumer_Memory(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())

	var received []string
	repo := NewMemoryIdempotentRepository()

	_, err := ctx.CreateRouteBuilder().
		From("direct:start").
		IdempotentConsumer("${header.MessageId}", repo).
		ProcessFunc(func(e *Exchange) error {
			body, _ := e.GetBodyAsString()
			received = append(received, body)
			return nil
		}).
		Build()
	require.NoError(t, err)

	require.NoError(t, ctx.Start())
	defer ctx.Stop()

	send := func(body, messageId string) {
		ex := NewExchange(context.Background())
		ex.In.SetBody(body)
		ex.In.SetHeader("MessageId", messageId)
		p, _ := ctx.CreateEndpoint("direct:start")
		prod, _ := p.CreateProducer()
		prod.Start(context.Background())
		prod.Send(ex)
		prod.Stop()
	}

	send("msg1", "1")
	send("msg2", "2")
	send("msg1-dup", "1") // Should be dropped
	send("msg3", "3")
	send("msg2-dup", "2") // Should be dropped

	assert.Equal(t, []string{"msg1", "msg2", "msg3"}, received)
}

func TestIdempotentConsumer_Func(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())

	var received []string
	repo := NewMemoryIdempotentRepository()

	_, err := ctx.CreateRouteBuilder().
		From("direct:start").
		IdempotentConsumer(func(e *Exchange) (string, error) {
			return e.GetIn().GetBody().(string), nil
		}, repo).
		ProcessFunc(func(e *Exchange) error {
			body, _ := e.GetBodyAsString()
			received = append(received, body)
			return nil
		}).
		Build()
	require.NoError(t, err)

	require.NoError(t, ctx.Start())
	defer ctx.Stop()

	send := func(body string) {
		ex := NewExchange(context.Background())
		ex.In.SetBody(body)
		p, _ := ctx.CreateEndpoint("direct:start")
		prod, _ := p.CreateProducer()
		prod.Start(context.Background())
		prod.Send(ex)
		prod.Stop()
	}

	send("a")
	send("b")
	send("a") // Duplicate body
	send("c")

	assert.Equal(t, []string{"a", "b", "c"}, received)
}
