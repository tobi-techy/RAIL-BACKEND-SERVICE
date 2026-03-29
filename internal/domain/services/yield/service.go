package yield

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

var minCreditAmount = decimal.NewFromFloat(0.01)

// Repository handles yield persistence.
type Repository interface {
	RecordSnapshot(ctx context.Context, userID uuid.UUID, balance decimal.Decimal) error
	GetLastSnapshotBefore(ctx context.Context, userID uuid.UUID, before time.Time) (*entities.YieldBalanceSnapshot, error)
	GetSnapshotsInWindow(ctx context.Context, userID uuid.UUID, from, to time.Time) ([]*entities.YieldBalanceSnapshot, error)
	GetAllUsersWithSnapshotsInWindow(ctx context.Context, from, to time.Time) ([]uuid.UUID, error)
	CreateDistribution(ctx context.Context, d *entities.YieldDistribution) error
	UpdateDistribution(ctx context.Context, d *entities.YieldDistribution) error
	UpsertDistributionUser(ctx context.Context, u *entities.YieldDistributionUser) error
	GetDistributionByPeriod(ctx context.Context, start, end time.Time) (*entities.YieldDistribution, error)
}

// RewardsProvider fetches the accrued reward amount from the yield provider.
type RewardsProvider interface {
	GetRewardsSummary(ctx context.Context, currency string) (*RewardSummary, error)
}

// RewardSummary holds the distributable reward amount from the yield provider.
type RewardSummary struct {
	Rewards string
}

// LedgerCreditor credits a user's stash balance in the ledger.
type LedgerCreditor interface {
	CreditStash(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, description string) error
}

