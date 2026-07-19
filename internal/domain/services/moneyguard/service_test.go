package moneyguard

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	spendingsvc "github.com/rail-service/rail_service/internal/domain/services/spending"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type repoFake struct {
	settings *entities.MoneyGuardSettings
	caps     []entities.SpendingCap
	events   []*entities.MoneyGuardEvent
}

func (r *repoFake) GetSettings(_ context.Context, _ uuid.UUID) (*entities.MoneyGuardSettings, error) {
	return r.settings, nil
}

func (r *repoFake) UpsertSettings(_ context.Context, settings *entities.MoneyGuardSettings) error {
	r.settings = settings
	return nil
}

func (r *repoFake) CreateCap(_ context.Context, cap *entities.SpendingCap) error {
	r.caps = append(r.caps, *cap)
	return nil
}

func (r *repoFake) ListCaps(_ context.Context, _ uuid.UUID, activeOnly bool) ([]entities.SpendingCap, error) {
	if !activeOnly {
		return r.caps, nil
	}
	out := []entities.SpendingCap{}
	for _, cap := range r.caps {
		if cap.IsActive {
			out = append(out, cap)
		}
	}
	return out, nil
}

func (r *repoFake) DeleteCap(_ context.Context, _, id uuid.UUID) error {
	for i := range r.caps {
		if r.caps[i].ID == id {
			r.caps = append(r.caps[:i], r.caps[i+1:]...)
			return nil
		}
	}
	return nil
}

func (r *repoFake) RecordEvent(_ context.Context, event *entities.MoneyGuardEvent) error {
	r.events = append(r.events, event)
	return nil
}

func (r *repoFake) CountEvents(_ context.Context, _ uuid.UUID, _ time.Time, _ ...string) (int, error) {
	return len(r.events), nil
}

func (r *repoFake) CountEventsByType(_ context.Context, _ uuid.UUID, eventType string, _ time.Time) (int, error) {
	count := 0
	for _, event := range r.events {
		if event.EventType == eventType {
			count++
		}
	}
	return count, nil
}

type balanceFake struct{ spend decimal.Decimal }

func (b *balanceFake) GetAccountBalance(_ context.Context, _ uuid.UUID, accountType entities.AccountType) (decimal.Decimal, error) {
	if accountType == entities.AccountTypeSpendingBalance {
		return b.spend, nil
	}
	return decimal.Zero, nil
}

type sweeperFake struct{ amount decimal.Decimal }

func (s *sweeperFake) TransferSpendingToStash(_ context.Context, _ uuid.UUID, amount decimal.Decimal, _ string) error {
	s.amount = s.amount.Add(amount)
	return nil
}

type spendingFake struct {
	total      decimal.Decimal
	category   string
	categoryTx decimal.Decimal
	merchant   string
	merchantTx decimal.Decimal
}

func (s spendingFake) GetSummary(_ context.Context, _ uuid.UUID, _, _ time.Time) (*spendingsvc.Summary, error) {
	return &spendingsvc.Summary{
		Total: s.total, TxCount: 4,
		Categories: []entities.SpendingByCategory{{Category: s.category, Total: s.categoryTx, Count: 2}},
		Merchants:  []entities.SpendingByMerchant{{Merchant: s.merchant, Total: s.merchantTx, Count: 2}},
	}, nil
}

func (s spendingFake) GetMoneyFlow(_ context.Context, _ uuid.UUID, _, _ time.Time) (*entities.MoneyFlowSummary, error) {
	return &entities.MoneyFlowSummary{TotalCardSpend: s.total}, nil
}

type budgetFake struct{ budget *entities.SpendingBudget }

func (b budgetFake) GetByUserID(_ context.Context, _ uuid.UUID) (*entities.SpendingBudget, error) {
	return b.budget, nil
}

type obligationFake struct {
	obligations []entities.FinancialObligation
}

func (o obligationFake) ListActive(_ context.Context, _ uuid.UUID) ([]entities.FinancialObligation, error) {
	return o.obligations, nil
}

type profileFake struct{ profile *entities.FinancialProfile }

