package gocamel

import (
	"context"
	"strings"
	"testing"
)

func TestTransformProcessor_Func(t *testing.T) {
	camelCtx := NewCamelContext()
	camelCtx.AddComponent("direct", NewDirectComponent())

	var finalBody string

	_, err := camelCtx.CreateRouteBuilder().
		From("direct:start").
		Transform(func(e *Exchange) (any, error) {
			body, _ := e.GetBodyAsString()
			return strings.ToUpper(body), nil
		}).
		ProcessFunc(func(e *Exchange) error {
			finalBody, _ = e.GetBodyAsString()
			return nil
		}).
		Build()

	if err != nil {
		t.Fatalf("Failed to build route: %v", err)
	}

	if err := camelCtx.Start(); err != nil {
		t.Fatalf("Failed to start context: %v", err)
	}
	defer camelCtx.Stop()

	exchange := NewExchange(context.Background())
	exchange.In.SetBody("hello-world")

	endpoint, _ := camelCtx.CreateEndpoint("direct:start")
	producer, _ := endpoint.CreateProducer()

	if err := producer.Send(exchange); err != nil {
		t.Fatalf("Exchange failed: %v", err)
	}

	if finalBody != "HELLO-WORLD" {
		t.Errorf("Expected transformed body 'HELLO-WORLD', got %q", finalBody)
	}
}

func TestTransformProcessor_Simple(t *testing.T) {
	camelCtx := NewCamelContext()
	camelCtx.AddComponent("direct", NewDirectComponent())

	var finalBody string

	_, err := camelCtx.CreateRouteBuilder().
		From("direct:start").
		TransformSimple("Modified: ${body} and type is ${header.Type}").
		ProcessFunc(func(e *Exchange) error {
			finalBody, _ = e.GetBodyAsString()
			return nil
		}).
		Build()

	if err != nil {
		t.Fatalf("Failed to build route: %v", err)
	}

	if err := camelCtx.Start(); err != nil {
		t.Fatalf("Failed to start context: %v", err)
	}
	defer camelCtx.Stop()

	exchange := NewExchange(context.Background())
	exchange.In.SetBody("Apple")
	exchange.In.SetHeader("Type", "fruit")

	endpoint, _ := camelCtx.CreateEndpoint("direct:start")
	producer, _ := endpoint.CreateProducer()

	if err := producer.Send(exchange); err != nil {
		t.Fatalf("Exchange failed: %v", err)
	}

	expected := "Modified: Apple and type is fruit"
	if finalBody != expected {
		t.Errorf("Expected body %q, got %q", expected, finalBody)
	}
}
