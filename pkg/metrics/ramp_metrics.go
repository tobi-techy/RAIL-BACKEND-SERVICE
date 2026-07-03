package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// RampProviderTotal counts which provider actually served each ramp
	// operation (op="onramp"|"offramp", provider="ramphub"|"paj"). The ratio of
	// ramphub to paj is RampHub's real-world success rate — before this existed,
	// RampHub could fail 100% of the time and stay invisible because the Paj
	// fallback silently absorbed it.
	RampProviderTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ramp_provider_total",
			Help: "Ramp operations by the provider that served them",
		},
		[]string{"op", "provider"},
	)

	// RampHubFallbackTotal counts every time a ramp operation fell back from
	// RampHub to Paj, labelled by reason so RampHub outages are diagnosable.
	RampHubFallbackTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ramphub_fallback_total",
			Help: "Ramp operations that fell back from RampHub to Paj, by reason",
		},
		[]string{"op", "reason"},
	)
)

// RecordRampProvider records that op ("onramp"/"offramp") was served by provider
// ("ramphub"/"paj").
func RecordRampProvider(op, provider string) {
	RampProviderTotal.WithLabelValues(op, provider).Inc()
}

// RecordRampFallback records a RampHub→Paj fallback for op with the given reason.
func RecordRampFallback(op, reason string) {
	RampHubFallbackTotal.WithLabelValues(op, reason).Inc()
}
