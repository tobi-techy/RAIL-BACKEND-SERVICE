package treasury

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/adapters/due"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/pkg/logger"
)

// DueProvider implements the ConversionProvider interface for Due
type DueProvider struct {
	client *due.Client
	logger *logger.Logger
	config entities.ConversionProvider
}

// NewDueProvider creates a new Due conversion provider
func NewDueProvider(client *due.Client, config entities.ConversionProvider, logger *logger.Logger) *DueProvider {
	return &DueProvider{
		client: client,
		logger: logger,
		config: config,
	}
}

// GetName returns the provider name
func (p *DueProvider) GetName() string {
	return p.config.Name
}

// GetProviderType returns the provider type
func (p *DueProvider) GetProviderType() string {
	return p.config.ProviderType
}

// InitiateConversion initiates a conversion operation via Due
func (p *DueProvider) InitiateConversion(ctx context.Context, req *ConversionRequest) (*ConversionResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, NewNonRetryableError(fmt.Errorf("invalid conversion request: %w", err))
	}

	p.logger.Info("Initiating Due conversion",
		"direction", req.Direction,
		"amount", req.SourceAmount,
		"idempotency_key", req.IdempotencyKey)

	// Convert to Due-specific request format
	var dueResp interface{}
	var providerTxID string
	var err error

	if req.Direction == entities.ConversionDirectionUSDCToUSD {
		// Off-ramp: USDC -> USD
		providerTxID, dueResp, err = p.initiateOfframp(ctx, req)
	} else {
		// On-ramp: USD -> USDC
		providerTxID, dueResp, err = p.initiateOnramp(ctx, req)
	}

	if err != nil {
		p.logger.Error("Failed to initiate Due conversion", "error", err)
		return nil, err
	}

	// Marshal provider response to map
	respBytes, _ := json.Marshal(dueResp)
	var providerResponseMap map[string]interface{}
	json.Unmarshal(respBytes, &providerResponseMap)

	response := &ConversionResponse{
		ProviderTxID:            providerTxID,
		Status:                  "pending",
		SourceAmount:            req.SourceAmount,
		DestinationAmount:       nil, // Due doesn't provide this immediately
		ExchangeRate:            nil,
		Fees:                    nil,
		EstimatedCompletionTime: p.getEstimatedCompletionTime(),
		ProviderResponse:        providerResponseMap,
	}

	p.logger.Info("Due conversion initiated",
		"provider_tx_id", providerTxID,
		"status", response.Status)

	return response, nil
}

// initiateOfframp initiates an off-ramp conversion (USDC -> USD)
func (p *DueProvider) initiateOfframp(ctx context.Context, req *ConversionRequest) (string, interface{}, error) {
	// For Due off-ramp, we typically create a quote first, then execute transfer
	// This is a simplified implementation - adjust based on actual Due API
	
	quoteReq := &due.CreateQuoteRequest{
		Sender:    p.getSystemSenderID(),    // System wallet ID
		Recipient: p.getSystemRecipientID(), // System USD recipient
		Amount:    req.SourceAmount.String(),
		Currency:  "USDC",
	}

	quote, err := p.client.CreateQuote(ctx, quoteReq)
	if err != nil {
		return "", nil, NewRetryableError(fmt.Errorf("failed to create Due quote: %w", err))
	}

	// In a real implementation, you would then execute the transfer using the quote
	// For now, we'll use the quote ID as the transaction ID
	return quote.ID, quote, nil
}

// initiateOnramp initiates an on-ramp conversion (USD -> USDC)
func (p *DueProvider) initiateOnramp(ctx context.Context, req *ConversionRequest) (string, interface{}, error) {
	// For Due on-ramp, create a quote for USD -> USDC conversion
	
	quoteReq := &due.CreateQuoteRequest{
		Sender:    p.getSystemSenderID(),    // System USD account
		Recipient: p.getSystemRecipientID(), // System USDC wallet
		Amount:    req.SourceAmount.String(),
		Currency:  "USD",
	}

	quote, err := p.client.CreateQuote(ctx, quoteReq)
	if err != nil {
		return "", nil, NewRetryableError(fmt.Errorf("failed to create Due quote: %w", err))
	}

	return quote.ID, quote, nil
}

