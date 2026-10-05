package gocamel

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRecipientList_StaticURIs(t *testing.T) {
	ctx := context.Background()
	camel := NewCamelContext()
	camel.AddComponent("direct", NewDirectComponent())

	var calls []string
	var mu sync.Mutex

	// Set up target routes
	for _, name := range []string{"a", "b", "c"} {
		targetName := name
		builder := NewRouteBuilder(camel)
		builder.From("direct:" + targetName).ProcessFunc(func(e *Exchange) error {
			mu.Lock()
			calls = append(calls, targetName)
			mu.Unlock()
			return nil
		})
		route, err := builder.Build()
		assert.NoError(t, err)
		camel.AddRoute(route)
	}

	// Create route with RecipientList using static comma-separated URIs
	builder := NewRouteBuilder(camel)
	builder.From("direct:start").RecipientList("direct:a,direct:b,direct:c")

	route, err := builder.Build()
	assert.NoError(t, err)
	camel.AddRoute(route)

	err = camel.Start()
	assert.NoError(t, err)
	defer camel.Stop()

	exchange := NewExchange(ctx)
	err = route.Process(exchange)
	assert.NoError(t, err)

	assert.ElementsMatch(t, []string{"a", "b", "c"}, calls)
}

func TestRecipientList_DynamicViaHeader(t *testing.T) {
	ctx := context.Background()
	camel := NewCamelContext()
	camel.AddComponent("direct", NewDirectComponent())

	var calls []string
	var mu sync.Mutex

	for _, name := range []string{"x", "y"} {
		targetName := name
		builder := NewRouteBuilder(camel)
		builder.From("direct:" + targetName).ProcessFunc(func(e *Exchange) error {
			mu.Lock()
			calls = append(calls, targetName)
			mu.Unlock()
			return nil
		})
		route, err := builder.Build()
		assert.NoError(t, err)
		camel.AddRoute(route)
	}

	// RecipientList using Simple Language expression from header
	// Note: SetHeader on RouteBuilder sets Out headers, but Simple Language
	// reads from In headers. We use ProcessFunc to set the In header directly.
	builder := NewRouteBuilder(camel)
	builder.From("direct:start").
		ProcessFunc(func(e *Exchange) error {
			e.GetIn().SetHeader("targets", "direct:x,direct:y")
			return nil
		}).
		RecipientList("${header.targets}")

	route, err := builder.Build()
	assert.NoError(t, err)
	camel.AddRoute(route)

	err = camel.Start()
	assert.NoError(t, err)
	defer camel.Stop()

	exchange := NewExchange(ctx)
	err = route.Process(exchange)
	assert.NoError(t, err)

	assert.ElementsMatch(t, []string{"x", "y"}, calls)
}

func TestRecipientList_GoFunction(t *testing.T) {
	ctx := context.Background()
	camel := NewCamelContext()
	camel.AddComponent("direct", NewDirectComponent())

	var calls []string
	var mu sync.Mutex

	for _, name := range []string{"p", "q"} {
		targetName := name
		builder := NewRouteBuilder(camel)
		builder.From("direct:" + targetName).ProcessFunc(func(e *Exchange) error {
			mu.Lock()
			calls = append(calls, targetName)
			mu.Unlock()
			return nil
		})
		route, err := builder.Build()
		assert.NoError(t, err)
		camel.AddRoute(route)
	}

	// RecipientList using a Go function returning dynamic URIs
	builder := NewRouteBuilder(camel)
	builder.From("direct:start").RecipientList(func(e *Exchange) ([]string, error) {
		return []string{"direct:p", "direct:q"}, nil
	})

	route, err := builder.Build()
	assert.NoError(t, err)
	camel.AddRoute(route)

	err = camel.Start()
	assert.NoError(t, err)
	defer camel.Stop()

	exchange := NewExchange(ctx)
	err = route.Process(exchange)
	assert.NoError(t, err)

	assert.ElementsMatch(t, []string{"p", "q"}, calls)
}

