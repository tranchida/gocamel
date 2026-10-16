package otel

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	gocamel "github.com/tranchida/gocamel"
)

func TestTracingInterceptor(t *testing.T) {
	// Setup OTel in-memory exporter
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSampler(trace.AlwaysSample()),
		trace.WithSpanProcessor(trace.NewSimpleSpanProcessor(exporter)),
	)
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(trace.NewTracerProvider())
	})

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

// TestTracingInterceptor_WrappedStopRoutingNotRecordedAsError verifies that a
// WRAPPED ErrStopRouting (the control signal with context attached by the
// returning processor) is not recorded as a span error, matching the core's
// errors.Is semantics in route.processFrom.
func TestTracingInterceptor_WrappedStopRoutingNotRecordedAsError(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSampler(trace.AlwaysSample()),
		trace.WithSpanProcessor(trace.NewSimpleSpanProcessor(exporter)),
	)
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(trace.NewTracerProvider())
	})

	ctx := gocamel.NewCamelContext()
	ctx.AddComponent("direct", gocamel.NewDirectComponent())

	_, err := ctx.CreateRouteBuilder().
		From("direct:start").
		Intercept(Tracing()).
		ProcessFunc(func(e *gocamel.Exchange) error {
			return fmt.Errorf("filter rejected message: %w", gocamel.ErrStopRouting)
		}).
		Build()
	require.NoError(t, err)

	// Contrast route: a plain error IS recorded on the span. Both routes are
	// declared before Start so the second does not hit "context is already
	// started".
	_, err = ctx.CreateRouteBuilder().
		From("direct:fail").
		Intercept(Tracing()).
		ProcessFunc(func(e *gocamel.Exchange) error {
			return errors.New("boom")
		}).
		Build()
	require.NoError(t, err)

	require.NoError(t, ctx.Start())
	defer ctx.Stop()

	ep, err := ctx.CreateEndpoint("direct:start")
	require.NoError(t, err)
	producer, err := ep.CreateProducer()
	require.NoError(t, err)
	require.NoError(t, producer.Start(context.Background()))
	defer producer.Stop()

	// The interceptor sees the wrapped ErrStopRouting inside the route; the
	// direct: producer does not propagate a called route's stop to its caller.
	err = producer.Send(gocamel.NewExchange(context.Background()))
	require.NoError(t, err)

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	assert.Empty(t, spans[0].Events,
		"a wrapped ErrStopRouting is a control signal, not a failure: no error event must be recorded")
	assert.NotEqual(t, codes.Error, spans[0].Status.Code)

	// Contrast: a plain error IS recorded.
	exporter.Reset()

	ep2, err := ctx.CreateEndpoint("direct:fail")
	require.NoError(t, err)
	producer2, err := ep2.CreateProducer()
	require.NoError(t, err)
	require.NoError(t, producer2.Start(context.Background()))
	defer producer2.Stop()

	err = producer2.Send(gocamel.NewExchange(context.Background()))
	require.Error(t, err)

	spans = exporter.GetSpans()
	require.Len(t, spans, 1)
	assert.NotEmpty(t, spans[0].Events,
		"a plain error must be recorded on the span")
	assert.Equal(t, codes.Error, spans[0].Status.Code)
}

func TestTracingWith_RestoresContext(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSampler(trace.AlwaysSample()),
		trace.WithSpanProcessor(trace.NewSimpleSpanProcessor(exporter)),
	)

	type ctxKey struct{}
	initialCtx := context.WithValue(context.Background(), ctxKey{}, "initial-val")

	ctx := gocamel.NewCamelContext()
	ctx.AddComponent("direct", gocamel.NewDirectComponent())

	var insideCtx context.Context
	_, err := ctx.CreateRouteBuilder().
		From("direct:custom-tp").
		Intercept(TracingWith(tp)).
		ProcessFunc(func(e *gocamel.Exchange) error {
			insideCtx = e.Context
			assert.Equal(t, "initial-val", insideCtx.Value(ctxKey{}))
			return nil
		}).
		Build()
	require.NoError(t, err)

	require.NoError(t, ctx.Start())
	defer ctx.Stop()

	ep, err := ctx.CreateEndpoint("direct:custom-tp")
	require.NoError(t, err)
	producer, err := ep.CreateProducer()
	require.NoError(t, err)
	require.NoError(t, producer.Start(context.Background()))
	defer producer.Stop()

	exchange := gocamel.NewExchange(initialCtx)
	err = producer.Send(exchange)
	require.NoError(t, err)

	// Inside route, context had the span attached (different context)
	assert.NotNil(t, insideCtx)
	assert.NotEqual(t, initialCtx, insideCtx)

	// Outside route, context was restored to initialCtx
	assert.Equal(t, initialCtx, exchange.Context)

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "route:route-1", spans[0].Name)
}
