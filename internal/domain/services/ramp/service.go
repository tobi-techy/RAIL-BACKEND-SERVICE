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
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/pajfunding"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/ramphub"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"github.com/rail-service/rail_service/pkg/metrics"
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
	Side     string `json:"side"`
	Provider string `json:"provider"`
	// SubProvider is the underlying RampHub provider the order will route to
	// (e.g. "usebread", "paycrest"), normalized. Empty for the Paj fallback. It
	// matters because RampHub bank codes are provider-scoped: an offramp sell
	// order must carry the SELECTED provider's payout bank code, not the
	// resolver's (Paycrest) code.
	SubProvider     string  `json:"subProvider,omitempty"`
	Asset           string  `json:"asset"`
	Chain           string  `json:"chain"`
	Currency        string  `json:"currency"`
	Rate            float64 `json:"rate"`
	EstimatedOutput float64 `json:"estimatedOutput"`
	TokenAmount     float64 `json:"tokenAmount"`
	Fee             float64 `json:"fee"`
}

// normalizeProviderName lowercases a RampHub provider label and drops the
// " Sandbox" suffix so "UseBread Sandbox" and "UseBread" both become "usebread".
func normalizeProviderName(p string) string {
	p = strings.ToLower(strings.TrimSpace(p))
	p = strings.TrimSuffix(p, " sandbox")
	return strings.TrimSpace(p)
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
				Side: side, Provider: ProviderRampHub, SubProvider: normalizeProviderName(resp.BestQuote.Provider),
				Asset: asset, Chain: chain, Currency: currency,
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

// Cache key is versioned: v2 switched the source from a 6-provider union to the
// resolver-compatible provider, so old cached lists (with non-Paystack codes)
// must not be served.
const ramphubBanksCacheKey = "ramphub:banks:v2"
const ramphubBanksCacheTTL = 24 * time.Hour

// ramphubResolverProvider is the provider whose bank codes match RampHub's
// account resolver (Paystack). Verified on mainnet (July 2026): resolving the
// same account via this provider's Moniepoint code (50515) succeeds, while the
// other providers' codes (090405, 0211, …) return "Unknown bank code for
// Paystack resolver". Sourcing the user-facing list from this one provider keeps
// every listed bank resolvable — the earlier 6-provider union served codes the
// resolver rejects, so account resolution 400'd for many banks.
const ramphubResolverProvider = "paycrest"

// GetBanks returns the payout bank list shown to users. Banks are sourced from
// the resolver-compatible provider so every listed bank can actually be resolved
// and used for a sell order. Falls back to the union of all sell-capable
// providers only if that provider is unavailable (so the list is never empty).
// Cached for 24h.
func (s *Service) GetBanks(ctx context.Context) ([]ramphub.Bank, error) {
	if s.redis != nil {
		var cached []ramphub.Bank
		if err := s.redis.Get(ctx, ramphubBanksCacheKey, &cached); err == nil && len(cached) > 0 {
			return cached, nil
		}
	}

	// Detach from the request context for all external API calls. The request
	// context has a short deadline from TimeoutMiddleware; when Redis is slow
	// (or unreachable), the cache miss eats most of that budget and all
	// subsequent RampHub calls inherit a nearly-expired deadline, immediately
	// producing "context canceled" for every provider. The httpClient already
	// enforces a 15s per-call timeout, so we don't lose safety here.
	fetchCtx, fetchCancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer fetchCancel()

	catalog, err := s.ramphubClient.GetCatalog(fetchCtx)
	if err != nil {
		return nil, err
	}

	// Primary source: the resolver-compatible provider (if the catalog lists it).
	banks := s.fetchBanks(fetchCtx, []string{ramphubResolverProvider})
	if len(banks) == 0 {
		// Fallback: union of every sell-capable provider. Codes here may not all
		// be resolver-compatible, but an incomplete list beats no list at all.
		s.logger.Warn("ramphub: resolver-provider bank list empty, falling back to multi-provider union",
			zap.String("provider", ramphubResolverProvider))
		var sellProviders []string
		for _, p := range catalog.Providers {
			if p.SupportsSell {
				sellProviders = append(sellProviders, strings.ToLower(p.Name))
			}
		}
		banks = s.fetchBanks(fetchCtx, sellProviders)
	}
	if len(banks) == 0 {
		return nil, fmt.Errorf("no banks available")
	}

	sort.Slice(banks, func(i, j int) bool { return banks[i].BankName < banks[j].BankName })

	if s.redis != nil && len(banks) > 0 {
		// Use a background context for the cache write: the request context may
		// have a tight deadline and we don't want a slow Redis to lose the result.
		cacheCtx, cacheCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cacheCancel()
		if cacheErr := s.redis.Set(cacheCtx, ramphubBanksCacheKey, banks, ramphubBanksCacheTTL); cacheErr != nil {
			s.logger.Warn("failed to cache ramphub banks", zap.Error(cacheErr))
		}
	}
	return banks, nil
}

// fetchBanks concurrently loads the given providers' bank lists and dedupes by
// bank code and by normalized name so each institution appears once.
func (s *Service) fetchBanks(ctx context.Context, providers []string) []ramphub.Bank {
	lists := make([][]ramphub.Bank, len(providers))
	var wg sync.WaitGroup
	for i, name := range providers {
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
	return banks
}

// bankNameFillerTokens are words that vary between providers' names for the same
// institution ("Moniepoint MFB" vs "Moniepoint Microfinance Bank") and carry no
// identifying value. Stripped before matching a bank across provider lists.
var bankNameFillerTokens = map[string]bool{
	"microfinance": true, "mfb": true, "bank": true, "digital": true,
	"services": true, "limited": true, "ltd": true, "plc": true,
	"nigeria": true, "ng": true, "the": true,
}

// normalizeBankNameForMatch reduces a provider-specific bank name to a stable key
// so the same institution matches across providers whose names/codes diverge.
// Drops parentheticals and filler tokens, keeps only alphanumerics. E.g. both
// "Moniepoint MFB" and "Moniepoint Microfinance Bank" -> "moniepoint".
func normalizeBankNameForMatch(name string) string {
	name = strings.ToLower(name)
	// Drop parentheticals, e.g. "OPay Digital Services Limited (OPay)".
	if i := strings.IndexByte(name, '('); i >= 0 {
		name = name[:i]
	}
	var b strings.Builder
	for _, tok := range strings.FieldsFunc(name, func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
	}) {
		if bankNameFillerTokens[tok] {
			continue
		}
		b.WriteString(tok)
	}
	return b.String()
}

// payoutBankForProvider translates the user-selected payout bank (resolved via
// the Paycrest-scoped list) into the SELECTED sell provider's own bank code and
// name, matching on a normalized name key. RampHub bank codes are provider-
// scoped, so a sell order must carry the routed provider's code or the
// beneficiary lookup 400s ("Failed to lookup beneficiary bank account").
//
// Returns ok=false when there is no UNIQUE match (zero or ambiguous) — callers
// must then refuse the withdrawal rather than guess a code that could misroute
// funds. providerBankListCache keeps this off the hot path.
func (s *Service) payoutBankForProvider(ctx context.Context, provider, userBankName string) (code, name string, ok bool) {
	key := normalizeBankNameForMatch(userBankName)
	if key == "" {
		return "", "", false
	}
	list, err := s.providerBankList(ctx, provider)
	if err != nil || len(list) == 0 {
		s.logger.Warn("ramphub: could not load provider bank list for payout code translation",
			zap.String("provider", provider), zap.Error(err))
		return "", "", false
	}
	code, name, ok = matchBankByName(list, userBankName)
	if !ok {
		s.logger.Warn("ramphub: payout bank did not uniquely match the selected provider",
			zap.String("provider", provider), zap.String("bank_name", userBankName))
	}
	return code, name, ok
}

// matchBankByName finds the single bank in list whose normalized name equals the
// normalized userBankName. Returns ok=false on zero or multiple matches so the
// caller can fail closed instead of guessing a provider bank code.
func matchBankByName(list []ramphub.Bank, userBankName string) (code, name string, ok bool) {
	key := normalizeBankNameForMatch(userBankName)
	if key == "" {
		return "", "", false
	}
	var matches []ramphub.Bank
	for _, b := range list {
		if b.BankCode != "" && normalizeBankNameForMatch(b.BankName) == key {
			matches = append(matches, b)
		}
	}
	if len(matches) != 1 {
		return "", "", false
	}
	return matches[0].BankCode, matches[0].BankName, true
}

const providerBankListCacheTTL = 24 * time.Hour

// providerBankList returns a single provider's payout bank list, cached 24h.
func (s *Service) providerBankList(ctx context.Context, provider string) ([]ramphub.Bank, error) {
	cacheKey := "ramphub:providerbanks:" + provider + ":v1"
	if s.redis != nil {
		var cached []ramphub.Bank
		if err := s.redis.Get(ctx, cacheKey, &cached); err == nil && len(cached) > 0 {
			return cached, nil
		}
	}
	fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	list, err := s.ramphubClient.GetProviderBankList(fetchCtx, provider)
	if err != nil {
		return nil, err
	}
	if s.redis != nil && len(list.Banks) > 0 {
		cacheCtx, cacheCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cacheCancel()
		if cacheErr := s.redis.Set(cacheCtx, cacheKey, list.Banks, providerBankListCacheTTL); cacheErr != nil {
			s.logger.Warn("failed to cache provider bank list", zap.Error(cacheErr))
		}
	}
	return list.Banks, nil
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
	// RampHub may be unconfigured in deployments that only run the Paj rail —
	// mirror CreateOfframp and fail cleanly instead of dereferencing a nil client.
	if s.ramphubClient == nil {
		return nil, fmt.Errorf("bank verification service temporarily unavailable")
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
				// RampHub can accept a buy order yet return no virtual account (no
				// providerDetails.virtualAccount). Returning that as success lands
				// the user on a "transfer to" screen with nothing to pay into, so
				// they can neither pay nor be credited. Treat a missing pay-in
				// account as a provider failure and fall through to the Paj
				// fallback rather than persisting an unusable order.
				if acctNum == "" || bankName == "" {
					s.logger.Error("ramphub onramp returned no virtual account, falling back to Paj",
						zap.String("ramphub_tx_id", order.TransactionID),
						zap.String("user_id", userID.String()))
					metrics.RecordRampFallback("onramp", "missing_virtual_account")
				} else {
					if perr := s.persistOnrampOrder(ctx, userID, order, fiatAmount, cryptoAmount.InexactFloat64(), currency, settlementAsset, rampChain, acctNum, acctName, bankName); perr != nil {
						s.logger.Error("CRITICAL: failed to persist ramphub onramp order", zap.Error(perr), zap.String("ramphub_tx_id", order.TransactionID))
						return nil, fmt.Errorf("failed to create deposit order, please try again")
					}
					metrics.RecordRampProvider("onramp", ProviderRampHub)
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
			} else {
				s.logger.Warn("ramphub onramp failed, falling back to Paj", zap.Error(err), zap.String("user_id", userID.String()))
				metrics.RecordRampFallback("onramp", "order_error")
			}
		} else {
			s.logger.Warn("ramphub onramp quote unavailable, falling back to Paj", zap.String("user_id", userID.String()))
			metrics.RecordRampFallback("onramp", "quote_unavailable")
		}
	}

	// Paj fallback.
	if s.pajFallback != nil {
		pajOrder, err := s.pajFallback.CreateOnrampOrder(ctx, userID, fiatAmount, currency)
		if err != nil {
			return nil, err
		}
		if pajOrder.AccountNumber == "" || pajOrder.Bank == "" {
			s.logger.Error("paj onramp returned no pay-in account",
				zap.String("paj_order_id", pajOrder.ID), zap.String("user_id", userID.String()))
			return nil, fmt.Errorf("deposit temporarily unavailable, please try again")
		}
		metrics.RecordRampProvider("onramp", ProviderPaj)
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
	// The provider names the customer's virtual pay-in account from the identity
	// we send. RampHub rejects unknown fields (no `name` field exists), and it
	// derives the account name from externalCustomerId — a dashed UUID fails the
	// Nigerian bank name rule ("Name can only contain alphanumeric characters …")
	// and every buy order silently 400s to the Paj fallback. Send a dash-free
	// customer id plus the user's real email (from our own records, never the
	// client) so the generated name is valid.
	req := ramphub.OrderRequest{
		Side:               "buy",
		Amount:             cryptoAmount,
		FiatAmount:         fiatAmount,
		FiatCurrency:       currency,
		Asset:              asset,
		Chain:              chain,
		WalletAddress:      walletAddress,
		Email:              s.getUserEmail(ctx, userID),
		ExternalCustomerID: rampCustomerID(userID),
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

// buyBankDetails extracts the pay-in account the customer must transfer to,
// normalized across RampHub providers (Paycrest's virtualAccount vs UseBread's
// nested data.deposit shape).
func buyBankDetails(order *ramphub.OrderResponse) (accountNumber, accountName, bankName string) {
	return order.PayInAccount()
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
func (s *Service) CreateOfframp(ctx context.Context, userID uuid.UUID, bankCode, accountNumber, bankName string, fiatAmount float64, currency string, expectedRate float64) (*OfframpResult, error) {
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

	// Rate staleness guard: if the frontend provided an expected rate, reject
	// when the live rate deviates by more than 5%. This prevents the user from
	// confirming at one rate and getting a significantly different one.
	if expectedRate > 0 && quote.Rate > 0 {
		deviation := (quote.Rate - expectedRate) / expectedRate
		if deviation < 0 {
			deviation = -deviation
		}
		if deviation > 0.05 {
			return nil, fmt.Errorf("rate has changed significantly (was ₦%.0f/USD, now ₦%.0f/USD). Please refresh and try again", expectedRate, quote.Rate)
		}
	}

	// RampHub/RIO minimum: $1 USDC per order. Compute from the live rate.
	minDollarFloor := 1.0 * quote.Rate
	if fiatAmount < minDollarFloor {
		return nil, fmt.Errorf("minimum withdrawal is ₦%.0f ($1.00 at current rate — try a larger amount)", math.Ceil(minDollarFloor))
	}

	// Verify the payout account before any money moves. RampHub requires the
	// resolved account name on sell orders, and failing here costs nothing to
	// unwind — no hold has been taken yet.
	accountName := ""
	rampHubViable := s.ramphubClient != nil && quote.Provider == ProviderRampHub
	if !rampHubViable {
		metrics.RecordRampFallback("offramp", "no_ramphub_quote")
	}
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
			metrics.RecordRampFallback("offramp", "resolve_unavailable")
			rampHubViable = false
		}
	}
	if !rampHubViable {
		// NOTE: Paj fallback is intentionally omitted here. Paj requires a MongoDB
		// ObjectID for bank identification, but this flow has only a RampHub bank
		// code (numeric, provider-scoped). Passing the wrong format would fail
		// every time. Users who need Paj should use the Paj-specific endpoint.
		return nil, fmt.Errorf("withdrawal service temporarily unavailable — RampHub bank resolution unavailable; try again later")
	}

	// Bank codes are provider-scoped. The account was resolved with the Paycrest
	// (resolver) code, but the sell order routes to the best-rate provider, which
	// needs ITS own code+name or the beneficiary lookup 400s. Translate to the
	// selected provider's payout bank; fail closed (no hold taken yet) rather than
	// send a code that could misroute funds. No-op when the routed provider is the
	// resolver provider itself.
	sellBankCode, sellBankName := bankCode, bankName
	if prov := quote.SubProvider; prov != "" && prov != ramphubResolverProvider {
		code, name, ok := s.payoutBankForProvider(ctx, prov, bankName)
		if !ok {
			metrics.RecordRampFallback("offramp", "bank_code_untranslatable")
			return nil, fmt.Errorf("this bank isn't currently supported for withdrawal, please try another account or try again later")
		}
		sellBankCode, sellBankName = code, name
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
	orderCrypto := decimal.NewFromFloat(fiatAmount).Div(decimal.NewFromFloat(quote.Rate)).Round(6)
	order, err := s.ramphubClient.CreateOrder(ctx, ramphub.OrderRequest{
		Side:                "sell",
		Amount:              orderCrypto.InexactFloat64(),
		FiatCurrency:        currency,
		Asset:               settlementAsset,
		Chain:               offrampSettlementChain,
		BankCode:            sellBankCode,
		AccountNumber:       accountNumber,
		AccountName:         accountName,
		BankName:            sellBankName,
		Email:               s.getUserEmail(ctx, userID),
		ExternalCustomerID:  rampCustomerID(userID),
		DeveloperFeePercent: s.developerFeePercent,
	})
	if err != nil {
		// Reverse hold and surface the error. Paj fallback is intentionally
		// omitted — Paj requires a MongoDB ObjectID bank identifier but this
		// flow carries a RampHub numeric bank code, so fallback always fails.
		s.reverseInitialHold(ctx, userID, totalHold, railFee, "ramphub_api_error")
		metrics.RecordRampFallback("offramp", "order_error")
		return nil, err
	}

	// Normalize: extract the deposit address from whatever field the provider
	// populated — ourCryptoAddress (top-level), providerDetails.depositAddress,
	// or providerDetails.data.deposit.address (UseBread sell).
	s.logger.Info("RampHub sell order response",
		zap.String("ramphub_tx_id", order.TransactionID),
		zap.String("selected_provider", order.SelectedProvider),
		zap.String("status", order.Status),
		zap.String("provider_status", order.ProviderDetails.Status),
		zap.String("our_crypto_address", order.OurCryptoAddress),
		zap.String("deposit_address", order.ProviderDetails.DepositAddress),
		zap.Float64("amount_to_send", order.ProviderDetails.AmountToSend),
		zap.String("provider_details", fmt.Sprintf("%+v", order.ProviderDetails)))
	if order.CryptoDepositAddress() != "" {
		order.OurCryptoAddress = order.CryptoDepositAddress()
	}
	if order.CryptoDepositAmount() > 0 {
		order.ProviderDetails.AmountToSend = order.CryptoDepositAmount()
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

	metrics.RecordRampProvider("offramp", ProviderRampHub)
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
	// Use a compact UUID as the reference key. The verbose prefix+timestamp
	// format produces idempotency_key values over 100 chars when embedded by
	// the ledger adapter, violating the VARCHAR(100) UNIQUE constraint on
	// ledger_transactions.idempotency_key.
	key := uuid.New().String()
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
	// RampHub references the order under several possible keys across schema
	// revisions (data.id, data.transactionId, data.reference, …). Collect every
	// candidate and match on all of them so a change in which field carries the
	// id does not break order matching.
	candidates := event.Data.Identifiers()
	if len(candidates) == 0 {
		return nil
	}

	// Verify the order exists before recording delivery — if the order hasn't
	// been persisted yet (race with create path), return an error so RampHub
	// retries the webhook instead of permanently consuming the delivery_id.
	//
	// A candidate can match either the order's UUID (ramphub_transaction_id) or
	// its RH-TX reference (request_reference); after matching we key every
	// downstream query on the canonical UUID.
	var userID uuid.UUID
	var currentStatus, orderType, canonicalTxID string
	err := s.db.QueryRowContext(ctx,
		`SELECT user_id, status, order_type, ramphub_transaction_id FROM ramphub_orders
		 WHERE ramphub_transaction_id = ANY($1) OR request_reference = ANY($1)`, pq.Array(candidates)).
		Scan(&userID, &currentStatus, &orderType, &canonicalTxID)
	if err == sql.ErrNoRows {
		return s.handleUnmatchedWebhook(ctx, candidates)
	}
	if err != nil {
		return fmt.Errorf("lookup ramphub order: %w", err)
	}
	txID := canonicalTxID
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
	credErr := s.creditOnrampIfCompleted(ctx, userID, txID, newStatus, float64(event.Data.TokenAmount), float64(event.Data.FiatAmount), event.Data.TxHash)
	revErr := s.reverseOfframpIfFailed(ctx, userID, txID, orderType, newStatus)
	s.notifyTerminal(ctx, userID, txID, orderType, newStatus, float64(event.Data.FiatAmount))
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

const (
	// ramphubWebhookMaxRetries bounds how many times an unmatched webhook is
	// retried (via a 5xx) before we stop asking RampHub to redeliver. A genuine
	// create-race resolves within seconds; beyond this the order does not exist
	// for us, and the poll/recovery workers settle it if it ever does — so
	// continuing to 500 only produces a retry storm.
	ramphubWebhookMaxRetries = 6
	// ramphubWebhookRetryTTL bounds the lifetime of the per-webhook attempt
	// counter so a later, legitimately-delayed order still gets a fresh grace
	// window rather than being permanently suppressed.
	ramphubWebhookRetryTTL = 2 * time.Hour
)

// handleUnmatchedWebhook decides whether to keep asking RampHub to retry a
// webhook whose order we cannot find (return error -> 5xx) or to give up and
// acknowledge it (return nil -> 200) once the create-race grace window has
// clearly passed. This prevents a permanent id mismatch or a stale/foreign
// delivery from 500-storming forever while still tolerating the normal race
// where a webhook arrives microseconds before the order row is committed.
func (s *Service) handleUnmatchedWebhook(ctx context.Context, candidates []string) error {
	retryErr := fmt.Errorf("order not yet persisted for tx %s", strings.Join(candidates, ","))

	// No shared counter available (tests / degraded mode): keep the retry
	// behaviour so a real early-race webhook is never dropped. Prod always has
	// Redis.
	if s.redis == nil {
		s.logger.Warn("ramphub webhook for unknown order — will retry",
			zap.Strings("ramphub_candidates", candidates))
		return retryErr
	}

	key := "ramphub:webhook:unmatched:" + unmatchedWebhookKey(candidates)
	attempts, incrErr := s.redis.Incr(ctx, key)
	if incrErr != nil {
		// Fail open to a retry: if we cannot track attempts we must not drop a
		// potentially-real early-race webhook because Redis hiccuped.
		s.logger.Warn("ramphub webhook: unmatched-retry counter failed — will retry",
			zap.Error(incrErr), zap.Strings("ramphub_candidates", candidates))
		return retryErr
	}
	if attempts == 1 {
		if expErr := s.redis.Expire(ctx, key, ramphubWebhookRetryTTL); expErr != nil {
			s.logger.Warn("ramphub webhook: failed to set unmatched-retry TTL",
				zap.Error(expErr), zap.Strings("ramphub_candidates", candidates))
		}
	}

	if attempts <= ramphubWebhookMaxRetries {
		s.logger.Warn("ramphub webhook for unknown order — will retry",
			zap.Strings("ramphub_candidates", candidates), zap.Int64("attempt", attempts))
		return retryErr
	}

	// Grace window exhausted: acknowledge so RampHub stops retrying. If the order
	// genuinely exists it is settled by the poll/recovery workers; if it never
	// existed this was a stale or foreign delivery. Either way, surface loudly
	// for manual reconciliation.
	s.logger.Error("CRITICAL: ramphub webhook unmatched after max retries — acking to stop storm; verify order reconciliation",
		zap.Strings("ramphub_candidates", candidates), zap.Int64("attempts", attempts))
	return nil
}

// unmatchedWebhookKey builds a stable, bounded Redis key fragment from the
// candidate identifiers so retries of the same logical webhook share a counter
// regardless of identifier ordering.
func unmatchedWebhookKey(candidates []string) string {
	sorted := append([]string(nil), candidates...)
	sort.Strings(sorted)
	h := fnv.New64a()
	_, _ = h.Write([]byte(strings.Join(sorted, "|")))
	return fmt.Sprintf("%x", h.Sum64())
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

// unclaimOnrampOrder releases the credit claim (deposit_id) taken by
// creditOnrampIfCompleted so a later webhook retry can attempt the credit
// again. A failed un-claim is CRITICAL: the order stays claimed, every retry
// is blocked by the deposit_id IS NULL guard, and the credit never lands
// without manual intervention — so it is both logged loudly and returned to
// the caller for inclusion in the delivery-failure error.
func (s *Service) unclaimOnrampOrder(ctx context.Context, txID string, claimedID uuid.UUID) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE ramphub_orders SET deposit_id = NULL WHERE id = $1`, claimedID); err != nil {
		s.logger.Error("CRITICAL: failed to un-claim ramphub onramp order — retries are blocked, manual intervention required",
			zap.Error(err),
			zap.String("ramphub_tx_id", txID),
			zap.String("claimed_id", claimedID.String()))
		return fmt.Errorf("un-claim ramphub onramp order %s (claim %s): %w", txID, claimedID, err)
	}
	return nil
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

	// A matching PENDING deposit means on-chain detection is crediting this
	// arrival right now (its row exists but ledger/confirm hasn't finished).
	// Crediting directly here would double-credit under a second idempotency
	// key. Un-claim and fail the delivery so RampHub retries: by then the
	// deposit is either confirmed (guard above skips) or was rolled back
	// (pending row deleted, direct credit proceeds safely).
	var pendingInFlight bool
	if scanErr := s.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM deposits WHERE user_id = $1 AND status = 'pending' AND created_at > NOW() - INTERVAL '1 hour' AND amount = $2)`,
		userID, creditAmount).Scan(&pendingInFlight); scanErr != nil {
		// Fail CLOSED: if we cannot verify that no on-chain credit is in
		// flight, crediting anyway risks a double credit. Un-claim and fail
		// the delivery so RampHub retries once the database is healthy.
		s.logger.Error("ramphub onramp backstop: pending-deposit check failed — deferring",
			zap.Error(scanErr), zap.String("ramphub_tx_id", txID), zap.String("user_id", userID.String()))
		if unclaimErr := s.unclaimOnrampOrder(ctx, txID, claimedID); unclaimErr != nil {
			return fmt.Errorf("failed to verify pending deposits for ramphub onramp %s: %v; un-claim also failed: %w", txID, scanErr, unclaimErr)
		}
		return fmt.Errorf("failed to verify pending deposits for ramphub onramp %s: %w", txID, scanErr)
	}
	if pendingInFlight {
		s.logger.Info("ramphub onramp backstop deferred — on-chain credit in flight",
			zap.String("ramphub_tx_id", txID), zap.String("user_id", userID.String()))
		if unclaimErr := s.unclaimOnrampOrder(ctx, txID, claimedID); unclaimErr != nil {
			return fmt.Errorf("on-chain credit in flight for ramphub onramp %s; un-claim also failed: %w", txID, unclaimErr)
		}
		return fmt.Errorf("on-chain credit in flight for ramphub onramp %s — deferring backstop credit", txID)
	}

	s.logger.Warn("on-chain detection has not credited RampHub onramp, falling back to direct credit",
		zap.String("user_id", userID.String()), zap.String("ramphub_tx_id", txID), zap.String("amount", creditAmount.String()))

	if s.depositLedger != nil {
		if err := s.depositLedger.CreditUSDCBalance(ctx, userID, creditAmount, idempotencyKey, map[string]interface{}{
			"provider": ProviderRampHub, "type": "onramp_credit", "ramphub_tx_id": txID, "fiat_amount": fiatAmount,
		}); err != nil {
			s.logger.Error("CRITICAL: failed to credit USDC after RampHub onramp", zap.Error(err), zap.String("ramphub_tx_id", txID))
			if uErr := s.unclaimOnrampOrder(ctx, txID, claimedID); uErr != nil {
				return fmt.Errorf("credit usdc balance: %v; un-claim also failed: %w", err, uErr)
			}
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
			if uErr := s.unclaimOnrampOrder(ctx, txID, claimedID); uErr != nil {
				return fmt.Errorf("allocation split: %v; un-claim also failed: %w", err, uErr)
			}
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
		if _, uErr := s.db.ExecContext(ctx, `UPDATE ramphub_orders SET deposit_id = NULL WHERE ramphub_transaction_id = $1`, txID); uErr != nil {
			s.logger.Error("CRITICAL: failed to un-claim RampHub offramp order after reversal failure — retries blocked, manual intervention required",
				zap.Error(uErr), zap.String("ramphub_tx_id", txID))
			return fmt.Errorf("reverse offramp hold: %v; un-claim also failed (retries blocked): %w", err, uErr)
		}
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

// rampCustomerID returns a RampHub externalCustomerId for the user. It strips
// the UUID dashes so the value is purely alphanumeric — the provider uses it to
// name the virtual pay-in account, which must satisfy Nigerian bank name rules.
func rampCustomerID(userID uuid.UUID) string {
	return strings.ReplaceAll(userID.String(), "-", "")
}

// getUserEmail loads the user's email from Rail's own record (source of truth —
// never client-supplied) for RampHub orders. Returns "" on miss (email is
// optional on the order).
func (s *Service) getUserEmail(ctx context.Context, userID uuid.UUID) string {
	var mail sql.NullString
	if err := s.db.QueryRowContext(ctx,
		`SELECT email FROM users WHERE id = $1`, userID).Scan(&mail); err != nil {
		s.logger.Warn("ramphub: failed to load user email for order",
			zap.Error(err), zap.String("user_id", userID.String()))
		return ""
	}
	return strings.TrimSpace(mail.String)
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
