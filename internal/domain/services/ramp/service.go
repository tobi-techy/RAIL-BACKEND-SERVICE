// Package ramp is a provider-agnostic NGN on/off ramp orchestrator. It routes
// each order to the best available rate using RampHub (itself a best-rate
// aggregator) as the primary provider and the existing Paj Cash integration as
// a fallback when RampHub is unavailable or has no quote for the corridor.
//
// Settlement reuses Rail's existing machinery: USDC moves through the user's
// Circle wallet (Solana direct, EVM bridged via ChainRails), balances move
// through the double-entry ledger, and completed onramps run the 70/30
// allocation split. The flow mirrors the proven Paj offramp/onramp path.
package ramp

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/pajfunding"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/ramphub"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// Provider identifiers used in responses and persisted metadata.
const (
	ProviderRampHub = "ramphub"
	ProviderPaj     = "paj"
)

// quoteAssets are the assets compared when finding the best rate. USDC only for
// now — the capital engine is USDC-denominated end-to-end. Add more assets here
// once their full credit/debit path (and any swap to USDC) is supported.
var quoteAssets = []string{"USDC"}

// settlementAsset is the asset Rail credits/debits end-to-end (USDC).
const settlementAsset = "USDC"

// offrampSettlementChain is the chain we deliver USDC on for offramps. Mirrors
// the Paj path: always Solana, bridging EVM-held USDC via ChainRails.
const offrampSettlementChain = "solana"

// Service orchestrates RampHub (primary) and Paj (fallback) ramps.
type Service struct {
	db            *sqlx.DB
	ramphubClient *ramphub.Client
	pajFallback   *pajfunding.Service

	ledger            pajfunding.LedgerService
	allocationService pajfunding.AllocationService
	depositLedger     pajfunding.DepositLedgerService
	depositRepo       pajfunding.DepositRepository
	notifier          pajfunding.NotificationService
	walletProvider    pajfunding.WalletProvider
	circleTransfer    pajfunding.CircleTransferAdapter
	chainRailsAdapter pajfunding.ChainRailsAdapter
	limitsChecker     pajfunding.WithdrawalLimitsChecker
	depositLimits     pajfunding.DepositLimitsChecker
	gameplayHooks     pajfunding.GameplayHooks

	developerFeePercent float64

	redis  cache.RedisClient
	logger *zap.Logger
}

// NewService builds the ramp orchestrator. pajFallback may be nil (RampHub-only).
func NewService(db *sqlx.DB, ramphubClient *ramphub.Client, pajFallback *pajfunding.Service, redis cache.RedisClient, logger *zap.Logger) *Service {
	return &Service{db: db, ramphubClient: ramphubClient, pajFallback: pajFallback, redis: redis, logger: logger}
}

func (s *Service) SetLedger(l pajfunding.LedgerService)                    { s.ledger = l }
func (s *Service) SetAllocationService(a pajfunding.AllocationService)     { s.allocationService = a }
func (s *Service) SetDepositLedger(d pajfunding.DepositLedgerService)      { s.depositLedger = d }
func (s *Service) SetDepositRepository(r pajfunding.DepositRepository)     { s.depositRepo = r }
func (s *Service) SetNotificationService(n pajfunding.NotificationService) { s.notifier = n }
func (s *Service) SetWalletProvider(w pajfunding.WalletProvider)           { s.walletProvider = w }
func (s *Service) SetCircleTransfer(c pajfunding.CircleTransferAdapter)    { s.circleTransfer = c }
func (s *Service) SetChainRailsAdapter(c pajfunding.ChainRailsAdapter)     { s.chainRailsAdapter = c }
func (s *Service) SetLimitsChecker(l pajfunding.WithdrawalLimitsChecker)   { s.limitsChecker = l }
func (s *Service) SetDepositLimits(d pajfunding.DepositLimitsChecker)      { s.depositLimits = d }
func (s *Service) SetGameplayHooks(g pajfunding.GameplayHooks)             { s.gameplayHooks = g }

// SetDeveloperFeePercent sets Rail's business fee % applied to every order.
func (s *Service) SetDeveloperFeePercent(pct float64) { s.developerFeePercent = pct }

// WebhookSecret exposes the configured RampHub signing secret for the handler.
func (s *Service) WebhookSecret() string { return s.ramphubClient.WebhookSecret() }

// IsSandbox reports whether the RampHub client is in sandbox mode. The handler
// uses this to decide whether to accept webhook events with livemode:false.
func (s *Service) IsSandbox() bool { return s.ramphubClient != nil && s.ramphubClient.IsSandbox() }

func contextWithTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 3*time.Minute)
}

// --- Quotes ---

// Quote is a unified, provider-agnostic best rate.
//   - Onramp: EstimatedOutput and TokenAmount are the USDC the user receives.
//   - Offramp: EstimatedOutput is the NGN the user receives; TokenAmount is the
//     USDC it costs.
type Quote struct {
	Side            string  `json:"side"`
	Provider        string  `json:"provider"`
	Asset           string  `json:"asset"`
	Chain           string  `json:"chain"`
	Currency        string  `json:"currency"`
	Rate            float64 `json:"rate"`
	EstimatedOutput float64 `json:"estimatedOutput"`
	TokenAmount     float64 `json:"tokenAmount"`
	Fee             float64 `json:"fee"`
}

// GetBestQuote returns the single best rate for a corridor on the default
// settlement chain. For onramp it compares USDC and USDT via RampHub; for
// offramp it quotes USDC. If RampHub is unavailable it falls back to Paj.
func (s *Service) GetBestQuote(ctx context.Context, side string, fiatAmount, tokenAmount float64, currency string) (*Quote, error) {
	return s.getBestQuoteForChain(ctx, side, fiatAmount, tokenAmount, currency, offrampSettlementChain)
}

