package ai

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
)

const (
	ToolCreateAutomation = "create_automation"
	ToolListAutomations  = "list_automations"
)

// AutomationProvider is the subset of automation.Service the orchestrator needs.
type AutomationProvider interface {
	Create(ctx context.Context, userID uuid.UUID, req *AutomationRequest) (*entities.MiriamAutomation, error)
	List(ctx context.Context, userID uuid.UUID) ([]entities.MiriamAutomation, error)
}

// AutomationRequest mirrors automation.CreateAutomationRequest to avoid import cycle.
type AutomationRequest struct {
	Name              string                 `json:"name"`
	Description       *string                `json:"description"`
	TriggerType       string                 `json:"trigger_type"`
	TriggerConfig     map[string]interface{} `json:"trigger_config"`
	ActionType        string                 `json:"action_type"`
	ActionConfig      map[string]interface{} `json:"action_config"`
	MaxTriggersPerDay int                    `json:"max_triggers_per_day"`
	CooldownMinutes   int                    `json:"cooldown_minutes"`
}

// SetAutomationProvider wires the automation dependency.
func (o *Orchestrator) SetAutomationProvider(a AutomationProvider) {
	o.automationProvider = a
}

// AutomationTools returns tool definitions for automation management.
func AutomationTools() []infraai.Tool {
	return []infraai.Tool{
		{
			Name: ToolCreateAutomation,
			Description: `Create an automation rule that runs automatically without the user having to do anything.
Use when the user wants to:
- Move money on a schedule ("move $50 to stash every Friday", "save $20 every Monday")
- Trigger a transfer when balance crosses a threshold ("when spend goes above $500, move $100 to stash", "if stash drops below $200, move $50 from spend")

Trigger types:
- "schedule": runs on specific weekdays at a specific hour. trigger_config: {"weekdays": [5], "hour": 9} (0=Sun,1=Mon,2=Tue,3=Wed,4=Thu,5=Fri,6=Sat)
- "balance_threshold": fires when a wallet balance crosses a level. trigger_config: {"wallet": "spend", "operator": "above", "threshold": 500}

Action types:
- "transfer_to_stash": move money from spend to stash. action_config: {"amount": 50}
- "transfer_to_spend": move money from stash to spend. action_config: {"amount": 50}

Requires user confirmation before creating.`,
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name":           map[string]interface{}{"type": "string", "description": "Short name, e.g. 'Friday Stash Move'"},
					"trigger_type":   map[string]interface{}{"type": "string", "enum": []string{"schedule", "balance_threshold"}},
					"trigger_config": map[string]interface{}{"type": "object", "description": "For schedule: {weekdays:[5], hour:9} where hour is 1-23 (0 means any hour). For balance_threshold: {wallet:'spend', operator:'above', threshold:500}"},
					"action_type":    map[string]interface{}{"type": "string", "enum": []string{"transfer_to_stash", "transfer_to_spend"}},
					"action_config":  map[string]interface{}{"type": "object", "description": "{amount: 50}"},
					"description":    map[string]interface{}{"type": "string"},
				},
				"required": []string{"name", "trigger_type", "trigger_config", "action_type", "action_config"},
			},
		},
		{
			Name:        ToolListAutomations,
			Description: "List the user's active automation rules. Use when user asks 'what automations do I have', 'show my rules', 'what's set up automatically', or 'is anything running for me'.",
			Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "required": []string{}, "additionalProperties": false},
		},
	}
}

func (o *Orchestrator) createAutomationAction(ctx context.Context, userID, convID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	name, _ := args["name"].(string)
	triggerType, _ := args["trigger_type"].(string)
	actionType, _ := args["action_type"].(string)
	triggerConfig, _ := args["trigger_config"].(map[string]interface{})
	actionConfig, _ := args["action_config"].(map[string]interface{})

	if name == "" || triggerType == "" || actionType == "" || triggerConfig == nil || actionConfig == nil {
		return map[string]interface{}{"error": "name, trigger_type, trigger_config, action_type, and action_config are required"}, nil
	}
	if amountF, ok := actionConfig["amount"].(float64); !ok || amountF <= 0 {
		return map[string]interface{}{"error": "action_config must include a positive amount"}, nil
	}

	params := map[string]interface{}{
		"name":           name,
		"trigger_type":   triggerType,
		"trigger_config": triggerConfig,
		"action_type":    actionType,
		"action_config":  actionConfig,
	}
	if desc, ok := args["description"].(string); ok && desc != "" {
		params["description"] = desc
	}

	action := &entities.PendingAction{
		ID:             uuid.New().String(),
		ConversationID: convID,
		UserID:         userID,
		Action:         ToolCreateAutomation,
		Description:    fmt.Sprintf("Create automation '%s': %s → %s", name, triggerType, actionType),
		Params:         params,
		ExpiresAt:      time.Now().Add(pendingActionTTL),
		CreatedAt:      time.Now(),
	}

	if err := o.pending.Set(ctx, convID, action); err != nil {
		return nil, fmt.Errorf("store pending automation action: %w", err)
	}

	return map[string]interface{}{"action_required": true, "pending_action": action}, nil
}

func (o *Orchestrator) executeListAutomations(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	if o.automationProvider == nil {
		return map[string]interface{}{"automations": []interface{}{}, "count": 0}, nil
	}
	automations, err := o.automationProvider.List(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list automations: %w", err)
	}
	items := make([]map[string]interface{}, len(automations))
	for i, a := range automations {
		item := map[string]interface{}{
			"id":           a.ID.String(),
			"name":         a.Name,
			"trigger_type": a.TriggerType,
			"action_type":  a.ActionType,
			"is_active":    a.IsActive,
		}
		if a.LastTriggeredAt != nil {
			item["last_triggered"] = a.LastTriggeredAt.Format("Jan 2, 2006")
		}
		items[i] = item
	}
	return map[string]interface{}{"automations": items, "count": len(items)}, nil
}

func (o *Orchestrator) executeCreateAutomation(ctx context.Context, userID uuid.UUID, params map[string]interface{}) (map[string]interface{}, error) {
	if o.automationProvider == nil {
		return nil, fmt.Errorf("automation service unavailable")
	}
	name, _ := params["name"].(string)
	triggerType, _ := params["trigger_type"].(string)
	actionType, _ := params["action_type"].(string)
	triggerConfig, _ := params["trigger_config"].(map[string]interface{})
	actionConfig, _ := params["action_config"].(map[string]interface{})

	req := &AutomationRequest{
		Name:          name,
		TriggerType:   triggerType,
		TriggerConfig: triggerConfig,
		ActionType:    actionType,
		ActionConfig:  actionConfig,
	}
	if desc, ok := params["description"].(string); ok && desc != "" {
		req.Description = &desc
	}

	automation, err := o.automationProvider.Create(ctx, userID, req)
	if err != nil {
		return nil, fmt.Errorf("create automation: %w", err)
	}
	return map[string]interface{}{"id": automation.ID.String(), "name": automation.Name, "created": true}, nil
}
