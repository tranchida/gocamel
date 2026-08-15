package gocamel

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJSONPath_EvaluateDirect(t *testing.T) {
	jsonData := `{
		"store": {
			"book": [
				{"category": "reference", "author": "Nigel Rees", "title": "Sayings of the Century", "price": 8.95},
				{"category": "fiction", "author": "Evelyn Waugh", "title": "Sword of Honour", "price": 12.99},
				{"category": "fiction", "author": "Herman Melville", "title": "Moby Dick", "isbn": "0-553-21311-3", "price": 8.99}
			],
			"bicycle": {
				"color": "red",
				"price": 19.95
			}
		},
		"expensive": 10
	}`

	// Extract single scalar
	val, err := EvaluateJSONPath("$.store.bicycle.color", jsonData)
	require.NoError(t, err)
	assert.Equal(t, "red", val)

	// Extract nested array element
	val, err = EvaluateJSONPath("$.store.book[0].title", jsonData)
	require.NoError(t, err)
	assert.Equal(t, "Sayings of the Century", val)

	// Extract negative index
	val, err = EvaluateJSONPath("$.store.book[-1].title", jsonData)
	require.NoError(t, err)
	assert.Equal(t, "Moby Dick", val)

	// Bracket notation
	val, err = EvaluateJSONPath("$['store']['bicycle']['price']", jsonData)
	require.NoError(t, err)
	assert.Equal(t, 19.95, val)

	// Wildcard on array
	val, err = EvaluateJSONPath("$.store.book[*].author", jsonData)
	require.NoError(t, err)
	authors, ok := val.([]any)
	require.True(t, ok)
	assert.Len(t, authors, 3)
	assert.Contains(t, authors, "Nigel Rees")
	assert.Contains(t, authors, "Evelyn Waugh")
	assert.Contains(t, authors, "Herman Melville")

	// Recursive descent
	val, err = EvaluateJSONPath("$..price", jsonData)
	require.NoError(t, err)
	prices, ok := val.([]any)
	require.True(t, ok)
	assert.Len(t, prices, 4) // 3 books + 1 bicycle
}

func TestJSONPath_SimpleLanguageIntegration(t *testing.T) {
	jsonData := `{"user": {"name": "Alice", "role": "admin", "age": 30}}`

	exchange := NewExchange(context.Background())
	exchange.In.SetBody(jsonData)
	exchange.In.SetHeader("AuthPayload", `{"token": "xyz-999", "scope": "read"}`)

	// Expression on body
	expr, err := ParseSimpleTemplate("User is ${jsonpath:$.user.name} with role ${jsonpath:$.user.role}")
	require.NoError(t, err)

	res, err := expr.EvaluateAsString(exchange)
	require.NoError(t, err)
	assert.Equal(t, "User is Alice with role admin", res)

	// Expression on header
	exprHeader, err := ParseSimpleTemplate("Token: ${jsonpath:header.AuthPayload:$.token}")
	require.NoError(t, err)
	resHeader, err := exprHeader.EvaluateAsString(exchange)
	require.NoError(t, err)
	assert.Equal(t, "Token: xyz-999", resHeader)
}

func TestJSONPath_ChoiceAndPredicate(t *testing.T) {
	camelCtx := NewCamelContext()
	camelCtx.AddComponent("direct", NewDirectComponent())

	var captured string

	_, err := camelCtx.CreateRouteBuilder().
		From("direct:start").
		Choice().
		When("${jsonpath:$.user.role == 'admin'}").
		ProcessFunc(func(e *Exchange) error {
			captured = "admin"
			return nil
		}).
		Otherwise().
		ProcessFunc(func(e *Exchange) error {
			captured = "user"
			return nil
		}).
		EndChoice().
		Build()
	require.NoError(t, err)

	require.NoError(t, camelCtx.Start())
	defer camelCtx.Stop()

	endpoint, err := camelCtx.CreateEndpoint("direct:start")
	require.NoError(t, err)
	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)

	// 1. Admin user
	ex1 := NewExchange(context.Background())
	ex1.In.SetBody(`{"user": {"name": "Bob", "role": "admin"}}`)
	require.NoError(t, producer.Send(ex1))
	assert.Equal(t, "admin", captured)

	// 2. Regular user
	ex2 := NewExchange(context.Background())
	ex2.In.SetBody(`{"user": {"name": "Charlie", "role": "viewer"}}`)
	require.NoError(t, producer.Send(ex2))
	assert.Equal(t, "user", captured)
}
