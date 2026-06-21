package blend

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	chainrailspkg "github.com/rail-service/rail_service/internal/infrastructure/adapters/chainrails"
	circlepkg "github.com/rail-service/rail_service/internal/infrastructure/adapters/circle"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

var errNilRouter = errors.New("blend deposit router is nil")

// Route states for blend_deposit_routes.status.
const (
	routeStatusPending         = "pending"
	routeStatusAccountReady    = "account_ready"
	routeStatusSafeRequested   = "safe_requested"
	routeStatusSafeReady       = "safe_ready"
	routeStatusAwaitingFunding = "awaiting_funding"
	routeStatusQuoted          = "quoted"
	routeStatusExecuting       = "executing"
	routeStatusSubmitted       = "submitted"
	routeStatusSettling        = "settling"
	routeStatusComplete        = "complete"

	// Terminal error states (NOT auto-retried; require operator review). Retryable
	// and flowplan failures instead preserve the route's in-flight status and only
	// push out next_retry_at, so retries resume at the step that failed.
	routeStatusErrorPayload  = "error_payload"
	routeStatusErrorTerminal = "error_terminal"

	defaultRetryInterval = 30 * time.Second
	flowplanBackoff      = 5 * time.Minute
	terminalRetryDelay   = 24 * time.Hour
)

// CircleWalletProvider is the Circle API surface the router needs (resolving the
// user's Base EOA, reading balances, moving USDC for funding, and reading tx state).
type CircleWalletProvider interface {
	GetWallet(ctx context.Context, walletID string) (*circlepkg.Wallet, error)
	GetTransaction(ctx context.Context, txID string) (*circlepkg.Transaction, error)
	GetTokenBalance(ctx context.Context, walletID string) ([]circlepkg.TokenBalance, error)
	GetUSDCTokenID(ctx context.Context, walletID string) (string, error)
	ListCircleWalletsByRefID(ctx context.Context, refID string) ([]circlepkg.Wallet, error)
	TransferUSDCWithIdempotency(ctx context.Context, walletID, tokenID, destinationAddress, amount, idempotencyKey string) (*circlepkg.Transaction, error)
}

// ChainRailsBridge is the cross-chain settlement surface used to move stash USDC
// from the deposit chain to the user's Base EOA.
type ChainRailsBridge interface {
	CreateIntent(ctx context.Context, req *chainrailspkg.CreateIntentRequest) (*chainrailspkg.CreateIntentResponse, error)
	GetIntentStatus(ctx context.Context, intentAddress string) (*chainrailspkg.IntentStatus, error)
}

// DepositRouter routes user stash deposits into Blend.money on Base.
//
// State machine:
//
//	pending -> account_ready -> safe_requested -> safe_ready ->
//	quoted -> executing -> submitted -> settling -> complete
//
// Errors land in error_retryable / error_flowplan / error_payload / error_terminal.
// A background worker (NOT this router directly) drives retries; the router is
// safe to call repeatedly with the same depositID — idempotency is enforced
// by the database and by Circle/Blend idempotency keys.
type DepositRouter struct {
	db       *sqlx.DB
	blend    *Client
	circle   CircleWalletProvider
	executor *PlanExecutor
	logger   *zap.Logger

	chainID  int64
	usdcAddr string

	configMu          sync.RWMutex
	retryInterval     time.Duration
	redeemTimeout     time.Duration
	batchSize         int
	chainRails        ChainRailsBridge
	chainRailsDestCfg string
	stopCh            chan struct{}
	stopOnce          sync.Once
}

// SetChainRailsBridge enables cross-chain settlement of stash USDC into the user's
// Base EOA. destinationChain is the ChainRails chain identifier for Base
// (e.g. "BASE_MAINNET"); when empty it is derived from the configured chain ID.
func (r *DepositRouter) SetChainRailsBridge(bridge ChainRailsBridge, destinationChain string) {
	if r == nil {
		return
	}
	r.configMu.Lock()
	r.chainRails = bridge
	r.chainRailsDestCfg = strings.TrimSpace(destinationChain)
	r.configMu.Unlock()
}

func (r *DepositRouter) getChainRails() (ChainRailsBridge, string) {
	r.configMu.RLock()
	defer r.configMu.RUnlock()
	return r.chainRails, r.chainRailsDestCfg
}

// SetWorkerBatchSize overrides how many routes/redemptions are processed per tick.
func (r *DepositRouter) SetWorkerBatchSize(n int) {
	if r == nil || n <= 0 {
		return
	}
	r.configMu.Lock()
	r.batchSize = n
	r.configMu.Unlock()
}

