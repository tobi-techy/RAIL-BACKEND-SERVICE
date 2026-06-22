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
		if err := r.reserveRedemption(ctx, userID, acct.BlendAccountID, amount, idempotencyKey); err != nil {
			return err
		}
		existing, err = r.getRedemption(ctx, idempotencyKey)
		if err != nil {
			return err
		}
	}

	// Verify the user actually has enough principal to cover this redemption,
	// EXCLUDING amounts already reserved by other in-flight redemptions.
	if err := r.assertSufficientPosition(ctx, userID, amount, existing.ID); err != nil {
		_ = r.markRedemptionFailed(ctx, idempotencyKey, err.Error())
		return err
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

	if err := r.driveRedemption(pollCtx, acct, existing, amount); err != nil {
		return err
	}
	return nil
}

// driveRedemption advances a single redemption through quote → lock → execute →
// submit → settle, then decrements the user's positions. Bounded by pollCtx.
func (r *DepositRouter) driveRedemption(ctx context.Context, acct *blendUserAccount, red *redemption, amount decimal.Decimal) error {
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

	micro := USDCMicroUnits(amount)

	// 2. Quote the withdraw (idempotent: re-quoting an OPEN session is safe).
	if red.Status == redemptionStatusPending {
		quote, err := r.blend.QuoteWithdraw(ctx, acct.BlendAccountID, intentID, r.chainID, micro, false)
		if err != nil {
			return fmt.Errorf("blend: quote withdraw: %w", err)
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
		if err := r.executeWithdraw(ctx, acct, red, intentID); err != nil {
			return err
		}
	}

	// 4. Poll until SETTLED within the deadline.
	return r.awaitWithdrawSettlement(ctx, acct, red, amount)
}

func (r *DepositRouter) acquireWithdrawSession(ctx context.Context, accountID, externalRef string) (*Session, error) {
	session, err := r.blend.GetOrCreateSession(ctx, accountID, externalRef, false)
	if err != nil {
		return nil, fmt.Errorf("blend: get/create withdraw session: %w", err)
	}
	// If a deposit is occupying the session slot in a non-terminal state, we must
	// not hijack it. Surface a retryable error; the deposit settles in ~1 min.
	if session.Type == IntentTypeDeposit && isNonTerminalIntent(session.Status) {
		return nil, &APIError{
			Status:  409,
			Message: "FLOWPLAN_CONFLICT: a deposit session is in progress; retry withdrawal shortly",
			Method:  "POST",
			Path:    "/intent/session",
		}
	}
	// If the existing session is already a withdraw but in a terminal failed/cancelled
	// state, reset it to get a fresh OPEN session.
	if isTerminalIntent(session.Status) {
		session, err = r.blend.GetOrCreateSession(ctx, accountID, externalRef, true)
		if err != nil {
			return nil, fmt.Errorf("blend: reset withdraw session: %w", err)
		}
	}
	return session, nil
}

func (r *DepositRouter) executeWithdraw(ctx context.Context, acct *blendUserAccount, red *redemption, intentID string) error {
	plan, err := ParseActionPlan(red.QuotePayload)
	if err != nil {
		_ = r.markRedemptionFailed(ctx, red.IdempotencyKey, "parse withdraw plan: "+err.Error())
		return fmt.Errorf("blend: parse withdraw action plan: %w", err)
	}

	// Tolerate re-entry: only lock when the session is still OPEN.
	current, err := r.blend.GetSession(ctx, acct.BlendAccountID, intentID)
	if err != nil {
		return fmt.Errorf("blend: get withdraw session before lock: %w", err)
	}
	switch current.Status {
	case IntentStatusOpen:
		if _, err := r.blend.LockSession(ctx, acct.BlendAccountID, intentID, acct.EOAAddress); err != nil {
			return fmt.Errorf("blend: lock withdraw session: %w", err)
		}
	case IntentStatusLocked, IntentStatusSubmitted, IntentStatusSettled:
		// Already progressed by a prior attempt — continue.
	case IntentStatusFailed, IntentStatusCancelled:
		return fmt.Errorf("blend: withdraw session %s is %s; cannot execute", intentID, current.Status)
	}

	if _, err := r.db.ExecContext(ctx, `
		UPDATE blend_yield_redemptions SET status = $2, updated_at = NOW() WHERE id = $1
	`, red.ID, redemptionStatusExecuting); err != nil {
		return fmt.Errorf("blend: mark redemption executing: %w", err)
	}

	executed, err := r.executor.Execute(ctx, acct.CircleWalletID, plan, fmt.Sprintf("blend-redeem-%s", red.ID.String()),
		&TrustedSafe{Address: acct.SafeAddress, OwnerEOA: acct.EOAAddress, ChainID: acct.ChainID})
	if err != nil {
		return fmt.Errorf("blend: execute withdraw plan: %w", err)
	}

	hashes := make([]TxHashRef, 0, len(executed))
	for _, ex := range executed {
		if ex.TxHash == "" {
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
		return fmt.Errorf("blend: submit withdraw tx hashes: %w", err)
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

	for {
		session, err := r.blend.GetSession(ctx, acct.BlendAccountID, red.IntentID.String)
		if err == nil {
			switch session.Status {
			case IntentStatusSettled:
				return r.finalizeRedemption(ctx, red, amount, session)
			case IntentStatusFailed, IntentStatusCancelled:
				msg := session.ErrorMessage
				if msg == "" {
					msg = session.Status
				}
				_ = r.markRedemptionFailed(ctx, red.IdempotencyKey, msg)
				return fmt.Errorf("blend: withdraw session %s ended in %s: %s", session.IntentID, session.Status, msg)
			}
		} else {
			r.logger.Warn("Blend: poll withdraw session failed, will retry",
				zap.String("redemption_id", red.ID.String()), zap.Error(err))
		}

		select {
		case <-ctx.Done():
			// Timed out waiting for settlement. Leave the redemption in its current
			// state so a later retry (same idempotency key) can resume polling, and
			// return an error so the withdrawal does NOT spend unsettled funds.
			return fmt.Errorf("blend: withdraw for redemption %s did not settle within timeout; funds remain in Safe: %w", red.ID, ctx.Err())
		case <-ticker.C:
		}
	}
}

// finalizeRedemption decrements positions FIFO and marks the redemption complete,
// atomically.
func (r *DepositRouter) finalizeRedemption(ctx context.Context, red *redemption, amount decimal.Decimal, session *Session) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("blend: begin redemption finalize tx: %w", err)
	}
	defer tx.Rollback()

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

// --- helpers ---

func (r *DepositRouter) reserveRedemption(ctx context.Context, userID uuid.UUID, accountID string, amount decimal.Decimal, idempotencyKey string) error {
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO blend_yield_redemptions (
			id, user_id, blend_account_id, amount, destination_chain_id, idempotency_key, status, next_retry_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
		ON CONFLICT (idempotency_key) DO NOTHING
	`, uuid.New(), userID, accountID, amount, r.chainID, idempotencyKey, redemptionStatusPending)
	if err != nil {
		return fmt.Errorf("blend: reserve redemption: %w", err)
	}
	_ = res
	return nil
}

func (r *DepositRouter) assertSufficientPosition(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, currentRedemptionID uuid.UUID) error {
	// Use advisory lock on user to serialize concurrent redemption checks.
	// This prevents two concurrent RedeemStashYield calls from both passing.
	lockKey := int64(userID[0])<<56 | int64(userID[1])<<48 | int64(userID[2])<<40 | int64(userID[3])<<32 |
		int64(userID[4])<<24 | int64(userID[5])<<16 | int64(userID[6])<<8 | int64(userID[7])
	if _, err := r.db.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, lockKey); err != nil {
		return fmt.Errorf("blend: advisory lock for redemption check: %w", err)
	}
	var available decimal.Decimal
	if err := r.db.GetContext(ctx, &available, `
		SELECT COALESCE(SUM(principal_amount - redeemed_amount), 0)
		FROM blend_yield_positions
		WHERE user_id = $1 AND status = 'active'
	`, userID); err != nil {
		return fmt.Errorf("blend: read user position: %w", err)
	}
	var reserved decimal.Decimal
	if err := r.db.GetContext(ctx, &reserved, `
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
	return nil
}

func (r *DepositRouter) markRedemptionFailed(ctx context.Context, idempotencyKey, reason string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE blend_yield_redemptions
		SET status = $2, last_error = $3, updated_at = NOW()
		WHERE idempotency_key = $1 AND status NOT IN ($4)
	`, idempotencyKey, redemptionStatusFailed, reason, redemptionStatusComplete)
	return err
}

func (r *DepositRouter) getRedemption(ctx context.Context, idempotencyKey string) (*redemption, error) {
	var red redemption
	err := r.db.GetContext(ctx, &red, `
		SELECT id, user_id, blend_account_id, amount, destination_chain_id,
			intent_id, intent_status, quote_payload, tx_hash, submitted_at, settled_at,
			idempotency_key, status, attempts, last_error, next_retry_at
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
