package di

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/api/handlers"
	activityhandlers "github.com/rail-service/rail_service/internal/api/handlers/activity"
	fundinghandlers "github.com/rail-service/rail_service/internal/api/handlers/funding"
	opportunityhandlers "github.com/rail-service/rail_service/internal/api/handlers/opportunities"
	p2phandlers "github.com/rail-service/rail_service/internal/api/handlers/p2p"
	premiumhandlers "github.com/rail-service/rail_service/internal/api/handlers/premium"
	"github.com/rail-service/rail_service/internal/api/handlers/webhooks"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services"
	"github.com/rail-service/rail_service/internal/domain/services/account"
	activitysvc "github.com/rail-service/rail_service/internal/domain/services/activity"
	aiservice "github.com/rail-service/rail_service/internal/domain/services/ai"
	"github.com/rail-service/rail_service/internal/domain/services/allocation"
	alpacaservice "github.com/rail-service/rail_service/internal/domain/services/alpaca"
	analyticsservice "github.com/rail-service/rail_service/internal/domain/services/analytics"
	"github.com/rail-service/rail_service/internal/domain/services/apikey"
	"github.com/rail-service/rail_service/internal/domain/services/audit"
	"github.com/rail-service/rail_service/internal/domain/services/autoinvest"
	"github.com/rail-service/rail_service/internal/domain/services/automation"
	"github.com/rail-service/rail_service/internal/domain/services/card"
	compliancesvc "github.com/rail-service/rail_service/internal/domain/services/compliance"
	conversationsvc "github.com/rail-service/rail_service/internal/domain/services/conversation"
	"github.com/rail-service/rail_service/internal/domain/services/copytrading"
	"github.com/rail-service/rail_service/internal/domain/services/funding"
	"github.com/rail-service/rail_service/internal/domain/services/gameplay"
	"github.com/rail-service/rail_service/internal/domain/services/growthmail"
	"github.com/rail-service/rail_service/internal/domain/services/integration"
	"github.com/rail-service/rail_service/internal/domain/services/investing"
	knowledgesvc "github.com/rail-service/rail_service/internal/domain/services/knowledge"
	"github.com/rail-service/rail_service/internal/domain/services/kyc"
	"github.com/rail-service/rail_service/internal/domain/services/ledger"
	"github.com/rail-service/rail_service/internal/domain/services/limits"
	marketservice "github.com/rail-service/rail_service/internal/domain/services/market"
	miriamservice "github.com/rail-service/rail_service/internal/domain/services/miriam"
	moneyguardservice "github.com/rail-service/rail_service/internal/domain/services/moneyguard"
	newsservice "github.com/rail-service/rail_service/internal/domain/services/news"
	obligationservice "github.com/rail-service/rail_service/internal/domain/services/obligation"
	"github.com/rail-service/rail_service/internal/domain/services/onboarding"
	opportunitysvc "github.com/rail-service/rail_service/internal/domain/services/opportunity"
	"github.com/rail-service/rail_service/internal/domain/services/p2p"
	"github.com/rail-service/rail_service/internal/domain/services/pajfunding"
	"github.com/rail-service/rail_service/internal/domain/services/passcode"
	"github.com/rail-service/rail_service/internal/domain/services/premium"
	"github.com/rail-service/rail_service/internal/domain/services/reconciliation"
	"github.com/rail-service/rail_service/internal/domain/services/roundup"
	"github.com/rail-service/rail_service/internal/domain/services/security"
	"github.com/rail-service/rail_service/internal/domain/services/session"
	"github.com/rail-service/rail_service/internal/domain/services/socialauth"
	spendingsvc "github.com/rail-service/rail_service/internal/domain/services/spending"
	"github.com/rail-service/rail_service/internal/domain/services/stashlock"
	"github.com/rail-service/rail_service/internal/domain/services/station"
	"github.com/rail-service/rail_service/internal/domain/services/strategy"
	subscriptionsvc "github.com/rail-service/rail_service/internal/domain/services/subscription"
	"github.com/rail-service/rail_service/internal/domain/services/twofa"
	"github.com/rail-service/rail_service/internal/domain/services/umbrawallet"
	usagesvc "github.com/rail-service/rail_service/internal/domain/services/usage"
	waitlistsvc "github.com/rail-service/rail_service/internal/domain/services/waitlist"
	"github.com/rail-service/rail_service/internal/domain/services/wallet"
	"github.com/rail-service/rail_service/internal/domain/services/webauthn"
	yieldsvc "github.com/rail-service/rail_service/internal/domain/services/yield"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/alpaca"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/bridge"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/chainrails"
	circleadapter "github.com/rail-service/rail_service/internal/infrastructure/adapters/circle"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/didit"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/embeddings"
	pajadapter "github.com/rail-service/rail_service/internal/infrastructure/adapters/paj"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/reflect"
	superteamadapter "github.com/rail-service/rail_service/internal/infrastructure/adapters/superteam"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/umbra"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	recon "github.com/rail-service/rail_service/internal/workers/reconciliation"
	treasury_sweep "github.com/rail-service/rail_service/internal/workers/treasury_sweep"
	yield_distribution "github.com/rail-service/rail_service/internal/workers/yield_distribution"
	"github.com/rail-service/rail_service/pkg/auth"
	"github.com/rail-service/rail_service/pkg/captcha"
	commonmetrics "github.com/rail-service/rail_service/pkg/common/metrics"
	"github.com/rail-service/rail_service/pkg/logger"
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
		return "", fmt.Errorf("EUR bank recipients are not yet supported by the Bridge adapter")
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

// Container holds all application dependencies
type Container struct {
	Config *config.Config
	DB     *sql.DB
	Logger *logger.Logger
	ZapLog *zap.Logger

	// Repositories
	UserRepo                  *repositories.UserRepository
	OnboardingFlowRepo        *repositories.OnboardingFlowRepository
	KYCSubmissionRepo         *repositories.KYCSubmissionRepository
	WalletRepo                *repositories.WalletRepository
	WalletSetRepo             *repositories.WalletSetRepository
	WalletProvisioningJobRepo *repositories.WalletProvisioningJobRepository
	DepositRepo               *repositories.DepositRepository
	WithdrawalRepo            *repositories.WithdrawalRepository
	ReceiptRepo               *repositories.ReceiptRepository
	BudgetRepo                *repositories.BudgetRepository
	FinancialProfileRepo      *repositories.FinancialProfileRepository
	FinancialObligationRepo   *repositories.FinancialObligationRepository
	AutomationRepo            *repositories.AutomationRepository
	ContextSignalRepo         *repositories.ContextSignalRepository
	LedgerSpendingRepo        *repositories.LedgerSpendingRepository
	ConversionRepo            *repositories.ConversionRepository
	BalanceRepo               *repositories.BalanceRepository
	FundingEventJobRepo       *repositories.FundingEventJobRepository
	SumsubWebhookEventRepo    *repositories.SumsubWebhookEventRepository
	KYCSyncJobRepo            *repositories.KYCSyncJobRepository
	LedgerRepo                *repositories.LedgerRepository
	ReconciliationRepo        repositories.ReconciliationRepository
	GrowthMailRepo            *repositories.GrowthMailRepository

	// External Services
	AlpacaClient       *alpaca.Client
	AlpacaService      *alpaca.Service
	BridgeClient       *bridge.Client
	BridgeAdapter      *bridge.Adapter
	CircleAdapter      *circleadapter.Adapter
	UmbraClient        *umbra.Client
	UmbraWalletService *umbrawallet.Service
	EmailService       *adapters.EmailService
	SMSService         *adapters.SMSService
	AuditService       *adapters.AuditService
	RedisClient        cache.RedisClient

	// Bridge Domain Adapters
	BridgeKYCAdapter              *BridgeKYCAdapter
	BridgeVirtualAccountService   *funding.BridgeVirtualAccountService
	BridgeWebhookHandler          *handlers.BridgeWebhookHandler
	CircleWebhookHandler          *webhooks.CircleWebhookHandler
	BridgeCustomerStatusProcessor *webhooks.BridgeCustomerStatusProcessor

	// Domain Services
	OnboardingService          *onboarding.Service
	OnboardingJobService       *services.OnboardingJobService
	VerificationService        services.VerificationService
	PasscodeService            *passcode.Service
	SessionService             *session.Service
	TwoFAService               *twofa.Service
	APIKeyService              *apikey.Service
	WalletService              *wallet.Service
	FundingService             *funding.Service
	InvestingService           *investing.Service
	BalanceService             *services.BalanceService
	LedgerService              *ledger.Service
	YieldService               *yieldsvc.Service
	yieldRepo                  *repositories.YieldRepository
	ReconciliationService      *reconciliation.Service
	ReconciliationScheduler    *reconciliation.Scheduler
	StashReconciliation        *recon.Worker
	TreasurySweepWorker        *treasury_sweep.Worker
	ReflectDepositRouter       *reflect.CircleDepositRouter
	YieldDistributionWorker    *yield_distribution.Worker
	AllocationService          *allocation.Service
	AutoInvestService          *autoinvest.Service
	StrategyEngine             *strategy.Engine
	StationService             *station.Service
	GameplayXPService          *gameplay.XPService
	GameplayStreakService      *gameplay.StreakService
	GameplayChallengeService   *gameplay.ChallengeService
	GameplayAchievementService *gameplay.AchievementService
	GameplayRepo               *repositories.GameplayRepository
	GameplayHooks              *gameplay.Hooks
	GameplayRingsService       *gameplay.RingsService
	GameplayBoostService       *gameplay.BoostService
	GameplayPointsService      *gameplay.PointsService
	GameplayGraceDayService    *gameplay.GraceDayService
	GameplayRecapService       *gameplay.RecapService
	SubscriptionService        *subscriptionsvc.Service
	FinancialObligationService *obligationservice.Service
	MoneyGuardService          *moneyguardservice.Service
	MiriamIntelligenceService  *miriamservice.Service
	AutomationService          *automation.Service
	GrowthMailService          *growthmail.Service
	NotificationService        *services.NotificationService
	SocialAuthService          *socialauth.Service
	WebAuthnService            *webauthn.Service
	LimitsService              *limits.Service
	DomainAuditService         *audit.Service
	WithdrawalService          *services.WithdrawalService
	StashLockService           *stashlock.Service

	// AI Financial Manager Services
	AIProviderManager      *ai.ProviderManager
	AIOrchestrator         *aiservice.Orchestrator
	DiditClient            *didit.Client
	ComplianceService      *compliancesvc.Service
	AIRecommender          *aiservice.Recommender
	NewsService            *newsservice.Service
	PortfolioDataProvider  *aiservice.PortfolioDataProviderImpl
	ActivityDataProvider   *aiservice.ActivityDataProviderImpl
	ConversationRepo       *repositories.ConversationRepository
	ConversationService    *conversationsvc.Service
	UsageRepo              *repositories.AIUsageRepository
	UsageService           *usagesvc.Service
	EmbeddingsClient       *embeddings.Client
	KnowledgeRepo          *repositories.KnowledgeRepository
	KnowledgeService       *knowledgesvc.Service
	MemoryService          *aiservice.MemoryService
	MiriamIntelligenceRepo *repositories.MiriamIntelligenceRepository

	// Additional Repositories
	OnboardingJobRepo *repositories.OnboardingJobRepository

	// Alpaca Investment Repositories
	AlpacaAccountRepo        *repositories.AlpacaAccountRepository
	InvestmentOrderRepo      *repositories.InvestmentOrderRepository
	InvestmentPositionRepo   *repositories.InvestmentPositionRepository
	AlpacaEventRepo          *repositories.AlpacaEventRepository
	AlpacaInstantFundingRepo *repositories.AlpacaInstantFundingRepository

	// Advanced Features Repositories
	PortfolioSnapshotRepo   *repositories.PortfolioSnapshotRepository
	ScheduledInvestmentRepo *repositories.ScheduledInvestmentRepository
	RebalancingConfigRepo   *repositories.RebalancingConfigRepository
	InvestmentRulesRepo     *repositories.InvestmentRulesRepository
	MarketAlertRepo         *repositories.MarketAlertRepository

	// Alpaca Investment Services
	AlpacaAccountService *alpacaservice.AccountService
	AlpacaFundingBridge  *alpacaservice.FundingBridge
	AlpacaEventProcessor *alpacaservice.EventProcessor
	AlpacaPortfolioSync  *alpacaservice.PortfolioSyncService

	// Advanced Features Services
	PortfolioAnalyticsService  *analyticsservice.PortfolioAnalyticsService
	MarketDataService          *marketservice.MarketDataService
	ScheduledInvestmentService *investing.ScheduledInvestmentService
	RebalancingService         *investing.RebalancingService

	// Brokerage Adapter
	BrokerageAdapter *adapters.BrokerageAdapter

	// Round-up Services
	RoundupRepo    *repositories.RoundupRepository
	RoundupService *roundup.Service

	// Copy Trading Services
	CopyTradingRepo    *repositories.CopyTradingRepository
	CopyTradingService *copytrading.Service

	// Card Services
	CardRepo    *repositories.CardRepository
	CardService *card.Service

	// Workers
	WalletProvisioningScheduler interface{} // Type interface{} to avoid circular dependency, will be set at runtime
	FundingWebhookManager       interface{} // Type interface{} to avoid circular dependency, will be set at runtime

	// Cache & Queue
	CacheInvalidator *cache.CacheInvalidator
	JobQueue         interface{} // Job queue for background processing
	JobScheduler     interface{} // Job scheduler for cron jobs

	// Security Services
	LoginProtectionService    *security.LoginProtectionService
	DeviceTrackingService     *security.DeviceTrackingService
	WithdrawalSecurityService *security.WithdrawalSecurityService
	IPWhitelistService        *security.IPWhitelistService
	PasswordPolicyService     *security.PasswordPolicyService
	SecurityEventLogger       *security.SecurityEventLogger
	PasswordService           *security.PasswordService

	// Enhanced Security Services (MFA, Geo, Fraud, Incident Response)
	MFAService              *security.MFAService
	GeoSecurityService      *security.GeoSecurityService
	FraudDetectionService   *security.FraudDetectionService
	IncidentResponseService *security.IncidentResponseService
	OnboardingFraudService  *security.OnboardingFraudService

	// Token and Rate Limiting
	TokenBlacklist      *auth.TokenBlacklist
	JWTService          *auth.JWTService
	TieredRateLimiter   *ratelimit.TieredLimiter
	LoginAttemptTracker *ratelimit.LoginAttemptTracker
	CaptchaVerifier     *captcha.Verifier

	// Device-Bound JWT (Priority 1)
	DeviceSessionRepo      *repositories.DeviceSessionRepository
	DeviceBindingAuditRepo *repositories.DeviceBindingAuditRepository
	DeviceBoundJWTService  *auth.DeviceBoundJWTService

	// Adaptive Rate Limiting (Priority 3)
	RiskScoringEngine   *ratelimit.RiskScoringEngine
	AdaptiveRateLimiter *ratelimit.AdaptiveRateLimiter

	// Instant Funding Services
	InstantFundingRepo     *repositories.InstantFundingRepository
	UserAccountRepo        *repositories.UserAccountRepository
	InstantFundingService  *funding.InstantFundingService
	InstantFundingHandlers *fundinghandlers.InstantFundingHandlers
	ChainRailsHandlers     *fundinghandlers.ChainRailsHandlers
	ChainRailsClient       *chainrails.Client
	DepositSweepRepo       *repositories.DepositSweepRepository
	PajHandlers            *fundinghandlers.PajHandlers
	ActivityHandlers       *activityhandlers.Handlers

	// Security Stores
	WithdrawalSecurityStore *repositories.WithdrawalSecurityStore
	DepositSecurityStore    *repositories.DepositSecurityStore

	// Account Management
	AccountDeletionService *account.DeletionService

	// P2P Transfer Services
	P2PRepo               *repositories.P2PRepository
	P2PService            *p2p.Service
	P2PNotificationSender *adapters.P2PNotificationSender
	P2PHandlers           *p2phandlers.Handlers

	// Notification Services
	DeviceTokenRepo  *repositories.DeviceTokenRepository
	NotificationRepo *repositories.NotificationRepository
	ExpoPushService  *adapters.ExpoPushService
	SNSPushService   *adapters.SNSPushService

	// Unified Webhook Handler
	UnifiedFundingWebhookHandler *webhooks.UnifiedFundingWebhookHandler

	// Security Features (v2) - Risk Scoring, Whitelist, Anomaly, Limits, Adaptive MFA
	SecurityFeaturesRepo    *repositories.SecurityFeaturesRepository
	RiskScoringService      *security.RiskScoringService
	AddressWhitelistService *security.AddressWhitelistService
	SessionAnomalyService   *security.SessionAnomalyService
	WithdrawalLimitsService *security.WithdrawalLimitsService
	AdaptiveMFAService      *security.AdaptiveMFAService
	DeviceSecurityService   *security.DeviceSecurityService

	// Premium Feature Repositories
	FamilySupportRepo *repositories.FamilySupportRepository
	ScamRepo          *repositories.ScamRepository
	TaxResidencyRepo  *repositories.TaxResidencyRepository
	WellnessRepo      *repositories.WellnessRepository
	EmergencyRepo     *repositories.EmergencyRepository
	ExchangeRateRepo  *repositories.ExchangeRateRepository
	VisaProofRepo     *repositories.VisaProofRepository
	ReceiptSplitRepo  *repositories.ReceiptSplitRepository

	// Premium Feature Services
	NairaShieldService      *premium.NairaShieldService
	BlackTaxService         *premium.BlackTaxService
	ReceiptSplitService     *premium.ReceiptSplitService
	ScamIntelligenceService *premium.ScamIntelligenceService
	TaxResidencyService     *premium.TaxResidencyService
	IncomeSmoothingService  *premium.IncomeSmoothingService
	FinancialTraumaService  *premium.FinancialTraumaService
	VisaProofService        *premium.VisaProofService
	PanicButtonService      *premium.PanicButtonService
	PremiumHandlers         *premiumhandlers.Handlers

	// Opportunity Intelligence
	OpportunityRepo    *repositories.OpportunityRepository
	OpportunityService *opportunitysvc.Service

	// Waitlist
	WaitlistRepo    *repositories.WaitlistRepository
	WaitlistService *waitlistsvc.Service
}

