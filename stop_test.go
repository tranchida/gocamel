package gocamel

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStopEIP(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())
	
	continued := false
	
	route := NewRouteBuilder(ctx).
		From("direct:start").
		Stop().
		ProcessFunc(func(exchange *Exchange) error {
			continued = true
			return nil
		}).
		Build()
	
	exchange := NewExchange(context.Background())
	err := route.Process(exchange)
	
	assert.ErrorIs(t, err, ErrStopRouting)
	assert.False(t, continued, "Second processor should NOT have been executed")
}

func TestStopInSplit(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())
	
	processedParts := 0
	continuedAfterSplit := false
	
	// Route avec un split où on arrête le traitement pour chaque partie
	route := NewRouteBuilder(ctx).
		From("direct:split-stop").
		Split(func(e *Exchange) (any, error) {
			return []string{"a", "b", "c"}, nil
		}).
			Stop().
			ProcessFunc(func(exchange *Exchange) error {
				processedParts++ // Ne devrait jamais être incrémenté
				return nil
			}).
		End().
		ProcessFunc(func(exchange *Exchange) error {
			continuedAfterSplit = true
			return nil
		}).
		Build()
	
	exchange := NewExchange(context.Background())
	err := route.Process(exchange)
	
	assert.NoError(t, err)
	assert.Equal(t, 0, processedParts, "Processed parts should be 0 because of Stop()")
	assert.True(t, continuedAfterSplit, "Should have continued after split")
}

func TestStopInSplitPartial(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())
	
	processedParts := 0
	continuedAfterSplit := false
	
	// Route avec un split où on arrête le traitement seulement pour la partie "b"
	route := NewRouteBuilder(ctx).
		From("direct:split-stop-partial").
		Split(func(e *Exchange) (any, error) {
			return []string{"a", "b", "c"}, nil
		}).
			ProcessFunc(func(exchange *Exchange) error {
				if exchange.GetIn().GetBody().(string) == "b" {
					return ErrStopRouting
				}
				return nil
			}).
			ProcessFunc(func(exchange *Exchange) error {
				processedParts++
				return nil
			}).
		End().
		ProcessFunc(func(exchange *Exchange) error {
			continuedAfterSplit = true
			return nil
		}).
		Build()
	
	exchange := NewExchange(context.Background())
	err := route.Process(exchange)
	
	assert.NoError(t, err)
	assert.Equal(t, 2, processedParts, "Expected 2 processed parts (a and c)")
	assert.True(t, continuedAfterSplit, "Should have continued after split")
}
