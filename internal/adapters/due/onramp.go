package due

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// CreateRecipient creates a recipient for blockchain transfer
func (a *Adapter) CreateRecipient(ctx context.Context, name, address, chain string) (*CreateRecipientResponse, error) {
	a.logger.Info("Creating Due recipient",
		"name", name,
		"address", address,
		"chain", chain)

	schema := "evm"
	if chain == "solana" || chain == "sol_devnet" {
		schema = "solana"
	}

	req := &CreateRecipientRequest{
		Name: name,
		Details: RecipientDetails{
			Schema:  schema,
			Address: address,
		},
		IsExternal: false, // User's own wallet
	}

	resp, err := a.client.CreateRecipient(ctx, req)
	if err != nil {
		a.logger.Error("Failed to create recipient", "error", err)
		return nil, fmt.Errorf("create recipient failed: %w", err)
	}

	a.logger.Info("Recipient created", "recipient_id", resp.ID)
	return resp, nil
}

// CreateOnRampQuote generates a quote for USD to USDC conversion
func (a *Adapter) CreateOnRampQuote(ctx context.Context, usdAmount decimal.Decimal, chain string) (*OnRampQuoteResponse, error) {
	a.logger.Info("Creating on-ramp quote",
		"usd_amount", usdAmount.String(),
		"chain", chain)

	rail := "ethereum"
	if chain == "solana" || chain == "sol_devnet" {
		rail = "solana"
	}

	req := &OnRampQuoteRequest{
		Source: QuoteSource{
			Rail:     "ach",
			Currency: "USD",
			Amount:   usdAmount.String(),
		},
		Destination: QuoteDestination{
			Rail:     rail,
			Currency: "USDC",
			Amount:   "0", // Let Due calculate destination amount
		},
	}

	resp, err := a.client.CreateOnRampQuote(ctx, req)
	if err != nil {
		a.logger.Error("Failed to create quote", "error", err)
		return nil, fmt.Errorf("create quote failed: %w", err)
	}

	a.logger.Info("Quote created",
		"source_amount", resp.Source.Amount,
		"destination_amount", resp.Destination.Amount,
		"fx_rate", resp.FXRate)

	return resp, nil
}

// CreateOnRampTransfer initiates USD to USDC on-ramp transfer
func (a *Adapter) CreateOnRampTransfer(ctx context.Context, quoteToken, senderWalletID, recipientID, memo string) (*OnRampTransferResponse, error) {
	a.logger.Info("Creating on-ramp transfer",
		"sender_wallet_id", senderWalletID,
		"recipient_id", recipientID)

	req := &OnRampTransferRequest{
		Quote:     quoteToken,
		Sender:    senderWalletID,
		Recipient: recipientID,
		Memo:      memo,
	}

	resp, err := a.client.CreateOnRampTransfer(ctx, req)
	if err != nil {
		a.logger.Error("Failed to create transfer", "error", err)
		return nil, fmt.Errorf("create transfer failed: %w", err)
	}

	a.logger.Info("Transfer created",
		"transfer_id", resp.ID,
		"status", resp.Status)

	return resp, nil
}

// CreateFundingAddress creates a funding address for the transfer
func (a *Adapter) CreateFundingAddress(ctx context.Context, transferID string) (*FundingAddressResponse, error) {
	a.logger.Info("Creating funding address", "transfer_id", transferID)

	req := &FundingAddressRequest{
		Rail: "ethereum", // Default to ethereum
	}

	resp, err := a.client.CreateFundingAddress(ctx, transferID, req)
	if err != nil {
		a.logger.Error("Failed to create funding address", "error", err)
		return nil, fmt.Errorf("create funding address failed: %w", err)
	}

	a.logger.Info("Funding address created", "address", resp.Details.Address)
	return resp, nil
}