// NewContainer creates a new dependency injection container
func NewContainer(cfg *config.Config, db *sql.DB, log *logger.Logger) (*Container, error) {
	zapLog := log.Zap()

	// Wrap sql.DB with sqlx for repositories that need it
	sqlxDB := sqlx.NewDb(db, "postgres")

	// Initialize repositories
	userRepo := repositories.NewUserRepository(db, zapLog)
	onboardingFlowRepo := repositories.NewOnboardingFlowRepository(db, zapLog)
	kycSubmissionRepo := repositories.NewKYCSubmissionRepository(db, zapLog)
	walletRepo := repositories.NewWalletRepository(db, zapLog)
	walletSetRepo := repositories.NewWalletSetRepository(db, zapLog)
	walletProvisioningJobRepo := repositories.NewWalletProvisioningJobRepository(db, zapLog)
	depositRepo := repositories.NewDepositRepository(sqlxDB)
	withdrawalRepo := repositories.NewWithdrawalRepository(sqlxDB)
	receiptRepo := repositories.NewReceiptRepository(sqlxDB)
	budgetRepo := repositories.NewBudgetRepository(sqlxDB)
	financialProfileRepo := repositories.NewFinancialProfileRepository(sqlxDB)
	financialObligationRepo := repositories.NewFinancialObligationRepository(sqlxDB)
	automationRepo := repositories.NewAutomationRepository(sqlxDB)
	ledgerSpendingRepo := repositories.NewLedgerSpendingRepository(sqlxDB)
	conversionRepo := repositories.NewConversionRepository(sqlxDB)
	balanceRepo := repositories.NewBalanceRepository(db, zapLog)
	fundingEventJobRepo := repositories.NewFundingEventJobRepository(db, log)
	sumsubWebhookEventRepo := repositories.NewSumsubWebhookEventRepository(db, zapLog)
	kycSyncJobRepo := repositories.NewKYCSyncJobRepository(db, zapLog)
	ledgerRepo := repositories.NewLedgerRepository(sqlxDB)
	reconciliationRepo := repositories.NewPostgresReconciliationRepository(db)
	onboardingJobRepo := repositories.NewOnboardingJobRepository(db, zapLog)
	growthMailRepo := repositories.NewGrowthMailRepository(db)

	// Initialize premium feature repositories
	familySupportRepo := repositories.NewFamilySupportRepository(sqlxDB)
	scamRepo := repositories.NewScamRepository(sqlxDB)
	taxResidencyRepo := repositories.NewTaxResidencyRepository(sqlxDB)
	wellnessRepo := repositories.NewWellnessRepository(sqlxDB)
	emergencyRepo := repositories.NewEmergencyRepository(sqlxDB)
	exchangeRateRepo := repositories.NewExchangeRateRepository(sqlxDB)
	visaProofRepo := repositories.NewVisaProofRepository(sqlxDB)
	receiptSplitRepo := repositories.NewReceiptSplitRepository(sqlxDB)

	// Initialize external services
	// Initialize Alpaca service
	alpacaConfig := alpaca.Config{
		ClientID:      cfg.Alpaca.ClientID,
		SecretKey:     cfg.Alpaca.SecretKey,
		BaseURL:       cfg.Alpaca.BaseURL,
		DataBaseURL:   cfg.Alpaca.DataBaseURL,
		DataAPIKey:    cfg.Alpaca.DataAPIKey,
		DataAPISecret: cfg.Alpaca.DataAPISecret,
		DataFeed:      cfg.Alpaca.DataFeed,
		Environment:   cfg.Alpaca.Environment,
		Timeout:       time.Duration(cfg.Alpaca.Timeout) * time.Second,
	}
	alpacaClient := alpaca.NewClient(alpacaConfig, zapLog)
	alpacaService := alpaca.NewService(alpacaClient, zapLog)

	// Initialize Bridge service
	bridgeConfig := bridge.Config{
		APIKey:      cfg.Bridge.APIKey,
		BaseURL:     cfg.Bridge.BaseURL,
		Environment: cfg.Bridge.Environment,
		Timeout:     time.Duration(cfg.Bridge.Timeout) * time.Second,
		MaxRetries:  cfg.Bridge.MaxRetries,
	}
	bridgeClient := bridge.NewClient(bridgeConfig, zapLog)
	bridgeAdapter := bridge.NewAdapter(bridgeClient, zapLog)

	// Initialize Circle Programmable Wallets client
	var circleAdapter *circleadapter.Adapter
	if cfg.Circle.APIKey != "" && cfg.Circle.EntitySecret != "" {
		circleClient, circleErr := circleadapter.NewHTTPClient(circleadapter.Config{
			APIKey:             cfg.Circle.APIKey,
			EntitySecret:       cfg.Circle.EntitySecret,
			PublicKeyPEM:       cfg.Circle.PublicKeyPEM,
			BaseURL:            cfg.Circle.BaseURL,
			Timeout:            time.Duration(cfg.Circle.Timeout) * time.Second,
			MaxRetries:         cfg.Circle.MaxRetries,
			DefaultWalletSetID: cfg.Circle.DefaultWalletSetID,
		}, zapLog)
		if circleErr != nil {
			zapLog.Warn("Circle client init failed, wallet features degraded", zap.Error(circleErr))
		} else {
			isSandbox := cfg.Circle.Environment == "sandbox"
			if isSandbox {
				circleAdapter = circleadapter.NewSandboxAdapter(circleClient, zapLog)
			} else {
				circleAdapter = circleadapter.NewAdapter(circleClient, zapLog)
			}
			zapLog.Info("Circle Programmable Wallets initialized", zap.Bool("sandbox", isSandbox))
		}
	} else {
		zapLog.Info("Circle Programmable Wallets not configured (missing API key or entity secret)")
	}

	// Initialize Umbra privacy sidecar client
	var umbraClient *umbra.Client
	if cfg.Umbra.Enabled && cfg.Umbra.SidecarURL != "" {
		umbraClient = umbra.NewClient(cfg.Umbra.SidecarURL, cfg.Umbra.AuthToken)
		zapLog.Info("Umbra privacy sidecar enabled", zap.String("url", cfg.Umbra.SidecarURL), zap.String("network", cfg.Umbra.Network))
	} else {
		zapLog.Info("Umbra privacy sidecar disabled")
	}

	// Initialize email service with Unosend configuration
	var err error
	emailServiceConfig := adapters.EmailServiceConfig{
		Provider:    cfg.Email.Provider,
		APIKey:      cfg.Email.APIKey,
		FromEmail:   cfg.Email.FromEmail,
		FromName:    cfg.Email.FromName,
		Environment: cfg.Email.Environment,
		BaseURL:     cfg.Email.BaseURL,
		ReplyTo:     cfg.Email.ReplyTo,
	}
	var emailService *adapters.EmailService
	if strings.TrimSpace(cfg.Email.Provider) != "" {
		emailService, err = adapters.NewEmailService(zapLog, emailServiceConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize email service: %w", err)
		}
	} else {
		zapLog.Warn("Email provider not configured; email notifications disabled")
	}

	// Initialize SMS service
	var smsService *adapters.SMSService
	if strings.TrimSpace(cfg.SMS.Provider) != "" {
		smsService, err = adapters.NewSMSService(zapLog, adapters.SMSConfig{
			Provider:    cfg.SMS.Provider,
			APIKey:      cfg.SMS.APIKey,
			APISecret:   cfg.SMS.APISecret,
			FromNumber:  cfg.SMS.FromNumber,
			Environment: cfg.SMS.Environment,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to initialize SMS service: %w", err)
		}
	} else {
		zapLog.Warn("SMS provider not configured; SMS notifications disabled")
	}

	// Initialize Redis client
	redisClient, err := cache.NewRedisClient(&cfg.Redis, zapLog)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Redis client: %w", err)
	}

	auditService := adapters.NewAuditService(db, zapLog)

	// Initialize cache invalidator
	cacheInvalidator := cache.NewCacheInvalidator(redisClient, zapLog, cache.InvalidateImmediate)

	container := &Container{
		Config: cfg,
		DB:     db,
		Logger: log,
		ZapLog: zapLog,

		// Repositories
		UserRepo:                  userRepo,
		OnboardingFlowRepo:        onboardingFlowRepo,
		KYCSubmissionRepo:         kycSubmissionRepo,
		WalletRepo:                walletRepo,
		WalletSetRepo:             walletSetRepo,
		WalletProvisioningJobRepo: walletProvisioningJobRepo,
		DepositRepo:               depositRepo,
		WithdrawalRepo:            withdrawalRepo,
		ReceiptRepo:               receiptRepo,
		BudgetRepo:                budgetRepo,
		FinancialProfileRepo:      financialProfileRepo,
		FinancialObligationRepo:   financialObligationRepo,
		AutomationRepo:            automationRepo,
		LedgerSpendingRepo:        ledgerSpendingRepo,
		ConversionRepo:            conversionRepo,
		BalanceRepo:               balanceRepo,
		FundingEventJobRepo:       fundingEventJobRepo,
		SumsubWebhookEventRepo:    sumsubWebhookEventRepo,
		KYCSyncJobRepo:            kycSyncJobRepo,
		LedgerRepo:                ledgerRepo,
		ReconciliationRepo:        reconciliationRepo,
		OnboardingJobRepo:         onboardingJobRepo,
		GrowthMailRepo:            growthMailRepo,
		FamilySupportRepo:         familySupportRepo,
		ScamRepo:                  scamRepo,
		TaxResidencyRepo:          taxResidencyRepo,
		WellnessRepo:              wellnessRepo,
		EmergencyRepo:             emergencyRepo,
		ExchangeRateRepo:          exchangeRateRepo,
		VisaProofRepo:             visaProofRepo,
		ReceiptSplitRepo:          receiptSplitRepo,
		yieldRepo:                 repositories.NewYieldRepository(sqlxDB),
		DeviceTokenRepo:           repositories.NewDeviceTokenRepository(db),
		NotificationRepo:          repositories.NewNotificationRepository(db),

		// External Services
		AlpacaClient:  alpacaClient,
		AlpacaService: alpacaService,
		BridgeClient:  bridgeClient,
		BridgeAdapter: bridgeAdapter,
		CircleAdapter: circleAdapter,
		UmbraClient:   umbraClient,
		EmailService:  emailService,
		SMSService:    smsService,
		AuditService:  auditService,
		RedisClient:   redisClient,

		// Bridge Domain Adapters
		BridgeKYCAdapter: NewBridgeKYCAdapter(bridgeAdapter, userRepo),

		// Cache & Queue
		CacheInvalidator: cacheInvalidator,
	}

	// Initialize Bridge virtual account service and webhook handler
	container.initializeBridgeServices()

	// Initialize domain services with their dependencies
	if err := container.initializeDomainServices(); err != nil {
		return nil, fmt.Errorf("failed to initialize domain services: %w", err)
	}

	// Initialize verification and onboarding job services
	container.VerificationService = services.NewVerificationService(
		container.RedisClient,
		container.EmailService,
		container.SMSService,
		container.ZapLog,
		container.Config,
	)

	container.OnboardingJobService = services.NewOnboardingJobService(container.OnboardingJobRepo, container.ZapLog, convertWalletChains(cfg.Bridge.SupportedChains, container.ZapLog))

	if container.EmailService != nil {
		container.GrowthMailService = growthmail.NewService(
			container.GrowthMailRepo,
			container.EmailService,
			growthmail.Config{BaseURL: cfg.Email.BaseURL, Limit: 500},
			container.ZapLog,
		)
	}

	// Initialize opportunity intelligence
	container.initializeOpportunityService(sqlxDB)

	// Initialize waitlist
	container.WaitlistRepo = repositories.NewWaitlistRepository(db, zapLog)
	container.WaitlistService = waitlistsvc.NewService(container.WaitlistRepo, zapLog)

	return container, nil
}

