package vault

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

func dec(s string) decimal.Decimal {
	value, err := decimal.NewFromString(s)
	if err != nil {
		panic(err)
	}
	return value
}

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse time %q: %v", value, err)
	}
	return parsed
}

// ---------------------------------------------------------------------------
// Unlock date: the LATER of retirement-age and minimum-lock legs.
// ---------------------------------------------------------------------------

func TestComputeUnlockDate(t *testing.T) {
	dob := func(value string) *time.Time {
		parsed, _ := time.Parse("2006-01-02", value)
		return &parsed
	}
	funded := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name          string
		dob           *time.Time
		retirementAge int
		minLockYears  int
		want          string // RFC3339, empty means expect error
		wantErr       error
	}{
		{
			name:          "lock leg wins when the user is young",
			dob:           dob("1995-06-15"), // turns 50 in 2045, funds 2026 -> 5y = 2031
			retirementAge: 50,
			minLockYears:  5,
			want:          "2045-06-15T00:00:00Z",
		},
		{
			name:          "age leg wins when the user is already old enough",
			dob:           dob("1970-03-02"), // turns 50 in 2020 (past), funds 2026 -> 5y = 2031
			retirementAge: 50,
			minLockYears:  5,
			want:          "2031-01-01T00:00:00Z",
		},
		{
			name:          "exact tie keeps a valid date",
			dob:           dob("1981-01-01"), // turns 50 in 2031-01-01, lock 5y = 2031-01-01
			retirementAge: 50,
			minLockYears:  5,
			want:          "2031-01-01T00:00:00Z",
		},
		{
			name:          "no date of birth fails closed",
			dob:           nil,
			retirementAge: 50,
			minLockYears:  5,
			wantErr:       ErrUnlockDateUnavailable,
		},
		{
			name:          "implausible retirement age rejected",
			dob:           dob("1995-06-15"),
			retirementAge: 5,
			minLockYears:  5,
			wantErr:       ErrInvalidPolicy,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ComputeUnlockDate(tc.dob, tc.retirementAge, funded, tc.minLockYears)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("want error %v, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if want := mustTime(t, tc.want); !got.Equal(want) {
				t.Fatalf("want %s, got %s", want, got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Withdrawal: principal-first, 10% of the earnings portion taken while locked.
// ---------------------------------------------------------------------------

func TestComputeWithdrawal(t *testing.T) {
	unlock := time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC)
	before := unlock.Add(-time.Nanosecond)
	after := unlock

	position := entities.VaultPosition{
		PrincipalUSD:   dec("1000"),
		MarketValueUSD: dec("1400"),
		UnlockDate:     unlock,
	}

	tests := []struct {
		name          string
		gross         string
		now           time.Time
		wantPrincipal string
		wantEarnings  string
		wantPenalty   string
		wantNet       string
		wantLocked    bool
	}{
		{
			name:          "principal only before unlock is free",
			gross:         "1000",
			now:           before,
			wantPrincipal: "1000",
			wantEarnings:  "0",
			wantPenalty:   "0",
			wantNet:       "1000",
			wantLocked:    true,
		},
		{
			name:          "touching earnings before unlock incurs 10% of the earnings taken",
			gross:         "1200",
			now:           before,
			wantPrincipal: "1000",
			wantEarnings:  "200",
			wantPenalty:   "20",
			wantNet:       "1180",
			wantLocked:    true,
		},
		{
			name:          "full withdrawal before unlock",
			gross:         "1400",
			now:           before,
			wantPrincipal: "1000",
			wantEarnings:  "400",
			wantPenalty:   "40",
			wantNet:       "1360",
			wantLocked:    true,
		},
		{
			name:          "identical withdrawal exactly at unlock has no penalty",
			gross:         "1200",
			now:           after,
			wantPrincipal: "1000",
			wantEarnings:  "200",
			wantPenalty:   "0",
			wantNet:       "1200",
			wantLocked:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := ComputeWithdrawal(position, dec(tc.gross), tc.now, dec("0.10"))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !plan.PrincipalReturnedUSD.Equal(dec(tc.wantPrincipal)) {
				t.Errorf("principal: want %s got %s", tc.wantPrincipal, plan.PrincipalReturnedUSD)
			}
			if !plan.EarningsReturnedUSD.Equal(dec(tc.wantEarnings)) {
				t.Errorf("earnings: want %s got %s", tc.wantEarnings, plan.EarningsReturnedUSD)
			}
			if !plan.PenaltyUSD.Equal(dec(tc.wantPenalty)) {
				t.Errorf("penalty: want %s got %s", tc.wantPenalty, plan.PenaltyUSD)
			}
			if !plan.NetToUserUSD.Equal(dec(tc.wantNet)) {
				t.Errorf("net: want %s got %s", tc.wantNet, plan.NetToUserUSD)
			}
			if plan.Locked != tc.wantLocked {
				t.Errorf("locked: want %v got %v", tc.wantLocked, plan.Locked)
			}
			// The plan's own arithmetic must balance.
			if !plan.NetToUserUSD.Equal(plan.GrossUSD.Sub(plan.PenaltyUSD)) {
				t.Errorf("net != gross - penalty")
			}
		})
	}
}

func TestComputeWithdrawalRejectsUnsafeInputs(t *testing.T) {
	unlock := time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC)

	t.Run("zero amount", func(t *testing.T) {
		_, err := ComputeWithdrawal(entities.VaultPosition{PrincipalUSD: dec("10"), MarketValueUSD: dec("20"), UnlockDate: unlock}, decimal.Zero, unlock, dec("0.1"))
		if !errors.Is(err, ErrInvalidAmount) {
			t.Fatalf("want ErrInvalidAmount got %v", err)
		}
	})

	t.Run("more than the vault is worth", func(t *testing.T) {
		_, err := ComputeWithdrawal(entities.VaultPosition{PrincipalUSD: dec("10"), MarketValueUSD: dec("20"), UnlockDate: unlock}, dec("25"), unlock, dec("0.1"))
		if !errors.Is(err, ErrInsufficientValue) {
			t.Fatalf("want ErrInsufficientValue got %v", err)
		}
	})

	t.Run("unknown unlock date fails closed", func(t *testing.T) {
		_, err := ComputeWithdrawal(entities.VaultPosition{PrincipalUSD: dec("10"), MarketValueUSD: dec("20")}, dec("5"), unlock, dec("0.1"))
		if !errors.Is(err, ErrUnlockDateUnavailable) {
			t.Fatalf("want ErrUnlockDateUnavailable got %v", err)
		}
	})
}

func TestPenaltyIfWithdrawnNow(t *testing.T) {
	unlock := time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC)
	p := entities.VaultPosition{PrincipalUSD: dec("1000"), MarketValueUSD: dec("1400"), UnlockDate: unlock}

	if got := PenaltyIfWithdrawnNow(p, unlock.Add(-time.Hour), dec("0.10")); !got.Equal(dec("40")) {
		t.Fatalf("locked: want 40 got %s", got)
	}
	if got := PenaltyIfWithdrawnNow(p, unlock, dec("0.10")); !got.Equal(decimal.Zero) {
		t.Fatalf("unlocked: want 0 got %s", got)
	}
	// Underwater position: no earnings, so no penalty.
	under := entities.VaultPosition{PrincipalUSD: dec("1000"), MarketValueUSD: dec("900"), UnlockDate: unlock}
	if got := PenaltyIfWithdrawnNow(under, unlock.Add(-time.Hour), dec("0.10")); !got.Equal(decimal.Zero) {
		t.Fatalf("underwater: want 0 got %s", got)
	}
}

