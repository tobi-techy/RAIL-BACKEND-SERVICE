package due

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/pkg/logger"
)

// OffRampAdapter handles USDC to USD off-ramp operations
type OffRampAdapter struct {
	client *Client
	logger *logger.Logger
}

// NewOffRampAdapter creates a new off-ramp adapter
func NewOffRampAdapter(client *Client, logger *logger.Logger) *OffRampAdapter {
	return &OffRampAdapter{
		client: client,
		logger: logger,
	}
}

// InitiateOffRamp creates a transfer to convert USDC to USD
func (a *OffRampAdapter) InitiateOffRamp(ctx context.Context, req *OffRampRequest) (*OffRampResponse, error) {
	a.logger.Info("Initiating off-ramp transfer",
		"virtual_account_id", req.VirtualAccountID,
		"recipient_id", req.RecipientID,
		"amount", req.Amount.String())

	// Create transfer request
	transferReq := &CreateTransferRequest{
		SourceID:      req.VirtualAccountID,
		DestinationID: req.RecipientID,
		Amount:        req.Amount.String(),
		Currency:      "USDC",
		Reference:     req.Reference,
	}

	// Execute transfer
	transferResp, err := a.client.CreateTransfer(ctx, transferReq)
	if err != nil {
		a.logger.Error("Failed to create off-ramp transfer",
			"reference", req.Reference,
			"error", err)
		return nil, fmt.Errorf("create transfer failed: %w", err)
	}

	a.logger.Info("Off-ramp transfer created",
		"transfer_id", transferResp.ID,
		"status", transferResp.Status)

	return &OffRampResponse{
		TransferID: transferResp.ID,
		Status:     transferResp.Status,
		SourceAmount: decimal.RequireFromString(transferResp.Source.Amount),
		DestAmount: decimal.RequireFromString(transferResp.Destination.Amount),
		Fee: decimal.RequireFromString(transferResp.Destination.Fee),
	}, nil
}

// GetTransferStatus retrieves the current status of a transfer
func (a *OffRampAdapter) GetTransferStatus(ctx context.Context, transferID string) (*OffRampResponse, error) {
	transfer, err := a.client.GetTransfer(ctx, transferID)
	if err != nil {
		return nil, fmt.Errorf("get transfer failed: %w", err)
	}

	return &OffRampResponse{
		TransferID: transfer.ID,
		Status:     transfer.Status,
		SourceAmount: decimal.RequireFromString(transfer.Source.Amount),
		DestAmount: decimal.RequireFromString(transfer.Destination.Amount),
		Fee: decimal.RequireFromString(transfer.Destination.Fee),
	}, nil
}

// OffRampRequest represents an off-ramp request
type OffRampRequest struct {
	VirtualAccountID string
	RecipientID      string
	Amount           decimal.Decimal
	Reference        string
}

// OffRampResponse represents an off-ramp response
type OffRampResponse struct {
	TransferID   string
	Status       TransferStatus
	SourceAmount decimal.Decimal
	DestAmount   decimal.Decimal
	Fee          decimal.Decimal
}