// initializeDomainServices initializes all domain services with their dependencies
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

	// Initialize ledger service
	c.LedgerService = ledger.NewService(c.LedgerRepo, sqlxDB, c.Logger)
	c.FinancialObligationService = obligationservice.NewService(c.FinancialObligationRepo)
	automationAdapter := &fundsTransfererAdapter{ledger: c.LedgerService}
	c.AutomationService = automation.NewService(c.AutomationRepo, automationAdapter, automationAdapter, c.ZapLog)

	// Wire optional automation dependencies
	if c.NotificationService != nil {
		c.AutomationService.SetNotificationSender(&automationNotificationAdapter{svc: c.NotificationService, logger: c.ZapLog})
	}
	if c.FinancialObligationService != nil {
		c.AutomationService.SetObligationProvider(c.FinancialObligationService)
	}
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
	c.MiriamIntelligenceService = miriamservice.NewService(
		c.MiriamIntelligenceRepo,
		c.LedgerService,
		moneyGuardSpendingSvc,
		c.FinancialObligationService,
		c.FinancialProfileRepo,
		c.MoneyGuardService,
		c.LedgerService,
		c.NotificationService,
		c.ZapLog,
	)

	// Initialize yield service (Reflect-backed). A private key is only needed for
	// treasury-owned sweeps; Circle-backed deposit routes use user Circle wallets
	// to sign Reflect mint transactions.
	var reflectClient *reflect.Client
	if c.Config.Reflect.SolanaRPC != "" {
		var err error
		reflectClient, err = reflect.NewClient(
			c.Config.Reflect.BaseURL,
			c.Config.Reflect.APIKey,
			c.Config.Reflect.SolanaRPC,
			c.Config.Reflect.OwnerWallet,
			c.Config.Reflect.PrivateKey,
			c.Config.Reflect.StablecoinIndex,
			c.ZapLog,
		)
		if err != nil {
			return fmt.Errorf("failed to create reflect client: %w", err)
		}
		reflectClient.SetAllowedProgramIDs(c.Config.Reflect.AllowedProgramIDs)
		rewardsAdapter := &reflectRewardsAdapter{client: reflectClient, db: sqlxDB}

		if c.Config.Reflect.EnableTreasurySweep {
			minSweep, err := decimal.NewFromString(c.Config.Reflect.MinSweepAmount)
			if err != nil {
				return fmt.Errorf("invalid reflect.min_sweep_amount %q: %w", c.Config.Reflect.MinSweepAmount, err)
			}
			if minSweep.IsZero() {
				minSweep = decimal.NewFromFloat(0.01)
			}
			interval := time.Duration(c.Config.Reflect.SweepInterval) * time.Minute
			if interval == 0 {
				interval = 10 * time.Minute
			}
			if c.CircleAdapter == nil || c.Config.Reflect.CircleSourceWalletID == "" || c.Config.Reflect.PrivateKey == "" || c.Config.Reflect.OwnerWallet == "" {
				c.ZapLog.Warn("Circle treasury sweep enabled but not fully configured; user-wallet Reflect routing remains primary",
					zap.Bool("circle_adapter_configured", c.CircleAdapter != nil),
					zap.Bool("circle_source_wallet_configured", c.Config.Reflect.CircleSourceWalletID != ""),
					zap.Bool("reflect_owner_wallet_configured", c.Config.Reflect.OwnerWallet != ""),
					zap.Bool("reflect_private_key_configured", c.Config.Reflect.PrivateKey != ""))
			} else {
				sweepWorker := treasury_sweep.NewWorker(
					reflectClient,
					c.CircleAdapter,
					c.LedgerRepo,
					c.yieldRepo,
					sqlxDB,
					c.Config.Reflect.CircleSourceWalletID,
					c.Config.Reflect.OwnerWallet,
					minSweep,
					interval,
					c.ZapLog,
				)
				sweepWorker.Start()
				c.TreasurySweepWorker = sweepWorker
				c.ZapLog.Info("Circle treasury sweep started",
					zap.String("min_sweep_amount", minSweep.String()),
					zap.Duration("interval", interval),
					zap.String("circle_source_wallet_id", c.Config.Reflect.CircleSourceWalletID),
					zap.String("reflect_owner_wallet", c.Config.Reflect.OwnerWallet))
			}
		} else {
			c.ZapLog.Info("Reflect treasury sweep disabled; user Circle wallet mint/burn routing is primary")
		}

		c.YieldService = yieldsvc.NewService(c.yieldRepo, c.LedgerService, c.ZapLog)

		// Stash reconciliation: daily check that ledger stash total matches Reflect deposited value.
		if c.Config.Reflect.OwnerWallet != "" {
			reconAdapter := &reflectReconciliationAdapter{db: sqlxDB}
			c.StashReconciliation = recon.NewWorker(
				c.LedgerRepo,
				reconAdapter,
				c.yieldRepo,
				c.Config.Reflect.OwnerWallet,
				c.Config.Reflect.OwnerWallet,
				c.ZapLog,
			)
		}

		// Yield distribution worker — wired with injected rate functions to avoid di→worker circular dep.
		c.YieldDistributionWorker = yield_distribution.NewWorker(
			c.YieldService,
			rewardsAdapter,
			func(ctx context.Context) (decimal.Decimal, error) {
				return reflectClient.GetExchangeRate(ctx)
			},
			func(ctx context.Context, db *sqlx.DB, rate decimal.Decimal, distributedYield decimal.Decimal) error {
				return AdvanceExchangeRateMark(ctx, db, rate, distributedYield)
			},
			sqlxDB,
			c.ZapLog,
		)
	} else {
		c.ZapLog.Warn("Reflect Solana RPC not configured; yield routing disabled")
		c.YieldService = yieldsvc.NewService(c.yieldRepo, c.LedgerService, c.ZapLog)
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
	if reflectClient != nil && c.CircleAdapter != nil {
		c.ReflectDepositRouter = reflect.NewCircleDepositRouter(
			sqlxDB,
			c.CircleAdapter,
			reflectClient,
			c.Config.Reflect.OwnerWallet,
			c.Config.Reflect.AllowedProgramIDs,
			c.ZapLog,
		)
		c.ReflectDepositRouter.SetYieldLedger(&reflectFeeLedgerAdapter{ledger: c.LedgerService})
		c.ReflectDepositRouter.SetSolanaFeeFunding(c.Config.Circle.DefaultWalletSetID, c.Config.Circle.TreasuryWalletAddress)
		schemaReady, schemaErr := c.ReflectDepositRouter.RequiredSchemaAvailable(context.Background())
		if schemaErr != nil {
			c.ZapLog.Warn("Circle-backed user-wallet Reflect deposit router disabled because schema check failed", zap.Error(schemaErr))
		} else if !schemaReady {
			c.ZapLog.Warn("Circle-backed user-wallet Reflect deposit router disabled because required tables are missing; apply migration 189 before enabling it")
		} else {
			if err := c.ReflectDepositRouter.Start(); err != nil {
				c.ZapLog.Error("Failed to start Circle-backed user-wallet Reflect deposit router", zap.Error(err))
			} else {
				c.AllocationService.SetYieldRouter(c.ReflectDepositRouter)
				c.ZapLog.Info("Circle-backed user-wallet Reflect deposit router started")
			}
		}
	} else if reflectClient != nil {
		c.ZapLog.Warn("Circle-backed Reflect deposit router disabled",
			zap.Bool("circle_configured", c.CircleAdapter != nil))
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
	// Wire push notification service (SNS preferred, Expo fallback)
	c.ZapLog.Info("SNS push config check",
		zap.String("ios_arn", c.Config.SNSPush.IOSPlatformARN),
		zap.String("android_arn", c.Config.SNSPush.AndroidPlatformARN),
		zap.String("region", c.Config.SNSPush.Region))
	if c.Config.SNSPush.IOSPlatformARN != "" || c.Config.SNSPush.AndroidPlatformARN != "" {
		region := c.Config.SNSPush.Region
		if region == "" {
			region = "us-east-1" // default
		}
		snsPushSvc, err := adapters.NewSNSPushService(context.Background(), adapters.SNSPushConfig{
			Region:             region,
			IOSPlatformARN:     c.Config.SNSPush.IOSPlatformARN,
			AndroidPlatformARN: c.Config.SNSPush.AndroidPlatformARN,
		}, c.DeviceTokenRepo, c.ZapLog)
		if err != nil {
			c.Logger.Warn("Failed to init SNS push, falling back to Expo", err)
			expoPushService := adapters.NewExpoPushService(c.DeviceTokenRepo, c.ZapLog)
			c.ExpoPushService = expoPushService
			c.NotificationService.SetPushSender(expoPushService)
		} else {
			c.SNSPushService = snsPushSvc
			c.NotificationService.SetPushSender(snsPushSvc)
			c.Logger.Info("SNS push service initialized",
				zap.Bool("ios", c.Config.SNSPush.IOSPlatformARN != ""),
				zap.Bool("android", c.Config.SNSPush.AndroidPlatformARN != ""))
		}
	} else {
		expoPushService := adapters.NewExpoPushService(c.DeviceTokenRepo, c.ZapLog)
		c.ExpoPushService = expoPushService
		c.NotificationService.SetPushSender(expoPushService)
	}
	// Wire email notifications for important events
	if c.EmailService != nil {
		c.NotificationService.SetEmailSender(adapters.NewEmailSenderAdapter(c.EmailService))
	}
	c.NotificationService.SetUserEmailLookup(adapters.NewUserEmailLookup(c.UserRepo))

	// Wire push notifier into gameplay services (now that push provider is resolved)
	// Use SNS if available, otherwise Expo
	var pushNotifier gameplay.PushNotifier
	if c.SNSPushService != nil {
		pushNotifier = c.SNSPushService
	} else if c.ExpoPushService != nil {
		pushNotifier = c.ExpoPushService
	}
	if pushNotifier != nil {
		c.GameplayXPService.SetNotifier(pushNotifier)
		c.GameplayChallengeService.SetNotifier(pushNotifier)
		c.GameplayAchievementService.SetNotifier(pushNotifier)
		c.SubscriptionService.SetNotifier(pushNotifier)
		c.GameplayBoostService.SetNotifier(pushNotifier)
		c.GameplayGraceDayService.SetNotifier(pushNotifier)
	}

	// Wire Bridge transfer into subscription service for fee collection
	if c.BridgeClient != nil && c.Config.Bridge.TreasuryWalletAddress != "" && c.WalletRepo != nil {
		c.SubscriptionService.SetBridgeTransfer(&SubscriptionBridgeTransferAdapter{
			bridgeClient:      c.BridgeClient,
			walletRepo:        c.WalletRepo,
			userRepo:          c.UserRepo,
			companyWalletAddr: c.Config.Bridge.TreasuryWalletAddress,
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

	// Wire Circle crypto transfer adapter
	if c.CircleAdapter != nil {
		c.WithdrawalService.SetCircleTransferAdapter(c.CircleAdapter)
	}
	if c.ReflectDepositRouter != nil {
		c.WithdrawalService.SetStashYieldRedeemer(c.ReflectDepositRouter)
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
		c.Config.Bridge.TreasuryWalletAddress,
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
	if c.BridgeClient != nil {
		c.P2PService.SetBridgeOfframp(NewP2PBridgeOfframpAdapter(bridge.NewAdapter(c.BridgeClient, c.ZapLog)))
	}
	c.P2PHandlers = p2phandlers.NewHandlers(c.P2PService, c.ZapLog)

	// Wire P2P service to onboarding for auto-claim
	c.OnboardingService.SetP2PService(c.P2PService)

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

// GetOnboardingService returns the onboarding service
func (c *Container) GetOnboardingService() *onboarding.Service {
	return c.OnboardingService
}

// GetPasscodeService returns the passcode service
func (c *Container) GetPasscodeService() *passcode.Service {
	return c.PasscodeService
}

// GetSessionService returns the session service
func (c *Container) GetSessionService() *session.Service {
	return c.SessionService
}

// GetTwoFAService returns the 2FA service
func (c *Container) GetTwoFAService() *twofa.Service {
	return c.TwoFAService
}

// GetSocialAuthService returns the social auth service
func (c *Container) GetSocialAuthService() *socialauth.Service {
	return c.SocialAuthService
}

// GetWebAuthnService returns the WebAuthn service
func (c *Container) GetWebAuthnService() *webauthn.Service {
	return c.WebAuthnService
}

// GetAPIKeyService returns the API key service
func (c *Container) GetAPIKeyService() *apikey.Service {
	return c.APIKeyService
}

// GetWalletService returns the wallet service
func (c *Container) GetWalletService() *wallet.Service {
	return c.WalletService
}

// GetFundingService returns the funding service
func (c *Container) GetFundingService() *funding.Service {
	return c.FundingService
}

// GetWithdrawalService returns the withdrawal service
func (c *Container) GetWithdrawalService() *services.WithdrawalService {
	return c.WithdrawalService
}

// GetInvestingService returns the investing service
func (c *Container) GetInvestingService() *investing.Service {
	return c.InvestingService
}

// GetBalanceService returns the Balance service
func (c *Container) GetBalanceService() *services.BalanceService {
	return c.BalanceService
}

// GetLedgerService returns the Ledger service
func (c *Container) GetLedgerService() *ledger.Service {
	return c.LedgerService
}

// GetVerificationService returns the verification service
func (c *Container) GetVerificationService() services.VerificationService {
	return c.VerificationService
}

// GetOnboardingJobService returns the onboarding job service
func (c *Container) GetOnboardingJobService() *services.OnboardingJobService {
	return c.OnboardingJobService
}

// GetAllocationService returns the allocation service
func (c *Container) GetAllocationService() *allocation.Service {
	return c.AllocationService
}

// GetAutoInvestService returns the auto-invest service
func (c *Container) GetAutoInvestService() *autoinvest.Service {
	return c.AutoInvestService
}

// GetLimitsService returns the limits service
func (c *Container) GetLimitsService() *limits.Service {
	return c.LimitsService
}

// GetLimitsHandler returns a new limits handler
func (c *Container) GetLimitsHandler() *handlers.LimitsHandler {
	if c.LimitsService == nil {
		return nil
	}
	return handlers.NewLimitsHandler(c.LimitsService, c.Logger)
}

// GetLoginProtectionService returns the login protection service
func (c *Container) GetLoginProtectionService() *security.LoginProtectionService {
	return c.LoginProtectionService
}

// GetDeviceTrackingService returns the device tracking service
func (c *Container) GetDeviceTrackingService() *security.DeviceTrackingService {
	return c.DeviceTrackingService
}

// GetWithdrawalSecurityService returns the withdrawal security service
func (c *Container) GetWithdrawalSecurityService() *security.WithdrawalSecurityService {
	return c.WithdrawalSecurityService
}

// GetIPWhitelistService returns the IP whitelist service
func (c *Container) GetIPWhitelistService() *security.IPWhitelistService {
	return c.IPWhitelistService
}

// GetPasswordPolicyService returns the password policy service
func (c *Container) GetPasswordPolicyService() *security.PasswordPolicyService {
	return c.PasswordPolicyService
}

// GetSecurityEventLogger returns the security event logger
func (c *Container) GetSecurityEventLogger() *security.SecurityEventLogger {
	return c.SecurityEventLogger
}

// GetPasswordService returns the enhanced password service
func (c *Container) GetPasswordService() *security.PasswordService {
	return c.PasswordService
}

// GetMFAService returns the unified MFA service
func (c *Container) GetMFAService() *security.MFAService {
	return c.MFAService
}

// GetGeoSecurityService returns the geo security service
func (c *Container) GetGeoSecurityService() *security.GeoSecurityService {
	return c.GeoSecurityService
}

// GetFraudDetectionService returns the fraud detection service
func (c *Container) GetFraudDetectionService() *security.FraudDetectionService {
	return c.FraudDetectionService
}

// GetIncidentResponseService returns the incident response service
func (c *Container) GetIncidentResponseService() *security.IncidentResponseService {
	return c.IncidentResponseService
}

// GetOnboardingFraudService returns the onboarding fraud detection service
func (c *Container) GetOnboardingFraudService() *security.OnboardingFraudService {
	return c.OnboardingFraudService
}

// GetSecurityFeaturesRepo returns the security features repository
func (c *Container) GetSecurityFeaturesRepo() *repositories.SecurityFeaturesRepository {
	return c.SecurityFeaturesRepo
}

// GetRiskScoringService returns the transaction risk scoring service
func (c *Container) GetRiskScoringService() *security.RiskScoringService {
	return c.RiskScoringService
}

// GetAddressWhitelistService returns the address whitelist service
func (c *Container) GetAddressWhitelistService() *security.AddressWhitelistService {
	return c.AddressWhitelistService
}

// GetSessionAnomalyService returns the session anomaly detection service
func (c *Container) GetSessionAnomalyService() *security.SessionAnomalyService {
	return c.SessionAnomalyService
}

// GetWithdrawalLimitsService returns the tiered withdrawal limits service
func (c *Container) GetWithdrawalLimitsService() *security.WithdrawalLimitsService {
	return c.WithdrawalLimitsService
}

// GetAdaptiveMFAService returns the adaptive MFA service
func (c *Container) GetAdaptiveMFAService() *security.AdaptiveMFAService {
	return c.AdaptiveMFAService
}

// GetDeviceSecurityService returns the device security service
func (c *Container) GetDeviceSecurityService() *security.DeviceSecurityService {
	return c.DeviceSecurityService
}

// GetTokenBlacklist returns the token blacklist service
func (c *Container) GetTokenBlacklist() *auth.TokenBlacklist {
	return c.TokenBlacklist
}

// GetJWTService returns the enhanced JWT service
func (c *Container) GetJWTService() *auth.JWTService {
	return c.JWTService
}

// GetTieredRateLimiter returns the tiered rate limiter
func (c *Container) GetTieredRateLimiter() *ratelimit.TieredLimiter {
	return c.TieredRateLimiter
}

// GetAccountDeletionService returns the account deletion service
func (c *Container) GetAccountDeletionService() *account.DeletionService {
	return c.AccountDeletionService
}

// GetRateLimitConfig returns the rate limit configuration
func (c *Container) GetRateLimitConfig() *config.RateLimitConfig {
	return &c.Config.RateLimit
}

// GetLoginAttemptTracker returns the login attempt tracker
func (c *Container) GetLoginAttemptTracker() *ratelimit.LoginAttemptTracker {
	return c.LoginAttemptTracker
}

// GetCaptchaVerifier returns the CAPTCHA verifier (may be nil if not configured)
func (c *Container) GetCaptchaVerifier() *captcha.Verifier {
	return c.CaptchaVerifier
}

// initializeReconciliationService initializes the reconciliation service and scheduler
func (c *Container) initializeReconciliationService() error {
	// Initialize metrics service (placeholder - extend pkg/metrics/reconciliation_metrics.go)
	metricsService := &reconciliationMetricsService{}

	// Create reconciliation service config
	reconciliationConfig := &reconciliation.Config{
		AutoCorrectLowSeverity: true,
		ToleranceCircle:        decimal.NewFromFloat(10.0),
		ToleranceAlpaca:        decimal.NewFromFloat(100.0),
		EnableAlerting:         true,
		AlertWebhookURL:        c.Config.Reconciliation.AlertWebhookURL,
	}

	// Initialize reconciliation service with all dependencies
	c.ReconciliationService = reconciliation.NewService(
		c.ReconciliationRepo,
		c.LedgerRepo,
		c.DepositRepo,
		c.WithdrawalRepo,
		c.ConversionRepo,
		c.LedgerService,
		&bridgeBalanceAdapter{
			bridgeAdapter: c.BridgeAdapter,
			walletRepo:    c.WalletRepo,
			userRepo:      c.UserRepo,
		},
		&alpacaClientAdapter{
			client:  c.AlpacaClient,
			service: c.AlpacaService,
			db:      c.DB,
		},
		c.Logger,
		metricsService,
		reconciliationConfig,
	)

	// Initialize reconciliation scheduler
	schedulerConfig := &reconciliation.SchedulerConfig{
		HourlyInterval: 1 * time.Hour,
		DailyInterval:  24 * time.Hour,
	}

	c.ReconciliationScheduler = reconciliation.NewScheduler(
		c.ReconciliationService,
		c.Logger,
		schedulerConfig,
	)

	return nil
}

// Adapters for reconciliation service
type bridgeBalanceAdapter struct {
	bridgeAdapter *bridge.Adapter
	walletRepo    *repositories.WalletRepository
	userRepo      *repositories.UserRepository
}

func (a *bridgeBalanceAdapter) GetTotalUSDCBalance(ctx context.Context) (decimal.Decimal, error) {
	filters := repositories.WalletListFilters{
		Status: (*entities.WalletStatus)(ptrOf(entities.WalletStatusLive)),
		Limit:  10000,
		Offset: 0,
	}
	wallets, _, err := a.walletRepo.ListWithFilters(ctx, filters)
	if err != nil {
		return decimal.Zero, fmt.Errorf("failed to list wallets: %w", err)
	}

	total := decimal.Zero
	for _, wallet := range wallets {
		if wallet.BridgeWalletID == "" {
			continue
		}
		user, err := a.userRepo.GetByID(ctx, wallet.UserID)
		if err != nil || user == nil || user.BridgeCustomerID == nil || *user.BridgeCustomerID == "" {
			continue
		}
		wb, err := a.bridgeAdapter.GetWalletBalance(ctx, *user.BridgeCustomerID, wallet.BridgeWalletID)
		if err != nil {
			continue
		}
		if amt, err := decimal.NewFromString(wb.GetUSDCAmount()); err == nil {
			total = total.Add(amt)
		}
	}
	return total, nil
}

type alpacaClientAdapter struct {
	client  *alpaca.Client
	service *alpaca.Service
	db      *sql.DB
}

func (a *alpacaClientAdapter) GetTotalBuyingPower(ctx context.Context) (decimal.Decimal, error) {
	// Query all users from database who have Alpaca accounts
	query := `
		SELECT alpaca_account_id 
		FROM users 
		WHERE alpaca_account_id IS NOT NULL AND alpaca_account_id != '' AND is_active = true
	`

	rows, err := a.db.QueryContext(ctx, query)
	if err != nil {
		return decimal.Zero, fmt.Errorf("failed to query users with Alpaca accounts: %w", err)
	}
	defer rows.Close()

	var accountIDs []string
	for rows.Next() {
		var accountID string
		if err := rows.Scan(&accountID); err != nil {
			continue
		}
		accountIDs = append(accountIDs, accountID)
	}

	// Aggregate buying power from all accounts
	totalBuyingPower := decimal.Zero
	for _, accountID := range accountIDs {
		account, err := a.service.GetAccount(ctx, accountID)
		if err != nil {
			// Log error but continue with other accounts
			continue
		}

		// Add buying power (already decimal.Decimal)
		if !account.BuyingPower.IsZero() {
			totalBuyingPower = totalBuyingPower.Add(account.BuyingPower)
		}
	}

	return totalBuyingPower, nil
}

// Real metrics service using Prometheus metrics from pkg/common/metrics
type reconciliationMetricsService struct{}

func (m *reconciliationMetricsService) RecordReconciliationRun(runType string) {
	// Increment run counter
	commonmetrics.ReconciliationRunsTotal.WithLabelValues(runType, "started").Inc()
	commonmetrics.ReconciliationRunsInProgress.WithLabelValues(runType).Inc()
}

func (m *reconciliationMetricsService) RecordReconciliationCompleted(runType string, totalChecks, passedChecks, failedChecks, exceptionsCount int) {
	// Decrement in-progress counter
	commonmetrics.ReconciliationRunsInProgress.WithLabelValues(runType).Dec()
	// Increment completed counter
	commonmetrics.ReconciliationRunsTotal.WithLabelValues(runType, "completed").Inc()
}

func (m *reconciliationMetricsService) RecordCheckResult(checkType string, passed bool, duration time.Duration) {
	// Record check execution
	commonmetrics.ReconciliationChecksTotal.WithLabelValues(checkType).Inc()
	commonmetrics.ReconciliationCheckDuration.WithLabelValues(checkType).Observe(duration.Seconds())

	if passed {
		commonmetrics.ReconciliationChecksPassed.WithLabelValues(checkType).Inc()
	} else {
		commonmetrics.ReconciliationChecksFailed.WithLabelValues(checkType).Inc()
	}
}

func (m *reconciliationMetricsService) RecordExceptionAutoCorrected(checkType string) {
	// Record auto-corrected exception
	commonmetrics.ReconciliationExceptionsAutoCorrected.WithLabelValues(checkType).Inc()
}

func (m *reconciliationMetricsService) RecordDiscrepancyAmount(checkType string, amount decimal.Decimal) {
	// Record discrepancy amount
	amountFloat, _ := amount.Float64()
	commonmetrics.ReconciliationDiscrepancyAmount.WithLabelValues(checkType, "USD").Set(amountFloat)
}

func (m *reconciliationMetricsService) RecordReconciliationAlert(checkType, severity string) {
	// Record alert sent
	commonmetrics.ReconciliationAlertsTotal.WithLabelValues(checkType, severity).Inc()
}

// Helper function to create pointer to value
func ptrOf[T any](v T) *T {
	return &v
}

func convertWalletChains(raw []string, logger *zap.Logger) []entities.WalletChain {
	if len(raw) == 0 {
		logger.Fatal("bridge.supported_chains not configured - refusing to start with default testnet chain")
		return nil // unreachable; Fatal calls os.Exit
	}

	normalized := make([]entities.WalletChain, 0, len(raw))
	seen := make(map[entities.WalletChain]struct{})

	for _, entry := range raw {
		if strings.TrimSpace(entry) == "" {
			continue
		}

		upper := strings.ToUpper(strings.TrimSpace(entry))
		chain := entities.WalletChain(upper)
		if !chain.IsValid() {
			normalizedKey := strings.NewReplacer("-", "_", " ", "_").Replace(upper)
			switch normalizedKey {
			case "SOLANA", "SOL":
				chain = entities.WalletChainSolana
			case "ETHEREUM", "ETH":
				chain = entities.WalletChainEthereum
			case "POLYGON", "MATIC":
				chain = entities.WalletChainPolygon
			case "CELO":
				chain = entities.WalletChainCelo
			case "BASE":
				chain = entities.WalletChainBase
			case "AVALANCHE", "AVAX":
				chain = entities.WalletChainAvalanche
			case "ARBITRUM", "ARB":
				chain = entities.WalletChainArbitrum
			case "OPTIMISM", "OP":
				chain = entities.WalletChainOptimism
			default:
				logger.Warn("Ignoring unsupported wallet chain from configuration", zap.String("chain", upper))
				continue
			}
		}

		if !chain.IsValid() {
			logger.Warn("Ignoring unsupported wallet chain from configuration", zap.String("chain", string(chain)))
			continue
		}
		if _, ok := seen[chain]; ok {
			continue
		}
		seen[chain] = struct{}{}
		normalized = append(normalized, chain)
	}

	if len(normalized) == 0 {
		logger.Fatal("bridge.supported_chains contained no valid entries - refusing to start with default testnet chain")
		return nil // unreachable; Fatal calls os.Exit
	}

	return normalized
}

// initializeAIServices initializes AI Financial Manager services
func (c *Container) initializeAIServices(sqlxDB *sqlx.DB, positionRepo *repositories.PositionRepository, allocationRepo *repositories.AllocationRepository, basketRepo *repositories.BasketRepository) error {
	// Check if AI is configured
	if c.Config.AI.OpenAI.APIKey == "" && c.Config.AI.Gemini.APIKey == "" && c.Config.AI.Kimi.APIKey == "" && c.Config.AI.Groq.APIKey == "" && c.Config.AI.Bedrock.ModelID == "" {
		return fmt.Errorf("no AI provider configured")
	}

	// Helper to resolve timeout from config with a sensible default
	resolveTimeout := func(seconds int) time.Duration {
		if seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
		return 10 * time.Second
	}

	// Initialize AI providers — Bedrock first (primary), others as fallbacks
	var providers []ai.AIProvider

	// Amazon Bedrock — primary provider, uses AWS IAM credentials (no API key needed)
	if strings.TrimSpace(c.Config.AI.Bedrock.ModelID) != "" {
		bedrockRegion := c.Config.AI.Bedrock.Region
		if bedrockRegion == "" {
			bedrockRegion = "us-east-1"
		}
		awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(), awsconfig.WithRegion(bedrockRegion))
		if err != nil {
			c.ZapLog.Warn("bedrock: failed to load AWS config, skipping", zap.Error(err))
		} else {
			bedrockProvider := ai.NewBedrockProvider(&ai.BedrockConfig{
				AWSConfig:        awsCfg,
				ModelID:          c.Config.AI.Bedrock.ModelID,
				MaxTokens:        c.Config.AI.Bedrock.MaxTokens,
				Temperature:      c.Config.AI.Bedrock.Temperature,
				TopP:             c.Config.AI.Bedrock.TopP,
				GuardrailID:      c.Config.AI.Bedrock.GuardrailID,
				GuardrailVersion: c.Config.AI.Bedrock.GuardrailVersion,
			}, c.ZapLog)
			providers = append(providers, bedrockProvider)
		}
	}

	// Fallback providers (used if Bedrock is unavailable or not configured)
	if strings.TrimSpace(c.Config.AI.Gemini.APIKey) != "" {
		geminiConfig := &ai.ProviderConfig{
			APIKey:       strings.TrimSpace(c.Config.AI.Gemini.APIKey),
			Model:        c.Config.AI.Gemini.Model,
			MaxTokens:    c.Config.AI.Gemini.MaxTokens,
			Temperature:  c.Config.AI.Gemini.Temperature,
			TopP:         c.Config.AI.Gemini.TopP,
			Timeout:      resolveTimeout(c.Config.AI.Gemini.TimeoutSeconds),
			RateLimitRPM: c.Config.AI.Gemini.RateLimitRPM,
		}
		geminiProvider := ai.NewGeminiProvider(geminiConfig, c.ZapLog)
		providers = append(providers, geminiProvider)
	}

	if strings.TrimSpace(c.Config.AI.OpenAI.APIKey) != "" {
		openaiConfig := &ai.ProviderConfig{
			APIKey:       strings.TrimSpace(c.Config.AI.OpenAI.APIKey),
			Model:        c.Config.AI.OpenAI.Model,
			MaxTokens:    c.Config.AI.OpenAI.MaxTokens,
			Temperature:  c.Config.AI.OpenAI.Temperature,
			TopP:         c.Config.AI.OpenAI.TopP,
			Timeout:      resolveTimeout(c.Config.AI.OpenAI.TimeoutSeconds),
			RateLimitRPM: c.Config.AI.OpenAI.RateLimitRPM,
		}
		openaiProvider := ai.NewOpenAIProvider(openaiConfig, c.ZapLog)
		providers = append(providers, openaiProvider)
	}

	// Groq — OpenAI-compatible provider
	if strings.TrimSpace(c.Config.AI.Groq.APIKey) != "" {
		groqConfig := &ai.ProviderConfig{
			APIKey:       strings.TrimSpace(c.Config.AI.Groq.APIKey),
			BaseURL:      "https://api.groq.com/openai/v1",
			Model:        c.Config.AI.Groq.Model,
			MaxTokens:    c.Config.AI.Groq.MaxTokens,
			Temperature:  c.Config.AI.Groq.Temperature,
			TopP:         c.Config.AI.Groq.TopP,
			Timeout:      resolveTimeout(c.Config.AI.Groq.TimeoutSeconds),
			RateLimitRPM: c.Config.AI.Groq.RateLimitRPM,
			ProviderName: "groq",
		}
		groqProvider := ai.NewOpenAIProvider(groqConfig, c.ZapLog)
		providers = append(providers, groqProvider)
	}

	// Kimi (Moonshot) — OpenAI-compatible provider
	if strings.TrimSpace(c.Config.AI.Kimi.APIKey) != "" {
		baseURL := strings.TrimSpace(c.Config.AI.Kimi.BaseURL)
		if baseURL == "" {
			baseURL = "https://api.moonshot.ai/v1"
		}
		kimiConfig := &ai.ProviderConfig{
			APIKey:       strings.TrimSpace(c.Config.AI.Kimi.APIKey),
			BaseURL:      baseURL,
			Model:        c.Config.AI.Kimi.Model,
			MaxTokens:    c.Config.AI.Kimi.MaxTokens,
			Temperature:  c.Config.AI.Kimi.Temperature,
			TopP:         c.Config.AI.Kimi.TopP,
			Timeout:      resolveTimeout(c.Config.AI.Kimi.TimeoutSeconds),
			RateLimitRPM: c.Config.AI.Kimi.RateLimitRPM,
			ProviderName: "kimi",
		}
		if kimiConfig.MaxTokens == 0 {
			kimiConfig.MaxTokens = 2048
		}
		if kimiConfig.Temperature == 0 {
			kimiConfig.Temperature = 1.0 // Kimi API requires temperature=1 for some models
		}
		// kimi-k2.6 only accepts temperature=1 and top_p=0.95
		if strings.HasPrefix(kimiConfig.Model, "kimi-k2") {
			kimiConfig.Temperature = 1.0
			kimiConfig.TopP = 0.95
			if kimiConfig.Timeout < 60*time.Second {
				kimiConfig.Timeout = 60 * time.Second // K2 thinking mode needs more time
			}
		}
		kimiProvider := ai.NewOpenAIProvider(kimiConfig, c.ZapLog)
		providers = append(providers, kimiProvider)
	}

	if len(providers) == 0 {
		return fmt.Errorf("no AI providers available")
	}

	// Set primary and fallbacks based on config
	var primary ai.AIProvider
	var fallbacks []ai.AIProvider

	primaryName := c.Config.AI.Primary
	for i, p := range providers {
		if p.Name() == primaryName {
			primary = p
			fallbacks = append(providers[:i:i], providers[i+1:]...)
			break
		}
	}
	if primary == nil {
		primary = providers[0]
		if len(providers) > 1 {
			fallbacks = providers[1:]
		}
	}

	c.AIProviderManager = ai.NewProviderManager(primary, fallbacks, &ai.ProviderManagerConfig{
		RetryAttempts: 1,
		RetryDelay:    500 * time.Millisecond,
	}, c.ZapLog)

	// Validate primary provider connectivity at startup (non-blocking)
	go func() {
		checkCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if !c.AIProviderManager.IsAvailable(checkCtx) {
			c.ZapLog.Warn("Primary AI provider is not available",
				zap.String("provider", primary.Name()),
				zap.String("hint", "Check that the API key is valid and has not expired"),
			)
		} else {
			c.ZapLog.Info("Primary AI provider is available", zap.String("provider", primary.Name()))
		}
	}()

	// Initialize repositories for AI services
	userNewsRepo := repositories.NewUserNewsRepository(c.DB, c.ZapLog)
	streakRepo := repositories.NewInvestmentStreakRepository(c.DB, c.ZapLog)
	contributionsRepo := repositories.NewUserContributionsRepository(c.DB, c.ZapLog)
	portfolioRepo := repositories.NewPortfolioRepository(c.DB, c.ZapLog)

	// Initialize data providers
	c.PortfolioDataProvider = aiservice.NewPortfolioDataProvider(
		&portfolioValueAdapter{repo: portfolioRepo},
		positionRepo,
		c.ZapLog,
	)

	c.ActivityDataProvider = aiservice.NewActivityDataProvider(
		&contributionRepoAdapter{repo: contributionsRepo},
		&streakRepoAdapter{repo: streakRepo},
		c.ZapLog,
	)

	// Initialize news service
	c.NewsService = newsservice.NewService(
		&alpacaNewsAdapter{client: c.AlpacaClient},
		userNewsRepo,
		positionRepo,
		c.ZapLog,
	)

	// Initialize AI orchestrator (wired to ProviderManager for failover)
	c.AIOrchestrator = aiservice.NewOrchestratorWithDeps(
		c.AIProviderManager,
		c.PortfolioDataProvider,
		c.ActivityDataProvider,
		&newsProviderAdapter{svc: c.NewsService},
		c.ZapLog,
		aiservice.OrchestratorDeps{},
	)

	// Initialize basket recommender
	c.AIRecommender = aiservice.NewRecommender(
		c.AIProviderManager,
		&basketRepoAdapter{repo: basketRepo},
		c.PortfolioDataProvider,
		c.ZapLog,
	)

	// Initialize conversation persistence
	c.ConversationRepo = repositories.NewConversationRepository(c.DB, c.ZapLog)
	c.ConversationService = conversationsvc.NewService(c.ConversationRepo, c.AIProviderManager, c.ZapLog)
	c.AIOrchestrator.SetConversations(c.ConversationService)

	// Initialize Miriam's long-term memory (fact extraction + tone calibration)
	memoryRepo := repositories.NewMiriamMemoryRepository(sqlxDB)
	memorySvc := aiservice.NewMemoryService(memoryRepo, c.AIProviderManager, c.ZapLog)
	c.AIOrchestrator.SetMemory(memorySvc)
	c.MemoryService = memorySvc

	// Initialize usage tracking
	c.UsageRepo = repositories.NewAIUsageRepository(c.DB, c.ZapLog)
	c.UsageService = usagesvc.NewService(c.UsageRepo, c.ZapLog)
	c.AIOrchestrator.SetUsageTracker(c.UsageService)

	// Initialize knowledge base (RAG)
	// Embeddings use Gemini regardless of chat provider; gate on Gemini key availability.
	if strings.TrimSpace(c.Config.AI.Gemini.APIKey) != "" {
		c.EmbeddingsClient = embeddings.NewGeminiClient(strings.TrimSpace(c.Config.AI.Gemini.APIKey), c.ZapLog)
		c.KnowledgeRepo = repositories.NewKnowledgeRepository(c.DB, c.ZapLog)
		c.KnowledgeService = knowledgesvc.NewService(c.KnowledgeRepo, c.EmbeddingsClient, c.RedisClient, c.ZapLog)
		c.AIOrchestrator.SetKnowledge(c.KnowledgeService)
	}

	// Wire embedder to memory service now that EmbeddingsClient is initialized
	if c.EmbeddingsClient != nil && c.MemoryService != nil {
		c.MemoryService.SetEmbedder(c.EmbeddingsClient)
	}

	// Initialize spending analysis (all outflows: card, withdrawal, p2p)
	spendingSvc := spendingsvc.NewService(c.LedgerSpendingRepo)
	c.AIOrchestrator.SetSpending(spendingSvc)

	c.ContextSignalRepo = repositories.NewContextSignalRepository(sqlxDB)
	c.AIOrchestrator.SetContextSignals(c.ContextSignalRepo)

	// Initialize balance history (stash growth chart)
	if c.yieldRepo != nil {
		c.AIOrchestrator.SetBalanceHistory(c.yieldRepo)
	}

	// Initialize pattern analysis
	if c.CardRepo != nil {
		c.AIOrchestrator.SetPatterns(c.CardRepo)
	}

	// Initialize comparative context (uses ledger for balances)
	if c.LedgerService != nil {
		c.AIOrchestrator.SetAggregateStats(c.LedgerService)
	}

	// Initialize action tools (funds transfer + audit)
	if c.LedgerService != nil {
		c.AIOrchestrator.SetFundsTransferer(&fundsTransfererAdapter{ledger: c.LedgerService})
		auditRepo := repositories.NewActionAuditRepository(sqlxDB, c.ZapLog)
		c.AIOrchestrator.SetActionAuditor(auditRepo)
	}

	// Wire account checker for fraud/freeze checks on AI-initiated transfers
	if c.UserRepo != nil {
		c.AIOrchestrator.SetAccountChecker(&accountCheckerAdapter{repo: c.UserRepo})
	}

	// Wire emergency withdrawer for stash-to-spend transfers during lock period
	if c.WithdrawalService != nil {
		c.AIOrchestrator.SetEmergencyWithdrawer(c.WithdrawalService)
	}

	// Use Redis for pending actions (survives restarts, works across instances)
	if c.RedisClient != nil {
		c.AIOrchestrator.SetPendingActions(aiservice.NewRedisPendingActions(c.RedisClient, c.ZapLog))
	}

	// Wire read-only data tools
	if c.CardRepo != nil {
		c.AIOrchestrator.SetCardTransactions(c.CardRepo)
	}
	if c.DepositRepo != nil {
		c.AIOrchestrator.SetDepositHistory(c.DepositRepo)
	}
	if c.WithdrawalRepo != nil {
		c.AIOrchestrator.SetWithdrawalHistory(c.WithdrawalRepo)
	}
	if c.ReceiptRepo != nil {
		c.AIOrchestrator.SetReceiptHistory(c.ReceiptRepo)
	}
	c.AIOrchestrator.SetBudgetProvider(c.BudgetRepo)
	c.AIOrchestrator.SetFinancialProfileProvider(c.FinancialProfileRepo)
	c.AIOrchestrator.SetFinancialObligationProvider(c.FinancialObligationService)
	c.AIOrchestrator.SetAutomationCreator(&automationCreatorAdapter{service: c.AutomationService})
	c.AIOrchestrator.SetAutomationProvider(&automationProviderAdapter{svc: c.AutomationService})
	c.AIOrchestrator.SetMiriamIntelligenceProvider(c.MiriamIntelligenceService)
	c.AIOrchestrator.SetObligationCreator(&obligationCreatorAdapter{service: c.FinancialObligationService})
	c.AIOrchestrator.SetCurrencyRateProvider(c.ExchangeRateRepo)
	warrantyRepo := repositories.NewWarrantyRepository(sqlxDB)
	c.AIOrchestrator.SetWarrantyTracker(warrantyRepo)

	// Wire recurring expense detector
	recurringRepo := repositories.NewRecurringExpenseRepository(sqlxDB)
	c.AIOrchestrator.SetRecurringDetector(recurringRepo)

	// Wire receipt challenges and savings suggestions
	if c.ReceiptRepo != nil {
		c.AIOrchestrator.SetReceiptChallenges(aiservice.NewReceiptChallengeProvider(c.ReceiptRepo, c.BudgetRepo, spendingSvc))
		c.AIOrchestrator.SetSavingsSuggestions(aiservice.NewSavingsSuggestionProvider(c.ReceiptRepo, spendingSvc))
	}

	// Wire price tracking
	priceRepo := repositories.NewPriceTrackingRepository(sqlxDB)
	c.AIOrchestrator.SetPriceTracker(priceRepo)

	// Wire merchant intelligence
	merchantRepo := repositories.NewMerchantRepository(sqlxDB)
	c.AIOrchestrator.SetMerchantAnalyzer(merchantRepo)

	if c.yieldRepo != nil {
		c.AIOrchestrator.SetYieldProvider(c.yieldRepo)
	}

	// Wire tax, email, and goals tools
	c.AIOrchestrator.SetUserProfile(&userProfileAdapter{userRepo: c.UserRepo})
	if c.EmailService != nil {
		c.AIOrchestrator.SetReportEmailSender(c.EmailService)
	}

	c.ZapLog.Info("AI Financial Manager services initialized",
		zap.String("primary_provider", primary.Name()),
		zap.Int("fallback_count", len(fallbacks)),
	)

	return nil
}

// AI service adapters

type portfolioValueAdapter struct {
	repo *repositories.PortfolioRepository
}

func (a *portfolioValueAdapter) GetPortfolioValue(ctx context.Context, userID uuid.UUID, date time.Time) (decimal.Decimal, error) {
	return a.repo.GetPortfolioValue(ctx, userID, date)
}

type contributionRepoAdapter struct {
	repo *repositories.UserContributionsRepository
}

func (a *contributionRepoAdapter) GetByUserID(ctx context.Context, userID uuid.UUID, contributionType *entities.ContributionType, startDate, endDate *time.Time, limit, offset int) ([]*entities.UserContribution, error) {
	return a.repo.GetByUserID(ctx, userID, contributionType, startDate, endDate, limit, offset)
}

func (a *contributionRepoAdapter) GetTotalByType(ctx context.Context, userID uuid.UUID, startDate, endDate time.Time) (map[entities.ContributionType]string, error) {
	return a.repo.GetTotalByType(ctx, userID, startDate, endDate)
}

type streakRepoAdapter struct {
	repo *repositories.InvestmentStreakRepository
}

func (a *streakRepoAdapter) GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.InvestmentStreak, error) {
	return a.repo.GetByUserID(ctx, userID)
}

type newsProviderAdapter struct {
	svc *newsservice.Service
}

func (a *newsProviderAdapter) GetWeeklyNews(ctx context.Context, userID uuid.UUID) ([]*entities.UserNews, error) {
	return a.svc.GetWeeklyNews(ctx, userID)
}

type basketRepoAdapter struct {
	repo *repositories.BasketRepository
}

func (a *basketRepoAdapter) GetCuratedBaskets(ctx context.Context) ([]*entities.Basket, error) {
	return a.repo.GetAll(ctx)
}

func (a *basketRepoAdapter) GetByID(ctx context.Context, id uuid.UUID) (*entities.Basket, error) {
	return a.repo.GetByID(ctx, id)
}

type alpacaNewsAdapter struct {
	client *alpaca.Client
}

func (a *alpacaNewsAdapter) GetNews(ctx context.Context, req *entities.AlpacaNewsRequest) (*entities.AlpacaNewsResponse, error) {
	return a.client.GetNews(ctx, req)
}

// GetAIOrchestrator returns the AI orchestrator
func (c *Container) GetAIOrchestrator() *aiservice.Orchestrator {
	return c.AIOrchestrator
}

// GetAIRecommender returns the AI recommender
func (c *Container) GetAIRecommender() *aiservice.Recommender {
	return c.AIRecommender
}

// GetConversationService returns the conversation service
func (c *Container) GetConversationService() *conversationsvc.Service {
	return c.ConversationService
}

// GetUsageService returns the usage service
func (c *Container) GetUsageService() *usagesvc.Service {
	return c.UsageService
}

// GetKnowledgeService returns the knowledge service
func (c *Container) GetKnowledgeService() *knowledgesvc.Service {
	return c.KnowledgeService
}

// GetNewsService returns the news service
func (c *Container) GetNewsService() *newsservice.Service {
	return c.NewsService
}

// GetPortfolioDataProvider returns the portfolio data provider
func (c *Container) GetPortfolioDataProvider() *aiservice.PortfolioDataProviderImpl {
	return c.PortfolioDataProvider
}

// GetActivityDataProvider returns the activity data provider
func (c *Container) GetActivityDataProvider() *aiservice.ActivityDataProviderImpl {
	return c.ActivityDataProvider
}

// GetStreakRepository returns the investment streak repository adapter
func (c *Container) GetStreakRepository() handlers.InvestmentStreakRepository {
	if c.ActivityDataProvider == nil {
		return nil
	}
	return &streakRepoAdapter{repo: repositories.NewInvestmentStreakRepository(c.DB, c.ZapLog)}
}

// GetContributionsRepository returns the user contributions repository adapter
func (c *Container) GetContributionsRepository() handlers.UserContributionsRepository {
	if c.ActivityDataProvider == nil {
		return nil
	}
	return &contributionRepoAdapter{repo: repositories.NewUserContributionsRepository(c.DB, c.ZapLog)}
}

// initializeAlpacaInvestmentServices initializes Alpaca investment infrastructure
func (c *Container) initializeAlpacaInvestmentServices(sqlxDB *sqlx.DB) error {
	// Initialize repositories
	c.AlpacaAccountRepo = repositories.NewAlpacaAccountRepository(sqlxDB)
	c.InvestmentOrderRepo = repositories.NewInvestmentOrderRepository(sqlxDB)
	c.InvestmentPositionRepo = repositories.NewInvestmentPositionRepository(sqlxDB)
	c.AlpacaEventRepo = repositories.NewAlpacaEventRepository(sqlxDB)
	c.AlpacaInstantFundingRepo = repositories.NewAlpacaInstantFundingRepository(sqlxDB)

	// User profile adapter for account service
	userProfileAdapter := repositories.NewUserProfileAdapter(c.UserRepo)

	// Initialize Account Service
	c.AlpacaAccountService = alpacaservice.NewAccountService(
		c.AlpacaClient,
		c.AlpacaAccountRepo,
		userProfileAdapter,
		c.ZapLog,
	)

	// Initialize Funding Bridge
	c.AlpacaFundingBridge = alpacaservice.NewFundingBridge(
		c.AlpacaClient,
		c.AlpacaAccountRepo,
		c.AlpacaInstantFundingRepo,
		c.BalanceRepo,
		c.Config.Alpaca.FirmAccountNo,
		c.ZapLog,
	)

	// Initialize Event Processor
	c.AlpacaEventProcessor = alpacaservice.NewEventProcessor(
		c.AlpacaAccountRepo,
		c.InvestmentOrderRepo,
		c.InvestmentPositionRepo,
		c.AlpacaEventRepo,
		c.BalanceRepo,
		c.ZapLog,
	)

	// Initialize Portfolio Sync Service
	c.AlpacaPortfolioSync = alpacaservice.NewPortfolioSyncService(
		c.AlpacaClient,
		c.AlpacaAccountRepo,
		c.InvestmentPositionRepo,
		c.BalanceRepo,
		c.ZapLog,
	)

	c.ZapLog.Info("Alpaca investment services initialized")
	return nil
}

// initializeAdvancedFeatures initializes analytics, market data, and automation services
func (c *Container) initializeAdvancedFeatures(sqlxDB *sqlx.DB) error {
	// Initialize repositories
	c.PortfolioSnapshotRepo = repositories.NewPortfolioSnapshotRepository(sqlxDB)
	c.ScheduledInvestmentRepo = repositories.NewScheduledInvestmentRepository(sqlxDB)
	c.RebalancingConfigRepo = repositories.NewRebalancingConfigRepository(sqlxDB)
	c.MarketAlertRepo = repositories.NewMarketAlertRepository(sqlxDB)

	// Initialize Portfolio Analytics Service
	c.PortfolioAnalyticsService = analyticsservice.NewPortfolioAnalyticsService(
		c.PortfolioSnapshotRepo,
		c.InvestmentPositionRepo,
		c.AlpacaAccountRepo,
		c.ZapLog,
	)

	// Initialize Market Data Service
	c.MarketDataService = marketservice.NewMarketDataService(
		c.AlpacaClient,
		c.MarketAlertRepo,
		&marketNotificationAdapter{svc: c.NotificationService},
		c.ZapLog,
	)

	// Initialize Order Placer adapter for scheduled investments
	orderPlacer := &orderPlacerAdapter{
		investingService: c.InvestingService,
		accountService:   c.AlpacaAccountService,
		alpacaClient:     c.AlpacaClient,
		orderRepo:        c.InvestmentOrderRepo,
		logger:           c.ZapLog,
	}

	// Initialize Scheduled Investment Service
	c.ScheduledInvestmentService = investing.NewScheduledInvestmentService(
		c.ScheduledInvestmentRepo,
		orderPlacer,
		c.BrokerageAdapter, // BasketOrderPlacer
		c.ZapLog,
	)

	// Initialize Rebalancing Service
	c.RebalancingService = investing.NewRebalancingService(
		c.RebalancingConfigRepo,
		c.InvestmentPositionRepo,
		c.MarketDataService,
		orderPlacer,
		c.ZapLog,
	)

	// Initialize Round-up Service
	c.RoundupRepo = repositories.NewRoundupRepository(sqlxDB)
	c.RoundupService = roundup.NewService(
		c.RoundupRepo,
		c.LedgerService,
		orderPlacer,
		nil, // ContributionRecorder - can be added later
		c.ZapLog,
		sqlxDB,
	)

	// Initialize Copy Trading Service
	c.CopyTradingRepo = repositories.NewCopyTradingRepository(sqlxDB)
	c.CopyTradingService = copytrading.NewService(
		c.CopyTradingRepo,
		&copyTradingBalanceAdapter{ledgerService: c.LedgerService, userID: uuid.Nil},
		&copyTradingTradingAdapter{alpacaClient: c.AlpacaClient, accountRepo: c.AlpacaAccountRepo},
		c.ZapLog,
	)

	// Initialize Card Service
	c.CardRepo = repositories.NewCardRepository(sqlxDB)
	c.CardService = card.NewService(
		c.CardRepo,
		c.BridgeAdapter,
		&cardUserProfileAdapter{userRepo: c.UserRepo},
		&cardWalletAdapter{walletService: c.WalletService},
		&cardBalanceAdapter{ledgerService: c.LedgerService},
		c.ZapLog,
	)
	// Wire ledger service to card service for transaction ledger entries
	c.CardService.SetLedgerService(c.LedgerService)
	if c.NotificationService != nil {
		c.CardService.SetNotificationService(c.NotificationService)
	}
	if c.AutomationService != nil {
		c.AutomationService.SetCardController(&automationCardControllerAdapter{card: c.CardService})
	}
	if c.MoneyGuardService != nil && c.CardService != nil {
		c.CardService.SetMoneyGuard(&cardMoneyGuardAdapter{service: c.MoneyGuardService})
	}

	// Rewire Bridge webhook service now that card service is available.
	if c.BridgeWebhookHandler != nil && c.BridgeVirtualAccountService != nil {
		bridgeWebhookService := webhooks.NewBridgeWebhookService(
			&BridgeVirtualAccountWebhookAdapter{service: c.BridgeVirtualAccountService},
			c.BridgeCustomerStatusProcessor, // preserve KYC processor — do NOT pass nil
			&BridgeCardWebhookAdapter{service: c.CardService},
			c.WithdrawalService,
			&bridgeWebhookNotifierAdapter{svc: c.NotificationService},
			c.UserRepo,
			c.ZapLog,
			c.DB,
		)
		c.BridgeWebhookHandler.SetService(bridgeWebhookService)
	}

	c.ZapLog.Info("Advanced features initialized")
	return nil
}

// marketNotificationAdapter adapts NotificationService for market alerts
type marketNotificationAdapter struct {
	svc *services.NotificationService
}

func (a *marketNotificationAdapter) SendPushNotification(ctx context.Context, userID uuid.UUID, title, message string) error {
	if a.svc == nil {
		return nil
	}
	return a.svc.SendGenericNotification(ctx, userID, title, message)
}

type automationCardControllerAdapter struct {
	card *card.Service
}

func (a *automationCardControllerAdapter) FreezeCard(ctx context.Context, userID, cardID uuid.UUID) error {
	_, err := a.card.FreezeCard(ctx, userID, cardID)
	return err
}

func (a *automationCardControllerAdapter) UnfreezeCard(ctx context.Context, userID, cardID uuid.UUID) error {
	_, err := a.card.UnfreezeCard(ctx, userID, cardID)
	return err
}

func (a *automationCardControllerAdapter) GetCardsByUser(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	cards, err := a.card.GetUserCards(ctx, userID)
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0, len(cards))
	for _, c := range cards {
		ids = append(ids, c.ID)
	}
	return ids, nil
}

type cardMoneyGuardAdapter struct {
	service *moneyguardservice.Service
}

func (a *cardMoneyGuardAdapter) EvaluateCardAuthorization(ctx context.Context, userID uuid.UUID, input card.MoneyGuardTransactionInput) (*entities.MoneyGuardDecision, error) {
	return a.service.EvaluateCardAuthorization(ctx, userID, moneyguardservice.TransactionInput{
		Amount: input.Amount, Currency: input.Currency, Merchant: input.Merchant,
		Category: input.Category, Reference: input.Reference,
	})
}

func (a *cardMoneyGuardAdapter) EvaluateCardTransaction(ctx context.Context, userID uuid.UUID, input card.MoneyGuardTransactionInput) (*entities.MoneyGuardDecision, error) {
	return a.service.EvaluateCardTransaction(ctx, userID, moneyguardservice.TransactionInput{
		Amount: input.Amount, Currency: input.Currency, Merchant: input.Merchant,
		Category: input.Category, Reference: input.Reference,
	})
}

// walletWebhookAdapter adapts wallet.Service to WalletWebhookService interface
type walletWebhookAdapter struct {
	walletService *wallet.Service
}

func (a *walletWebhookAdapter) SyncWalletStatus(ctx context.Context, bridgeWalletID string, status string) error {
	if a.walletService == nil {
		return fmt.Errorf("wallet service not available")
	}
	return a.walletService.SyncWalletStatus(ctx, bridgeWalletID, status)
}

// bridgeWebhookNotifierAdapter adapts NotificationService to BridgeWebhookNotifier
type bridgeWebhookNotifierAdapter struct {
	svc *services.NotificationService
}

func (a *bridgeWebhookNotifierAdapter) NotifyDepositReceived(ctx *gin.Context, userID uuid.UUID, amount, currency string) error {
	if a.svc == nil {
		return nil
	}
	return a.svc.NotifyDepositConfirmed(ctx.Request.Context(), userID, amount+" "+currency, "", "")
}

func (a *bridgeWebhookNotifierAdapter) NotifyKYCStatusChanged(ctx *gin.Context, userID uuid.UUID, status string) error {
	if a.svc == nil {
		return nil
	}
	switch status {
	case "active":
		return a.svc.NotifyKYCApproved(ctx.Request.Context(), userID)
	case "rejected":
		return a.svc.NotifyKYCRejected(ctx.Request.Context(), userID)
	}
	return nil
}

// copyTradingBalanceAdapter adapts LedgerService for copy trading balance operations
type copyTradingBalanceAdapter struct {
	ledgerService *ledger.Service
	userID        uuid.UUID
}

func (a *copyTradingBalanceAdapter) GetAvailableBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	if a.ledgerService == nil {
		return decimal.Zero, fmt.Errorf("ledger service not available")
	}
	balances, err := a.ledgerService.GetUserBalances(ctx, userID)
	if err != nil {
		return decimal.Zero, err
	}
	return balances.USDCBalance, nil
}

func (a *copyTradingBalanceAdapter) DeductBalance(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, description string) error {
	if a.ledgerService == nil {
		return fmt.Errorf("ledger service not available")
	}
	// Reserve funds for copy trading allocation
	return a.ledgerService.ReserveForInvestment(ctx, userID, amount)
}

func (a *copyTradingBalanceAdapter) AddBalance(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, description string) error {
	if a.ledgerService == nil {
		return fmt.Errorf("ledger service not available")
	}
	// Release reserved funds back to user
	return a.ledgerService.ReleaseReservation(ctx, userID, amount)
}

// copyTradingTradingAdapter adapts Alpaca client for copy trading order execution
type copyTradingTradingAdapter struct {
	alpacaClient *alpaca.Client
	accountRepo  *repositories.AlpacaAccountRepository
}

func (a *copyTradingTradingAdapter) PlaceOrder(ctx context.Context, userID uuid.UUID, symbol string, side string, quantity decimal.Decimal) (string, decimal.Decimal, error) {
	if a.alpacaClient == nil || a.accountRepo == nil {
		return "", decimal.Zero, fmt.Errorf("trading adapter not configured")
	}

	// Get user's Alpaca account
	account, err := a.accountRepo.GetByUserID(ctx, userID)
	if err != nil || account == nil {
		return "", decimal.Zero, fmt.Errorf("user has no brokerage account")
	}

	// Place order via Alpaca
	orderSide := entities.AlpacaOrderSideBuy
	if side == "sell" {
		orderSide = entities.AlpacaOrderSideSell
	}

	orderReq := &entities.AlpacaCreateOrderRequest{
		Symbol:      symbol,
		Qty:         &quantity,
		Side:        orderSide,
		Type:        entities.AlpacaOrderTypeMarket,
		TimeInForce: entities.AlpacaTimeInForceDay,
	}

	resp, err := a.alpacaClient.CreateOrder(ctx, account.AlpacaAccountID, orderReq)
	if err != nil {
		return "", decimal.Zero, fmt.Errorf("failed to place order: %w", err)
	}

	// Get executed price (for market orders, use filled_avg_price or current price)
	executedPrice := decimal.Zero
	if resp.FilledAvgPrice != nil && !resp.FilledAvgPrice.IsZero() {
		executedPrice = *resp.FilledAvgPrice
	}

	return resp.ID, executedPrice, nil
}

func (a *copyTradingTradingAdapter) GetCurrentPrice(ctx context.Context, symbol string) (decimal.Decimal, error) {
	if a.alpacaClient == nil {
		return decimal.Zero, fmt.Errorf("trading adapter not configured")
	}

	quote, err := a.alpacaClient.GetLatestQuote(ctx, symbol)
	if err != nil {
		return decimal.Zero, fmt.Errorf("failed to get quote: %w", err)
	}

	return quote.Ask, nil
}

// autoInvestOrderPlacerAdapter implements autoinvest.OrderPlacer interface
type autoInvestOrderPlacerAdapter struct {
	accountService *alpacaservice.AccountService
	alpacaClient   *alpaca.Client
	orderRepo      *repositories.InvestmentOrderRepository
	logger         *zap.Logger
}

func (a *autoInvestOrderPlacerAdapter) PlaceMarketOrder(ctx context.Context, userID uuid.UUID, symbol string, amount decimal.Decimal, clientOrderID string) (*entities.AlpacaOrderResponse, error) {
	// Get user's Alpaca account
	account, err := a.accountService.GetUserAccount(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get account: %w", err)
	}
	if account == nil {
		return nil, fmt.Errorf("user has no Alpaca account")
	}

	// Guard: account must be tradeable
	if account.AccountBlocked {
		return nil, fmt.Errorf("alpaca account is blocked for user %s", userID)
	}
	if account.TradingBlocked {
		return nil, fmt.Errorf("trading is blocked on alpaca account for user %s", userID)
	}

	// Guard: only place orders during market hours (Mon–Fri 09:30–16:00 ET)
	// DAY orders are rejected by Alpaca outside these hours; queue for next open instead.
	if !isMarketOpen() {
		return nil, fmt.Errorf("market is closed: order for %s queued for next market open", symbol)
	}

	// Create market order via Alpaca
	orderReq := &entities.AlpacaCreateOrderRequest{
		Symbol:        symbol,
		Notional:      &amount,
		Side:          entities.AlpacaOrderSideBuy,
		Type:          entities.AlpacaOrderTypeMarket,
		TimeInForce:   entities.AlpacaTimeInForceDay,
		ClientOrderID: clientOrderID,
	}

	alpacaOrder, err := a.alpacaClient.CreateOrder(ctx, account.AlpacaAccountID, orderReq)
	if err != nil {
		return nil, fmt.Errorf("create order: %w", err)
	}

	// Store order in database for tracking
	now := time.Now()
	order := &entities.InvestmentOrder{
		ID:              uuid.New(),
		UserID:          userID,
		AlpacaAccountID: &account.ID,
		AlpacaOrderID:   &alpacaOrder.ID,
		ClientOrderID:   alpacaOrder.ClientOrderID,
		Symbol:          symbol,
		Side:            entities.AlpacaOrderSideBuy,
		OrderType:       entities.AlpacaOrderTypeMarket,
		TimeInForce:     entities.AlpacaTimeInForceDay,
		Notional:        &amount,
		Status:          alpacaOrder.Status,
		SubmittedAt:     &now,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	if err := a.orderRepo.Create(ctx, order); err != nil {
		a.logger.Error("Failed to store auto-invest order", zap.Error(err))
	}

	return alpacaOrder, nil
}

// isMarketOpen returns true when the US equity market is currently open (Mon–Fri 09:30–16:00 ET).
func isMarketOpen() bool {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		// If timezone data is unavailable, fail open so orders aren't silently dropped.
		return true
	}
	et := time.Now().In(loc)
	wd := et.Weekday()
	if wd == time.Saturday || wd == time.Sunday {
		return false
	}
	open := time.Date(et.Year(), et.Month(), et.Day(), 9, 30, 0, 0, loc)
	close := time.Date(et.Year(), et.Month(), et.Day(), 16, 0, 0, 0, loc)
	return et.After(open) && et.Before(close)
}

// strategyUserProfileAdapter adapts UserRepository for strategy engine
type strategyUserProfileAdapter struct {
	userRepo *repositories.UserRepository
}

func (a *strategyUserProfileAdapter) GetByID(ctx context.Context, id uuid.UUID) (*entities.UserProfile, error) {
	if a.userRepo == nil {
		return nil, fmt.Errorf("user repository not available")
	}
	return a.userRepo.GetByID(ctx, id)
}

// orderPlacerAdapter implements OrderPlacer interface for scheduled investments
type orderPlacerAdapter struct {
	investingService *investing.Service
	accountService   *alpacaservice.AccountService
	alpacaClient     *alpaca.Client
	orderRepo        *repositories.InvestmentOrderRepository
	logger           *zap.Logger
}

func (a *orderPlacerAdapter) PlaceMarketOrder(ctx context.Context, userID uuid.UUID, symbol string, notional decimal.Decimal) (*entities.InvestmentOrder, error) {
	// Get user's Alpaca account
	account, err := a.accountService.GetUserAccount(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get account: %w", err)
	}
	if account == nil {
		return nil, fmt.Errorf("user has no Alpaca account")
	}

	// Determine side based on notional sign
	side := entities.AlpacaOrderSideBuy
	if notional.LessThan(decimal.Zero) {
		side = entities.AlpacaOrderSideSell
		notional = notional.Abs()
	}

	// Create order via Alpaca
	orderReq := &entities.AlpacaCreateOrderRequest{
		Symbol:      symbol,
		Notional:    &notional,
		Side:        side,
		Type:        entities.AlpacaOrderTypeMarket,
		TimeInForce: entities.AlpacaTimeInForceDay,
	}

	alpacaOrder, err := a.alpacaClient.CreateOrder(ctx, account.AlpacaAccountID, orderReq)
	if err != nil {
		return nil, fmt.Errorf("create order: %w", err)
	}

	// Store order in database
	now := time.Now()
	order := &entities.InvestmentOrder{
		ID:              uuid.New(),
		UserID:          userID,
		AlpacaAccountID: &account.ID,
		AlpacaOrderID:   &alpacaOrder.ID,
		ClientOrderID:   alpacaOrder.ClientOrderID,
		Symbol:          symbol,
		Side:            side,
		OrderType:       entities.AlpacaOrderTypeMarket,
		TimeInForce:     entities.AlpacaTimeInForceDay,
		Notional:        &notional,
		Status:          alpacaOrder.Status,
		SubmittedAt:     &now,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	if err := a.orderRepo.Create(ctx, order); err != nil {
		a.logger.Error("Failed to store order", zap.Error(err))
	}

	return order, nil
}

// Card service adapters

// cardUserProfileAdapter adapts UserRepository for card service
type cardUserProfileAdapter struct {
	userRepo *repositories.UserRepository
}

func (a *cardUserProfileAdapter) GetByID(ctx context.Context, id uuid.UUID) (*entities.UserProfile, error) {
	if a.userRepo == nil {
		return nil, fmt.Errorf("user repository not available")
	}
	return a.userRepo.GetByID(ctx, id)
}

// cardWalletAdapter adapts WalletService for card service
type cardWalletAdapter struct {
	walletService *wallet.Service
}

func (a *cardWalletAdapter) GetUserWalletByChain(ctx context.Context, userID uuid.UUID, chain string) (*entities.ManagedWallet, error) {
	if a.walletService == nil {
		return nil, fmt.Errorf("wallet service not available")
	}
	walletChain := entities.WalletChain(strings.ToUpper(chain))
	return a.walletService.GetWalletByUserAndChain(ctx, userID, walletChain)
}

// cardBalanceAdapter adapts LedgerService for card balance operations
type cardBalanceAdapter struct {
	ledgerService *ledger.Service
}

func (a *cardBalanceAdapter) GetSpendBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	if a.ledgerService == nil {
		return decimal.Zero, fmt.Errorf("ledger service not available")
	}
	// Get spending balance account directly
	account, err := a.ledgerService.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil {
		return decimal.Zero, err
	}
	return account.Balance, nil
}

func (a *cardBalanceAdapter) DeductSpendBalance(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, reference string) error {
	if a.ledgerService == nil {
		return fmt.Errorf("ledger service not available")
	}
	// Create a debit entry for card transaction
	return a.ledgerService.RecordCardTransaction(ctx, userID, amount, reference)
}

// Getters for new services

// GetAlpacaAccountService returns the Alpaca account service
func (c *Container) GetAlpacaAccountService() *alpacaservice.AccountService {
	return c.AlpacaAccountService
}

// GetAlpacaFundingBridge returns the Alpaca funding bridge
func (c *Container) GetAlpacaFundingBridge() *alpacaservice.FundingBridge {
	return c.AlpacaFundingBridge
}

// GetAlpacaEventProcessor returns the Alpaca event processor
func (c *Container) GetAlpacaEventProcessor() *alpacaservice.EventProcessor {
	return c.AlpacaEventProcessor
}

// GetAlpacaPortfolioSync returns the Alpaca portfolio sync service
func (c *Container) GetAlpacaPortfolioSync() *alpacaservice.PortfolioSyncService {
	return c.AlpacaPortfolioSync
}

// GetPortfolioAnalyticsService returns the portfolio analytics service
func (c *Container) GetPortfolioAnalyticsService() *analyticsservice.PortfolioAnalyticsService {
	return c.PortfolioAnalyticsService
}

// GetMarketDataService returns the market data service
func (c *Container) GetMarketDataService() *marketservice.MarketDataService {
	return c.MarketDataService
}

// GetScheduledInvestmentService returns the scheduled investment service
func (c *Container) GetScheduledInvestmentService() *investing.ScheduledInvestmentService {
	return c.ScheduledInvestmentService
}

// GetRebalancingService returns the rebalancing service
func (c *Container) GetRebalancingService() *investing.RebalancingService {
	return c.RebalancingService
}

// GetInvestmentHandlers returns investment handlers
func (c *Container) GetInvestmentHandlers() *handlers.InvestmentHandlers {
	if c.AlpacaAccountService == nil {
		return nil
	}
	return handlers.NewInvestmentHandlers(
		c.AlpacaAccountService,
		c.AlpacaFundingBridge,
		c.AlpacaPortfolioSync,
		c.Logger,
	)
}

// GetAlpacaWebhookHandlers returns Alpaca webhook handlers
func (c *Container) GetAlpacaWebhookHandlers() *handlers.AlpacaWebhookHandlers {
	if c.AlpacaEventProcessor == nil {
		return nil
	}
	// Get webhook secret from config
	webhookSecret := c.Config.Alpaca.WebhookSecret
	if webhookSecret == "" {
		c.ZapLog.Warn("Alpaca webhook secret not configured")
	}
	// Determine if webhook verification should be skipped (only in development)
	skipWebhookVerification := c.Config.Environment == "development" && webhookSecret == ""
	return handlers.NewAlpacaWebhookHandlers(c.AlpacaEventProcessor, c.Logger, webhookSecret, skipWebhookVerification, c.Config.Environment)
}

// GetAnalyticsHandlers returns analytics handlers
func (c *Container) GetAnalyticsHandlers() *handlers.AnalyticsHandlers {
	if c.PortfolioAnalyticsService == nil {
		return nil
	}
	return handlers.NewAnalyticsHandlers(c.PortfolioAnalyticsService, c.Logger)
}

// GetMarketHandlers returns market data handlers
func (c *Container) GetMarketHandlers() *handlers.MarketHandlers {
	if c.MarketDataService == nil {
		return nil
	}
	return handlers.NewMarketHandlers(c.MarketDataService, c.Logger)
}

// GetScheduledInvestmentHandlers returns scheduled investment handlers
func (c *Container) GetScheduledInvestmentHandlers() *handlers.ScheduledInvestmentHandlers {
	if c.ScheduledInvestmentService == nil {
		return nil
	}
	return handlers.NewScheduledInvestmentHandlers(c.ScheduledInvestmentService, c.Logger)
}

// GetRebalancingHandlers returns rebalancing handlers
func (c *Container) GetRebalancingHandlers() *handlers.RebalancingHandlers {
	if c.RebalancingService == nil {
		return nil
	}
	return handlers.NewRebalancingHandlers(c.RebalancingService, c.Logger)
}

// GetRoundupService returns the round-up service
func (c *Container) GetRoundupService() *roundup.Service {
	return c.RoundupService
}

// GetRoundupHandlers returns round-up handlers
func (c *Container) GetRoundupHandlers() *handlers.RoundupHandlers {
	if c.RoundupService == nil {
		return nil
	}
	return handlers.NewRoundupHandlers(c.RoundupService, c.ZapLog)
}

// GetCopyTradingService returns the copy trading service
func (c *Container) GetCopyTradingService() *copytrading.Service {
	return c.CopyTradingService
}

// GetCopyTradingHandlers returns copy trading handlers
func (c *Container) GetCopyTradingHandlers() *handlers.CopyTradingHandlers {
	if c.CopyTradingService == nil {
		return nil
	}
	return handlers.NewCopyTradingHandlers(c.CopyTradingService, c.Logger)
}

// GetCardService returns the card service
func (c *Container) GetCardService() *card.Service {
	return c.CardService
}

// GetCardHandlers returns card handlers
func (c *Container) GetCardHandlers() *handlers.CardHandlers {
	if c.CardService == nil {
		return nil
	}
	return handlers.NewCardHandlers(c.CardService, c.ZapLog)
}

// GetStationHandlers returns station handlers
func (c *Container) GetStationHandlers() *handlers.StationHandlers {
	if c.StationService == nil {
		return nil
	}
	if c.RedisClient != nil {
		cached := station.NewCachedService(c.StationService, c.RedisClient)
		return handlers.NewStationHandlers(cached, c.ZapLog)
	}
	return handlers.NewStationHandlers(c.StationService, c.ZapLog)
}

// GetSpendingStashHandlers returns spending stash handlers
func (c *Container) GetSpendingStashHandlers() *handlers.SpendingStashHandlers {
	h := handlers.NewSpendingStashHandlers(
		c.AllocationService,
		c.CardService,
		c.RoundupService,
		c.ZapLog,
	)
	if c.P2PRepo != nil {
		h.SetP2PRepo(c.P2PRepo)
	}
	if c.WithdrawalRepo != nil {
		h.SetWithdrawalRepo(c.WithdrawalRepo)
	}
	return h
}

// GetInvestmentStashHandlers returns investment stash handlers
func (c *Container) GetInvestmentStashHandlers() *handlers.InvestmentStashHandlers {
	if c.AllocationService == nil || c.InvestmentPositionRepo == nil || c.InvestmentOrderRepo == nil || c.PortfolioAnalyticsService == nil {
		return nil
	}

	h := handlers.NewInvestmentStashHandlers(
		c.AllocationService,
		c.InvestmentPositionRepo,
		c.InvestmentOrderRepo,
		c.PortfolioAnalyticsService,
		c.ZapLog,
	)
	h.SetAutoInvestRepository(repositories.NewAutoInvestRepository(sqlx.NewDb(c.DB, "postgres")))
	if c.StrategyEngine != nil {
		h.SetStrategyProvider(c.StrategyEngine)
	}
	if c.AlpacaPortfolioSync != nil {
		h.SetPortfolioSyncer(c.AlpacaPortfolioSync)
	}
	return h
}

// GetPremiumHandlers returns premium feature HTTP handlers
func (c *Container) GetPremiumHandlers() *premiumhandlers.Handlers {
	if c.PremiumHandlers == nil {
		c.PremiumHandlers = premiumhandlers.NewHandlers(
			c.NairaShieldService,
			c.BlackTaxService,
			c.ReceiptSplitService,
			c.ScamIntelligenceService,
			c.TaxResidencyService,
			c.IncomeSmoothingService,
			c.FinancialTraumaService,
			c.VisaProofService,
			c.PanicButtonService,
			c.ZapLog,
		)
	}
	return c.PremiumHandlers
}

// GetCopyTradingRepository returns the copy trading repository
func (c *Container) GetCopyTradingRepository() *repositories.CopyTradingRepository {
	return c.CopyTradingRepo
}

// ListAllActiveUserIDs returns all active user IDs (for portfolio snapshot worker)
func (c *Container) ListAllActiveUserIDs(ctx context.Context) ([]uuid.UUID, error) {
	query := `SELECT id FROM users WHERE is_active = true`
	rows, err := c.DB.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var userIDs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			continue
		}
		userIDs = append(userIDs, id)
	}
	return userIDs, rows.Err()
}

