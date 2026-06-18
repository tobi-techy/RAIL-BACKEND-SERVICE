package blend

import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Worker tuning.
const (
	defaultWorkerBatchSize = 25
	// leaseWindow is how far we push next_retry_at when claiming a route/redemption,
	// preventing a second worker (or a second instance) from grabbing the same row
	// while the first is still processing it.
	leaseWindow = 2 * time.Minute
)

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
			r.logger.Error("Blend reconcile panic recovered", zap.Any("panic", rec))
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()

	r.reconcileDepositRoutes(ctx)
	r.reconcileRedemptions(ctx)
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
		if err := r.ProcessRouteByID(ctx, id); err != nil {
			r.logger.Warn("Blend: deposit route processing failed (will retry)",
				zap.String("route_id", id.String()), zap.Error(err))
		}
	}
}

// claimDepositRoutes atomically leases a batch of due, non-terminal routes by pushing
// their next_retry_at into the future, returning the claimed IDs. FOR UPDATE SKIP LOCKED
// guarantees concurrent workers never claim the same row.
func (r *DepositRouter) claimDepositRoutes(ctx context.Context, limit int) ([]uuid.UUID, error) {
	rows, err := r.db.QueryxContext(ctx, `
		UPDATE blend_deposit_routes
		SET next_retry_at = NOW() + ($2 || ' seconds')::interval, updated_at = NOW()
		WHERE id IN (
			SELECT id FROM blend_deposit_routes
			WHERE status NOT IN ('complete', 'error_terminal', 'error_payload')
				AND next_retry_at <= NOW()
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

func (r *DepositRouter) claimRedemptions(ctx context.Context, limit int) ([]string, error) {
	rows, err := r.db.QueryxContext(ctx, `
		UPDATE blend_yield_redemptions
		SET next_retry_at = NOW() + ($2 || ' seconds')::interval, updated_at = NOW()
		WHERE idempotency_key IN (
			SELECT idempotency_key FROM blend_yield_redemptions
			WHERE status IN ('pending', 'quoted', 'executing', 'submitted')
				AND next_retry_at <= NOW()
			ORDER BY next_retry_at
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING idempotency_key
	`, limit, int(leaseWindow.Seconds()))
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
	acct, err := r.getUserAccount(ctx, red.UserID)
	if err != nil || acct == nil {
		r.logger.Warn("Blend: resume redemption missing account",
			zap.String("redemption_id", red.ID.String()), zap.Error(err))
		return
	}
	pollCtx, cancel := context.WithTimeout(ctx, r.getRedeemTimeout())
	defer cancel()
	if err := r.driveRedemption(pollCtx, acct, red, red.Amount); err != nil {
		r.logger.Warn("Blend: resume redemption did not settle (will retry)",
			zap.String("redemption_id", red.ID.String()), zap.Error(err))
	}
}
