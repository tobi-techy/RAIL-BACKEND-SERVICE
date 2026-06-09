package ai

import (
	"time"
)

// PersonalityMetrics tracks engagement signals for A/B testing personality changes.
type PersonalityMetrics struct {
	// Response quality
	QualityGatePassed  bool     `json:"quality_gate_passed"`
	QualityGateRetried bool     `json:"quality_gate_retried"`
	Failures           []string `json:"failures,omitempty"`

	// Engagement signals (measured client-side, logged server-side)
	ResponseLengthChars int `json:"response_length_chars"`
	ToolsUsed           int `json:"tools_used"`

	// Timing
	ContextAssemblyMs int64 `json:"context_assembly_ms"`
	FirstTokenMs      int64 `json:"first_token_ms,omitempty"`
	TotalMs           int64 `json:"total_ms"`

	// Routing
	ToolCategory     ToolCategory `json:"tool_category"`
	ToolCategoryName string       `json:"tool_category_name,omitempty"`
	ToolsOffered     int          `json:"tools_offered"`
}

// ToolCategoryToName converts a ToolCategory int to a human-readable string.
func ToolCategoryToName(cat ToolCategory) string {
	switch cat {
	case CategoryOverview:
		return "overview"
	case CategorySpending:
		return "spending"
	case CategoryAction:
		return "action"
	case CategoryPlanning:
		return "planning"
	case CategoryHistory:
		return "history"
	case CategoryAutomation:
		return "automation"
	case CategoryFull:
		return "full"
	default:
		return "unknown"
	}
}

// TrackPersonalityEvent is called after each chat response to log engagement data.
// This feeds into A/B test analysis — compare sessions before/after personality changes.
func TrackPersonalityEvent(metrics PersonalityMetrics) map[string]interface{} {
	catName := metrics.ToolCategoryName
	if catName == "" {
		catName = ToolCategoryToName(metrics.ToolCategory)
	}
	return map[string]interface{}{
		"event":                "miriam_response_quality",
		"timestamp":            time.Now().UTC().Format(time.RFC3339),
		"quality_gate_passed":  metrics.QualityGatePassed,
		"quality_gate_retried": metrics.QualityGateRetried,
		"failures":             metrics.Failures,
		"response_length":      metrics.ResponseLengthChars,
		"tools_used":           metrics.ToolsUsed,
		"context_assembly_ms":  metrics.ContextAssemblyMs,
		"first_token_ms":       metrics.FirstTokenMs,
		"total_ms":             metrics.TotalMs,
		"tool_category":        int(metrics.ToolCategory),
		"tool_category_name":   catName,
		"tools_offered":        metrics.ToolsOffered,
	}
}

// Client-side engagement metrics (sent back from the app):
// - reply_rate: did the user send another message within 60s? (conversation continues)
// - session_length: how many exchanges in this session?
// - screenshot_shared: did user share/screenshot the response?
// - reaction_tapped: did user tap a quick-reaction chip?
//
// These are tracked via the existing analytics pipeline (not implemented here).
// The personality_metrics above provide the SERVER-SIDE correlation data.
