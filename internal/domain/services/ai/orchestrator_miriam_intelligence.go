package ai

import (
	"context"

	"github.com/google/uuid"
	aiintelligence "github.com/rail-service/rail_service/internal/domain/services/ai/intelligence"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
)

const (
	ToolGetMiriamMoneyState       = "get_miriam_money_state"
	ToolListMiriamMandates        = "list_miriam_mandates"
	ToolGetMiriamDecisionReceipts = "get_miriam_decision_receipts"
)

// Deprecated: Use aiintelligence.MiriamIntelligenceReader instead.
type MiriamIntelligenceReader = aiintelligence.MiriamIntelligenceReader

func MiriamIntelligenceTools() []infraai.Tool {
	return []infraai.Tool{
		{
			Name:        ToolGetMiriamMoneyState,
			Description: "Get Miriam's durable money state for the user: income cadence, upcoming obligations, safe-to-spend, runway, stash target, recurring spend, recent anomalies, and confidence. Use before explaining what Miriam quietly sees or why she should/shouldn't move money.",
			Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "required": []string{}, "additionalProperties": false},
		},
		{
			Name:        ToolListMiriamMandates,
			Description: "List user-approved Miriam autopilot mandates and their bounded rules. Use when the user asks what Miriam is allowed to do automatically or why an autopilot action can happen.",
			Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "required": []string{}, "additionalProperties": false},
		},
		{
			Name:        ToolGetMiriamDecisionReceipts,
			Description: "Get recent Miriam decision receipts for quiet actions, skipped actions, and failed actions. Use when the user asks what Miriam did, why money moved, or whether autopilot has been active.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"limit": map[string]interface{}{"type": "integer", "default": 5, "description": "Number of receipts to return, max 20"},
				},
				"required":             []string{},
				"additionalProperties": false,
			},
		},
	}
}

func (o *AgentAdapter) SetMiriamIntelligenceProvider(p MiriamIntelligenceReader) {
	o.miriamIntelligence = p
}

func (o *AgentAdapter) executeMiriamMoneyState(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	state, err := o.miriamIntelligence.GetMoneyState(ctx, userID)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"state": state}, nil
}

func (o *AgentAdapter) executeListMiriamMandates(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	mandates, err := o.miriamIntelligence.ListMandates(ctx, userID)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"mandates": mandates, "count": len(mandates)}, nil
}

func (o *AgentAdapter) executeMiriamDecisionReceipts(ctx context.Context, userID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	limit := 5
	if raw, ok := args["limit"].(float64); ok && raw > 0 {
		limit = int(raw)
	}
	if limit > 20 {
		limit = 20
	}
	receipts, err := o.miriamIntelligence.ListReceipts(ctx, userID, limit)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"receipts": receipts, "count": len(receipts)}, nil
}
