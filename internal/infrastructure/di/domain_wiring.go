package di

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/api/handlers"
	p2phandlers "github.com/rail-service/rail_service/internal/api/handlers/p2p"
	"github.com/rail-service/rail_service/internal/api/handlers/webhooks"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services"
	"github.com/rail-service/rail_service/internal/domain/services/account"
	"github.com/rail-service/rail_service/internal/domain/services/allocation"
	"github.com/rail-service/rail_service/internal/domain/services/apikey"
	"github.com/rail-service/rail_service/internal/domain/services/audit"
	"github.com/rail-service/rail_service/internal/domain/services/autoinvest"
	"github.com/rail-service/rail_service/internal/domain/services/automation"
	aiservice "github.com/rail-service/rail_service/internal/domain/services/ai"
	"github.com/rail-service/rail_service/internal/domain/services/card"
	compliancesvc "github.com/rail-service/rail_service/internal/domain/services/compliance"
	"github.com/rail-service/rail_service/internal/domain/services/consciousspending"
	"github.com/rail-service/rail_service/internal/domain/services/funding"
	"github.com/rail-service/rail_service/internal/domain/services/gameplay"
	"github.com/rail-service/rail_service/internal/domain/services/goals"
	"github.com/rail-service/rail_service/internal/domain/services/growthengine"
	"github.com/rail-service/rail_service/internal/domain/services/integration"
	"github.com/rail-service/rail_service/internal/domain/services/investing"
	"github.com/rail-service/rail_service/internal/domain/services/kyc"
	"github.com/rail-service/rail_service/internal/domain/services/ledger"
	"github.com/rail-service/rail_service/internal/domain/services/limits"
	miriamservice "github.com/rail-service/rail_service/internal/domain/services/miriam"
	moneyguardservice "github.com/rail-service/rail_service/internal/domain/services/moneyguard"
	obligationservice "github.com/rail-service/rail_service/internal/domain/services/obligation"
	"github.com/rail-service/rail_service/internal/domain/services/onboarding"
	"github.com/rail-service/rail_service/internal/domain/services/p2p"
	"github.com/rail-service/rail_service/internal/domain/services/passcode"
	"github.com/rail-service/rail_service/internal/domain/services/premium"
	"github.com/rail-service/rail_service/internal/domain/services/security"
	"github.com/rail-service/rail_service/internal/domain/services/session"
	"github.com/rail-service/rail_service/internal/domain/services/sharedgoal"
	"github.com/rail-service/rail_service/internal/domain/services/socialauth"
	spendingsvc "github.com/rail-service/rail_service/internal/domain/services/spending"
	spendingcommitmentservice "github.com/rail-service/rail_service/internal/domain/services/spendingcommitment"
	"github.com/rail-service/rail_service/internal/domain/services/stashlock"
	"github.com/rail-service/rail_service/internal/domain/services/station"
	"github.com/rail-service/rail_service/internal/domain/services/strategy"
	subscriptionsvc "github.com/rail-service/rail_service/internal/domain/services/subscription"
	"github.com/rail-service/rail_service/internal/domain/services/twofa"
	"github.com/rail-service/rail_service/internal/domain/services/umbrawallet"
	"github.com/rail-service/rail_service/internal/domain/services/wallet"
	"github.com/rail-service/rail_service/internal/domain/services/webauthn"
	yieldsvc "github.com/rail-service/rail_service/internal/domain/services/yield"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/alpaca"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/blend"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/bridge"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/didit"
	platform "github.com/rail-service/rail_service/internal/infrastructure/platform"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	recon "github.com/rail-service/rail_service/internal/workers/reconciliation"
	revenue_sweep "github.com/rail-service/rail_service/internal/workers/revenue_sweep"
	"github.com/rail-service/rail_service/pkg/auth"
	"github.com/rail-service/rail_service/pkg/captcha"
	"github.com/rail-service/rail_service/pkg/ratelimit"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// BridgeWalletBalanceAdapter adapts bridge.Adapter to services that need (customerID, walletID) -> string balance
type BridgeWalletBalanceAdapter struct {
	adapter *bridge.Adapter
}

// circleWalletLookupAdapter adapts WalletRepository to webhooks.CircleWalletLookup.
type circleWalletLookupAdapter struct {
	repo *repositories.WalletRepository
}

func (a *circleWalletLookupAdapter) GetUserByCircleWalletID(ctx context.Context, circleWalletID string) (uuid.UUID, error) {
	wallet, err := a.repo.GetByCircleWalletID(ctx, circleWalletID)
	if err != nil {
		return uuid.Nil, err
	}
	return wallet.UserID, nil
}

// BridgeWalletProvisioningAdapter adapts bridge.Client to wallet.BridgeWalletLister
type BridgeWalletProvisioningAdapter struct {
	client *bridge.Client
}

func (a *BridgeWalletProvisioningAdapter) CreateWalletForCustomer(ctx context.Context, customerID string, chain string) (*entities.ManagedWallet, error) {
	bridgeChain := entities.WalletChain(chain).ToBridgeWalletChain()
	if bridgeChain == "" {
		return nil, fmt.Errorf("unsupported chain: %s", chain)
	}

	idempotencyKey := fmt.Sprintf("wallet-%s-%s-%s", customerID, chain, bridgeChain)
	ctxWithKey := bridge.WithIdempotencyKey(ctx, idempotencyKey)

	w, err := a.client.CreateWallet(ctxWithKey, customerID, &bridge.CreateWalletRequest{
		Chain:    bridge.PaymentRail(bridgeChain),
		Currency: bridge.CurrencyUSDC,
	})
	if err != nil {
		return nil, err
	}
	return w.ToDomainManagedWallet(uuid.New(), uuid.Nil), nil
}

func (a *BridgeWalletProvisioningAdapter) ListWallets(ctx context.Context, customerID string) ([]*entities.ManagedWallet, error) {
	resp, err := a.client.ListWallets(ctx, customerID)
	if err != nil {
		return nil, err
	}
	wallets := make([]*entities.ManagedWallet, 0, len(resp.Data))
	for _, w := range resp.Data {
		wallets = append(wallets, w.ToDomainManagedWallet(uuid.New(), uuid.Nil))
	}
	return wallets, nil
}

// UserProfileProviderAdapter adapts the user repository to wallet.UserProfileProvider
type UserProfileProviderAdapter struct {
	repo interface {
		GetByID(ctx context.Context, id uuid.UUID) (*entities.UserProfile, error)
	}
}

func (a *UserProfileProviderAdapter) GetBridgeCustomerID(ctx context.Context, userID uuid.UUID) (string, error) {
	profile, err := a.repo.GetByID(ctx, userID)
	if err != nil {
		return "", err
	}
	if profile.BridgeCustomerID == nil || *profile.BridgeCustomerID == "" {
		return "", fmt.Errorf("no bridge customer ID for user %s", userID)
	}
	return *profile.BridgeCustomerID, nil
}

func (a *BridgeWalletBalanceAdapter) GetWalletBalance(ctx context.Context, customerID, walletID string) (string, error) {
	bal, err := a.adapter.GetWalletBalance(ctx, customerID, walletID)
	if err != nil {
		return "0", err
	}
	return bal.GetUSDCAmount(), nil
}

func (a *BridgeWalletBalanceAdapter) TransferFunds(ctx context.Context, req map[string]interface{}) (map[string]interface{}, error) {
	// Minimal sweep implementation: extract fields and call bridge adapter
	amount, _ := req["amount"].(string)
	walletID, _ := req["source_wallet"].(string)
	toAddress, _ := req["destination"].(string)
	onBehalfOf, _ := req["on_behalf_of"].(string)
	transfer, err := a.adapter.TransferFunds(ctx, &bridge.CreateTransferRequest{
		OnBehalfOf: onBehalfOf,
		Amount:     amount,
		Source: bridge.TransferSource{
			PaymentRail:    bridge.PaymentRail("bridge_wallet"),
			Currency:       bridge.CurrencyUSDC,
			BridgeWalletID: walletID,
		},
		Destination: bridge.TransferDestination{
			PaymentRail: bridge.PaymentRailSolana,
			Currency:    bridge.CurrencyUSDC,
			ToAddress:   toAddress,
		},
	})
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"id": transfer.ID, "state": string(transfer.State)}, nil
}

// BridgeDepositAdapter adapts bridge.Client to funding.BridgeDepositClient interface
type BridgeDepositAdapter struct {
	client *bridge.Client
}

func (a *BridgeDepositAdapter) ListWallets(ctx context.Context, customerID string) ([]funding.BridgeWalletInfo, error) {
	resp, err := a.client.ListWallets(ctx, customerID)
	if err != nil {
		return nil, err
	}
	out := make([]funding.BridgeWalletInfo, len(resp.Data))
	for i, w := range resp.Data {
		out[i] = funding.BridgeWalletInfo{ID: w.ID, Chain: string(w.Chain), Currency: string(w.Currency), Address: w.Address}
	}
	return out, nil
}

func (a *BridgeDepositAdapter) CreateWallet(ctx context.Context, customerID string, chain string, currency string) (string, string, error) {
	cur := bridge.CurrencyUSDC
	if currency != "" {
		cur = bridge.StablecoinToBridgeCurrency(currency)
	}
	w, err := a.client.CreateWallet(ctx, customerID, &bridge.CreateWalletRequest{Chain: bridge.PaymentRail(chain), Currency: cur})
	if err != nil {
		return "", "", err
	}
	return w.ID, w.Address, nil
}

func (a *BridgeDepositAdapter) ListLiquidationAddresses(ctx context.Context, customerID string) ([]funding.BridgeLiquidationAddr, error) {
	resp, err := a.client.ListLiquidationAddresses(ctx, customerID)
	if err != nil {
		return nil, err
	}
	out := make([]funding.BridgeLiquidationAddr, len(resp.Data))
	for i, la := range resp.Data {
		out[i] = funding.BridgeLiquidationAddr{
			ID:                 la.ID,
			Chain:              string(la.Chain),
			Currency:           string(la.Currency),
			Address:            la.Address,
			DestinationAddress: la.DestinationAddress,
		}
	}
	return out, nil
}

func (a *BridgeDepositAdapter) IsSandbox() bool {
	return strings.EqualFold(strings.TrimSpace(a.client.Config().Environment), "sandbox")
}

func (a *BridgeDepositAdapter) CreateLiquidationAddress(ctx context.Context, customerID string, sourceChain string, destinationChain string, destinationAddress string) (string, string, error) {
	sourceRail := entities.WalletChain(sourceChain).ToBridgePaymentRail()
	destRail := entities.WalletChain(destinationChain).ToBridgePaymentRail()
	if sourceRail == "" {
		return "", "", fmt.Errorf("unsupported source chain: %s", sourceChain)
	}
	if destRail == "" {
		return "", "", fmt.Errorf("unsupported destination chain: %s", destinationChain)
	}
	req := &bridge.CreateLiquidationAddressRequest{
		Chain:                  bridge.PaymentRail(sourceRail),
		Currency:               bridge.CurrencyUSDC,
		DestinationPaymentRail: bridge.PaymentRail(destRail),
		DestinationCurrency:    bridge.CurrencyUSDC,
		DestinationAddress:     destinationAddress,
	}
	la, err := a.client.CreateLiquidationAddress(ctx, customerID, req)
	if err != nil {
		return "", "", err
	}
	return la.ID, la.Address, nil
}

func (a *BridgeDepositAdapter) CreateLiquidationAddressForWallet(ctx context.Context, customerID string, sourceChain string, walletID string, walletAddress string) (string, string, error) {
	sourceRail := entities.WalletChain(sourceChain).ToBridgePaymentRail()
	walletChain := entities.WalletChain(sourceChain).ToBridgeWalletChain()

	req := &bridge.CreateLiquidationAddressRequest{
		Chain:                  bridge.PaymentRail(sourceRail),
		Currency:               bridge.CurrencyUSDC,
		DestinationPaymentRail: bridge.PaymentRail(walletChain),
		DestinationCurrency:    bridge.CurrencyUSDC,
		DestinationAddress:     walletAddress,
	}

	la, err := a.client.CreateLiquidationAddress(ctx, customerID, req)
	if err != nil {
		return "", "", err
	}
	return la.ID, la.Address, nil
}

// AlpacaFundingAdapter adapts alpaca.FundingAdapter to funding.AlpacaAdapter interface
type AlpacaFundingAdapter struct {
	adapter *alpaca.FundingAdapter
	client  *alpaca.Client
}

func (a *AlpacaFundingAdapter) GetAccount(ctx context.Context, accountID string) (*entities.AlpacaAccountResponse, error) {
	return a.client.GetAccount(ctx, accountID)
}

func (a *AlpacaFundingAdapter) InitiateInstantFunding(ctx context.Context, req *entities.AlpacaInstantFundingRequest) (*entities.AlpacaInstantFundingResponse, error) {
	return a.adapter.InitiateInstantFunding(ctx, req)
}

func (a *AlpacaFundingAdapter) GetInstantFundingStatus(ctx context.Context, transferID string) (*entities.AlpacaInstantFundingResponse, error) {
	return a.adapter.GetInstantFundingStatus(ctx, transferID)
}

func (a *AlpacaFundingAdapter) GetAccountBalance(ctx context.Context, accountID string) (*entities.AlpacaAccountResponse, error) {
	return a.adapter.GetAccountBalance(ctx, accountID)
}

func (a *AlpacaFundingAdapter) CreateJournal(ctx context.Context, req *entities.AlpacaJournalRequest) (*entities.AlpacaJournalResponse, error) {
	return a.adapter.CreateJournal(ctx, req)
}

// LedgerIntegrationAdapter adapts integration.LedgerIntegration to funding.LedgerIntegration interface
type LedgerIntegrationAdapter struct {
	integration *integration.LedgerIntegration
}

func (a *LedgerIntegrationAdapter) RecordDeposit(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, depositID uuid.UUID, chain, txHash string) error {
	return a.integration.RecordDeposit(ctx, userID, amount, depositID, chain, txHash)
}

func (a *LedgerIntegrationAdapter) RecordDepositWithAllocation(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, depositID uuid.UUID, chain, txHash string, spendingAmount, stashAmount decimal.Decimal) error {
	return a.integration.RecordDepositWithAllocation(ctx, userID, amount, depositID, chain, txHash, spendingAmount, stashAmount)
}

func (a *LedgerIntegrationAdapter) CompensateDeposit(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, depositID uuid.UUID) error {
	return a.integration.CompensateDeposit(ctx, userID, amount, depositID)
}

