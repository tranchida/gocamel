package gocamel

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPropertyEIPs(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())

	route := NewRouteBuilder(ctx).
		From("direct:props").
		SetProperty("Prop1", "Value1").
		SetPropertyFunc("Prop2", func(e *Exchange) (any, error) {
			return "Value2", nil
		}).
		RemoveProperty("ToBeRemoved").
		RemoveProperties("Camel*", "CamelHttp*").
		Build()

	exchange := NewExchange(context.Background())
	exchange.SetProperty("ToBeRemoved", "old")
	exchange.SetProperty("ToKeep", "stay")
	exchange.SetProperty("CamelFileName", "test.txt")
	exchange.SetProperty("CamelHttpUrl", "http://localhost")
	exchange.SetProperty("CamelHttpMethod", "GET")

	err := route.Process(exchange)
	assert.NoError(t, err)

	assert.Equal(t, "Value1", exchange.Properties["Prop1"])
	assert.Equal(t, "Value2", exchange.Properties["Prop2"])
	assert.False(t, exchange.HasProperty("ToBeRemoved"))
	assert.True(t, exchange.HasProperty("ToKeep"))
	assert.False(t, exchange.HasProperty("CamelFileName"))
	assert.True(t, exchange.HasProperty("CamelHttpUrl"))
	assert.True(t, exchange.HasProperty("CamelHttpMethod"))
}
