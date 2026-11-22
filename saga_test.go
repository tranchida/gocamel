package gocamel

import (
	"context"
	"errors"
	"testing"
)

func TestSagaProcessor_Success(t *testing.T) {
	camelCtx := NewCamelContext()
	camelCtx.AddComponent("direct", NewDirectComponent())

	var orderReserved, flightBooked bool
	var orderCompensated, flightCompensated bool

	_, err := camelCtx.CreateRouteBuilder().
		From("direct:start").
		Saga().
		ActionFunc(
			func(e *Exchange) error {
				orderReserved = true
				return nil
			},
			func(e *Exchange) error {
				orderCompensated = true
				return nil
			},
		).
		ActionFunc(
			func(e *Exchange) error {
				flightBooked = true
				return nil
			},
			func(e *Exchange) error {
				flightCompensated = true
				return nil
			},
		).
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
	endpoint, _ := camelCtx.CreateEndpoint("direct:start")
	producer, _ := endpoint.CreateProducer()

	if err := producer.Send(exchange); err != nil {
		t.Fatalf("Exchange failed: %v", err)
	}

	// Verify all actions completed successfully
	if !orderReserved || !flightBooked {
		t.Errorf("Actions did not complete: order=%t, flight=%t", orderReserved, flightBooked)
	}

	// Verify no compensations were executed
	if orderCompensated || flightCompensated {
		t.Errorf("Compensations should not have run: order=%t, flight=%t", orderCompensated, flightCompensated)
	}
}

func TestSagaProcessor_FailureAndRollback(t *testing.T) {
	camelCtx := NewCamelContext()
	camelCtx.AddComponent("direct", NewDirectComponent())

	var orderReserved bool
	var orderCompensated bool
	var flightCompensated bool

	// Slice to record compensation execution order
	var compensationOrder []string

	_, err := camelCtx.CreateRouteBuilder().
		From("direct:start").
		Saga().
		ActionFunc(
			func(e *Exchange) error {
				orderReserved = true
				return nil
			},
			func(e *Exchange) error {
				orderCompensated = true
				compensationOrder = append(compensationOrder, "order")
				return nil
			},
		).
		ActionFunc(
			func(e *Exchange) error {
				// Second step fails
				return errors.New("flight booking failed")
			},
			func(e *Exchange) error {
				flightCompensated = true
				compensationOrder = append(compensationOrder, "flight")
				return nil
			},
		).
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
	endpoint, _ := camelCtx.CreateEndpoint("direct:start")
	producer, _ := endpoint.CreateProducer()

	err = producer.Send(exchange)
	if err == nil {
		t.Fatal("Expected saga execution to fail, but it succeeded")
	}

	// First action should have run, second action failed
	if !orderReserved {
		t.Error("Expected first action to have completed")
	}

	// Only preceding steps (step 1) should be compensated
	if !orderCompensated {
		t.Error("Expected first step compensation to be executed")
	}
	if flightCompensated {
		t.Error("Compensation for the failed step itself should not be executed")
	}

	// Verify order
	if len(compensationOrder) != 1 || compensationOrder[0] != "order" {
		t.Errorf("Unexpected compensation execution list: %v", compensationOrder)
	}
}

func TestSagaProcessor_LIFORollback(t *testing.T) {
	camelCtx := NewCamelContext()
	camelCtx.AddComponent("direct", NewDirectComponent())

	var compensationOrder []string

	_, err := camelCtx.CreateRouteBuilder().
		From("direct:start").
		Saga().
		ActionFunc(
			func(e *Exchange) error { return nil },
			func(e *Exchange) error {
				compensationOrder = append(compensationOrder, "first")
				return nil
			},
		).
		ActionFunc(
			func(e *Exchange) error { return nil },
			func(e *Exchange) error {
				compensationOrder = append(compensationOrder, "second")
				return nil
			},
		).
		ActionFunc(
			func(e *Exchange) error { return errors.New("third step failure") },
			func(e *Exchange) error {
				compensationOrder = append(compensationOrder, "third")
				return nil
			},
		).
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
	endpoint, _ := camelCtx.CreateEndpoint("direct:start")
	producer, _ := endpoint.CreateProducer()

	_ = producer.Send(exchange)

	// Preceding compensations must run in LIFO order: "second" then "first"
	if len(compensationOrder) != 2 {
		t.Fatalf("Expected 2 compensations to execute, got %d", len(compensationOrder))
	}
	if compensationOrder[0] != "second" || compensationOrder[1] != "first" {
		t.Errorf("Expected LIFO compensation order ['second', 'first'], got %v", compensationOrder)
	}
}

