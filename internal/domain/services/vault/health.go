package vault

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

// Health flag names. They are ops vocabulary, never user copy. A failed plan
// open is observable through vault_enroll_failures instead of a flag, because
// it never owns a health row.
const (
	HealthFlagLotWithoutFund     = "lot_without_fund"
	HealthFlagDriftOverThreshold = "drift_over_threshold"
	HealthFlagFailedWithdraw     = "failed_withdraw_after_key_mint"
	HealthFlagRepeatedFloorSkips = "repeated_floor_skips"
	HealthFlagUnlockSoon         = "unlock_under_30_days"
)

// SortActivityNewestFirst orders the activity feed newest first.
func SortActivityNewestFirst(entries []entities.VaultActivityEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].At.After(entries[j].At)
	})
}

// RecordFailedEnroll persists a failed-enroll flag so ops can see a vault that
// never got its portfolio. The vault row itself never exists in this path, so
// the failure lives in its own table rather than vault_health (which is keyed,
// and foreign-keyed, to a vault row).
func (s *Service) RecordFailedEnroll(ctx context.Context, userID uuid.UUID, cause error) {
	failure := &entities.VaultEnrollFailure{
		UserID: userID,
		Tries:  1,
	}
	if cause != nil {
		failure.LastError = cause.Error()
	}
	failure.LastErrorAt = s.now()
	if existing, err := s.repo.GetEnrollFailure(ctx, userID); err == nil && existing != nil {
		failure.Tries = existing.Tries + 1
		if existing.LastErrorAt.After(failure.LastErrorAt) {
			failure.LastErrorAt = existing.LastErrorAt
		}
	}
	if err := s.repo.UpsertEnrollFailure(ctx, failure); err != nil {
		s.log.Warn("vault enroll failure not recorded", "user_id", userID.String(), "error", err)
	}
}

// RecordFailedWithdraw flags a withdrawal that died after the key was minted.
func (s *Service) RecordFailedWithdraw(ctx context.Context, vault *entities.RetirementVault, cause error) {
	if vault == nil {
		return
	}
	health, err := s.loadHealth(ctx, vault)
	if err != nil {
		return
	}
	addFlag(health, HealthFlagFailedWithdraw)
	if cause != nil {
		health.LastError = cause.Error()
		at := s.now()
		health.LastErrorAt = &at
	}
	health.CheckedAt = s.now()
	if err := s.repo.UpsertHealth(ctx, health); err != nil {
		s.log.Warn("vault health not recorded after failed withdraw", "vault_id", vault.ID.String(), "error", err)
	}
}

// refreshHealth recomputes a vault's health row after a state change. lastEvent
// is an internal note, never user copy. Best-effort: it must never break the
// deposit or withdraw path.
func (s *Service) refreshHealth(ctx context.Context, vault *entities.RetirementVault, lastEvent string) error {
	if vault == nil {
		return nil
	}
	health, err := s.loadHealth(ctx, vault)
	if err != nil {
		return err
	}
	now := s.now()
	_ = lastEvent

	hasMark, err := s.refreshLedgerState(ctx, vault, health)
	if err != nil {
		return err
	}
	s.refreshFundingFlag(health, hasMark)
	s.refreshDriftFlag(health)
	s.refreshSkipsFlag(ctx, vault, health, now)
	s.refreshUnlockFlag(vault, health, now)
	s.refreshPendingOps(ctx, vault, health, now)
	s.refreshLastSkip(ctx, vault, health)

	health.CheckedAt = now
	if err := s.repo.UpsertHealth(ctx, health); err != nil {
		return err
	}

	s.maybeNotifyUnlockSoon(ctx, vault, health)
	return nil
}

// refreshLedgerState reloads cost basis and the latest provider mark. It
// reports whether any mark exists right now: basis without a mark means
// nothing proves the money reached the portfolio.
func (s *Service) refreshLedgerState(ctx context.Context, vault *entities.RetirementVault, health *entities.VaultHealth) (bool, error) {
	lots, err := s.repo.ListOpenLots(ctx, vault.ID)
	if err != nil {
		return false, err
	}
	health.LedgerUSD = SumPrincipal(lots)

	hasMark := false
	snapshot, err := s.repo.LatestSnapshot(ctx, vault.ID)
	if err != nil {
		return false, err
	}
	if snapshot != nil {
		hasMark = true
		health.LastSnapshotAt = &snapshot.AsOf
		health.ProviderUSD = snapshot.MarketValueUSD
	}
	if enrollmentValue, haveProvider := s.providerValue(vault); haveProvider {
		hasMark = true
		health.ProviderUSD = enrollmentValue
	}
	return hasMark, nil
}

// refreshFundingFlag tracks lots without any provider mark: basis exists but
// no snapshot ever arrived, so nothing proves the money reached the portfolio.
func (s *Service) refreshFundingFlag(health *entities.VaultHealth, hasMark bool) {
	if health.LedgerUSD.GreaterThan(decimal.Zero) && !hasMark {
		addFlag(health, HealthFlagLotWithoutFund)
	} else {
		removeFlag(health, HealthFlagLotWithoutFund)
	}
}

