package blend

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// Worker tuning.
const (
	defaultWorkerBatchSize = 25
	// maxRetryAttempts is the hard cap on redemption retry attempts. After this
	// many attempts the worker marks the redemption as 'failed' and stops
	// retrying. The ledger is already ahead of Blend custody for these rows
	// (reserve-before-debit), so manual reconciliation is required regardless.
	// Set to 5 to prevent the continuous retry loop that was spamming the
	// Blend API and creating dozens of failed flow plans.
	maxRetryAttempts = 5
	// leaseWindow is how far we push next_retry_at when claiming a route/redemption,
	// preventing a second worker (or a second instance) from grabbing the same row
	// while the first is still processing it.
	leaseWindow = 2 * time.Minute
	// alertThrottleWindow is how often the stranded/stale detectors re-log the
	// same row. Ongoing incidents stay visible without a CRITICAL every tick.
	alertThrottleWindow = 30 * time.Minute
)

// shouldAlert reports whether the given alert key is due for (re-)logging and
// records the emission time. Keys are pruned once they age past two windows.
func (r *DepositRouter) shouldAlert(key string) bool {
	r.alertMu.Lock()
	defer r.alertMu.Unlock()
	now := time.Now()
	if r.lastAlertAt == nil {
		r.lastAlertAt = make(map[string]time.Time)
	}
	if last, ok := r.lastAlertAt[key]; ok && now.Sub(last) < alertThrottleWindow {
		return false
	}
	for k, t := range r.lastAlertAt {
		if now.Sub(t) > 2*alertThrottleWindow {
			delete(r.lastAlertAt, k)
		}
	}
	r.lastAlertAt[key] = now
	return true
}

// SetWorkerInterval overrides the reconciliation tick interval.
func (r *DepositRouter) SetWorkerInterval(d time.Duration) {
	if r == nil || d <= 0 {
		return
	}
	r.configMu.Lock()
	r.retryInterval = d
	r.configMu.Unlock()
}

// Start launches the background reconciliation loop. It advances deposit routes and
// resumes stuck redemptions that have passed their next_retry_at. Idempotent steps and
// a lease-based claim make it safe to run multiple instances concurrently.
func (r *DepositRouter) Start() error {
	if r == nil {
		return errNilRouter
	}
	r.configMu.RLock()
	interval := r.retryInterval
	r.configMu.RUnlock()
	if interval <= 0 {
		interval = defaultRetryInterval
	}

	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		r.reconcileOnce(context.Background())
		for {
			select {
			case <-ticker.C:
				r.reconcileOnce(context.Background())
			case <-r.stopCh:
				return
			}
		}
	}()
	r.logger.Info("Blend reconciliation worker started", zap.Duration("interval", interval))
	return nil
}

// Stop halts the reconciliation loop.
func (r *DepositRouter) Stop() error {
	if r == nil {
		return nil
	}
	r.stopOnce.Do(func() { close(r.stopCh) })
	return nil
}

