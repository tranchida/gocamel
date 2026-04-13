package gocamel

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestMulticast_Simple(t *testing.T) {
	ctx := context.Background()
	camel := NewCamelContext()
	camel.AddComponent("direct", NewDirectComponent())
	
	var processedA, processedB bool
	var mu sync.Mutex

	builder := NewRouteBuilder(camel)
	builder.From("direct:start").
		Multicast().
			ProcessFunc(func(e *Exchange) error {
				mu.Lock()
				processedA = true
				mu.Unlock()
				return nil
			}).
			ProcessFunc(func(e *Exchange) error {
				mu.Lock()
				processedB = true
				mu.Unlock()
				return nil
			}).
		End()

	route := builder.Build()
	
	exchange := NewExchange(ctx)
	exchange.GetIn().SetBody("test")
	
	err := route.Process(exchange)
	assert.NoError(t, err)
	
	assert.True(t, processedA)
	assert.True(t, processedB)
}

func TestMulticast_Parallel(t *testing.T) {
	ctx := context.Background()
	camel := NewCamelContext()
	camel.AddComponent("direct", NewDirectComponent())
	
	var mu sync.Mutex
	var order []string

	builder := NewRouteBuilder(camel)
	builder.From("direct:start").
		Multicast().
			ParallelProcessing().
			ProcessFunc(func(e *Exchange) error {
				time.Sleep(50 * time.Millisecond)
				mu.Lock()
				order = append(order, "A")
				mu.Unlock()
				return nil
			}).
			ProcessFunc(func(e *Exchange) error {
				// Pas de sleep, devrait finir en premier
				mu.Lock()
				order = append(order, "B")
				mu.Unlock()
				return nil
			}).
		End()

	route := builder.Build()
	
	exchange := NewExchange(ctx)
	err := route.Process(exchange)
	assert.NoError(t, err)
	
	assert.Len(t, order, 2)
	assert.Equal(t, "B", order[0], "B devrait avoir fini avant A grâce au sleep de A")
	assert.Equal(t, "A", order[1])
}

func TestMulticast_Aggregation(t *testing.T) {
	ctx := context.Background()
	camel := NewCamelContext()
	camel.AddComponent("direct", NewDirectComponent())
	
	strategy := &StringConcatStrategy{}

	builder := NewRouteBuilder(camel)
	builder.From("direct:start").
		Multicast().
			AggregationStrategy(strategy).
			ProcessFunc(func(e *Exchange) error {
				e.GetIn().SetBody("resultA")
				return nil
			}).
			ProcessFunc(func(e *Exchange) error {
				e.GetIn().SetBody("resultB")
				return nil
			}).
		End()

	route := builder.Build()
	
	exchange := NewExchange(ctx)
	err := route.Process(exchange)
	assert.NoError(t, err)
	
	// resultA + "," + resultB
	assert.Equal(t, "resultA,resultB", exchange.GetIn().GetBody())
}

func TestMulticast_Pipeline(t *testing.T) {
	ctx := context.Background()
	camel := NewCamelContext()
	camel.AddComponent("direct", NewDirectComponent())
	
	var results []string
	var mu sync.Mutex

	builder := NewRouteBuilder(camel)
	builder.From("direct:start").
		Multicast().
			Pipeline().
				SetBody("A1").
				ProcessFunc(func(e *Exchange) error {
					body := e.GetIn().GetBody().(string)
					mu.Lock()
					results = append(results, body)
					mu.Unlock()
					e.GetIn().SetBody(body + "A2")
					return nil
				}).
				ProcessFunc(func(e *Exchange) error {
					body := e.GetIn().GetBody().(string)
					mu.Lock()
					results = append(results, body)
					mu.Unlock()
					return nil
				}).
			End().
			Pipeline().
				SetBody("B1").
				ProcessFunc(func(e *Exchange) error {
					body := e.GetIn().GetBody().(string)
					mu.Lock()
					results = append(results, body)
					mu.Unlock()
					return nil
				}).
			End().
		End()

	route := builder.Build()
	
	exchange := NewExchange(ctx)
	err := route.Process(exchange)
	assert.NoError(t, err)
	
	assert.Equal(t, []string{"A1", "A1A2", "B1"}, results)
}


func TestMulticast_ToVariadic(t *testing.T) {
	ctx := context.Background()
	camel := NewCamelContext()
	
	// On mock les endpoints pour vérifier qu'ils sont appelés
	// En fait, on peut juste utiliser direct: et un processeur derrière.
	camel.AddComponent("direct", NewDirectComponent())
	
	var calls []string
	var mu sync.Mutex

	builder1 := NewRouteBuilder(camel)
	builder1.From("direct:a").ProcessFunc(func(e *Exchange) error {
		mu.Lock()
		calls = append(calls, "a")
		mu.Unlock()
		return nil
	})
	camel.AddRoute(builder1.Build())

	builder2 := NewRouteBuilder(camel)
	builder2.From("direct:b").ProcessFunc(func(e *Exchange) error {
		mu.Lock()
		calls = append(calls, "b")
		mu.Unlock()
		return nil
	})
	camel.AddRoute(builder2.Build())

	builder := NewRouteBuilder(camel)
	builder.From("direct:start").To("direct:a", "direct:b")
	
	route := builder.Build()
	camel.AddRoute(route)

	err := camel.Start()
	assert.NoError(t, err)
	defer camel.Stop()
	
	exchange := NewExchange(ctx)
	err = route.Process(exchange)
	assert.NoError(t, err)
	
	assert.ElementsMatch(t, []string{"a", "b"}, calls)
}
