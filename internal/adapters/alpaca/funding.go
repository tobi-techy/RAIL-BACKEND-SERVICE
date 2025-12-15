package alpaca

import (
	"context"
	"fmt"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

const (
	instantFundingEndpoint = "/v1/instant_funding"
	instantFundingLimitsEndpoint = "/v1/instant_funding/limits"
	instantFundingSettlementsEndpoint = "/v1/instant_funding/settlements"
	journalsEndpoint = "/v1/journals"
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

// InitiateInstantFunding creates an instant funding transfer to extend buying power immediately
func (a *FundingAdapter) InitiateInstantFunding(ctx context.Context, req *entities.AlpacaInstantFundingRequest) (*entities.AlpacaInstantFundingResponse, error) {
	a.logger.Info("Initiating Alpaca instant funding",
		zap.String("account_no", req.AccountNo),
		zap.String("amount", req.Amount.String()))

	var response entities.AlpacaInstantFundingResponse
	_, err := a.client.circuitBreaker.Execute(func() (interface{}, error) {
		return &response, a.client.doRequestWithRetry(ctx, "POST", instantFundingEndpoint, req, &response, false)
	})

	if err != nil {
		a.logger.Error("Failed to create instant funding transfer",
			zap.String("account_no", req.AccountNo),
			zap.Error(err))
		return nil, fmt.Errorf("instant funding failed: %w", err)
	}

	a.logger.Info("Instant funding transfer created",
		zap.String("transfer_id", response.ID),
		zap.String("status", response.Status),
		zap.String("deadline", response.Deadline))

	return &response, nil
}

// GetInstantFundingStatus retrieves the status of an instant funding transfer
func (a *FundingAdapter) GetInstantFundingStatus(ctx context.Context, transferID string) (*entities.AlpacaInstantFundingResponse, error) {
	endpoint := fmt.Sprintf("%s/%s", instantFundingEndpoint, transferID)

	var response entities.AlpacaInstantFundingResponse
	_, err := a.client.circuitBreaker.Execute(func() (interface{}, error) {
		return &response, a.client.doRequestWithRetry(ctx, "GET", endpoint, nil, &response, false)
	})

	if err != nil {
		a.logger.Error("Failed to get instant funding status",
			zap.String("transfer_id", transferID),
			zap.Error(err))
		return nil, fmt.Errorf("get instant funding status failed: %w", err)
	}

	return &response, nil
}

// GetInstantFundingLimits retrieves instant funding limits at correspondent level
func (a *FundingAdapter) GetInstantFundingLimits(ctx context.Context) (*entities.AlpacaInstantFundingLimitsResponse, error) {
	var response entities.AlpacaInstantFundingLimitsResponse
	_, err := a.client.circuitBreaker.Execute(func() (interface{}, error) {
		return &response, a.client.doRequestWithRetry(ctx, "GET", instantFundingLimitsEndpoint, nil, &response, false)
	})

	if err != nil {
		a.logger.Error("Failed to get instant funding limits", zap.Error(err))
		return nil, fmt.Errorf("get instant funding limits failed: %w", err)
	}

	return &response, nil
}

// CreateJournal creates a journal entry to transfer funds between accounts
func (a *FundingAdapter) CreateJournal(ctx context.Context, req *entities.AlpacaJournalRequest) (*entities.AlpacaJournalResponse, error) {
	a.logger.Info("Creating Alpaca journal",
		zap.String("from_account", req.FromAccount),
		zap.String("to_account", req.ToAccount),
		zap.String("amount", req.Amount.String()))

	var response entities.AlpacaJournalResponse
	_, err := a.client.circuitBreaker.Execute(func() (interface{}, error) {
		return &response, a.client.doRequestWithRetry(ctx, "POST", journalsEndpoint, req, &response, false)
	})

	if err != nil {
		a.logger.Error("Failed to create journal",
			zap.String("from_account", req.FromAccount),
			zap.String("to_account", req.ToAccount),
			zap.Error(err))
		return nil, fmt.Errorf("create journal failed: %w", err)
	}

	a.logger.Info("Journal created successfully",
		zap.String("journal_id", response.ID),
		zap.String("status", response.Status))

	return &response, nil
}

// GetAccountBalance retrieves current account balance with buying power
func (a *FundingAdapter) GetAccountBalance(ctx context.Context, accountID string) (*entities.AlpacaAccountResponse, error) {
	account, err := a.client.GetAccount(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("get account failed: %w", err)
	}

	a.logger.Info("Retrieved account balance",
		zap.String("account_id", account.ID),
		zap.String("buying_power", account.BuyingPower.String()),
		zap.String("cash", account.Cash.String()))

	return account, nil
}
