package gocamel

import (
	"context"
	"sync/atomic"
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

type enricherPanicStrategy struct{}

func (enricherPanicStrategy) Aggregate(*Exchange, *Exchange) *Exchange {
	panic("boom in enrich strategy")
}

type enricherSyncTracker struct {
	completed atomic.Int32
	failed    atomic.Int32
}

func (s *enricherSyncTracker) OnComplete(*Exchange) { s.completed.Add(1) }
func (s *enricherSyncTracker) OnFailure(*Exchange)  { s.failed.Add(1) }

func TestContentEnricher_ReleasesProducerAndCompletesAndRecoversStrategyPanic(t *testing.T) {
	t.Run("releases producer and marks child failed on producer panic", func(t *testing.T) {
		ctx := NewCamelContext()
		ctx.SetProducerCacheSize(1)
		comp := &panicTrackerComponent{}
		ctx.AddComponent("track", comp)
		require.NoError(t, ctx.Start())
		t.Cleanup(func() { _ = ctx.Stop() })

		enricher := NewContentEnricher(ctx, "track:panic", nil)
		err := ProcessSafely(enricher, NewExchange(context.Background()))
		require.ErrorIs(t, err, ErrProcessorPanic)

		require.NoError(t, ctx.sendToURI("track:other", NewExchange(context.Background())))
		comp.mu.Lock()
		panicProd := comp.producers["track:panic"]
		comp.mu.Unlock()
		require.NotNil(t, panicProd)
		assert.Equal(t, int32(1), panicProd.stopped.Load(), "panicking producer must have been released and evicted")
	})

	t.Run("recovers strategy panic and completes child exchange as failure", func(t *testing.T) {
		ctx := NewCamelContext()
		tracker := &enricherSyncTracker{}
		_, err := ctx.CreateRouteBuilder().
			From("direct:enrich-ok").
			ProcessFunc(func(e *Exchange) error {
				e.AddSynchronization(tracker)
				e.GetOut().SetBody("enriched")
				return nil
			}).
			Build()
		require.NoError(t, err)
		require.NoError(t, ctx.Start())
		t.Cleanup(func() { _ = ctx.Stop() })

		enricher := NewContentEnricher(ctx, "direct:enrich-ok", enricherPanicStrategy{})
		// Call Process directly (not wrapped in ProcessSafely) to verify Enricher itself catches strategy panics.
		err = enricher.Process(NewExchange(context.Background()))
		require.ErrorIs(t, err, ErrProcessorPanic)
		assert.Equal(t, int32(1), tracker.failed.Load(), "child exchange must complete with OnFailure when strategy panics")
		assert.Equal(t, int32(0), tracker.completed.Load())
	})
}
