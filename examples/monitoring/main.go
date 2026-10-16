package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/tranchida/gocamel"
	gocamelotel "github.com/tranchida/gocamel/observability/otel"
	gocamelprom "github.com/tranchida/gocamel/observability/prometheus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
)

func initTracer() (*sdktrace.TracerProvider, error) {
	exporter, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String("gocamel-monitoring-example"),
		)),
	)
	otel.SetTracerProvider(tp)
	return tp, nil
}

func main() {
	// 1. Initialize OpenTelemetry (export to stdout for this example).
	tp, err := initTracer()
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = tp.Shutdown(context.Background()) }()

	// 2. Initialize the GoCamel context. NewCamelContext already registers
	// the stdlib components (direct, timer, http, ...), so no AddComponent
	// call is needed here.
	ctx := gocamel.NewCamelContext()

	// 3. Define the route with monitoring.
	_, err = ctx.CreateRouteBuilder().
		From("timer:monitor?period=5000").
		SetID("monitoring-route").
		Intercept(gocamelprom.Metrics()). // Enable Prometheus metrics.
		Intercept(gocamelotel.Tracing()). // Enable OpenTelemetry tracing.
		ProcessFunc(func(e *gocamel.Exchange) error {
			fmt.Println("Processing exchange...")
			e.GetIn().SetBody("Hello Monitoring!")
			return nil
		}).
		To("direct:next").
		Build()

	if err != nil {
		log.Fatal(err)
	}

	// Destination route.
	_, err = ctx.CreateRouteBuilder().
		From("direct:next").
		SetID("destination-route").
		Intercept(gocamelprom.Metrics()).
		Intercept(gocamelotel.Tracing()).
		ProcessFunc(func(e *gocamel.Exchange) error {
			time.Sleep(100 * time.Millisecond) // Simulate processing.
			fmt.Printf("Received: %v\n", e.GetIn().GetBody())
			return nil
		}).
		Build()
	if err != nil {
		log.Fatal(err)
	}

	// 4. HTTP server to expose Prometheus metrics.
	go func() {
		fmt.Println("Prometheus metrics available at http://localhost:8080/metrics")
		http.Handle("/metrics", promhttp.Handler())
		srv := &http.Server{
			Addr:              ":8080",
			ReadHeaderTimeout: 10 * time.Second,
		}
		log.Fatal(srv.ListenAndServe())
	}()

	// 5. Start the context.
	if err := ctx.Start(); err != nil {
		log.Fatal(err)
	}

	fmt.Println("GoCamel started. Press Ctrl+C to stop.")
	select {}
}
