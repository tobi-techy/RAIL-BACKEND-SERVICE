package ai

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

const ToolInitiateWithdrawal = "initiate_withdrawal"

// WithdrawalInitiator initiates a fiat withdrawal to the user's linked bank.
type WithdrawalInitiator interface {
	InitiateFiatWithdrawal(ctx context.Context, req *entities.InitiateFiatWithdrawalRequest) (*entities.InitiateWithdrawalResponse, error)
}

// WithdrawalTool returns the tool definition for voice-triggered withdrawals.
func WithdrawalTool() infraai.Tool {
	return infraai.Tool{
		Name: ToolInitiateWithdrawal,
		Description: `Withdraw money from the user's spend wallet to their linked bank account.
Use when the user says "send money to my bank", "withdraw to bank", "cash out", "I need naira in my account".
Requires user confirmation before execution.
The currency determines the destination: NGN goes to their naira bank, USD to their dollar account.`,
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"amount":   map[string]interface{}{"type": "number", "description": "Amount to withdraw"},
				"currency": map[string]interface{}{"type": "string", "enum": []string{"NGN", "USD", "GBP", "EUR"}, "description": "Withdrawal currency"},
			},
			"required":             []string{"amount", "currency"},
			"additionalProperties": false,
		},
	}
}

func (o *Orchestrator) createWithdrawalAction(ctx context.Context, userID, convID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	amountF, _ := args["amount"].(float64)
	currency, _ := args["currency"].(string)

	if amountF <= 0 {
		return map[string]interface{}{"error": "Amount must be positive"}, nil
	}
	if currency == "" {
		currency = "NGN"
	}

	amount := decimal.NewFromFloat(amountF)

	// Check spend balance
	if o.fundsTransferer != nil {
		balance, err := o.fundsTransferer.GetSpendBalance(ctx, userID)
		if err == nil && balance.LessThan(amount) {
			return map[string]interface{}{
				"error":   "Insufficient balance",
				"balance": balance.StringFixed(2),
				"requested": amount.StringFixed(2),
			}, nil
		}
	}

	desc := fmt.Sprintf("Withdraw %s %s to bank", amount.StringFixed(0), currency)

	action := &entities.PendingAction{
		ID:             uuid.New().String(),
		ConversationID: convID,
		UserID:         userID,
		Action:         ToolInitiateWithdrawal,
		Description:    desc,
		Params: map[string]interface{}{
			"amount":   amount.StringFixed(2),
			"currency": currency,
		},
		ExpiresAt: time.Now().Add(pendingActionTTL),
		CreatedAt: time.Now(),
	}

	if err := o.pending.Set(ctx, convID, action); err != nil {
		return nil, fmt.Errorf("store pending withdrawal: %w", err)
	}

	return map[string]interface{}{
		"action_required": true,
		"pending_action":  action,
		"message":         fmt.Sprintf("I'll withdraw %s %s to your bank. Confirm?", amount.StringFixed(0), currency),
	}, nil
}

func (o *Orchestrator) executeWithdrawal(ctx context.Context, userID uuid.UUID, action *entities.PendingAction) error {
	if o.withdrawalInitiator == nil {
		return fmt.Errorf("withdrawal service unavailable")
	}

	amountStr, _ := action.Params["amount"].(string)
	currency, _ := action.Params["currency"].(string)

	amount, err := decimal.NewFromString(amountStr)
	if err != nil {
		return fmt.Errorf("invalid amount: %w", err)
	}

	req := &entities.InitiateFiatWithdrawalRequest{
		UserID:        userID,
		Amount:        amount,
		Currency:      entities.WithdrawalCurrency(currency),
		SourceAccount: entities.WithdrawalSourceSpendingBalance,
		Narration:     "Miriam voice withdrawal",
	}

	resp, err := o.withdrawalInitiator.InitiateFiatWithdrawal(ctx, req)
	if err != nil {
		o.logger.Error("voice withdrawal failed",
			zap.String("user_id", userID.String()),
			zap.String("amount", amount.String()),
			zap.Error(err))
		return err
	}

	o.logger.Info("voice withdrawal initiated",
		zap.String("user_id", userID.String()),
		zap.String("withdrawal_id", resp.WithdrawalID.String()),
		zap.String("amount", amount.String()),
		zap.String("currency", currency))
	return nil
}