// GetConversionStatus checks the status of a conversion
func (p *DueProvider) GetConversionStatus(ctx context.Context, providerTxID string) (*ConversionStatusResponse, error) {
	p.logger.Debug("Checking Due conversion status", "provider_tx_id", providerTxID)

	// Query Due API for transfer status
	// This is a simplified implementation - adjust based on actual Due API
	filters := &due.TransferFilters{
		Limit: 100,
		Order: "desc",
	}
	
	transfers, err := p.client.ListTransfers(ctx, filters)
	if err != nil {
		return nil, NewRetryableError(fmt.Errorf("failed to get transfers: %w", err))
	}

	// Find the transfer by ID
	var transfer *due.CreateTransferResponse
	for _, t := range transfers.Data {
		if t.ID == providerTxID {
			transfer = &t
			break
		}
	}

	if transfer == nil {
		return nil, NewNonRetryableError(fmt.Errorf("transfer not found: %s", providerTxID))
	}

	// Convert Due status to our status
	status := p.mapDueStatusToProviderStatus(transfer.Status)
	
	// Parse amounts
	var sourceAmount, destinationAmount, exchangeRate, fees *decimal.Decimal
	if transfer.Source.Amount != "" {
		if amt, err := decimal.NewFromString(transfer.Source.Amount); err == nil {
			sourceAmount = &amt
		}
	}
	if transfer.Source.Fee != "" {
		if fee, err := decimal.NewFromString(transfer.Source.Fee); err == nil {
			fees = &fee
		}
	}
	if transfer.Destination.Amount != "" {
		if amt, err := decimal.NewFromString(transfer.Destination.Amount); err == nil {
			destinationAmount = &amt
		}
	}
	if transfer.FXRate > 0 {
		rate := decimal.NewFromFloat(transfer.FXRate)
		exchangeRate = &rate
	}

	// Marshal provider response
	respBytes, _ := json.Marshal(transfer)
	var providerResponseMap map[string]interface{}
	json.Unmarshal(respBytes, &providerResponseMap)

	response := &ConversionStatusResponse{
		ProviderTxID:      providerTxID,
		Status:            status,
		SourceAmount:      *sourceAmount,
		DestinationAmount: destinationAmount,
		ExchangeRate:      exchangeRate,
		Fees:              fees,
		CompletedAt:       p.getCompletedAtTime(transfer),
		FailureReason:     p.getFailureReason(transfer),
		ProviderResponse:  providerResponseMap,
	}

	return response, nil
}

// CancelConversion cancels a pending conversion
func (p *DueProvider) CancelConversion(ctx context.Context, providerTxID string) error {
	p.logger.Info("Cancelling Due conversion", "provider_tx_id", providerTxID)

	// Due API doesn't currently support cancellation in their documented API
	// This would need to be implemented once Due provides this capability
	return NewNonRetryableError(fmt.Errorf("conversion cancellation not supported by Due"))
}

// SupportsDirection checks if provider supports the conversion direction
func (p *DueProvider) SupportsDirection(direction entities.ConversionDirection) bool {
	if direction == entities.ConversionDirectionUSDCToUSD {
		return p.config.SupportsUSDCToUSD
	}
	if direction == entities.ConversionDirectionUSDToUSDC {
		return p.config.SupportsUSDToUSDC
	}
	return false
}

// ValidateAmount checks if the amount is within provider's limits
func (p *DueProvider) ValidateAmount(amount decimal.Decimal, direction entities.ConversionDirection) error {
	if !p.SupportsDirection(direction) {
		return fmt.Errorf("direction not supported: %s", direction)
	}

	if amount.LessThan(p.config.MinConversionAmount) {
		return fmt.Errorf("amount %s is below minimum %s", amount, p.config.MinConversionAmount)
	}

	if p.config.MaxConversionAmount != nil && amount.GreaterThan(*p.config.MaxConversionAmount) {
		return fmt.Errorf("amount %s exceeds maximum %s", amount, *p.config.MaxConversionAmount)
	}

	return nil
}

// EstimateFees estimates fees for a conversion
func (p *DueProvider) EstimateFees(ctx context.Context, amount decimal.Decimal, direction entities.ConversionDirection) (*FeeEstimate, error) {
	// Due provides fee estimates in quotes
	// For now, return a conservative estimate
	// In production, you'd call the actual Due API to get a quote
	
	feeRate := decimal.NewFromFloat(0.005) // 0.5% fee estimate
	totalFee := amount.Mul(feeRate)
	
	estimatedOutput := amount.Sub(totalFee)
	estimatedRate := decimal.NewFromInt(1) // 1:1 for stablecoins

	return &FeeEstimate{
		TotalFee:        totalFee,
		NetworkFee:      nil,
		ProviderFee:     &totalFee,
		EstimatedRate:   &estimatedRate,
		EstimatedOutput: &estimatedOutput,
	}, nil
}

// Helper methods

func (p *DueProvider) mapDueStatusToProviderStatus(dueStatus due.TransferStatus) ConversionProviderStatus {
	switch dueStatus {
	case due.TransferStatusPending:
		return ConversionProviderStatusPending
	case due.TransferStatusPaymentProcessed:
		return ConversionProviderStatusProcessing
	case due.TransferStatusCompleted:
		return ConversionProviderStatusCompleted
	case due.TransferStatusFailed:
		return ConversionProviderStatusFailed
	default:
		return ConversionProviderStatusFailed
	}
}

func (p *DueProvider) getCompletedAtTime(transfer *due.CreateTransferResponse) *string {
	// Due API doesn't provide CompletedAt in the response
	// Return nil for now
	return nil
}

func (p *DueProvider) getFailureReason(transfer *due.CreateTransferResponse) *string {
	// Due API doesn't provide failure reason in the response
	// Return nil for now
	return nil
}

func (p *DueProvider) getSystemSenderID() string {
	// Return configured system sender ID
	// This should be configured in the provider config
	return "system-sender-id"
}

func (p *DueProvider) getSystemRecipientID() string {
	// Return configured system recipient ID
	// This should be configured in the provider config
	return "system-recipient-id"
}

func (p *DueProvider) getEstimatedCompletionTime() *int {
	// Return estimated completion time in seconds
	// For Due, this is typically 1-3 business days
	// Using 2 days (48 hours) as estimate
	estimated := 172800 // 48 hours in seconds
	return &estimated
}


