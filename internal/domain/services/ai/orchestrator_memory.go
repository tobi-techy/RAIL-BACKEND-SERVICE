package ai

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
)

const (
	ToolListMemory   = "list_memory"
	ToolForgetFact   = "forget_fact"
	ToolForgetCategory = "forget_category"
)

// MemoryTools returns the tools for user-facing memory controls.
func MemoryTools() []infraai.Tool {
	return []infraai.Tool{
		{
			Name:        ToolListMemory,
			Description: "List everything Miriam remembers about the user. Use when the user asks 'what do you know about me?', 'what do you remember?', or similar.",
			Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "required": []string{}, "additionalProperties": false},
		},
		{
			Name:        ToolForgetFact,
			Description: "Forget a specific fact about the user. Use when the user says 'forget that', 'that's not true anymore', or wants to correct something. Requires the fact_id from list_memory.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"fact_id": map[string]interface{}{"type": "string", "description": "UUID of the fact to forget"},
				},
				"required": []string{"fact_id"},
			},
		},
		{
			Name:        ToolForgetCategory,
			Description: "Forget everything Miriam knows about the user in a specific category. Use when the user says 'forget everything about my work' or 'clear my goals'.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"category": map[string]interface{}{
						"type": "string",
						"enum": []string{"goal", "life_event", "preference", "habit", "fear", "family", "work", "location", "identity", "financial_behavior"},
					},
				},
				"required": []string{"category"},
			},
		},
	}
}

func (o *Orchestrator) executeListMemory(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	if o.memory == nil {
		return map[string]interface{}{"facts": []interface{}{}, "message": "Memory not available"}, nil
	}
	facts, err := o.memory.ListUserFacts(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list memory: %w", err)
	}
	if len(facts) == 0 {
		return map[string]interface{}{"facts": []interface{}{}, "message": "I don't have any memories about you yet. As we chat more, I'll remember things you share."}, nil
	}

	items := make([]map[string]interface{}, 0, len(facts))
	for _, f := range facts {
		items = append(items, map[string]interface{}{
			"id":       f.ID.String(),
			"category": f.Category,
			"fact":     f.Fact,
			"since":    f.FirstObservedAt.Format("Jan 2006"),
		})
	}
	return map[string]interface{}{"facts": items, "count": len(items)}, nil
}

func (o *Orchestrator) executeForgetFact(ctx context.Context, userID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	if o.memory == nil {
		return map[string]interface{}{"error": "Memory not available"}, nil
	}
	factIDStr, ok := args["fact_id"].(string)
	if !ok || factIDStr == "" {
		return map[string]interface{}{"error": "fact_id is required"}, nil
	}
	factID, err := uuid.Parse(factIDStr)
	if err != nil {
		return map[string]interface{}{"error": "invalid fact_id"}, nil
	}
	if err := o.memory.ForgetFact(ctx, userID, factID); err != nil {
		return nil, fmt.Errorf("forget fact: %w", err)
	}
	return map[string]interface{}{"success": true, "message": "Done — I've forgotten that."}, nil
}

func (o *Orchestrator) executeForgetCategory(ctx context.Context, userID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	if o.memory == nil {
		return map[string]interface{}{"error": "Memory not available"}, nil
	}
	category, ok := args["category"].(string)
	if !ok || category == "" {
		return map[string]interface{}{"error": "category is required"}, nil
	}
	if err := o.memory.ForgetCategory(ctx, userID, category); err != nil {
		return nil, fmt.Errorf("forget category: %w", err)
	}
	return map[string]interface{}{"success": true, "message": fmt.Sprintf("Done — I've cleared everything I knew about your %s.", category)}, nil
}