func TestRecipientList_ParallelProcessing(t *testing.T) {
	ctx := context.Background()
	camel := NewCamelContext()
	camel.AddComponent("direct", NewDirectComponent())

	var mu sync.Mutex
	var order []string

	for _, name := range []string{"slow", "fast"} {
		targetName := name
		builder := NewRouteBuilder(camel)
		builder.From("direct:" + targetName).ProcessFunc(func(e *Exchange) error {
			if targetName == "slow" {
				// time.Sleep is intentional: simulates a slow recipient to test ordering.
				time.Sleep(50 * time.Millisecond)
			}
			mu.Lock()
			order = append(order, targetName)
			mu.Unlock()
			return nil
		})
		route, err := builder.Build()
		assert.NoError(t, err)
		camel.AddRoute(route)
	}

	builder := NewRouteBuilder(camel)
	builder.From("direct:start").
		RecipientList("direct:slow,direct:fast").ParallelProcessing()

	route, err := builder.Build()
	assert.NoError(t, err)
	camel.AddRoute(route)

	err = camel.Start()
	assert.NoError(t, err)
	defer camel.Stop()

	exchange := NewExchange(ctx)
	err = route.Process(exchange)
	assert.NoError(t, err)

	assert.Len(t, order, 2)
	assert.Equal(t, "fast", order[0], "fast should finish before slow (parallel)")
	assert.Equal(t, "slow", order[1])
}

func TestRecipientList_Aggregation(t *testing.T) {
	ctx := context.Background()
	camel := NewCamelContext()
	camel.AddComponent("direct", NewDirectComponent())

	// Set up target routes that set specific bodies
	for _, entry := range []struct{ name, body string }{
		{"a", "resultA"},
		{"b", "resultB"},
	} {
		targetBody := entry.body
		builder := NewRouteBuilder(camel)
		builder.From("direct:" + entry.name).ProcessFunc(func(e *Exchange) error {
			e.GetIn().SetBody(targetBody)
			return nil
		})
		route, err := builder.Build()
		assert.NoError(t, err)
		camel.AddRoute(route)
	}

	strategy := &StringConcatStrategy{}

	builder := NewRouteBuilder(camel)
	builder.From("direct:start").
		RecipientList("direct:a,direct:b").AggregationStrategy(strategy)

	route, err := builder.Build()
	assert.NoError(t, err)
	camel.AddRoute(route)

	err = camel.Start()
	assert.NoError(t, err)
	defer camel.Stop()

	exchange := NewExchange(ctx)
	err = route.Process(exchange)
	assert.NoError(t, err)

	// StringConcatStrategy produces "old,new" -> "resultA,resultB"
	assert.Equal(t, "resultA,resultB", exchange.GetIn().GetBody())
}

func TestRecipientList_RecipientProperties(t *testing.T) {
	ctx := context.Background()
	camel := NewCamelContext()
	camel.AddComponent("direct", NewDirectComponent())

	var indices []int
	var sizes []int
	var completes []bool
	var mu sync.Mutex

	for _, name := range []string{"1", "2", "3"} {
		targetName := name
		builder := NewRouteBuilder(camel)
		builder.From("direct:" + targetName).ProcessFunc(func(e *Exchange) error {
			idx, _ := e.GetPropertyAsInt(CamelRecipientListIndex)
			sz, _ := e.GetPropertyAsInt(CamelRecipientListSize)
			cpl, _ := e.GetPropertyAsBool(CamelRecipientListComplete)
			mu.Lock()
			indices = append(indices, idx)
			sizes = append(sizes, sz)
			completes = append(completes, cpl)
			mu.Unlock()
			return nil
		})
		route, err := builder.Build()
		assert.NoError(t, err)
		camel.AddRoute(route)
	}

	builder := NewRouteBuilder(camel)
	builder.From("direct:start").RecipientList("direct:1,direct:2,direct:3")

	route, err := builder.Build()
	assert.NoError(t, err)
	camel.AddRoute(route)

	err = camel.Start()
	assert.NoError(t, err)
	defer camel.Stop()

	exchange := NewExchange(ctx)
	err = route.Process(exchange)
	assert.NoError(t, err)

	assert.ElementsMatch(t, []int{0, 1, 2}, indices)
	for _, sz := range sizes {
		assert.Equal(t, 3, sz)
	}
	assert.Contains(t, completes, false)
	assert.Contains(t, completes, true)
}

func TestRecipientList_EmptyExpression(t *testing.T) {
	// Test that empty expression resolves to empty list and does nothing
	rl := NewRecipientList(NewCamelContext())
	rl.SetStaticURIs("")
	exchange := NewExchange(context.Background())
	err := rl.Process(exchange)
	assert.NoError(t, err)
}
