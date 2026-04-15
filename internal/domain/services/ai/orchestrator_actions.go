package ai

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	"go.uber.org/zap"
)

// Action tool names.
const (
	ToolTransferFunds   = "transfer_funds"
	ToolSetSavingsGoal  = "set_savings_goal"
	ToolConfirmAction   = "confirm_action"
	ToolCancelAction    = "cancel_action"
)

const pendingActionTTL = 2 * time.Minute

// FundsTransferer moves money between spend and stash.
type FundsTransferer interface {
	TransferSpendToStash(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error
	TransferStashToSpend(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error
	GetSpendBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error)
	GetStashBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error)
}

// ActionAuditor persists action audit entries.
type ActionAuditor interface {
	RecordAction(ctx context.Context, entry *entities.ActionAuditEntry) error
}

// pendingActions is an in-memory store for actions awaiting confirmation.
// Keyed by conversationID string so each conversation has at most one pending action.
type pendingActions struct {
	mu    sync.Mutex
	store map[string]*entities.PendingAction
}

func newPendingActions() *pendingActions {
	return &pendingActions{store: make(map[string]*entities.PendingAction)}
}

func (p *pendingActions) Set(convID uuid.UUID, action *entities.PendingAction) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.store[convID.String()] = action
}

func (p *pendingActions) Get(convID uuid.UUID) *entities.PendingAction {
	p.mu.Lock()
	defer p.mu.Unlock()
	a, ok := p.store[convID.String()]
	if !ok || a.IsExpired() {
		delete(p.store, convID.String())
		return nil
	}
	return a
}

func (p *pendingActions) Delete(convID uuid.UUID) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.store, convID.String())
}

// SetFundsTransferer wires the funds transfer dependency.
func (o *Orchestrator) SetFundsTransferer(f FundsTransferer) {
	o.fundsTransferer = f
}

// SetActionAuditor wires the action audit dependency.
func (o *Orchestrator) SetActionAuditor(a ActionAuditor) {
	o.actionAuditor = a
}

// ActionTools returns the action-capable tools.
func ActionTools() []ai.Tool {
	return []ai.Tool{
		{
			Name:        ToolTransferFunds,
			Description: "Transfer money between the user's Spend and Stash wallets. Requires user confirmation before execution.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"from":   map[string]interface{}{"type": "string", "enum": []string{"spend", "stash"}, "description": "Source wallet"},
					"to":     map[string]interface{}{"type": "string", "enum": []string{"spend", "stash"}, "description": "Destination wallet"},
					"amount": map[string]interface{}{"type": "number", "description": "Amount in USD to transfer"},
				},
				"required": []string{"from", "to", "amount"},
			},
		},
		{
			Name:        ToolSetSavingsGoal,
			Description: "Set a savings goal with a target amount and optional deadline for the user. Requires user confirmation.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name":     map[string]interface{}{"type": "string", "description": "Goal name, e.g. 'Emergency Fund'"},
					"target":   map[string]interface{}{"type": "number", "description": "Target amount in USD"},
					"deadline": map[string]interface{}{"type": "string", "description": "Optional deadline in YYYY-MM-DD format"},
				},
				"required": []string{"name", "target"},
			},
		},
	}
}

// executeActionTool handles action tool calls by creating a pending action
// instead of executing immediately. Returns a confirmation payload for the frontend.
func (o *Orchestrator) executeActionTool(ctx context.Context, userID, convID uuid.UUID, tc ai.ToolCall) (map[string]interface{}, error) {
	switch tc.Name {
	case ToolTransferFunds:
		return o.createTransferAction(ctx, userID, convID, tc.Arguments)
	case ToolSetSavingsGoal:
		return o.createSavingsGoalAction(ctx, userID, convID, tc.Arguments)
	default:
		return nil, fmt.Errorf("unknown action tool: %s", tc.Name)
	}
}

func (o *Orchestrator) createTransferAction(ctx context.Context, userID, convID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	from, _ := args["from"].(string)
	to, _ := args["to"].(string)
	amountF, _ := args["amount"].(float64)

	if from == "" || to == "" || amountF <= 0 {
		return map[string]interface{}{"error": "Invalid transfer parameters"}, nil
	}
	if from == to {
		return map[string]interface{}{"error": "Source and destination must be different"}, nil
	}
	if from != "spend" && from != "stash" {
		return map[string]interface{}{"error": "Source must be 'spend' or 'stash'"}, nil
	}
	if to != "spend" && to != "stash" {
		return map[string]interface{}{"error": "Destination must be 'spend' or 'stash'"}, nil
	}
	if amountF > 50000 {
		return map[string]interface{}{"error": "Transfer amount exceeds maximum ($50,000)"}, nil
	}

	amount := decimal.NewFromFloat(amountF)

	// Check balance
	var balance decimal.Decimal
	var err error
	if from == "spend" {
		balance, err = o.fundsTransferer.GetSpendBalance(ctx, userID)
	} else {
		balance, err = o.fundsTransferer.GetStashBalance(ctx, userID)
	}
	if err != nil {
		return nil, fmt.Errorf("check balance: %w", err)
	}
	if balance.LessThan(amount) {
		return map[string]interface{}{
			"error":             "Insufficient balance",
			"available_balance": balance.StringFixed(2),
			"requested_amount":  amount.StringFixed(2),
		}, nil
	}

	action := &entities.PendingAction{
		ID:             uuid.New().String(),
		ConversationID: convID,
		UserID:         userID,
		Action:         ToolTransferFunds,
		Description:    fmt.Sprintf("Move $%s from %s to %s", amount.StringFixed(2), from, to),
		Params:         map[string]interface{}{"from": from, "to": to, "amount": amount.StringFixed(2)},
		ExpiresAt:      time.Now().Add(pendingActionTTL),
		CreatedAt:      time.Now(),
	}

	o.pending.Set(convID, action)

	return map[string]interface{}{
		"action_required": true,
		"pending_action":  action,
	}, nil
}