// GetBridgeWebhookHandler returns the Bridge webhook handler
func (c *Container) GetBridgeWebhookHandler() *handlers.BridgeWebhookHandler {
	return c.BridgeWebhookHandler
}

// GetCircleWebhookHandler returns the Circle webhook handler.
func (c *Container) GetCircleWebhookHandler() *webhooks.CircleWebhookHandler {
	return c.CircleWebhookHandler
}

// GetBridgeVirtualAccountService returns the Bridge virtual account service
func (c *Container) GetBridgeVirtualAccountService() *funding.BridgeVirtualAccountService {
	return c.BridgeVirtualAccountService
}

// initializeBridgeServices initializes Bridge-related services
func (c *Container) initializeBridgeServices() {
	if c.BridgeClient == nil {
		c.ZapLog.Warn("Bridge client not configured, skipping Bridge services initialization")
		return
	}

	// Bridge virtual account service will be initialized after allocation service
	// For now, just set up the webhook handler with a placeholder service
	webhookSecret := c.Config.Bridge.WebhookSecret
	if webhookSecret == "" {
		c.ZapLog.Warn("Bridge webhook secret not configured")
	}

	// Determine if webhook verification should be skipped (only in development)
	skipWebhookVerification := c.Config.Environment == "development" && webhookSecret == ""

	// Create a minimal webhook service for now
	// Full service will be wired after domain services are initialized
	c.BridgeWebhookHandler = handlers.NewBridgeWebhookHandler(
		nil, // Service will be set later
		&walletWebhookAdapter{walletService: c.WalletService},
		c.ZapLog,
		webhookSecret,
		skipWebhookVerification,
		c.Config.Environment,
	)

	c.ZapLog.Info("Bridge webhook handler initialized")
}

