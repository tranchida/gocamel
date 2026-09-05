package prometheus

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
)

func TestReviewMetricsMultipleRoutes(t *testing.T) {
	reg := prometheus.NewRegistry()
	_ = MetricsWith(reg)
	assert.NotPanics(t, func() { _ = MetricsWith(reg) })
}