func (a *LedgerIntegrationAdapter) GetUserBalance(ctx context.Context, userID uuid.UUID) (*funding.LedgerBalanceView, error) {
	view, err := a.integration.GetUserBalance(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &funding.LedgerBalanceView{
		USDCBalance:       view.USDCBalance,
		FiatExposure:      view.FiatExposure,
		PendingInvestment: view.PendingInvestment,
		TotalValue:        view.TotalValue,
	}, nil
}

// BridgeVirtualAccountWebhookAdapter adapts domain Bridge VA service to webhook processor interface.
type BridgeVirtualAccountWebhookAdapter struct {
	service *funding.BridgeVirtualAccountService
}

func (a *BridgeVirtualAccountWebhookAdapter) ProcessFiatDeposit(ctx *gin.Context, event *webhooks.BridgeDepositEvent) error {
	if a == nil || a.service == nil {
		return fmt.Errorf("bridge virtual account service not configured")
	}
	if event == nil {
		return fmt.Errorf("bridge deposit event is required")
	}

	return a.service.ProcessFiatDeposit(ctx.Request.Context(), &funding.BridgeFiatDepositEvent{
		VirtualAccountID: event.VirtualAccountID,
		CustomerID:       event.CustomerID,
		Amount:           event.Amount,
		Currency:         event.Currency,
		TransactionRef:   event.TransactionRef,
		Status:           event.Status,
	})
}

func (a *BridgeVirtualAccountWebhookAdapter) ProcessCryptoDeposit(ctx context.Context, userID uuid.UUID, transferID string, amount decimal.Decimal, chain string) error {
	if a == nil || a.service == nil {
		return fmt.Errorf("bridge virtual account service not configured")
	}
	return a.service.ProcessCryptoDeposit(ctx, userID, transferID, amount, chain)
}

// BridgeCardWebhookAdapter adapts domain card service to Bridge webhook card processor interface.
type BridgeCardWebhookAdapter struct {
	service *card.Service
}

func (a *BridgeCardWebhookAdapter) Authorize(ctx *gin.Context, cardID string, amount decimal.Decimal, merchantName, merchantCategory string) (bool, string, error) {
	if a == nil || a.service == nil {
		return false, "service_unavailable", fmt.Errorf("card service not configured")
	}
	return a.service.ProcessCardAuthorization(ctx.Request.Context(), cardID, amount, merchantName, merchantCategory)
}

func (a *BridgeCardWebhookAdapter) ProcessAuthorization(ctx *gin.Context, cardID string, amount decimal.Decimal, merchantName, merchantCategory string) error {
	if a == nil || a.service == nil {
		return fmt.Errorf("card service not configured")
	}
	return a.service.ProcessAuthorization(ctx.Request.Context(), cardID, amount, merchantName, merchantCategory)
}

func (a *BridgeCardWebhookAdapter) RecordTransaction(ctx *gin.Context, cardID, transactionID string, amount decimal.Decimal, merchantName, merchantCategory, status string) error {
	if a == nil || a.service == nil {
		return fmt.Errorf("card service not configured")
	}

	txType := "capture"
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "pending", "declined":
		txType = "authorization"
	case "reversed", "refunded":
		txType = "reversal"
	}

	return a.service.RecordTransaction(ctx.Request.Context(), cardID, transactionID, txType, amount, merchantName, merchantCategory, status, nil)
}

func (a *BridgeCardWebhookAdapter) RecordDeclinedTransaction(ctx *gin.Context, cardID, transactionID, declineReason string) error {
	if a == nil || a.service == nil {
		return fmt.Errorf("card service not configured")
	}
	return a.service.RecordDeclinedTransaction(ctx.Request.Context(), cardID, transactionID, declineReason)
}

func (a *BridgeCardWebhookAdapter) SyncCardStatus(ctx *gin.Context, cardID, _ string) error {
	if a == nil || a.service == nil {
		return fmt.Errorf("card service not configured")
	}
	return a.service.SyncCardStatus(ctx.Request.Context(), cardID)
}

// WithdrawalLedgerAdapter adapts ledger.Service to withdrawal.LedgerService interface
type WithdrawalLedgerAdapter struct {
	ledgerService *ledger.Service
}

func NewWithdrawalLedgerAdapter(ledgerService *ledger.Service) *WithdrawalLedgerAdapter {
	return &WithdrawalLedgerAdapter{ledgerService: ledgerService}
}

func (a *WithdrawalLedgerAdapter) GetAccountBalance(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (decimal.Decimal, error) {
	account, err := a.ledgerService.GetOrCreateUserAccount(ctx, userID, accountType)
	if err != nil {
		return decimal.Zero, err
	}
	return account.Balance, nil
}

func (a *WithdrawalLedgerAdapter) CreateTransaction(ctx context.Context, userID uuid.UUID, accountType entities.AccountType, txType entities.TransactionType, amount decimal.Decimal, metadata map[string]interface{}) error {
	userAccount, err := a.ledgerService.GetOrCreateUserAccount(ctx, userID, accountType)
	if err != nil {
		return err
	}

	systemAccount, err := a.ledgerService.GetSystemAccount(ctx, entities.AccountTypeSystemBufferUSDC)
	if err != nil {
		return err
	}
	feeAmount, err := withdrawalPlatformFeeFromMetadata(metadata, amount)
	if err != nil {
		return err
	}
	if feeAmount.IsPositive() {
		if metadata == nil {
			metadata = make(map[string]interface{})
		}
		metadata["fee_revenue_posted"] = true
	}
	principalAmount := amount.Sub(feeAmount)
	if principalAmount.IsNegative() {
		return fmt.Errorf("withdrawal fee %s exceeds total amount %s", feeAmount.String(), amount.String())
	}

	desc := "Withdrawal transaction"
	idempotencyKey := fmt.Sprintf("withdrawal-ledger-%s-%d", userID.String(), time.Now().UnixNano())
	if metadata != nil {
		if withdrawalID, ok := metadata["withdrawal_id"].(string); ok && strings.TrimSpace(withdrawalID) != "" {
			idempotencyKey = "withdrawal-ledger-" + withdrawalID
		}
	}

	entries := []entities.CreateEntryRequest{
		{
			AccountID:   userAccount.ID,
			EntryType:   entities.EntryTypeCredit,
			Amount:      amount,
			Currency:    "USDC",
			Description: &desc,
		},
	}
	if principalAmount.IsPositive() {
		entries = append(entries, entities.CreateEntryRequest{
			AccountID:   systemAccount.ID,
			EntryType:   entities.EntryTypeDebit,
			Amount:      principalAmount,
			Currency:    "USDC",
			Description: &desc,
		})
	}
	if feeAmount.IsPositive() {
		revenueAccount, err := a.ledgerService.GetSystemAccount(ctx, entities.AccountTypeWithdrawalFeeRevenue)
		if err != nil {
			return fmt.Errorf("get withdrawal fee revenue account: %w", err)
		}
		entries = append(entries, entities.CreateEntryRequest{
			AccountID:   revenueAccount.ID,
			EntryType:   entities.EntryTypeDebit,
			Amount:      feeAmount,
			Currency:    "USDC",
			Description: &desc,
		})
	}

	req := &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: txType,
		IdempotencyKey:  idempotencyKey,
		Description:     &desc,
		Metadata:        metadata,
		Entries:         entries,
	}

	_, err = a.ledgerService.CreateTransaction(ctx, req)
	return err
}

func (a *WithdrawalLedgerAdapter) CreatePendingTransaction(ctx context.Context, userID uuid.UUID, accountType entities.AccountType, txType entities.TransactionType, amount decimal.Decimal, idempotencyKey string, metadata map[string]interface{}) error {
	userAccount, err := a.ledgerService.GetOrCreateUserAccount(ctx, userID, accountType)
	if err != nil {
		return err
	}

	systemAccount, err := a.ledgerService.GetSystemAccount(ctx, entities.AccountTypeSystemBufferUSDC)
	if err != nil {
		return err
	}
	feeAmount, err := withdrawalPlatformFeeFromMetadata(metadata, amount)
	if err != nil {
		return err
	}
	if feeAmount.IsPositive() {
		if metadata == nil {
			metadata = make(map[string]interface{})
		}
		metadata["fee_revenue_posted"] = true
	}
	principalAmount := amount.Sub(feeAmount)
	if principalAmount.IsNegative() {
		return fmt.Errorf("withdrawal fee %s exceeds total amount %s", feeAmount.String(), amount.String())
	}

	desc := "Withdrawal transaction (pending)"

	entries := []entities.CreateEntryRequest{
		{
			AccountID:   userAccount.ID,
			EntryType:   entities.EntryTypeCredit,
			Amount:      amount,
			Currency:    "USDC",
			Description: &desc,
		},
	}
	if principalAmount.IsPositive() {
		entries = append(entries, entities.CreateEntryRequest{
			AccountID:   systemAccount.ID,
			EntryType:   entities.EntryTypeDebit,
			Amount:      principalAmount,
			Currency:    "USDC",
			Description: &desc,
		})
	}
	if feeAmount.IsPositive() {
		revenueAccount, err := a.ledgerService.GetSystemAccount(ctx, entities.AccountTypeWithdrawalFeeRevenue)
		if err != nil {
			return fmt.Errorf("get withdrawal fee revenue account: %w", err)
		}
		entries = append(entries, entities.CreateEntryRequest{
			AccountID:   revenueAccount.ID,
			EntryType:   entities.EntryTypeDebit,
			Amount:      feeAmount,
			Currency:    "USDC",
			Description: &desc,
		})
	}

	req := &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: txType,
		IdempotencyKey:  idempotencyKey,
		Description:     &desc,
		Metadata:        metadata,
		Entries:         entries,
	}

	return a.ledgerService.CreatePendingTransaction(ctx, req)
}

func (a *WithdrawalLedgerAdapter) CommitPendingTransaction(ctx context.Context, idempotencyKey string) error {
	return a.ledgerService.CommitPendingTransaction(ctx, idempotencyKey)
}

func (a *WithdrawalLedgerAdapter) FailPendingTransaction(ctx context.Context, idempotencyKey string) error {
	return a.ledgerService.FailPendingTransaction(ctx, idempotencyKey)
}

func (a *WithdrawalLedgerAdapter) GetLedgerTransactionStatus(ctx context.Context, idempotencyKey string) (entities.TransactionStatus, error) {
	return a.ledgerService.GetLedgerTransactionStatus(ctx, idempotencyKey)
}

func (a *WithdrawalLedgerAdapter) ReverseTransaction(ctx context.Context, userID uuid.UUID, accountType entities.AccountType, originalTxID string, amount decimal.Decimal, metadata map[string]interface{}) error {
	userAccount, err := a.ledgerService.GetOrCreateUserAccount(ctx, userID, accountType)
	if err != nil {
		return err
	}

	systemAccount, err := a.ledgerService.GetSystemAccount(ctx, entities.AccountTypeSystemBufferUSDC)
	if err != nil {
		return err
	}
	feeAmount, err := withdrawalPlatformFeeFromMetadata(metadata, amount)
	if err != nil {
		return err
	}
	if feeAmount.IsPositive() {
		posted, err := a.withdrawalFeeRevenueWasPosted(ctx, originalTxID, metadata)
		if err != nil {
			return err
		}
		if !posted {
			feeAmount = decimal.Zero
		}
	}
	principalAmount := amount.Sub(feeAmount)
	if principalAmount.IsNegative() {
		return fmt.Errorf("withdrawal fee %s exceeds reversal amount %s", feeAmount.String(), amount.String())
	}

	desc := "Withdrawal reversal"
	revIdempotencyKey := fmt.Sprintf("withdrawal-reversal-%s-%d", originalTxID, time.Now().UnixNano())
	if metadata != nil {
		if withdrawalID, ok := metadata["withdrawal_id"].(string); ok && strings.TrimSpace(withdrawalID) != "" {
			revIdempotencyKey = "withdrawal-reversal-" + withdrawalID
		} else if strings.TrimSpace(originalTxID) != "" {
			revIdempotencyKey = "withdrawal-reversal-" + strings.TrimSpace(originalTxID)
		}
	} else if strings.TrimSpace(originalTxID) != "" {
		revIdempotencyKey = "withdrawal-reversal-" + strings.TrimSpace(originalTxID)
	}

	revMetadata := map[string]interface{}{
		"reversal_of_tx": originalTxID,
		"reversal_type":  "failed_withdrawal",
	}
	for k, v := range metadata {
		revMetadata[k] = v
	}

	entries := []entities.CreateEntryRequest{
		{
			AccountID:   userAccount.ID,
			EntryType:   entities.EntryTypeDebit,
			Amount:      amount,
			Currency:    "USDC",
			Description: &desc,
		},
	}
	if principalAmount.IsPositive() {
		entries = append(entries, entities.CreateEntryRequest{
			AccountID:   systemAccount.ID,
			EntryType:   entities.EntryTypeCredit,
			Amount:      principalAmount,
			Currency:    "USDC",
			Description: &desc,
		})
	}
	if feeAmount.IsPositive() {
		revenueAccount, err := a.ledgerService.GetSystemAccount(ctx, entities.AccountTypeWithdrawalFeeRevenue)
		if err != nil {
			return fmt.Errorf("get withdrawal fee revenue account: %w", err)
		}
		entries = append(entries, entities.CreateEntryRequest{
			AccountID:   revenueAccount.ID,
			EntryType:   entities.EntryTypeCredit,
			Amount:      feeAmount,
			Currency:    "USDC",
			Description: &desc,
		})
	}

	req := &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeReversal,
		IdempotencyKey:  revIdempotencyKey,
		Description:     &desc,
		Metadata:        revMetadata,
		Entries:         entries,
	}

	_, err = a.ledgerService.CreateTransaction(ctx, req)
	return err
}

func withdrawalPlatformFeeFromMetadata(metadata map[string]interface{}, total decimal.Decimal) (decimal.Decimal, error) {
	if metadata == nil {
		return decimal.Zero, nil
	}
	for _, key := range []string{"fee_amount", "rail_fee"} {
		value, ok := metadata[key]
		if !ok {
			continue
		}
		fee, err := decimalFromMetadataValue(value)
		if err != nil {
			return decimal.Zero, fmt.Errorf("invalid %s metadata: %w", key, err)
		}
		if fee.IsNegative() {
			return decimal.Zero, fmt.Errorf("%s cannot be negative", key)
		}
		if fee.GreaterThan(total) {
			return decimal.Zero, fmt.Errorf("%s %s exceeds total %s", key, fee.String(), total.String())
		}
		return fee, nil
	}
	return decimal.Zero, nil
}

