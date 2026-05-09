package gocamel

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMessageFilter_PassThrough(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())

	var received []string
	route, err := ctx.CreateRouteBuilder().
		From("direct:start").
		Filter(func(e *Exchange) (bool, error) {
			body, _ := e.GetBodyAsString()
			return body == "keep", nil
		}).
		ProcessFunc(func(e *Exchange) error {
			body, _ := e.GetBodyAsString()
			received = append(received, body)
			return nil
		}).
		Build()
	require.NoError(t, err)
	_ = route

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

	send("keep")
	send("drop")
	send("keep")

	assert.Equal(t, []string{"keep", "keep"}, received)
}

func TestMessageFilter_SimpleExpression(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())

	var received []string
	// Comparisons must be inside ${...} for Simple Language evaluation.
	_, err := ctx.CreateRouteBuilder().
		From("direct:in").
		Filter("${header.type == 'order'}").
		ProcessFunc(func(e *Exchange) error {
			body, _ := e.GetBodyAsString()
			received = append(received, body)
			return nil
		}).
		Build()
	require.NoError(t, err)

	require.NoError(t, ctx.Start())
	defer ctx.Stop()

	send := func(body, headerType string) {
		ex := NewExchange(context.Background())
		ex.In.SetBody(body)
		ex.In.SetHeader("type", headerType)
		p, _ := ctx.CreateEndpoint("direct:in")
		prod, _ := p.CreateProducer()
		prod.Start(context.Background())
		prod.Send(ex)
		prod.Stop()
	}

	send("order1", "order")
	send("notif1", "notification")
	send("order2", "order")

	assert.Equal(t, []string{"order1", "order2"}, received)
}

func TestMessageFilter_PredicateError(t *testing.T) {
	f := NewMessageFilter(func(e *Exchange) (bool, error) {
		return false, errors.New("predicate boom")
	})
	ex := NewExchange(context.Background())
	err := f.Process(ex)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "predicate boom")
}

func TestMessageFilter_MissingHeader(t *testing.T) {
	// Missing header evaluates to nil/empty — comparison returns false, not an error.
	f, err := NewSimpleMessageFilter("${header.missing == 'x'}")
	require.NoError(t, err)
	ex := NewExchange(context.Background())
	err = f.Process(ex)
	// Missing header → false → ErrStopRouting (exchange dropped, no failure)
	assert.ErrorIs(t, err, ErrStopRouting)
}

func TestRouteBuilder_Filter_UnsupportedType(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())
	_, err := ctx.CreateRouteBuilder().
		From("direct:x").
		Filter(42). // unsupported type
		Build()
	require.Error(t, err)
}
