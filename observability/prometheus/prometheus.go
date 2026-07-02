// Package prometheus provides a Prometheus metrics interceptor for gocamel routes.
package prometheus

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	gocamel "gitlab.com/tranchida/gocamel"
)

var (
	exchangesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "gocamel_exchanges_total",
		Help: "The total number of processed exchanges",
	}, []string{"route_id", "status"})

	exchangesDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "gocamel_exchange_duration_seconds",
		Help:    "The duration of exchange processing in seconds",
		Buckets: prometheus.DefBuckets,
	}, []string{"route_id"})

	exchangesInFlight = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "gocamel_exchanges_inflight",
		Help: "The number of exchanges currently being processed",
	}, []string{"route_id"})
)

// Metrics returns a RouteInterceptor that records exchange counts, durations
// and in-flight gauges in the default Prometheus registry.
//
//	route.Intercept(prometheus.Metrics())
func Metrics() gocamel.RouteInterceptor {
	return func(route *gocamel.Route, next func(*gocamel.Exchange) error) func(*gocamel.Exchange) error {
		return func(exchange *gocamel.Exchange) error {
			start := time.Now()
			exchangesInFlight.WithLabelValues(route.ID).Inc()
			defer exchangesInFlight.WithLabelValues(route.ID).Dec()

			err := next(exchange)

			exchangesDuration.WithLabelValues(route.ID).Observe(time.Since(start).Seconds())

			status := "success"
			if err != nil && err != gocamel.ErrStopRouting {
				status = "failure"
			}
			exchangesTotal.WithLabelValues(route.ID, status).Inc()
			return err
		}
	}
}
