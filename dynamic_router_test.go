package gocamel

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDynamicRouter_Basic(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())

	var steps []string

	// Control function: decides next step based on a counter in exchange property
	controlFn := func(e *Exchange) (string, error) {
		val, _ := e.GetProperty("step_count")
		count, _ := val.(int)
		e.SetProperty("step_count", count+1)

		switch count {
		case 0:
			return "direct:step1", nil
		case 1:
			return "direct:step2", nil
		default:
			return "", nil // End routing
		}
	}

	_, err := ctx.CreateRouteBuilder().
		From("direct:start").
		DynamicRouter(controlFn).
		Build()
	require.NoError(t, err)

	_, err = ctx.CreateRouteBuilder().
		From("direct:step1").
		ProcessFunc(func(e *Exchange) error {
			steps = append(steps, "step1")
			return nil
		}).
		Build()
	require.NoError(t, err)

	_, err = ctx.CreateRouteBuilder().
		From("direct:step2").
		ProcessFunc(func(e *Exchange) error {
			steps = append(steps, "step2")
			return nil
		}).
		Build()
	require.NoError(t, err)

	require.NoError(t, ctx.Start())
	defer ctx.Stop()

	ex := NewExchange(context.Background())
	p, _ := ctx.CreateEndpoint("direct:start")
	prod, _ := p.CreateProducer()
	prod.Start(context.Background())
	prod.Send(ex)
	prod.Stop()

	assert.Equal(t, []string{"step1", "step2"}, steps)
}

func TestDynamicRouter_Simple(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())

	var received string

	// Use Simple expression to resolve next URI from a header
	// The header "next" is updated in each step.
	_, err := ctx.CreateRouteBuilder().
		From("direct:start").
		DynamicRouter("${header.next}").
		Build()
	require.NoError(t, err)

	_, err = ctx.CreateRouteBuilder().
		From("direct:step1").
		ProcessFunc(func(e *Exchange) error {
			e.GetIn().SetHeader("next", "direct:step2")
			return nil
		}).
		Build()
	require.NoError(t, err)

	_, err = ctx.CreateRouteBuilder().
		From("direct:step2").
		ProcessFunc(func(e *Exchange) error {
			received = "done"
			e.GetIn().RemoveHeader("next") // This will result in empty string -> end
			return nil
		}).
		Build()
	require.NoError(t, err)

	require.NoError(t, ctx.Start())
	defer ctx.Stop()

	ex := NewExchange(context.Background())
	ex.GetIn().SetHeader("next", "direct:step1")

	p, _ := ctx.CreateEndpoint("direct:start")
	prod, _ := p.CreateProducer()
	prod.Start(context.Background())
	prod.Send(ex)
	prod.Stop()

	assert.Equal(t, "done", received)
}