// YieldNotifier sends push notifications after yield is credited.
type YieldNotifier interface {
	NotifyYieldCredited(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error
}

// Service handles yield distribution.
type Service struct {
	repo     Repository
	rewards  RewardsProvider
	ledger   LedgerCreditor
	notifier YieldNotifier
	logger   *zap.Logger
}

func NewService(repo Repository, rewards RewardsProvider, ledger LedgerCreditor, logger *zap.Logger) *Service {
	return &Service{repo: repo, rewards: rewards, ledger: ledger, logger: logger}
}

// SetNotifier wires push notifications for yield credited events.
func (s *Service) SetNotifier(n YieldNotifier) {
	s.notifier = n
}

// RecordSnapshot writes a stash balance snapshot. Call this whenever stash balance changes.
func (s *Service) RecordSnapshot(ctx context.Context, userID uuid.UUID, balance decimal.Decimal) error {
	return s.repo.RecordSnapshot(ctx, userID, balance)
}

// EstimateDailyYield returns an estimated daily yield for a user based on their current stash balance.
// APY is expressed as a decimal (e.g. 0.045 for 4.5%).
func (s *Service) EstimateDailyYield(stashBalance, apy decimal.Decimal) decimal.Decimal {
	if stashBalance.IsZero() || apy.IsZero() {
		return decimal.Zero
	}
	return stashBalance.Mul(apy).Div(decimal.NewFromInt(365))
}

// RunDistribution executes the monthly yield distribution for a given period.
// totalReward must be the actual amount already received from the yield provider — never estimated.
// freezeTime should be time.Now() at job start; snapshots after this are ignored.
func (s *Service) RunDistribution(ctx context.Context, periodStart, periodEnd, freezeTime time.Time, totalReward decimal.Decimal) error {
	if totalReward.LessThanOrEqual(decimal.Zero) {
		s.logger.Info("Skipping yield distribution: zero or negative reward", zap.String("reward", totalReward.String()))
		return nil
	}

	// Idempotency: skip if already completed for this period.
	existing, err := s.repo.GetDistributionByPeriod(ctx, periodStart, periodEnd)
	if err != nil {
		return fmt.Errorf("yield: check existing distribution: %w", err)
	}
	if existing != nil && existing.Status == "completed" {
		s.logger.Info("Yield distribution already completed for period", zap.Time("start", periodStart))
		return nil
	}

	// Create or reuse distribution record.
	dist := &entities.YieldDistribution{
		ID:          uuid.New(),
		PeriodStart: periodStart,
		PeriodEnd:   periodEnd,
		TotalReward: totalReward,
		Status:      "pending",
		CreatedAt:   time.Now(),
	}
	if existing != nil {
		dist = existing // resume
	} else {
		if err := s.repo.CreateDistribution(ctx, dist); err != nil {
			return fmt.Errorf("yield: create distribution: %w", err)
		}
	}

	// Clamp upper bound: never include snapshots beyond periodEnd.
	upper := freezeTime
	if periodEnd.Before(freezeTime) {
		upper = periodEnd
	}

	// Get all users with any stash activity within the window.
	userIDs, err := s.repo.GetAllUsersWithSnapshotsInWindow(ctx, periodStart, upper)
	if err != nil {
		return fmt.Errorf("yield: list users: %w", err)
	}

	// Compute TWB for each user using the clamped upper bound.
	type userTWB struct {
		userID uuid.UUID
		twb    decimal.Decimal
	}
	twbs := make([]userTWB, 0, len(userIDs))
	totalTWB := decimal.Zero

	for _, uid := range userIDs {
		twb, err := s.computeTWB(ctx, uid, periodStart, upper)
		if err != nil {
			s.logger.Error("Failed to compute TWB for user", zap.String("user_id", uid.String()), zap.Error(err))
			continue
		}
		if twb.IsZero() {
			continue
		}
		twbs = append(twbs, userTWB{uid, twb})
		totalTWB = totalTWB.Add(twb)
	}

	if totalTWB.IsZero() {
		dist.Remainder = totalReward
		dist.Status = "completed"
		if err := s.repo.UpdateDistribution(ctx, dist); err != nil {
			return fmt.Errorf("yield: update distribution (no eligible users): %w", err)
		}
		return nil
	}

	// Distribute proportionally: credit ledger first, then record.
	totalDistributed := decimal.Zero
	desc := fmt.Sprintf("Yield distribution %s", dist.ID)
	for _, ut := range twbs {
		sharePct := ut.twb.Div(totalTWB)
		reward := totalReward.Mul(sharePct).Truncate(6)

		if reward.LessThan(minCreditAmount) {
			continue
		}

		// Credit ledger first — source of truth.
		if err := s.ledger.CreditStash(ctx, ut.userID, reward, desc); err != nil {
			s.logger.Error("Failed to credit stash for yield", zap.String("user_id", ut.userID.String()), zap.Error(err))
			continue
		}

		// Only record distribution row after successful credit.
		now := time.Now()
		row := &entities.YieldDistributionUser{
			ID:             uuid.NewSHA1(uuid.NameSpaceOID, []byte(dist.ID.String()+":"+ut.userID.String())),
			DistributionID: dist.ID,
			UserID:         ut.userID,
			TWB:            ut.twb,
			SharePct:       sharePct,
			RewardAmount:   reward,
			CreditedAt:     &now,
		}
		if err := s.repo.UpsertDistributionUser(ctx, row); err != nil {
			s.logger.Error("Failed to upsert distribution user", zap.String("user_id", ut.userID.String()), zap.Error(err))
			// Credit already happened — log but don't reverse; retry will be idempotent via ledger key.
		} else if s.notifier != nil {
			// Notify only after DB record is persisted.
			if err := s.notifier.NotifyYieldCredited(ctx, ut.userID, reward); err != nil {
				s.logger.Warn("Failed to send yield credited notification", zap.String("user_id", ut.userID.String()), zap.Error(err))
			}
		}

		totalDistributed = totalDistributed.Add(reward)
	}

	dist.TotalTWB = totalTWB
	dist.TotalDistributed = totalDistributed
	dist.Remainder = totalReward.Sub(totalDistributed)
	dist.Status = "completed"

	if err := s.repo.UpdateDistribution(ctx, dist); err != nil {
		return fmt.Errorf("yield: update distribution: %w", err)
	}

	s.logger.Info("Yield distribution completed",
		zap.String("period_start", periodStart.Format(time.DateOnly)),
		zap.String("total_reward", totalReward.String()),
		zap.String("total_distributed", totalDistributed.String()),
		zap.String("remainder", dist.Remainder.String()),
		zap.Int("users", len(twbs)),
	)
	return nil
}

// computeTWB calculates the time-weighted balance for a user within [from, freezeTime].
func (s *Service) computeTWB(ctx context.Context, userID uuid.UUID, from, to time.Time) (decimal.Decimal, error) {
	// Opening balance: last snapshot before period start.
	opening, err := s.repo.GetLastSnapshotBefore(ctx, userID, from)
	if err != nil {
		return decimal.Zero, err
	}

	// Snapshots within the window.
	inWindow, err := s.repo.GetSnapshotsInWindow(ctx, userID, from, to)
	if err != nil {
		return decimal.Zero, err
	}

	// Build timeline: [(time, balance), ...]
	type point struct {
		t time.Time
		b decimal.Decimal
	}
	var timeline []point
	if opening != nil && opening.Balance.IsPositive() {
		timeline = append(timeline, point{from, opening.Balance})
	}
	for _, snap := range inWindow {
		timeline = append(timeline, point{snap.RecordedAt, snap.Balance})
	}

	if len(timeline) == 0 {
		return decimal.Zero, nil
	}

	twb := decimal.Zero
	for i, p := range timeline {
		end := to
		if i+1 < len(timeline) {
			end = timeline[i+1].t
		}
		seconds := decimal.NewFromFloat(end.Sub(p.t).Seconds())
		twb = twb.Add(p.b.Mul(seconds))
	}
	return twb, nil
}
