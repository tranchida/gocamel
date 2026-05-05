package gocamel

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
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
