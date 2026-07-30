package blend

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// Redemption states for blend_yield_redemptions.status.
const (
	redemptionStatusPending   = "pending"
	redemptionStatusQuoted    = "quoted"
	redemptionStatusExecuting = "executing"
	redemptionStatusSubmitted = "submitted"
	redemptionStatusComplete  = "complete"
	redemptionStatusFailed    = "failed"
)

// ErrRedemptionRetrying indicates the Blend redemption was reset for worker retry
// (e.g. session failed but attempts < threshold). The caller should NOT treat this
// as a terminal failure — the redemption is still alive and the worker will resume it.
var ErrRedemptionRetrying = errors.New("blend: redemption reset for retry")

// RedeemStashYield withdraws `amount` USDC from the user's Blend Safe back to their
// EOA (Base Circle wallet) so a subsequent stash withdrawal can spend it.
//
// CRITICAL CONTRACT: this returns nil ONLY when the funds have settled on-chain and
// are available in the user's wallet. The withdrawal pipeline spends USDC immediately
// after this returns, so any premature success would spend funds that aren't there.
// On timeout or failure it returns an error and the funds stay in the Safe untouched.
//
// Idempotent: re-invoking with the same idempotencyKey resumes/short-circuits rather
// than issuing a second withdrawal.
func (r *DepositRouter) RedeemStashYield(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, idempotencyKey string) error {
	if r == nil || r.db == nil || r.blend == nil {
		return errors.New("blend redeemer not configured")
	}
	if userID == uuid.Nil {
		return errors.New("user_id is required")
	}
	if !amount.GreaterThan(decimal.Zero) {
		return nil // nothing to redeem
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		return errors.New("redemption idempotency key is required")
	}
	amount = amount.Truncate(6)

	// Fast path: already completed on a prior attempt.
	existing, err := r.getRedemption(ctx, idempotencyKey)
	if err != nil {
		return err
	}
	if existing != nil && existing.Status == redemptionStatusComplete {
		return nil
	}

	acct, err := r.getUserAccount(ctx, userID)
	if err != nil {
		return err
	}
	if acct == nil || acct.BlendAccountID == "" {
		return fmt.Errorf("blend: user %s has no Blend account to redeem from", userID)
	}
	if acct.SafeStatus != SafeStatusValidated {
		return fmt.Errorf("blend: user %s Safe is not validated (status=%s); cannot redeem", userID, acct.SafeStatus)
	}

	// Reserve the redemption before doing anything irreversible. The unique
	// idempotency key guarantees only one redemption row per withdrawal.
	if existing == nil {
		if err := r.reserveRedemption(ctx, userID, acct.BlendAccountID, amount, r.chainID, idempotencyKey); err != nil {
			return err
		}
		existing, err = r.getRedemption(ctx, idempotencyKey)
		if err != nil {
			return err
		}
	}

	// Verify the user actually has enough principal to cover this redemption,
	// EXCLUDING amounts already reserved by other in-flight redemptions.
	//
	// This path runs AFTER the ledger has moved (stash→spend / emergency), so a
	// terminal failure here would leave the ledger permanently ahead of Blend
	// custody with nothing for the worker to resume — the exact incident class
	// this code exists to prevent. Positions are often just not recorded yet
	// (an in-flight deposit route), so defer and let the worker retry.
	if err := r.assertSufficientPosition(ctx, userID, amount, existing.ID); err != nil {
		r.logger.Error("Blend: redemption blocked on insufficient position — deferring for worker retry",
			zap.String("redemption_id", existing.ID.String()),
			zap.String("user_id", userID.String()),
			zap.String("amount", amount.StringFixed(6)),
			zap.Error(err))
		r.deferRedemption(ctx, existing.ID, err.Error(), 2*time.Minute)
		return fmt.Errorf("%w: insufficient position: %v", ErrRedemptionRetrying, err)
	}

	timeout := r.getRedeemTimeout()

	// Lease this redemption away from the reconciliation worker for the duration of
	// this synchronous attempt, so the worker doesn't redundantly process it in
	// parallel. (Steps are idempotent regardless, but this avoids wasted API calls.)
	leaseSecs := int(timeout.Seconds()) + 30
	if _, err := r.db.ExecContext(ctx, `
		UPDATE blend_yield_redemptions
		SET next_retry_at = NOW() + ($2 || ' seconds')::interval, updated_at = NOW()
		WHERE id = $1
	`, existing.ID, fmt.Sprintf("%d", leaseSecs)); err != nil {
		return fmt.Errorf("blend: lease redemption: %w", err)
	}

	deadline := time.Now().Add(timeout)
	pollCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	if err := r.driveRedemption(pollCtx, acct, existing, amount, false); err != nil {
		return err
	}
	return nil
}

