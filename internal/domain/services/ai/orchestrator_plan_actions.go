package ai

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

// StageOperatingPlanAction converts a structured operating-plan proposal into
// the existing pending-action confirmation flow.
func (o *AgentAdapter) StageOperatingPlanAction(ctx context.Context, userID, convID uuid.UUID, actionType string, params map[string]interface{}) (map[string]interface{}, error) {
	switch actionType {
	case "set_budget":
		return o.createSetBudgetAction(ctx, userID, convID, params)
	case "transfer_to_stash":
		params["from"] = "spend"
		params["to"] = "stash"
		return o.createTransferAction(ctx, userID, convID, params)
	case "create_automation":
		return o.createAutomationAction(ctx, userID, convID, params)
	case "create_obligation_reminder", "create_obligation_reminders":
		return o.createObligationReminderAction(ctx, userID, convID, params)
	case "reserve_tax":
		return o.createObligationReminderAction(ctx, userID, convID, map[string]interface{}{
			"type":     entities.ObligationTypeTax,
			"name":     "Tax reserve",
			"amount":   params["amount"],
			"currency": coalesceString(valueStringArg(params["currency"]), "USD"),
			"cadence":  entities.ObligationCadenceMonthly,
			"priority": entities.ObligationPriorityHigh,
			"metadata": map[string]interface{}{"source": "operating_plan"},
		})
	case "cap_family_support":
		return o.createObligationReminderAction(ctx, userID, convID, map[string]interface{}{
			"type":     entities.ObligationTypeFamilySupport,
			"name":     "Family support cap",
			"amount":   params["amount"],
			"currency": coalesceString(valueStringArg(params["currency"]), "USD"),
			"cadence":  entities.ObligationCadenceMonthly,
			"priority": entities.ObligationPriorityHigh,
			"metadata": map[string]interface{}{"source": "operating_plan"},
		})
	default:
		return map[string]interface{}{"error": "unsupported operating plan action type"}, nil
	}
}

func (o *AgentAdapter) createObligationReminderAction(ctx context.Context, userID, convID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	if o.obligationCreator == nil {
		return map[string]interface{}{"error": "financial obligation service is unavailable"}, nil
	}
	name, _ := args["name"].(string)
	obligationType, _ := args["type"].(string)
	cadence, _ := args["cadence"].(string)
	currency, _ := args["currency"].(string)
	amount, ok := decimalArg(args["amount"])
	if name == "" || obligationType == "" || cadence == "" || currency == "" || !ok || !amount.IsPositive() {
		return map[string]interface{}{"error": "type, name, amount, currency, and cadence are required"}, nil
	}

	action := &entities.PendingAction{
		ID:             uuid.New().String(),
		ConversationID: convID,
		UserID:         userID,
		Action:         ToolCreateObligationReminder,
		Description:    fmt.Sprintf("Create financial obligation reminder: %s", name),
		Params:         args,
		ExpiresAt:      time.Now().Add(pendingActionTTL),
		CreatedAt:      time.Now(),
	}
	if err := o.pending.Set(ctx, convID, action); err != nil {
		return nil, fmt.Errorf("store pending obligation reminder action: %w", err)
	}
	return map[string]interface{}{"action_required": true, "pending_action": action}, nil
}

func (o *AgentAdapter) executeCreateObligationReminder(ctx context.Context, userID uuid.UUID, params map[string]interface{}) (*entities.FinancialObligation, error) {
	if o.obligationCreator == nil {
		return nil, fmt.Errorf("financial obligation service is unavailable")
	}
	amount, ok := decimalArg(params["amount"])
	if !ok {
		return nil, fmt.Errorf("amount must be a positive number")
	}
	req := AIServiceObligationRequest{
		Type:     valueStringArg(params["type"]),
		Name:     valueStringArg(params["name"]),
		Amount:   amount,
		Currency: valueStringArg(params["currency"]),
		Cadence:  valueStringArg(params["cadence"]),
		Priority: valueStringArg(params["priority"]),
		Metadata: mapArg(params["metadata"]),
	}
	if dueDay, ok := numberArg(params["due_day"]); ok {
		value := int(dueDay)
		req.DueDay = &value
	}
	if counterparty := valueStringArg(params["counterparty"]); counterparty != "" {
		req.Counterparty = &counterparty
	}
	return o.obligationCreator.CreateObligationFromAI(ctx, userID, req)
}

func valueStringArg(value interface{}) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}

func mapArg(value interface{}) map[string]interface{} {
	if m, ok := value.(map[string]interface{}); ok {
		return m
	}
	return nil
}

func numberArg(value interface{}) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	default:
		return 0, false
	}
}

func decimalArg(value interface{}) (decimal.Decimal, bool) {
	switch v := value.(type) {
	case float64:
		return decimal.NewFromFloat(v), true
	case string:
		out, err := decimal.NewFromString(v)
		return out, err == nil
	case decimal.Decimal:
		return v, true
	default:
		return decimal.Zero, false
	}
}