func TestSagaProcessor_PanickingCompensationDoesNotAbortRemaining(t *testing.T) {
	camelCtx := NewCamelContext()
	camelCtx.AddComponent("direct", NewDirectComponent())

	var firstCompensated bool

	_, err := camelCtx.CreateRouteBuilder().
		From("direct:start").
		Saga().
		ActionFunc(
			func(e *Exchange) error { return nil },
			func(e *Exchange) error {
				firstCompensated = true
				return nil
			},
		).
		ActionFunc(
			func(e *Exchange) error { return nil },
			func(e *Exchange) error {
				panic("boom in second compensation")
			},
		).
		ActionFunc(
			func(e *Exchange) error { return errors.New("third step failure") },
			nil,
		).
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
	endpoint, _ := camelCtx.CreateEndpoint("direct:start")
	producer, _ := endpoint.CreateProducer()

	err = producer.Send(exchange)
	if err == nil {
		t.Fatal("expected saga to return failure error")
	}

	if !firstCompensated {
		t.Error("expected first compensation to execute even if second compensation panicked")
	}
}

func TestSagaProcessor_DSLRejectsNilActionAndHandlesEmptyCompensation(t *testing.T) {
	ctx := NewCamelContext()

	_, err := ctx.CreateRouteBuilder().
		From("direct:saga-nil-fn").
		Saga().
		ActionFunc(nil, nil).
		End().
		Build()
	if err == nil {
		t.Fatal("expected build error for ActionFunc(nil, nil)")
	}

	_, err = ctx.CreateRouteBuilder().
		From("direct:saga-empty-action").
		Saga().
		Action("", "direct:comp").
		End().
		Build()
	if err == nil {
		t.Fatal("expected build error for empty actionURI")
	}

	repo := NewMemorySagaRepository()
	var step1Executed bool
	_, err = ctx.CreateRouteBuilder().
		From("direct:saga-step1").
		ProcessFunc(func(e *Exchange) error {
			step1Executed = true
			return nil
		}).
		Build()
	if err != nil {
		t.Fatalf("failed to build step1 route: %v", err)
	}

	_, err = ctx.CreateRouteBuilder().
		From("direct:saga-main").
		Saga().
		Repository(repo).
		Action("direct:saga-step1", "").
		ActionFunc(func(e *Exchange) error { return errors.New("boom") }, nil).
		End().
		Build()
	if err != nil {
		t.Fatalf("expected valid route with empty compensationURI, got: %v", err)
	}

	if err := ctx.Start(); err != nil {
		t.Fatalf("failed to start context: %v", err)
	}
	defer ctx.Stop()

	p, err := ctx.GetProducer("direct:saga-main")
	if err != nil {
		t.Fatalf("failed to get producer: %v", err)
	}
	if err := p.Send(NewExchange(context.Background())); err == nil {
		t.Fatal("expected saga error")
	}
	if !step1Executed {
		t.Error("expected step1 to execute")
	}
	incomplete, err := repo.ListIncomplete(context.Background())
	if err != nil {
		t.Fatalf("failed to list incomplete sagas: %v", err)
	}
	if len(incomplete) != 0 {
		t.Errorf("expected no incomplete saga checkpoint for empty compensationURI, got %v", incomplete)
	}
}
