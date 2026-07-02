package prometheus

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gocamel "gitlab.com/tranchida/gocamel"
)

func TestMetricsInterceptor(t *testing.T) {
	ctx := gocamel.NewCamelContext()
	ctx.AddComponent("direct", gocamel.NewDirectComponent())

	_, err := ctx.CreateRouteBuilder().
		From("direct:start").
		Intercept(Metrics()).
		ProcessFunc(func(e *gocamel.Exchange) error {
			time.Sleep(10 * time.Millisecond)
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

	// Check metrics via HTTP handler
	handler := promhttp.Handler()
	req := httptest.NewRequest("GET", "/metrics", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	body := rr.Body.String()
	assert.Contains(t, body, "gocamel_exchanges_total")
	assert.Contains(t, body, "route_id=\"route-1\"")
	assert.Contains(t, body, "status=\"success\"")
}
