// Package prometheus provides a Prometheus metrics interceptor for gocamel routes.
package prometheus

import (
	"errors"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	gocamel "github.com/tranchida/gocamel"
)

// metricFamilies groups the three metric families exposed by this package.
// They are created per Registerer (not as package-level promauto defaults) so
// embedders can pass their own registry and tests stay isolated from each
// other and from the default registry.
type metricFamilies struct {
	exchangesTotal    *prometheus.CounterVec
	exchangesDuration *prometheus.HistogramVec
	exchangesInFlight *prometheus.GaugeVec
}

func newMetricFamilies(reg prometheus.Registerer) *metricFamilies {

	total := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "gocamel_exchanges_total",
		Help: "The total number of processed exchanges",
	}, []string{"route_id", "status"})

	duration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "gocamel_exchange_duration_seconds",
		Help:    "The duration of exchange processing in seconds",
		Buckets: prometheus.DefBuckets,
	}, []string{"route_id"})

	inFlight := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "gocamel_exchanges_inflight",
		Help: "The number of exchanges currently being processed",
	}, []string{"route_id"})

	return &metricFamilies{
		exchangesTotal:    registerMetric(reg, total),
		exchangesDuration: registerMetric(reg, duration),
		exchangesInFlight: registerMetric(reg, inFlight),
	}
}

// Metrics returns a RouteInterceptor that records exchange counts, durations
// and in-flight gauges in the default Prometheus registry.
//
//	route.Intercept(prometheus.Metrics())
func Metrics() gocamel.RouteInterceptor {
	return MetricsWith(prometheus.DefaultRegisterer)
}

// MetricsWith is like Metrics but registers the metric families on the given
// prometheus.Registerer instead of the default registry. Use it to embed the
// interceptor in an application that already owns a registry (custom
// collectors, push gateway setups) or to isolate test registrations.
//
// Calls using the same registry reuse its compatible metric families; each
// route still gets its own route_id label value.
func MetricsWith(reg prometheus.Registerer) gocamel.RouteInterceptor {
	families := newMetricFamilies(reg)

	return func(route *gocamel.Route, next func(*gocamel.Exchange) error) func(*gocamel.Exchange) error {
		return func(exchange *gocamel.Exchange) error {
			start := time.Now()
			families.exchangesInFlight.WithLabelValues(route.ID).Inc()
			defer families.exchangesInFlight.WithLabelValues(route.ID).Dec()

			err := next(exchange)

			families.exchangesDuration.WithLabelValues(route.ID).Observe(time.Since(start).Seconds())

			status := "success"
			// ErrStopRouting is a control signal, not a failure — and it may
			// be wrapped by the processor that returned it, so compare with
			// errors.Is like the core does (route.processFrom), not by value.
			if err != nil && !errors.Is(err, gocamel.ErrStopRouting) {
				status = "failure"
			}
			families.exchangesTotal.WithLabelValues(route.ID, status).Inc()
			return err
		}
	}
}

// registerMetric reuses the existing family when independently constructed
// interceptors target the same registry. Register handles concurrent callers.
func registerMetric[T prometheus.Collector](reg prometheus.Registerer, collector T) T {
	if reg == nil {
		return collector
	}
	if err := reg.Register(collector); err != nil {
		var registered prometheus.AlreadyRegisteredError
		if errors.As(err, &registered) {
			if existing, ok := registered.ExistingCollector.(T); ok {
				return existing
			}
		}
		panic(err) // incompatible collector definitions remain configuration errors
	}
	return collector
}