func (o *Orchestrator) createSavingsGoalAction(ctx context.Context, userID, convID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	name, _ := args["name"].(string)
	targetF, _ := args["target"].(float64)
	deadline, _ := args["deadline"].(string)

	if name == "" || targetF <= 0 {
		return map[string]interface{}{"error": "Invalid goal parameters"}, nil
	}

	target := decimal.NewFromFloat(targetF)
	params := map[string]interface{}{"name": name, "target": target.StringFixed(2)}
	desc := fmt.Sprintf("Set savings goal '%s' for $%s", name, target.StringFixed(2))
	if deadline != "" {
		if _, err := time.Parse("2006-01-02", deadline); err != nil {
			return map[string]interface{}{"error": "Invalid deadline format, use YYYY-MM-DD"}, nil
		}
		params["deadline"] = deadline
		desc += fmt.Sprintf(" by %s", deadline)
	}

	action := &entities.PendingAction{
		ID:             uuid.New().String(),
		ConversationID: convID,
		UserID:         userID,
		Action:         ToolSetSavingsGoal,
		Description:    desc,
		Params:         params,
		ExpiresAt:      time.Now().Add(pendingActionTTL),
		CreatedAt:      time.Now(),
	}

	o.pending.Set(convID, action)

	return map[string]interface{}{
		"action_required": true,
		"pending_action":  action,
	}, nil
}

// ConfirmAction executes a pending action after user confirmation.
func (o *Orchestrator) ConfirmAction(ctx context.Context, userID, convID uuid.UUID) (*entities.PendingAction, error) {
	action := o.pending.Get(convID)
	if action == nil {
		// Check if it was expired (Get auto-deletes expired)
		return nil, fmt.Errorf("no pending action or action expired")
	}
	if action.UserID != userID {
		return nil, fmt.Errorf("action does not belong to user")
	}

	var execErr error
	switch action.Action {
	case ToolTransferFunds:
		execErr = o.executeTransfer(ctx, userID, action)
	case ToolSetSavingsGoal:
		o.logger.Info("Savings goal set via Ada",
			zap.String("user_id", userID.String()),
			zap.Any("params", action.Params))
	default:
		execErr = fmt.Errorf("unknown action: %s", action.Action)
	}

	o.pending.Delete(convID)

	status := "executed"
	errMsg := ""
	if execErr != nil {
		status = "failed"
		errMsg = execErr.Error()
	}
	o.auditAction(ctx, userID, convID, action, status, errMsg)

	if execErr != nil {
		return nil, execErr
	}
	return action, nil
}

// CancelAction discards a pending action.
func (o *Orchestrator) CancelAction(ctx context.Context, userID, convID uuid.UUID) error {
	action := o.pending.Get(convID)
	if action == nil {
		return nil // Already gone or expired — safe to ignore
	}
	if action.UserID != userID {
		return fmt.Errorf("action does not belong to user")
	}
	o.pending.Delete(convID)
	o.auditAction(ctx, userID, convID, action, "cancelled", "")
	return nil
}

func (o *Orchestrator) executeTransfer(ctx context.Context, userID uuid.UUID, action *entities.PendingAction) error {
	from, _ := action.Params["from"].(string)
	amountStr, _ := action.Params["amount"].(string)
	amount, err := decimal.NewFromString(amountStr)
	if err != nil {
		return fmt.Errorf("invalid amount: %w", err)
	}

	// Re-check balance at execution time (TOCTOU protection)
	var balance decimal.Decimal
	if from == "spend" {
		balance, err = o.fundsTransferer.GetSpendBalance(ctx, userID)
	} else {
		balance, err = o.fundsTransferer.GetStashBalance(ctx, userID)
	}
	if err != nil {
		return fmt.Errorf("check balance: %w", err)
	}
	if balance.LessThan(amount) {
		return fmt.Errorf("insufficient balance: available $%s, requested $%s", balance.StringFixed(2), amount.StringFixed(2))
	}

	// Use action ID as idempotency key to prevent double-execution
	if from == "spend" {
		return o.fundsTransferer.TransferSpendToStash(ctx, userID, amount)
	}
	return o.fundsTransferer.TransferStashToSpend(ctx, userID, amount)
}

func (o *Orchestrator) auditAction(ctx context.Context, userID, convID uuid.UUID, action *entities.PendingAction, status, errMsg string) {
	if o.actionAuditor == nil {
		return
	}
	entry := &entities.ActionAuditEntry{
		ID:             uuid.New(),
		UserID:         userID,
		ConversationID: convID,
		Action:         action.Action,
		Params:         action.Params,
		Status:         status,
		ErrorMessage:   errMsg,
		CreatedAt:      time.Now(),
	}
	if err := o.actionAuditor.RecordAction(ctx, entry); err != nil {
		o.logger.Error("failed to audit action", zap.Error(err))
	}
}

// isActionTool returns true if the tool name is an action tool.
func isActionTool(name string) bool {
	return name == ToolTransferFunds || name == ToolSetSavingsGoal
}