func (a *WithdrawalLedgerAdapter) withdrawalFeeRevenueWasPosted(ctx context.Context, originalTxID string, metadata map[string]interface{}) (bool, error) {
	if metadataBool(metadata, "fee_revenue_posted") {
		return true, nil
	}
	originalKey := ""
	if metadata != nil {
		if withdrawalID, ok := metadata["withdrawal_id"].(string); ok && strings.TrimSpace(withdrawalID) != "" {
			originalKey = "withdrawal-ledger-" + strings.TrimSpace(withdrawalID)
		}
	}
	if originalKey == "" && strings.TrimSpace(originalTxID) != "" {
		originalKey = "withdrawal-ledger-" + strings.TrimSpace(originalTxID)
	}
	if originalKey == "" {
		return false, nil
	}
	tx, err := a.ledgerService.GetTransactionByIdempotencyKey(ctx, originalKey)
	if err != nil {
		return false, fmt.Errorf("lookup fee revenue posting: %w", err)
	}
	if tx == nil {
		return false, nil
	}
	return metadataBool(tx.Metadata, "fee_revenue_posted"), nil
}

func metadataBool(metadata map[string]interface{}, key string) bool {
	if metadata == nil {
		return false
	}
	switch v := metadata[key].(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	default:
		return false
	}
}

func decimalFromMetadataValue(value interface{}) (decimal.Decimal, error) {
	switch v := value.(type) {
	case decimal.Decimal:
		return v, nil
	case string:
		if strings.TrimSpace(v) == "" {
			return decimal.Zero, nil
		}
		return decimal.NewFromString(strings.TrimSpace(v))
	case float64:
		return decimal.NewFromFloat(v), nil
	case float32:
		return decimal.NewFromFloat32(v), nil
	case int:
		return decimal.NewFromInt(int64(v)), nil
	case int64:
		return decimal.NewFromInt(v), nil
	case int32:
		return decimal.NewFromInt(int64(v)), nil
	default:
		return decimal.Zero, fmt.Errorf("unsupported type %T", value)
	}
}

func (a *WithdrawalLedgerAdapter) TransferSpendingToStash(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, idempotencyKey string) error {
	return a.ledgerService.TransferSpendingToStash(ctx, userID, amount, idempotencyKey)
}

// WithdrawalBridgeAdapter adapts bridge.Adapter to withdrawal.BridgeAdapter interface
type WithdrawalBridgeAdapter struct {
	adapter *bridge.Adapter
}

func (a *WithdrawalBridgeAdapter) CreateRecipient(ctx context.Context, req map[string]interface{}) (string, error) {
	customerID, _ := req["customer_id"].(string)
	accountHolderName, _ := req["account_holder_name"].(string)
	currency := strings.ToUpper(strings.TrimSpace(fmt.Sprintf("%v", req["currency"])))

	if strings.TrimSpace(customerID) == "" {
		return "", fmt.Errorf("customer_id is required")
	}

	switch currency {
	case "USD":
		routingNumber, _ := req["routing_number"].(string)
		accountNumber, _ := req["account_number"].(string)
		extAcct, err := a.adapter.Client().CreateExternalAccount(ctx, customerID, &bridge.CreateExternalAccountRequest{
			Currency: bridge.CurrencyUSD,
			BankDetails: bridge.ExternalAccountBankDetails{
				AccountOwnerName: strings.TrimSpace(accountHolderName),
				AccountType:      bridge.ExternalAccountChecking,
				RoutingNumber:    strings.TrimSpace(routingNumber),
				AccountNumber:    strings.TrimSpace(accountNumber),
			},
		})
		if err != nil {
			return "", fmt.Errorf("bridge create external account: %w", err)
		}
		return customerID + ":" + extAcct.ID, nil
	case "EUR":
		iban, _ := req["iban"].(string)
		bic, _ := req["bic"].(string)
		extAcct, err := a.adapter.Client().CreateExternalAccount(ctx, customerID, &bridge.CreateExternalAccountRequest{
			Currency: bridge.CurrencyEUR,
			BankDetails: bridge.ExternalAccountBankDetails{
				AccountOwnerName: strings.TrimSpace(accountHolderName),
				IBAN:             strings.TrimSpace(iban),
				BIC:              strings.TrimSpace(bic),
			},
		})
		if err != nil {
			return "", fmt.Errorf("bridge create external account: %w", err)
		}
		return customerID + ":" + extAcct.ID, nil
	case "GBP":
		sortCode, _ := req["sort_code"].(string)
		accountNumber, _ := req["account_number"].(string)
		extAcct, err := a.adapter.Client().CreateExternalAccount(ctx, customerID, &bridge.CreateExternalAccountRequest{
			Currency: bridge.CurrencyGBP,
			BankDetails: bridge.ExternalAccountBankDetails{
				AccountOwnerName: strings.TrimSpace(accountHolderName),
				SortCode:         strings.TrimSpace(sortCode),
				AccountNumber:    strings.TrimSpace(accountNumber),
			},
		})
		if err != nil {
			return "", fmt.Errorf("bridge create external account: %w", err)
		}
		return customerID + ":" + extAcct.ID, nil
	default:
		return "", fmt.Errorf("unsupported fiat currency: %s", currency)
	}
}

func (a *WithdrawalBridgeAdapter) InitiateTransfer(ctx context.Context, req map[string]interface{}) (map[string]interface{}, error) {
	amountDec, err := decimalFromMetadataValue(req["amount"])
	if err != nil || amountDec.IsZero() {
		return nil, fmt.Errorf("invalid or missing amount in transfer request")
	}
	amount := amountDec.StringFixed(2)

	var developerFee string
	if raw, ok := req["developer_fee"]; ok && raw != nil {
		feeDec, err := decimalFromMetadataValue(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid developer_fee in transfer request: %w", err)
		}
		developerFee = feeDec.StringFixed(2)
	}

	currency, _ := req["currency"].(string)
	recipientID, _ := req["recipient_id"].(string)
	sourceWalletID, _ := req["source_wallet_id"].(string)
	onBehalfOf, _ := req["on_behalf_of"].(string)

	parts := strings.SplitN(recipientID, ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid recipient_id format, expected <customerID>:<externalAccountID>")
	}
	externalAccountID := parts[1]

	bridgeCurrency, err := mapFiatCurrencyToBridgeCurrency(currency)
	if err != nil {
		return nil, err
	}
	destinationRail, err := mapFiatCurrencyToPaymentRail(currency)
	if err != nil {
		return nil, err
	}

	transferReq := &bridge.CreateTransferRequest{
		ClientReferenceID: fmt.Sprintf("withdrawal-%s-%s", onBehalfOf, uuid.New().String()),
		OnBehalfOf:        onBehalfOf,
		Amount:            amount,
		Source: bridge.TransferSource{
			PaymentRail:    bridge.PaymentRail("bridge_wallet"),
			Currency:       bridge.CurrencyUSDC,
			BridgeWalletID: sourceWalletID,
		},
		Destination: bridge.TransferDestination{
			PaymentRail:       destinationRail,
			Currency:          bridgeCurrency,
			ExternalAccountID: externalAccountID,
		},
		DeveloperFee: strings.TrimSpace(developerFee),
	}

	transfer, err := a.adapter.TransferFunds(ctx, transferReq)
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"id":     transfer.ID,
		"status": string(transfer.State),
		"amount": transfer.Amount,
	}, nil
}

func (a *WithdrawalBridgeAdapter) GetTransferStatus(ctx context.Context, transferID string) (map[string]interface{}, error) {
	transfer, err := a.adapter.Client().GetTransfer(ctx, transferID)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"id":     transfer.ID,
		"status": string(transfer.State),
	}, nil
}

func (a *WithdrawalBridgeAdapter) CancelTransfer(ctx context.Context, transferID string) error {
	transfer, err := a.adapter.Client().GetTransfer(ctx, transferID)
	if err != nil {
		return fmt.Errorf("failed to get transfer status before cancellation: %w", err)
	}
	switch transfer.State {
	case bridge.TransferStatusPaymentProcessed, bridge.TransferStatusCanceled, bridge.TransferStatusReturned:
		return fmt.Errorf("transfer %s is in terminal state %s and cannot be cancelled", transferID, transfer.State)
	}
	return fmt.Errorf("bridge transfer cancellation not supported by API; transfer %s must expire or fail naturally", transferID)
}

func mapFiatCurrencyToPaymentRail(currency string) (bridge.PaymentRail, error) {
	switch strings.ToUpper(strings.TrimSpace(currency)) {
	case "USD":
		return bridge.PaymentRail("us_ach"), nil
	case "EUR":
		return bridge.PaymentRail("sepa"), nil
	case "GBP":
		return bridge.PaymentRail("faster_payments"), nil
	default:
		return "", fmt.Errorf("unsupported fiat payment rail for currency %s", currency)
	}
}

func mapFiatCurrencyToBridgeCurrency(currency string) (bridge.Currency, error) {
	switch strings.ToUpper(strings.TrimSpace(currency)) {
	case "USD":
		return bridge.CurrencyUSD, nil
	case "EUR":
		return bridge.CurrencyEUR, nil
	case "GBP":
		return bridge.CurrencyGBP, nil
	default:
		return "", fmt.Errorf("unsupported fiat currency %s", currency)
	}
}

func mapChainToPaymentRail(chain string) bridge.PaymentRail {
	switch chain {
	case "ETH", "ethereum":
		return bridge.PaymentRailEthereum
	case "MATIC", "polygon":
		return bridge.PaymentRailPolygon
	case "SOL", "solana":
		return bridge.PaymentRailSolana
	case "BASE", "base":
		return bridge.PaymentRailBase
	default:
		return bridge.PaymentRailSolana
	}
}

// stateCodeToName converts 2-letter US state codes to full names
// e.g., "NY" -> "New York", "CA" -> "California"
func stateCodeToName(code string) string {
	if code == "" {
		return code
	}
	// If looks like a full name already (contains space or lowercase), return as-is
	if strings.Contains(code, " ") || strings.Contains(code, "-") {
		return toTitleCaseStr(code)
	}
	// If already 2 uppercase letters, convert to name
	if len(code) == 2 && code == strings.ToUpper(code) {
		stateMap := map[string]string{
			"AL": "Alabama", "AK": "Alaska", "AZ": "Arizona", "AR": "Arkansas",
			"CA": "California", "CO": "Colorado", "CT": "Connecticut", "DE": "Delaware",
			"FL": "Florida", "GA": "Georgia", "HI": "Hawaii", "ID": "Idaho",
			"IL": "Illinois", "IN": "Indiana", "IA": "Iowa", "KS": "Kansas",
			"KY": "Kentucky", "LA": "Louisiana", "ME": "Maine", "MD": "Maryland",
			"MA": "Massachusetts", "MI": "Michigan", "MN": "Minnesota", "MS": "Mississippi",
			"MO": "Missouri", "MT": "Montana", "NE": "Nebraska", "NV": "Nevada",
			"NH": "New Hampshire", "NJ": "New Jersey", "NM": "New Mexico", "NY": "New York",
			"NC": "North Carolina", "ND": "North Dakota", "OH": "Ohio", "OK": "Oklahoma",
			"OR": "Oregon", "PA": "Pennsylvania", "RI": "Rhode Island", "SC": "South Carolina",
			"SD": "South Dakota", "TN": "Tennessee", "TX": "Texas", "UT": "Utah",
			"VT": "Vermont", "VA": "Virginia", "WA": "Washington", "WV": "West Virginia",
			"WI": "Wisconsin", "WY": "Wyoming", "DC": "District of Columbia",
		}
		if name, ok := stateMap[strings.ToUpper(code)]; ok {
			return name
		}
	}
	// Return original if not found
	return code
}

// normalizeUSStateToCode converts a US state name or code to its 2-letter ISO code.
// e.g., "Kansas" -> "KS", "KS" -> "KS", "New York" -> "NY"
func normalizeUSStateToCode(state string) string {
	if state == "" {
		return state
	}
	upper := strings.ToUpper(strings.TrimSpace(state))
	// Already a 2-letter code
	if len(upper) == 2 {
		return upper
	}
	nameToCode := map[string]string{
		"ALABAMA": "AL", "ALASKA": "AK", "ARIZONA": "AZ", "ARKANSAS": "AR",
		"CALIFORNIA": "CA", "COLORADO": "CO", "CONNECTICUT": "CT", "DELAWARE": "DE",
		"FLORIDA": "FL", "GEORGIA": "GA", "HAWAII": "HI", "IDAHO": "ID",
		"ILLINOIS": "IL", "INDIANA": "IN", "IOWA": "IA", "KANSAS": "KS",
		"KENTUCKY": "KY", "LOUISIANA": "LA", "MAINE": "ME", "MARYLAND": "MD",
		"MASSACHUSETTS": "MA", "MICHIGAN": "MI", "MINNESOTA": "MN", "MISSISSIPPI": "MS",
		"MISSOURI": "MO", "MONTANA": "MT", "NEBRASKA": "NE", "NEVADA": "NV",
		"NEW HAMPSHIRE": "NH", "NEW JERSEY": "NJ", "NEW MEXICO": "NM", "NEW YORK": "NY",
		"NORTH CAROLINA": "NC", "NORTH DAKOTA": "ND", "OHIO": "OH", "OKLAHOMA": "OK",
		"OREGON": "OR", "PENNSYLVANIA": "PA", "RHODE ISLAND": "RI", "SOUTH CAROLINA": "SC",
		"SOUTH DAKOTA": "SD", "TENNESSEE": "TN", "TEXAS": "TX", "UTAH": "UT",
		"VERMONT": "VT", "VIRGINIA": "VA", "WASHINGTON": "WA", "WEST VIRGINIA": "WV",
		"WISCONSIN": "WI", "WYOMING": "WY", "DISTRICT OF COLUMBIA": "DC",
	}
	if code, ok := nameToCode[upper]; ok {
		return code
	}
	return upper
}

// toTitleCaseStr converts a string to title case
func toTitleCaseStr(s string) string {
	words := strings.Fields(strings.ToLower(s))
	for i, word := range words {
		if len(word) > 0 {
			words[i] = strings.ToUpper(word[:1]) + word[1:]
		}
	}
	return strings.Join(words, " ")
}

