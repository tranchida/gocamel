// Package otel provides an OpenTelemetry tracing interceptor for gocamel routes.
package otel

import (
	"context"
	"errors"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	gocamel "github.com/tranchida/gocamel"
)

const (
	instrumentationName = "github.com/tranchida/gocamel/observability/otel"
)

// Tracing returns a RouteInterceptor that wraps every exchange in an
// OpenTelemetry span named after the route using the global TracerProvider.
//
//	route.Intercept(otel.Tracing())
func Tracing() gocamel.RouteInterceptor {
	return TracingWith(nil)
}

// TracingWith returns a RouteInterceptor using the provided TracerProvider.
// If tp is nil, otel.GetTracerProvider() is used.
func TracingWith(tp trace.TracerProvider) gocamel.RouteInterceptor {
	return func(route *gocamel.Route, next func(*gocamel.Exchange) error) func(*gocamel.Exchange) error {
		var tracer trace.Tracer
		if tp != nil {
			tracer = tp.Tracer(instrumentationName)
		} else {
			tracer = otel.Tracer(instrumentationName)
		}
		return func(exchange *gocamel.Exchange) error {
			prevCtx := exchange.Context
			ctx := prevCtx
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
			defer func() {
				exchange.Context = prevCtx
			}()

			err := next(exchange)
			// ErrStopRouting is a control signal, not a failure — and it may
			// be wrapped by the processor that returned it, so compare with
			// errors.Is like the core does (route.processFrom), not by value.
			if err != nil && !errors.Is(err, gocamel.ErrStopRouting) {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
			}
			return err
		}
	}
}
