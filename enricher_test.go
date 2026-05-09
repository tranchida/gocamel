package gocamel

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mergeStrategy appends the enrichment body to the original body.
type mergeStrategy struct{}

func (s *mergeStrategy) Aggregate(original, enriched *Exchange) *Exchange {
	if original == nil {
		return enriched
	}
	origBody, _ := original.GetBodyAsString()
	enrichBody, _ := enriched.GetBodyAsString()
	original.GetIn().SetBody(origBody + "+" + enrichBody)
	return original
}

func TestContentEnricher_WithStrategy(t *testing.T) {
	ctx := NewCamelContext()
	direct := NewDirectComponent()
	ctx.AddComponent("direct", direct)

	// Enrichment route: echoes a fixed response.
	_, err := ctx.CreateRouteBuilder().
		From("direct:enrich").
		SetBody("ENRICHED").
		Build()
	require.NoError(t, err)

	var got string
	_, err = ctx.CreateRouteBuilder().
		From("direct:start").
		Enrich("direct:enrich", &mergeStrategy{}).
		ProcessFunc(func(e *Exchange) error {
			got, _ = e.GetBodyAsString()
			return nil
		}).
		Build()
	require.NoError(t, err)

	require.NoError(t, ctx.Start())
	defer ctx.Stop()

	ex := NewExchange(context.Background())
	ex.In.SetBody("ORIGINAL")
	p, _ := ctx.CreateEndpoint("direct:start")
	prod, _ := p.CreateProducer()
	prod.Start(context.Background())
	prod.Send(ex)
	prod.Stop()

	assert.Equal(t, "ORIGINAL+ENRICHED", got)
}

func TestContentEnricher_NoStrategy_ReplacesBody(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())

	_, err := ctx.CreateRouteBuilder().
		From("direct:lookup").
		SetBody("FROM_LOOKUP").
		Build()
	require.NoError(t, err)

	var got string
	_, err = ctx.CreateRouteBuilder().
		From("direct:main").
		Enrich("direct:lookup", nil).
		ProcessFunc(func(e *Exchange) error {
			got, _ = e.GetBodyAsString()
			return nil
		}).
		Build()
	require.NoError(t, err)

	require.NoError(t, ctx.Start())
	defer ctx.Stop()

	ex := NewExchange(context.Background())
	ex.In.SetBody("ORIGINAL")
	p, _ := ctx.CreateEndpoint("direct:main")
	prod, _ := p.CreateProducer()
	prod.Start(context.Background())
	prod.Send(ex)
	prod.Stop()

	assert.Equal(t, "FROM_LOOKUP", got)
}
