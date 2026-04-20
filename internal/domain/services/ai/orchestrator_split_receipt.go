package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
)

const ToolSplitReceipt = "split_receipt"

// SplitReceiptTool returns the tool definition for splitting a receipt.
func SplitReceiptTool() infraai.Tool {
	return infraai.Tool{
		Name:        ToolSplitReceipt,
		Description: "Split a scanned receipt with friends via P2P transfer requests. Requires user confirmation. Use when user says 'split this receipt with @tag1 and @tag2' or 'split the bill'.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"receipt_id":   map[string]interface{}{"type": "string", "description": "UUID of the receipt to split"},
				"participants": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "List of rail_tags (e.g. @john, @jane)"},
				"message":      map[string]interface{}{"type": "string", "description": "Optional message for the split request"},
			},
			"required": []string{"receipt_id", "participants"},
		},
	}
}

func (o *Orchestrator) createSplitReceiptAction(ctx context.Context, userID, convID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	receiptIDStr, _ := args["receipt_id"].(string)
	if receiptIDStr == "" {
		return map[string]interface{}{"error": "receipt_id is required"}, nil
	}
	receiptID, err := uuid.Parse(receiptIDStr)
	if err != nil {
		return map[string]interface{}{"error": "invalid receipt_id"}, nil
	}

	participantsRaw, _ := args["participants"].([]interface{})
	if len(participantsRaw) == 0 {
		return map[string]interface{}{"error": "at least one participant required"}, nil
	}

	// Verify receipt ownership
	if o.receiptHistory == nil {
		return map[string]interface{}{"error": "receipts not available"}, nil
	}

	// Build participant list for description
	tags := make([]string, 0, len(participantsRaw))
	for _, p := range participantsRaw {
		tag, _ := p.(string)
		tag = strings.TrimPrefix(strings.TrimSpace(tag), "@")
		if tag != "" {
			tags = append(tags, "@"+tag)
		}
	}
	if len(tags) == 0 {
		return map[string]interface{}{"error": "no valid participants"}, nil
	}

	message, _ := args["message"].(string)

	params := map[string]interface{}{
		"receipt_id":   receiptID.String(),
		"participants": tags,
		"message":      message,
	}

	action := &entities.PendingAction{
		ID:             uuid.New().String(),
		ConversationID: convID,
		UserID:         userID,
		Action:         ToolSplitReceipt,
		Description:    fmt.Sprintf("Split receipt with %s", strings.Join(tags, ", ")),
		Params:         params,
		ExpiresAt:      time.Now().Add(pendingActionTTL),
		CreatedAt:      time.Now(),
	}

	o.pending.Set(ctx, convID, action)

	return map[string]interface{}{
		"action_required": true,
		"pending_action":  action,
	}, nil
}
