package due

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/pkg/logger"
)

// Adapter implements the DueAdapter interface
type Adapter struct {
	client DueClient
	logger *logger.Logger
}

// NewAdapter creates a new Due adapter
func NewAdapter(client DueClient, logger *logger.Logger) *Adapter {
	return &Adapter{
		client: client,
		logger: logger,
	}
}

// CreateAccount creates a Due account
func (a *Adapter) CreateAccount(ctx context.Context, req *entities.CreateAccountRequest) (*entities.CreateAccountResponse, error) {
	a.logger.Info("Creating Due account", "email", req.Email, "type", req.Type)

	return a.client.CreateAccount(ctx, req)
}

// CreateVirtualAccount creates a virtual account via Due API and returns domain entity
func (a *Adapter) CreateVirtualAccount(ctx context.Context, userID uuid.UUID, alpacaAccountID string) (*entities.VirtualAccount, error) {
	a.logger.Info("Creating virtual account via Due adapter",
		"user_id", userID.String(),
		"alpaca_account_id", alpacaAccountID)

	// Generate unique reference for tracking
	reference := fmt.Sprintf("alpaca_%s_%s", alpacaAccountID, userID.String()[:8])

	// Prepare Due API request for USD ACH virtual account
	// This creates a US bank account that will receive USD and settle to the destination
	req := &CreateVirtualAccountRequest{
		Destination:  alpacaAccountID, // Alpaca account ID as destination
		SchemaIn:     "bank_us",       // US bank transfer input
		CurrencyIn:   "USD",           // Accept USD deposits
		RailOut:      "ach",           // Settle via ACH
		CurrencyOut:  "USD",           // Output in USD
		Reference:    reference,       // Unique tracking reference
	}

	// Call Due API
	dueResponse, err := a.client.CreateVirtualAccount(ctx, req)
	if err != nil {
		a.logger.Error("Failed to create virtual account via Due API",
			"user_id", userID.String(),
			"error", err)
		return nil, fmt.Errorf("Due API call failed: %w", err)
	}

	// Convert Due API response to domain entity
	now := time.Now()
	status := entities.VirtualAccountStatusPending
	if dueResponse.IsActive {
		status = entities.VirtualAccountStatusActive
	}

	// Extract account details from Due response
	accountNumber := dueResponse.Details.AccountNumber
	routingNumber := dueResponse.Details.RoutingNumber
	if accountNumber == "" && dueResponse.Details.IBAN != "" {
		// For SEPA accounts, use IBAN as account number
		accountNumber = dueResponse.Details.IBAN
	}

	virtualAccount := &entities.VirtualAccount{
		ID:              uuid.New(),
		UserID:          userID,
		DueAccountID:    dueResponse.Nonce, // Due uses nonce as the unique identifier
		AlpacaAccountID: alpacaAccountID,
		AccountNumber:   accountNumber,
		RoutingNumber:   routingNumber,
		Status:          status,
		Currency:        "USD",
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	a.logger.Info("Successfully created virtual account",
		"virtual_account_id", virtualAccount.ID.String(),
		"due_account_id", virtualAccount.DueAccountID,
		"account_number", virtualAccount.AccountNumber,
		"is_active", dueResponse.IsActive)

	return virtualAccount, nil
}