// getBestQuoteForChain is GetBestQuote for a specific chain — the quote and the
// subsequent order must use the same corridor or providers can reject the order.
func (s *Service) getBestQuoteForChain(ctx context.Context, side string, fiatAmount, tokenAmount float64, currency, chain string) (*Quote, error) {
	side = strings.ToLower(strings.TrimSpace(side))
	if currency == "" {
		currency = "NGN"
	}
	if chain == "" {
		chain = offrampSettlementChain
	}

	buy := normalizeSide(side) == "buy"

	var best *Quote
	if s.ramphubClient != nil {
		// Compare every supported asset and keep the best for the side. RampHub
		// already routes to the best provider per asset; this picks the best asset.
		// Currently USDC-only.
		// Sell quotes need a token amount; use the caller's, else a probe (the
		// rate is effectively amount-independent and we recompute for the real
		// fiat target below).
		probeToken := tokenAmount
		if !buy && probeToken <= 0 {
			probeToken = 10.0
		}
		for _, asset := range quoteAssets {
			req := ramphub.QuoteRequest{Side: normalizeSide(side), FiatCurrency: currency, Asset: asset, Chain: chain}
			if buy {
				req.FiatAmount = fiatAmount
			} else {
				req.TokenAmount = probeToken
			}
			resp, err := s.ramphubClient.GetQuote(ctx, req)
			if err != nil {
				s.logger.Warn("ramphub quote failed", zap.String("asset", asset), zap.Error(err))
				continue
			}
			q := &Quote{
				Side: side, Provider: ProviderRampHub, Asset: asset, Chain: chain, Currency: currency,
				Rate: resp.BestQuote.Rate, Fee: resp.BestQuote.Fee,
			}
			if buy {
				q.EstimatedOutput = resp.BestQuote.EstimatedOutput // USDC received
				q.TokenAmount = resp.BestQuote.EstimatedOutput
			} else {
				q.EstimatedOutput = fiatAmount // NGN received (user's target)
				if q.Rate > 0 {
					q.TokenAmount = fiatAmount / q.Rate // USDC cost
				}
			}
			if best == nil || betterQuote(buy, q, best) {
				best = q
			}
		}
	}

	if best != nil {
		return best, nil
	}

	// Fallback: Paj rate.
	if s.pajFallback != nil {
		rates, err := s.pajFallback.GetRates(ctx)
		if err == nil && rates != nil {
			rate := rates.OnRampRate.Rate
			if !buy {
				rate = rates.OffRampRate.Rate
			}
			if rate > 0 {
				q := &Quote{
					Side: side, Provider: ProviderPaj, Asset: settlementAsset, Chain: offrampSettlementChain,
					Currency: currency, Rate: rate,
				}
				if buy {
					q.EstimatedOutput = fiatAmount / rate
					q.TokenAmount = q.EstimatedOutput
				} else {
					q.EstimatedOutput = fiatAmount
					q.TokenAmount = fiatAmount / rate
				}
				return q, nil
			}
		}
	}

	return nil, fmt.Errorf("no quote available for %s %s", side, currency)
}

// betterQuote reports whether a is a better quote than b for the side. For buy,
// more USDC received wins; for sell, a higher NGN-per-USDC rate wins (fewer USDC
// for the same payout).
func betterQuote(buy bool, a, b *Quote) bool {
	if buy {
		return a.EstimatedOutput > b.EstimatedOutput
	}
	return a.Rate > b.Rate
}

func normalizeSide(side string) string {
	switch strings.ToLower(side) {
	case "onramp", "buy":
		return "buy"
	case "offramp", "sell":
		return "sell"
	default:
		return side
	}
}

// --- Banks ---

const ramphubBanksCacheKey = "ramphub:banks"
const ramphubBanksCacheTTL = 24 * time.Hour

// GetBanks returns the unified payout bank list. RampHub exposes banks per
// provider, so we union the lists of every sell-capable provider and dedup by
// bank code, giving the user one complete list regardless of which provider
// ultimately wins the route. Cached for 24h.
func (s *Service) GetBanks(ctx context.Context) ([]ramphub.Bank, error) {
	if s.redis != nil {
		var cached []ramphub.Bank
		if err := s.redis.Get(ctx, ramphubBanksCacheKey, &cached); err == nil && len(cached) > 0 {
			return cached, nil
		}
	}

	catalog, err := s.ramphubClient.GetCatalog(ctx)
	if err != nil {
		return nil, err
	}

	// Fetch every sell-capable provider's list concurrently — this is the
	// cold-cache path and providers are independent.
	var sellProviders []string
	for _, p := range catalog.Providers {
		if p.SupportsSell {
			sellProviders = append(sellProviders, strings.ToLower(p.Name))
		}
	}
	lists := make([][]ramphub.Bank, len(sellProviders))
	var wg sync.WaitGroup
	for i, name := range sellProviders {
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			list, lerr := s.ramphubClient.GetProviderBankList(ctx, name)
			if lerr != nil {
				s.logger.Warn("ramphub provider bank list failed, skipping provider",
					zap.String("provider", name), zap.Error(lerr))
				return
			}
			lists[i] = list.Banks
		}(i, name)
	}
	wg.Wait()

	// Bank codes are provider-scoped, so the same institution can appear under
	// several codes. Dedupe by code and by normalized name so the user sees each
	// bank once; RampHub converts whichever code we send to the winning
	// provider's directory internally.
	seenCode := make(map[string]bool)
	seenName := make(map[string]bool)
	var banks []ramphub.Bank
	for _, list := range lists {
		for _, b := range list {
			nameKey := strings.ToLower(strings.Join(strings.Fields(b.BankName), " "))
			if b.BankCode == "" || seenCode[b.BankCode] || nameKey == "" || seenName[nameKey] {
				continue
			}
			seenCode[b.BankCode] = true
			seenName[nameKey] = true
			banks = append(banks, b)
		}
	}
	if len(banks) == 0 {
		return nil, fmt.Errorf("no banks available")
	}

	sort.Slice(banks, func(i, j int) bool { return banks[i].BankName < banks[j].BankName })

	if s.redis != nil && len(banks) > 0 {
		if cacheErr := s.redis.Set(ctx, ramphubBanksCacheKey, banks, ramphubBanksCacheTTL); cacheErr != nil {
			s.logger.Warn("failed to cache ramphub banks", zap.Error(cacheErr))
		}
	}
	return banks, nil
}

// nubanPattern matches Nigerian NUBAN account numbers (exactly 10 digits).
var nubanPattern = regexp.MustCompile(`^\d{10}$`)

// bankCodePattern matches RampHub bank codes. Codes are provider-scoped and
// vary in length (NIP codes, fintech codes like 999992), so allow 3–12
// alphanumerics — enough to reject junk without rejecting a valid directory.
var bankCodePattern = regexp.MustCompile(`^[A-Za-z0-9]{3,12}$`)

// ResolveBankAccount validates payout bank details before an offramp. bankName
// is optional but improves match rates because RampHub bank codes are
// provider-scoped.
func (s *Service) ResolveBankAccount(ctx context.Context, bankCode, accountNumber, bankName string) (*ramphub.ResolvedAccount, error) {
	if !nubanPattern.MatchString(accountNumber) {
		return nil, fmt.Errorf("account number must be exactly 10 digits")
	}
	if !bankCodePattern.MatchString(bankCode) {
		return nil, fmt.Errorf("invalid bank code")
	}
	return s.ramphubClient.ResolveBankAccount(ctx, bankCode, accountNumber, bankName)
}

// --- Onramp (NGN → USDC) ---

// OnrampResult is returned to the API after creating an onramp order.
type OnrampResult struct {
	TransactionID string  `json:"transactionId"`
	Provider      string  `json:"provider"`
	AccountNumber string  `json:"accountNumber"`
	AccountName   string  `json:"accountName"`
	Bank          string  `json:"bank"`
	FiatAmount    float64 `json:"fiatAmount"`
	Rate          float64 `json:"rate"`
	Asset         string  `json:"asset"`
}