// GetInstantFundingHandlers returns the instant funding handlers
func (c *Container) GetInstantFundingHandlers() *fundinghandlers.InstantFundingHandlers {
	return c.InstantFundingHandlers
}

// GetP2PHandlers returns the P2P transfer handlers
func (c *Container) GetP2PHandlers() *p2phandlers.Handlers {
	return c.P2PHandlers
}

// GetWithdrawalSecurityStore returns the withdrawal security store
func (c *Container) GetWithdrawalSecurityStore() *repositories.WithdrawalSecurityStore {
	return c.WithdrawalSecurityStore
}

// GetDepositSecurityStore returns the deposit security store
func (c *Container) GetDepositSecurityStore() *repositories.DepositSecurityStore {
	return c.DepositSecurityStore
}

// GetUnifiedFundingWebhookHandler returns the unified funding webhook handler
func (c *Container) GetUnifiedFundingWebhookHandler() *webhooks.UnifiedFundingWebhookHandler {
	return c.UnifiedFundingWebhookHandler
}

// initializeInstantFundingServices initializes instant funding services
func (c *Container) initializeInstantFundingServices(sqlxDB *sqlx.DB) {
	// Initialize repositories
	c.InstantFundingRepo = repositories.NewInstantFundingRepository(sqlxDB)
	c.UserAccountRepo = repositories.NewUserAccountRepository(sqlxDB)
	c.WithdrawalSecurityStore = repositories.NewWithdrawalSecurityStore(sqlxDB)
	c.DepositSecurityStore = repositories.NewDepositSecurityStore(sqlxDB)

	// Initialize virtual account repo for instant funding
	virtualAccountRepo := repositories.NewVirtualAccountRepository(sqlxDB)

	// Create Alpaca adapter for instant funding
	alpacaAdapter := &InstantFundingAlpacaAdapterImpl{
		service: c.AlpacaService,
	}

	// Initialize instant funding service
	c.InstantFundingService = funding.NewInstantFundingService(
		alpacaAdapter,
		virtualAccountRepo,
		c.InstantFundingRepo,
		c.UserAccountRepo,
		c.ZapLog,
		c.Config.Alpaca.FirmAccountNo,
	)

	// Initialize handlers
	c.InstantFundingHandlers = fundinghandlers.NewInstantFundingHandlers(
		c.InstantFundingService,
		c.ZapLog,
	)

	// Wire deposit security store to validation service
	if c.FundingService != nil && c.FundingService.GetValidationService() != nil {
		c.FundingService.GetValidationService().SetDepositSecurityStore(c.DepositSecurityStore)
	}

	c.ZapLog.Info("Instant funding services initialized")

	// --- ChainRails (cross-chain deposit funnel) ---
	c.ZapLog.Info("ChainRails config",
		zap.Bool("api_key_present", c.Config.ChainRails.APIKey != ""),
		zap.Bool("webhook_secret_present", c.Config.ChainRails.WebhookSecret != ""),
		zap.String("destination_chain", c.Config.ChainRails.DestinationChain),
		zap.String("settlement_token", c.Config.ChainRails.SettlementToken))

	if c.Config.ChainRails.APIKey != "" {
		crClient := chainrails.NewClient(chainrails.Config{
			APIKey:           c.Config.ChainRails.APIKey,
			WebhookSecret:    c.Config.ChainRails.WebhookSecret,
			BaseURL:          c.Config.ChainRails.BaseURL,
			DestinationChain: c.Config.ChainRails.DestinationChain,
			SettlementToken:  c.Config.ChainRails.SettlementToken,
		}, c.ZapLog)
		c.ChainRailsClient = crClient
		c.ChainRailsHandlers = fundinghandlers.NewChainRailsHandlers(
			c.ChainRailsClient, c.FundingService, c.Config.ChainRails.WebhookSecret, c.Config.ChainRails.DestinationChain, c.Logger,
		)
		// Wire ChainRails into withdrawal service for cross-chain withdrawals
		if c.WithdrawalService != nil {
			c.WithdrawalService.SetChainRailsAdapter(c.ChainRailsClient)
			c.ChainRailsHandlers.SetWithdrawalService(c.WithdrawalService)
		}
		if c.WalletService != nil {
			c.ChainRailsHandlers.SetWalletLookup(c.WalletService)
		}
		// Wire deposit sweep repository for auto-sweep and webhook handling
		c.DepositSweepRepo = repositories.NewDepositSweepRepository(sqlxDB)
		if c.FundingService != nil {
			c.FundingService.SetDepositSweepRepo(c.DepositSweepRepo)
		}
		c.ChainRailsHandlers.SetSweepService(c.DepositSweepRepo)

		if c.ReflectDepositRouter != nil {
			c.ReflectDepositRouter.SetChainRailsBridge(c.ChainRailsClient, c.Config.ChainRails.DestinationChain)
			c.ZapLog.Info("ChainRails wired into Reflect deposit router",
				zap.String("destination_chain", c.Config.ChainRails.DestinationChain))
		}
		c.ZapLog.Info("ChainRails deposit funnel initialized")
	} else {
		c.ZapLog.Warn("ChainRails API key is empty, skipping initialization")
	}

	// --- Paj Cash (NGN on/off ramp) ---
	if c.Config.Paj.APIKey != "" {
		pajClient := pajadapter.NewClient(pajadapter.Config{
			APIKey:        c.Config.Paj.APIKey,
			BaseURL:       c.Config.Paj.BaseURL,
			WebhookURL:    c.Config.Paj.WebhookURL,
			WalletAddress: c.Config.Paj.WalletAddress,
			TokenMint:     c.Config.Paj.TokenMint,
			Chain:         c.Config.Paj.Chain,
		}, c.ZapLog)
		pajService := pajfunding.NewService(sqlxDB, pajClient, &WithdrawalLedgerAdapter{ledgerService: c.LedgerService}, c.AllocationService, &PajDepositLedgerAdapter{ledgerService: c.LedgerService}, c.RedisClient, c.Config.Security.EncryptionKey, c.ZapLog)
		pajService.SetDepositRepository(c.DepositRepo)
		if c.NotificationService != nil {
			pajService.SetNotificationService(c.NotificationService)
		}
		if c.WalletService != nil {
			pajService.SetWalletProvider(c.WalletService)
		}
		if c.CircleAdapter != nil {
			pajService.SetCircleTransfer(c.CircleAdapter)
		}
		if c.ChainRailsClient != nil {
			pajService.SetChainRailsAdapter(c.ChainRailsClient)
		}
		if c.GameplayHooks != nil {
			pajService.SetGameplayHooks(c.GameplayHooks)
		}
		if c.LimitsService != nil {
			pajService.SetLimitsChecker(&PajLimitsAdapter{limitsService: c.LimitsService})
			pajService.SetDepositLimits(c.LimitsService)
		}
		c.PajHandlers = fundinghandlers.NewPajHandlers(pajService, c.ZapLog)
		c.ZapLog.Info("Paj Cash NGN ramp initialized")
	} else {
		c.ZapLog.Warn("Paj API key is empty, skipping initialization")
	}

	// Initialize unified activity feed service
	activityService := activitysvc.NewService(sqlxDB, c.ZapLog)
	c.ActivityHandlers = activityhandlers.NewHandlers(activityService, c.ZapLog)
	c.ZapLog.Info("Activity feed service initialized")
}