// GetTransferStatus retrieves the status of a transfer
func (a *Adapter) GetTransferStatus(ctx context.Context, transferID string) (*OnRampTransferResponse, error) {
	resp, err := a.client.GetTransfer(ctx, transferID)
	if err != nil {
		a.logger.Error("Failed to get transfer status", "error", err, "transfer_id", transferID)
		return nil, fmt.Errorf("get transfer status failed: %w", err)
	}

	// Convert CreateTransferResponse to OnRampTransferResponse
	onRampResp := &OnRampTransferResponse{
		ID:      resp.ID,
		OwnerID: resp.OwnerID,
		Status:  string(resp.Status),
		Source: QuoteSource{
			Rail:     resp.Source.Rail,
			Currency: resp.Source.Currency,
			Amount:   resp.Source.Amount,
		},
		Destination: QuoteDestination{
			Rail:     resp.Destination.Rail,
			Currency: resp.Destination.Currency,
			Amount:   resp.Destination.Amount,
		},
		FXRate:    resp.FXRate,
		CreatedAt: resp.CreatedAt,
	}

	return onRampResp, nil
}

// ProcessWithdrawal orchestrates the full USD to USDC withdrawal flow
func (a *Adapter) ProcessWithdrawal(ctx context.Context, req *entities.InitiateWithdrawalRequest) (*ProcessWithdrawalResponse, error) {
	a.logger.Info("Processing withdrawal",
		"user_id", req.UserID.String(),
		"amount", req.Amount.String(),
		"chain", req.DestinationChain,
		"address", req.DestinationAddress)

	// Step 1: Create recipient
	recipientName := fmt.Sprintf("User %s Wallet", req.UserID.String()[:8])
	recipient, err := a.CreateRecipient(ctx, recipientName, req.DestinationAddress, req.DestinationChain)
	if err != nil {
		return nil, fmt.Errorf("failed to create recipient: %w", err)
	}

	// Step 2: Generate quote
	quote, err := a.CreateOnRampQuote(ctx, req.Amount, req.DestinationChain)
	if err != nil {
		return nil, fmt.Errorf("failed to create quote: %w", err)
	}

	// Step 3: Get or create virtual account
	virtualAccountID, err := a.getOrCreateVirtualAccount(ctx, req.AlpacaAccountID)
	if err != nil {
		return nil, fmt.Errorf("failed to get virtual account: %w", err)
	}

	memo := fmt.Sprintf("Withdrawal for user %s", req.UserID.String())

	transfer, err := a.CreateOnRampTransfer(ctx, quote.Token, virtualAccountID, recipient.ID, memo)
	if err != nil {
		return nil, fmt.Errorf("failed to create transfer: %w", err)
	}

	// Step 4: Create funding address for USD deposit
	fundingAddr, err := a.CreateFundingAddress(ctx, transfer.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to create funding address: %w", err)
	}

	a.logger.Info("Withdrawal processing initiated",
		"transfer_id", transfer.ID,
		"recipient_id", recipient.ID,
		"funding_address", fundingAddr.Details.Address)

	return &ProcessWithdrawalResponse{
		TransferID:      transfer.ID,
		RecipientID:     recipient.ID,
		FundingAddress:  fundingAddr.Details.Address,
		SourceAmount:    quote.Source.Amount,
		DestAmount:      quote.Destination.Amount,
		Status:          transfer.Status,
	}, nil
}

// ProcessWithdrawalResponse contains the result of withdrawal processing
type ProcessWithdrawalResponse struct {
	TransferID     string `json:"transfer_id"`
	RecipientID    string `json:"recipient_id"`
	FundingAddress string `json:"funding_address"`
	SourceAmount   string `json:"source_amount"`
	DestAmount     string `json:"dest_amount"`
	Status         string `json:"status"`
}

func (a *Adapter) getOrCreateVirtualAccount(ctx context.Context, alpacaAccountID string) (string, error) {
	virtualAccountID := fmt.Sprintf("va_%s", alpacaAccountID)
	a.logger.Info("Using virtual account", "virtual_account_id", virtualAccountID)
	return virtualAccountID, nil
}