// refreshDriftFlag tracks provider/ledger disagreement beyond the configured
// percent of ledger. A zero threshold disables the flag.
func (s *Service) refreshDriftFlag(health *entities.VaultHealth) {
	if !s.cfg.HealthDriftThresholdPct.GreaterThan(decimal.Zero) ||
		!health.LedgerUSD.GreaterThan(decimal.Zero) {
		return
	}
	diff := health.ProviderUSD.Sub(health.LedgerUSD).Abs()
	driftPct := diff.Div(health.LedgerUSD).Mul(decimal.NewFromInt(100))
	if driftPct.GreaterThan(s.cfg.HealthDriftThresholdPct) {
		addFlag(health, HealthFlagDriftOverThreshold)
	} else {
		removeFlag(health, HealthFlagDriftOverThreshold)
	}
}

// refreshSkipsFlag tracks 3+ floor skips in the last 30 days. A failed count
// read leaves the previous flag alone rather than clearing signal on noise.
func (s *Service) refreshSkipsFlag(ctx context.Context, vault *entities.RetirementVault, health *entities.VaultHealth, now time.Time) {
	n, err := s.repo.CountRecentSkips(ctx, vault.ID, now.AddDate(0, 0, -30))
	if err != nil {
		return
	}
	if n >= 3 {
		addFlag(health, HealthFlagRepeatedFloorSkips)
	} else {
		removeFlag(health, HealthFlagRepeatedFloorSkips)
	}
}

// refreshUnlockFlag tracks the only user-actionable condition: growth unlocks
// within 30 days.
func (s *Service) refreshUnlockFlag(vault *entities.RetirementVault, health *entities.VaultHealth, now time.Time) {
	health.UserActionNeeded = false
	removeFlag(health, HealthFlagUnlockSoon)
	if vault.UnlockDate == nil {
		return
	}
	if until := vault.UnlockDate.Sub(now); until >= 0 && until < 30*24*time.Hour {
		addFlag(health, HealthFlagUnlockSoon)
		health.UserActionNeeded = true
	}
}

// refreshPendingOps counts live authorizations plus pending penalties.
func (s *Service) refreshPendingOps(ctx context.Context, vault *entities.RetirementVault, health *entities.VaultHealth, now time.Time) {
	pending := 0
	if vault.GliderEnrollmentID != nil {
		if live, err := s.repo.HasIssuedAuthorization(ctx, *vault.GliderEnrollmentID, now); err == nil && live {
			pending++
		}
	}
	if penalties, err := s.repo.ListPenaltyEvents(ctx, vault.ID, 50); err == nil {
		for _, penalty := range penalties {
			if penalty != nil && penalty.Status == entities.VaultPenaltyPending {
				pending++
			}
		}
	}
	health.PendingOps = pending
}

// refreshLastSkip records the most recent floor skip, if any.
func (s *Service) refreshLastSkip(ctx context.Context, vault *entities.RetirementVault, health *entities.VaultHealth) {
	if skips, err := s.repo.ListSkips(ctx, vault.ID, 1); err == nil && len(skips) > 0 {
		health.LastSkip = &skips[0].CreatedAt
	}
}

// maybeNotifyUnlockSoon pages the user about exactly one thing: their growth
// is about to unlock. Everything else stays ops-only.
func (s *Service) maybeNotifyUnlockSoon(ctx context.Context, vault *entities.RetirementVault, health *entities.VaultHealth) {
	if !health.UserActionNeeded || s.notifier == nil || vault.UnlockDate == nil {
		return
	}
	if err := s.notifier.NotifyVaultActionRequired(ctx, vault.UserID,
		"Your retirement growth unlocks soon",
		"Your Retirement Plan growth unlocks on "+vault.UnlockDate.Format("2 Jan 2006")+"."); err != nil {
		s.log.Warn("vault unlock notification failed", "vault_id", vault.ID.String(), "error", err)
	}
}

func (s *Service) loadHealth(ctx context.Context, vault *entities.RetirementVault) (*entities.VaultHealth, error) {
	health, err := s.repo.GetHealth(ctx, vault.ID)
	if err != nil {
		return nil, err
	}
	if health == nil {
		health = &entities.VaultHealth{VaultID: vault.ID, UserID: vault.UserID, Flags: []string{}}
	}
	if health.Flags == nil {
		health.Flags = []string{}
	}
	var fundedAt *time.Time
	if lots, err := s.repo.ListLots(ctx, vault.ID, 1); err == nil && len(lots) > 0 {
		fundedAt = &lots[0].AcquiredAt
	}
	if fundedAt == nil {
		fundedAt = vault.FundedAt
	}
	health.LastFundedAt = fundedAt
	return health, nil
}

// providerValue reports the live enrollment value when the engine can reach
// the provider. False means unreachable: callers fall back to ledger state.
func (s *Service) providerValue(vault *entities.RetirementVault) (decimal.Decimal, bool) {
	return decimal.Zero, false
}

func addFlag(health *entities.VaultHealth, flag string) {
	for _, existing := range health.Flags {
		if existing == flag {
			return
		}
	}
	health.Flags = append(health.Flags, flag)
}

func removeFlag(health *entities.VaultHealth, flag string) {
	kept := health.Flags[:0]
	for _, existing := range health.Flags {
		if !strings.EqualFold(existing, flag) {
			kept = append(kept, existing)
		}
	}
	health.Flags = kept
}