// BridgeOnboardingAdapter adapts bridge.Adapter to onboarding.BridgeAdapter interface
type BridgeOnboardingAdapter struct {
	adapter *bridge.Adapter
}

func (a *BridgeOnboardingAdapter) CreateCustomer(ctx context.Context, req *entities.CreateAccountRequest) (*entities.CreateAccountResponse, error) {
	bridgeReq := &bridge.CreateCustomerRequest{
		Type:      bridge.CustomerTypeIndividual,
		FirstName: req.FirstName,
		LastName:  req.LastName,
		Email:     req.Email,
	}

	// Store original 2-letter country code for subdivision prefix
	country2 := strings.ToUpper(req.Country)

	// Normalize 2-letter country code to 3-letter (Bridge requires ISO 3166-1 alpha-3)
	country3 := country2
	switch country2 {
	case "US":
		country3 = "USA"
	case "GB":
		country3 = "GBR"
	case "NG":
		country3 = "NGA"
	case "CA":
		country3 = "CAN"
	case "AU":
		country3 = "AUS"
	case "DE":
		country3 = "DEU"
	case "MX":
		country3 = "MEX"
	case "BR":
		country3 = "BRA"
	case "IN":
		country3 = "IND"
	case "ZA":
		country3 = "ZAF"
	case "KE":
		country3 = "KEN"
	}

	// Add residential address if provided
	if req.Address != nil {
		// Bridge API expects ISO 3166-2 subdivision code without country prefix (e.g., "KS" not "Kansas")
		subdivision := normalizeUSStateToCode(req.Address.State)

		bridgeReq.ResidentialAddress = &bridge.Address{
			StreetLine1: req.Address.Street,
			City:        req.Address.City,
			Subdivision: subdivision,
			PostalCode:  req.Address.PostalCode,
			Country:     country3,
		}
	}

	// Add birth date if provided
	if req.DateOfBirth != nil {
		bridgeReq.BirthDate = req.DateOfBirth.Format("2006-01-02")
	}

	// Add tax ID if provided (required for production, optional for sandbox)
	if req.SSN != "" {
		taxIDType := kyc.GetSupportedTaxIDType(country3)
		if taxIDType == "" {
			taxIDType = "ssn"
		}
		bridgeReq.IdentifyingInformation = []bridge.IdentifyingInfo{
			{
				Type:           taxIDType,
				IssuingCountry: strings.ToLower(country3),
				Number:         req.SSN,
			},
		}
	}

	// For sandbox: use dummy signed_agreement_id if not provided
	// For production: this must be obtained from Bridge ToS API first
	// Note: Sandbox may auto-approve without signed_agreement_id
	if req.SignedAgreementID != "" {
		bridgeReq.SignedAgreementID = req.SignedAgreementID
	}

	// Use direct customer creation (not CreateCustomerWithWallet) to ensure all fields are sent
	cust, err := a.adapter.Client().CreateCustomer(ctx, bridgeReq)
	if err != nil {
		return nil, err
	}

	return &entities.CreateAccountResponse{
		AccountID: cust.ID,
		Status:    string(cust.Status),
	}, nil
}

func (a *BridgeOnboardingAdapter) GetCustomerByEmail(ctx context.Context, email string) (*entities.CreateAccountResponse, error) {
	cust, err := a.adapter.Client().GetCustomerByEmail(ctx, email)
	if err != nil {
		return nil, err
	}
	if cust == nil {
		return nil, nil
	}
	return &entities.CreateAccountResponse{
		AccountID: cust.ID,
		Status:    string(cust.Status),
	}, nil
}

func (a *BridgeOnboardingAdapter) DeleteCustomer(ctx context.Context, customerID string) error {
	return a.adapter.Client().DeleteCustomer(ctx, customerID)
}

func (a *BridgeOnboardingAdapter) UpdateCustomer(ctx context.Context, customerID string, req *bridge.UpdateCustomerRequest) (*bridge.Customer, error) {
	return a.adapter.UpdateCustomer(ctx, customerID, req)
}

// FundingNotificationAdapter adapts NotificationService to funding.FundingNotificationService
type FundingNotificationAdapter struct {
	svc *services.NotificationService
}

// automationNotificationAdapter adapts NotificationService to automation.NotificationSender.
type automationNotificationAdapter struct {
	svc    *services.NotificationService
	logger *zap.Logger
}

func (a *automationNotificationAdapter) SendPush(ctx context.Context, userID uuid.UUID, title, message string, data map[string]interface{}) error {
	// Send push notification
	notif := &entities.Notification{
		ID:       uuid.New(),
		UserID:   userID,
		Type:     "automation",
		Channel:  entities.ChannelPush,
		Priority: entities.PriorityMedium,
		Title:    title,
		Body:     message,
		Data:     data,
	}
	if err := a.svc.Send(ctx, notif, nil); err != nil {
		if a.logger != nil {
			a.logger.Error("Failed to send automation push notification",
				zap.Error(err),
				zap.String("user_id", userID.String()),
				zap.String("notification_id", notif.ID.String()))
		}
	}

	// Also persist in-app so the deep-link data is available from notification center
	inApp := *notif
	inApp.ID = uuid.New()
	inApp.Channel = entities.ChannelInApp
	return a.svc.Send(ctx, &inApp, nil)
}

func (a *FundingNotificationAdapter) NotifyDepositConfirmed(ctx context.Context, userID uuid.UUID, amount, chain, txHash string) error {
	return a.svc.NotifyDepositConfirmed(ctx, userID, amount, chain, txHash)
}

func (a *FundingNotificationAdapter) NotifyLargeBalanceChange(ctx context.Context, userID uuid.UUID, changeType string, amount decimal.Decimal, newBalance decimal.Decimal) error {
	return a.svc.NotifyLargeBalanceChange(ctx, userID, changeType, amount, newBalance)
}

func (a *FundingNotificationAdapter) NotifyAllocationFailed(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, depositID uuid.UUID, reason string) error {
	return a.svc.NotifyAllocationFailed(ctx, userID, amount, depositID, reason)
}

// WithdrawalNotificationAdapter adapts NotificationService to withdrawal.WithdrawalNotificationService
type WithdrawalNotificationAdapter struct {
	svc *services.NotificationService
}

func (a *WithdrawalNotificationAdapter) NotifyWithdrawalCompleted(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, destination string) error {
	return a.svc.NotifyWithdrawalCompleted(ctx, userID, amount.String(), destination)
}

func (a *WithdrawalNotificationAdapter) NotifyWithdrawalFailed(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, reason string) error {
	return a.svc.NotifyWithdrawalFailed(ctx, userID, amount.String(), reason)
}

func (a *WithdrawalNotificationAdapter) NotifyLargeBalanceChange(ctx context.Context, userID uuid.UUID, changeType string, amount decimal.Decimal, newBalance decimal.Decimal) error {
	return a.svc.NotifyLargeBalanceChange(ctx, userID, changeType, amount, newBalance)
}

func (a *WithdrawalNotificationAdapter) NotifyWithdrawalSubmitted(ctx context.Context, userID uuid.UUID, amount string) error {
	return a.svc.NotifyWithdrawalSubmitted(ctx, userID, amount)
}

func (a *WithdrawalNotificationAdapter) NotifyEmergencyWithdrawal(ctx context.Context, userID uuid.UUID, amount, fee decimal.Decimal) error {
	msg := fmt.Sprintf("Emergency withdrawal of $%s completed (fee: $%s)", amount.StringFixed(2), fee.StringFixed(2))
	return a.svc.NotifyWithdrawalCompleted(ctx, userID, amount.String(), msg)
}

// deletionLedgerAdapter adapts LedgerService to account.LedgerService
type deletionLedgerAdapter struct {
	ledgerService *ledger.Service
}

func (a *deletionLedgerAdapter) GetUserBalances(ctx context.Context, userID uuid.UUID) (*entities.UserBalances, error) {
	return a.ledgerService.GetUserBalances(ctx, userID)
}

// deletionUserRepoAdapter adapts UserRepository to account.UserRepository
type deletionUserRepoAdapter struct {
	userRepo *repositories.UserRepository
}

func (a *deletionUserRepoAdapter) HardDelete(ctx context.Context, userID uuid.UUID) error {
	return a.userRepo.HardDelete(ctx, userID)
}

func (a *deletionUserRepoAdapter) AnonymizeUser(ctx context.Context, userID uuid.UUID) error {
	return a.userRepo.AnonymizeUser(ctx, userID)
}

// deletionBridgeAdapter adapts bridge.Client to account.BridgeClient
type deletionBridgeAdapter struct {
	client *bridge.Client
}

func (a *deletionBridgeAdapter) DeactivateVirtualAccount(ctx context.Context, customerID, virtualAccountID string) error {
	_, err := a.client.DeactivateVirtualAccount(ctx, customerID, virtualAccountID)
	return err
}

func (a *deletionBridgeAdapter) DeleteCustomer(ctx context.Context, customerID string) error {
	return a.client.DeleteCustomer(ctx, customerID)
}

// tieredLimitsAdapter adapts security.WithdrawalLimitsService to withdrawal.TieredWithdrawalLimitChecker
type tieredLimitsAdapter struct {
	svc *security.WithdrawalLimitsService
}

func (a *tieredLimitsAdapter) CheckWithdrawalLimit(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, accountAge time.Duration, kycLevel string) error {
	_, err := a.svc.CheckWithdrawalLimit(ctx, userID, amount, accountAge, kycLevel)
	return err
}

func (a *tieredLimitsAdapter) RecordWithdrawal(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error {
	return a.svc.RecordWithdrawal(ctx, userID, amount)
}

// Embedder generates embedding vectors for text. Satisfied by both the
// legacy embeddings.Client and the new CencoriEmbeddingsClient.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
}

// wireChatOnboarding enables chat-first account creation for unlinked
// messaging senders (name → country → email+OTP → phone+OTP → consent →
// Tier-1 user + wallet + auto-link). MUST be called after every dependency
// exists: OnboardingService + BabyStepsSeeder (set in initializeDomainServices),
// VerificationService (container.go), and platformProcessor/platformLinking
// (initializePlatformMessaging). It previously ran inside
// initializeDomainServices, where VerificationService is still nil, so the
// gate silently failed and unlinked senders always got the legacy
// "Please link your account first" fallback instead of onboarding.
func (c *Container) wireChatOnboarding() {
	if !c.Config.Platform.OnboardingEnabled {
		return
	}
	// Missing deps are logged individually: the original bug was a silent
	// nil VerificationService here, and a combined guard would hide any
	// future init-order regression the same way.
	var missing []string
	if c.platformProcessor == nil {
		missing = append(missing, "platformProcessor (initializePlatformMessaging)")
	}
	if c.platformLinking == nil {
		missing = append(missing, "platformLinking (initializePlatformMessaging)")
	}
	if c.RedisClient == nil {
		missing = append(missing, "RedisClient")
	}
	if c.VerificationService == nil {
		missing = append(missing, "VerificationService")
	}
	if c.UserRepo == nil {
		missing = append(missing, "UserRepo")
	}
	if len(missing) > 0 {
		c.ZapLog.Warn("chat onboarding disabled: missing dependencies — unlinked senders will get the legacy link-first fallback",
			zap.Strings("missing", missing))
		return
	}
	onboarder := platform.NewChatOnboarder(
		c.RedisClient,
		c.VerificationService,
		c.UserRepo,
		c.OnboardingService,
		c.platformLinking,
		c.Config.Platform.AppDownloadURL,
		c.ZapLog,
	)
	c.platformProcessor.SetOnboarder(onboarder)
	// Fire the first-login Baby Steps seeder from both paths: iMessage
	// handshake (processor.tryCompleteHandshake) and chat-first onboarding
	// completion (onboarder.handleConsent). The seeder is idempotent —
	// HasAnyGoal gates the inserts — so racing both paths is safe.
	if c.BabyStepsSeeder != nil {
		c.platformProcessor.SetBabyStepsSeeder(c.BabyStepsSeeder)
		onboarder.SetBabyStepsSeeder(c.BabyStepsSeeder)
	}
	c.ZapLog.Info("Chat-first onboarding enabled for platform messaging")
}