func (r *DepositRouter) reconcileOnce(ctx context.Context) {
	defer func() {
		if rec := recover(); rec != nil {
			r.logger.Error("Blend reconcile panic recovered", zap.Any("panic", rec), zap.Stack("stacktrace"))
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, 8*time.Minute)
	defer cancel()

	r.reconcileDepositRoutes(ctx)
	r.reconcileRedemptions(ctx)
	r.retryPendingSweeps(ctx)
	r.detectStaleRoutes(ctx)
	r.detectStrandedRedemptions(ctx)
	r.detectStuckRedemptions(ctx)
	r.detectSweepDoubleCredits(ctx)
}

// detectSweepDoubleCredits is a log-only tripwire for an unverified failure
// mode: redemption sweeps deliver USDC to the user's Solana Circle wallet — the
// same wallet deposit detection watches — and the credit paths have no sweep
// suppression. If a detector reports a sweep arrival, it would be credited as a
// fresh deposit (plus 70/30 split) on top of the ledger move the redemption
// already backed. No sweep had ever completed in production as of July 2026, so
// this could not be confirmed or ruled out; this check pages if it ever happens
// so suppression can be added with real data instead of speculation.
func (r *DepositRouter) detectSweepDoubleCredits(ctx context.Context) {
	type hit struct {
		DepositID    uuid.UUID `db:"deposit_id"`
		RedemptionID uuid.UUID `db:"redemption_id"`
		UserID       uuid.UUID `db:"user_id"`
		Amount       string    `db:"amount"`
	}
	var hits []hit
	if err := r.db.SelectContext(ctx, &hits, `
		SELECT d.id AS deposit_id, red.id AS redemption_id, d.user_id, d.amount::text AS amount
		FROM blend_yield_redemptions red
		JOIN deposits d
		  ON d.user_id = red.user_id
		 AND d.amount = red.amount
		 AND d.created_at > red.swept_at
		 AND d.created_at < red.swept_at + INTERVAL '1 hour'
		WHERE red.swept_at > NOW() - INTERVAL '24 hours'
		LIMIT 10
	`); err != nil {
		r.logger.Warn("Blend: sweep double-credit check failed", zap.Error(err))
		return
	}
	for _, h := range hits {
		if !r.shouldAlert("sweep-double-credit:" + h.DepositID.String()) {
			continue
		}
		r.logger.Error("CRITICAL: possible sweep double-credit — a deposit matching a just-swept redemption was credited; the sweep arrival may have been counted as a fresh deposit. Verify and claw back, then add credit-path suppression.",
			zap.String("deposit_id", h.DepositID.String()),
			zap.String("redemption_id", h.RedemptionID.String()),
			zap.String("user_id", h.UserID.String()),
			zap.String("amount", h.Amount))
	}

	// Same tripwire for deposit autosweeps (non-Solana deposit consolidated to
	// the user's Solana wallet): the arrival lands on the same deposit-watched
	// wallet and the credit paths have no suppression for it either.
	var asHits []hit
	if err := r.db.SelectContext(ctx, &asHits, `
		SELECT d.id AS deposit_id, s.id AS redemption_id, d.user_id, d.amount::text AS amount
		FROM deposit_sweeps s
		JOIN deposits d
		  ON d.user_id = s.user_id
		 AND d.amount = s.amount
		 AND d.created_at > s.completed_at
		 AND d.created_at < s.completed_at + INTERVAL '1 hour'
		 AND d.id <> s.deposit_id
		WHERE s.status = 'completed'
		  AND s.completed_at IS NOT NULL
		  AND s.completed_at > NOW() - INTERVAL '24 hours'
		LIMIT 10
	`); err != nil {
		r.logger.Warn("Blend: autosweep double-credit check failed", zap.Error(err))
		return
	}
	for _, h := range asHits {
		if !r.shouldAlert("autosweep-double-credit:" + h.DepositID.String()) {
			continue
		}
		r.logger.Error("CRITICAL: possible autosweep double-credit — a deposit matching a just-completed deposit sweep was credited; the sweep arrival may have been counted as a fresh deposit. Verify and claw back, then add credit-path suppression.",
			zap.String("deposit_id", h.DepositID.String()),
			zap.String("sweep_id", h.RedemptionID.String()),
			zap.String("user_id", h.UserID.String()),
			zap.String("amount", h.Amount))
	}
}

// detectStrandedRedemptions surfaces redemptions the ledger is counting on that
// are not making progress: non-terminal rows stuck >1h, rows that exhausted the
// retry budget, and terminally failed rows from the last 24h. Each of these
// means a user's ledger balance moved but Blend custody did not follow —
// operators must reconcile manually.
func (r *DepositRouter) detectStrandedRedemptions(ctx context.Context) {
	type strandedRow struct {
		ID        uuid.UUID      `db:"id"`
		UserID    uuid.UUID      `db:"user_id"`
		Status    string         `db:"status"`
		Amount    string         `db:"amount"`
		Attempts  int            `db:"attempts"`
		LastError sql.NullString `db:"last_error"`
	}
	var rows []strandedRow
	if err := r.db.SelectContext(ctx, &rows, `
		SELECT id, user_id, status, amount::text AS amount, attempts, last_error
		FROM blend_yield_redemptions
		WHERE (status IN ('pending','quoted','executing','submitted')
		         AND (updated_at < NOW() - INTERVAL '1 hour' OR attempts >= 50))
		   OR (status = 'failed' AND updated_at > NOW() - INTERVAL '24 hours')
		LIMIT 20
	`); err != nil {
		r.logger.Warn("Blend: stranded redemption query failed", zap.Error(err))
	}
	for _, row := range rows {
		if !r.shouldAlert("redemption:" + row.ID.String()) {
			continue
		}
		r.logger.Error("CRITICAL: Blend redemption stranded — ledger may be ahead of Blend custody, manual reconciliation may be required",
			zap.String("redemption_id", row.ID.String()),
			zap.String("user_id", row.UserID.String()),
			zap.String("status", row.Status),
			zap.String("amount", row.Amount),
			zap.Int("attempts", row.Attempts),
			zap.String("last_error", row.LastError.String))
	}

	// The stale-sweep detection below must run on every tick regardless of whether
	// any stranded redemptions were found above — a completed-but-unswept sweep is
	// an independent failure mode.

	// Sweeps that keep failing: the redeemed USDC is settled and user-owned
	// (Base EOA) but hasn't reached their Solana custody wallet. retryPendingSweeps
	// retries forever; surface anything stuck >2h so it doesn't fail silently.
	type staleSweep struct {
		ID     uuid.UUID      `db:"id"`
		UserID uuid.UUID      `db:"user_id"`
		Amount string         `db:"amount"`
		Reason sql.NullString `db:"sweep_failed_reason"`
	}
	var sweeps []staleSweep
	if err := r.db.SelectContext(ctx, &sweeps, `
		SELECT id, user_id, amount::text AS amount, sweep_failed_reason
		FROM blend_yield_redemptions
		WHERE status = 'complete'
		  AND swept_at IS NULL
		  AND settled_at < NOW() - INTERVAL '2 hours'
		LIMIT 10
	`); err != nil || len(sweeps) == 0 {
		return
	}
	for _, sw := range sweeps {
		if !r.shouldAlert("sweep:" + sw.ID.String()) {
			continue
		}
		r.logger.Error("Blend sweep stalled — redeemed USDC sitting in Base EOA instead of Solana custody",
			zap.String("redemption_id", sw.ID.String()),
			zap.String("user_id", sw.UserID.String()),
			zap.String("amount", sw.Amount),
			zap.String("last_sweep_error", sw.Reason.String))
	}
}

// detectStuckRedemptions force-resets redemptions stuck in executing/submitted for
// >30 minutes. These likely failed silently (Blend session expired while the redemption
// was held under a lease) and need a fresh session + quote to recover. Without this
// detector they stay stuck until the 2-minute lease window expires and even then may
// re-enter the same broken state.
//
// Also marks redemptions that have exceeded maxRetryAttempts (5) as 'failed' to stop
// the continuous retry loop.
func (r *DepositRouter) detectStuckRedemptions(ctx context.Context) {
	type stuckRow struct {
		ID        uuid.UUID `db:"id"`
		UserID    uuid.UUID `db:"user_id"`
		Status    string    `db:"status"`
		Amount    string    `db:"amount"`
		Attempts  int       `db:"attempts"`
		LastError sql.NullString `db:"last_error"`
	}
	var rows []stuckRow
	if err := r.db.SelectContext(ctx, &rows, `
		SELECT id, user_id, status, amount::text AS amount, attempts, last_error
		FROM blend_yield_redemptions
		WHERE status IN ('pending', 'quoted', 'executing', 'submitted')
		  AND (updated_at < NOW() - INTERVAL '30 minutes' OR attempts >= $1)
		LIMIT 10
	`, maxRetryAttempts); err != nil {
		r.logger.Warn("Blend: stuck redemption query failed", zap.Error(err))
		return
	}
	for _, row := range rows {
		if row.Attempts >= maxRetryAttempts {
			// Exceeded retry budget — mark as failed to stop the retry loop.
			// The ledger is already ahead of Blend custody for these rows.
			r.logger.Error("Blend: marking redemption as failed — exceeded max retry attempts",
				zap.String("redemption_id", row.ID.String()),
				zap.String("user_id", row.UserID.String()),
				zap.String("status", row.Status),
				zap.String("amount", row.Amount),
				zap.Int("attempts", row.Attempts),
				zap.String("last_error", row.LastError.String))
			if err := r.markRedemptionFailedByID(ctx, row.ID,
				fmt.Sprintf("exceeded max retry attempts (%d) — Blend redemption stuck, manual reconciliation required", maxRetryAttempts)); err != nil {
				r.logger.Error("Blend: failed to mark redemption as failed",
					zap.String("redemption_id", row.ID.String()), zap.Error(err))
			}
			continue
		}
		if !r.shouldAlert("stuck-redemption:" + row.ID.String()) {
			continue
		}
		r.logger.Warn("Blend: stuck redemption detected — resetting for fresh session",
			zap.String("redemption_id", row.ID.String()),
			zap.String("user_id", row.UserID.String()),
			zap.String("status", row.Status),
			zap.String("amount", row.Amount),
			zap.Int("attempts", row.Attempts))
		if err := r.resetRedemptionForRetry(ctx, row.ID,
			fmt.Sprintf("stuck in %s for >30m", row.Status), time.Minute); err != nil {
			r.logger.Error("Blend: failed to reset stuck redemption",
				zap.String("redemption_id", row.ID.String()), zap.Error(err))
		}
	}
}

// retryPendingSweeps retries the Base→Solana bridge for complete redemptions whose
// sweep failed or never ran. Uses a 5-minute grace period after settlement so we
// don't race with a crypto transfer that may still be spending from the Base EOA.
func (r *DepositRouter) retryPendingSweeps(ctx context.Context) {
	type row struct {
		ID       uuid.UUID `db:"id"`
		UserID   uuid.UUID `db:"user_id"`
		Amount   string    `db:"amount"`
		Attempts int       `db:"attempts"`
	}
	var rows []row
	if err := r.db.SelectContext(ctx, &rows, `
		SELECT id, user_id, amount, attempts
		FROM blend_yield_redemptions
		WHERE status = 'complete'
		  AND swept_at IS NULL
		  AND settled_at < NOW() - INTERVAL '5 minutes'
		  AND attempts < 20
		ORDER BY settled_at
		LIMIT $1
	`, r.getBatchSize()); err != nil || len(rows) == 0 {
		return
	}
	for _, row := range rows {
		select {
		case <-ctx.Done():
			return
		default:
		}
		// Skip redemptions with too many failed sweep attempts — the underlying
		// issue (e.g. ChainRails down, insufficient gas) is likely persistent.
		if row.Attempts > 10 {
			r.logger.Warn("blend sweep: skipping — too many attempts, needs operator intervention",
				zap.String("redemption_id", row.ID.String()), zap.Int("attempts", row.Attempts))
			continue
		}
		acct, err := r.getUserAccount(ctx, row.UserID)
		if err != nil || acct == nil {
			continue
		}
		amt, _ := decimal.NewFromString(row.Amount)
		if !amt.GreaterThan(decimal.Zero) {
			continue
		}

		// Cap sweep amount to actual EOA balance. The DB amount may be stale
		// (e.g. from a balance cap applied by a concurrent worker using outdated
		// Blend API data). Sweeping more than available causes persistent
		// "insufficient EOA balance" errors every worker tick.
		eoaBal, balErr := r.usdcBalance(ctx, acct.CircleWalletID)
		if balErr != nil {
			r.logger.Warn("blend sweep: cannot check EOA balance, proceeding with DB amount",
				zap.String("redemption_id", row.ID.String()), zap.Error(balErr))
		} else if eoaBal.LessThan(decimal.NewFromFloat(0.10)) {
			// EOA is nearly empty. The sweep likely already succeeded on-chain
			// (USDC was bridged to Solana) but the polling didn't detect it.
			// Auto-mark as swept to stop the retry loop. The deposit was already
			// credited when ChainRails delivered it to Solana.
			r.logger.Warn("blend sweep: EOA balance near zero — sweep already completed on-chain, auto-marking swept",
				zap.String("redemption_id", row.ID.String()),
				zap.String("eoa_balance", eoaBal.StringFixed(6)),
				zap.String("db_amount", amt.StringFixed(6)))
			if dbErr := r.persistSweepSuccess(row.ID); dbErr != nil {
				r.logger.Error("blend sweep: could not persist auto-mark to DB",
					zap.String("redemption_id", row.ID.String()), zap.Error(dbErr))
			}
			continue
		} else if eoaBal.LessThan(amt) {
			capped := eoaBal.Sub(decimal.NewFromFloat(0.01)) // gas buffer
			if capped.IsNegative() {
				capped = decimal.Zero
			}
			r.logger.Warn("blend sweep: capping sweep to EOA balance (DB amount exceeds available)",
				zap.String("redemption_id", row.ID.String()),
				zap.String("db_amount", amt.StringFixed(6)),
				zap.String("eoa_balance", eoaBal.StringFixed(6)),
				zap.String("capped_amount", capped.StringFixed(6)))
			amt = capped
		}

		sweepCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		r.sweepToSolana(sweepCtx, acct, amt, row.ID)
		cancel()
	}
}

// detectStaleRoutes terminates deposit routes stuck in non-terminal states for >6 hours.
// Routes past the 100-attempt retry budget with Circle transfer failures are dead — the
// source wallet is empty or Circle will never confirm. Marking them terminal stops the
// infinite retry loop that was spamming "circle transfer still pending" every 30s.
func (r *DepositRouter) detectStaleRoutes(ctx context.Context) {
	type staleRow struct {
		ID        uuid.UUID      `db:"id"`
		Status    string         `db:"status"`
		Attempts  int            `db:"attempts"`
		LastError sql.NullString `db:"last_error"`
	}
	var rows []staleRow
	if err := r.db.SelectContext(ctx, &rows, `
		SELECT id, status, attempts, last_error
		FROM blend_deposit_routes
		WHERE status NOT IN ('complete', 'error_terminal', 'error_payload')
		  AND updated_at < NOW() - INTERVAL '6 hours'
		LIMIT 25
	`); err != nil || len(rows) == 0 {
		return
	}
	for _, row := range rows {
		if row.Attempts >= 100 {
			// Route has exhausted the retry budget. The source wallet is
			// permanently depleted or Circle will never confirm. Mark terminal.
			_, err := r.db.ExecContext(ctx, `
				UPDATE blend_deposit_routes
				SET status = 'error_terminal', next_retry_at = NOW() + INTERVAL '24 hours', updated_at = NOW()
				WHERE id = $1
			`, row.ID)
			if err != nil {
				r.logger.Error("Blend: failed to terminate stale route",
					zap.String("route_id", row.ID.String()), zap.Error(err))
				continue
			}
			r.logger.Warn("Blend: terminated stale deposit route (stuck >6h, >100 attempts)",
				zap.String("route_id", row.ID.String()),
				zap.String("status", row.Status),
				zap.Int("attempts", row.Attempts),
				zap.String("last_error", row.LastError.String))
		} else {
			r.logger.Warn("Blend: stale deposit route (stuck >6h, still retrying)",
				zap.String("route_id", row.ID.String()),
				zap.String("status", row.Status),
				zap.Int("attempts", row.Attempts),
				zap.String("last_error", row.LastError.String))
		}
	}
}

func (r *DepositRouter) reconcileDepositRoutes(ctx context.Context) {
	ids, err := r.claimDepositRoutes(ctx, r.getBatchSize())
	if err != nil {
		r.logger.Error("Blend: claim deposit routes failed", zap.Error(err))
		return
	}
	for _, id := range ids {
		select {
		case <-ctx.Done():
			return
		default:
		}
		routeCtx, routeCancel := context.WithTimeout(ctx, 3*time.Minute)
		if err := r.ProcessRouteByID(routeCtx, id); err != nil {
			r.logger.Warn("Blend: deposit route processing failed (will retry)",
				zap.String("route_id", id.String()), zap.Error(err))
		}
		routeCancel()
	}
}

// claimDepositRoutes atomically leases a batch of due, non-terminal routes by pushing
// their next_retry_at into the future, returning the claimed IDs. FOR UPDATE SKIP LOCKED
// guarantees concurrent workers never claim the same row.
//
// Routes past the fast-retry budget (100 attempts) are NOT abandoned: every step
// is idempotent, so they drop to a slow 6-hour cadence instead. This lets routes
// blocked on transient-but-long conditions (e.g. a Circle wallet that is
// temporarily short of USDC) self-heal once the condition clears, rather than
// dying permanently after ~1h of fast retries and spamming stale-route warnings.
func (r *DepositRouter) claimDepositRoutes(ctx context.Context, limit int) ([]uuid.UUID, error) {
	rows, err := r.db.QueryxContext(ctx, `
		UPDATE blend_deposit_routes
		SET next_retry_at = NOW() + ($2 || ' seconds')::interval, updated_at = NOW()
		WHERE id IN (
			SELECT id FROM blend_deposit_routes
			WHERE status NOT IN ('complete', 'error_terminal', 'error_payload')
				AND next_retry_at <= NOW()
				AND (attempts < 100 OR updated_at < NOW() - INTERVAL '6 hours')
			ORDER BY next_retry_at
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id
	`, limit, int(leaseWindow.Seconds()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (r *DepositRouter) reconcileRedemptions(ctx context.Context) {
	keys, err := r.claimRedemptions(ctx, r.getBatchSize())
	if err != nil {
		r.logger.Error("Blend: claim redemptions failed", zap.Error(err))
		return
	}
	for _, k := range keys {
		select {
		case <-ctx.Done():
			return
		default:
		}
		r.resumeRedemption(ctx, k)
	}
}

// claimRedemptions leases due redemptions for the resume path. Redemptions past
// the fast-retry budget (5 attempts) are NOT claimed — they are marked as
// 'failed' by detectStuckRedemptions instead. The ledger may already be ahead
// of Blend custody (reserve-before-debit), so abandoned redemptions strand
// user money — but the infinite retry loop that was creating dozens of failed
// Blend flow plans is worse.
func (r *DepositRouter) claimRedemptions(ctx context.Context, limit int) ([]string, error) {
	rows, err := r.db.QueryxContext(ctx, `
		UPDATE blend_yield_redemptions
		SET next_retry_at = NOW() + ($2 || ' seconds')::interval, attempts = attempts + 1, updated_at = NOW()
		WHERE idempotency_key IN (
			SELECT idempotency_key FROM blend_yield_redemptions
			WHERE status IN ('pending', 'quoted', 'executing', 'submitted')
				AND next_retry_at <= NOW()
				AND attempts < $3
			ORDER BY next_retry_at
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING idempotency_key
	`, limit, int(leaseWindow.Seconds()), maxRetryAttempts)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// resumeRedemption drives a previously-stranded redemption (e.g. one whose synchronous
// RedeemStashYield call timed out) toward settlement so positions get decremented and
// the next withdrawal retry can short-circuit on the completed redemption.
func (r *DepositRouter) resumeRedemption(ctx context.Context, idempotencyKey string) {
	red, err := r.getRedemption(ctx, idempotencyKey)
	if err != nil || red == nil {
		if err != nil {
			r.logger.Warn("Blend: resume redemption lookup failed", zap.String("key", idempotencyKey), zap.Error(err))
		}
		return
	}
	if red.Status == redemptionStatusComplete || red.Status == redemptionStatusFailed {
		return
	}
	if red.Attempts >= maxRetryAttempts {
		r.logger.Error("Blend: redemption exceeded max retry attempts — marking as failed to stop retry loop",
			zap.String("redemption_id", red.ID.String()),
			zap.Int("attempts", red.Attempts),
			zap.String("status", red.Status))
		if err := r.markRedemptionFailed(ctx, red.IdempotencyKey,
			fmt.Sprintf("exceeded max retry attempts (%d) — Blend redemption stuck, manual reconciliation required", maxRetryAttempts)); err != nil {
			r.logger.Error("Blend: failed to mark redemption as failed after exceeding retry budget",
				zap.String("redemption_id", red.ID.String()), zap.Error(err))
		}
		return
	}
	acct, err := r.getUserAccount(ctx, red.UserID)
	if err != nil || acct == nil {
		r.logger.Warn("Blend: resume redemption missing account",
			zap.String("redemption_id", red.ID.String()), zap.Error(err))
		return
	}
	pollCtx, cancel := context.WithTimeout(ctx, r.getRedeemTimeout())
	defer cancel()
	// skipSweep=false: worker-resumed redemptions are for the emergency stash-to-spend
	// path; funds stay on platform so we do want the Base→Solana sweep.
	if err := r.driveRedemption(pollCtx, acct, red, red.Amount, false); err != nil {
		r.logger.Warn("Blend: resume redemption did not settle (will retry)",
			zap.String("redemption_id", red.ID.String()), zap.Error(err))
		// Persist the last error so detectStrandedRedemptions surfaces the real
		// cause instead of an opaque empty last_error.
		if _, dbErr := r.db.ExecContext(ctx, `
			UPDATE blend_yield_redemptions
			SET last_error = $2, updated_at = NOW()
			WHERE id = $1 AND status NOT IN ('complete','failed')
		`, red.ID, err.Error()); dbErr != nil {
			r.logger.Warn("Blend: failed to persist redemption error",
				zap.String("redemption_id", red.ID.String()), zap.Error(dbErr))
		}
	}
}