func (r *DepositRouter) getBatchSize() int {
	r.configMu.RLock()
	defer r.configMu.RUnlock()
	if r.batchSize <= 0 {
		return defaultWorkerBatchSize
	}
	return r.batchSize
}

// SetRedeemTimeout configures how long RedeemStashYield waits for an on-chain
// withdrawal to settle before failing. Defaults to 3 minutes.
func (r *DepositRouter) SetRedeemTimeout(d time.Duration) {
	if r == nil || d <= 0 {
		return
	}
	r.configMu.Lock()
	r.redeemTimeout = d
	r.configMu.Unlock()
}

func (r *DepositRouter) getRedeemTimeout() time.Duration {
	r.configMu.RLock()
	defer r.configMu.RUnlock()
	if r.redeemTimeout <= 0 {
		return 3 * time.Minute
	}
	return r.redeemTimeout
}

// NewDepositRouter creates a Blend deposit router. chainID defaults to Base mainnet (8453).
func NewDepositRouter(db *sqlx.DB, blend *Client, circle CircleWalletProvider, executor *PlanExecutor, chainID int64, usdcAddr string, logger *zap.Logger) *DepositRouter {
	if chainID == 0 {
		chainID = BaseMainnetChainID
	}
	if usdcAddr == "" {
		usdcAddr = BaseUSDCAddress
	}
	return &DepositRouter{
		db:            db,
		blend:         blend,
		circle:        circle,
		executor:      executor,
		logger:        logger,
		chainID:       chainID,
		usdcAddr:      usdcAddr,
		retryInterval: defaultRetryInterval,
		stopCh:        make(chan struct{}),
	}
}

// --- YieldRouter interface (matches domain/services/allocation.YieldRouter) ---

