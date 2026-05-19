package ai

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

const (
	ToolProtectSubscription      = "protect_subscription"
	ToolMarkSubscriptionCancelled = "mark_subscription_cancelled"
	ToolIgnoreSubscription       = "ignore_subscription"
)

func (o *Orchestrator) createProtectSubscriptionAction(ctx context.Context, userID, convID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	if o.obligationCreator == nil {
		return map[string]interface{}{"error": "financial obligation service is unavailable"}, nil
	}
	name := subscriptionNameArg(args)
	amount, ok := decimalArg(args["amount"])
	if name == "" || !ok || !amount.IsPositive() {
		return map[string]interface{}{"error": "name and positive amount are required"}, nil
	}

	params := subscriptionActionParams(args, name, amount)
	action := &entities.PendingAction{
		ID:             uuid.New().String(),
		ConversationID: convID,
		UserID:         userID,
		Action:         ToolProtectSubscription,
		Description:    fmt.Sprintf("Protect subscription: %s", name),
		Params:         params,
		ExpiresAt:      time.Now().Add(pendingActionTTL),
		CreatedAt:      time.Now(),
	}
	if err := o.pending.Set(ctx, convID, action); err != nil {
		return nil, fmt.Errorf("store pending protect-subscription action: %w", err)
	}
	return map[string]interface{}{"action_required": true, "pending_action": action}, nil
}

func (o *Orchestrator) executeProtectSubscription(ctx context.Context, userID uuid.UUID, params map[string]interface{}) (*entities.FinancialObligation, error) {
	if o.obligationCreator == nil {
		return nil, fmt.Errorf("financial obligation service is unavailable")
	}
	amount, ok := decimalArg(params["amount"])
	if !ok || !amount.IsPositive() {
		return nil, fmt.Errorf("amount must be a positive number")
	}
	req := AIServiceObligationRequest{
		Type:     entities.ObligationTypeSubscription,
		Name:     subscriptionNameArg(params),
		Amount:   amount,
		Currency: coalesceString(valueStringArg(params["currency"]), "USD"),
		Cadence:  subscriptionCadence(valueStringArg(params["frequency"])),
		Priority: entities.ObligationPriorityMedium,
		Status:   entities.ObligationStatusActive,
		Metadata: subscriptionMetadata(params, "subscription_hunter"),
	}
	if dueDay, ok := numberArg(params["due_day"]); ok {
		value := int(dueDay)
		req.DueDay = &value
	}
	return o.obligationCreator.CreateObligationFromAI(ctx, userID, req)
}

func (o *Orchestrator) createMarkSubscriptionCancelledAction(ctx context.Context, userID, convID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	if o.obligationManager == nil && o.obligationCreator == nil {
		return map[string]interface{}{"error": "financial obligation service is unavailable"}, nil
	}
	name := subscriptionNameArg(args)
	if name == "" {
		return map[string]interface{}{"error": "name is required"}, nil
	}
	amount, _ := decimalArg(args["amount"])
	params := subscriptionActionParams(args, name, amount)
	if obligationID := valueStringArg(args["obligation_id"]); obligationID != "" {
		if _, err := uuid.Parse(obligationID); err != nil {
			return map[string]interface{}{"error": "obligation_id must be a valid UUID"}, nil
		}
		params["obligation_id"] = obligationID
	}
	if note := valueStringArg(args["cancelled_note"]); note != "" {
		params["cancelled_note"] = note
	}

	action := &entities.PendingAction{
		ID:             uuid.New().String(),
		ConversationID: convID,
		UserID:         userID,
		Action:         ToolMarkSubscriptionCancelled,
		Description:    fmt.Sprintf("Mark subscription cancelled: %s", name),
		Params:         params,
		ExpiresAt:      time.Now().Add(pendingActionTTL),
		CreatedAt:      time.Now(),
	}
	if err := o.pending.Set(ctx, convID, action); err != nil {
		return nil, fmt.Errorf("store pending subscription-cancelled action: %w", err)
	}
	return map[string]interface{}{"action_required": true, "pending_action": action}, nil
}