// PajLimitsAdapter adapts limits.Service to pajfunding.WithdrawalLimitsChecker.
type PajLimitsAdapter struct {
	limitsService *limits.Service
}

func (a *PajLimitsAdapter) ValidateWithdrawal(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error {
	result, err := a.limitsService.ValidateWithdrawal(ctx, userID, amount)
	if err != nil {
		return err
	}
	if !result.Allowed {
		return fmt.Errorf("%s", result.Reason)
	}
	return nil
}

func (a *PajLimitsAdapter) ValidateWithdrawalWithCurrency(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, currency string) error {
	result, err := a.limitsService.ValidateWithdrawalWithCurrency(ctx, userID, amount, currency)
	if err != nil {
		return err
	}
	if !result.Allowed {
		return fmt.Errorf("%s", result.Reason)
	}
	return nil
}

// PajDepositLedgerAdapter credits USDC balance for PAJ onramp deposits using the
// correct double-entry direction (Debit = increase user balance).
type PajDepositLedgerAdapter struct {
	ledgerService *ledger.Service
}

func (a *PajDepositLedgerAdapter) CreditUSDCBalance(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, idempotencyKey string, metadata map[string]interface{}) error {
	userAccount, err := a.ledgerService.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeUSDCBalance)
	if err != nil {
		return fmt.Errorf("get user USDC account: %w", err)
	}
	systemAccount, err := a.ledgerService.GetSystemAccount(ctx, entities.AccountTypeSystemBufferUSDC)
	if err != nil {
		return fmt.Errorf("get system buffer account: %w", err)
	}
	desc := "PAJ onramp USDC deposit"
	req := &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeDeposit,
		IdempotencyKey:  idempotencyKey,
		Description:     &desc,
		Metadata:        metadata,
		Entries: []entities.CreateEntryRequest{
			{AccountID: userAccount.ID, EntryType: entities.EntryTypeDebit, Amount: amount, Currency: "USDC", Description: &desc},
			{AccountID: systemAccount.ID, EntryType: entities.EntryTypeCredit, Amount: amount, Currency: "USDC", Description: &desc},
		},
	}
	_, err = a.ledgerService.CreateTransaction(ctx, req)
	return err
}

