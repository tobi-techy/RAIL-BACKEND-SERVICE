package ai

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
)

const ToolGetPriceChanges = "get_price_changes"

// PriceChange represents a tracked item's price movement over time.
type PriceChange struct {
	ItemName      string `json:"item_name"`
	PreviousPrice string `json:"previous_price"`
	CurrentPrice  string `json:"current_price"`
	ChangePct     string `json:"change_pct"`
	Currency      string `json:"currency"`
	FirstSeen     string `json:"first_seen"`
	LastSeen      string `json:"last_seen"`
	Occurrences   int    `json:"occurrences"`
}

// PriceTracker tracks item price changes across receipt scans.
type PriceTracker interface {
	GetPriceChanges(ctx context.Context, userID uuid.UUID, limit int) ([]PriceChange, error)
}

// SetPriceTracker sets the price tracking provider.
// Deprecated: Use NewOrchestratorWithDeps instead.
func (o *Orchestrator) SetPriceTracker(p PriceTracker) {
	o.priceTracker = p
}

// PriceTrackingTool returns the tool definition for price tracking.
func PriceTrackingTool() infraai.Tool {
	return infraai.Tool{
		Name:        ToolGetPriceChanges,
		Description: "Track price changes for items you buy regularly. Shows inflation impact on your groceries and frequent purchases.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"limit": map[string]interface{}{"type": "integer", "description": "Number of items to return (max 20)", "default": 10},
			},
		},
	}
}

func (o *Orchestrator) executePriceChanges(ctx context.Context, userID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	if o.priceTracker == nil {
		return map[string]interface{}{"error": "price tracking not available"}, nil
	}

	limit := 10
	if l, ok := args["limit"].(float64); ok && l > 0 && l <= 20 {
		limit = int(l)
	}

	changes, err := o.priceTracker.GetPriceChanges(ctx, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("price changes: %w", err)
	}

	items := make([]map[string]interface{}, len(changes))
	for i, c := range changes {
		items[i] = map[string]interface{}{
			"item_name":      c.ItemName,
			"previous_price": c.PreviousPrice,
			"current_price":  c.CurrentPrice,
			"change_pct":     c.ChangePct,
			"currency":       c.Currency,
			"first_seen":     c.FirstSeen,
			"last_seen":      c.LastSeen,
			"occurrences":    c.Occurrences,
		}
	}

	return map[string]interface{}{"price_changes": items, "count": len(items)}, nil
}