func (o *Orchestrator) executeMarkSubscriptionCancelled(ctx context.Context, userID uuid.UUID, params map[string]interface{}) (*entities.FinancialObligation, error) {
	if obligationID := valueStringArg(params["obligation_id"]); obligationID != "" {
		if o.obligationManager == nil {
			return nil, fmt.Errorf("financial obligation service is unavailable")
		}
		id, err := uuid.Parse(obligationID)
		if err != nil {
			return nil, fmt.Errorf("obligation_id must be a valid UUID")
		}
		return o.obligationManager.MarkCancelled(ctx, userID, id)
	}
	if o.obligationCreator == nil {
		return nil, fmt.Errorf("financial obligation service is unavailable")
	}
	amount, ok := decimalArg(params["amount"])
	if !ok || !amount.IsPositive() {
		amount = decimal.NewFromInt(1)
	}
	return o.obligationCreator.CreateObligationFromAI(ctx, userID, AIServiceObligationRequest{
		Type:     entities.ObligationTypeSubscription,
		Name:     subscriptionNameArg(params),
		Amount:   amount,
		Currency: coalesceString(valueStringArg(params["currency"]), "USD"),
		Cadence:  subscriptionCadence(valueStringArg(params["frequency"])),
		Priority: entities.ObligationPriorityLow,
		Status:   entities.ObligationStatusCancelled,
		Metadata: subscriptionMetadata(params, "subscription_hunter_cancelled"),
	})
}

func (o *Orchestrator) createIgnoreSubscriptionAction(ctx context.Context, userID, convID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	name := subscriptionNameArg(args)
	if name == "" {
		return map[string]interface{}{"error": "name is required"}, nil
	}
	params := map[string]interface{}{"name": name}
	if candidateID := valueStringArg(args["candidate_id"]); candidateID != "" {
		params["candidate_id"] = candidateID
	}
	if reason := valueStringArg(args["reason"]); reason != "" {
		params["reason"] = reason
	}
	action := &entities.PendingAction{
		ID:             uuid.New().String(),
		ConversationID: convID,
		UserID:         userID,
		Action:         ToolIgnoreSubscription,
		Description:    fmt.Sprintf("Ignore subscription candidate: %s", name),
		Params:         params,
		ExpiresAt:      time.Now().Add(pendingActionTTL),
		CreatedAt:      time.Now(),
	}
	if err := o.pending.Set(ctx, convID, action); err != nil {
		return nil, fmt.Errorf("store pending ignore-subscription action: %w", err)
	}
	return map[string]interface{}{"action_required": true, "pending_action": action}, nil
}

func subscriptionActionParams(args map[string]interface{}, name string, amount decimal.Decimal) map[string]interface{} {
	params := map[string]interface{}{
		"name":      name,
		"amount":    amount.StringFixed(2),
		"currency":  coalesceString(valueStringArg(args["currency"]), "USD"),
		"frequency": coalesceString(valueStringArg(args["frequency"]), "monthly"),
	}
	if candidateID := valueStringArg(args["candidate_id"]); candidateID != "" {
		params["candidate_id"] = candidateID
	}
	if dueDay, ok := numberArg(args["due_day"]); ok {
		params["due_day"] = int(dueDay)
	}
	return params
}

func subscriptionMetadata(params map[string]interface{}, source string) map[string]interface{} {
	metadata := map[string]interface{}{"source": source}
	for _, key := range []string{"candidate_id", "frequency", "cancelled_note"} {
		if value := valueStringArg(params[key]); value != "" {
			metadata[key] = value
		}
	}
	return metadata
}

func subscriptionNameArg(args map[string]interface{}) string {
	if name := valueStringArg(args["name"]); name != "" {
		return name
	}
	return valueStringArg(args["merchant"])
}

func subscriptionCadence(frequency string) string {
	switch frequency {
	case entities.ObligationCadenceWeekly:
		return entities.ObligationCadenceWeekly
	case entities.ObligationCadenceAnnual, "yearly":
		return entities.ObligationCadenceAnnual
	default:
		return entities.ObligationCadenceMonthly
	}
}
