package ai

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/shopspring/decimal"
)

const (
	ToolCreateAutomation     = "create_automation"
	ToolListAutomations      = "list_automations"
	ToolSuggestSmartTiming   = "suggest_smart_timing"
	ToolSuggestAdaptiveAmount = "suggest_adaptive_amount"
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
	SavingsGoalID     *uuid.UUID             `json:"savings_goal_id,omitempty"`
	ObligationID      *uuid.UUID             `json:"obligation_id,omitempty"`
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
- Trigger a transfer when balance crosses a threshold ("when spend goes above $500, move $100 to stash")
- Get notified when balance is low ("alert me when spend drops below $100")
- Pause card on spending spikes ("freeze my card if I spend too much")
- Save toward a goal ("save $50/week until I hit $2000 for my trip")
- Get reminded before bills are due ("remind me 3 days before rent is due")
- React to life events ("when I get a raise, increase my stash transfer by 20%")

Trigger types:
- "schedule": runs on specific weekdays at a specific hour. trigger_config: {"weekdays": [5], "hour": 9}
- "balance_threshold": fires when a wallet balance crosses a level. trigger_config: {"wallet": "spend", "operator": "below", "threshold": 100}
- "spending_spike": fires when unusual spending is detected. trigger_config: {}
- "obligation_due": fires N days before an obligation is due. trigger_config: {"days_before_due": 3}
- "life_event": fires when Miriam detects a life event. trigger_config: {"event_type": "income_increase", "threshold": 0.20}

Action types:
- "transfer_to_stash": move money from spend to stash. action_config: {"amount": 50}
- "transfer_to_spend": move money from stash to spend. action_config: {"amount": 50}
- "notify": send a notification. action_config: {"title": "Low Balance", "message": "Your spend is below $100"}
- "pause_card_cooldown": pause card for a cooldown period then auto-resume. action_config: {"cooldown_minutes": 30, "message": "Spending spike detected"}
- "pause_card": freeze the card. action_config: {}
- "resume_card": unfreeze the card. action_config: {}

Optional fields:
- savings_goal_id: link to a savings goal; automation auto-deactivates when goal is reached
- obligation_id: link to an obligation for bill-aware triggers

Requires user confirmation before creating.`,
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name":            map[string]interface{}{"type": "string", "description": "Short name, e.g. 'Friday Stash Move'"},
					"trigger_type":    map[string]interface{}{"type": "string", "enum": []string{"schedule", "balance_threshold", "spending_spike", "obligation_due", "life_event"}},
					"trigger_config":  map[string]interface{}{"type": "object"},
					"action_type":     map[string]interface{}{"type": "string", "enum": []string{"transfer_to_stash", "transfer_to_spend", "notify", "pause_card_cooldown", "pause_card", "resume_card"}},
					"action_config":   map[string]interface{}{"type": "object"},
					"description":     map[string]interface{}{"type": "string"},
					"savings_goal_id": map[string]interface{}{"type": "string", "description": "UUID of savings goal to link to"},
					"obligation_id":   map[string]interface{}{"type": "string", "description": "UUID of obligation to link to"},
				},
				"required": []string{"name", "trigger_type", "trigger_config", "action_type", "action_config"},
			},
		},
		{
			Name:        ToolListAutomations,
			Description: "List the user's active automation rules. Use when user asks 'what automations do I have', 'show my rules', 'what's set up automatically', or 'is anything running for me'.",
			Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "required": []string{}, "additionalProperties": false},
		},
		{
			Name: ToolSuggestSmartTiming,
			Description: `Analyze the user's spending patterns to suggest the best day and time to run automated transfers.
Use when the user asks "when should I save?", "what's the best day to move money?", or when creating a scheduled automation and you want to suggest optimal timing.
Returns the lowest-spending day of the week and recommended hour.`,
			Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "required": []string{}, "additionalProperties": false},
		},
		{
			Name: ToolSuggestAdaptiveAmount,
			Description: `Calculate a recommended transfer amount based on the user's recent income, spending, and upcoming obligations.
Use when the user asks "how much should I save?", "what can I afford to move to stash?", or when creating a transfer automation and you want to suggest an amount.
Returns a recommended amount and the reasoning.`,
			Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "required": []string{}, "additionalProperties": false},
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
	// Only require positive amount for transfer actions
	isTransfer := actionType == entities.ActionTransferToStash || actionType == entities.ActionTransferToSpend
	if isTransfer {
		if amountF, ok := actionConfig["amount"].(float64); !ok || amountF <= 0 {
			return map[string]interface{}{"error": "transfer action_config must include a positive amount"}, nil
		}
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
	if goalID, ok := args["savings_goal_id"].(string); ok && goalID != "" {
		params["savings_goal_id"] = goalID
	}
	if obligationID, ok := args["obligation_id"].(string); ok && obligationID != "" {
		params["obligation_id"] = obligationID
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

	if actionType == entities.ActionTransferToStash || actionType == entities.ActionTransferToSpend {
		if actionConfig == nil {
			actionConfig = map[string]interface{}{}
		}
		now := time.Now().UTC()
		actionConfig["acknowledged_future_transfer"] = true
		actionConfig["passcode_session_verified_at"] = now.Format(time.RFC3339)
		actionConfig["reauthorization_due_at"] = now.Add(90 * 24 * time.Hour).Format(time.RFC3339)
		actionConfig["reauthorization_window_days"] = 90
	}

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
	if goalIDStr, ok := params["savings_goal_id"].(string); ok && goalIDStr != "" {
		if gid, err := uuid.Parse(goalIDStr); err == nil {
			req.SavingsGoalID = &gid
		}
	}
	if obligationIDStr, ok := params["obligation_id"].(string); ok && obligationIDStr != "" {
		if oid, err := uuid.Parse(obligationIDStr); err == nil {
			req.ObligationID = &oid
		}
	}

	result, err := o.automationProvider.Create(ctx, userID, req)
	if err != nil {
		return nil, fmt.Errorf("create automation: %w", err)
	}
	return map[string]interface{}{"id": result.ID.String(), "name": result.Name, "created": true}, nil
}

func (o *Orchestrator) executeSuggestSmartTiming(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	if o.patterns == nil {
		return map[string]interface{}{"error": "spending pattern analysis is unavailable"}, nil
	}
	end := time.Now().UTC()
	start := end.AddDate(0, -1, 0)
	dayData, err := o.patterns.GetSpendingByDayOfWeek(ctx, userID, start, end)
	if err != nil || len(dayData) == 0 {
		return map[string]interface{}{
			"suggested_weekday":      1, // Monday default
			"suggested_hour":         9,
			"reasoning":              "Not enough spending data yet. Defaulting to Monday 9am.",
			"data_available":         false,
		}, nil
	}

	// Find the day with the lowest spending
	minDay := 0
	minAmount := dayData[0].Total
	for _, d := range dayData {
		if d.Total.LessThan(minAmount) {
			minAmount = d.Total
			minDay = d.DayOfWeek
		}
	}
	dayNames := []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
	return map[string]interface{}{
		"suggested_weekday":      minDay,
		"suggested_weekday_name": dayNames[minDay],
		"suggested_hour":         9,
		"lowest_spending_amount": minAmount.StringFixed(2),
		"reasoning":              fmt.Sprintf("You spend the least on %ss ($%s avg). Moving money then minimizes the chance you'll need it for spending.", dayNames[minDay], minAmount.StringFixed(2)),
		"data_available":         true,
	}, nil
}

func (o *Orchestrator) executeSuggestAdaptiveAmount(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	end := time.Now().UTC()
	start := end.AddDate(0, -1, 0)

	// Get total spending last month
	var monthlySpend decimal.Decimal
	if o.patterns != nil {
		total, _, err := o.patterns.GetSpendingTotal(ctx, userID, start, end)
		if err == nil {
			monthlySpend = total
		}
	}

	// Get spend balance
	var spendBal decimal.Decimal
	if o.fundsTransferer != nil {
		bal, err := o.fundsTransferer.GetSpendBalance(ctx, userID)
		if err == nil {
			spendBal = bal
		}
	}

	// Get upcoming obligations
	var obligationTotal decimal.Decimal
	if o.obligations != nil {
		obs, err := o.obligations.ListActive(ctx, userID)
		if err == nil {
			for _, ob := range obs {
				obligationTotal = obligationTotal.Add(ob.Amount)
			}
		}
	}

	// Calculate: available = spend balance - monthly spending average - obligations buffer
	// Recommend saving 20% of the surplus
	weeklySpend := monthlySpend.Div(decimal.NewFromInt(4))
	buffer := weeklySpend.Add(obligationTotal)
	surplus := spendBal.Sub(buffer)

	var recommended decimal.Decimal
	var reasoning string
	if surplus.IsPositive() {
		recommended = surplus.Mul(decimal.NewFromFloat(0.20)).Round(0)
		if recommended.LessThan(decimal.NewFromInt(5)) {
			recommended = decimal.NewFromInt(5)
		}
		reasoning = fmt.Sprintf("Based on your $%s spend balance, ~$%s/week spending, and $%s in obligations, you have ~$%s surplus. Recommending $%s/week to stash.",
			spendBal.StringFixed(2), weeklySpend.StringFixed(2), obligationTotal.StringFixed(2), surplus.StringFixed(2), recommended.StringFixed(0))
	} else {
		recommended = decimal.NewFromInt(10)
		reasoning = "Your balance is tight relative to spending and obligations. Starting with a small $10/week transfer to build the habit."
	}

	return map[string]interface{}{
		"recommended_amount":  recommended.InexactFloat64(),
		"recommended_cadence": "weekly",
		"spend_balance":       spendBal.StringFixed(2),
		"monthly_spending":    monthlySpend.StringFixed(2),
		"monthly_obligations": obligationTotal.StringFixed(2),
		"reasoning":           reasoning,
	}, nil
}
