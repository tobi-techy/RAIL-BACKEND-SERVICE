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

// BankAccountProvider retrieves user's linked bank accounts.
type BankAccountProvider interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.BankAccount, error)
}

// WithdrawalTool returns the tool definition for voice-triggered withdrawals.
func WithdrawalTool() infraai.Tool {
	return infraai.Tool{
		Name: ToolInitiateWithdrawal,
		Description: `Withdraw money from the user's spend wallet to their linked bank account.
Use when the user says "send money to my bank", "withdraw to bank", "cash out", "I need naira in my account".

IMPORTANT: Before calling this tool, you MUST first call get_linked_banks to see the user's bank accounts.
Then confirm the details with the user: amount, currency, and which bank (name + last 4 digits).
Only call this tool AFTER the user confirms.`,
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"amount":          map[string]interface{}{"type": "number", "description": "Amount to withdraw"},
				"currency":        map[string]interface{}{"type": "string", "enum": []string{"NGN", "USD", "GBP", "EUR"}, "description": "Withdrawal currency"},
				"bank_account_id": map[string]interface{}{"type": "string", "description": "Bank account ID from get_linked_banks. If user has only one account for the currency, use that."},
			},
			"required":             []string{"amount", "currency"},
			"additionalProperties": false,
		},
	}
}

const ToolGetLinkedBanks = "get_linked_banks"

// LinkedBanksTool returns the tool to list user's bank accounts.
func LinkedBanksTool() infraai.Tool {
	return infraai.Tool{
		Name:        ToolGetLinkedBanks,
		Description: "Get the user's linked bank accounts. Call this when the user mentions withdrawing, sending to bank, or cashing out. Returns bank name, last 4 digits, currency. Call this BEFORE initiate_withdrawal.",
		Parameters: map[string]interface{}{
			"type":                 "object",
			"properties":           map[string]interface{}{},
			"required":             []string{},
			"additionalProperties": false,
		},
	}
}

func (o *Orchestrator) executeGetLinkedBanks(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	if o.bankAccountProvider == nil {
		return map[string]interface{}{"accounts": []interface{}{}, "message": "No bank accounts linked yet"}, nil
	}
	accounts, err := o.bankAccountProvider.GetByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(accounts) == 0 {
		return map[string]interface{}{"accounts": []interface{}{}, "message": "No bank accounts linked. User needs to add one in the app."}, nil
	}
	result := make([]map[string]interface{}, 0, len(accounts))
	for _, a := range accounts {
		acc := map[string]interface{}{
			"id":        a.ID.String(),
			"bank_name": a.BankName,
			"last4":     a.AccountNumberLast4,
			"currency":  string(a.Currency),
			"primary":   a.IsPrimary,
			"verified":  a.IsVerified,
		}
		result = append(result, acc)
	}
	return map[string]interface{}{"accounts": result}, nil
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
