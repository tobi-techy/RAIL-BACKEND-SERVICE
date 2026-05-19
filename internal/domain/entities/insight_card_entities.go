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
	Points     []ChartPoint   `json:"points"`
	Milestones map[string]int `json:"milestones,omitempty"`
	FinalValue string         `json:"final_value"`
	YieldTotal string         `json:"yield_total"`
}

// ─── New Insight Card Types ───────────────────────────────────────────────────

// TipData powers a "tip" card — actionable financial advice with optional CTA.
type TipData struct {
	Emoji       string `json:"emoji,omitempty"`
	Message     string `json:"message"`
	ActionLabel string `json:"action_label,omitempty"`
	ActionRoute string `json:"action_route,omitempty"`
	Severity    string `json:"severity,omitempty"` // "info", "warning", "success"
}

// SubscriptionItem is a single subscription in an audit card.
type SubscriptionItem struct {
	Name      string          `json:"name"`
	Amount    decimal.Decimal `json:"amount"`
	Frequency string          `json:"frequency"` // "monthly", "yearly", "weekly"
	Category  string          `json:"category,omitempty"`
	LogoURL   string          `json:"logo_url,omitempty"`
	Status    string          `json:"status,omitempty"` // "active", "trial", "cancellable"
}

// SubscriptionAuditData powers a "subscription_audit" card.
type SubscriptionAuditData struct {
	TotalMonthly  decimal.Decimal    `json:"total_monthly"`
	TotalYearly   decimal.Decimal    `json:"total_yearly"`
	Subscriptions []SubscriptionItem `json:"subscriptions"`
	SavingsTip    string             `json:"savings_tip,omitempty"`
}

// RunwayData powers a "runway" card — how long money lasts at current burn.
type RunwayData struct {
	Months       int             `json:"months"`
	Days         int             `json:"days"`
	Status       string          `json:"status"` // "healthy", "caution", "critical"
	DailyBurn    decimal.Decimal `json:"daily_burn"`
	Balance      decimal.Decimal `json:"balance"`
	ProjectedEnd string          `json:"projected_end,omitempty"` // date string
}

// DepositPatternData powers a "deposit_pattern" card.
type DepositPatternData struct {
	Frequency     string          `json:"frequency"`      // "weekly", "biweekly", "monthly", "irregular"
	AverageAmount decimal.Decimal `json:"average_amount"`
	LastDeposit   string          `json:"last_deposit"`   // date string
	NextExpected  string          `json:"next_expected"`  // date string
	Streak        int             `json:"streak"`         // consecutive on-time deposits
	Consistency   int             `json:"consistency"`    // 0-100 score
	History       []ChartPoint    `json:"history,omitempty"`
}

// YieldSummaryData powers a "yield_summary" card — stash yield performance.
type YieldSummaryData struct {
	TotalEarned   decimal.Decimal `json:"total_earned"`
	MonthEarned   decimal.Decimal `json:"month_earned"`
	CurrentAPY    decimal.Decimal `json:"current_apy"`
	StashBalance  decimal.Decimal `json:"stash_balance"`
	DailyEstimate decimal.Decimal `json:"daily_estimate"`
	GrowthPoints  []ChartPoint    `json:"growth_points,omitempty"`
}

// ComparisonItem is one side of a comparison.
type ComparisonItem struct {
	Label string          `json:"label"`
	Value decimal.Decimal `json:"value"`
	Color string          `json:"color,omitempty"`
}

// ComparisonData powers a "comparison" card — side-by-side metric comparison.
type ComparisonData struct {
	MetricLabel string         `json:"metric_label"`
	Current     ComparisonItem `json:"current"`
	Previous    ComparisonItem `json:"previous"`
	DeltaPct    decimal.Decimal `json:"delta_pct"`
	Sentiment   string         `json:"sentiment,omitempty"`
}

// ShareableCard wraps an InsightCard with sharing metadata.
type ShareableCard struct {
	Card      InsightCard `json:"card"`
	ShareText string      `json:"share_text"`
	Hashtag   string      `json:"hashtag,omitempty"`
}