// ---------------------------------------------------------------------------
// Lot accounting: FIFO, partial, spanning, and refuse-to-guess.
// ---------------------------------------------------------------------------

func lot(amount, remaining, acquired string, status entities.VaultLotStatus) *entities.VaultContributionLot {
	at, _ := time.Parse(time.RFC3339, acquired)
	return &entities.VaultContributionLot{
		ID:           uuid.New(),
		AmountUSD:    dec(amount),
		RemainingUSD: dec(remaining),
		AcquiredAt:   at,
		Status:       status,
	}
}

func TestConsumeLotsFIFO(t *testing.T) {
	t.Run("partial single lot", func(t *testing.T) {
		lots := []*entities.VaultContributionLot{lot("100", "100", "2026-01-01T00:00:00Z", entities.VaultLotOpen)}
		if err := ConsumeLots(lots, dec("40")); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !lots[0].RemainingUSD.Equal(dec("60")) {
			t.Fatalf("want 60 remaining got %s", lots[0].RemainingUSD)
		}
		if lots[0].Status != entities.VaultLotOpen {
			t.Fatalf("lot should still be open")
		}
	})

	t.Run("spans lots and closes them", func(t *testing.T) {
		lots := []*entities.VaultContributionLot{
			lot("100", "100", "2026-01-01T00:00:00Z", entities.VaultLotOpen),
			lot("50", "50", "2026-02-01T00:00:00Z", entities.VaultLotOpen),
			lot("25", "25", "2026-03-01T00:00:00Z", entities.VaultLotOpen),
		}
		if err := ConsumeLots(lots, dec("120")); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if lots[0].Status != entities.VaultLotConsumed || !lots[0].RemainingUSD.IsZero() {
			t.Errorf("first lot should be consumed, got %s/%s", lots[0].Status, lots[0].RemainingUSD)
		}
		if !lots[1].RemainingUSD.Equal(dec("30")) || lots[1].Status != entities.VaultLotOpen {
			t.Errorf("second lot should hold 30, got %s/%s", lots[1].RemainingUSD, lots[1].Status)
		}
		if !lots[2].RemainingUSD.Equal(dec("25")) {
			t.Errorf("third lot untouched, got %s", lots[2].RemainingUSD)
		}
	})

	t.Run("over-consume is refused", func(t *testing.T) {
		lots := []*entities.VaultContributionLot{lot("100", "100", "2026-01-01T00:00:00Z", entities.VaultLotOpen)}
		err := ConsumeLots(lots, dec("150"))
		if !errors.Is(err, ErrLotBasisMismatch) {
			t.Fatalf("want ErrLotBasisMismatch got %v", err)
		}
	})

	t.Run("zero principal consumes nothing", func(t *testing.T) {
		lots := []*entities.VaultContributionLot{lot("100", "100", "2026-01-01T00:00:00Z", entities.VaultLotOpen)}
		if err := ConsumeLots(lots, decimal.Zero); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !lots[0].RemainingUSD.Equal(dec("100")) {
			t.Fatalf("lot should be untouched")
		}
	})
}

func TestSumPrincipalAndSort(t *testing.T) {
	lots := []*entities.VaultContributionLot{
		lot("50", "50", "2026-03-01T00:00:00Z", entities.VaultLotOpen),
		lot("100", "10", "2026-01-01T00:00:00Z", entities.VaultLotOpen),
		lot("25", "0", "2026-02-01T00:00:00Z", entities.VaultLotConsumed),
	}
	if got := SumPrincipal(lots); !got.Equal(dec("60")) {
		t.Fatalf("want 60 got %s", got)
	}
	SortLotsFIFO(lots)
	if lots[0].AcquiredAt.Month() != time.January {
		t.Fatalf("FIFO order wrong: first lot is %s", lots[0].AcquiredAt)
	}
}
