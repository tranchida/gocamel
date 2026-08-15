package gocamel

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stringAppendStrategy struct{}

func (s *stringAppendStrategy) Aggregate(oldExchange, newExchange *Exchange) *Exchange {
	if newExchange == nil {
		return oldExchange
	}
	oldBody := oldExchange.GetIn().GetBody().(string)
	newBody := newExchange.GetIn().GetBody().(string)
	oldExchange.GetIn().SetBody(fmt.Sprintf("%s + %s", oldBody, newBody))
	return oldExchange
}

func TestPollEnrich_ReplaceBody(t *testing.T) {
	camelCtx := NewCamelContext()
	camelCtx.AddComponent("direct", NewDirectComponent())
	camelCtx.AddComponent("seda", NewSedaComponent())

	// Pre-populate SEDA queue with a message
	sedaEndpoint, err := camelCtx.CreateEndpoint("seda:inbox")
	require.NoError(t, err)
	sedaProducer, err := sedaEndpoint.CreateProducer()
	require.NoError(t, err)
	require.NoError(t, sedaProducer.Start(context.Background()))

	prepEx := NewExchange(context.Background())
	prepEx.In.SetBody("Polled Secret Data")
	require.NoError(t, sedaProducer.Send(prepEx))

	var captured string

	_, err = camelCtx.CreateRouteBuilder().
		From("direct:start").
		PollEnrich("seda:inbox", nil, 1*time.Second).
		ProcessFunc(func(e *Exchange) error {
			captured, _ = e.GetIn().GetBodyAsString()
			return nil
		}).
		Build()
	require.NoError(t, err)

	require.NoError(t, camelCtx.Start())
	defer camelCtx.Stop()

	directEndpoint, err := camelCtx.CreateEndpoint("direct:start")
	require.NoError(t, err)
	directProducer, err := directEndpoint.CreateProducer()
	require.NoError(t, err)

	triggerEx := NewExchange(context.Background())
	triggerEx.In.SetBody("Initial Body")

	require.NoError(t, directProducer.Send(triggerEx))
	assert.Equal(t, "Polled Secret Data", captured)
}

func TestPollEnrich_WithAggregationStrategy(t *testing.T) {
	camelCtx := NewCamelContext()
	camelCtx.AddComponent("direct", NewDirectComponent())
	camelCtx.AddComponent("seda", NewSedaComponent())

	sedaEndpoint, err := camelCtx.CreateEndpoint("seda:config")
	require.NoError(t, err)
	sedaProducer, err := sedaEndpoint.CreateProducer()
	require.NoError(t, err)
	require.NoError(t, sedaProducer.Start(context.Background()))

	prepEx := NewExchange(context.Background())
	prepEx.In.SetBody("Extra Info")
	require.NoError(t, sedaProducer.Send(prepEx))

	var captured string

	_, err = camelCtx.CreateRouteBuilder().
		From("direct:start").
		PollEnrich("seda:config", &stringAppendStrategy{}, 1*time.Second).
		ProcessFunc(func(e *Exchange) error {
			captured, _ = e.GetIn().GetBodyAsString()
			return nil
		}).
		Build()
	require.NoError(t, err)

	require.NoError(t, camelCtx.Start())
	defer camelCtx.Stop()

	directEndpoint, err := camelCtx.CreateEndpoint("direct:start")
	require.NoError(t, err)
	directProducer, err := directEndpoint.CreateProducer()
	require.NoError(t, err)

	triggerEx := NewExchange(context.Background())
	triggerEx.In.SetBody("Base Data")

	require.NoError(t, directProducer.Send(triggerEx))
	assert.Equal(t, "Base Data + Extra Info", captured)
}

func TestPollEnrich_TimeoutEmptyQueue(t *testing.T) {
	camelCtx := NewCamelContext()
	camelCtx.AddComponent("direct", NewDirectComponent())
	camelCtx.AddComponent("seda", NewSedaComponent())

	var captured string

	_, err := camelCtx.CreateRouteBuilder().
		From("direct:start").
		PollEnrich("seda:empty", nil, 50*time.Millisecond).
		ProcessFunc(func(e *Exchange) error {
			captured, _ = e.GetIn().GetBodyAsString()
			return nil
		}).
		Build()
	require.NoError(t, err)

	require.NoError(t, camelCtx.Start())
	defer camelCtx.Stop()

	directEndpoint, err := camelCtx.CreateEndpoint("direct:start")
	require.NoError(t, err)
	directProducer, err := directEndpoint.CreateProducer()
	require.NoError(t, err)

	triggerEx := NewExchange(context.Background())
	triggerEx.In.SetBody("Untouched Body")

	require.NoError(t, directProducer.Send(triggerEx))
	// Body should remain untouched since polling timed out
	assert.Equal(t, "Untouched Body", captured)
}
