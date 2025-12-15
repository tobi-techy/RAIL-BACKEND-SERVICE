package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// OffRampTotal tracks total off-ramp attempts
	OffRampTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "offramp_total",
			Help: "Total number of off-ramp attempts",
		},
		[]string{"status"},
	)

	// OffRampDuration tracks off-ramp processing duration
	OffRampDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "offramp_duration_seconds",
			Help:    "Off-ramp processing duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"status"},
	)

	// OffRampRetries tracks retry attempts
	OffRampRetries = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "offramp_retries_total",
			Help: "Total number of off-ramp retry attempts",
		},
		[]string{"reason"},
	)

	// OffRampAmount tracks off-ramp amounts
	OffRampAmount = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "offramp_amount_usd",
			Help:    "Off-ramp amount in USD",
			Buckets: []float64{10, 50, 100, 500, 1000, 5000, 10000},
		},
		[]string{"status"},
	)
)