func (p profileFake) GetByUserID(_ context.Context, _ uuid.UUID) (*entities.FinancialProfile, error) {
	return p.profile, nil
}

type pauserFake struct{ called bool }

func (p *pauserFake) PauseUserCards(_ context.Context, _ uuid.UUID, _ int, _ string) error {
	p.called = true
	return nil
}

type notifierFake struct{ called bool }

func (n *notifierFake) SendGenericNotification(_ context.Context, _ uuid.UUID, _, _ string) error {
	n.called = true
	return nil
}

func TestSafeToSpendProtectsBillsAndBudget(t *testing.T) {
	userID := uuid.New()
	dueDate := time.Now().UTC().AddDate(0, 0, 2)
	svc := NewService(
		&repoFake{},
		&balanceFake{spend: decimal.NewFromInt(300)},
		nil,
		spendingFake{total: decimal.NewFromInt(150)},
		budgetFake{budget: &entities.SpendingBudget{MonthlyLimit: decimal.NewFromInt(250)}},
		obligationFake{obligations: []entities.FinancialObligation{{
			ID: uuid.New(), UserID: userID, Name: "Rent", Type: entities.ObligationTypeRent,
			Amount: decimal.NewFromInt(80), Currency: "USD", Cadence: entities.ObligationCadenceMonthly,
			DueDate: &dueDate, Status: entities.ObligationStatusActive,
		}}},
		profileFake{profile: &entities.FinancialProfile{UserID: userID, IncomeFrequency: "monthly"}},
		nil, nil, zap.NewNop(),
	)

	safe, err := svc.SafeToSpend(context.Background(), userID)
	require.NoError(t, err)
	require.Equal(t, "100.00", safe.BudgetRemaining.StringFixed(2))
	require.Equal(t, "80.00", safe.ProtectedAmount.StringFixed(2))
	require.Equal(t, "3.33", safe.DailySafeToSpend.StringFixed(2))
}

func TestEvaluateCardTransactionTriggersCapPauseAndDecimalSweep(t *testing.T) {
	userID := uuid.New()
	repo := &repoFake{
		settings: &entities.MoneyGuardSettings{
			UserID: userID, GuardianMode: entities.GuardianModeStrict, DecimalSweepEnabled: true,
			CardCooldownMinutes: 20, SafeToSpendFloor: decimal.NewFromInt(10),
		},
		caps: []entities.SpendingCap{{
			ID: uuid.New(), UserID: userID, Scope: entities.CapScopeMerchant, ScopeValue: "Uber Eats",
			Period: entities.CapPeriodMonth, LimitAmount: decimal.NewFromInt(50),
			Currency: "USD", EnforcementAction: entities.CapActionPauseCard, IsActive: true,
		}},
	}
	balances := &balanceFake{spend: decimal.RequireFromString("123.45")}
	sweeper := &sweeperFake{}
	pauser := &pauserFake{}
	notifier := &notifierFake{}
	svc := NewService(
		repo, balances, sweeper,
		spendingFake{total: decimal.NewFromInt(90), merchant: "Uber Eats", merchantTx: decimal.NewFromInt(75)},
		nil, nil, nil, notifier, pauser, zap.NewNop(),
	)

	decision, err := svc.EvaluateCardTransaction(context.Background(), userID, TransactionInput{
		Amount: decimal.NewFromInt(25), Merchant: "Uber Eats", Reference: "tx_123",
	})
	require.NoError(t, err)
	require.Equal(t, entities.CapActionPauseCard, decision.Action)
	require.True(t, pauser.called)
	require.True(t, notifier.called)
	require.Equal(t, "0.45", sweeper.amount.StringFixed(2))
	require.Len(t, repo.events, 1)
}