// RedeemStashYieldForTransfer is like RedeemStashYield but skips the Base→Solana sweep.
// Use this when funds are about to leave the platform via a crypto withdrawal — the
// withdrawal transfer itself will spend the USDC from the Base EOA, so sweeping first
// would race with and potentially consume the funds before the transfer can use them.
func (r *DepositRouter) RedeemStashYieldForTransfer(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, idempotencyKey string) error {
	if r == nil || r.db == nil || r.blend == nil {
		return errors.New("blend redeemer not configured")
	}
	if userID == uuid.Nil {
		return errors.New("user_id is required")
	}
	if !amount.GreaterThan(decimal.Zero) {
		return nil
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		return errors.New("redemption idempotency key is required")
	}
	amount = amount.Truncate(6)

	existing, err := r.getRedemption(ctx, idempotencyKey)
	if err != nil {
		return err
	}
	if existing != nil && existing.Status == redemptionStatusComplete {
		return nil
	}

	acct, err := r.getUserAccount(ctx, userID)
	if err != nil {
		return err
	}
	if acct == nil || acct.BlendAccountID == "" {
		return fmt.Errorf("blend: user %s has no Blend account to redeem from", userID)
	}
	if acct.SafeStatus != SafeStatusValidated {
		return fmt.Errorf("blend: user %s Safe is not validated (status=%s); cannot redeem", userID, acct.SafeStatus)
	}

	if existing == nil {
		if err := r.reserveRedemption(ctx, userID, acct.BlendAccountID, amount, r.chainID, idempotencyKey); err != nil {
			return err
		}
		existing, err = r.getRedemption(ctx, idempotencyKey)
		if err != nil {
			return err
		}
	}

	if err := r.assertSufficientPosition(ctx, userID, amount, existing.ID); err != nil {
		r.logger.Error("Blend: redemption (transfer) blocked on insufficient position — deferring for worker retry",
			zap.String("redemption_id", existing.ID.String()),
			zap.String("user_id", userID.String()),
			zap.String("amount", amount.StringFixed(6)),
			zap.Error(err))
		r.deferRedemption(ctx, existing.ID, err.Error(), 2*time.Minute)
		return fmt.Errorf("%w: insufficient position: %v", ErrRedemptionRetrying, err)
	}

	timeout := r.getRedeemTimeout()
	leaseSecs := int(timeout.Seconds()) + 30
	if _, err := r.db.ExecContext(ctx, `
		UPDATE blend_yield_redemptions
		SET next_retry_at = NOW() + ($2 || ' seconds')::interval, updated_at = NOW()
		WHERE id = $1
	`, existing.ID, fmt.Sprintf("%d", leaseSecs)); err != nil {
		return fmt.Errorf("blend: lease redemption: %w", err)
	}

	deadline := time.Now().Add(timeout)
	pollCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	// skipSweep=true: funds are leaving the platform; the crypto transfer will spend the
	// USDC directly from the Base EOA, so we must not race with a concurrent sweep.
	return r.driveRedemption(pollCtx, acct, existing, amount, true)
}

// driveRedemption advances a single redemption through quote → lock → execute →
// submit → settle, then decrements the user's positions. Bounded by pollCtx.
// skipSweep suppresses the async Base→Solana bridge goroutine (use when funds are
// about to leave the platform via a crypto withdrawal transfer).
func (r *DepositRouter) driveRedemption(ctx context.Context, acct *blendUserAccount, red *redemption, amount decimal.Decimal, skipSweep bool) error {
	// 1. Obtain a withdraw session. Refuse to proceed if a non-terminal DEPOSIT
	//    session is occupying the single per-account session slot.
	intentID := red.IntentID.String
	if intentID == "" {
		session, err := r.acquireWithdrawSession(ctx, acct.BlendAccountID, red.IdempotencyKey)
		if err != nil {
			return err
		}
		intentID = session.IntentID
	}

	// 2. Cap to Blend's live available balance to handle vault share price drift.
	//    Our DB tracks principal_amount (what we deposited), but Blend's redeemable
	//    value = shares × current_share_price, which can be slightly less (~0.07%).
	//    Without this cap, QuoteWithdraw fails with "Insufficient balance".
	if balance, balErr := r.blend.GetBalance(ctx, acct.BlendAccountID); balErr != nil {
		r.logger.Warn("Blend: GetBalance failed during redemption cap, proceeding with original amount",
			zap.String("redemption_id", red.ID.String()), zap.Error(balErr))
	} else if available := aggregateUnderlying(balance); !available.IsPositive() {
		r.logger.Warn("Blend: aggregateUnderlying returned non-positive, proceeding with original amount",
			zap.String("redemption_id", red.ID.String()), zap.String("available", available.StringFixed(6)))
	} else if amount.GreaterThan(available) {
		r.logger.Warn("Blend: capping redemption to live available balance (vault share drift)",
			zap.String("redemption_id", red.ID.String()),
			zap.String("requested", amount.StringFixed(6)),
			zap.String("available", available.StringFixed(6)),
			zap.String("diff", amount.Sub(available).StringFixed(6)))
		amount = available
		// Persist the cap so the worker uses the corrected amount on retry.
		if _, dbErr := r.db.ExecContext(ctx, `
			UPDATE blend_yield_redemptions
			SET amount = $2, updated_at = NOW()
			WHERE id = $1
		`, red.ID, amount); dbErr != nil {
			r.logger.Error("Blend: failed to persist capped redemption amount",
				zap.String("redemption_id", red.ID.String()), zap.Error(dbErr))
		}
		// Sync the withdrawal record so the user sees the actual redeemed amount,
		// not the original (higher) amount they requested.
		withdrawKey := "withdrawal-" + red.ID.String()
		if _, wErr := r.db.ExecContext(ctx, `
			UPDATE withdrawals
			SET amount = $2, total_amount = $2 + fee_amount, updated_at = NOW()
			WHERE idempotency_key = $1 AND status IN ('pending', 'processing')
		`, withdrawKey, amount); wErr != nil {
			r.logger.Error("Blend: failed to sync withdrawal amount after balance cap",
				zap.String("redemption_id", red.ID.String()), zap.Error(wErr))
		}
	}

	micro := USDCMicroUnits(amount)

	// 3. Quote the withdraw (idempotent: re-quoting an OPEN session is safe).
	destChain := red.DestinationChainID
	if destChain == 0 {
		destChain = r.chainID
	}
	if red.Status == redemptionStatusPending {
		quote, err := r.blend.QuoteWithdraw(ctx, acct.BlendAccountID, intentID, destChain, micro, false)
		if err != nil {
			return fmt.Errorf("%w: quote withdraw: %v", ErrRedemptionRetrying, err)
		}
		if _, err := r.db.ExecContext(ctx, `
			UPDATE blend_yield_redemptions
			SET intent_id = $2, intent_status = $3, quote_payload = $4, status = $5, updated_at = NOW()
			WHERE id = $1
		`, red.ID, quote.IntentID, quote.Status, []byte(quote.Payload), redemptionStatusQuoted); err != nil {
			return fmt.Errorf("blend: persist withdraw quote: %w", err)
		}
		red.IntentID = sql.NullString{String: quote.IntentID, Valid: true}
		red.QuotePayload = []byte(quote.Payload)
		red.Status = redemptionStatusQuoted
		intentID = quote.IntentID
	}

	// 3. Lock + execute + submit (tolerant of re-entry).
	if red.Status == redemptionStatusQuoted || red.Status == redemptionStatusExecuting {
		if err := r.executeWithdraw(ctx, acct, red, intentID, destChain); err != nil {
			return err
		}
	}

	// 4. Poll until SETTLED within the deadline.
	if err := r.awaitWithdrawSettlement(ctx, acct, red, amount); err != nil {
		return err
	}

	// 5. Bridge redeemed USDC from Base EOA → user's Solana wallet (async, best-effort).
	// Skipped when funds are about to leave the platform via a crypto transfer — the
	// transfer spends from the Base EOA directly, and a concurrent sweep would race it.
	// Do NOT set swept_at here — if the transfer fails, the worker's retryPendingSweeps
	// must be able to discover this complete-but-unswept redemption and bridge the funds
	// back to Solana. The 5-minute grace period in retryPendingSweeps avoids racing a
	// transfer that is still in flight.
	if skipSweep {
		return nil
	}

	sweepCtx, sweepCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				r.logger.Error("CRITICAL: panic in Solana sweep goroutine",
					zap.String("redemption_id", red.ID.String()),
					zap.Any("panic", p),
					zap.Stack("stack"))
			}
		}()
		defer sweepCancel()
		r.sweepToSolana(sweepCtx, acct, amount, red.ID, red.DestinationChainID)
	}()

	return nil
}

