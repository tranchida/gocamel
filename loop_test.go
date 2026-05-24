package gocamel

import (
	"context"
	"testing"
)

func TestLoopProcessor_Static(t *testing.T) {
	camelCtx := NewCamelContext()
	camelCtx.AddComponent("direct", NewDirectComponent())

	// Accumulate loop runs
	var runs []int
	var indices []int

	_, err := camelCtx.CreateRouteBuilder().
		From("direct:start").
		Loop(3).
			ProcessFunc(func(e *Exchange) error {
				idx, _ := e.GetPropertyAsInt(CamelLoopIndex)
				size, _ := e.GetPropertyAsInt(CamelLoopSize)
				indices = append(indices, idx)
				runs = append(runs, size)

				body, _ := e.GetBodyAsString()
				e.In.SetBody(body + "x")
				return nil
			}).
		End().
		To("direct:end").
		Build()

	if err != nil {
		t.Fatalf("Failed to build route: %v", err)
	}

	var finalBody string
	_, _ = camelCtx.CreateRouteBuilder().
		From("direct:end").
		ProcessFunc(func(e *Exchange) error {
			finalBody, _ = e.GetBodyAsString()
			return nil
		}).
		Build()

	if err := camelCtx.Start(); err != nil {
		t.Fatalf("Failed to start context: %v", err)
	}
	defer camelCtx.Stop()

	exchange := NewExchange(context.Background())
	exchange.In.SetBody("start-")

	endpoint, err := camelCtx.CreateEndpoint("direct:start")
	if err != nil {
		t.Fatalf("Failed to create endpoint: %v", err)
	}

	producer, err := endpoint.CreateProducer()
	if err != nil {
		t.Fatalf("Failed to create producer: %v", err)
	}

	if err := producer.Send(exchange); err != nil {
		t.Fatalf("Failed to send exchange: %v", err)
	}

	// Verify loop size and index properties
	if len(runs) != 3 {
		t.Errorf("Expected 3 runs, got %d", len(runs))
	}
	expectedIndices := []int{0, 1, 2}
	for i, idx := range indices {
		if idx != expectedIndices[i] {
			t.Errorf("Expected index %d at run %d, got %d", expectedIndices[i], i, idx)
		}
		if runs[i] != 3 {
			t.Errorf("Expected size 3 at run %d, got %d", i, runs[i])
		}
	}

	// Body should have 3 'x' appended sequentially
	expectedBody := "start-xxx"
	if finalBody != expectedBody {
		t.Errorf("Expected body %q, got %q", expectedBody, finalBody)
	}
}

func TestLoopProcessor_SimpleExpression(t *testing.T) {
	camelCtx := NewCamelContext()
	camelCtx.AddComponent("direct", NewDirectComponent())

	var counter int

	_, err := camelCtx.CreateRouteBuilder().
		From("direct:start").
		LoopSimple("${header.LoopCount}").
			ProcessFunc(func(e *Exchange) error {
				counter++
				complete, _ := e.GetPropertyAsBool(CamelLoopComplete)
				idx, _ := e.GetPropertyAsInt(CamelLoopIndex)
				if idx == 1 && complete {
					t.Errorf("Loop should not be complete at index 1")
				}
				if idx == 2 && !complete {
					t.Errorf("Loop should be complete at index 2")
				}
				return nil
			}).
		End().
		Build()

	if err != nil {
		t.Fatalf("Failed to build route: %v", err)
	}

	if err := camelCtx.Start(); err != nil {
		t.Fatalf("Failed to start context: %v", err)
	}
	defer camelCtx.Stop()

	exchange := NewExchange(context.Background())
	exchange.In.SetHeader("LoopCount", 3) // Set iteration count dynamically

	endpoint, err := camelCtx.CreateEndpoint("direct:start")
	if err != nil {
		t.Fatalf("Failed to create endpoint: %v", err)
	}

	producer, err := endpoint.CreateProducer()
	if err != nil {
		t.Fatalf("Failed to create producer: %v", err)
	}

	if err := producer.Send(exchange); err != nil {
		t.Fatalf("Failed to send exchange: %v", err)
	}

	if counter != 3 {
		t.Errorf("Expected loop to run 3 times, ran %d times", counter)
	}
}

func TestLoopProcessor_StopRouting(t *testing.T) {
	camelCtx := NewCamelContext()
	camelCtx.AddComponent("direct", NewDirectComponent())

	var counter int

	_, err := camelCtx.CreateRouteBuilder().
		From("direct:start").
		Loop(5).
			ProcessFunc(func(e *Exchange) error {
				counter++
				idx, _ := e.GetPropertyAsInt(CamelLoopIndex)
				if idx == 2 {
					return ErrStopRouting // Break out of the loop early
				}
				return nil
			}).
		End().
		Build()

	if err != nil {
		t.Fatalf("Failed to build route: %v", err)
	}

	if err := camelCtx.Start(); err != nil {
		t.Fatalf("Failed to start context: %v", err)
	}
	defer camelCtx.Stop()

	exchange := NewExchange(context.Background())
	endpoint, err := camelCtx.CreateEndpoint("direct:start")
	if err != nil {
		t.Fatalf("Failed to create endpoint: %v", err)
	}

	producer, err := endpoint.CreateProducer()
	if err != nil {
		t.Fatalf("Failed to create producer: %v", err)
	}

	_ = producer.Send(exchange)

	// Counter should stop at 3 (indices 0, 1, 2)
	if counter != 3 {
		t.Errorf("Expected loop to stop after 3 runs, ran %d times", counter)
	}
}