func (c *Container) initializeDomainServices() error {
	// Use Circle supported chains when Circle is the wallet provider, fall back to Bridge chains.
	walletChainSource := c.Config.Bridge.SupportedChains
	if c.CircleAdapter != nil && len(c.Config.Circle.SupportedChains) > 0 {
		walletChainSource = c.Config.Circle.SupportedChains
	}
	defaultWalletChains := convertWalletChains(walletChainSource, c.ZapLog)
	walletServiceConfig := wallet.Config{
		WalletSetNamePrefix: c.Config.Circle.DefaultWalletSetName,
		SupportedChains:     defaultWalletChains,
		DefaultWalletSetID:  c.Config.Circle.DefaultWalletSetID,
	}

	// Initialize wallet service first (no dependencies on other domain services)
	c.WalletService = wallet.NewService(
		c.WalletRepo,
		c.WalletProvisioningJobRepo,
		c.AuditService,
		c.OnboardingService,
		&BridgeWalletProvisioningAdapter{client: c.BridgeClient},
		c.CircleAdapter,
		&UserProfileProviderAdapter{repo: c.UserRepo},
		c.ZapLog,
		walletServiceConfig,
	)

	// Initialize Alpaca adapter
	alpacaAdapter := alpaca.NewAdapter(c.AlpacaClient, c.Logger)

	// Initialize Bridge onboarding adapter
	bridgeOnboardingAdapter := &BridgeOnboardingAdapter{adapter: c.BridgeAdapter}

	// Initialize onboarding service (depends on wallet service)
	// Note: AllocationService will be injected after it's initialized
	c.OnboardingService = onboarding.NewService(
		c.UserRepo,
		c.OnboardingFlowRepo,
		c.KYCSubmissionRepo,
		c.WalletService, // Domain service dependency
		c.EmailService,
		c.AuditService,
		bridgeOnboardingAdapter,
		alpacaAdapter,
		nil, // AllocationService - will be set after initialization
		c.ZapLog,
		append([]entities.WalletChain(nil), walletServiceConfig.SupportedChains...),
	)

	// Inject onboarding service back into wallet service to complete circular dependency
	c.WalletService.SetOnboardingService(c.OnboardingService)

	// Initialize passcode service for transaction security
	c.PasscodeService = passcode.NewService(
		c.UserRepo,
		c.RedisClient,
		c.ZapLog,
	)

	// Initialize security services
	c.SessionService = session.NewService(c.DB, c.RedisClient.Client(), c.ZapLog)
	c.TwoFAService = twofa.NewService(c.DB, c.ZapLog, c.Config.Security.EncryptionKey, c.RedisClient)
	c.APIKeyService = apikey.NewService(c.DB, c.ZapLog)

	// Initialize social auth service
	socialAuthConfig := socialauth.Config{
		Google: socialauth.OAuthConfig{
			ClientID:     c.Config.SocialAuth.Google.ClientID,
			ClientSecret: c.Config.SocialAuth.Google.ClientSecret,
			RedirectURI:  c.Config.SocialAuth.Google.RedirectURI,
		},
		Apple: socialauth.AppleOAuthConfig{
			ClientID:    c.Config.SocialAuth.Apple.ClientID,
			TeamID:      c.Config.SocialAuth.Apple.TeamID,
			KeyID:       c.Config.SocialAuth.Apple.KeyID,
			PrivateKey:  c.Config.SocialAuth.Apple.PrivateKey,
			RedirectURI: c.Config.SocialAuth.Apple.RedirectURI,
		},
	}
	c.SocialAuthService = socialauth.NewService(c.DB, c.ZapLog, socialAuthConfig)

	// Initialize WebAuthn service
	if c.Config.WebAuthn.RPID != "" {
		webauthnConfig := webauthn.Config{
			RPDisplayName: c.Config.WebAuthn.RPDisplayName,
			RPID:          c.Config.WebAuthn.RPID,
			RPOrigins:     c.Config.WebAuthn.RPOrigins,
		}
		webauthnSvc, err := webauthn.NewService(c.DB, c.ZapLog, webauthnConfig)
		if err != nil {
			c.Logger.Warn("Failed to initialize WebAuthn service", zap.Error(err))
		} else {
			c.WebAuthnService = webauthnSvc
		}
	}

	// Initialize simple wallet repository for funding service
	simpleWalletRepo := repositories.NewSimpleWalletRepository(c.DB, c.Logger)

	// Initialize virtual account repository
	sqlxDB := sqlx.NewDb(c.DB, "postgres")
	virtualAccountRepo := repositories.NewVirtualAccountRepository(sqlxDB)

	// Initialize Alpaca funding adapter
	alpacaFundingAdapter := alpaca.NewFundingAdapter(c.AlpacaClient, c.ZapLog)

	// Initialize ledger service with outbox (writes events atomically in the
	// same DB transaction for reliable downstream consumption).
	ledgerOutbox := ledger.NewOutboxWriter(c.LedgerRepo)
	c.LedgerService = ledger.NewService(c.LedgerRepo, sqlxDB, c.Logger, ledgerOutbox)
	c.FinancialObligationService = obligationservice.NewService(c.FinancialObligationRepo)
	automationAdapter := &fundsTransfererAdapter{ledger: c.LedgerService, logger: c.ZapLog}
	c.AutomationService = automation.NewService(c.AutomationRepo, automationAdapter, automationAdapter, c.ZapLog)
	c.SharedGoalService = sharedgoal.NewService(c.SharedGoalRepo, &sharedGoalUserLookupAdapter{repo: c.UserRepo}, c.ZapLog)

	// Wire optional automation dependencies
	if c.NotificationService != nil {
		c.AutomationService.SetNotificationSender(&automationNotificationAdapter{svc: c.NotificationService, logger: c.ZapLog})
	}
	if c.FinancialObligationService != nil {
		c.AutomationService.SetObligationProvider(c.FinancialObligationService)
	}
	// Wire goal sub-account support
	c.AutomationService.SetGoalChecker(&goalCheckerAdapter{goalRepo: c.SharedGoalRepo, ledger: c.LedgerService})
	c.AutomationService.SetGoalTransferExecutor(&goalTransferAdapter{ledger: c.LedgerService})
	c.AutomationService.SetGoalContributor(&goalContributorAdapter{goalRepo: c.SharedGoalRepo})
	c.AutomationService.SetTotalSavingsProvider(c.LedgerService)
	if c.YieldService != nil {
		c.AutomationService.SetYieldSnapshotRecorder(c.YieldService)
	}

	// Wire automation trigger evaluation dependencies (income_detected,
	// spending_spike, payday, life_event). The AnomalyEngine is also stored
	// on the container for use by application.go.
	if c.LedgerSpendingRepo != nil {
		c.AnomalyEngine = aiservice.NewAnomalyEngine(c.LedgerSpendingRepo, c.LedgerSpendingRepo, c.LedgerSpendingRepo, c.LedgerSpendingRepo, c.ZapLog)
	}
	wireAutomationTriggers(c.AutomationService, c.LedgerSpendingRepo, c.ContextSignalRepo, c.AnomalyEngine)

	// Goals service (Postgres-backed multi-goal). Wired early so subsequent
	// services (automation, AI, workers) can hold a reference.
	c.UserGoalRepo = repositories.NewUserGoalRepository(sqlxDB)
	c.GoalsService = goals.NewService(c.UserGoalRepo, c.ZapLog)
	c.ConsciousSpendingPlanRepo = repositories.NewConsciousSpendingPlanRepository(sqlxDB)
	c.ConsciousSpendingPlanService = consciousspending.NewService(c.ConsciousSpendingPlanRepo)
	c.BabyStepsSeeder = goals.NewBabyStepsSeed(c.GoalsService, c.UserGoalRepo, c.FinancialProfileRepo, c.FinancialObligationService, c.ZapLog)
	c.GoalProgressHooks = NewGoalProgressHooks(c.GoalsService, c.ZapLog)
	if c.AutomationService != nil && c.GoalProgressHooks != nil {
		c.AutomationService.SetDepositHook(c.GoalProgressHooks)
	}
	// ProactiveCoordinator — single per-user daily cap across all sources.
	// Default cap is 5/day; tunable via MIRIAM_DAILY_PROACTIVE_CAP.
	c.ProactiveCoordinator = platform.NewProactiveCoordinator(
		c.RedisClient, c.ZapLog, 5, map[string]int{
			platform.ProactiveCategorySpendingCoach: 1, // weekly only
			platform.ProactiveCategoryGoalProgress:  2, // milestones + pace
		},
	)

	moneyGuardSpendingSvc := spendingsvc.NewService(c.LedgerSpendingRepo)
	c.MoneyGuardService = moneyguardservice.NewService(
		repositories.NewMoneyGuardRepository(sqlxDB),
		c.LedgerService,
		c.LedgerService,
		moneyGuardSpendingSvc,
		c.BudgetRepo,
		c.FinancialObligationService,
		c.FinancialProfileRepo,
		c.NotificationService,
		c.AutomationService,
		c.ZapLog,
	)
	c.LedgerService.SetStashRaidObserver(c.MoneyGuardService)
	c.MiriamIntelligenceRepo = repositories.NewMiriamIntelligenceRepository(sqlxDB)
	// All of Miriam's proactive output goes to iMessage only — no push. When the
	// bridge dispatcher is wired it is the notifier; otherwise fall back to the
	// in-app notification service (dev/no-bridge environments).
	var miriamNotifier miriamservice.Notifier = c.NotificationService
	if c.MiriamBridgeDispatcher != nil {
		miriamNotifier = c.MiriamBridgeDispatcher
	}
	c.MiriamIntelligenceService = miriamservice.NewService(
		c.MiriamIntelligenceRepo,
		c.LedgerService, // BalanceProvider
		moneyGuardSpendingSvc,
		c.FinancialObligationService,
		c.FinancialProfileRepo,
		c.MoneyGuardService,
		c.LedgerService, // TransferExecutor — same service, different interface
		miriamNotifier,
		c.ZapLog,
	)

	// Wire Miriam intelligence subsystem (unified brain).
	contextSignalRepo := repositories.NewContextSignalRepository(sqlxDB)
	decisionRepo := repositories.NewMiriamDecisionRepository(sqlxDB)
	predictionRepo := repositories.NewMiriamPredictionRepository(sqlxDB)
	nudgeRepo := repositories.NewProactiveNudgeRepository(sqlxDB)
	healthRepo := repositories.NewHealthScoreRepository(sqlxDB)
	suggestionRepo := repositories.NewMandateSuggestionRepository(sqlxDB)
	transactionRepo := repositories.NewTransactionRepository(sqlxDB)
	transactionProvider := repositories.NewTransactionProviderAdapter(transactionRepo)
	notifPrefRepo := repositories.NewNotificationPreferenceRepository(sqlxDB)
	notifDigestRepo := repositories.NewNotificationDigestRepository(sqlxDB)

	c.MiriamSignalDetector = miriamservice.NewSignalDetector(
		contextSignalRepo,
		moneyGuardSpendingSvc,
		c.FinancialObligationService,
		c.LedgerService,
		c.ZapLog,
	)
	c.MiriamPredictiveEngine = miriamservice.NewPredictiveEngine(
		predictionRepo,
		moneyGuardSpendingSvc,
		c.FinancialObligationService,
		c.LedgerService,
		c.FinancialProfileRepo,
		c.ZapLog,
	)
	c.MiriamDecisionEngine = miriamservice.NewDecisionEngine(
		decisionRepo,
		c.MiriamPredictiveEngine,
		nil, // MemoryReader — deferred via SetMemory after memory service init
		c.ZapLog,
	)
	c.MiriamProactiveNudgeEngine = miriamservice.NewProactiveNudgeEngine(
		nudgeRepo,
		c.MiriamPredictiveEngine,
		c.LedgerService, // BalanceProvider
		nil,             // MemoryReader — deferred via SetMemory after memory service init
		miriamNotifier,
		c.ZapLog,
	)
	if c.MiriamProactiveChatSender != nil {
		c.MiriamProactiveNudgeEngine.SetChatSender(c.MiriamProactiveChatSender)
	}
	c.MiriamMandateSuggestionEngine = miriamservice.NewMandateSuggestionEngine(
		suggestionRepo,
		c.MiriamIntelligenceService, // MandateProvider
		c.LedgerService,
		moneyGuardSpendingSvc,
		c.FinancialObligationService,
		c.FinancialProfileRepo,
		c.ZapLog,
	)
	if c.MiriamDecisionEngine != nil {
		c.MiriamMandateSuggestionEngine.SetLearningBiasProvider(c.MiriamDecisionEngine)
	}

	c.MiriamObligationDetector = miriamservice.NewObligationAutoDetector(
		transactionProvider,
		c.FinancialObligationService,
		c.LedgerService,
		c.ZapLog,
	)
	c.MiriamNotificationDispatcher = miriamservice.NewNotificationDispatcher(
		notifPrefRepo,
		notifDigestRepo,
		c.NotificationService,
		c.ZapLog,
	)
	c.MiriamHealthScoreTracker = miriamservice.NewHealthScoreTracker(
		healthRepo,
		c.ZapLog,
	)
	c.MiriamOutcomeTracker = miriamservice.NewOutcomeTracker(
		c.MiriamIntelligenceRepo,
		moneyGuardSpendingSvc,
		c.LedgerService,
		c.FinancialObligationService,
		c.ZapLog,
	)
	c.MiriamIntelligenceOrchestrator = miriamservice.NewIntelligenceOrchestrator(
		c.MiriamIntelligenceService,
		c.MiriamDecisionEngine,
		c.MiriamProactiveNudgeEngine,
		c.MiriamPredictiveEngine,
		c.MiriamSignalDetector,
		c.MiriamMandateSuggestionEngine,
		c.MiriamObligationDetector,
		c.MiriamNotificationDispatcher,
		nil, // MemoryReader — deferred via SetMemory after memory service init
		miriamNotifier,
		c.MiriamHealthScoreTracker,
		c.MiriamOutcomeTracker,
		c.ZapLog,
	)

	// Self-review: Miriam grades her own recent actions and messaging, feeds the
	// verdict back into the learning-bias (money) and nudge-cadence (messaging)
	// levers, and records an audit trail. Money influence flows only through the
	// existing learning-signal → decision-engine path.
	c.MiriamSelfReviewEngine = miriamservice.NewSelfReviewEngine(
		c.MiriamIntelligenceRepo,
		nudgeRepo,
		c.MiriamHealthScoreTracker,
		miriamNotifier,
		c.ZapLog,
	)
	c.MiriamIntelligenceOrchestrator.SetSelfReview(c.MiriamSelfReviewEngine)
	c.MiriamProactiveNudgeEngine.SetCadenceReader(c.MiriamIntelligenceRepo)
	// yield provider; per-user yield accrues in each user's Safe and is surfaced via
	// the Blend overview endpoint rather than a shared exchange-rate distribution.
	c.YieldService = yieldsvc.NewService(c.yieldRepo, c.LedgerService, c.ZapLog)

	// Stash reconciliation: daily check that ledger stash principal matches the Blend
	// principal held for users (settled positions + in-flight routes).
	c.StashReconciliation = recon.NewWorker(
		c.LedgerRepo,
		&blendReconciliationAdapter{db: sqlxDB},
		c.yieldRepo,
		"blend",
		"blend",
		c.ZapLog,
	)

	// Revenue sweep: periodically transfer accumulated fee revenue to treasury wallet.
	// Independent of Reflect — only requires Circle + treasury address.
	if c.Config.Circle.TreasuryWalletAddress != "" && c.CircleAdapter != nil {
		revSweepAdapter := &revenueSweepTransferAdapter{
			circle:          c.CircleAdapter,
			treasuryAddress: c.Config.Circle.TreasuryWalletAddress,
		}
		revSweep := revenue_sweep.NewWorker(
			revSweepAdapter,
			sqlxDB,
			decimal.NewFromFloat(0.10), // min $0.10 to sweep (flat fees are $0.10)
			24*time.Hour,
			c.ZapLog,
		)
		revSweep.Start()
		c.RevenueSweepWorker = revSweep
		c.ZapLog.Info("Revenue sweep worker started",
			zap.String("treasury_address", c.Config.Circle.TreasuryWalletAddress))
	} else {
		// #region agent log
		writeFeeDebugLog("container.go:initializeServices", "revenue sweep worker disabled", "H1", map[string]interface{}{
			"treasury_configured": c.Config.Circle.TreasuryWalletAddress != "",
			"circle_configured":   c.CircleAdapter != nil,
		})
		// #endregion
		c.ZapLog.Warn("Revenue sweep worker disabled: missing treasury_wallet_address or circle config")
	}

	// Initialize ledger integration (bridges legacy and new ledger system)
	ledgerIntegration := integration.NewLedgerIntegration(
		c.LedgerService,
		c.BalanceRepo,
		c.Logger,
		false, // shadowMode disabled - fully migrated to ledger
		false, // strictMode
	)

	// Initialize standalone Balance service with Alpaca adapter
	alpacaBalanceAdapter := &AlpacaFundingAdapter{adapter: alpacaFundingAdapter, client: c.AlpacaClient}
	c.BalanceService = services.NewBalanceService(c.BalanceRepo, alpacaBalanceAdapter, c.Logger)

	// Initialize funding service with ledger integration (Bridge replaces Circle)
	ledgerAdapter := &LedgerIntegrationAdapter{integration: ledgerIntegration}
	c.FundingService = funding.NewService(
		c.DepositRepo,
		simpleWalletRepo,
		c.WalletRepo,
		virtualAccountRepo,
		&AlpacaFundingAdapter{adapter: alpacaFundingAdapter, client: c.AlpacaClient},
		ledgerAdapter,
		c.Logger,
	)
	if c.AlpacaAccountRepo != nil {
		c.FundingService.SetAlpacaAccountLookup(c.AlpacaAccountRepo)
	}
	c.FundingService.SetBridgeDepositClient(&BridgeDepositAdapter{client: c.BridgeClient})
	c.FundingService.SetUserRepo(c.UserRepo)

	// Wire default wallet set ID for funding service wallet creation
	if c.Config.Circle.DefaultWalletSetID != "" {
		if ws, err := c.WalletSetRepo.GetByCircleWalletSetID(context.Background(), c.Config.Circle.DefaultWalletSetID); err == nil && ws != nil {
			c.FundingService.SetDefaultWalletSetID(ws.ID)
		}
	}

	// Initialize allocation service
	allocationRepo := repositories.NewAllocationRepository(sqlxDB, c.Logger)
	c.AllocationService = allocation.NewService(
		allocationRepo,
		c.LedgerService,
		c.Logger,
	)
	// Initialize Blend.money yield router — the sole yield provider. Stash deposits
	// route into each user's Blend Safe on Base.
	if c.Config.Blend.Enabled {
		if c.CircleAdapter == nil {
			c.ZapLog.Error("Blend enabled but Circle adapter is not configured; Blend yield disabled")
		} else {
			blendClient, blendErr := blend.NewClient(blend.Config{
				BaseURL:       c.Config.Blend.BaseURL,
				APIKey:        c.Config.Blend.APIKey,
				AccountTypeID: c.Config.Blend.AccountTypeID,
			}, c.ZapLog)
			if blendErr != nil {
				c.ZapLog.Error("Failed to construct Blend client; Blend yield disabled", zap.Error(blendErr))
			} else {
				c.BlendClient = blendClient
				allowlist := blend.NewAllowlist(c.Config.Blend.AllowedContracts)
				executor := blend.NewPlanExecutor(c.CircleAdapter, allowlist, c.ZapLog)
				// On-chain Safe verification for the executor's dynamic allow. Required in
				// production (config validation enforces base_rpc_url there).
				if rpc := c.Config.Blend.BaseRPCURL; rpc != "" {
					executor.SetSafeVerifier(blend.NewEVMSafeVerifier(rpc, c.ZapLog))
					c.ZapLog.Info("Blend Safe verifier enabled")
				} else {
					c.ZapLog.Warn("Blend Safe verifier DISABLED (no blend.base_rpc_url) — dynamic Safe trust is unverified; dev only")
				}
				router := blend.NewDepositRouter(
					sqlxDB,
					blendClient,
					c.CircleAdapter,
					executor,
					c.Config.Blend.ChainID,
					c.Config.Blend.USDCAddress,
					c.ZapLog,
				)
				if c.Config.Blend.RedeemTimeoutSecs > 0 {
					router.SetRedeemTimeout(time.Duration(c.Config.Blend.RedeemTimeoutSecs) * time.Second)
				}
				if c.Config.Blend.WorkerIntervalSecs > 0 {
					router.SetWorkerInterval(time.Duration(c.Config.Blend.WorkerIntervalSecs) * time.Second)
				}
				if c.Config.Blend.WorkerBatchSize > 0 {
					router.SetWorkerBatchSize(c.Config.Blend.WorkerBatchSize)
				}
				if startErr := router.Start(); startErr != nil {
					c.ZapLog.Error("Failed to start Blend reconciliation worker", zap.Error(startErr))
				} else {
					c.BlendDepositRouter = router
					// Backfill the automation funds-transfer adapter so its
					// reserve-before-debit path (TransferStashToSpend) actually drives
					// Blend — the adapter was built before the router existed, leaving
					// blendRouter nil and the whole reservation invariant inert.
					automationAdapter.blendRouter = router
					// Blend wins over Reflect for new deposits.
					c.AllocationService.SetYieldRouter(router)
					c.ZapLog.Info("Blend yield router started; routing new stash deposits to Blend",
						zap.Int64("chain_id", c.Config.Blend.ChainID),
						zap.Int("allowlisted_contracts", len(c.Config.Blend.AllowedContracts)))
				}
			}
		}
	}

	// Initialize Bridge virtual account service now that allocation + ledger are available.
	if c.BridgeClient != nil {
		c.BridgeVirtualAccountService = funding.NewBridgeVirtualAccountService(
			c.BridgeClient,
			virtualAccountRepo,
			c.DepositRepo,
			c.AllocationService,
			ledgerAdapter,
			c.Logger,
		)
		c.FundingService.SetBridgeVAService(c.BridgeVirtualAccountService)

		// Wire notification service to Bridge VA service
		if c.NotificationService != nil {
			notificationAdapter := &FundingNotificationAdapter{svc: c.NotificationService}
			c.BridgeVirtualAccountService.SetNotificationService(notificationAdapter)
		}

		// Create customer status processor for handling Bridge KYC webhooks
		customerStatusProcessor := webhooks.NewBridgeCustomerStatusProcessor(
			c.UserRepo,
			c.ZapLog,
		)
		c.BridgeCustomerStatusProcessor = customerStatusProcessor

		bridgeWebhookService := webhooks.NewBridgeWebhookService(
			&BridgeVirtualAccountWebhookAdapter{service: c.BridgeVirtualAccountService},
			customerStatusProcessor,
			nil, // Card processor can be injected later.
			nil, // Withdrawal processor can be injected later.
			&bridgeWebhookNotifierAdapter{svc: c.NotificationService},
			c.UserRepo,
			c.ZapLog,
			c.DB,
		)

		// Wire notifier into customer status processor so KYC events fire push notifications
		customerStatusProcessor.SetNotifier(&bridgeWebhookNotifierAdapter{svc: c.NotificationService})

		webhookSecret := c.Config.Bridge.WebhookSecret
		// Security fix: Only skip verification if explicitly configured for development AND no secret is set
		// This ensures production ALWAYS requires verification
		skipWebhookVerification := c.Config.Environment == "development" && webhookSecret == ""
		walletWebhookAdapter := &walletWebhookAdapter{walletService: c.WalletService}
		c.BridgeWebhookHandler = handlers.NewBridgeWebhookHandler(
			bridgeWebhookService,
			walletWebhookAdapter,
			c.ZapLog,
			webhookSecret,
			skipWebhookVerification,
			c.Config.Environment,
		)
	} else {
		c.ZapLog.Warn("Bridge client not configured - Bridge virtual account service disabled")
	}

	// Initialize Circle webhook handler for inbound deposit notifications.
	if c.FundingService != nil && c.WalletRepo != nil {
		c.CircleWebhookHandler = webhooks.NewCircleWebhookHandler(
			c.FundingService,
			&circleWalletLookupAdapter{repo: c.WalletRepo},
			c.ZapLog,
			c.Config.Circle.WebhookSecret,
			c.RedisClient,
		)
		if c.NotificationService != nil {
			c.CircleWebhookHandler.SetNotifier(c.NotificationService)
		}
		if c.CircleAdapter != nil {
			c.CircleWebhookHandler.SetUnsupportedAssetService(c.CircleAdapter)
		}
		// Wire Blend wallet checker to prevent double-counting bridge transfers as deposits.
		c.CircleWebhookHandler.SetBlendWalletChecker(func(ctx context.Context, walletID string) (bool, error) {
			var exists bool
			err := sqlxDB.GetContext(ctx, &exists, `SELECT EXISTS(SELECT 1 FROM blend_user_accounts WHERE circle_wallet_id = $1)`, walletID)
			return exists, err
		})
		// Wire sweep suppressor to prevent Blend redemption sweeps from being
		// double-credited as fresh deposits. When USDC is bridged from Base EOA
		// to Solana via ChainRails, Circle sees it as an inbound on the user's
		// Solana wallet. Without suppression, deposit detection would credit it
		// on top of the ledger move the redemption already backed.
		c.CircleWebhookHandler.SetSweepSuppressor(func(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) (bool, error) {
			var exists bool
			err := sqlxDB.GetContext(ctx, &exists, `
				SELECT EXISTS(
					SELECT 1 FROM blend_yield_redemptions
					WHERE user_id = $1
					  AND status = 'complete'
					  AND swept_at IS NOT NULL
					  AND swept_at > NOW() - INTERVAL '1 hour'
					  AND ABS(amount - $2) < 0.01
				)`, userID, amount)
			return exists, err
		})
	}

	// Initialize auto-invest service (OrderPlacer will be set after InvestingService is created)
	autoInvestRepo := repositories.NewAutoInvestRepository(sqlxDB)
	autoInvestConfig := autoinvest.Config{
		MinThreshold: decimal.Zero,
	}
	c.AutoInvestService = autoinvest.NewService(
		c.LedgerService,
		nil, // OrderPlacer - will be set after InvestingService initialization
		autoInvestConfig,
		c.Logger,
	)
	c.AutoInvestService.SetUserRepository(c.UserRepo)
	c.AutoInvestService.SetAutoInvestRepository(autoInvestRepo)

	// Wire yield snapshotter so stash balance changes are recorded for TWB calculation
	c.AllocationService.SetYieldSnapshotter(c.YieldService)

	// Wire stash lock recorder so deposits start a 90-day lock cycle
	if c.StashLockService != nil {
		c.AllocationService.SetStashLockRecorder(c.StashLockService)
	}
	// Wire deposit-triggered automations
	if c.AutomationService != nil {
		c.AllocationService.SetDepositAutomationEvaluator(c.AutomationService)
	}

	// Inject allocation service into onboarding service (for auto-enabling 70/30 mode)
	c.OnboardingService.SetAllocationService(c.AllocationService)

	// Initialize station service (for home screen / Station endpoint)
	c.StationService = station.NewService(
		c.LedgerService,
		allocationRepo,
		c.DepositRepo,
		c.ZapLog,
	)
	c.StationService.SetAlpacaAccountRepository(c.AlpacaAccountRepo)
	if c.FinancialObligationService != nil {
		c.StationService.SetObligationProvider(&stationObligationAdapter{obligations: c.FinancialObligationService})
	}
	c.StationService.SetGoalBalanceProvider(c.LedgerService)
	if c.CardRepo != nil {
		c.StationService.SetCardCountProvider(c.CardRepo)
	}

	// Initialize gameplay services (notifiers wired after push service is resolved below)
	c.GameplayRepo = repositories.NewGameplayRepository(sqlxDB)
	c.GameplayXPService = gameplay.NewXPService(c.GameplayRepo, nil, c.ZapLog)
	c.GameplayStreakService = gameplay.NewStreakService(c.GameplayRepo, c.ZapLog)
	c.GameplayChallengeService = gameplay.NewChallengeService(c.GameplayRepo, c.GameplayXPService, nil, c.ZapLog)
	c.GameplayAchievementService = gameplay.NewAchievementService(c.GameplayRepo, c.GameplayStreakService, nil, c.ZapLog)
	c.SubscriptionService = subscriptionsvc.NewService(c.GameplayRepo, c.LedgerService, nil, c.ZapLog)
	c.GameplayChallengeService.SetSubscriptionChecker(c.SubscriptionService)
	c.GameplayHooks = gameplay.NewHooks(c.GameplayXPService, c.GameplayStreakService, c.GameplayChallengeService, c.ZapLog)

	// Initialize V2 gameplay services
	c.GameplayRingsService = gameplay.NewRingsService(c.GameplayRepo, c.ZapLog)
	c.GameplayBoostService = gameplay.NewBoostService(c.GameplayRepo, nil, c.ZapLog)
	c.GameplayPointsService = gameplay.NewPointsService(c.GameplayRepo, c.ZapLog)
	c.GameplayGraceDayService = gameplay.NewGraceDayService(c.GameplayRepo, c.GameplayPointsService, nil, c.ZapLog)
	c.GameplayRecapService = gameplay.NewRecapService(c.GameplayRepo, c.GameplayRingsService, c.GameplayPointsService, c.ZapLog)

	// Wire new services into hooks
	c.GameplayHooks.SetBoosts(c.GameplayBoostService)
	c.GameplayHooks.SetPoints(c.GameplayPointsService)
	c.GameplayHooks.SetGraceDay(c.GameplayGraceDayService)

	// Wire V2 services into achievement evaluator
	c.GameplayAchievementService.SetRingsService(c.GameplayRingsService)
	c.GameplayAchievementService.SetPointsService(c.GameplayPointsService)
	c.GameplayAchievementService.SetBoostService(c.GameplayBoostService)
	c.GameplayAchievementService.SetGraceDayService(c.GameplayGraceDayService)

	// Wire gameplay hooks into existing services
	if c.FundingService != nil {
		c.FundingService.SetGameplayHooks(c.GameplayHooks)
	}
	if c.RoundupService != nil {
		c.RoundupService.SetGameplayHooks(c.GameplayHooks)
	}
	if c.OnboardingService != nil {
		c.OnboardingService.SetGameplayHooks(c.GameplayHooks)
	}
	if c.CardService != nil {
		c.CardService.SetGameplayHooks(c.GameplayHooks)
	}
	if c.BridgeVirtualAccountService != nil {
		c.BridgeVirtualAccountService.SetGameplayHooks(c.GameplayHooks)
	}

	// Wire user stats provider for achievement evaluation
	c.GameplayAchievementService.SetUserStatsProvider(repositories.NewUserStatsRepository(sqlxDB))

	// Initialize investing service with repositories
	basketRepo := repositories.NewBasketRepository(c.DB, c.ZapLog)
	orderRepo := repositories.NewOrderRepository(c.DB, c.ZapLog)
	positionRepo := repositories.NewPositionRepository(c.DB, c.ZapLog)

	// Initialize brokerage adapter with Alpaca service and required repositories
	brokerageAdapter := adapters.NewBrokerageAdapter(
		c.AlpacaClient,
		basketRepo,
		c.AlpacaAccountRepo,
		c.ZapLog,
	)
	c.BrokerageAdapter = brokerageAdapter

	// Initialize notification service with persister for in-app notifications
	c.NotificationService = services.NewNotificationService(c.ZapLog)
	c.NotificationService.SetPersister(adapters.NewNotificationPersisterAdapter(c.NotificationRepo))

	// Defer-wire Notifier into Miriam intelligence services (initialized before this point).
	// iMessage-only: prefer the bridge dispatcher, fall back to in-app notifications.
	var deferredMiriamNotifier miriamservice.Notifier = c.NotificationService
	if c.MiriamBridgeDispatcher != nil {
		deferredMiriamNotifier = c.MiriamBridgeDispatcher
	}
	if c.MiriamProactiveNudgeEngine != nil {
		c.MiriamProactiveNudgeEngine.SetNotifier(deferredMiriamNotifier)
	}
	if c.MiriamIntelligenceOrchestrator != nil {
		c.MiriamIntelligenceOrchestrator.SetNotifier(deferredMiriamNotifier)
	}
	if c.MiriamSelfReviewEngine != nil {
		c.MiriamSelfReviewEngine.SetNotifier(deferredMiriamNotifier)
	}

	// Wire push notification service. OneSignal is the preferred delivery path
	// when configured (PUSH_PROVIDER=onesignal + ONESIGNAL_* credentials);
	// otherwise Expo remains the default. AWS/SNS was decommissioned, so stale
	// SNS env config must never win the routing (it silently swallowed every
	// push after the AWS account went away).
	if c.Config.SNSPush.IOSPlatformARN != "" || c.Config.SNSPush.AndroidPlatformARN != "" {
		c.ZapLog.Warn("SNS push config is set but ignored — AWS is decommissioned; using OneSignal/Expo push. Remove SNS_PUSH_* env vars.")
	}
	var livePush interface {
		SendToUser(ctx context.Context, userID uuid.UUID, title, body string, data map[string]interface{}) error
	}
	switch c.Config.Push.Provider {
	case "onesignal":
		if c.Config.Push.OneSignalAppID == "" || c.Config.Push.OneSignalAPIKey == "" {
			c.ZapLog.Error("PUSH_PROVIDER=onesignal but ONESIGNAL_APP_ID/ONESIGNAL_API_KEY missing — falling back to Expo push")
		} else {
			oneSignalPushService := adapters.NewOneSignalPushService(
				c.Config.Push.OneSignalAppID,
				c.Config.Push.OneSignalAPIKey,
				c.ZapLog,
			)
			oneSignalPushService.SetAndroidChannelID(c.Config.Push.OneSignalChannel)
			c.OneSignalPushService = oneSignalPushService
			livePush = oneSignalPushService
			c.ZapLog.Info("OneSignal push service initialized as the push delivery path")
		}
	}
	if livePush == nil {
		expoPushService := adapters.NewExpoPushService(c.DeviceTokenRepo, c.ZapLog)
		c.ExpoPushService = expoPushService
		livePush = expoPushService
		c.ZapLog.Info("Expo push service initialized as the push delivery path")
	}
	c.NotificationService.SetPushSender(livePush)
	// Wire email notifications for important events
	if c.EmailService != nil {
		c.NotificationService.SetEmailSender(adapters.NewEmailSenderAdapter(c.EmailService))
	}
	c.NotificationService.SetUserEmailLookup(adapters.NewUserEmailLookup(c.UserRepo))

	if c.GrowthEngineRepo != nil {
		var growthPush growthengine.PushSender = livePush
		c.GrowthEngineService = growthengine.NewService(
			c.GrowthEngineRepo,
			c.EmailService,
			growthPush,
			growthengine.Config{Limit: 1000},
			c.ZapLog,
		)
		if c.EmailService != nil {
			c.GrowthEngineService.SetBatchEmailSender(&growthBatchEmailAdapter{email: c.EmailService})
		}
	}

	// Wire push notifier into gameplay services (now that push provider is resolved).
	var pushNotifier gameplay.PushNotifier = livePush
	if pushNotifier != nil {
		c.GameplayXPService.SetNotifier(pushNotifier)
		c.GameplayChallengeService.SetNotifier(pushNotifier)
		c.GameplayAchievementService.SetNotifier(pushNotifier)
		c.SubscriptionService.SetNotifier(pushNotifier)
		c.GameplayBoostService.SetNotifier(pushNotifier)
		c.GameplayGraceDayService.SetNotifier(pushNotifier)
	}

	// Wire Bridge transfer into subscription service for fee collection
	if c.BridgeClient != nil && c.Config.Circle.TreasuryWalletAddress != "" && c.WalletRepo != nil {
		c.SubscriptionService.SetBridgeTransfer(&SubscriptionBridgeTransferAdapter{
			bridgeClient:      c.BridgeClient,
			walletRepo:        c.WalletRepo,
			userRepo:          c.UserRepo,
			companyWalletAddr: c.Config.Circle.TreasuryWalletAddress,
			logger:            c.ZapLog,
		})
	}

	// Wire notification service into auto-invest and allocation for failure alerts
	c.AutoInvestService.SetNotificationService(c.NotificationService)
	c.AllocationService.SetNotificationService(c.NotificationService)

	// Wire Umbra privacy shielder into allocation service
	if c.UmbraClient != nil {
		umbraWalletRepo := repositories.NewUmbraWalletRepository(sqlxDB)
		c.UmbraWalletService = umbrawallet.NewService(
			umbraWalletRepo, c.UmbraClient,
			c.Config.Security.EncryptionKey, c.Config.Umbra.Network,
			c.Logger,
		)
		c.AllocationService.SetUmbraShielder(&UmbraShielderAdapter{
			client:        c.UmbraClient,
			walletService: c.UmbraWalletService,
		})
		c.OnboardingService.SetUmbraProvisioner(&UmbraProvisionerAdapter{walletService: c.UmbraWalletService})
	}
	if c.YieldService != nil {
		c.YieldService.SetNotifier(c.NotificationService)
	}

	c.InvestingService = investing.NewService(
		basketRepo,
		orderRepo,
		positionRepo,
		c.BalanceRepo,
		brokerageAdapter,
		c.WalletRepo,
		&BridgeWalletBalanceAdapter{adapter: c.BridgeAdapter},
		c.AllocationService,
		c.NotificationService,
		c.Logger,
	)

	// NOTE: AutoInvestService OrderPlacer/FundingBridge wiring is done after
	// initializeAlpacaInvestmentServices (below) so AlpacaAccountService is non-nil.

	// Initialize strategy engine and wire to auto-invest service
	c.StrategyEngine = strategy.NewEngine(&strategyUserProfileAdapter{userRepo: c.UserRepo}, c.Logger)
	c.StrategyEngine.SetRulesProvider(repositories.NewInvestmentRulesRepository(sqlxDB))
	c.InvestmentRulesRepo = repositories.NewInvestmentRulesRepository(sqlxDB)
	c.StrategyEngine.SetFrequencyProvider(repositories.NewDepositRepository(sqlxDB))
	c.AutoInvestService.SetStrategyEngine(c.StrategyEngine)

	// Initialize reconciliation service
	if err := c.initializeReconciliationService(); err != nil {
		return fmt.Errorf("failed to initialize reconciliation service: %w", err)
	}

	// Initialize limits service for deposit/withdrawal limits
	usageRepo := repositories.NewUsageRepository(c.DB, c.ZapLog)
	c.LimitsService = limits.NewService(c.UserRepo, usageRepo, c.Logger)
	c.LimitsService.SetRateProvider(NewPajRateProvider(c.RedisClient))

	// Initialize self-imposed daily spending commitment service
	commitmentFee := entities.DefaultSpendingCommitmentIncreaseFee
	if raw := os.Getenv("SPENDING_COMMITMENT_INCREASE_FEE"); raw != "" {
		if parsed, err := decimal.NewFromString(raw); err == nil && parsed.IsPositive() {
			commitmentFee = parsed
		}
	}
	c.SpendingCommitmentService = spendingcommitmentservice.NewService(
		repositories.NewSpendingCommitmentRepository(sqlxDB),
		c.LedgerService,
		c.LedgerService,
		commitmentFee,
		c.ZapLog,
	)
	c.SpendingCommitmentService.SetRateProvider(NewPajRateProvider(c.RedisClient))

	// Initialize domain audit service for compliance logging
	auditRepo := repositories.NewAuditRepository(sqlxDB)
	c.DomainAuditService = audit.NewService(auditRepo, c.ZapLog)

	// Initialize security services
	c.LoginProtectionService = security.NewLoginProtectionService(c.RedisClient.Client(), c.ZapLog)
	c.DeviceTrackingService = security.NewDeviceTrackingService(c.DB, c.ZapLog)
	c.WithdrawalSecurityService = security.NewWithdrawalSecurityService(c.DB, c.RedisClient.Client(), c.ZapLog)
	c.IPWhitelistService = security.NewIPWhitelistService(c.DB, c.RedisClient.Client(), c.ZapLog)
	c.PasswordPolicyService = security.NewPasswordPolicyService(c.Config.Security.CheckPasswordBreaches)
	c.SecurityEventLogger = security.NewSecurityEventLogger(c.DB, c.ZapLog)
	c.PasswordService = security.NewPasswordService(c.DB, c.ZapLog, c.Config.Security.CheckPasswordBreaches)

	// Initialize enhanced security services (MFA, Geo, Fraud, Incident Response)
	c.MFAService = security.NewMFAService(c.DB, c.RedisClient.Client(), c.ZapLog, c.Config.Security.EncryptionKey, nil) // SMS provider can be injected later
	c.GeoSecurityService = security.NewGeoSecurityService(c.DB, c.RedisClient.Client(), c.ZapLog, "")                   // IP API key can be configured
	c.FraudDetectionService = security.NewFraudDetectionService(c.DB, c.RedisClient.Client(), c.ZapLog)
	c.IncidentResponseService = security.NewIncidentResponseService(c.DB, c.RedisClient.Client(), c.ZapLog, nil, c.SecurityEventLogger)

	// Initialize onboarding fraud detection (cross-account device/IP correlation)
	onboardingFraudRepo := repositories.NewOnboardingFraudRepository(sqlxDB)
	c.OnboardingFraudService = security.NewOnboardingFraudService(onboardingFraudRepo, c.ZapLog)

	// Initialize security features v2 (risk scoring, whitelist, anomaly, limits, adaptive MFA)
	c.SecurityFeaturesRepo = repositories.NewSecurityFeaturesRepository(sqlxDB)
	c.RiskScoringService = security.NewRiskScoringService(c.SecurityFeaturesRepo, c.ZapLog)
	c.AddressWhitelistService = security.NewAddressWhitelistService(c.SecurityFeaturesRepo, c.ZapLog)
	c.SessionAnomalyService = security.NewSessionAnomalyService(c.SecurityFeaturesRepo, c.ZapLog)
	c.WithdrawalLimitsService = security.NewWithdrawalLimitsService(c.SecurityFeaturesRepo, c.ZapLog)
	c.AdaptiveMFAService = security.NewAdaptiveMFAService(c.SecurityFeaturesRepo, c.ZapLog)
	c.DeviceSecurityService = security.NewDeviceSecurityService(c.DeviceTrackingService, c.SecurityEventLogger, c.ZapLog)

	// Initialize token blacklist and JWT service
	if c.Config.Security.EnableTokenBlacklist {
		c.TokenBlacklist = auth.NewTokenBlacklist(c.RedisClient.Client())
		c.JWTService = auth.NewJWTService(
			c.Config.JWT.Secret,
			c.Config.Security.AccessTokenTTL,
			c.Config.Security.RefreshTokenTTL,
			c.TokenBlacklist,
		)
	}

	// Initialize tiered rate limiter with configuration
	endpointLimits := make(map[string]ratelimit.EndpointLimit)
	for key, limit := range c.Config.RateLimit.EndpointLimits {
		endpointLimits[key] = ratelimit.EndpointLimit{
			Limit:  limit.Limit,
			Window: time.Duration(limit.Window) * time.Second,
		}
	}

	tieredConfig := ratelimit.TieredConfig{
		GlobalLimit:    c.Config.RateLimit.GlobalLimit,
		GlobalWindow:   time.Duration(c.Config.RateLimit.GlobalWindow) * time.Second,
		IPLimit:        c.Config.RateLimit.IPLimit,
		IPWindow:       time.Duration(c.Config.RateLimit.IPWindow) * time.Second,
		UserLimit:      c.Config.RateLimit.UserLimit,
		UserWindow:     time.Duration(c.Config.RateLimit.UserWindow) * time.Second,
		EndpointLimits: endpointLimits,
	}
	c.TieredRateLimiter = ratelimit.NewTieredLimiter(c.RedisClient.Client(), tieredConfig, c.ZapLog)
	c.LoginAttemptTracker = ratelimit.NewLoginAttemptTracker(c.RedisClient.Client(), c.ZapLog)

	// Initialize CAPTCHA verifier if secret key is configured
	if captchaKey := c.Config.Security.CaptchaSecretKey; captchaKey != "" {
		c.CaptchaVerifier = captcha.NewVerifier(captcha.Config{
			Enabled:   true,
			Provider:  captcha.ProviderRecaptcha,
			SecretKey: captchaKey,
		})
	}

	// Initialize Device-Bound JWT (Priority 1)
	if c.Config.Security.DeviceBinding.Enabled {
		sqlxDB := sqlx.NewDb(c.DB, "postgres")
		c.DeviceSessionRepo = repositories.NewDeviceSessionRepository(sqlxDB)
		c.DeviceBindingAuditRepo = repositories.NewDeviceBindingAuditRepository(sqlxDB)
		c.DeviceBoundJWTService = auth.NewDeviceBoundJWTService(
			c.JWTService,
			c.DeviceSessionRepo,
			c.DeviceBindingAuditRepo,
			auth.DeviceBindingConfig{
				Enabled:               c.Config.Security.DeviceBinding.Enabled,
				MaxConcurrentSessions: c.Config.Security.DeviceBinding.MaxConcurrentSessions,
				SessionTTL:            time.Duration(c.Config.Security.DeviceBinding.SessionTTLHours) * time.Hour,
				StrictValidation:      c.Config.Security.DeviceBinding.StrictValidation,
			},
			c.ZapLog,
		)
	}

	// Initialize Adaptive Rate Limiter (Priority 3)
	if c.Config.Security.AdaptiveRateLimit.Enabled {
		c.RiskScoringEngine = ratelimit.NewRiskScoringEngine(
			c.RedisClient.Client(),
			ratelimit.DefaultRiskWeights(),
			c.ZapLog,
		)
		c.AdaptiveRateLimiter = ratelimit.NewAdaptiveRateLimiter(
			c.RedisClient.Client(),
			c.RiskScoringEngine,
			ratelimit.DefaultAdaptiveConfig(),
			c.ZapLog,
		)
	}

	// Wire limits and audit services to funding service
	c.FundingService.SetLimitsService(c.LimitsService)
	c.FundingService.SetAuditService(c.DomainAuditService)
	c.FundingService.SetNotificationService(&FundingNotificationAdapter{svc: c.NotificationService})
	c.FundingService.SetAllocationService(c.AllocationService) // Enable automatic 70/30 deposit split

	// Initialize withdrawal service with adapters
	withdrawalBridgeAdapter := &WithdrawalBridgeAdapter{adapter: c.BridgeAdapter}

	// Create bank account repository
	bankAccountRepo := repositories.NewBankAccountRepository(sqlxDB)
	c.BankAccountRepo = bankAccountRepo

	// Create adapters for withdrawal service
	withdrawalLedgerAdapter := &WithdrawalLedgerAdapter{ledgerService: c.LedgerService}
	withdrawalNotificationAdapter := &WithdrawalNotificationAdapter{svc: c.NotificationService}

	c.WithdrawalService = services.NewWithdrawalService(
		c.WithdrawalRepo,
		c.UserRepo,
		withdrawalLedgerAdapter,
		bankAccountRepo,
		c.LimitsService,
		c.DomainAuditService,
		withdrawalNotificationAdapter,
		withdrawalBridgeAdapter, // BridgeAdapter (fiat offramp)
		c.BridgeAdapter,         // BridgeCryptoTransferAdapter (crypto wallet transfers)
		sqlx.NewDb(c.DB, "postgres"),
		c.Logger,
	)

	// Wire stash lock enforcement
	stashLockRepo := repositories.NewStashLockRepository(sqlx.NewDb(c.DB, "postgres"))
	stashLockSvc := stashlock.NewService(stashLockRepo, c.ZapLog)
	if c.NotificationService != nil {
		stashLockSvc.SetNotifier(c.NotificationService)
	}
	c.WithdrawalService.SetStashLockChecker(stashLockSvc)
	c.WithdrawalService.SetEmergencyLedger(c.LedgerService)
	c.LedgerService.SetStashLockChecker(stashLockSvc)
	c.StashLockService = stashLockSvc

	// Wire stash transfer repo for audit logging
	stashTransferRepo := repositories.NewStashTransferRepository(sqlx.NewDb(c.DB, "postgres"))
	c.WithdrawalService.SetStashTransferRepo(stashTransferRepo)

	// Wire Circle crypto transfer adapter
	if c.CircleAdapter != nil {
		c.WithdrawalService.SetCircleTransferAdapter(c.CircleAdapter)
	}
	// Wire stash yield redemption to Blend (the sole yield provider). A withdrawal
	// from stash redeems the user's Blend position back to USDC before it exits.
	if c.BlendDepositRouter != nil {
		c.WithdrawalService.SetStashYieldRedeemer(c.BlendDepositRouter)
	}

	// Initialize compliance screening (Didit transaction monitoring + AML) — wired below after DiditClient creation

	if c.BridgeWebhookHandler != nil && c.BridgeVirtualAccountService != nil {
		var cardProcessor webhooks.BridgeCardProcessor
		if c.CardService != nil {
			cardProcessor = &BridgeCardWebhookAdapter{service: c.CardService}
		}
		bridgeWebhookService := webhooks.NewBridgeWebhookService(
			&BridgeVirtualAccountWebhookAdapter{service: c.BridgeVirtualAccountService},
			c.BridgeCustomerStatusProcessor,
			cardProcessor,
			c.WithdrawalService,
			&bridgeWebhookNotifierAdapter{svc: c.NotificationService},
			c.UserRepo,
			c.ZapLog,
			c.DB,
		)
		c.BridgeWebhookHandler.SetService(bridgeWebhookService)
	}

	// Initialize AI Financial Manager services
	if err := c.initializeAIServices(sqlxDB, positionRepo, allocationRepo, basketRepo); err != nil {
		c.ZapLog.Warn("AI services initialization failed, AI features disabled", zap.Error(err))
	}

	// Initialize Alpaca investment infrastructure
	if err := c.initializeAlpacaInvestmentServices(sqlxDB); err != nil {
		c.ZapLog.Warn("Alpaca investment services initialization failed", zap.Error(err))
	}

	// Wire auto-invest service with OrderPlacer now that AlpacaAccountService is initialized
	autoInvestOrderPlacer := &autoInvestOrderPlacerAdapter{
		accountService: c.AlpacaAccountService,
		alpacaClient:   c.AlpacaClient,
		orderRepo:      c.InvestmentOrderRepo,
		logger:         c.ZapLog,
	}
	c.AutoInvestService.SetOrderPlacer(autoInvestOrderPlacer)
	if c.AlpacaFundingBridge != nil {
		c.AutoInvestService.SetFundingBridge(c.AlpacaFundingBridge)
	}
	if c.AlpacaAccountRepo != nil {
		c.AutoInvestService.SetAccountLookup(c.AlpacaAccountRepo)
	}
	if c.AlpacaPortfolioSync != nil {
		c.AutoInvestService.SetPositionSyncer(c.AlpacaPortfolioSync)
	}

	// Wire station service with AlpacaAccountService now that it's initialized
	if c.AlpacaAccountService != nil {
		c.StationService.SetAlpacaAccountService(c.AlpacaAccountService)
	}

	// Initialize advanced features (analytics, market data, scheduled investments, rebalancing)
	if err := c.initializeAdvancedFeatures(sqlxDB); err != nil {
		c.ZapLog.Warn("Advanced features initialization failed", zap.Error(err))
	}

	// Initialize instant funding + ChainRails cross-chain deposit services
	c.initializeInstantFundingServices(sqlxDB)

	// Initialize unified funding webhook handler (Bridge + Alpaca).
	alpacaWebhookHandler := c.GetAlpacaWebhookHandlers()
	c.UnifiedFundingWebhookHandler = webhooks.NewUnifiedFundingWebhookHandler(
		c.BridgeWebhookHandler,
		nil, // circleHandler removed
		alpacaWebhookHandler,
		c.ZapLog,
	)
	if bridgeSecret := strings.TrimSpace(c.Config.Bridge.WebhookSecret); bridgeSecret != "" {
		c.UnifiedFundingWebhookHandler.SetWebhookSecret("bridge", bridgeSecret)
	}
	if alpacaSecret := strings.TrimSpace(c.Config.Alpaca.WebhookSecret); alpacaSecret != "" {
		c.UnifiedFundingWebhookHandler.SetWebhookSecret("alpaca", alpacaSecret)
	}

	// Initialize account deletion service
	c.AccountDeletionService = account.NewDeletionService(
		&deletionLedgerAdapter{ledgerService: c.LedgerService},
		c.WalletRepo,
		&BridgeWalletBalanceAdapter{adapter: c.BridgeAdapter},
		&deletionUserRepoAdapter{userRepo: c.UserRepo},
		c.DomainAuditService,
		c.Config.Circle.TreasuryWalletAddress,
		c.Logger,
	)

	// Wire external provider cleanup for account deletion
	if c.AlpacaAccountRepo != nil && c.AlpacaClient != nil {
		c.AccountDeletionService.SetAlpacaClient(c.AlpacaAccountRepo, c.AlpacaClient)
	}
	deletionVirtualAccountRepo := repositories.NewVirtualAccountRepository(sqlxDB)
	if c.BridgeClient != nil {
		c.AccountDeletionService.SetBridgeClient(deletionVirtualAccountRepo, &deletionBridgeAdapter{client: c.BridgeClient})
	}
	if c.SessionService != nil {
		c.AccountDeletionService.SetSessionService(c.SessionService)
	}
	if c.DeviceTokenRepo != nil {
		c.AccountDeletionService.SetDeviceTokenRepo(c.DeviceTokenRepo)
	}

	// Wire Didit session deletion for account closure (GDPR)
	if diditAPIKey := c.Config.KYC.DiditAPIKey; diditAPIKey != "" {
		if c.UserRepo == nil {
			return fmt.Errorf("UserRepo must be initialized before setting up Didit client")
		}
		diditClient := didit.NewClient(didit.Config{
			APIKey:        diditAPIKey,
			WebhookSecret: c.Config.KYC.DiditWebhookSecret,
		}, c.ZapLog)
		c.DiditClient = diditClient
		c.AccountDeletionService.SetDiditClient(diditClient, c.UserRepo, c.KYCSubmissionRepo)

		// Wire compliance screening (transaction monitoring + AML)
		complianceRepo := repositories.NewComplianceRepository(sqlxDB, c.ZapLog)
		c.ComplianceService = compliancesvc.NewService(diditClient, complianceRepo, c.ZapLog)
		c.ComplianceService.SetUserLookup(c.UserRepo)
		c.ComplianceService.SetUserFreezer(&complianceUserFreezer{userRepo: c.UserRepo, logger: c.ZapLog})
		c.FundingService.SetComplianceScreener(c.ComplianceService)
		c.WithdrawalService.SetComplianceScreener(c.ComplianceService)
		if c.BridgeVirtualAccountService != nil {
			c.BridgeVirtualAccountService.SetComplianceScreener(c.ComplianceService)
		}
		c.ZapLog.Info("Compliance screening enabled (Didit transaction monitoring)")
	}

	// Wire security features v2 into withdrawal service
	if c.AddressWhitelistService != nil {
		c.WithdrawalService.SetAddressWhitelistChecker(c.AddressWhitelistService)
	}
	if c.WithdrawalLimitsService != nil {
		c.WithdrawalService.SetTieredWithdrawalLimits(&tieredLimitsAdapter{svc: c.WithdrawalLimitsService})
	}

	// Wire fraud detection and session anomaly enforcement into withdrawal path
	if c.FraudDetectionService != nil {
		c.WithdrawalService.SetFraudChecker(c.FraudDetectionService)
	}
	if c.SessionAnomalyService != nil {
		c.WithdrawalService.SetSessionAnomalyChecker(c.SessionAnomalyService)
	}
	if c.SpendingCommitmentService != nil {
		c.WithdrawalService.SetSpendingCommitment(c.SpendingCommitmentService)
	}

	// Wire admin error alert emails
	if c.EmailService != nil && c.Config.AdminAlertEmail != "" {
		c.WithdrawalService.SetAdminAlerter(adapters.NewAdminAlertService(c.EmailService, c.Config.AdminAlertEmail, c.ZapLog))
		c.ZapLog.Info("Admin error alert emails configured", zap.String("email", c.Config.AdminAlertEmail))
	}

	// Wire Circle webhook handler to withdrawal service now that it's initialized
	if c.CircleWebhookHandler != nil {
		c.CircleWebhookHandler.SetWithdrawalCompleter(c.WithdrawalService)
	}

	// Initialize P2P transfer services
	c.P2PRepo = repositories.NewP2PRepository(sqlxDB, c.ZapLog)
	c.P2PNotificationSender = adapters.NewP2PNotificationSender(
		c.EmailService,
		c.UserRepo,
		c.Config.Email.BaseURL,
		c.ZapLog,
	)
	if c.NotificationService != nil {
		c.P2PNotificationSender.SetPushService(c.NotificationService)
	}
	c.P2PService = p2p.NewService(
		c.P2PRepo,
		c.UserRepo,
		repositories.NewP2PBalanceProvider(c.LedgerService),
		repositories.NewP2PTransferExecutor(c.LedgerService),
		c.P2PNotificationSender,
		c.ZapLog,
	)
	c.P2PService.SetUserUpdater(c.UserRepo)
	c.P2PService.SetWalletLookup(c.WalletRepo)
	c.P2PService.SetTapIntentStore(c.RedisClient)
	if c.SpendingCommitmentService != nil {
		c.P2PService.SetSpendingCommitment(c.SpendingCommitmentService)
	}
	if c.BridgeClient != nil {
		c.P2PService.SetBridgeOfframp(NewP2PBridgeOfframpAdapter(bridge.NewAdapter(c.BridgeClient, c.ZapLog)))
	}
	c.P2PHandlers = p2phandlers.NewHandlers(c.P2PService, c.ZapLog)

	// Wire P2P service to onboarding for auto-claim
	c.OnboardingService.SetP2PService(c.P2PService)

	// Pay-bill automations send real money to a payee via P2P on the due day.
	if c.AutomationService != nil {
		c.AutomationService.SetBillPayer(&p2pBillPayerAdapter{p2p: c.P2PService})
	}

	// Wire wallet provider to virtual account service for on-demand provisioning
	if c.BridgeVirtualAccountService != nil && c.WalletService != nil {
		c.BridgeVirtualAccountService.SetWalletProvider(c.WalletService)
	}

	// Initialize premium feature services
	c.NairaShieldService = premium.NewNairaShieldService(c.ExchangeRateRepo, c.LedgerService, c.ZapLog)
	c.BlackTaxService = premium.NewBlackTaxService(c.FamilySupportRepo, c.P2PService, c.ZapLog)
	c.ReceiptSplitService = premium.NewReceiptSplitService(c.ReceiptRepo, c.ReceiptSplitRepo, c.P2PService, c.ZapLog)
	c.ScamIntelligenceService = premium.NewScamIntelligenceService(c.ScamRepo, c.ZapLog)
	c.TaxResidencyService = premium.NewTaxResidencyService(c.TaxResidencyRepo, c.ZapLog)
	c.IncomeSmoothingService = premium.NewIncomeSmoothingService(c.DepositRepo, c.LedgerService, c.ZapLog)
	c.FinancialTraumaService = premium.NewFinancialTraumaService(c.WellnessRepo, c.CardService, c.ZapLog)
	c.VisaProofService = premium.NewVisaProofService(c.VisaProofRepo, c.LedgerService, c.DepositRepo, c.ZapLog)
	c.PanicButtonService = premium.NewPanicButtonService(c.EmergencyRepo, c.LedgerService, c.ZapLog)

	return nil
}