func (r *DepositRouter) acquireWithdrawSession(ctx context.Context, accountID, externalRef string) (*Session, error) {
	session, err := r.blend.GetOrCreateSession(ctx, accountID, externalRef, false)
	if err != nil {
		return nil, fmt.Errorf("%w: get/create withdraw session: %v", ErrRedemptionRetrying, err)
	}
	// If a deposit is occupying the session slot in a non-terminal state, we must
	// not hijack it. Surface a retryable error; the deposit settles in ~1 min.
	if session.Type == IntentTypeDeposit && isNonTerminalIntent(session.Status) {
		return nil, fmt.Errorf("%w: a deposit session is in progress; retry withdrawal shortly", ErrRedemptionRetrying)
	}
	// If the existing session is already a withdraw but in a terminal failed/cancelled
	// state, reset it to get a fresh OPEN session.
	if isTerminalIntent(session.Status) {
		session, err = r.blend.GetOrCreateSession(ctx, accountID, externalRef, true)
		if err != nil {
			return nil, fmt.Errorf("%w: reset withdraw session: %v", ErrRedemptionRetrying, err)
		}
		// Verify the reset actually produced a non-terminal session. If Blend
		// returns the same CANCELLED session (e.g. forceReset is ignored for
		// this account), returning it will cause an infinite retry loop.
		if isTerminalIntent(session.Status) {
			return nil, fmt.Errorf("blend: force-reset returned terminal session %s (%s); cannot proceed", session.IntentID, session.Status)
		}
		return session, nil
	}
	// Force-reset stale non-terminal withdraw sessions (LOCKED/SUBMITTED). A
	// previous attempt likely executed on-chain but Blend never reported SETTLED
	// (e.g. session expired). Leaving it locked causes QuoteWithdraw 409 errors
	// and blocks the account indefinitely. Resetting is safe: the Circle tx is
	// idempotent and the redemption worker will re-quote on a fresh session.
	if session.Type == IntentTypeWithdraw && (session.Status == IntentStatusLocked || session.Status == IntentStatusSubmitted) {
		r.logger.Warn("Blend: force-reset stale non-terminal withdraw session",
			zap.String("intent_id", session.IntentID),
			zap.String("status", session.Status),
			zap.String("account_id", accountID))
		session, err = r.blend.GetOrCreateSession(ctx, accountID, externalRef, true)
		if err != nil {
			return nil, fmt.Errorf("%w: reset stale withdraw session: %v", ErrRedemptionRetrying, err)
		}
		if isTerminalIntent(session.Status) {
			return nil, fmt.Errorf("blend: force-reset returned terminal session %s (%s); cannot proceed", session.IntentID, session.Status)
		}
	}
	return session, nil
}

