package entities

import "github.com/shopspring/decimal"

// InsightCard is a structured visual block the iOS app renders as a rich card.
// The backend returns these alongside text responses so the app can show
// charts, stat grids, progress bars, and breakdowns — not just plain text.
//
// Card types and their Data fields:
//
//   "stat_grid"     → Stats []StatItem
//   "chart"         → ChartData
//   "breakdown"     → BreakdownItems []BreakdownItem
//   "progress"      → ProgressData
//   "highlight"     → (uses Title + Subtitle only)
//   "alert"         → (uses Title + Subtitle + Sentiment)

type InsightCard struct {
	Type      string      `json:"type"`                 // stat_grid, chart, breakdown, progress, highlight, alert
	Title     string      `json:"title"`
	Subtitle  string      `json:"subtitle,omitempty"`
	Sentiment string      `json:"sentiment,omitempty"`  // positive, negative, neutral
	Data      interface{} `json:"data,omitempty"`
}

// StatItem is a single metric in a stat_grid card.
type StatItem struct {
	Label     string `json:"label"`
	Value     string `json:"value"`
	Change    string `json:"change,omitempty"`     // "+12.5%" or "-3.2%"
	Sentiment string `json:"sentiment,omitempty"`  // positive, negative, neutral
}

// ChartData powers a line/bar chart card.
type ChartData struct {
	ChartType string       `json:"chart_type"` // line, bar
	Points    []ChartPoint `json:"points"`
	YLabel    string       `json:"y_label,omitempty"`
}

// ChartPoint is a single data point.
type ChartPoint struct {
	Label string          `json:"label"` // date or category
	Value decimal.Decimal `json:"value"`
}

// BreakdownItem is a row in a breakdown card (spending categories, allocations).
type BreakdownItem struct {
	Label   string          `json:"label"`
	Amount  decimal.Decimal `json:"amount"`
	Percent decimal.Decimal `json:"percent"`
	Color   string          `json:"color,omitempty"` // hex color for iOS
}

// ProgressData powers a progress/gauge card.
type ProgressData struct {
	Current decimal.Decimal `json:"current"`
	Target  decimal.Decimal `json:"target"`
	Unit    string          `json:"unit,omitempty"` // "$", "days", etc.
}
