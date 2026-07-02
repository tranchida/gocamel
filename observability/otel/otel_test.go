package otel

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	gocamel "gitlab.com/tranchida/gocamel"
)

func TestTracingInterceptor(t *testing.T) {
	// Setup OTel in-memory exporter
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSampler(trace.AlwaysSample()),
		trace.WithSpanProcessor(trace.NewSimpleSpanProcessor(exporter)),
	)
	otel.SetTracerProvider(tp)

	ctx := gocamel.NewCamelContext()
	ctx.AddComponent("direct", gocamel.NewDirectComponent())

	_, err := ctx.CreateRouteBuilder().
		From("direct:start").
		Intercept(Tracing()).
		ProcessFunc(func(e *gocamel.Exchange) error {
			return nil
		}).
		Build()
	require.NoError(t, err)

	err = ctx.Start()
	require.NoError(t, err)
	defer ctx.Stop()

	// Send an exchange
	ep, _ := ctx.CreateEndpoint("direct:start")
	producer, _ := ep.CreateProducer()
	exchange := gocamel.NewExchange(context.Background())
	err = producer.Send(exchange)
	require.NoError(t, err)

	// Check spans
	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "route:route-1", spans[0].Name)

	// Check attributes
	attrs := spans[0].Attributes
	foundRouteID := false
	for _, attr := range attrs {
		if attr.Key == "camel.route.id" && attr.Value.AsString() == "route-1" {
			foundRouteID = true
		}
	}
	assert.True(t, foundRouteID, "camel.route.id attribute not found")
}