func (r *DepositRouter) executeWithdraw(ctx context.Context, acct *blendUserAccount, red *redemption, intentID string, destChainID int64) error {
	if destChainID == 0 {
		destChainID = r.chainID
	}

	// Resolve the wallet for the destination chain — the EOA that executes the Safe
	// transaction varies per chain (e.g. Base vs Ethereum use different Circle wallets).
	destWallet, destErr := r.resolveUserWalletByChainID(ctx, acct.UserID, destChainID)
	if destErr != nil {
		return fmt.Errorf("blend: resolve wallet for chain %d: %w", destChainID, destErr)
	}

	plan, err := ParseActionPlan(red.QuotePayload)
	if err != nil {
		// Corrupt/stale quote payload — reset to pending so the retry re-quotes,
		// rather than terminally failing a redemption the ledger already counts on.
		if rerr := r.resetRedemptionForRetry(ctx, red.ID, "parse withdraw plan: "+err.Error(), time.Minute); rerr != nil {
			r.logger.Error("Blend: failed to reset redemption after plan parse failure",
				zap.String("redemption_id", red.ID.String()), zap.Error(rerr))
		}
		return fmt.Errorf("blend: parse withdraw action plan: %w", err)
	}

	// Tolerate re-entry: only lock when the session is still OPEN.
	current, err := r.blend.GetSession(ctx, acct.BlendAccountID, intentID)
	if err != nil {
		return fmt.Errorf("%w: get withdraw session before lock: %v", ErrRedemptionRetrying, err)
	}
	switch current.Status {
	case IntentStatusOpen:
		if _, err := r.blend.LockSession(ctx, acct.BlendAccountID, intentID, destWallet.Address); err != nil {
			return fmt.Errorf("%w: lock withdraw session: %v", ErrRedemptionRetrying, err)
		}
	case IntentStatusLocked, IntentStatusSubmitted, IntentStatusSettled:
		// Already progressed by a prior attempt — continue.
	case IntentStatusFailed, IntentStatusCancelled:
		// Session expired — but the on-chain tx may have already executed.
		// Check the on-chain balance before giving up.
		if fErr := r.tryOnChainSettlementFallback(ctx, acct, red, red.Amount); fErr == nil {
			return nil // finalized via on-chain proof
		}
		if red.Attempts >= 10 {
			reason := fmt.Sprintf("withdraw session %s is %s after %d attempts", intentID, current.Status, red.Attempts)
			if rerr := r.markRedemptionFailed(ctx, red.IdempotencyKey, reason); rerr != nil {
				r.logger.Error("Blend: failed to mark redemption terminal after repeated CANCELLED sessions",
					zap.String("redemption_id", red.ID.String()), zap.Error(rerr))
			}
			return fmt.Errorf("blend: %s; terminal", reason)
		}
		// Otherwise reset with backoff so the worker retries with a fresh session.
		msg := fmt.Sprintf("withdraw session %s is %s", intentID, current.Status)
		if rerr := r.resetRedemptionForRetry(ctx, red.ID, msg, 30*time.Second); rerr != nil {
			r.logger.Error("Blend: failed to reset redemption after CANCELLED session",
				zap.String("redemption_id", red.ID.String()), zap.Error(rerr))
		}
		return fmt.Errorf("%w: withdraw session %s is %s; reset to retry", ErrRedemptionRetrying, intentID, current.Status)
	}

	if _, err := r.db.ExecContext(ctx, `
		UPDATE blend_yield_redemptions SET status = $2, updated_at = NOW() WHERE id = $1
	`, red.ID, redemptionStatusExecuting); err != nil {
		return fmt.Errorf("blend: mark redemption executing: %w", err)
	}

	// Snapshot the destination wallet's on-chain USDC balance BEFORE the withdraw moves
	// any funds, and persist it once. finalizeRedemption then requires have >= pre_balance
	// + amount, so a redemption can only settle if the balance actually rose by ITS amount
	// — concurrent redemptions can't all pass against the same balance.
	if !red.PreRedeemBalance.Valid {
		pre, balErr := r.usdcBalance(ctx, destWallet.CircleWalletID)
		if balErr != nil {
			return fmt.Errorf("blend: snapshot pre-redeem balance on chain %d: %w", destChainID, balErr)
		}
		if _, err := r.db.ExecContext(ctx, `
			UPDATE blend_yield_redemptions SET pre_redeem_eoa_balance = $2, updated_at = NOW()
			WHERE id = $1 AND pre_redeem_eoa_balance IS NULL
		`, red.ID, pre); err != nil {
			return fmt.Errorf("blend: persist pre-redeem balance: %w", err)
		}
		red.PreRedeemBalance = decimal.NullDecimal{Decimal: pre, Valid: true}
	}

	executed, err := r.executor.Execute(ctx, func(ctx context.Context, chainID int64) (string, string, error) {
		w, err := r.resolveUserWalletByChainID(ctx, acct.UserID, chainID)
		if err != nil {
			return "", "", err
		}
		return w.CircleWalletID, w.Address, nil
	}, plan, fmt.Sprintf("blend-redeem-%s", red.ID.String()),
		&TrustedSafe{Address: acct.SafeAddress, OwnerEOA: destWallet.Address, ChainID: destChainID})
	if err != nil {
		return fmt.Errorf("%w: execute withdraw plan: %v", ErrRedemptionRetrying, err)
	}

	hashes := make([]TxHashRef, 0, len(executed))
	for _, ex := range executed {
		if ex.TxHash == "" && ex.TransactionID != "" {
			tx, err := r.circle.GetTransaction(ctx, ex.TransactionID)
			if err != nil {
				return fmt.Errorf("blend: resolve circle withdraw tx %s: %w", ex.TransactionID, err)
			}
			ex.TxHash = tx.TxHash
		}
		if ex.TxHash == "" {
			return fmt.Errorf("blend: circle withdraw tx %s has no hash yet", ex.TransactionID)
		}
		hashes = append(hashes, TxHashRef{Hash: ex.TxHash, ChainID: ex.ChainID})
	}

	session, err := r.blend.SubmitTxHashes(ctx, acct.BlendAccountID, intentID, hashes)
	if err != nil {
		return fmt.Errorf("%w: submit withdraw tx hashes: %v", ErrRedemptionRetrying, err)
	}
	primaryHash := ""
	if len(hashes) > 0 {
		primaryHash = hashes[len(hashes)-1].Hash
	}
	if _, err := r.db.ExecContext(ctx, `
		UPDATE blend_yield_redemptions
		SET status = $2, intent_status = $3, tx_hash = $4, submitted_at = NOW(), updated_at = NOW()
		WHERE id = $1
	`, red.ID, redemptionStatusSubmitted, session.Status, primaryHash); err != nil {
		return fmt.Errorf("blend: mark redemption submitted: %w", err)
	}
	red.Status = redemptionStatusSubmitted
	return nil
}

