// Package otel provides an OpenTelemetry tracing interceptor for gocamel routes.
package otel

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	gocamel "gitlab.com/tranchida/gocamel"
)

const (
	instrumentationName = "gitlab.com/tranchida/gocamel/observability/otel"
)

// Tracing returns a RouteInterceptor that wraps every exchange in an
// OpenTelemetry span named after the route.
//
//	route.Intercept(otel.Tracing())
func Tracing() gocamel.RouteInterceptor {
	return func(route *gocamel.Route, next func(*gocamel.Exchange) error) func(*gocamel.Exchange) error {
		tracer := otel.Tracer(instrumentationName)
		return func(exchange *gocamel.Exchange) error {
			ctx := exchange.Context
			if ctx == nil {
				ctx = context.Background()
			}

			ctx, span := tracer.Start(ctx, "route:"+route.ID,
				trace.WithAttributes(
					attribute.String("camel.route.id", route.ID),
					attribute.String("camel.exchange.id", exchange.ID),
				),
			)
			defer span.End()

			exchange.Context = ctx
			err := next(exchange)
			if err != nil && err != gocamel.ErrStopRouting {
				span.RecordError(err)
			}
			return err
		}
	}
}
