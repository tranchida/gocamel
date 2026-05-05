package gocamel

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	instrumentationName = "gitlab.com/tranchida/gocamel"
)

// EnableTracing active le traçage OpenTelemetry pour la route.
func (r *Route) EnableTracing() *Route {
	r.tracing = true
	return r
}

func (r *Route) traceProcess(exchange *Exchange) error {
	ctx := exchange.Context
	if ctx == nil {
		ctx = context.Background()
	}

	tracer := otel.Tracer(instrumentationName)
	ctx, span := tracer.Start(ctx, "route:"+r.ID,
		trace.WithAttributes(
			attribute.String("camel.route.id", r.ID),
			attribute.String("camel.exchange.id", fmt.Sprintf("%p", exchange)),
		),
	)
	defer span.End()

	exchange.Context = ctx
	err := r.doProcess(exchange)
	if err != nil && err != ErrStopRouting {
		span.RecordError(err)
	}
	return err
}