func (r *DepositRouter) awaitWithdrawSettlement(ctx context.Context, acct *blendUserAccount, red *redemption, amount decimal.Decimal) error {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	// After Blend has been LOCKED/SUBMITTED for this long, check on-chain
	// balance as a fallback. Blend sometimes never reports SETTLED (expired
	// sessions, internal bugs), but the USDC actually arrived in the EOA.
	settlementFallbackAfter := 3 * time.Minute
	fallbackDeadline := time.Now().Add(settlementFallbackAfter)
	fallbackChecked := false

	for {
		session, err := r.blend.GetSession(ctx, acct.BlendAccountID, red.IntentID.String)
		if err == nil {
			switch session.Status {
			case IntentStatusSettled:
				return r.finalizeRedemption(ctx, acct, red, amount, session)
			case IntentStatusFailed, IntentStatusCancelled:
				msg := session.ErrorMessage
				if msg == "" {
					msg = session.Status
				}
				// The Blend session expired/cancelled, but the on-chain tx may have
				// already executed (vault shares redeemed, USDC in EOA). Check the
				// on-chain balance before giving up — Blend's API status is not the
				// source of truth when our executor confirmed the tx.
				if fErr := r.tryOnChainSettlementFallback(ctx, acct, red, amount); fErr == nil {
					return nil // finalized via on-chain proof
				} else {
					r.logger.Warn("Blend: on-chain settlement fallback failed after session cancellation",
						zap.String("redemption_id", red.ID.String()),
						zap.String("session_status", session.Status),
						zap.Error(fErr))
				}
				// A failed/cancelled Blend session means this attempt didn't settle —
				// not that the redemption can never settle. Reset to pending (fresh
				// session + quote on retry) instead of terminally failing: the ledger
				// may already have moved, and 'failed' rows are never retried.
				//
				// However, if this pattern repeats across many attempts, the session
				// itself (or the account) is likely broken and retrying won't help.
				if red.Attempts >= 15 {
					reason := fmt.Sprintf("withdraw session %s ended in %s after %d attempts: %s", session.IntentID, session.Status, red.Attempts, msg)
					if rerr := r.markRedemptionFailed(ctx, red.IdempotencyKey, reason); rerr != nil {
						r.logger.Error("Blend: failed to mark redemption terminal after repeated poll failures",
							zap.String("redemption_id", red.ID.String()), zap.Error(rerr))
					}
					return fmt.Errorf("blend: %s; terminal", reason)
				}
				r.logger.Error("Blend: withdraw session failed — resetting redemption for worker retry",
					zap.String("redemption_id", red.ID.String()),
					zap.String("intent_id", session.IntentID),
					zap.String("session_status", session.Status),
					zap.String("session_error", msg))
				if rerr := r.resetRedemptionForRetry(ctx, red.ID, msg, time.Minute); rerr != nil {
					r.logger.Error("Blend: failed to reset redemption after session failure",
						zap.String("redemption_id", red.ID.String()), zap.Error(rerr))
				}
				return fmt.Errorf("%w: withdraw session %s ended in %s: %s", ErrRedemptionRetrying, session.IntentID, session.Status, msg)

			case IntentStatusLocked, IntentStatusSubmitted:
				// On-chain balance fallback: when Blend is stuck LOCKED/SUBMITTED,
				// check if USDC actually arrived in the EOA. Blend's reporting is
				// not the source of truth — the on-chain balance is.
				if !fallbackChecked && time.Now().After(fallbackDeadline) {
					fallbackChecked = true
					if fErr := r.tryOnChainSettlementFallback(ctx, acct, red, amount); fErr != nil {
						// Non-fatal: either balance not yet present or finalize failed.
						// Continue polling until context deadline.
						r.logger.Warn("Blend: on-chain settlement fallback did not complete, continuing to poll",
							zap.String("redemption_id", red.ID.String()), zap.Error(fErr))
					} else {
						return nil // finalized via on-chain proof
					}
				}
			}
		} else {
			r.logger.Warn("Blend: poll withdraw session failed, will retry",
				zap.String("redemption_id", red.ID.String()), zap.Error(err))
		}

		select {
		case <-ctx.Done():
			// Timed out waiting for settlement. The redemption stays in its current
			// state (executing/submitted) so a later retry can resume polling.
			// Return ErrRedemptionRetrying so the withdrawal stays in 'processing'
			// — the redemption worker will reset it via detectStuckRedemptions.
			return fmt.Errorf("%w: withdraw for redemption %s did not settle within timeout; funds remain in Safe: %v", ErrRedemptionRetrying, red.ID, ctx.Err())
		case <-ticker.C:
		}
	}
}

