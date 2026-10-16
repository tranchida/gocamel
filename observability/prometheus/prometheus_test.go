package prometheus

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gocamel "github.com/tranchida/gocamel"
)

// newIsolatedContext builds a started CamelContext with one direct route whose
// single processor is proc, wrapped in the MetricsWith interceptor registered
// on reg. The returned context is stopped automatically.
func newIsolatedContext(t *testing.T, reg prometheus.Registerer, proc gocamel.Processor) *gocamel.CamelContext {
	t.Helper()

	ctx := gocamel.NewCamelContext()
	ctx.AddComponent("direct", gocamel.NewDirectComponent())

	_, err := ctx.CreateRouteBuilder().
		From("direct:start").
		Intercept(MetricsWith(reg)).
		Process(proc).
		Build()
	require.NoError(t, err)

	require.NoError(t, ctx.Start())
	t.Cleanup(func() { _ = ctx.Stop() })

	return ctx
}

// sendExchange sends one exchange and returns the processor's error as seen
// by the caller (direct endpoints propagate the processor error through Send).
func sendExchange(t *testing.T, ctx *gocamel.CamelContext) error {
	t.Helper()

	ep, err := ctx.CreateEndpoint("direct:start")
	require.NoError(t, err)
	producer, err := ep.CreateProducer()
	require.NoError(t, err)
	require.NoError(t, producer.Start(context.Background()))
	t.Cleanup(func() { _ = producer.Stop() })

	return producer.Send(gocamel.NewExchange(context.Background()))
}

func scrape(t *testing.T, reg prometheus.Gatherer) string {
	t.Helper()

	handler := promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
	req := httptest.NewRequest("GET", "/metrics", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr.Body.String()
}

func TestMetricsInterceptor(t *testing.T) {
	reg := prometheus.NewRegistry()

	ctx := newIsolatedContext(t, reg, gocamel.ProcessorFunc(func(e *gocamel.Exchange) error {
		time.Sleep(10 * time.Millisecond)
		return nil
	}))
	sendExchange(t, ctx)

	body := scrape(t, reg)
	assert.Contains(t, body, "gocamel_exchanges_total")
	assert.Contains(t, body, `route_id="route-1"`)
	assert.Contains(t, body, `status="success"`)
	assert.Contains(t, body, "gocamel_exchange_duration_seconds")
	assert.Contains(t, body, "gocamel_exchanges_inflight")
}

// TestMetricsInterceptor_ClassifiesFailures verifies that a failing processor
// is counted as status="failure" while a plain ErrStopRouting is a success.
func TestMetricsInterceptor_ClassifiesFailures(t *testing.T) {
	reg := prometheus.NewRegistry()

	// Plain ErrStopRouting is a control signal: counted as success.
	ctx := newIsolatedContext(t, reg, gocamel.ProcessorFunc(func(e *gocamel.Exchange) error {
		return gocamel.ErrStopRouting
	}))
	// The interceptor sees ErrStopRouting as the route result, which is what
	// the classification tests; the direct: producer does not propagate a
	// called route's stop to its caller.
	err := sendExchange(t, ctx)
	require.NoError(t, err)

	body := scrape(t, reg)
	assert.Contains(t, body, `gocamel_exchanges_total{route_id="route-1",status="success"} 1`,
		"plain ErrStopRouting must not be counted as a failure")
	assert.NotContains(t, body, `status="failure"`)

	// A wrapped ErrStopRouting must ALSO be a success: it is the same control
	// signal with context attached by the returning processor. A fresh registry
	// is used here to isolate the test scrape metrics.
	reg2 := prometheus.NewRegistry()
	ctx2 := newIsolatedContext(t, reg2, gocamel.ProcessorFunc(func(e *gocamel.Exchange) error {
		return fmt.Errorf("filter rejected message: %w", gocamel.ErrStopRouting)
	}))
	err = sendExchange(t, ctx2)
	require.NoError(t, err)

	body = scrape(t, reg2)
	assert.Contains(t, body, `gocamel_exchanges_total{route_id="route-1",status="success"} 1`,
		"a wrapped ErrStopRouting must not be counted as a failure")
	assert.NotContains(t, body, `status="failure"`,
		"no exchange in this test may be classified as a failure")
}

// TestMetricsInterceptor_ClassifiesRealFailures verifies that ordinary errors
// are still counted as failures.
func TestMetricsInterceptor_ClassifiesRealFailures(t *testing.T) {
	reg := prometheus.NewRegistry()

	ctx := newIsolatedContext(t, reg, gocamel.ProcessorFunc(func(e *gocamel.Exchange) error {
		return errors.New("boom")
	}))
	err := sendExchange(t, ctx)
	require.Error(t, err)

	body := scrape(t, reg)
	assert.Contains(t, body, `gocamel_exchanges_total{route_id="route-1",status="failure"} 1`)
}