func TestEvaluateCardAuthorizationDoesNotApplySideEffects(t *testing.T) {
	userID := uuid.New()
	repo := &repoFake{
		settings: &entities.MoneyGuardSettings{
			UserID: userID, GuardianMode: entities.GuardianModeStrict, DecimalSweepEnabled: true,
			CardCooldownMinutes: 20, SafeToSpendFloor: decimal.NewFromInt(10),
		},
		caps: []entities.SpendingCap{{
			ID: uuid.New(), UserID: userID, Scope: entities.CapScopeMerchant, ScopeValue: "Uber Eats",
			Period: entities.CapPeriodMonth, LimitAmount: decimal.NewFromInt(50),
			Currency: "USD", EnforcementAction: entities.CapActionPauseCard, IsActive: true,
		}},
	}
	balances := &balanceFake{spend: decimal.RequireFromString("123.45")}
	sweeper := &sweeperFake{}
	pauser := &pauserFake{}
	notifier := &notifierFake{}
	svc := NewService(
		repo, balances, sweeper,
		spendingFake{total: decimal.NewFromInt(90), merchant: "Uber Eats", merchantTx: decimal.NewFromInt(75)},
		nil, nil, nil, notifier, pauser, zap.NewNop(),
	)

	decision, err := svc.EvaluateCardAuthorization(context.Background(), userID, TransactionInput{
		Amount: decimal.NewFromInt(25), Merchant: "Uber Eats", Reference: "auth_123",
	})
	require.NoError(t, err)
	require.Equal(t, entities.CapActionPauseCard, decision.Action)
	require.False(t, pauser.called)
	require.False(t, notifier.called)
	require.True(t, sweeper.amount.IsZero())
	require.Empty(t, repo.events)
}

func TestScopedCapsOnlyTriggerForMatchingTransactions(t *testing.T) {
	userID := uuid.New()
	repo := &repoFake{
		settings: &entities.MoneyGuardSettings{
			UserID: userID, GuardianMode: entities.GuardianModeNudge,
			SafeToSpendFloor: decimal.NewFromInt(10),
		},
		caps: []entities.SpendingCap{{
			ID: uuid.New(), UserID: userID, Scope: entities.CapScopeMerchant, ScopeValue: "Bet9ja",
			Period: entities.CapPeriodMonth, LimitAmount: merchantBlockLimit,
			Currency: "USD", EnforcementAction: entities.CapActionDecline, IsActive: true,
		}},
	}
	svc := NewService(
		repo, &balanceFake{spend: decimal.NewFromInt(500)}, nil,
		spendingFake{total: decimal.NewFromInt(90)},
		nil, nil, nil, nil, nil, zap.NewNop(),
	)

	// Unrelated merchant must not trip the block cap.
	decision, err := svc.EvaluateCardAuthorization(context.Background(), userID, TransactionInput{
		Amount: decimal.NewFromInt(12), Merchant: "Starbucks", Reference: "auth_ok",
	})
	require.NoError(t, err)
	require.True(t, decision.Allowed)
	require.Empty(t, decision.TriggeredCaps)

	// Noisy card-network descriptor for the blocked merchant must decline.
	decision, err = svc.EvaluateCardAuthorization(context.Background(), userID, TransactionInput{
		Amount: decimal.NewFromInt(12), Merchant: "BET9JA LAGOS NG", Reference: "auth_blocked",
	})
	require.NoError(t, err)
	require.False(t, decision.Allowed)
	require.Len(t, decision.TriggeredCaps, 1)
}

func TestEvaluateStashRaidWarnsAndPausesAfterLimit(t *testing.T) {
	userID := uuid.New()
	repo := &repoFake{
		settings: &entities.MoneyGuardSettings{
			UserID: userID, GuardianMode: entities.GuardianModeStrict,
			StashRaidLimitPerMonth: 1, CardCooldownMinutes: 15,
			SafeToSpendFloor: decimal.NewFromInt(10),
		},
		events: []*entities.MoneyGuardEvent{{EventType: "stash_raid", Severity: "warning"}},
	}
	pauser := &pauserFake{}
	notifier := &notifierFake{}
	svc := NewService(repo, &balanceFake{}, nil, nil, nil, nil, nil, notifier, pauser, zap.NewNop())

	err := svc.EvaluateStashRaid(context.Background(), userID, decimal.NewFromInt(40), "stash_xfer_1")
	require.NoError(t, err)
	require.True(t, notifier.called)
	require.True(t, pauser.called)
	require.Len(t, repo.events, 2)
	require.Equal(t, "critical", repo.events[1].Severity)
}
