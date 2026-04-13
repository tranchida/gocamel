package gocamel

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSplit_SimpleSlice(t *testing.T) {
	ctx := context.Background()
	camel := NewCamelContext()
	camel.AddComponent("direct", NewDirectComponent())

	var processedItems []string
	var mu sync.Mutex

	builder := NewRouteBuilder(camel)
	builder.From("direct:start").
		Split(func(e *Exchange) (any, error) {
			body := e.GetIn().GetBody().(string)
			return strings.Split(body, ","), nil
		}).
		ProcessFunc(func(e *Exchange) error {
			body := e.GetIn().GetBody().(string)
			mu.Lock()
			processedItems = append(processedItems, body)
			mu.Unlock()
			return nil
		}).
		End().
		ProcessFunc(func(e *Exchange) error {
			// Ce processeur s'exécute après le split sur l'échange original
			e.GetIn().SetHeader("AfterSplit", true)
			return nil
		})

	route := builder.Build()
	// Pas besoin d'ajouter manuellement car CreateRoute le fait via NewRouteBuilder
	
	// On ne démarre pas tout le contexte pour un test unitaire de processeur si possible,
	// mais ici on utilise la route complète.
	// direct:start nécessite un consommateur.
	
	exchange := NewExchange(ctx)
	exchange.GetIn().SetBody("a,b,c")
	
	err := route.Process(exchange)
	assert.NoError(t, err)
	
	assert.Equal(t, []string{"a", "b", "c"}, processedItems)
	assert.Equal(t, "a,b,c", exchange.GetIn().GetBody(), "Le corps original doit être préservé")
	
	val, ok := exchange.GetIn().GetHeader("AfterSplit")
	assert.True(t, ok)
	assert.True(t, val.(bool))
}

func TestSplit_WithAggregation(t *testing.T) {
	ctx := context.Background()
	camel := NewCamelContext()
	camel.AddComponent("direct", NewDirectComponent())

	strategy := &StringConcatStrategy{}

	builder := NewRouteBuilder(camel)
	builder.From("direct:start").
		Split(func(e *Exchange) (any, error) {
			return []string{"1", "2", "3"}, nil
		}).
		AggregationStrategy(strategy).
		ProcessFunc(func(e *Exchange) error {
			body := e.GetIn().GetBody().(string)
			e.GetIn().SetBody("item-" + body)
			return nil
		}).
		End()

	route := builder.Build()
	
	exchange := NewExchange(ctx)
	exchange.GetIn().SetBody("initial")
	
	err := route.Process(exchange)
	assert.NoError(t, err)
	
	// La stratégie StringConcatStrategy concatène avec des virgules.
	// Comme c'est le premier appel, oldExchange est nil, donc le premier item reste "item-1".
	// Ensuite "item-1" + "," + "item-2" = "item-1,item-2", etc.
	assert.Equal(t, "item-1,item-2,item-3", exchange.GetIn().GetBody())
}

func TestSplit_Properties(t *testing.T) {
	ctx := context.Background()
	camel := NewCamelContext()
	camel.AddComponent("direct", NewDirectComponent())

	var indices []int
	var sizes []int
	var completes []bool
	var mu sync.Mutex

	builder := NewRouteBuilder(camel)
	builder.From("direct:start").
		Split(func(e *Exchange) (any, error) {
			return []int{10, 20, 30}, nil
		}).
		ProcessFunc(func(e *Exchange) error {
			idx, _ := e.GetPropertyAsInt("CamelSplitIndex")
			size, _ := e.GetPropertyAsInt("CamelSplitSize")
			complete, _ := e.GetPropertyAsBool("CamelSplitComplete")
			
			mu.Lock()
			indices = append(indices, idx)
			sizes = append(sizes, size)
			completes = append(completes, complete)
			mu.Unlock()
			return nil
		}).
		End()

	route := builder.Build()
	
	exchange := NewExchange(ctx)
	err := route.Process(exchange)
	assert.NoError(t, err)
	
	assert.Equal(t, []int{0, 1, 2}, indices)
	assert.Equal(t, []int{3, 3, 3}, sizes)
	assert.Equal(t, []bool{false, false, true}, completes)
}

func TestSplit_SingleItem(t *testing.T) {
	ctx := context.Background()
	camel := NewCamelContext()
	camel.AddComponent("direct", NewDirectComponent())

	var processedItems []string
	var mu sync.Mutex

	builder := NewRouteBuilder(camel)
	builder.From("direct:start").
		Split(func(e *Exchange) (any, error) {
			return "single", nil
		}).
		ProcessFunc(func(e *Exchange) error {
			body := e.GetIn().GetBody().(string)
			mu.Lock()
			processedItems = append(processedItems, body)
			mu.Unlock()
			return nil
		}).
		End()

	route := builder.Build()
	
	exchange := NewExchange(ctx)
	err := route.Process(exchange)
	assert.NoError(t, err)
	
	assert.Equal(t, []string{"single"}, processedItems)
}
