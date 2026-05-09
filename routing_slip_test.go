package gocamel

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRoutingSlip_StaticURIsSequentiallyPropagatesBody(t *testing.T) {
	camel := NewCamelContext()
	camel.AddComponent("direct", NewDirectComponent())

	_, err := camel.CreateRouteBuilder().
		From("direct:a").
		ProcessFunc(func(e *Exchange) error {
			body, _ := e.GetBodyAsString()
			e.GetOut().SetBody(body + "-a")
			return nil
		}).
		Build()
	require.NoError(t, err)

	_, err = camel.CreateRouteBuilder().
		From("direct:b").
		ProcessFunc(func(e *Exchange) error {
			body, _ := e.GetBodyAsString()
			e.GetOut().SetBody(body + "-b")
			return nil
		}).
		Build()
	require.NoError(t, err)

	var got string
	route, err := camel.CreateRouteBuilder().
		From("direct:start").
		RoutingSlip("direct:a,direct:b").
		ProcessFunc(func(e *Exchange) error {
			got, _ = e.GetBodyAsString()
			return nil
		}).
		Build()
	require.NoError(t, err)

	require.NoError(t, camel.Start())
	defer camel.Stop()

	exchange := NewExchange(context.Background())
	exchange.SetBody("start")
	require.NoError(t, route.Process(exchange))

	assert.Equal(t, "start-a-b", got)
}

func TestRoutingSlip_ClearsOutBetweenSteps(t *testing.T) {
	camel := NewCamelContext()
	camel.AddComponent("direct", NewDirectComponent())

	_, err := camel.CreateRouteBuilder().
		From("direct:a").
		ProcessFunc(func(e *Exchange) error {
			e.GetOut().SetBody("from-out")
			return nil
		}).
		Build()
	require.NoError(t, err)

	_, err = camel.CreateRouteBuilder().
		From("direct:b").
		ProcessFunc(func(e *Exchange) error {
			body, _ := e.GetBodyAsString()
			e.GetIn().SetBody(body + "-mutated-in")
			return nil
		}).
		Build()
	require.NoError(t, err)

	route, err := camel.CreateRouteBuilder().
		From("direct:start").
		RoutingSlip("direct:a,direct:b").
		Build()
	require.NoError(t, err)

	require.NoError(t, camel.Start())
	defer camel.Stop()

	exchange := NewExchange(context.Background())
	require.NoError(t, route.Process(exchange))

	assert.Equal(t, "from-out-mutated-in", exchange.GetIn().GetBody())
	assert.False(t, exchange.HasOut(), "routing slip should not leave a stale Out message")
}

func TestRoutingSlip_DynamicViaHeader(t *testing.T) {
	camel := NewCamelContext()
	camel.AddComponent("direct", NewDirectComponent())

	var calls []string
	for _, name := range []string{"x", "y"} {
		targetName := name
		_, err := camel.CreateRouteBuilder().
			From("direct:" + targetName).
			ProcessFunc(func(e *Exchange) error {
				calls = append(calls, targetName)
				return nil
			}).
			Build()
		require.NoError(t, err)
	}

	route, err := camel.CreateRouteBuilder().
		From("direct:start").
		ProcessFunc(func(e *Exchange) error {
			e.GetIn().SetHeader("slip", "direct:x,direct:y")
			return nil
		}).
		RoutingSlip("${header.slip}").
		Build()
	require.NoError(t, err)

	require.NoError(t, camel.Start())
	defer camel.Stop()

	require.NoError(t, route.Process(NewExchange(context.Background())))
	assert.Equal(t, []string{"x", "y"}, calls)
}

func TestRoutingSlip_GoFunction(t *testing.T) {
	camel := NewCamelContext()
	camel.AddComponent("direct", NewDirectComponent())

	var calls []string
	for _, name := range []string{"p", "q"} {
		targetName := name
		_, err := camel.CreateRouteBuilder().
			From("direct:" + targetName).
			ProcessFunc(func(e *Exchange) error {
				calls = append(calls, targetName)
				return nil
			}).
			Build()
		require.NoError(t, err)
	}

	route, err := camel.CreateRouteBuilder().
		From("direct:start").
		RoutingSlip(func(e *Exchange) ([]string, error) {
			return []string{"direct:p", "direct:q"}, nil
		}).
		Build()
	require.NoError(t, err)

	require.NoError(t, camel.Start())
	defer camel.Stop()

	require.NoError(t, route.Process(NewExchange(context.Background())))
	assert.Equal(t, []string{"p", "q"}, calls)
}

func TestRoutingSlip_Properties(t *testing.T) {
	camel := NewCamelContext()
	camel.AddComponent("direct", NewDirectComponent())

	var indices []int
	var sizes []int
	var completes []bool
	for _, name := range []string{"a", "b"} {
		_, err := camel.CreateRouteBuilder().
			From("direct:" + name).
			ProcessFunc(func(e *Exchange) error {
				index, _ := e.GetPropertyAsInt(CamelRoutingSlipIndex)
				size, _ := e.GetPropertyAsInt(CamelRoutingSlipSize)
				complete, _ := e.GetPropertyAsBool(CamelRoutingSlipComplete)
				indices = append(indices, index)
				sizes = append(sizes, size)
				completes = append(completes, complete)
				return nil
			}).
			Build()
		require.NoError(t, err)
	}

	route, err := camel.CreateRouteBuilder().
		From("direct:start").
		RoutingSlip("direct:a,direct:b").
		Build()
	require.NoError(t, err)

	require.NoError(t, camel.Start())
	defer camel.Stop()

	require.NoError(t, route.Process(NewExchange(context.Background())))

	assert.Equal(t, []int{0, 1}, indices)
	assert.Equal(t, []int{2, 2}, sizes)
	assert.Equal(t, []bool{false, true}, completes)
}

func TestRoutingSlip_URIFunctionError(t *testing.T) {
	rs := NewRoutingSlip(NewCamelContext()).SetURIFunc(func(e *Exchange) ([]string, error) {
		return nil, errors.New("boom")
	})

	err := rs.Process(NewExchange(context.Background()))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "routing slip URI function error")
}

func TestRouteBuilder_RoutingSlipUnsupportedType(t *testing.T) {
	camel := NewCamelContext()
	camel.AddComponent("direct", NewDirectComponent())

	route, err := camel.CreateRouteBuilder().
		From("direct:start").
		RoutingSlip(42).
		Build()

	assert.Nil(t, route)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported RoutingSlip expression type")
}