// tryOnChainSettlementFallback checks the EOA's on-chain USDC balance. If the
// redeemed funds have actually arrived (balance >= pre-redeem baseline + amount),
// finalize the redemption immediately without waiting for Blend to report SETTLED.
// This breaks the deadlock when Blend sessions get stuck LOCKED/SUBMITTED.
func (r *DepositRouter) tryOnChainSettlementFallback(ctx context.Context, acct *blendUserAccount, red *redemption, amount decimal.Decimal) error {
	have, balErr := r.usdcBalance(ctx, acct.CircleWalletID)
	if balErr != nil {
		return fmt.Errorf("check EOA balance: %w", balErr)
	}

	baseline := decimal.Zero
	if red.PreRedeemBalance.Valid {
		baseline = red.PreRedeemBalance.Decimal
	}
	required := baseline.Add(amount)

	if have.LessThan(required) {
		return fmt.Errorf("EOA balance %s < required %s (baseline %s + amount %s); funds not yet arrived",
			have.StringFixed(6), required.StringFixed(6), baseline.StringFixed(6), amount.StringFixed(6))
	}

	r.logger.Warn("Blend: settling via on-chain balance fallback (Blend session stuck — USDC confirmed in EOA)",
		zap.String("redemption_id", red.ID.String()),
		zap.String("eoa_balance", have.StringFixed(6)),
		zap.String("baseline", baseline.StringFixed(6)),
		zap.String("amount", amount.StringFixed(6)))

	// Synthesize a SETTLED session for finalizeRedemption. The on-chain balance
	// is the real proof; Blend's API status is irrelevant at this point.
	syntheticSession := &Session{
		IntentID: red.IntentID.String,
		Status:   IntentStatusSettled,
		Type:     IntentTypeWithdraw,
	}
	return r.finalizeRedemption(ctx, acct, red, amount, syntheticSession)
}

