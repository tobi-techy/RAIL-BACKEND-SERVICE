package entities

import "github.com/shopspring/decimal"

// InsightCard is a structured visual block the iOS app renders as a rich card.
type InsightCard struct {
	Type      string      `json:"type"`
	Title     string      `json:"title"`
	Subtitle  string      `json:"subtitle,omitempty"`
	Sentiment string      `json:"sentiment,omitempty"`
	Data      interface{} `json:"data,omitempty"`
}

// StatItem is a single metric in a stat_grid card.
type StatItem struct {
	Label     string `json:"label"`
	Value     string `json:"value"`
	Change    string `json:"change,omitempty"`
	Sentiment string `json:"sentiment,omitempty"`
	Icon      string `json:"icon,omitempty"`
}

// ChartData powers a line/bar chart card.
type ChartData struct {
	ChartType   string           `json:"chart_type"`
	Points      []ChartPoint     `json:"points"`
	YLabel      string           `json:"y_label,omitempty"`
	Annotations []ChartAnnotation `json:"annotations,omitempty"`
}

// ChartPoint is a single data point.
type ChartPoint struct {
	Label string          `json:"label"`
	Value decimal.Decimal `json:"value"`
}

// ChartAnnotation marks a notable point on a chart.
type ChartAnnotation struct {
	Label string          `json:"label"`
	Value decimal.Decimal `json:"value"`
	Index int             `json:"index"`
	Type  string          `json:"type"` // "peak", "milestone", "highlight"
}

// BreakdownItem is a row in a breakdown card.
type BreakdownItem struct {
	Label   string          `json:"label"`
	Amount  decimal.Decimal `json:"amount"`
	Percent decimal.Decimal `json:"percent"`
	Color   string          `json:"color,omitempty"`
	Icon    string          `json:"icon,omitempty"`
}

// ProgressData powers a progress/gauge card.
type ProgressData struct {
	Current decimal.Decimal `json:"current"`
	Target  decimal.Decimal `json:"target"`
	Unit    string          `json:"unit,omitempty"`
	Label   string          `json:"label,omitempty"`
}

// ProjectionData powers a savings projection card.
type ProjectionData struct {
	Points     []ChartPoint      `json:"points"`
	Milestones map[string]int    `json:"milestones,omitempty"`
	FinalValue string            `json:"final_value"`
	YieldTotal string            `json:"yield_total"`
}

// ShareableCard wraps an InsightCard with sharing metadata.
type ShareableCard struct {
	Card      InsightCard `json:"card"`
	ShareText string      `json:"share_text"`
	Hashtag   string      `json:"hashtag,omitempty"`
}