// CreateOnramp creates an NGN→USDC deposit order. RampHub sends USDC directly to
// the user's Circle wallet; crediting happens via on-chain deposit detection,
// with the completed webhook as a backstop.
func (s *Service) CreateOnramp(ctx context.Context, userID uuid.UUID, fiatAmount float64, currency string) (*OnrampResult, error) {
	if currency == "" {
		currency = "NGN"
	}
	if fiatAmount < float64(pajfunding.MinNGNTransactionAmount) {
		return nil, fmt.Errorf("minimum deposit is ₦%.0f", float64(pajfunding.MinNGNTransactionAmount))
	}

	// Duplicate protection: reject a same-amount pending onramp in the last 30s.
	var hasDuplicate bool
	if err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM ramphub_orders WHERE user_id = $1 AND order_type = 'onramp' AND fiat_amount = $2 AND status NOT IN ('completed','failed') AND created_at > NOW() - interval '30 seconds')`,
		userID, fiatAmount).Scan(&hasDuplicate); err != nil {
		return nil, fmt.Errorf("failed to check for duplicate deposit: %w", err)
	}
	if hasDuplicate {
		return nil, fmt.Errorf("deposit already in progress, please wait")
	}

	// Resolve the user's wallet first: the quote must be priced on the chain
	// the order will actually deliver to, or providers can reject the order
	// (which silently pushed every EVM-wallet deposit onto the Paj fallback).
	walletAddress, walletChain := s.resolveUserWallet(ctx, userID)
	if walletAddress == "" {
		return nil, fmt.Errorf("no wallet available to receive deposit")
	}
	rampChain := circleToRampChain(walletChain)

	// Fetch the best onramp quote once and reuse it for both limit conversion and
	// the crypto amount on the order (avoids a second live quote round-trip).
	quote, qErr := s.getBestQuoteForChain(ctx, "onramp", fiatAmount, 0, currency, rampChain)
	quoteRate := 0.0
	if qErr == nil && quote != nil {
		quoteRate = quote.Rate
	}

	// Enforce deposit limits (convert NGN→USD via the best rate for non-KYC caps).
	if s.depositLimits != nil {
		if err := s.enforceDepositLimits(ctx, userID, fiatAmount, currency, quoteRate); err != nil {
			return nil, err
		}
	}

	// Try RampHub first; fall back to Paj on failure. A buy order requires the
	// crypto amount, computed from the quote rate above.
	if s.ramphubClient != nil {
		if qErr == nil && quote.Provider == ProviderRampHub && quote.Rate > 0 {
			cryptoAmount := decimal.NewFromFloat(fiatAmount).Div(decimal.NewFromFloat(quote.Rate)).Round(6)
			order, err := s.createRampHubBuyOrder(ctx, userID, fiatAmount, cryptoAmount.InexactFloat64(), currency, settlementAsset, rampChain, walletAddress)
			if err == nil {
				acctNum, acctName, bankName := buyBankDetails(order)
				if perr := s.persistOnrampOrder(ctx, userID, order, fiatAmount, cryptoAmount.InexactFloat64(), currency, settlementAsset, rampChain, acctNum, acctName, bankName); perr != nil {
					s.logger.Error("CRITICAL: failed to persist ramphub onramp order", zap.Error(perr), zap.String("ramphub_tx_id", order.TransactionID))
					return nil, fmt.Errorf("failed to create deposit order, please try again")
				}
				return &OnrampResult{
					TransactionID: order.TransactionID,
					Provider:      ProviderRampHub,
					AccountNumber: acctNum,
					AccountName:   acctName,
					Bank:          bankName,
					FiatAmount:    fiatAmount,
					Rate:          order.BestRateUsed,
					Asset:         settlementAsset,
				}, nil
			}
			s.logger.Warn("ramphub onramp failed, falling back to Paj", zap.Error(err), zap.String("user_id", userID.String()))
		} else {
			s.logger.Warn("ramphub onramp quote unavailable, falling back to Paj", zap.String("user_id", userID.String()))
		}
	}

	// Paj fallback.
	if s.pajFallback != nil {
		pajOrder, err := s.pajFallback.CreateOnrampOrder(ctx, userID, fiatAmount, currency)
		if err != nil {
			return nil, err
		}
		return &OnrampResult{
			TransactionID: pajOrder.ID,
			Provider:      ProviderPaj,
			AccountNumber: pajOrder.AccountNumber,
			AccountName:   pajOrder.AccountName,
			Bank:          pajOrder.Bank,
			FiatAmount:    pajOrder.FiatAmount,
			Rate:          pajOrder.Rate,
			Asset:         settlementAsset,
		}, nil
	}

	return nil, fmt.Errorf("onramp temporarily unavailable")
}

func (s *Service) createRampHubBuyOrder(ctx context.Context, userID uuid.UUID, fiatAmount, cryptoAmount float64, currency, asset, chain, walletAddress string) (*ramphub.OrderResponse, error) {
	req := ramphub.OrderRequest{
		Side:               "buy",
		Amount:             cryptoAmount,
		FiatAmount:         fiatAmount,
		FiatCurrency:       currency,
		Asset:              asset,
		Chain:              chain,
		WalletAddress:      walletAddress,
		ExternalCustomerID: userID.String(),
		// Deposits are free — no developer fee on buy orders.
	}
	order, err := s.ramphubClient.CreateOrder(ctx, req)
	if err != nil && ramphub.IsActiveIntentConflict(err) {
		// Take over the existing payment window and retry once.
		req.OverrideActiveIntent = true
		order, err = s.ramphubClient.CreateOrder(ctx, req)
	}
	return order, err
}

// buyBankDetails extracts the virtual-account the customer must pay into.
func buyBankDetails(order *ramphub.OrderResponse) (accountNumber, accountName, bankName string) {
	if va := order.ProviderDetails.VirtualAccount; va != nil {
		return va.AccountNumber, va.AccountName, va.BankName
	}
	return "", "", ""
}

func (s *Service) persistOnrampOrder(ctx context.Context, userID uuid.UUID, order *ramphub.OrderResponse, fiatAmount, expectedToken float64, currency, asset, chain, acctNum, acctName, bankName string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO ramphub_orders (user_id, ramphub_transaction_id, request_reference, order_type, status, selected_provider, fiat_amount, token_amount, asset, chain, currency, rate, pay_account_number, pay_account_name, pay_bank, used_user_wallet)
		VALUES ($1,$2,$3,'onramp','pending',$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,true)`,
		userID, order.TransactionID, order.RequestReference, order.SelectedProvider, fiatAmount, expectedToken, asset, chain, currency,
		order.BestRateUsed, acctNum, acctName, bankName)
	return err
}

// --- Offramp (USDC → NGN) ---

