package ai

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	aiChatDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "rail_ai_chat_duration_seconds",
			Help:    "AI chat request latency",
			Buckets: []float64{0.5, 1, 2, 5, 10, 30},
		},
		[]string{"provider"},
	)

	aiToolCallsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "rail_ai_tool_calls_total",
			Help: "Total AI tool calls by tool name and status",
		},
		[]string{"tool", "status"},
	)

	aiChatTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "rail_ai_chat_total",
			Help: "Total AI chat requests by status",
		},
		[]string{"status"},
	)

	aiTokensUsed = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "rail_ai_tokens_total",
			Help: "Total tokens consumed by provider",
		},
		[]string{"provider"},
	)

	aiCostCeilingHits = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "rail_ai_cost_ceiling_hits_total",
			Help: "Number of requests blocked by cost ceiling",
		},
	)
)

// observeChat records metrics for a completed chat request.
func observeChat(provider string, duration time.Duration, tokens int, err error) {
	status := "success"
	if err != nil {
		status = "error"
	}
	aiChatTotal.WithLabelValues(status).Inc()
	aiChatDuration.WithLabelValues(provider).Observe(duration.Seconds())
	if tokens > 0 {
		aiTokensUsed.WithLabelValues(provider).Add(float64(tokens))
	}
}

// observeToolCall records metrics for a tool execution.
func observeToolCall(tool string, err error) {
	status := "success"
	if err != nil {
		status = "error"
	}
	aiToolCallsTotal.WithLabelValues(tool, status).Inc()
}

// observeCostCeilingHit records a cost ceiling block.
func observeCostCeilingHit() {
	aiCostCeilingHits.Inc()
}