// finalizeRedemption decrements positions FIFO and marks the redemption complete,
// atomically — but only after confirming the redeemed USDC actually landed in the
// destination chain's EOA on-chain. Blend reporting SETTLED is necessary but not
// sufficient: the withdrawal pipeline spends these funds immediately, so we must not
// mark success unless they are truly present and spendable in the wallet.
func (r *DepositRouter) finalizeRedemption(ctx context.Context, acct *blendUserAccount, red *redemption, amount decimal.Decimal, session *Session) error {
	// Resolve the wallet for the destination chain — the balance must be checked on
	// whichever chain the withdrawal targeted (e.g. Ethereum, not Base).
	destWallet, dErr := r.resolveUserWalletByChainID(ctx, acct.UserID, red.DestinationChainID)
	if dErr != nil {
		return fmt.Errorf("blend: resolve wallet for destination chain %d: %w", red.DestinationChainID, dErr)
	}

	// Fetch on-chain balance BEFORE acquiring any DB locks to minimise lock hold time.
	// The advisory-lock check inside the transaction uses this pre-fetched value.
	var have decimal.Decimal
	if destWallet.CircleWalletID != "" {
		var balErr error
		have, balErr = r.usdcBalance(ctx, destWallet.CircleWalletID)
		if balErr != nil {
			return fmt.Errorf("blend: verify redeemed funds in EOA %s on chain %d: %w", destWallet.CircleWalletID, red.DestinationChainID, balErr)
		}
	}

	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("blend: begin redemption finalize tx: %w", err)
	}
	defer tx.Rollback()

	// Serialize check-and-finalize per user (held for the whole tx) so two concurrent
	// redemptions can't both finalize against the same balance.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, userAdvisoryLockKey(red.UserID)); err != nil {
		return fmt.Errorf("blend: advisory lock for redemption finalize: %w", err)
	}

	// Airtight on-chain proof-of-arrival. Rather than each redemption only checking its own
	// delta — which two concurrent redemptions that snapshotted the same baseline could both
	// pass against a SINGLE arrival — we require the EOA balance to cover the baseline (the
	// lowest pre-snapshot among the user's in-flight redemptions, i.e. the balance before any
	// of them started) PLUS the TOTAL amount of every in-flight redemption awaiting funds
	// (including this one). So a batch of concurrent redemptions only finalizes once enough
	// funds have arrived for ALL of them — never two against the same dollars. Self-healing: a
	// sibling whose funds never arrive is marked 'failed' by its own settlement poll, dropping
	// out of the sum so the rest can finalize. (Rows predating the snapshot column have pre
	// NULL → baseline 0 → require the full sum present, the conservative fallback.)
	if acct != nil && acct.CircleWalletID != "" {
		var agg struct {
			Baseline decimal.Decimal `db:"baseline"`
			Claimed  decimal.Decimal `db:"claimed"`
		}
		if err := tx.GetContext(ctx, &agg, `
			SELECT COALESCE(MIN(pre_redeem_eoa_balance), 0) AS baseline,
			       COALESCE(SUM(amount), 0) AS claimed
			FROM blend_yield_redemptions
			WHERE user_id = $1 AND status IN ('executing', 'submitted')
		`, red.UserID); err != nil {
			return fmt.Errorf("blend: read in-flight redemptions for finalize: %w", err)
		}
		required := agg.Baseline.Add(agg.Claimed)
		if have.LessThan(required) {
			return fmt.Errorf("%w: redemption %s: EOA %s balance %s < required %s (baseline %s + in-flight claims %s); not finalizing",
				ErrRedemptionRetrying, red.ID, acct.CircleWalletID, have.StringFixed(6), required.StringFixed(6), agg.Baseline.StringFixed(6), agg.Claimed.StringFixed(6))
		}
	}

	// Mark complete only if still in a non-terminal state (guards against double-apply).
	res, err := tx.ExecContext(ctx, `
		UPDATE blend_yield_redemptions
		SET status = $2, intent_status = $3, settled_at = NOW(), last_error = NULL, updated_at = NOW()
		WHERE id = $1 AND status <> $2
	`, red.ID, redemptionStatusComplete, session.Status)
	if err != nil {
		return fmt.Errorf("blend: mark redemption complete: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		// Already completed by a concurrent finalize — nothing to do.
		return tx.Commit()
	}

	// FIFO decrement of active positions.
	if _, err := tx.ExecContext(ctx, `
		WITH selected AS (
			SELECT id, principal_amount, redeemed_amount,
				SUM(principal_amount - redeemed_amount) OVER (ORDER BY created_at, id) AS running_available
			FROM blend_yield_positions
			WHERE user_id = $1 AND status = 'active' AND principal_amount > redeemed_amount
			ORDER BY created_at, id
		), applied AS (
			SELECT id,
				LEAST(
					principal_amount - redeemed_amount,
					GREATEST($2::numeric - COALESCE(running_available - (principal_amount - redeemed_amount), 0), 0)
				) AS redeem_amount
			FROM selected
			WHERE running_available - (principal_amount - redeemed_amount) < $2::numeric
		)
		UPDATE blend_yield_positions p
		SET redeemed_amount = p.redeemed_amount + applied.redeem_amount,
			status = CASE WHEN p.principal_amount <= p.redeemed_amount + applied.redeem_amount THEN 'redeemed' ELSE p.status END,
			updated_at = NOW()
		FROM applied
		WHERE p.id = applied.id AND applied.redeem_amount > 0
	`, red.UserID, amount); err != nil {
		return fmt.Errorf("blend: apply redemption to positions: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("blend: commit redemption finalize: %w", err)
	}
	r.logger.Info("Blend redemption settled",
		zap.String("redemption_id", red.ID.String()),
		zap.String("user_id", red.UserID.String()),
		zap.String("amount", amount.StringFixed(6)),
		zap.String("idempotency_key", red.IdempotencyKey))
	return nil
}

// EnsureRedemptionReserved durably reserves a redemption row BEFORE the caller
// moves the ledger, so a crash or failure after the ledger move still leaves a
// record the reconciliation worker can drive to settlement. Returns false when
// the user has nothing in Blend to redeem (no account / Safe not validated) —
// callers should proceed without a Blend leg in that case.
func (r *DepositRouter) EnsureRedemptionReserved(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, idempotencyKey string) (bool, error) {
	if r == nil || r.db == nil || r.blend == nil {
		return false, nil // Blend not configured — nothing to reserve
	}
	if userID == uuid.Nil || !amount.GreaterThan(decimal.Zero) {
		return false, nil
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		return false, errors.New("redemption idempotency key is required")
	}
	acct, err := r.getUserAccount(ctx, userID)
	if err != nil {
		return false, err
	}
	if acct == nil || acct.BlendAccountID == "" || acct.SafeStatus != SafeStatusValidated {
		return false, nil // funds are not in Blend custody for this user
	}
	if err := r.reserveRedemption(ctx, userID, acct.BlendAccountID, amount.Truncate(6), r.chainID, idempotencyKey); err != nil {
		return false, err
	}
	return true, nil
}

// AbandonRedemption marks a reserved redemption failed. Use when the ledger
// move that the reservation was protecting did not happen, so the redemption
// must not be driven by the worker.
func (r *DepositRouter) AbandonRedemption(ctx context.Context, idempotencyKey, reason string) error {
	if r == nil || r.db == nil {
		return nil
	}
	return r.markRedemptionFailed(ctx, idempotencyKey, reason)
}

// --- helpers ---

func (r *DepositRouter) reserveRedemption(ctx context.Context, userID uuid.UUID, accountID string, amount decimal.Decimal, destChainID int64, idempotencyKey string) error {
	if destChainID == 0 {
		destChainID = r.chainID
	}
	// Insert a fresh reservation, or REVIVE one that a prior attempt abandoned
	// (status 'failed') back to 'pending'. Without the revive, ON CONFLICT DO
	// NOTHING would silently no-op against a failed row and the caller would move
	// the ledger believing an active redemption exists — the exact ledger-ahead-of-
	// custody divergence the reservation guards against. A non-terminal or
	// 'complete' row is left untouched (the WHERE makes the update a no-op), which
	// is correct and idempotent.
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO blend_yield_redemptions (
			id, user_id, blend_account_id, amount, destination_chain_id, idempotency_key, status, next_retry_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
		ON CONFLICT (idempotency_key) DO UPDATE
			SET status = $7, amount = EXCLUDED.amount, next_retry_at = NOW(), updated_at = NOW()
			WHERE blend_yield_redemptions.status = 'failed'
	`, uuid.New(), userID, accountID, amount, destChainID, idempotencyKey, redemptionStatusPending)
	if err != nil {
		return fmt.Errorf("blend: reserve redemption: %w", err)
	}
	return nil
}

// userAdvisoryLockKey derives a stable 64-bit Postgres advisory-lock key from a user ID,
// used to serialize per-user redemption checks and finalizations.
func userAdvisoryLockKey(userID uuid.UUID) int64 {
	return int64(userID[0])<<56 | int64(userID[1])<<48 | int64(userID[2])<<40 | int64(userID[3])<<32 |
		int64(userID[4])<<24 | int64(userID[5])<<16 | int64(userID[6])<<8 | int64(userID[7])
}

func (r *DepositRouter) assertSufficientPosition(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, currentRedemptionID uuid.UUID) error {
	// Hold the advisory lock across BOTH reads in one transaction. pg_advisory_xact_lock is
	// released at end-of-transaction, so it must run inside an explicit tx — issued on the
	// pooled connection (autocommit) it would release immediately and not protect the reads,
	// letting two concurrent RedeemStashYield calls both pass.
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("blend: begin position check tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, userAdvisoryLockKey(userID)); err != nil {
		return fmt.Errorf("blend: advisory lock for redemption check: %w", err)
	}
	var available decimal.Decimal
	if err := tx.GetContext(ctx, &available, `
		SELECT COALESCE(SUM(principal_amount - redeemed_amount), 0)
		FROM blend_yield_positions
		WHERE user_id = $1 AND status = 'active'
	`, userID); err != nil {
		return fmt.Errorf("blend: read user position: %w", err)
	}
	var reserved decimal.Decimal
	if err := tx.GetContext(ctx, &reserved, `
		SELECT COALESCE(SUM(amount), 0)
		FROM blend_yield_redemptions
		WHERE user_id = $1
			AND id <> $2
			AND status IN ('pending','quoted','executing','submitted')
	`, userID, currentRedemptionID); err != nil {
		return fmt.Errorf("blend: read reserved redemptions: %w", err)
	}
	spendable := available.Sub(reserved)
	if spendable.LessThan(amount) {
		return fmt.Errorf("blend: insufficient yield position: have %s spendable (%s reserved), need %s",
			spendable.StringFixed(6), reserved.StringFixed(6), amount.StringFixed(6))
	}
	return tx.Commit()
}

// deferRedemption reschedules a non-terminal redemption for a later worker
// retry, recording the reason. Used for conditions expected to resolve on
// their own (positions not yet recorded, transient provider state).
func (r *DepositRouter) deferRedemption(ctx context.Context, redID uuid.UUID, reason string, delay time.Duration) {
	if _, err := r.db.ExecContext(ctx, `
		UPDATE blend_yield_redemptions
		SET last_error = $2, next_retry_at = NOW() + ($3 || ' seconds')::interval, updated_at = NOW()
		WHERE id = $1 AND status NOT IN ('complete','failed')
	`, redID, reason, fmt.Sprintf("%d", int(delay.Seconds()))); err != nil {
		r.logger.Error("Blend: defer redemption failed",
			zap.String("redemption_id", redID.String()), zap.Error(err))
	}
}

// resetRedemptionForRetry returns a redemption to pending with a fresh slate
// (no intent/quote) so the next attempt re-quotes with a new session.
func (r *DepositRouter) resetRedemptionForRetry(ctx context.Context, redID uuid.UUID, reason string, delay time.Duration) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE blend_yield_redemptions
		SET intent_id = NULL, intent_status = NULL, quote_payload = NULL, status = $2,
			last_error = $3, next_retry_at = NOW() + ($4 || ' seconds')::interval, updated_at = NOW()
		WHERE id = $1 AND status NOT IN ('complete','failed')
	`, redID, redemptionStatusPending, reason, fmt.Sprintf("%d", int(delay.Seconds())))
	return err
}

func (r *DepositRouter) markRedemptionFailed(ctx context.Context, idempotencyKey, reason string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE blend_yield_redemptions
		SET status = $2, last_error = $3, updated_at = NOW()
		WHERE idempotency_key = $1 AND status NOT IN ($4)
	`, idempotencyKey, redemptionStatusFailed, reason, redemptionStatusComplete)
	return err
}

func (r *DepositRouter) markRedemptionFailedByID(ctx context.Context, id uuid.UUID, reason string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE blend_yield_redemptions
		SET status = $2, last_error = $3, updated_at = NOW()
		WHERE id = $1 AND status NOT IN ($4)
	`, id, redemptionStatusFailed, reason, redemptionStatusComplete)
	return err
}

