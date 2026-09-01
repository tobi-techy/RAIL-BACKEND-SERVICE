package ai

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	aiintelligence "github.com/rail-service/rail_service/internal/domain/services/ai/intelligence"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/shopspring/decimal"
)

const ToolGetBalanceHistory = "get_balance_history"

// BalanceHistoryProvider returns stash balance snapshots over time.
// Deprecated: Use aiintelligence.BalanceHistoryProvider instead.
type BalanceHistoryProvider = aiintelligence.BalanceHistoryProvider

// SetBalanceHistory sets the balance history provider.
// Deprecated: Use NewOrchestratorWithDeps instead.
func (o *AgentAdapter) SetBalanceHistory(b BalanceHistoryProvider) {
	o.balanceHistory = b
}

// BalanceHistoryTool returns the tool definition.
func BalanceHistoryTool() infraai.Tool {
	return infraai.Tool{
		Name:        ToolGetBalanceHistory,
		Description: "Get stash (savings) balance history over time for charts. Shows how the user's savings have grown. Use when user asks about balance growth, savings progress, or wants to see their stash chart.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"period": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"last_7_days", "last_30_days", "last_90_days"},
					"description": "Time period for balance history",
				},
			},
		},
	}
}

func (o *AgentAdapter) executeBalanceHistory(ctx context.Context, userID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	if o.balanceHistory == nil {
		return map[string]interface{}{"error": "balance history not available"}, nil
	}

	now := time.Now().UTC()
	var from time.Time
	switch args["period"] {
	case "last_90_days":
		from = now.AddDate(0, 0, -90)
	case "last_30_days":
		from = now.AddDate(0, 0, -30)
	default:
		from = now.AddDate(0, 0, -7)
	}

	snapshots, err := o.balanceHistory.GetSnapshotsInWindow(ctx, userID, from, now)
	if err != nil {
		return nil, fmt.Errorf("balance history: %w", err)
	}

	points := make([]map[string]interface{}, len(snapshots))
	for i, s := range snapshots {
		points[i] = map[string]interface{}{
			"date":    s.RecordedAt.Format("2006-01-02"),
			"balance": s.Balance.String(),
		}
	}

	var growth decimal.Decimal
	if len(snapshots) >= 2 {
		first := snapshots[0].Balance
		last := snapshots[len(snapshots)-1].Balance
		if !first.IsZero() {
			growth = last.Sub(first).Div(first).Mul(decimal.NewFromInt(100))
		}
	}

	return map[string]interface{}{
		"data_points": points,
		"count":       len(snapshots),
		"growth_pct":  growth.StringFixed(2),
	}, nil
}
