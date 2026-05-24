package gocamel

import (
	"context"
	"testing"
	"time"
)

func TestMessageHistory_Enabled(t *testing.T) {
	camelCtx := NewCamelContext()
	camelCtx.AddComponent("direct", NewDirectComponent())

	_, err := camelCtx.CreateRouteBuilder().
		From("direct:start").
		EnableMessageHistory().
		ProcessFunc(func(e *Exchange) error {
			time.Sleep(10 * time.Millisecond) // Ensure non-zero execution time
			return nil
		}).
		TransformSimple("Modified: ${body}").
		To("direct:end").
		Build()

	if err != nil {
		t.Fatalf("Failed to build route: %v", err)
	}

	_, _ = camelCtx.CreateRouteBuilder().
		From("direct:end").
		ProcessFunc(func(e *Exchange) error {
			// Inside the synchronous downstream route, the parent route's To (lazyProducer)
			// is still executing, so it has not completed and recorded its history entry yet.
			midHistory := GetMessageHistory(e)
			if len(midHistory) != 2 {
				t.Errorf("Expected 2 completed history entries during downstream execution, got %d: %v", len(midHistory), midHistory)
			}
			return nil
		}).
		Build()

	if err := camelCtx.Start(); err != nil {
		t.Fatalf("Failed to start context: %v", err)
	}
	defer camelCtx.Stop()

	exchange := NewExchange(context.Background())
	exchange.In.SetBody("Apple")

	endpoint, _ := camelCtx.CreateEndpoint("direct:start")
	producer, _ := endpoint.CreateProducer()

	if err := producer.Send(exchange); err != nil {
		t.Fatalf("Exchange failed: %v", err)
	}

	// Verify that history is captured completely AFTER the entire synchronous run completes
	history := GetMessageHistory(exchange)
	if len(history) == 0 {
		t.Fatal("Expected message history, but got none")
	}

	// The first route had 3 processors: ProcessFunc, Transformer (TransformSimple), and lazyProducer (To)
	if len(history) < 3 {
		t.Errorf("Expected at least 3 history entries, got %d: %v", len(history), history)
	}

	// Check fields on history entries
	foundProcessFunc := false
	foundTransformer := false
	foundLazyProducer := false

	for _, entry := range history {
		if entry.RouteID == "" {
			t.Error("Expected non-empty RouteID")
		}
		if entry.Timestamp.IsZero() {
			t.Error("Expected non-zero Timestamp")
		}
		if entry.Elapsed < 0 {
			t.Errorf("Expected positive elapsed duration, got %s", entry.Elapsed)
		}

		switch entry.NodeID {
		case "gocamel.ProcessorFunc":
			foundProcessFunc = true
			if entry.Elapsed < 10*time.Millisecond {
				t.Errorf("Expected ProcessFunc execution to take >=10ms, got %s", entry.Elapsed)
			}
		case "*gocamel.Transformer":
			foundTransformer = true
		case "*gocamel.lazyProducer":
			foundLazyProducer = true
		}
	}

	if !foundProcessFunc {
		t.Error("Expected to find ProcessorFunc in message history")
	}
	if !foundTransformer {
		t.Error("Expected to find Transformer in message history")
	}
	if !foundLazyProducer {
		t.Error("Expected to find lazyProducer in message history")
	}
}

func TestMessageHistory_Disabled(t *testing.T) {
	camelCtx := NewCamelContext()
	camelCtx.AddComponent("direct", NewDirectComponent())

	_, err := camelCtx.CreateRouteBuilder().
		From("direct:start").
		// EnableMessageHistory() is deliberately NOT called here
		ProcessFunc(func(e *Exchange) error {
			return nil
		}).
		To("direct:end").
		Build()

	if err != nil {
		t.Fatalf("Failed to build route: %v", err)
	}

	var history []MessageHistoryEntry

	_, _ = camelCtx.CreateRouteBuilder().
		From("direct:end").
		ProcessFunc(func(e *Exchange) error {
			history = GetMessageHistory(e)
			return nil
		}).
		Build()

	if err := camelCtx.Start(); err != nil {
		t.Fatalf("Failed to start context: %v", err)
	}
	defer camelCtx.Stop()

	exchange := NewExchange(context.Background())
	exchange.In.SetBody("Orange")

	endpoint, _ := camelCtx.CreateEndpoint("direct:start")
	producer, _ := endpoint.CreateProducer()

	if err := producer.Send(exchange); err != nil {
		t.Fatalf("Exchange failed: %v", err)
	}

	// Verify that history is NOT captured
	if len(history) != 0 {
		t.Errorf("Expected empty message history when disabled, but got %d entries: %v", len(history), history)
	}
}
