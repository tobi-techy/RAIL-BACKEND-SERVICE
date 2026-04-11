package entities

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// AIUsage tracks AI resource consumption per user per billing period.
type AIUsage struct {
	ID            uuid.UUID          `json:"id" db:"id"`
	UserID        uuid.UUID          `json:"user_id" db:"user_id"`
	PeriodStart   time.Time          `json:"period_start" db:"period_start"`
	MessageCount  int                `json:"message_count" db:"message_count"`
	VoiceSeconds  int                `json:"voice_seconds" db:"voice_seconds"`
	EstimatedCost decimal.Decimal    `json:"estimated_cost_usd" db:"estimated_cost_usd"`
	ModelCalls    map[string]int     `json:"model_calls" db:"model_calls"`
	UpdatedAt     time.Time          `json:"updated_at" db:"updated_at"`
}

// CostCeilingUSD is the per-user monthly cost threshold above which
// the AI agent degrades to the cheap model and disables voice responses.
var CostCeilingUSD = decimal.NewFromFloat(3.00)

// ModelPricing maps model identifiers to cost-per-token (output).
// Input tokens are roughly half the cost; we use output pricing as a
// conservative upper-bound estimate. Update when provider prices change.
var ModelPricing = map[string]decimal.Decimal{
	"gpt-4o":      decimal.NewFromFloat(0.00001),   // $10/1M tokens
	"gpt-4o-mini": decimal.NewFromFloat(0.0000006), // $0.60/1M tokens
	"gemini-pro":  decimal.NewFromFloat(0.000007),   // $7/1M tokens
}

// DefaultTokenCost is used when the model is not in the pricing table.
var DefaultTokenCost = decimal.NewFromFloat(0.00001)

// EstimateCost calculates the estimated cost for a given model and token count.
func EstimateCost(model string, tokens int) decimal.Decimal {
	perToken, ok := ModelPricing[model]
	if !ok {
		perToken = DefaultTokenCost
	}
	return perToken.Mul(decimal.NewFromInt(int64(tokens)))
}
