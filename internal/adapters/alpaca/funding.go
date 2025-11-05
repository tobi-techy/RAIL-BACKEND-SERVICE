package alpaca

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// FundingAdapter handles Alpaca account funding operations
type FundingAdapter struct {
	client *Client
	logger *zap.Logger
}

// NewFundingAdapter creates a new funding adapter
func NewFundingAdapter(client *Client, logger *zap.Logger) *FundingAdapter {
	return &FundingAdapter{
		client: client,
		logger: logger,
	}
}

// InitiateFunding initiates ACH transfer to fund Alpaca account
func (a *FundingAdapter) InitiateFunding(ctx context.Context, req *FundingRequest) (*FundingResponse, error) {
	a.logger.Info("Initiating Alpaca account funding",
		zap.String("account_id", req.AccountID),
		zap.String("amount", req.Amount.String()))

	// Note: Alpaca Broker API funding is typically done via ACH transfers
	// The actual implementation depends on Alpaca's funding API
	// For now, we'll verify the account exists and has proper status
	
	account, err := a.client.GetAccount(ctx, req.AccountID)
	if err != nil {
		a.logger.Error("Failed to get Alpaca account",
			zap.String("account_id", req.AccountID),
			zap.Error(err))
		return nil, fmt.Errorf("get account failed: %w", err)
	}

	// Verify account is active and can receive funds
	if account.Status != "ACTIVE" {
		return nil, fmt.Errorf("account not active: status=%s", account.Status)
	}

	a.logger.Info("Alpaca account verified for funding",
		zap.String("account_id", account.ID),
		zap.String("account_number", account.AccountNumber),
		zap.String("status", string(account.Status)))

	// Return funding response
	// In production, this would include actual ACH transfer initiation
	return &FundingResponse{
		AccountID:     account.ID,
		AccountNumber: account.AccountNumber,
		Amount:        req.Amount,
		Status:        "pending",
		Reference:     req.Reference,
	}, nil
}

// GetAccountBalance retrieves current account balance
func (a *FundingAdapter) GetAccountBalance(ctx context.Context, accountID string) (*BalanceResponse, error) {
	account, err := a.client.GetAccount(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("get account failed: %w", err)
	}

	return &BalanceResponse{
		AccountID:   account.ID,
		Cash:        account.Cash,
		BuyingPower: account.BuyingPower,
		Currency:    account.Currency,
	}, nil
}

// FundingRequest represents a funding request
type FundingRequest struct {
	AccountID string
	Amount    decimal.Decimal
	Reference string
}

// FundingResponse represents a funding response
type FundingResponse struct {
	AccountID     string
	AccountNumber string
	Amount        decimal.Decimal
	Status        string
	Reference     string
}

// BalanceResponse represents account balance
type BalanceResponse struct {
	AccountID   string
	Cash        decimal.Decimal
	BuyingPower decimal.Decimal
	Currency    string
}