func (r *DepositRouter) getRedemption(ctx context.Context, idempotencyKey string) (*redemption, error) {
	var red redemption
	err := r.db.GetContext(ctx, &red, `
		SELECT id, user_id, blend_account_id, amount, destination_chain_id,
			intent_id, intent_status, quote_payload, tx_hash, submitted_at, settled_at,
			idempotency_key, status, attempts, last_error, next_retry_at, pre_redeem_eoa_balance
		FROM blend_yield_redemptions WHERE idempotency_key = $1
	`, idempotencyKey)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("blend: get redemption: %w", err)
	}
	return &red, nil
}

func (r *DepositRouter) getUserAccount(ctx context.Context, userID uuid.UUID) (*blendUserAccount, error) {
	var acct blendUserAccount
	err := r.db.GetContext(ctx, &acct, `
		SELECT id, user_id, eoa_address, blend_account_id, COALESCE(safe_address,'') AS safe_address,
			chain_id, safe_status, safe_requested_at, safe_deployed_at, circle_wallet_id
		FROM blend_user_accounts WHERE user_id = $1
	`, userID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("blend: get user account: %w", err)
	}
	return &acct, nil
}

func isNonTerminalIntent(status string) bool {
	switch status {
	case IntentStatusOpen, IntentStatusLocked, IntentStatusSubmitted:
		return true
	}
	return false
}

func isTerminalIntent(status string) bool {
	switch status {
	case IntentStatusFailed, IntentStatusCancelled:
		return true
	}
	return false
}