// OfframpResult is returned to the API after creating an offramp order.
type OfframpResult struct {
	TransactionID string  `json:"transactionId"`
	Provider      string  `json:"provider"`
	FiatAmount    float64 `json:"fiatAmount"`
	Rate          float64 `json:"rate"`
	RailFee       float64 `json:"railFee"`
	Status        string  `json:"status"`
}

// CreateOfframp creates a USDC→NGN withdrawal order. Mirrors the Paj offramp:
// verify payout account → ledger hold → RampHub sell order → async
// Circle/ChainRails delivery to RampHub's deposit address → reconcile via
// signed webhook + poll. bankName is optional but improves RampHub's
// provider-scoped bank code matching.
func (s *Service) CreateOfframp(ctx context.Context, userID uuid.UUID, bankCode, accountNumber, bankName string, fiatAmount float64, currency string) (*OfframpResult, error) {
	if currency == "" {
		currency = "NGN"
	}
	if fiatAmount < float64(pajfunding.MinNGNTransactionAmount) {
		return nil, fmt.Errorf("minimum withdrawal is ₦%.0f", float64(pajfunding.MinNGNTransactionAmount))
	}
	if !nubanPattern.MatchString(accountNumber) {
		return nil, fmt.Errorf("account number must be exactly 10 digits")
	}
	if !bankCodePattern.MatchString(bankCode) {
		return nil, fmt.Errorf("invalid bank code")
	}

	unlock, lockErr := s.acquireOfframpLock(ctx, userID)
	if lockErr != nil {
		return nil, fmt.Errorf("withdrawal in progress, please wait")
	}
	defer unlock()

	// Idempotency: reject duplicate requests within 30s.
	var exists bool
	if err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM ramphub_orders WHERE user_id = $1 AND bank_code = $2 AND fiat_amount = $3 AND created_at > NOW() - interval '30 seconds' AND status != 'failed')`,
		userID, bankCode, fiatAmount).Scan(&exists); err != nil {
		return nil, fmt.Errorf("failed to check for duplicate withdrawal: %w", err)
	}
	if exists {
		return nil, fmt.Errorf("duplicate withdrawal request, please wait")
	}

	// Best offramp rate (RampHub primary, Paj fallback) to size the hold.
	quote, err := s.GetBestQuote(ctx, "offramp", fiatAmount, 0, currency)
	if err != nil || quote.Rate <= 0 {
		return nil, fmt.Errorf("offramp rate unavailable")
	}
	if quote.Rate < 100 || quote.Rate > 10000 {
		return nil, fmt.Errorf("offramp rate out of bounds: %.2f", quote.Rate)
	}

	// Verify the payout account before any money moves. RampHub requires the
	// resolved account name on sell orders, and failing here costs nothing to
	// unwind — no hold has been taken yet.
	accountName := ""
	rampHubViable := s.ramphubClient != nil && quote.Provider == ProviderRampHub
	if rampHubViable {
		resolved, rerr := s.ramphubClient.ResolveBankAccount(ctx, bankCode, accountNumber, bankName)
		switch {
		case rerr == nil:
			accountName = resolved.AccountName
		case errors.Is(rerr, ramphub.ErrAccountResolveFailed):
			// The account itself didn't resolve — a wrong number or bank, not a
			// provider outage. Refuse rather than pay out to an unverified account.
			return nil, fmt.Errorf("unable to verify bank account details, please check the account number and bank")
		default:
			// RampHub unreachable — skip its order path and use the Paj fallback.
			s.logger.Warn("ramphub resolve unavailable, using Paj fallback", zap.Error(rerr))
			rampHubViable = false
		}
	}
	if !rampHubViable {
		if s.pajFallback != nil {
			pajRes, pajErr := s.pajFallback.CreateOfframpOrder(ctx, userID, bankCode, accountNumber, fiatAmount, currency)
			if pajErr != nil {
				return nil, pajErr
			}
			return &OfframpResult{
				TransactionID: pajRes.Order.ID, Provider: ProviderPaj,
				FiatAmount: pajRes.Order.FiatAmount, Rate: pajRes.Order.Rate,
				RailFee: pajRes.RailFee, Status: "pending",
			}, nil
		}
		return nil, fmt.Errorf("withdrawal service temporarily unavailable")
	}

	// Estimate USDC: fiat/rate + 2% slippage buffer, capped at $50 to prevent
	// over-holding on large orders. The 0.5% developer fee is accrued by RampHub
	// on Rail's behalf (reduces NGN payout, not the USDC hold).
	// Tiered cap: $50 base for orders ≤$2,500; for larger orders add 0.5% of
	// the amount above $2,500 to accommodate rate volatility on high-value withdrawals.
	baseUSDC := decimal.NewFromFloat(fiatAmount).Div(decimal.NewFromFloat(quote.Rate)).Round(2)
	slippageBuf := baseUSDC.Mul(decimal.NewFromFloat(0.02))
	maxSlippage := decimal.NewFromFloat(50)
	tierFiat := decimal.NewFromFloat(2500)
	if decimal.NewFromFloat(fiatAmount).GreaterThan(tierFiat) {
		maxSlippage = maxSlippage.Add(decimal.NewFromFloat(fiatAmount).Sub(tierFiat).Div(decimal.NewFromFloat(quote.Rate)).Mul(decimal.NewFromFloat(0.005)))
	}
	if slippageBuf.GreaterThan(maxSlippage) {
		slippageBuf = maxSlippage
	}
	estimatedUSDC := baseUSDC.Add(slippageBuf).Round(2)
	railFee := decimal.Zero
	totalHold := estimatedUSDC

	// Withdrawal limits.
	if s.limitsChecker != nil {
		if limErr := s.limitsChecker.ValidateWithdrawalWithCurrency(ctx, userID, decimal.NewFromFloat(fiatAmount), currency); limErr != nil {
			return nil, limErr
		}
	}

	// Balance check + hold.
	if s.ledger != nil {
		balance, err := s.ledger.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
		if err != nil {
			return nil, fmt.Errorf("failed to check balance: %w", err)
		}
		if balance.LessThan(totalHold) {
			return nil, fmt.Errorf("insufficient balance: have %s USDC, need ~%s USDC (incl. $%s fee) for ₦%.0f",
				balance.String(), totalHold.String(), railFee.String(), fiatAmount)
		}
		if err := s.ledger.CreateTransaction(ctx, userID, entities.AccountTypeSpendingBalance,
			entities.TransactionTypeWithdrawal, totalHold, map[string]interface{}{
				"provider": ProviderRampHub, "type": "offramp_hold", "fiat_amount": fiatAmount,
				"currency": currency, "rail_fee": railFee.String(),
			}); err != nil {
			return nil, fmt.Errorf("failed to debit balance: %w", err)
		}
	}

	// A sell order requires the crypto amount (NGN payout is derived from it).
	orderCrypto := decimal.NewFromFloat(fiatAmount).Div(decimal.NewFromFloat(quote.Rate)).Round(2)
	order, err := s.ramphubClient.CreateOrder(ctx, ramphub.OrderRequest{
		Side:                "sell",
		Amount:              orderCrypto.InexactFloat64(),
		FiatCurrency:        currency,
		Asset:               settlementAsset,
		Chain:               offrampSettlementChain,
		BankCode:            bankCode,
		AccountNumber:       accountNumber,
		AccountName:         accountName,
		BankName:            bankName,
		ExternalCustomerID:  userID.String(),
		DeveloperFeePercent: s.developerFeePercent,
	})
	if err != nil {
		// Reverse hold and try Paj fallback (Paj uses its own hold internally).
		s.reverseInitialHold(ctx, userID, totalHold, railFee, "ramphub_api_error")
		if s.pajFallback != nil {
			s.logger.Warn("ramphub offramp failed, falling back to Paj", zap.Error(err))
			pajRes, pajErr := s.pajFallback.CreateOfframpOrder(ctx, userID, bankCode, accountNumber, fiatAmount, currency)
			if pajErr != nil {
				return nil, pajErr
			}
			return &OfframpResult{
				TransactionID: pajRes.Order.ID, Provider: ProviderPaj,
				FiatAmount: pajRes.Order.FiatAmount, Rate: pajRes.Order.Rate,
				RailFee: pajRes.RailFee, Status: "pending",
			}, nil
		}
		return nil, err
	}

	// Normalize: top-level ourCryptoAddress takes precedence; fall back to
	// providerDetails.depositAddress in case RampHub only populates the nested field.
	if order.OurCryptoAddress == "" && order.ProviderDetails.DepositAddress != "" {
		order.OurCryptoAddress = order.ProviderDetails.DepositAddress
	}
	if order.OurCryptoAddress == "" {
		s.logger.Error("RampHub sell order missing deposit address — reversing hold", zap.String("ramphub_tx_id", order.TransactionID))
		s.reverseInitialHold(ctx, userID, totalHold, railFee, "no_deposit_address")
		return nil, fmt.Errorf("withdrawal service returned invalid response")
	}

	// Persist order.
	if _, dbErr := s.db.ExecContext(ctx, `
		INSERT INTO ramphub_orders (user_id, ramphub_transaction_id, request_reference, order_type, status, selected_provider, fiat_amount, token_amount, asset, chain, currency, rate, rail_fee_usdc, hold_amount, bank_code, account_number, account_name, bank_name, our_crypto_address)
		VALUES ($1,$2,$3,'offramp','pending',$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`,
		userID, order.TransactionID, order.RequestReference, order.SelectedProvider, fiatAmount, cryptoSendAmount(order, fiatAmount, quote.Rate),
		settlementAsset, offrampSettlementChain, currency, order.BestRateUsed, railFee, totalHold,
		bankCode, accountNumber, accountName, bankName, order.OurCryptoAddress); dbErr != nil {
		s.logger.Error("CRITICAL: failed to persist ramphub offramp order — reversing hold", zap.Error(dbErr), zap.String("ramphub_tx_id", order.TransactionID))
		s.reverseHold(ctx, userID, order.TransactionID, totalHold, railFee, "db_fail")
		return nil, fmt.Errorf("failed to record withdrawal order")
	}

	if s.circleTransfer == nil {
		s.logger.Error("Circle transfer adapter not configured for RampHub offramp — reversing hold", zap.String("ramphub_tx_id", order.TransactionID))
		s.reverseHold(ctx, userID, order.TransactionID, totalHold, railFee, "no_transfer_config")
		return nil, fmt.Errorf("withdrawal infrastructure not available")
	}

	cryptoAmount := decimal.NewFromFloat(cryptoSendAmount(order, fiatAmount, quote.Rate))
	// WithoutCancel: ctx is the HTTP request context, which Gin cancels the
	// moment the response is written — the transfer must outlive the request.
	transferCtx, transferCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
	go func() {
		defer transferCancel()
		defer func() {
			if r := recover(); r != nil {
				s.logger.Error("CRITICAL: panic in Circle transfer goroutine",
					zap.String("ramphub_tx_id", order.TransactionID),
					zap.Any("panic", r),
					zap.Stack("stack"))
				s.reverseHold(context.Background(), userID, order.TransactionID, totalHold, railFee, "transfer_panic")
			}
		}()
		s.executeCircleTransferToRampHub(transferCtx, userID, order, cryptoAmount, totalHold, railFee)
	}()

	// Record usage against limits (non-blocking).
	if s.limitsChecker != nil {
		if recErr := s.limitsChecker.RecordWithdrawalWithCurrency(ctx, userID, decimal.NewFromFloat(fiatAmount), currency); recErr != nil {
			s.logger.Warn("failed to record withdrawal usage (non-blocking)", zap.Error(recErr), zap.String("user_id", userID.String()))
		}
	}

	feeDisplayNGN := fiatAmount * s.developerFeePercent / 100
	return &OfframpResult{
		TransactionID: order.TransactionID, Provider: ProviderRampHub,
		FiatAmount: fiatAmount, Rate: order.BestRateUsed, RailFee: feeDisplayNGN, Status: "pending",
	}, nil
}

// cryptoSendAmount determines how much USDC to send for a sell order, preferring
// RampHub's authoritative amountToSend, then falling back to fiat/rate.
func cryptoSendAmount(order *ramphub.OrderResponse, fiatAmount, rate float64) float64 {
	if order.ProviderDetails.AmountToSend > 0 {
		return order.ProviderDetails.AmountToSend
	}
	if order.BestRateUsed > 0 {
		return fiatAmount / order.BestRateUsed
	}
	if rate > 0 {
		return fiatAmount / rate
	}
	return 0
}

// reverseInitialHold reverses the hold taken before the order was persisted
// (no order row exists yet to claim).
func (s *Service) reverseInitialHold(ctx context.Context, userID uuid.UUID, amount, railFee decimal.Decimal, reason string) {
	if s.ledger == nil {
		return
	}
	key := fmt.Sprintf("ramphub_offramp_prepersist_%s_%d", userID, time.Now().UnixNano())
	if err := s.ledger.ReverseTransaction(ctx, userID, entities.AccountTypeSpendingBalance, key, amount, map[string]interface{}{
		"provider": ProviderRampHub, "type": "offramp_prepersist_reversal", "reason": reason,
		"rail_fee": railFee.String(), "fee_revenue_posted": true,
	}); err != nil {
		s.logger.Error("CRITICAL: failed to reverse RampHub pre-persist hold", zap.Error(err), zap.String("user_id", userID.String()))
	}
}

// --- Webhook + status reconciliation ---

// HandleWebhook processes a verified RampHub webhook event. Because RampHub
// signs its webhooks, the verified payload is trusted directly. deliveryID is
// the x-ramphub-delivery header used to dedupe retried deliveries.
func (s *Service) HandleWebhook(ctx context.Context, deliveryID string, event *ramphub.WebhookEvent) error {
	txID := event.Data.TransactionID
	if txID == "" {
		return nil
	}

	// Verify the order exists before recording delivery — if the order hasn't
	// been persisted yet (race with create path), return an error so RampHub
	// retries the webhook instead of permanently consuming the delivery_id.
	var userID uuid.UUID
	var currentStatus, orderType string
	err := s.db.QueryRowContext(ctx,
		`SELECT user_id, status, order_type FROM ramphub_orders WHERE ramphub_transaction_id = $1`, txID).
		Scan(&userID, &currentStatus, &orderType)
	if err == sql.ErrNoRows {
		s.logger.Warn("ramphub webhook for unknown order — will retry", zap.String("ramphub_tx_id", txID))
		return fmt.Errorf("order not yet persisted for tx %s", txID)
	}
	if err != nil {
		return fmt.Errorf("lookup ramphub order: %w", err)
	}
	if currentStatus == "completed" || currentStatus == "failed" {
		return nil
	}

	newStatus := ramphub.MapEventStatus(event.Type, event.Data.Status)
	if _, uerr := s.db.ExecContext(ctx, `
		UPDATE ramphub_orders SET status = $1, last_webhook_status = $2, last_webhook_at = NOW(), updated_at = NOW()
		WHERE ramphub_transaction_id = $3 AND status NOT IN ('completed','failed')`,
		newStatus, event.Type+":"+event.Data.Status, txID); uerr != nil {
		// Fail the delivery so RampHub retries — otherwise the order status and
		// the credit/reversal work below could diverge from provider truth.
		return fmt.Errorf("update ramphub order status: %w", uerr)
	}

	// Surface processing failures so the handler returns a non-2xx and RampHub
	// retries the delivery (credit/reversal are idempotent on retry).
	// Delivery-level idempotency is recorded only on success so a failed
	// credit/reversal does not permanently consume the delivery ID — RampHub
	// can retry and the work will be attempted again.
	credErr := s.creditOnrampIfCompleted(ctx, userID, txID, newStatus, event.Data.TokenAmount, event.Data.FiatAmount, event.Data.TxHash)
	revErr := s.reverseOfframpIfFailed(ctx, userID, txID, orderType, newStatus)
	s.notifyTerminal(ctx, userID, txID, orderType, newStatus, event.Data.FiatAmount)
	if credErr != nil {
		return credErr
	}
	if revErr != nil {
		return revErr
	}

	// Record the delivery ID only after all work succeeds so retries are not
	// permanently suppressed by a failed prior attempt.
	if deliveryID != "" {
		res, err := s.db.ExecContext(ctx,
			`INSERT INTO ramphub_webhook_deliveries (delivery_id, event_type, transaction_id)
			 VALUES ($1, $2, $3) ON CONFLICT (delivery_id) DO NOTHING`,
			deliveryID, event.Type, txID)
		if err != nil {
			return fmt.Errorf("record webhook delivery: %w", err)
		}
		if affected, _ := res.RowsAffected(); affected == 0 {
			s.logger.Info("ramphub webhook delivery already recorded",
				zap.String("delivery_id", deliveryID), zap.String("ramphub_tx_id", txID))
		}
	}
	return nil
}

// PollOrderStatus fetches the latest status from RampHub, verifying ownership.
func (s *Service) PollOrderStatus(ctx context.Context, userID uuid.UUID, txID string) (*ramphub.Transaction, error) {
	if s.redis != nil {
		pollKey := fmt.Sprintf("ramphub:poll:%s", userID.String())
		var exists bool
		if err := s.redis.Get(ctx, pollKey, &exists); err == nil {
			return nil, fmt.Errorf("please wait a few seconds before checking again")
		}
		s.redis.Set(ctx, pollKey, true, 5*time.Second)
	}

	var ownerID uuid.UUID
	var orderType string
	if err := s.db.QueryRowContext(ctx,
		`SELECT user_id, order_type FROM ramphub_orders WHERE ramphub_transaction_id = $1`, txID).Scan(&ownerID, &orderType); err != nil {
		// Orders created during a Paj fallback return a Paj order ID, which has
		// no ramphub_orders row. Poll it transparently so the client's status
		// screen keeps working regardless of which provider won the route.
		if errors.Is(err, sql.ErrNoRows) && s.pajFallback != nil {
			return s.pollPajFallbackOrder(ctx, userID, txID)
		}
		return nil, fmt.Errorf("order not found")
	}
	if ownerID != userID {
		return nil, fmt.Errorf("order not found")
	}

	tx, err := s.ramphubClient.MonitorStatus(ctx, txID)
	if err != nil {
		return nil, err
	}

	newStatus := tx.MappedStatus()
	if _, uerr := s.db.ExecContext(ctx, `
		UPDATE ramphub_orders SET status = $1, token_amount = COALESCE(NULLIF($2,0), token_amount), rate = COALESCE(NULLIF($3,0), rate), updated_at = NOW()
		WHERE ramphub_transaction_id = $4 AND status NOT IN ('completed','failed')`,
		newStatus, tx.TokenAmount, tx.Rate, txID); uerr != nil {
		s.logger.Error("failed to persist polled ramphub status", zap.Error(uerr), zap.String("ramphub_tx_id", txID))
	}

	// Poll path: log processing failures but still return the status to the user;
	// the webhook/recovery paths retry crediting/reversal idempotently.
	if err := s.creditOnrampIfCompleted(ctx, userID, txID, newStatus, tx.TokenAmount, tx.FiatAmount, tx.TxHash); err != nil {
		s.logger.Warn("poll: onramp credit pending", zap.Error(err), zap.String("ramphub_tx_id", txID))
	}
	if err := s.reverseOfframpIfFailed(ctx, userID, txID, orderType, newStatus); err != nil {
		s.logger.Warn("poll: offramp reversal pending", zap.Error(err), zap.String("ramphub_tx_id", txID))
	}
	s.notifyTerminal(ctx, userID, txID, orderType, newStatus, tx.FiatAmount)
	return tx, nil
}

// pollPajFallbackOrder polls a Paj order through the fallback service and
// adapts the result to the RampHub transaction shape, so orders that routed to
// Paj report status through the same /ramp endpoint the client already polls.
// Paj's PollOrderStatus verifies ownership and handles credit/reversal itself.
func (s *Service) pollPajFallbackOrder(ctx context.Context, userID uuid.UUID, pajOrderID string) (*ramphub.Transaction, error) {
	tx, err := s.pajFallback.PollOrderStatus(ctx, userID, pajOrderID)
	if err != nil {
		return nil, err
	}
	// Paj statuses: INIT, PAID, COMPLETED, FAILED.
	status := "pending"
	switch tx.Status {
	case "PAID":
		status = "paid"
	case "COMPLETED":
		status = "completed"
	case "FAILED":
		status = "failed"
	}
	side := "buy"
	if tx.TransactionType == "OFF_RAMP" {
		side = "sell"
	}
	return &ramphub.Transaction{
		TransactionID: pajOrderID,
		Status:        status,
		Completed:     status == "completed",
		Terminal:      status == "completed" || status == "failed",
		Side:          side,
		Provider:      ProviderPaj,
		FiatAmount:    tx.FiatAmount,
		TokenAmount:   tx.USDCAmount,
		Rate:          tx.Rate,
	}, nil
}

// creditOnrampIfCompleted credits USDC and runs the 70/30 allocation split when
// an onramp completes. Because RampHub delivers USDC straight to the user's
// wallet, on-chain detection normally credits first; this is an idempotent
// backstop that no-ops if the deposit was already credited.
func (s *Service) creditOnrampIfCompleted(ctx context.Context, userID uuid.UUID, txID, newStatus string, tokenAmount, fiatAmount float64, txHash string) error {
	if newStatus != "completed" {
		return nil
	}

	// Atomically claim this order for crediting. RampHub's webhook/monitor-status
	// don't echo the token amount, so fall back to the expected USDC we stored at
	// order creation (token_amount).
	var claimedID uuid.UUID
	var storedToken decimal.Decimal
	if err := s.db.QueryRowContext(ctx,
		`UPDATE ramphub_orders SET deposit_id = gen_random_uuid()
		 WHERE ramphub_transaction_id = $1 AND order_type = 'onramp' AND deposit_id IS NULL
		 RETURNING id, COALESCE(token_amount, 0)`, txID).Scan(&claimedID, &storedToken); err != nil {
		if err != sql.ErrNoRows {
			s.logger.Error("failed to claim RampHub onramp order for credit",
				zap.Error(err), zap.String("ramphub_tx_id", txID))
		}
		return nil // already credited, not an onramp, or DB error — skip
	}

	creditAmount := decimal.NewFromFloat(tokenAmount)
	if creditAmount.LessThanOrEqual(decimal.Zero) {
		creditAmount = storedToken
	}
	if creditAmount.LessThanOrEqual(decimal.Zero) {
		s.logger.Warn("ramphub onramp completed but no token amount available — leaving for on-chain credit",
			zap.String("ramphub_tx_id", txID))
		s.db.ExecContext(ctx, `UPDATE ramphub_orders SET deposit_id = NULL WHERE id = $1`, claimedID)
		return nil
	}
	idempotencyKey := "ramphub-onramp-" + txID

	// Backstop guard: skip if on-chain detection already credited this deposit.
	var alreadyCredited bool
	_ = s.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM ledger_transactions WHERE idempotency_key = $1)`, idempotencyKey).Scan(&alreadyCredited)
	if !alreadyCredited {
		_ = s.db.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM deposits WHERE user_id = $1 AND status IN ('completed','confirmed') AND created_at > NOW() - INTERVAL '1 hour' AND amount = $2)`,
			userID, creditAmount).Scan(&alreadyCredited)
	}
	if alreadyCredited {
		if s.notifier != nil {
			s.notifier.NotifyDepositConfirmed(ctx, userID, creditAmount.StringFixed(2), "NGN", txID)
		}
		return nil
	}

	s.logger.Warn("on-chain detection has not credited RampHub onramp, falling back to direct credit",
		zap.String("user_id", userID.String()), zap.String("ramphub_tx_id", txID), zap.String("amount", creditAmount.String()))

	if s.depositLedger != nil {
		if err := s.depositLedger.CreditUSDCBalance(ctx, userID, creditAmount, idempotencyKey, map[string]interface{}{
			"provider": ProviderRampHub, "type": "onramp_credit", "ramphub_tx_id": txID, "fiat_amount": fiatAmount,
		}); err != nil {
			s.logger.Error("CRITICAL: failed to credit USDC after RampHub onramp", zap.Error(err), zap.String("ramphub_tx_id", txID))
			s.db.ExecContext(ctx, `UPDATE ramphub_orders SET deposit_id = NULL WHERE id = $1`, claimedID)
			return fmt.Errorf("credit usdc balance: %w", err)
		}
	}

	if s.allocationService != nil {
		sourceTxID := "ramphub-onramp:" + txID
		depositID := claimedID
		if err := s.allocationService.ProcessIncomingFunds(ctx, &entities.IncomingFundsRequest{
			UserID: userID, Amount: creditAmount, EventType: entities.AllocationEventTypeFiatDeposit,
			DepositID: &depositID, SourceTxID: &sourceTxID,
			Metadata: map[string]any{"source": "ramphub_onramp", "ramphub_tx_id": txID, "fiat_amount": fiatAmount},
		}); err != nil {
			s.logger.Error("failed allocation split for RampHub onramp — will retry", zap.Error(err), zap.String("ramphub_tx_id", txID))
			s.db.ExecContext(ctx, `UPDATE ramphub_orders SET deposit_id = NULL WHERE id = $1`, claimedID)
			return fmt.Errorf("allocation split: %w", err)
		}
	}

	if s.depositRepo != nil {
		now := time.Now()
		_ = s.depositRepo.Create(ctx, &entities.Deposit{
			ID: claimedID, IdempotencyKey: idempotencyKey, CorrelationID: "ramphub-onramp:" + txID,
			UserID: userID, Chain: entities.ChainSOL, TxHash: txHash, Token: entities.StablecoinUSDC,
			Amount: creditAmount, Status: "confirmed", ConfirmedAt: &now, CreatedAt: now,
		})
	}

	if s.notifier != nil {
		s.notifier.NotifyDepositConfirmed(ctx, userID, creditAmount.StringFixed(2), "NGN", txID)
	}
	if s.gameplayHooks != nil {
		s.gameplayHooks.OnDeposit(ctx, userID, creditAmount, claimedID)
	}
	return nil
}

// reverseOfframpIfFailed reverses the ledger hold when an offramp order fails.
func (s *Service) reverseOfframpIfFailed(ctx context.Context, userID uuid.UUID, txID, orderType, newStatus string) error {
	if newStatus != "failed" || orderType != "offramp" || s.ledger == nil {
		return nil
	}
	var holdAmount decimal.Decimal
	claimErr := s.db.QueryRowContext(ctx,
		`UPDATE ramphub_orders SET deposit_id = gen_random_uuid()
		 WHERE ramphub_transaction_id = $1 AND order_type = 'offramp' AND deposit_id IS NULL
		 RETURNING COALESCE(hold_amount, token_amount)`, txID).Scan(&holdAmount)
	if claimErr == sql.ErrNoRows {
		return nil // already claimed by webhook or recovery worker
	}
	if claimErr != nil {
		s.logger.Error("failed to claim order for offramp reversal",
			zap.Error(claimErr), zap.String("ramphub_tx_id", txID))
		return fmt.Errorf("claim order for reversal: %w", claimErr)
	}
	if holdAmount.IsZero() || holdAmount.IsNegative() {
		return nil
	}
	if err := s.ledger.ReverseTransaction(ctx, userID, entities.AccountTypeSpendingBalance,
		entities.OfframpReversalKey(txID), holdAmount, map[string]interface{}{
			"provider": ProviderRampHub, "type": "offramp_failure_reversal", "ramphub_tx_id": txID, "fee_revenue_posted": true,
		}); err != nil {
		s.logger.Error("CRITICAL: failed to reverse RampHub offramp hold after failure", zap.Error(err), zap.String("ramphub_tx_id", txID))
		s.db.ExecContext(ctx, `UPDATE ramphub_orders SET deposit_id = NULL WHERE ramphub_transaction_id = $1`, txID)
		return fmt.Errorf("reverse offramp hold: %w", err)
	}
	return nil
}

func (s *Service) notifyTerminal(ctx context.Context, userID uuid.UUID, txID, orderType, status string, fiatAmount float64) {
	if s.notifier == nil || orderType != "offramp" {
		return
	}
	amount := fmt.Sprintf("₦%.0f", fiatAmount)
	switch status {
	case "completed":
		s.notifier.NotifyWithdrawalCompleted(ctx, userID, amount, "bank account")
	case "failed":
		s.notifier.NotifyWithdrawalFailed(ctx, userID, amount, "Transaction failed. Funds returned to your balance.")
	}
}

// --- Order history ---

// Order is a persisted RampHub order for transaction history.
type Order struct {
	TransactionID string    `db:"ramphub_transaction_id" json:"transactionId"`
	OrderType     string    `db:"order_type" json:"orderType"`
	Status        string    `db:"status" json:"status"`
	Provider      string    `db:"selected_provider" json:"provider"`
	FiatAmount    float64   `db:"fiat_amount" json:"fiatAmount"`
	TokenAmount   float64   `db:"token_amount" json:"tokenAmount"`
	Asset         string    `db:"asset" json:"asset"`
	Currency      string    `db:"currency" json:"currency"`
	Rate          float64   `db:"rate" json:"rate"`
	CreatedAt     time.Time `db:"created_at" json:"createdAt"`
}

// GetOrders returns the user's RampHub order history.
func (s *Service) GetOrders(ctx context.Context, userID uuid.UUID) ([]Order, error) {
	var orders []Order
	if err := s.db.SelectContext(ctx, &orders, `
		SELECT ramphub_transaction_id, order_type, status, COALESCE(selected_provider,'ramphub') as selected_provider,
			COALESCE(fiat_amount,0) as fiat_amount, COALESCE(token_amount,0) as token_amount,
			COALESCE(asset,'USDC') as asset, COALESCE(currency,'NGN') as currency, COALESCE(rate,0) as rate, created_at
		FROM ramphub_orders WHERE user_id = $1
		  AND (status IN ('completed','paid','processing') OR (status IN ('pending','failed') AND created_at > NOW() - interval '24 hours'))
		ORDER BY created_at DESC LIMIT 50`, userID); err != nil {
		return nil, fmt.Errorf("get ramphub orders: %w", err)
	}
	return orders, nil
}

// --- Helpers ---

func (s *Service) enforceDepositLimits(ctx context.Context, userID uuid.UUID, fiatAmount float64, currency string, rate float64) error {
	limitAmount := decimal.NewFromFloat(fiatAmount)
	limitCurrency := strings.ToUpper(strings.TrimSpace(currency))
	if limitCurrency == "" {
		limitCurrency = "NGN"
	}
	if limitCurrency == "NGN" {
		if rate <= 0 {
			return fmt.Errorf("unable to verify deposit limits, please try again")
		}
		limitAmount = limitAmount.Div(decimal.NewFromFloat(rate))
		limitCurrency = "USD"
	}
	result, limErr := s.depositLimits.ValidateDepositWithCurrency(ctx, userID, limitAmount, limitCurrency)
	if limErr != nil || (result != nil && !result.Allowed) {
		msg := "deposit limit exceeded"
		if result != nil && result.Reason != "" {
			msg = result.Reason
		}
		if limErr != nil {
			return fmt.Errorf("%s", msg)
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

// resolveUserWallet returns the user's wallet address and Circle blockchain ID
// to receive onramp USDC. Prefers a Circle wallet already holding USDC.
func (s *Service) resolveUserWallet(ctx context.Context, userID uuid.UUID) (address, blockchain string) {
	if s.circleTransfer != nil {
		_, _, chain, addr, err := s.circleTransfer.FindWalletWithUSDC(ctx, userID.String())
		if err == nil && addr != "" {
			return addr, chain
		}
	}
	if s.walletProvider != nil {
		if wallet, err := s.walletProvider.GetWalletByUserAndChain(ctx, userID, entities.WalletChainSolana); err == nil && wallet != nil && wallet.Address != "" {
			return wallet.Address, "SOL"
		}
	}
	return "", ""
}

// circleToRampChain maps a Circle blockchain ID to a RampHub chain name.
func circleToRampChain(blockchain string) string {
	b := strings.ToUpper(blockchain)
	switch {
	case strings.Contains(b, "SOL"):
		return "solana"
	case strings.Contains(b, "BASE"):
		return "base"
	case strings.Contains(b, "ARB"):
		return "arbitrum"
	case strings.Contains(b, "OP"):
		return "optimism"
	case strings.Contains(b, "AVAX"):
		return "avalanche"
	case strings.Contains(b, "MATIC"), strings.Contains(b, "POLY"):
		return "polygon"
	case strings.Contains(b, "ETH"):
		return "ethereum"
	default:
		return "solana"
	}
}

// acquireOfframpLock takes a per-user PostgreSQL advisory lock, using a distinct
// key space from the Paj and withdrawal services. A dedicated connection is used
// for both lock and unlock so they always execute on the same session — pool
// connections are different sessions and pg_advisory_unlock would have no effect.
func (s *Service) acquireOfframpLock(ctx context.Context, userID uuid.UUID) (func(), error) {
	h := fnv.New64a()
	b := [16]byte(userID)
	h.Write(b[:])
	key := int64(binary.BigEndian.Uint64(h.Sum(nil)[:8])) + 2000000

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire connection for offramp lock: %w", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		var acquired bool
		if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&acquired); err != nil {
			conn.Close()
			return nil, fmt.Errorf("failed to acquire offramp lock: %w", err)
		}
		if acquired {
			return func() {
				conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", key) //nolint:errcheck
				conn.Close()
			}, nil
		}
		if time.Now().After(deadline) {
			conn.Close()
			return nil, fmt.Errorf("timeout acquiring offramp lock")
		}
		time.Sleep(25 * time.Millisecond)
	}
}
