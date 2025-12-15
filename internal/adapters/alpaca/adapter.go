package alpaca

import (
	"context"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/pkg/logger"
)

// Adapter implements the AlpacaAdapter interface for the funding service
type Adapter struct {
	client *Client
	logger *logger.Logger
}

// NewAdapter creates a new Alpaca adapter
func NewAdapter(client *Client, logger *logger.Logger) *Adapter {
	return &Adapter{
		client: client,
		logger: logger,
	}
}

// CreateAccount creates an Alpaca brokerage account
func (a *Adapter) CreateAccount(ctx context.Context, req *entities.AlpacaCreateAccountRequest) (*entities.AlpacaAccountResponse, error) {
	a.logger.Info("Creating Alpaca account", "email", req.Contact.EmailAddress)

	return a.client.CreateAccount(ctx, req)
}

// GetAccount retrieves an Alpaca account by ID
func (a *Adapter) GetAccount(ctx context.Context, accountID string) (*entities.AlpacaAccountResponse, error) {
	return a.client.GetAccount(ctx, accountID)
}

// InitiateInstantFunding creates an instant funding transfer
func (a *Adapter) InitiateInstantFunding(ctx context.Context, req *entities.AlpacaInstantFundingRequest) (*entities.AlpacaInstantFundingResponse, error) {
	fundingAdapter := NewFundingAdapter(a.client, a.logger.Desugar())
	return fundingAdapter.InitiateInstantFunding(ctx, req)
}

// GetInstantFundingStatus retrieves instant funding status
func (a *Adapter) GetInstantFundingStatus(ctx context.Context, transferID string) (*entities.AlpacaInstantFundingResponse, error) {
	fundingAdapter := NewFundingAdapter(a.client, a.logger.Desugar())
	return fundingAdapter.GetInstantFundingStatus(ctx, transferID)
}

// GetAccountBalance retrieves account balance
func (a *Adapter) GetAccountBalance(ctx context.Context, accountID string) (*entities.AlpacaAccountResponse, error) {
	fundingAdapter := NewFundingAdapter(a.client, a.logger.Desugar())
	return fundingAdapter.GetAccountBalance(ctx, accountID)
}

// CreateJournal creates a journal entry to transfer funds
func (a *Adapter) CreateJournal(ctx context.Context, req *entities.AlpacaJournalRequest) (*entities.AlpacaJournalResponse, error) {
	fundingAdapter := NewFundingAdapter(a.client, a.logger.Desugar())
	return fundingAdapter.CreateJournal(ctx, req)
}