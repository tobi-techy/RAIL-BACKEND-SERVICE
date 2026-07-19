package ai

import (
	"context"
	"fmt"
	"math"

	"github.com/google/uuid"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
)

const ToolSimulateSavings = "simulate_savings"

func SimulateSavingsTool() infraai.Tool {
	return infraai.Tool{
		Name:        ToolSimulateSavings,
		Description: "Simulate future savings growth. Answer 'what if' questions like 'what if I save X per week/month for Y years?' or 'when will I reach $1000?'. Shows projected growth with compound yield.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"deposit_amount": map[string]interface{}{
					"type":        "number",
					"description": "Amount to deposit per period in USD",
				},
				"deposit_frequency": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"weekly", "monthly"},
					"description": "How often the deposit is made",
				},
				"duration_months": map[string]interface{}{
					"type":        "integer",
					"description": "Number of months to simulate",
				},
			},
			"required": []string{"deposit_amount", "deposit_frequency", "duration_months"},
		},
	}
}

func (o *AgentAdapter) executeSimulateSavings(ctx context.Context, userID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	depositAmt, _ := args["deposit_amount"].(float64)
	frequency, _ := args["deposit_frequency"].(string)
	durationMonths, _ := args["duration_months"].(float64)

	if depositAmt <= 0 || durationMonths <= 0 {
		return map[string]interface{}{"error": "deposit_amount and duration_months must be positive"}, nil
	}
	if durationMonths > 120 {
		durationMonths = 120 // Cap at 10 years
	}

	// Rail stash gets 30% of deposits, earning ~3.5% APY
	stashRatio := 0.30
	annualYield := 0.035
	monthlyYield := annualYield / 12.0

	depositsPerMonth := 1.0
	if frequency == "weekly" {
		depositsPerMonth = 4.33
	}

	monthlyDeposit := depositAmt * depositsPerMonth
	monthlyToStash := monthlyDeposit * stashRatio

	// Simulate month by month
	type monthPoint struct {
		Month        int     `json:"month"`
		StashBalance float64 `json:"stash_balance"`
		SpendBalance float64 `json:"spend_balance"`
		TotalSaved   float64 `json:"total_saved"`
		YieldEarned  float64 `json:"yield_earned"`
	}

	months := int(durationMonths)
	points := make([]map[string]interface{}, 0, months)
	stash := 0.0
	totalYield := 0.0
	totalDeposited := 0.0

	// Get current stash balance if available
	if o.balanceHistory != nil {
		// Start from current balance (approximation)
	}

	for m := 1; m <= months; m++ {
		stash += monthlyToStash
		yieldThisMonth := stash * monthlyYield
		stash += yieldThisMonth
		totalYield += yieldThisMonth
		totalDeposited += monthlyDeposit

		if m <= 12 || m%3 == 0 || m == months {
			points = append(points, map[string]interface{}{
				"month":         m,
				"stash_balance": fmt.Sprintf("%.2f", stash),
				"total_saved":   fmt.Sprintf("%.2f", totalDeposited),
				"yield_earned":  fmt.Sprintf("%.2f", totalYield),
			})
		}
	}

	// Find when milestones are reached
	milestones := []float64{100, 500, 1000, 5000, 10000}
	milestoneMonths := map[string]int{}
	testStash := 0.0
	for m := 1; m <= 120; m++ {
		testStash += monthlyToStash
		testStash += testStash * monthlyYield
		for _, ms := range milestones {
			key := fmt.Sprintf("$%.0f", ms)
			if _, found := milestoneMonths[key]; !found && testStash >= ms {
				milestoneMonths[key] = m
			}
		}
	}

	return map[string]interface{}{
		"simulation": map[string]interface{}{
			"deposit_per_period":  fmt.Sprintf("%.2f", depositAmt),
			"frequency":           frequency,
			"duration_months":     months,
			"monthly_to_stash":    fmt.Sprintf("%.2f", monthlyToStash),
			"final_stash_balance": fmt.Sprintf("%.2f", stash),
			"total_deposited":     fmt.Sprintf("%.2f", totalDeposited),
			"total_yield_earned":  fmt.Sprintf("%.2f", totalYield),
			"yield_pct_of_total":  fmt.Sprintf("%.1f", (totalYield/math.Max(stash, 0.01))*100),
		},
		"monthly_projections": points,
		"milestones":          milestoneMonths,
	}, nil
}