// EnsureDepositYieldRoute records the durable route before async settlement starts.
// Called synchronously from the allocation pipeline.
func (r *DepositRouter) EnsureDepositYieldRoute(ctx context.Context, userID, depositID uuid.UUID, amount decimal.Decimal, metadata map[string]any) error {
	if r == nil || r.db == nil || r.blend == nil {
		return errors.New("blend deposit router not configured")
	}
	if userID == uuid.Nil || depositID == uuid.Nil {
		return errors.New("user_id and deposit_id are required")
	}
	if !amount.GreaterThan(decimal.Zero) {
		return errors.New("amount must be positive")
	}
	// The Blend Safe is owned by the user's Base EOA, and the deposit action plan
	// executes from that wallet. Resolve the user's OWN Base Circle wallet — NOT the
	// deposit's wallet, which may be on any chain. Stash liquidity is funded into this
	// Base wallet separately; the funding guard parks the route until it arrives.
	baseWallet, err := r.resolveUserBaseWallet(ctx, userID)
	if err != nil {
		return err
	}

	// Capture the funding source (the deposit wallet) so the funding step can bridge
	// the stash USDC from wherever it landed to the user's Base EOA.
	// If not provided in metadata (e.g. fiat deposits), resolve from the user's SOL wallet.
	sourceWalletID := metadataString(metadata, "circle_wallet_id")
	sourceChain := metadataString(metadata, "chain")
	if sourceWalletID == "" {
		solWallet, solErr := r.resolveUserSolanaWallet(ctx, userID)
		if solErr == nil {
			sourceWalletID = solWallet.CircleWalletID
			sourceChain = "SOL"
		} else {
			r.logger.Warn("blend: failed to resolve source wallet for fiat deposit, route will have empty source",
				zap.String("user_id", userID.String()), zap.Error(solErr))
		}
	}

	micro := USDCMicroUnits(amount)
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO blend_deposit_routes (
			id, deposit_id, user_id, blend_account_id, eoa_address,
			circle_wallet_id, chain_id, input_asset, amount, amount_units,
			source_circle_wallet_id, source_chain,
			status, next_retry_at, external_ref
		) VALUES ($1, $2, $3, '', $4, $5, $6, $7, $8, $9, NULLIF($10,''), NULLIF($11,''), $12, NOW(), $13)
		ON CONFLICT (deposit_id) DO NOTHING
	`,
		uuid.New(), depositID, userID, baseWallet.Address, baseWallet.CircleWalletID, r.chainID,
		r.usdcAddr, amount, micro.String(), sourceWalletID, sourceChain, routeStatusPending,
		fmt.Sprintf("dep-%s", depositID.String()),
	)
	if err != nil {
		return fmt.Errorf("blend: insert deposit route: %w", err)
	}
	return nil
}

// RouteDepositYield idempotently routes a deposit's stash allocation into Blend.
// Called asynchronously from the allocation pipeline; on error the background
// worker will retry from where the state machine left off.
func (r *DepositRouter) RouteDepositYield(ctx context.Context, userID, depositID uuid.UUID, amount decimal.Decimal, metadata map[string]any) error {
	if err := r.EnsureDepositYieldRoute(ctx, userID, depositID, amount, metadata); err != nil {
		return err
	}
	route, err := r.getRoute(ctx, depositID)
	if err != nil {
		return err
	}
	return r.processRoute(ctx, route)
}

// --- State machine driver ---

// ProcessRouteByID is used by the reconciliation worker. Idempotent.
func (r *DepositRouter) ProcessRouteByID(ctx context.Context, routeID uuid.UUID) error {
	route, err := r.getRouteByID(ctx, routeID)
	if err != nil {
		return err
	}
	return r.processRoute(ctx, route)
}

func (r *DepositRouter) processRoute(ctx context.Context, route *depositRoute) error {
	if route.Status == routeStatusComplete {
		return nil
	}
	entryStatus := route.Status
	if _, err := r.db.ExecContext(ctx, `
		UPDATE blend_deposit_routes
		SET attempts = attempts + 1, last_error = NULL, updated_at = NOW()
		WHERE id = $1
	`, route.ID); err != nil {
		return fmt.Errorf("blend: bump attempts: %w", err)
	}

	switch route.Status {
	case routeStatusPending:
		if err := r.stepEnsureAccount(ctx, route); err != nil {
			return r.markErr(ctx, route, err)
		}
		fallthrough
	case routeStatusAccountReady:
		if err := r.stepEnsureSafe(ctx, route); err != nil {
			return r.markErr(ctx, route, err)
		}
	case routeStatusSafeRequested:
		if err := r.stepPollSafe(ctx, route); err != nil {
			return r.markErr(ctx, route, err)
		}
	case routeStatusSafeReady, routeStatusAwaitingFunding:
		if err := r.stepFundAndQuote(ctx, route); err != nil {
			return r.markErr(ctx, route, err)
		}
	case routeStatusQuoted:
		if err := r.stepExecuteAndSubmit(ctx, route); err != nil {
			return r.markErr(ctx, route, err)
		}
	case routeStatusSubmitted, routeStatusSettling:
		if err := r.stepPollSettlement(ctx, route); err != nil {
			return r.markErr(ctx, route, err)
		}
	case routeStatusExecuting:
		// Re-entrancy: tx submitted to Circle but we crashed before recording the hash.
		// Resume via Blend's idempotent session — re-quoting an open session returns
		// the same intentId.
		if err := r.stepExecuteAndSubmit(ctx, route); err != nil {
			return r.markErr(ctx, route, err)
		}
	case routeStatusErrorPayload, routeStatusErrorTerminal:
		return fmt.Errorf("blend route %s in terminal error state %s; operator review required", route.ID, route.Status)
	}

	// Fetch the latest state and check if we're done.
	updated, err := r.getRouteByID(ctx, route.ID)
	if err != nil {
		return err
	}
	if updated.Status == routeStatusComplete {
		return nil
	}
	// If we made forward progress, re-enter the state machine so back-to-back
	// transitions (e.g. safe_ready -> quoted -> submitted) all happen in one
	// synchronous call instead of waiting for the next worker tick. Compared
	// against the status at ENTRY (steps mutate route.Status as they advance).
	if updated.Status != entryStatus && shouldContinueImmediately(updated.Status) {
		return r.processRoute(ctx, updated)
	}
	return nil
}

func shouldContinueImmediately(status string) bool {
	switch status {
	case routeStatusAccountReady, routeStatusSafeReady, routeStatusQuoted, routeStatusSubmitted:
		return true
	}
	return false
}

// --- Step: ensure Blend account exists ---

func (r *DepositRouter) stepEnsureAccount(ctx context.Context, route *depositRoute) error {
	if route.BlendAccountID != "" {
		return nil
	}
	// Reuse an existing user_account row if we provisioned one earlier.
	var userAcct blendUserAccount
	err := r.db.GetContext(ctx, &userAcct, `
		SELECT id, user_id, eoa_address, blend_account_id, COALESCE(safe_address,'') AS safe_address,
			chain_id, safe_status, safe_requested_at, safe_deployed_at, circle_wallet_id
		FROM blend_user_accounts WHERE user_id = $1
	`, route.UserID)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("blend: read user account: %w", err)
	}
	if err == sql.ErrNoRows {
		acct, lookupErr := r.blend.LookupOrCreateAccount(ctx, route.EOAAddress)
		if lookupErr != nil {
			return fmt.Errorf("blend: lookup/create account: %w", lookupErr)
		}
		if _, insertErr := r.db.ExecContext(ctx, `
			INSERT INTO blend_user_accounts (user_id, eoa_address, blend_account_id, safe_address, chain_id, safe_status, circle_wallet_id)
			VALUES ($1, $2, $3, NULLIF($4,''), $5, $6, $7)
			ON CONFLICT (user_id) DO UPDATE SET
				blend_account_id = EXCLUDED.blend_account_id,
				safe_address = COALESCE(EXCLUDED.safe_address, blend_user_accounts.safe_address),
				updated_at = NOW()
		`, route.UserID, route.EOAAddress, acct.AccountID, acct.SafeAddress, route.ChainID, SafeStatusNotDeployed, route.CircleWalletID); insertErr != nil {
			return fmt.Errorf("blend: persist user account: %w", insertErr)
		}
		userAcct.BlendAccountID = acct.AccountID
		userAcct.SafeAddress = acct.SafeAddress
	}

	if _, err := r.db.ExecContext(ctx, `
		UPDATE blend_deposit_routes
		SET blend_account_id = $2, safe_address = NULLIF($3,''), status = $4, updated_at = NOW()
		WHERE id = $1 AND status NOT IN ($5)
	`, route.ID, userAcct.BlendAccountID, userAcct.SafeAddress, routeStatusAccountReady, routeStatusComplete); err != nil {
		return fmt.Errorf("blend: mark account_ready: %w", err)
	}
	route.BlendAccountID = userAcct.BlendAccountID
	route.SafeAddress = userAcct.SafeAddress
	route.Status = routeStatusAccountReady
	return nil
}

// --- Step: ensure Safe is deployed on the target chain ---

func (r *DepositRouter) stepEnsureSafe(ctx context.Context, route *depositRoute) error {
	resolve, err := r.blend.ResolveSafe(ctx, route.BlendAccountID, route.ChainID)
	if err != nil {
		return fmt.Errorf("blend: resolve safe: %w", err)
	}
	switch resolve.Status {
	case SafeStatusValidated:
		return r.markSafeReady(ctx, route, resolve.SafeAddress)
	case SafeStatusNotDeployed:
		if reqErr := r.blend.RequestSafe(ctx, route.BlendAccountID, route.ChainID); reqErr != nil {
			return fmt.Errorf("blend: request safe: %w", reqErr)
		}
		if _, err := r.db.ExecContext(ctx, `
			UPDATE blend_user_accounts SET safe_status = $2, safe_requested_at = NOW(), updated_at = NOW()
			WHERE user_id = $1
		`, route.UserID, SafeStatusNotDeployed); err != nil {
			return fmt.Errorf("blend: mark safe requested: %w", err)
		}
		if _, err := r.db.ExecContext(ctx, `
			UPDATE blend_deposit_routes SET status = $2, next_retry_at = NOW() + INTERVAL '20 seconds', updated_at = NOW()
			WHERE id = $1
		`, route.ID, routeStatusSafeRequested); err != nil {
			return fmt.Errorf("blend: mark route safe_requested: %w", err)
		}
		route.Status = routeStatusSafeRequested
		return nil
	default:
		return fmt.Errorf("blend: unexpected safe status %q for account %s on chain %d", resolve.Status, route.BlendAccountID, route.ChainID)
	}
}

func (r *DepositRouter) stepPollSafe(ctx context.Context, route *depositRoute) error {
	resolve, err := r.blend.ResolveSafe(ctx, route.BlendAccountID, route.ChainID)
	if err != nil {
		return fmt.Errorf("blend: poll safe: %w", err)
	}
	if resolve.Status != SafeStatusValidated {
		// Not deployed yet — schedule next retry.
		if _, err := r.db.ExecContext(ctx, `
			UPDATE blend_deposit_routes SET next_retry_at = NOW() + INTERVAL '20 seconds', updated_at = NOW()
			WHERE id = $1
		`, route.ID); err != nil {
			return fmt.Errorf("blend: reschedule safe poll: %w", err)
		}
		return nil
	}
	return r.markSafeReady(ctx, route, resolve.SafeAddress)
}

func (r *DepositRouter) markSafeReady(ctx context.Context, route *depositRoute, safeAddr string) error {
	if _, err := r.db.ExecContext(ctx, `
		UPDATE blend_user_accounts SET safe_status = $2, safe_deployed_at = COALESCE(safe_deployed_at, NOW()), safe_address = $3, updated_at = NOW()
		WHERE user_id = $1
	`, route.UserID, SafeStatusValidated, safeAddr); err != nil {
		return fmt.Errorf("blend: mark safe validated: %w", err)
	}
	if _, err := r.db.ExecContext(ctx, `
		UPDATE blend_deposit_routes SET safe_address = $2, status = $3, updated_at = NOW()
		WHERE id = $1
	`, route.ID, safeAddr, routeStatusSafeReady); err != nil {
		return fmt.Errorf("blend: mark route safe_ready: %w", err)
	}
	route.SafeAddress = safeAddr
	route.Status = routeStatusSafeReady
	return nil
}

// --- Step: ensure the Base EOA actually holds the USDC before we quote/sign ---

// stepFundAndQuote guarantees the Base EOA holds the stash USDC before any Blend
// deposit is signed (the action plan moves USDC out of the EOA into the Safe). If
// the EOA is already funded it proceeds to quote. Otherwise it bridges the stash
// USDC from the deposit's source wallet to the Base EOA via ChainRails (or a
// same-chain Circle transfer when the source is already on Base). It NEVER signs a
// deposit against absent funds; if funding can't complete the route parks in
// `awaiting_funding` and the worker retries.
func (r *DepositRouter) stepFundAndQuote(ctx context.Context, route *depositRoute) error {
	need := route.Amount.Truncate(6)

	have, err := r.usdcBalance(ctx, route.CircleWalletID)
	if err != nil {
		return err
	}
	if have.GreaterThanOrEqual(need) {
		if !route.FundedAt.Valid {
			_, _ = r.db.ExecContext(ctx, `UPDATE blend_deposit_routes SET funded_at = NOW(), updated_at = NOW() WHERE id = $1`, route.ID)
		}
		return r.stepQuoteDeposit(ctx, route)
	}

	// Need to fund the Base EOA from the source wallet.
	if err := r.fundBaseEOA(ctx, route, need); err != nil {
		return err
	}

	// Re-check after funding attempt.
	have, err = r.usdcBalance(ctx, route.CircleWalletID)
	if err != nil {
		return err
	}
	if have.GreaterThanOrEqual(need) {
		if _, uerr := r.db.ExecContext(ctx, `UPDATE blend_deposit_routes SET funded_at = NOW(), updated_at = NOW() WHERE id = $1`, route.ID); uerr != nil {
			return fmt.Errorf("blend: mark funded: %w", uerr)
		}
		return r.stepQuoteDeposit(ctx, route)
	}

	// Funding not yet reflected (bridge still settling, or no source). Park and retry.
	if _, uerr := r.db.ExecContext(ctx, `
		UPDATE blend_deposit_routes
		SET status = $2, last_error = $3, next_retry_at = NOW() + INTERVAL '30 seconds', updated_at = NOW()
		WHERE id = $1
	`, route.ID, routeStatusAwaitingFunding,
		fmt.Sprintf("awaiting Base USDC in %s: have %s, need %s", route.CircleWalletID, have.StringFixed(6), need.StringFixed(6))); uerr != nil {
		return fmt.Errorf("blend: mark awaiting_funding: %w", uerr)
	}
	route.Status = routeStatusAwaitingFunding
	return nil
}

func (r *DepositRouter) usdcBalance(ctx context.Context, walletID string) (decimal.Decimal, error) {
	balances, err := r.circle.GetTokenBalance(ctx, walletID)
	if err != nil {
		return decimal.Zero, fmt.Errorf("blend: get circle balances for %s: %w", walletID, err)
	}
	for _, b := range balances {
		if strings.EqualFold(b.Token.Symbol, "USDC") {
			amt, perr := decimal.NewFromString(b.Amount)
			if perr != nil {
				return decimal.Zero, fmt.Errorf("blend: parse USDC balance %q: %w", b.Amount, perr)
			}
			return amt, nil
		}
	}
	return decimal.Zero, nil
}

// --- Step: quote the deposit and persist the action plan ---

func (r *DepositRouter) stepQuoteDeposit(ctx context.Context, route *depositRoute) error {
	// Reuse intent_id if we already have one (re-quoting is safe on OPEN).
	session, err := r.blend.GetOrCreateSession(ctx, route.BlendAccountID, route.ExternalRef.String, false)
	if err != nil {
		return fmt.Errorf("blend: get/create session: %w", err)
	}

	micro, ok := parseBig(route.AmountUnits.String)
	if !ok || micro.Sign() <= 0 {
		// Recompute from decimal amount if amount_units is empty/invalid.
		micro = USDCMicroUnits(route.Amount)
	}
	quote, err := r.blend.QuoteDeposit(ctx, route.BlendAccountID, session.IntentID, route.ChainID, route.InputAsset, route.EOAAddress, micro)
	if err != nil {
		return fmt.Errorf("blend: quote deposit: %w", err)
	}

	if _, err := r.db.ExecContext(ctx, `
		UPDATE blend_deposit_routes
		SET intent_id = $2, intent_status = $3, intent_expires_at = $4,
			quote_payload = $5, quote_summary = $6, status = $7, updated_at = NOW()
		WHERE id = $1
	`, route.ID, quote.IntentID, quote.Status, quote.ExpiresAt, []byte(quote.Payload), []byte(quote.QuoteSummary), routeStatusQuoted); err != nil {
		return fmt.Errorf("blend: persist quote: %w", err)
	}
	route.IntentID.String = quote.IntentID
	route.IntentID.Valid = true
	route.QuotePayload = []byte(quote.Payload)
	route.Status = routeStatusQuoted
	return nil
}

// --- Step: lock the session, execute action plan via Circle, submit tx hashes ---

func (r *DepositRouter) stepExecuteAndSubmit(ctx context.Context, route *depositRoute) error {
	if !route.IntentID.Valid || route.IntentID.String == "" {
		return errors.New("blend: cannot execute without intent_id")
	}
	plan, err := ParseActionPlan(route.QuotePayload)
	if err != nil {
		if _, updateErr := r.db.ExecContext(ctx, `
			UPDATE blend_deposit_routes SET status = $2, last_error = $3, next_retry_at = NOW() + INTERVAL '24 hours', updated_at = NOW()
			WHERE id = $1
		`, route.ID, routeStatusErrorPayload, err.Error()); updateErr != nil {
			return fmt.Errorf("blend: mark payload error: %w", updateErr)
		}
		return fmt.Errorf("blend: parse action plan: %w", err)
	}

	// Acquire the signer lock — but tolerate the re-entrant case where a prior
	// attempt already locked the session (crash between lock and submit). Only
	// lock when the session is still OPEN; if it is already LOCKED by us, proceed.
	current, err := r.blend.GetSession(ctx, route.BlendAccountID, route.IntentID.String)
	if err != nil {
		return fmt.Errorf("blend: get session before lock: %w", err)
	}
	switch current.Status {
	case IntentStatusOpen:
		if _, err := r.blend.LockSession(ctx, route.BlendAccountID, route.IntentID.String, route.EOAAddress); err != nil {
			return fmt.Errorf("blend: lock session: %w", err)
		}
	case IntentStatusLocked, IntentStatusSubmitted:
		// Already locked/submitted by a prior attempt — safe to continue (Circle
		// idempotency keys dedupe any re-execution).
	case IntentStatusSettled:
		// Deposit already settled on a prior attempt; record and exit.
		return r.recordSettlement(ctx, route, current)
	case IntentStatusFailed, IntentStatusCancelled:
		return fmt.Errorf("blend: session %s is %s before execution; cannot proceed", route.IntentID.String, current.Status)
	}

	if _, err := r.db.ExecContext(ctx, `
		UPDATE blend_deposit_routes SET status = $2, updated_at = NOW() WHERE id = $1
	`, route.ID, routeStatusExecuting); err != nil {
		return fmt.Errorf("blend: mark executing: %w", err)
	}

	executed, err := r.executor.Execute(ctx, route.CircleWalletID, plan, fmt.Sprintf("blend-deposit-%s", route.ID.String()),
		&TrustedSafe{Address: route.SafeAddress, OwnerEOA: route.EOAAddress, ChainID: route.ChainID})
	if err != nil {
		return fmt.Errorf("blend: execute action plan: %w", err)
	}

	hashes := make([]TxHashRef, 0, len(executed))
	for _, ex := range executed {
		if ex.TxHash == "" {
			// Circle accepts tx but hasn't broadcast yet; resolve via GetTransaction.
			tx, err := r.circle.GetTransaction(ctx, ex.TransactionID)
			if err != nil {
				return fmt.Errorf("blend: get circle tx %s: %w", ex.TransactionID, err)
			}
			ex.TxHash = tx.TxHash
		}
		if ex.TxHash == "" {
			// Tx still pending on Circle side — re-enter this step shortly.
			if _, err := r.db.ExecContext(ctx, `
				UPDATE blend_deposit_routes SET next_retry_at = NOW() + INTERVAL '10 seconds', updated_at = NOW() WHERE id = $1
			`, route.ID); err != nil {
				return fmt.Errorf("blend: reschedule for circle tx hash: %w", err)
			}
			return nil
		}
		hashes = append(hashes, TxHashRef{Hash: ex.TxHash, ChainID: ex.ChainID})
	}

	session, err := r.blend.SubmitTxHashes(ctx, route.BlendAccountID, route.IntentID.String, hashes)
	if err != nil {
		return fmt.Errorf("blend: submit tx hashes: %w", err)
	}

	primaryHash := ""
	if len(hashes) > 0 {
		primaryHash = hashes[len(hashes)-1].Hash
	}
	if _, err := r.db.ExecContext(ctx, `
		UPDATE blend_deposit_routes
		SET status = $2, intent_status = $3, tx_hash = $4, submitted_at = NOW(), updated_at = NOW()
		WHERE id = $1
	`, route.ID, routeStatusSubmitted, session.Status, primaryHash); err != nil {
		return fmt.Errorf("blend: mark submitted: %w", err)
	}
	route.Status = routeStatusSubmitted
	return nil
}

// --- Step: poll Blend until session is SETTLED ---

func (r *DepositRouter) stepPollSettlement(ctx context.Context, route *depositRoute) error {
	if !route.IntentID.Valid || route.IntentID.String == "" {
		return errors.New("blend: cannot poll settlement without intent_id")
	}
	session, err := r.blend.GetSession(ctx, route.BlendAccountID, route.IntentID.String)
	if err != nil {
		return fmt.Errorf("blend: get session: %w", err)
	}
	if _, err := r.db.ExecContext(ctx, `
		UPDATE blend_deposit_routes SET intent_status = $2, updated_at = NOW() WHERE id = $1
	`, route.ID, session.Status); err != nil {
		return fmt.Errorf("blend: update intent status: %w", err)
	}
	switch session.Status {
	case IntentStatusSettled:
		return r.recordSettlement(ctx, route, session)
	case IntentStatusFailed, IntentStatusCancelled:
		errMsg := session.ErrorMessage
		if errMsg == "" {
			errMsg = session.Status
		}
		if _, err := r.db.ExecContext(ctx, `
			UPDATE blend_deposit_routes SET status = $2, last_error = $3, next_retry_at = NOW() + INTERVAL '24 hours', updated_at = NOW()
			WHERE id = $1
		`, route.ID, routeStatusErrorTerminal, errMsg); err != nil {
			return fmt.Errorf("blend: mark terminal: %w", err)
		}
		return fmt.Errorf("blend session %s ended in %s: %s", session.IntentID, session.Status, errMsg)
	default:
		if _, err := r.db.ExecContext(ctx, `
			UPDATE blend_deposit_routes SET status = $2, next_retry_at = NOW() + INTERVAL '15 seconds', updated_at = NOW()
			WHERE id = $1
		`, route.ID, routeStatusSettling); err != nil {
			return fmt.Errorf("blend: reschedule settlement poll: %w", err)
		}
		return nil
	}
}

func (r *DepositRouter) recordSettlement(ctx context.Context, route *depositRoute, session *Session) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("blend: begin settlement tx: %w", err)
	}
	defer tx.Rollback()

	primaryHash := route.TxHash.String
	if primaryHash == "" && len(session.TxHashes) > 0 {
		primaryHash = session.TxHashes[len(session.TxHashes)-1]
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO blend_yield_positions (
			id, user_id, route_id, deposit_id, blend_account_id, safe_address, chain_id,
			principal_amount, deposit_tx_hash, status
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'active')
		ON CONFLICT (route_id) DO NOTHING
	`, uuid.New(), route.UserID, route.ID, route.DepositID, route.BlendAccountID, route.SafeAddress, route.ChainID, route.Amount, primaryHash); err != nil {
		return fmt.Errorf("blend: insert yield position: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE blend_deposit_routes
		SET status = $2, intent_status = $3, settled_at = NOW(), tx_hash = COALESCE(NULLIF(tx_hash,''), $4), last_error = NULL, updated_at = NOW()
		WHERE id = $1
	`, route.ID, routeStatusComplete, session.Status, primaryHash); err != nil {
		return fmt.Errorf("blend: mark route complete: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("blend: commit settlement: %w", err)
	}
	r.logger.Info("Blend deposit settled",
		zap.String("route_id", route.ID.String()),
		zap.String("user_id", route.UserID.String()),
		zap.String("deposit_id", nullUUIDString(route.DepositID)),
		zap.String("source", route.Source),
		zap.String("amount", route.Amount.StringFixed(6)),
		zap.String("tx_hash", primaryHash),
	)
	return nil
}

// --- Error handling ---

func (r *DepositRouter) markErr(ctx context.Context, route *depositRoute, srcErr error) error {
	// Default: retryable. Preserve the route's CURRENT status so the retry resumes
	// at the step that failed rather than restarting the flow. Restarting from
	// account creation after a settlement-poll failure would re-quote and
	// re-execute an already-submitted deposit — a double-deposit.
	preserveStatus := true
	terminalStatus := ""
	retryDelay := r.retryInterval

	var apiErr *APIError
	if errors.As(srcErr, &apiErr) {
		switch {
		case apiErr.IsFlowplanConflict():
			// Account mid-rebalance. Resume the same step after a longer backoff.
			retryDelay = flowplanBackoff
		case apiErr.IsRetryable():
			retryDelay = r.retryInterval
		case apiErr.Status >= 400 && apiErr.Status < 500:
			// 4xx that aren't 409 are terminal — bad key, malformed request, etc.
			preserveStatus = false
			terminalStatus = routeStatusErrorTerminal
			retryDelay = terminalRetryDelay
		}
	}

	var execErr error
	if preserveStatus {
		_, execErr = r.db.ExecContext(ctx, `
			UPDATE blend_deposit_routes
			SET last_error = $2, next_retry_at = NOW() + ($3 || ' seconds')::interval, updated_at = NOW()
			WHERE id = $1
		`, route.ID, srcErr.Error(), fmt.Sprintf("%d", int(retryDelay.Seconds())))
	} else {
		_, execErr = r.db.ExecContext(ctx, `
			UPDATE blend_deposit_routes
			SET status = $2, last_error = $3, next_retry_at = NOW() + ($4 || ' seconds')::interval, updated_at = NOW()
			WHERE id = $1
		`, route.ID, terminalStatus, srcErr.Error(), fmt.Sprintf("%d", int(retryDelay.Seconds())))
	}
	if execErr != nil {
		r.logger.Error("Blend: failed to mark route error", zap.String("route_id", route.ID.String()), zap.Error(execErr))
	}
	return srcErr
}

// --- DB read helpers ---

func (r *DepositRouter) getRoute(ctx context.Context, depositID uuid.UUID) (*depositRoute, error) {
	var route depositRoute
	if err := r.db.GetContext(ctx, &route, depositRouteSelect+` WHERE deposit_id = $1`, depositID); err != nil {
		return nil, fmt.Errorf("blend: get route by deposit_id: %w", err)
	}
	return &route, nil
}

func (r *DepositRouter) getRouteByID(ctx context.Context, id uuid.UUID) (*depositRoute, error) {
	var route depositRoute
	if err := r.db.GetContext(ctx, &route, depositRouteSelect+` WHERE id = $1`, id); err != nil {
		return nil, fmt.Errorf("blend: get route by id: %w", err)
	}
	return &route, nil
}

func (r *DepositRouter) resolveUserEOAByCircleWallet(ctx context.Context, circleWalletID string) (string, error) {
	wallet, err := r.circle.GetWallet(ctx, circleWalletID)
	if err != nil {
		return "", fmt.Errorf("blend: get circle wallet %s: %w", circleWalletID, err)
	}
	if strings.TrimSpace(wallet.Address) == "" {
		return "", fmt.Errorf("blend: circle wallet %s has no address", circleWalletID)
	}
	blockchain := strings.ToUpper(strings.TrimSpace(string(wallet.Blockchain)))
	if blockchain != "BASE" && blockchain != "BASE-SEPOLIA" {
		return "", fmt.Errorf("blend: circle wallet %s is on %s, expected BASE", circleWalletID, blockchain)
	}
	return wallet.Address, nil
}

func metadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	v, ok := metadata[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return strings.TrimSpace(s)
}