// InstantFundingAlpacaAdapterImpl adapts alpaca.Service to funding.InstantFundingAlpacaAdapter
type InstantFundingAlpacaAdapterImpl struct {
	service *alpaca.Service
}

func (a *InstantFundingAlpacaAdapterImpl) CreateJournal(ctx context.Context, req *entities.AlpacaJournalRequest) (*entities.AlpacaJournalResponse, error) {
	return a.service.CreateJournal(ctx, req)
}

// GetInvestmentRulesRepo returns the investment rules repository.
func (c *Container) GetInvestmentRulesRepo() *repositories.InvestmentRulesRepository {
	return c.InvestmentRulesRepo
}

// rebalancingStrategyAdapter implements rebalancing_worker.StrategyProvider.
// It returns the target allocations from the user's first active RebalancingConfig.
type rebalancingStrategyAdapter struct {
	configRepo *repositories.RebalancingConfigRepository
}

func (a *rebalancingStrategyAdapter) GetTargetAllocations(ctx context.Context, userID uuid.UUID) (map[string]decimal.Decimal, error) {
	configs, err := a.configRepo.GetByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	for _, cfg := range configs {
		if cfg.Status == entities.ScheduleStatusActive && len(cfg.TargetAllocations) > 0 {
			return cfg.TargetAllocations, nil
		}
	}
	return nil, fmt.Errorf("no active rebalancing config for user %s", userID)
}

