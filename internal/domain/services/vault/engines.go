// Package vault implements the Premium Global Dollar Retirement Vault: the
// long-horizon, locked-USD sleeve of Rail's automated money system.
//
// Design rules encoded here (not in a prompt, not in copy):
//   - Withdrawals consume principal first. A user can always reach their own
//     money cheaply; only growth is expensive to raid.
//   - Earnings taken before unlock are haircut by the penalty rate (default
//     10%). After unlock the penalty is zero.
//   - The unlock date is the LATER of "reach your retirement age" and "hold for
//     the minimum lock years". If the age leg cannot be resolved (no date of
//     birth) the vault fails closed and refuses to unlock.
//   - Every uncertain input blocks the withdrawal. Nothing here guesses.
package vault

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

// Engine errors. Each one maps to a human, non-technical message at the edge.
var (
	// ErrUnlockDateUnavailable means we cannot prove when earnings unlock, so
	// we refuse to treat them as unlocked. Fail closed.
	ErrUnlockDateUnavailable = errors.New("vault: unlock date cannot be determined")
	// ErrInvalidAmount means the requested amount is not a positive quantity.
	ErrInvalidAmount = errors.New("vault: amount must be greater than zero")
	// ErrInsufficientValue means the request exceeds what the vault is worth.
	ErrInsufficientValue = errors.New("vault: amount exceeds the vault value")
	// ErrLotBasisMismatch means the lots cannot account for the principal being
	// returned. The books do not balance, so the withdraw is refused.
	ErrLotBasisMismatch = errors.New("vault: contribution basis does not reconcile")
	// ErrInvalidPolicy means the configured lock policy is nonsense.
	ErrInvalidPolicy = errors.New("vault: invalid lock policy")
)

// ComputeUnlockDate returns the date on which earnings become withdrawable
// without penalty: the later of the retirement-age leg and the minimum-lock leg.
//
// It is deliberately strict. A missing date of birth, or an implausible age,
// returns ErrUnlockDateUnavailable rather than silently locking the user out
// forever or letting them out early.
func ComputeUnlockDate(dob *time.Time, retirementAge int, fundedAt time.Time, minLockYears int) (time.Time, error) {
	if dob == nil {
		return time.Time{}, ErrUnlockDateUnavailable
	}
	if retirementAge < 18 || retirementAge > 120 {
		return time.Time{}, fmt.Errorf("%w: retirement age %d is out of range", ErrInvalidPolicy, retirementAge)
	}
	if minLockYears < 0 || minLockYears > 80 {
		return time.Time{}, fmt.Errorf("%w: minimum lock years %d is out of range", ErrInvalidPolicy, minLockYears)
	}
	if fundedAt.IsZero() {
		return time.Time{}, fmt.Errorf("%w: funding date is required", ErrInvalidPolicy)
	}

	ageLeg := dob.UTC().AddDate(retirementAge, 0, 0)
	lockLeg := fundedAt.UTC().AddDate(minLockYears, 0, 0)
	if ageLeg.After(lockLeg) {
		return ageLeg, nil
	}
	return lockLeg, nil
}

