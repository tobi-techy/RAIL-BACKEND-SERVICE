package ai

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
)

const ToolGetMerchantInsights = "get_merchant_insights"

// MerchantProfile represents spending profile for a merchant.
type MerchantProfile struct {
	Merchant         string `json:"merchant"`
	VisitCount       int    `json:"visit_count"`
	TotalSpent       string `json:"total_spent"`
	AvgSpend         string `json:"avg_spend"`
	LastVisit        string `json:"last_visit"`
	MonthlyAvg       string `json:"monthly_avg"`
	TrendVsLastMonth string `json:"trend_vs_last_month"`
	Category         string `json:"category"`
}

// MerchantAnalyzer provides merchant spending intelligence.
type MerchantAnalyzer interface {
	GetMerchantProfile(ctx context.Context, userID uuid.UUID, merchant string) (*MerchantProfile, error)
	GetTopMerchants(ctx context.Context, userID uuid.UUID, limit int) ([]MerchantProfile, error)
}

// SetMerchantAnalyzer sets the merchant analyzer provider.
// Deprecated: Use NewOrchestratorWithDeps instead.
func (o *AgentAdapter) SetMerchantAnalyzer(m MerchantAnalyzer) {
	o.merchantAnalyzer = m
}

// MerchantInsightsTool returns the tool definition for merchant intelligence.
func MerchantInsightsTool() infraai.Tool {
	return infraai.Tool{
		Name:        ToolGetMerchantInsights,
		Description: "Get detailed spending profile for a specific merchant or all frequent merchants. Shows visit frequency, average spend, trends.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"merchant": map[string]interface{}{"type": "string", "description": "Merchant name to analyze. If empty, returns top merchants."},
			},
		},
	}
}

func (o *AgentAdapter) executeMerchantInsights(ctx context.Context, userID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	if o.merchantAnalyzer == nil {
		return map[string]interface{}{"error": "merchant analysis not available"}, nil
	}

	merchant, _ := args["merchant"].(string)

	if merchant != "" {
		profile, err := o.merchantAnalyzer.GetMerchantProfile(ctx, userID, merchant)
		if err != nil {
			return nil, fmt.Errorf("merchant profile: %w", err)
		}
		if profile == nil {
			return map[string]interface{}{"message": fmt.Sprintf("No spending data found for %s", merchant)}, nil
		}
		return map[string]interface{}{"merchant": profile}, nil
	}

	profiles, err := o.merchantAnalyzer.GetTopMerchants(ctx, userID, 10)
	if err != nil {
		return nil, fmt.Errorf("top merchants: %w", err)
	}

	items := make([]map[string]interface{}, len(profiles))
	for i, p := range profiles {
		items[i] = map[string]interface{}{
			"merchant":            p.Merchant,
			"visit_count":         p.VisitCount,
			"total_spent":         p.TotalSpent,
			"avg_spend":           p.AvgSpend,
			"last_visit":          p.LastVisit,
			"monthly_avg":         p.MonthlyAvg,
			"trend_vs_last_month": p.TrendVsLastMonth,
			"category":            p.Category,
		}
	}

	return map[string]interface{}{"merchants": items, "count": len(items)}, nil
}