// rebalancingOrderAdapter adapts orderPlacerAdapter to rebalancing_worker.OrderPlacer.
type rebalancingOrderAdapter struct {
	inner *orderPlacerAdapter
}

func (a *rebalancingOrderAdapter) PlaceMarketOrder(ctx context.Context, userID uuid.UUID, symbol string, amount decimal.Decimal) (*entities.AlpacaOrderResponse, error) {
	order, err := a.inner.PlaceMarketOrder(ctx, userID, symbol, amount)
	if err != nil {
		return nil, err
	}
	if order.AlpacaOrderID == nil {
		return nil, fmt.Errorf("order placed but no Alpaca order ID returned")
	}
	return &entities.AlpacaOrderResponse{ID: *order.AlpacaOrderID}, nil
}

// GetRebalancingWorkerDeps returns the dependencies needed to start the rebalancing worker.
func (c *Container) GetRebalancingWorkerDeps() (
	rulesRepo *repositories.InvestmentRulesRepository,
	positionRepo *repositories.InvestmentPositionRepository,
	strategyProvider *rebalancingStrategyAdapter,
	orderPlacer *rebalancingOrderAdapter,
) {
	rulesRepo = c.InvestmentRulesRepo
	positionRepo = c.InvestmentPositionRepo
	strategyProvider = &rebalancingStrategyAdapter{configRepo: c.RebalancingConfigRepo}
	orderPlacer = &rebalancingOrderAdapter{inner: &orderPlacerAdapter{
		investingService: c.InvestingService,
		accountService:   c.AlpacaAccountService,
		alpacaClient:     c.AlpacaClient,
		orderRepo:        c.InvestmentOrderRepo,
		logger:           c.ZapLog,
	}}
	return
}

// SubscriptionBridgeTransferAdapter transfers subscription fees from user Bridge wallet to company wallet.
type SubscriptionBridgeTransferAdapter struct {
	bridgeClient      *bridge.Client
	walletRepo        *repositories.WalletRepository
	userRepo          *repositories.UserRepository
	companyWalletAddr string
	logger            *zap.Logger
}

func (a *SubscriptionBridgeTransferAdapter) TransferToCompanyWallet(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, reference string) error {
	if a.companyWalletAddr == "" || a.bridgeClient == nil {
		return fmt.Errorf("bridge transfer not configured")
	}

	wallet, err := a.walletRepo.GetByUserAndChain(ctx, userID, entities.WalletChainSolana)
	if err != nil {
		return fmt.Errorf("failed to get user wallet: %w", err)
	}
	if wallet == nil || wallet.BridgeWalletID == "" {
		return fmt.Errorf("user has no Bridge Solana wallet")
	}

	user, err := a.userRepo.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get user profile: %w", err)
	}
	if user == nil || user.BridgeCustomerID == nil || *user.BridgeCustomerID == "" {
		return fmt.Errorf("user has no Bridge customer ID")
	}

	_, err = a.bridgeClient.CreateTransfer(ctx, &bridge.CreateTransferRequest{
		ClientReferenceID: reference,
		OnBehalfOf:        *user.BridgeCustomerID,
		Amount:            amount.StringFixed(2),
		Source: bridge.TransferSource{
			PaymentRail:    bridge.PaymentRail("bridge_wallet"),
			Currency:       bridge.CurrencyUSDC,
			BridgeWalletID: wallet.BridgeWalletID,
		},
		Destination: bridge.TransferDestination{
			PaymentRail: bridge.PaymentRailSolana,
			Currency:    bridge.CurrencyUSDC,
			ToAddress:   a.companyWalletAddr,
		},
	})
	return err
}

// automationBalanceAdapter adapts LedgerService for automation balance checks.
type automationBalanceAdapter struct {
	ledger *ledger.Service
}

func (a *automationBalanceAdapter) GetSpendBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	if a.ledger == nil {
		return decimal.Zero, fmt.Errorf("ledger not available")
	}
	acct, err := a.ledger.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil {
		return decimal.Zero, err
	}
	return acct.Balance, nil
}

func (a *automationBalanceAdapter) GetStashBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	if a.ledger == nil {
		return decimal.Zero, fmt.Errorf("ledger not available")
	}
	acct, err := a.ledger.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeStashBalance)
	if err != nil {
		return decimal.Zero, err
	}
	return acct.Balance, nil
}

// automationTransferAdapter adapts LedgerService for automation transfers.
type automationTransferAdapter struct {
	ledger *ledger.Service
}

func (a *automationTransferAdapter) TransferBetweenStashes(ctx context.Context, userID uuid.UUID, from, to string, amount decimal.Decimal) error {
	if a.ledger == nil {
		return fmt.Errorf("ledger not available")
	}
	if from == "spend" && to == "stash" {
		return a.ledger.TransferSpendingToStash(ctx, userID, amount, uuid.New().String())
	}
	return a.ledger.TransferStashToSpending(ctx, userID, amount, uuid.New().String())
}

// automationProviderAdapter adapts automation.Service to the AI orchestrator's AutomationProvider interface.
type automationProviderAdapter struct {
	svc *automation.Service
}

func (a *automationProviderAdapter) Create(ctx context.Context, userID uuid.UUID, req *aiservice.AutomationRequest) (*entities.MiriamAutomation, error) {
	return a.svc.Create(ctx, userID, &automation.CreateAutomationRequest{
		Name:              req.Name,
		Description:       req.Description,
		TriggerType:       req.TriggerType,
		TriggerConfig:     req.TriggerConfig,
		ActionType:        req.ActionType,
		ActionConfig:      req.ActionConfig,
		MaxTriggersPerDay: req.MaxTriggersPerDay,
		CooldownMinutes:   req.CooldownMinutes,
		SavingsGoalID:     req.SavingsGoalID,
		ObligationID:      req.ObligationID,
	})
}

func (a *automationProviderAdapter) List(ctx context.Context, userID uuid.UUID) ([]entities.MiriamAutomation, error) {
	return a.svc.List(ctx, userID)
}

// initializeOpportunityService sets up the opportunity intelligence layer.
func (c *Container) initializeOpportunityService(sqlxDB *sqlx.DB) {
	superteamAPIKey := os.Getenv("SUPERTEAM_EARN_API_KEY")
	if superteamAPIKey == "" {
		c.ZapLog.Warn("SUPERTEAM_EARN_API_KEY not set, opportunity service disabled")
		return
	}

	superteamClient := superteamadapter.NewClient(superteamadapter.Config{
		APIKey: superteamAPIKey,
	}, c.ZapLog)

	c.OpportunityRepo = repositories.NewOpportunityRepository(sqlxDB)
	c.OpportunityService = opportunitysvc.NewService(c.OpportunityRepo, superteamClient, c.ZapLog)
}

// GetOpportunityHandlers returns opportunity HTTP handlers.
func (c *Container) GetOpportunityHandlers() *opportunityhandlers.Handlers {
	if c.OpportunityService == nil {
		return nil
	}
	return opportunityhandlers.NewHandlers(c.OpportunityService, c.ZapLog)
}