// ComputeWithdrawal splits a gross withdrawal into principal and earnings and
// applies the early-withdrawal penalty.
//
// Principal-first is the product rule: you always get your own money back
// before the growth is touched, so a pre-unlock withdrawal of exactly what you
// put in costs nothing.
func ComputeWithdrawal(
	position entities.VaultPosition,
	gross decimal.Decimal,
	now time.Time,
	penaltyRate decimal.Decimal,
) (*entities.VaultWithdrawalPlan, error) {
	if !gross.GreaterThan(decimal.Zero) {
		return nil, ErrInvalidAmount
	}
	if position.UnlockDate.IsZero() {
		return nil, ErrUnlockDateUnavailable
	}
	if gross.GreaterThan(position.MarketValueUSD) {
		return nil, fmt.Errorf("%w: requested %s against a vault worth %s",
			ErrInsufficientValue, gross.StringFixed(2), position.MarketValueUSD.StringFixed(2))
	}
	if penaltyRate.IsNegative() {
		return nil, fmt.Errorf("%w: penalty rate cannot be negative", ErrInvalidPolicy)
	}

	principalOut := gross
	if principalOut.GreaterThan(position.PrincipalUSD) {
		principalOut = position.PrincipalUSD
	}
	if principalOut.IsNegative() {
		principalOut = decimal.Zero
	}
	earningsOut := gross.Sub(principalOut)

	locked := now.Before(position.UnlockDate)
	penalty := decimal.Zero
	if locked && earningsOut.GreaterThan(decimal.Zero) {
		penalty = earningsOut.Mul(penaltyRate).Round(2)
		if penalty.GreaterThan(earningsOut) {
			// A penalty can never eat more than the growth it applies to.
			penalty = earningsOut
		}
	}

	return &entities.VaultWithdrawalPlan{
		GrossUSD:             gross,
		PrincipalReturnedUSD: principalOut,
		EarningsReturnedUSD:  earningsOut,
		PenaltyRate:          penaltyRate,
		PenaltyUSD:           penalty,
		NetToUserUSD:         gross.Sub(penalty),
		Locked:               locked,
		UnlockDate:           position.UnlockDate,
	}, nil
}

// PenaltyIfWithdrawnNow is the fee a user would pay to take all of today's
// growth. It drives the "if you withdraw today" line in the app.
func PenaltyIfWithdrawnNow(position entities.VaultPosition, now time.Time, penaltyRate decimal.Decimal) decimal.Decimal {
	if position.UnlockDate.IsZero() || !now.Before(position.UnlockDate) {
		return decimal.Zero
	}
	earnings := position.Earnings()
	if !earnings.GreaterThan(decimal.Zero) {
		return decimal.Zero
	}
	return earnings.Mul(penaltyRate).Round(2)
}

// SumPrincipal returns the remaining cost basis across open lots.
func SumPrincipal(lots []*entities.VaultContributionLot) decimal.Decimal {
	total := decimal.Zero
	for _, lot := range lots {
		if lot == nil || lot.Status != entities.VaultLotOpen {
			continue
		}
		total = total.Add(lot.RemainingUSD)
	}
	return total
}

// SortLotsFIFO orders lots oldest-first. Basis is consumed first-in-first-out.
func SortLotsFIFO(lots []*entities.VaultContributionLot) {
	sort.SliceStable(lots, func(i, j int) bool {
		return lots[i].AcquiredAt.Before(lots[j].AcquiredAt)
	})
}

// ConsumeLots reduces lot basis by principalReturned, oldest lots first.
//
// The mutation is applied to the lots in place. If the lots cannot cover the
// principal being returned, it returns ErrLotBasisMismatch and the caller must
// abandon the withdrawal: the books disagree, so no money moves.
func ConsumeLots(lots []*entities.VaultContributionLot, principalReturned decimal.Decimal) error {
	if !principalReturned.GreaterThan(decimal.Zero) {
		return nil
	}
	remaining := principalReturned
	for _, lot := range lots {
		if lot == nil || lot.Status != entities.VaultLotOpen {
			continue
		}
		if !lot.RemainingUSD.GreaterThan(decimal.Zero) {
			lot.Status = entities.VaultLotConsumed
			continue
		}
		take := lot.RemainingUSD
		if take.GreaterThan(remaining) {
			take = remaining
		}
		lot.RemainingUSD = lot.RemainingUSD.Sub(take)
		if lot.RemainingUSD.IsZero() {
			lot.Status = entities.VaultLotConsumed
		}
		remaining = remaining.Sub(take)
		if !remaining.GreaterThan(decimal.Zero) {
			return nil
		}
	}
	if remaining.GreaterThan(decimal.Zero) {
		return fmt.Errorf("%w: %s of principal had no matching contribution",
			ErrLotBasisMismatch, remaining.StringFixed(2))
	}
	return nil
}
