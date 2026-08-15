package gocamel

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidate_SimpleExpression(t *testing.T) {
	camelCtx := NewCamelContext()
	camelCtx.AddComponent("direct", NewDirectComponent())

	var processed bool

	_, err := camelCtx.CreateRouteBuilder().
		From("direct:start").
		Validate("${header.priority == 'high'}").
		ProcessFunc(func(e *Exchange) error {
			processed = true
			return nil
		}).
		Build()
	require.NoError(t, err)

	require.NoError(t, camelCtx.Start())
	defer camelCtx.Stop()

	endpoint, err := camelCtx.CreateEndpoint("direct:start")
	require.NoError(t, err)
	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)

	// 1. Valid exchange
	ex1 := NewExchange(context.Background())
	ex1.In.SetHeader("priority", "high")
	err = producer.Send(ex1)
	require.NoError(t, err)
	assert.True(t, processed)

	// 2. Invalid exchange
	processed = false
	ex2 := NewExchange(context.Background())
	ex2.In.SetHeader("priority", "low")
	err = producer.Send(ex2)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrValidationFailed))
	assert.False(t, processed)
}

func TestValidate_FuncPredicate(t *testing.T) {
	camelCtx := NewCamelContext()
	camelCtx.AddComponent("direct", NewDirectComponent())

	_, err := camelCtx.CreateRouteBuilder().
		From("direct:start").
		ValidateFunc(func(e *Exchange) bool {
			val, _ := e.GetIn().GetBodyAsString()
			return val == "VALID"
		}).
		ProcessFunc(func(e *Exchange) error { return nil }).
		Build()
	require.NoError(t, err)

	require.NoError(t, camelCtx.Start())
	defer camelCtx.Stop()

	endpoint, err := camelCtx.CreateEndpoint("direct:start")
	require.NoError(t, err)
	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)

	// Valid
	ex1 := NewExchange(context.Background())
	ex1.In.SetBody("VALID")
	assert.NoError(t, producer.Send(ex1))

	// Invalid
	ex2 := NewExchange(context.Background())
	ex2.In.SetBody("INVALID")
	err = producer.Send(ex2)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrValidationFailed))
}

func TestValidate_JsonPathExpression(t *testing.T) {
	camelCtx := NewCamelContext()
	camelCtx.AddComponent("direct", NewDirectComponent())

	_, err := camelCtx.CreateRouteBuilder().
		From("direct:start").
		Validate(JsonPath("$.user.active")).
		ProcessFunc(func(e *Exchange) error { return nil }).
		Build()
	require.NoError(t, err)

	require.NoError(t, camelCtx.Start())
	defer camelCtx.Stop()

	endpoint, err := camelCtx.CreateEndpoint("direct:start")
	require.NoError(t, err)
	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)

	// Active user
	ex1 := NewExchange(context.Background())
	ex1.In.SetBody(`{"user": {"name": "Alice", "active": true}}`)
	assert.NoError(t, producer.Send(ex1))

	// Inactive user
	ex2 := NewExchange(context.Background())
	ex2.In.SetBody(`{"user": {"name": "Bob", "active": false}}`)
	err = producer.Send(ex2)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrValidationFailed))
}

func TestValidate_BuildTimeValidation(t *testing.T) {
	camelCtx := NewCamelContext()
	camelCtx.AddComponent("direct", NewDirectComponent())

	// Missing predicate
	vp := &ValidateProcessor{predicate: nil}
	assert.Error(t, vp.Validate())
}
