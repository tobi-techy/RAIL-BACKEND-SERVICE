package ai

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
)

const ToolGetWarrantyItems = "get_warranty_items"

// WarrantyTracker retrieves warranty-eligible items from receipts.
type WarrantyTracker interface {
	GetWarrantyItems(ctx context.Context, userID uuid.UUID) ([]entities.WarrantyItem, error)
}

// SetWarrantyTracker sets the warranty tracker provider.
func (o *Orchestrator) SetWarrantyTracker(w WarrantyTracker) {
	o.warrantyTracker = w
}

// WarrantyTool returns the tool definition for warranty tracking.
func WarrantyTool() infraai.Tool {
	return infraai.Tool{
		Name:        ToolGetWarrantyItems,
		Description: "Get items that may still be under warranty or within return period. Tracks high-value purchases from receipts and reminds about warranty expiry.",
		Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "required": []string{}, "additionalProperties": false},
	}
}

func (o *Orchestrator) executeGetWarrantyItems(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	if o.warrantyTracker == nil {
		return map[string]interface{}{"error": "warranty tracking not available"}, nil
	}
	items, err := o.warrantyTracker.GetWarrantyItems(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("warranty items: %w", err)
	}

	expiringSoon, active := 0, 0
	for i := range items {
		switch items[i].Status {
		case "expiring_soon":
			expiringSoon++
		case "active":
			active++
		}
	}

	msg := "No warranty items found."
	if len(items) > 0 {
		if expiringSoon > 0 {
			msg = fmt.Sprintf("You have %d item(s) with warranties expiring soon. Check them before it's too late!", expiringSoon)
		} else {
			msg = fmt.Sprintf("You have %d active warranty item(s). All looking good!", active)
		}
	}

	return map[string]interface{}{
		"warranty_items": items,
		"expiring_soon":  expiringSoon,
		"active":         active,
		"message":        msg,
	}, nil
}
