package gocamel

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRemoveHeader(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())

	route := NewRouteBuilder(ctx).
		From("direct:start").
		RemoveHeader("ToBeRemoved").
		Build()

	exchange := NewExchange(context.Background())
	exchange.GetIn().SetHeader("ToBeRemoved", "value")
	exchange.GetIn().SetHeader("ToKeep", "value")

	err := route.Process(exchange)
	assert.NoError(t, err)

	assert.False(t, exchange.GetIn().HasHeader("ToBeRemoved"))
	assert.True(t, exchange.GetIn().HasHeader("ToKeep"))
}

func TestRemoveHeadersWildcard(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())

	route := NewRouteBuilder(ctx).
		From("direct:wildcard").
		RemoveHeaders("Camel*").
		Build()

	exchange := NewExchange(context.Background())
	exchange.GetIn().SetHeader("CamelFileName", "test.txt")
	exchange.GetIn().SetHeader("CamelHttpUrl", "http://localhost")
	exchange.GetIn().SetHeader("UserHeader", "value")

	err := route.Process(exchange)
	assert.NoError(t, err)

	assert.False(t, exchange.GetIn().HasHeader("CamelFileName"))
	assert.False(t, exchange.GetIn().HasHeader("CamelHttpUrl"))
	assert.True(t, exchange.GetIn().HasHeader("UserHeader"))
}

func TestRemoveHeadersExclusion(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())

	route := NewRouteBuilder(ctx).
		From("direct:exclusion").
		RemoveHeaders("Camel*", "CamelHttp*").
	    Build()

	exchange := NewExchange(context.Background())
	exchange.GetIn().SetHeader("CamelFileName", "test.txt")
	exchange.GetIn().SetHeader("CamelHttpUrl", "http://localhost")
	exchange.GetIn().SetHeader("CamelHttpMethod", "GET")

	err := route.Process(exchange)
	assert.NoError(t, err)

	assert.False(t, exchange.GetIn().HasHeader("CamelFileName"))
	assert.True(t, exchange.GetIn().HasHeader("CamelHttpUrl"))
	assert.True(t, exchange.GetIn().HasHeader("CamelHttpMethod"))
}

func TestSetHeaders(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())

	headers := map[string]any{
		"Header1": "Value1",
		"Header2": 123,
	}

	route := NewRouteBuilder(ctx).
		From("direct:setheaders").
		SetHeaders(headers).
		Build()

	exchange := NewExchange(context.Background())
	err := route.Process(exchange)
	assert.NoError(t, err)

	assert.Equal(t, "Value1", exchange.GetOut().GetHeaders()["Header1"])
	assert.Equal(t, 123, exchange.GetOut().GetHeaders()["Header2"])
}

func TestSetHeadersFunc(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())

	route := NewRouteBuilder(ctx).
		From("direct:setheadersfunc").
		SetHeadersFunc(func(e *Exchange) (map[string]any, error) {
			return map[string]any{
				"Dynamic": "Value",
			}, nil
		}).
		Build()

	exchange := NewExchange(context.Background())
	err := route.Process(exchange)
	assert.NoError(t, err)

	assert.Equal(t, "Value", exchange.GetOut().GetHeaders()["Dynamic"])
}
