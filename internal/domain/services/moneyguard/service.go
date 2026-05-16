package moneyguard

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	spendingsvc "github.com/rail-service/rail_service/internal/domain/services/spending"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

type Repository interface {
	GetSettings(ctx context.Context, userID uuid.UUID) (*entities.MoneyGuardSettings, error)
	UpsertSettings(ctx context.Context, settings *entities.MoneyGuardSettings) error
	CreateCap(ctx context.Context, cap *entities.SpendingCap) error
	ListCaps(ctx context.Context, userID uuid.UUID, activeOnly bool) ([]entities.SpendingCap, error)
	DeleteCap(ctx context.Context, userID, id uuid.UUID) error
	RecordEvent(ctx context.Context, event *entities.MoneyGuardEvent) error
	CountEvents(ctx context.Context, userID uuid.UUID, since time.Time, severities ...string) (int, error)
	CountEventsByType(ctx context.Context, userID uuid.UUID, eventType string, since time.Time) (int, error)
}

type BalanceProvider interface {
	GetAccountBalance(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (decimal.Decimal, error)
}

type DecimalSweeper interface {
	TransferSpendingToStash(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, idempotencyKey string) error
}

type BudgetProvider interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.SpendingBudget, error)
}

type SpendingProvider interface {
	GetSummary(ctx context.Context, userID uuid.UUID, start, end time.Time) (*spendingsvc.Summary, error)
	GetMoneyFlow(ctx context.Context, userID uuid.UUID, start, end time.Time) (*entities.MoneyFlowSummary, error)
}

type ObligationProvider interface {
	ListActive(ctx context.Context, userID uuid.UUID) ([]entities.FinancialObligation, error)
}

type FinancialProfileProvider interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.FinancialProfile, error)
}

type Notifier interface {
	SendGenericNotification(ctx context.Context, userID uuid.UUID, title, message string) error
}

type CardPauser interface {
	PauseUserCards(ctx context.Context, userID uuid.UUID, cooldownMinutes int, reason string) error
}

type Service struct {
	repo        Repository
	balances    BalanceProvider
	sweeper     DecimalSweeper
	spending    SpendingProvider
	budgets     BudgetProvider
	obligations ObligationProvider
	profiles    FinancialProfileProvider
	notifier    Notifier
	cardPauser  CardPauser
	logger      *zap.Logger
}

func NewService(
	repo Repository,
	balances BalanceProvider,
	sweeper DecimalSweeper,
	spending SpendingProvider,
	budgets BudgetProvider,
	obligations ObligationProvider,
	profiles FinancialProfileProvider,
	notifier Notifier,
	cardPauser CardPauser,
	logger *zap.Logger,
) *Service {
	return &Service{
		repo: repo, balances: balances, sweeper: sweeper, spending: spending,
		budgets: budgets, obligations: obligations, profiles: profiles,
		notifier: notifier, cardPauser: cardPauser, logger: logger,
	}
}

type UpdateSettingsRequest struct {
	GuardianMode           *string          `json:"guardian_mode"`
	DecimalSweepEnabled    *bool            `json:"decimal_sweep_enabled"`
	StashRaidLimitPerMonth *int             `json:"stash_raid_limit_per_month"`
	CardCooldownMinutes    *int             `json:"card_cooldown_minutes"`
	SafeToSpendFloor       *decimal.Decimal `json:"safe_to_spend_floor"`
	RecoveryModeDays       *int             `json:"recovery_mode_days"`
}

type CreateCapRequest struct {
	Scope             string          `json:"scope" binding:"required"`
	ScopeValue        string          `json:"scope_value"`
	Period            string          `json:"period"`
	LimitAmount       decimal.Decimal `json:"limit_amount" binding:"required"`
	Currency          string          `json:"currency"`
	EnforcementAction string          `json:"enforcement_action"`
}

type TransactionInput struct {
	Amount    decimal.Decimal
	Currency  string
	Merchant  string
	Category  string
	Reference string
}

func (s *Service) GetSettings(ctx context.Context, userID uuid.UUID) (*entities.MoneyGuardSettings, error) {
	settings, err := s.repo.GetSettings(ctx, userID)
	if err != nil {
		return nil, err
	}
	if settings != nil {
		return settings, nil
	}
	return defaultSettings(userID), nil
}

func (s *Service) UpdateSettings(ctx context.Context, userID uuid.UUID, req UpdateSettingsRequest) (*entities.MoneyGuardSettings, error) {
	settings, err := s.GetSettings(ctx, userID)
	if err != nil {
		return nil, err
	}
	if req.GuardianMode != nil {
		mode := strings.ToLower(strings.TrimSpace(*req.GuardianMode))
		if !validGuardianMode(mode) {
			return nil, fmt.Errorf("unsupported guardian_mode: %s", mode)
		}
		settings.GuardianMode = mode
	}
	if req.DecimalSweepEnabled != nil {
		settings.DecimalSweepEnabled = *req.DecimalSweepEnabled
	}
	if req.StashRaidLimitPerMonth != nil {
		if *req.StashRaidLimitPerMonth < 0 || *req.StashRaidLimitPerMonth > 20 {
			return nil, fmt.Errorf("stash_raid_limit_per_month must be between 0 and 20")
		}
		settings.StashRaidLimitPerMonth = *req.StashRaidLimitPerMonth
	}
	if req.CardCooldownMinutes != nil {
		if *req.CardCooldownMinutes < 0 || *req.CardCooldownMinutes > 1440 {
			return nil, fmt.Errorf("card_cooldown_minutes must be between 0 and 1440")
		}
		settings.CardCooldownMinutes = *req.CardCooldownMinutes
	}
	if req.SafeToSpendFloor != nil {
		if req.SafeToSpendFloor.IsNegative() {
			return nil, fmt.Errorf("safe_to_spend_floor cannot be negative")
		}
		settings.SafeToSpendFloor = *req.SafeToSpendFloor
	}
	if req.RecoveryModeDays != nil {
		if *req.RecoveryModeDays <= 0 {
			settings.RecoveryModeUntil = nil
		} else {
			until := time.Now().UTC().AddDate(0, 0, *req.RecoveryModeDays)
			settings.RecoveryModeUntil = &until
		}
	}
	if err := s.repo.UpsertSettings(ctx, settings); err != nil {
		return nil, err
	}
	return s.GetSettings(ctx, userID)
}

func (s *Service) CreateCap(ctx context.Context, userID uuid.UUID, req CreateCapRequest) (*entities.SpendingCap, error) {
	scope := strings.ToLower(strings.TrimSpace(req.Scope))
	period := strings.ToLower(strings.TrimSpace(req.Period))
	action := strings.ToLower(strings.TrimSpace(req.EnforcementAction))
	if period == "" {
		period = entities.CapPeriodMonth
	}
	if action == "" {
		action = entities.CapActionWarn
	}
	if req.Currency == "" {
		req.Currency = "USD"
	}
	if !validCapScope(scope) {
		return nil, fmt.Errorf("unsupported cap scope: %s", scope)
	}
	if !validCapPeriod(period) {
		return nil, fmt.Errorf("unsupported cap period: %s", period)
	}
	if !validCapAction(action) {
		return nil, fmt.Errorf("unsupported enforcement_action: %s", action)
	}
	if !req.LimitAmount.IsPositive() {
		return nil, fmt.Errorf("limit_amount must be positive")
	}
	cap := &entities.SpendingCap{
		ID: uuid.New(), UserID: userID, Scope: scope, ScopeValue: strings.TrimSpace(req.ScopeValue),
		Period: period, LimitAmount: req.LimitAmount, Currency: strings.ToUpper(req.Currency),
		EnforcementAction: action, IsActive: true,
	}
	if (scope == entities.CapScopeCategory || scope == entities.CapScopeMerchant) && cap.ScopeValue == "" {
		return nil, fmt.Errorf("scope_value is required for %s caps", scope)
	}
	if err := s.repo.CreateCap(ctx, cap); err != nil {
		return nil, err
	}
	return cap, nil
}

func (s *Service) ListCaps(ctx context.Context, userID uuid.UUID) ([]entities.SpendingCap, error) {
	return s.repo.ListCaps(ctx, userID, false)
}

func (s *Service) DeleteCap(ctx context.Context, userID, id uuid.UUID) error {
	err := s.repo.DeleteCap(ctx, userID, id)
	if err == sql.ErrNoRows {
		return err
	}
	return err
}

func (s *Service) Summary(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	settings, err := s.GetSettings(ctx, userID)
	if err != nil {
		return nil, err
	}
	safe, err := s.SafeToSpend(ctx, userID)
	if err != nil {
		return nil, err
	}
	score, err := s.HabitScore(ctx, userID, safe)
	if err != nil {
		return nil, err
	}
	caps, err := s.repo.ListCaps(ctx, userID, true)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"settings":      settings,
		"safe_to_spend": safe,
		"habit_score":   score,
		"active_caps":   caps,
	}, nil
}

func (s *Service) SafeToSpend(ctx context.Context, userID uuid.UUID) (entities.SafeToSpendSnapshot, error) {
	settings, err := s.GetSettings(ctx, userID)
	if err != nil {
		return entities.SafeToSpendSnapshot{}, err
	}
	spend, err := s.balances.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil {
		return entities.SafeToSpendSnapshot{}, fmt.Errorf("get spend balance: %w", err)
	}
	now := time.Now().UTC()
	days := daysToPlan(now)
	if p := s.profileDaysToPlan(ctx, userID); p > 0 {
		days = p
	}

	protected := s.protectedObligations(ctx, userID, now, days)
	budgetRemaining := spend.Sub(protected)
	if s.budgets != nil && s.spending != nil {
		if budget, err := s.budgets.GetByUserID(ctx, userID); err == nil && budget != nil && budget.MonthlyLimit.IsPositive() {
			monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
			if summary, err := s.spending.GetSummary(ctx, userID, monthStart, now); err == nil {
				budgetRemaining = budget.MonthlyLimit.Sub(summary.Total)
			}
		}
	}

	available := spend.Sub(protected)
	if budgetRemaining.LessThan(available) {
		available = budgetRemaining
	}
	if available.IsNegative() {
		available = decimal.Zero
	}
	daily := available.Div(decimal.NewFromInt(int64(maxInt(days, 1)))).RoundBank(2)
	weekly := daily.Mul(decimal.NewFromInt(7)).RoundBank(2)

	status := "safe"
	constraint := "balance"
	if budgetRemaining.LessThan(spend.Sub(protected)) {
		constraint = "budget"
	}
	if daily.LessThanOrEqual(decimal.Zero) {
		status = "danger"
	} else if daily.LessThan(settings.SafeToSpendFloor) {
		status = "tight"
	}

	return entities.SafeToSpendSnapshot{
		SpendBalance: spend.RoundBank(2), ProtectedAmount: protected.RoundBank(2),
		BudgetRemaining: budgetRemaining.RoundBank(2), DaysToPlan: days,
		DailySafeToSpend: daily, WeeklySafeToSpend: weekly, Status: status,
		PrimaryConstraint: constraint,
	}, nil
}

func (s *Service) EvaluateCardTransaction(ctx context.Context, userID uuid.UUID, input TransactionInput) (*entities.MoneyGuardDecision, error) {
	settings, err := s.GetSettings(ctx, userID)
	if err != nil {
		return nil, err
	}
	safe, err := s.SafeToSpend(ctx, userID)
	if err != nil {
		return nil, err
	}
	caps, err := s.repo.ListCaps(ctx, userID, true)
	if err != nil {
		return nil, fmt.Errorf("list caps: %w", err)
	}
	triggered := s.triggeredCaps(ctx, userID, caps, input)
	reasons := make([]string, 0, len(triggered)+2)
	for _, cap := range triggered {
		reasons = append(reasons, fmt.Sprintf("%s cap crossed", cap.Scope))
	}
	if safe.Status == "danger" || safe.Status == "tight" {
		reasons = append(reasons, "safe-to-spend is "+safe.Status)
	}

	action := entities.CapActionWarn
	severity := "info"
	allowed := true
	if len(triggered) > 0 || safe.Status == "tight" {
		severity = "warning"
	}
	if safe.Status == "danger" {
		severity = "critical"
	}
	for _, cap := range triggered {
		action = strongerAction(action, cap.EnforcementAction)
	}
	if settings.GuardianMode == entities.GuardianModeObserve {
		action = entities.CapActionWarn
	}
	if action == entities.CapActionDecline {
		allowed = false
	}
	message := s.decisionMessage(input, safe, triggered)

	decision := &entities.MoneyGuardDecision{
		Mode: settings.GuardianMode, Allowed: allowed, Action: action, Severity: severity,
		Message: message, Reasons: reasons, TriggeredCaps: triggered, SafeToSpend: safe,
	}

	if settings.DecimalSweepEnabled && s.sweeper != nil {
		if swept := s.sweepDecimalBalance(ctx, userID, input.Reference); swept.IsPositive() {
			decision.DecimalSweep = swept
		}
	}

	if severity != "info" {
		s.recordDecision(ctx, userID, input, decision)
		if s.notifier != nil {
			_ = s.notifier.SendGenericNotification(ctx, userID, "Money Guard", message)
		}
	}

	if settings.GuardianMode == entities.GuardianModeStrict && (action == entities.CapActionPauseCard || severity == "critical") && s.cardPauser != nil {
		if err := s.cardPauser.PauseUserCards(ctx, userID, settings.CardCooldownMinutes, message); err != nil {
			s.logger.Warn("money guard failed to pause card", zap.String("user_id", userID.String()), zap.Error(err))
		}
	}

	return decision, nil
}

func (s *Service) EvaluateStashRaid(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, reference string) error {
	settings, err := s.GetSettings(ctx, userID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	count, err := s.repo.CountEventsByType(ctx, userID, "stash_raid", monthStart)
	if err != nil {
		return err
	}
	count++
	severity := "info"
	action := entities.CapActionWarn
	message := fmt.Sprintf("Stash pull #%d this month: $%s moved to Spend.", count, amount.StringFixed(2))
	if settings.StashRaidLimitPerMonth > 0 && count >= settings.StashRaidLimitPerMonth {
		severity = "warning"
		message = fmt.Sprintf("Stash pull #%d this month. Your limit is %d. This is becoming a pattern.", count, settings.StashRaidLimitPerMonth)
	}
	if settings.StashRaidLimitPerMonth > 0 && count > settings.StashRaidLimitPerMonth {
		severity = "critical"
		action = entities.CapActionPauseCard
		message = fmt.Sprintf("Stash pull #%d this month crossed your limit. Money Guard is moving you into recovery mode.", count)
	}
	metadata, _ := json.Marshal(map[string]interface{}{"reference": reference, "monthly_count": count, "limit": settings.StashRaidLimitPerMonth})
	if err := s.repo.RecordEvent(ctx, &entities.MoneyGuardEvent{
		ID: uuid.New(), UserID: userID, EventType: "stash_raid", Severity: severity,
		Amount: amount, Currency: "USD", Action: action, Message: message,
		Metadata: metadata, OccurredAt: now,
	}); err != nil {
		return err
	}
	if severity != "info" && s.notifier != nil {
		_ = s.notifier.SendGenericNotification(ctx, userID, "Stash protection", message)
	}
	if settings.GuardianMode == entities.GuardianModeStrict && severity == "critical" && s.cardPauser != nil {
		if err := s.cardPauser.PauseUserCards(ctx, userID, settings.CardCooldownMinutes, message); err != nil {
			s.logger.Warn("stash raid cooldown failed", zap.String("user_id", userID.String()), zap.Error(err))
		}
	}
	return nil
}

func (s *Service) WeeklyAutopsy(ctx context.Context, userID uuid.UUID) (*entities.WeeklyAutopsy, error) {
	end := time.Now().UTC()
	start := end.AddDate(0, 0, -7)
	summary, err := s.spending.GetSummary(ctx, userID, start, end)
	if err != nil {
		return nil, err
	}
	safe, err := s.SafeToSpend(ctx, userID)
	if err != nil {
		return nil, err
	}
	score, _ := s.HabitScore(ctx, userID, safe)
	autopsy := &entities.WeeklyAutopsy{
		PeriodStart: start, PeriodEnd: end, TotalOutflow: summary.Total,
		TransactionCount: summary.TxCount, SafeToSpend: safe, HabitScore: score,
		WhatHelped: []string{}, WhatHurt: []string{}, RecommendedActions: []string{},
	}
	if len(summary.Categories) > 0 {
		autopsy.TopCategory = summary.Categories[0].Category
		autopsy.TopCategoryAmount = summary.Categories[0].Total
		autopsy.WhatHurt = append(autopsy.WhatHurt, fmt.Sprintf("%s led the week at $%s", autopsy.TopCategory, autopsy.TopCategoryAmount.StringFixed(2)))
		autopsy.RecommendedActions = append(autopsy.RecommendedActions, "Add or tighten a category cap for "+autopsy.TopCategory)
	}
	if len(summary.Merchants) > 0 {
		autopsy.TopMerchant = summary.Merchants[0].Merchant
		autopsy.TopMerchantAmount = summary.Merchants[0].Total
		autopsy.RecommendedActions = append(autopsy.RecommendedActions, "Set a merchant cap for "+autopsy.TopMerchant)
	}
	if safe.Status == "safe" {
		autopsy.WhatHelped = append(autopsy.WhatHelped, "Safe-to-spend stayed positive")
	} else {
		autopsy.WhatHurt = append(autopsy.WhatHurt, "Safe-to-spend is "+safe.Status)
		autopsy.RecommendedActions = append(autopsy.RecommendedActions, "Start a 3-day recovery plan")
	}
	if len(autopsy.RecommendedActions) == 0 {
		autopsy.RecommendedActions = append(autopsy.RecommendedActions, "Keep current caps and review again next week")
	}
	return autopsy, nil
}

func (s *Service) RecoveryPlan(ctx context.Context, userID uuid.UUID) (*entities.RecoveryPlan, error) {
	safe, err := s.SafeToSpend(ctx, userID)
	if err != nil {
		return nil, err
	}
	score, _ := s.HabitScore(ctx, userID, safe)
	days := 3
	target := safe.DailySafeToSpend
	if target.IsZero() {
		target = decimal.NewFromInt(5)
	}
	steps := []string{
		fmt.Sprintf("Keep discretionary spend at or below $%s/day for %d days.", target.StringFixed(2), days),
		"Pause the top leak category until the next check-in.",
		"Do not pull from Stash unless it is an actual emergency.",
	}
	if safe.ProtectedAmount.IsPositive() {
		steps = append(steps, fmt.Sprintf("Leave $%s protected for bills and obligations.", safe.ProtectedAmount.StringFixed(2)))
	}
	return &entities.RecoveryPlan{
		Status: safe.Status, Days: days, DailyTarget: target,
		Steps: steps, SafeToSpend: safe, HabitScore: score,
	}, nil
}

func (s *Service) HabitScore(ctx context.Context, userID uuid.UUID, safe entities.SafeToSpendSnapshot) (int, error) {
	score := 100
	if safe.Status == "tight" {
		score -= 18
	}
	if safe.Status == "danger" {
		score -= 35
	}
	since := time.Now().UTC().AddDate(0, 0, -7)
	events, err := s.repo.CountEvents(ctx, userID, since, "warning", "critical")
	if err != nil {
		return 0, err
	}
	score -= events * 8
	if s.budgets != nil && s.spending != nil {
		now := time.Now().UTC()
		monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		if budget, err := s.budgets.GetByUserID(ctx, userID); err == nil && budget != nil && budget.MonthlyLimit.IsPositive() {
			if summary, err := s.spending.GetSummary(ctx, userID, monthStart, now); err == nil {
				pct := summary.Total.Div(budget.MonthlyLimit).Mul(decimal.NewFromInt(100))
				if pct.GreaterThan(decimal.NewFromInt(100)) {
					score -= 25
				} else if pct.GreaterThan(decimal.NewFromInt(90)) {
					score -= 12
				}
			}
		}
	}
	if score < 0 {
		return 0, nil
	}
	if score > 100 {
		return 100, nil
	}
	return score, nil
}

func (s *Service) sweepDecimalBalance(ctx context.Context, userID uuid.UUID, reference string) decimal.Decimal {
	if s.sweeper == nil {
		return decimal.Zero
	}
	balance, err := s.balances.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil || !balance.IsPositive() {
		return decimal.Zero
	}
	whole := decimal.NewFromInt(balance.IntPart())
	fractional := balance.Sub(whole).RoundBank(2)
	if fractional.LessThan(decimal.NewFromFloat(0.01)) {
		return decimal.Zero
	}
	key := fmt.Sprintf("decimal-sweep-%s-%s", userID.String(), strings.TrimSpace(reference))
	if key == fmt.Sprintf("decimal-sweep-%s-", userID.String()) {
		key = fmt.Sprintf("decimal-sweep-%s-%d", userID.String(), time.Now().UTC().UnixNano())
	}
	if err := s.sweeper.TransferSpendingToStash(ctx, userID, fractional, key); err != nil {
		s.logger.Warn("decimal sweep failed", zap.String("user_id", userID.String()), zap.String("amount", fractional.StringFixed(2)), zap.Error(err))
		return decimal.Zero
	}
	return fractional
}

func (s *Service) triggeredCaps(ctx context.Context, userID uuid.UUID, caps []entities.SpendingCap, input TransactionInput) []entities.SpendingCap {
	if len(caps) == 0 || s.spending == nil {
		return nil
	}
	now := time.Now().UTC()
	triggered := []entities.SpendingCap{}
	for _, cap := range caps {
		start := periodStart(now, cap.Period)
		summary, err := s.spending.GetSummary(ctx, userID, start, now)
		if err != nil {
			continue
		}
		var used decimal.Decimal
		switch cap.Scope {
		case entities.CapScopeDaily, entities.CapScopeWeekly, entities.CapScopeMonthly:
			used = summary.Total
		case entities.CapScopeCategory:
			for _, category := range summary.Categories {
				if strings.EqualFold(category.Category, cap.ScopeValue) {
					used = category.Total
					break
				}
			}
		case entities.CapScopeMerchant:
			for _, merchant := range summary.Merchants {
				if strings.EqualFold(merchant.Merchant, cap.ScopeValue) {
					used = merchant.Total
					break
				}
			}
		}
		if used.Add(input.Amount).GreaterThan(cap.LimitAmount) {
			triggered = append(triggered, cap)
		}
	}
	return triggered
}

func (s *Service) protectedObligations(ctx context.Context, userID uuid.UUID, now time.Time, days int) decimal.Decimal {
	if s.obligations == nil {
		return decimal.Zero
	}
	obligations, err := s.obligations.ListActive(ctx, userID)
	if err != nil {
		return decimal.Zero
	}
	end := now.AddDate(0, 0, days)
	total := decimal.Zero
	for _, obligation := range obligations {
		if obligation.Currency != "" && !strings.EqualFold(obligation.Currency, "USD") && !strings.EqualFold(obligation.Currency, "USDC") {
			continue
		}
		if obligation.DueDate != nil {
			if !obligation.DueDate.Before(now) && !obligation.DueDate.After(end) {
				total = total.Add(obligation.Amount)
			}
			continue
		}
		if obligation.DueDay != nil {
			due := time.Date(now.Year(), now.Month(), *obligation.DueDay, 0, 0, 0, 0, time.UTC)
			if due.Before(now) {
				due = due.AddDate(0, 1, 0)
			}
			if !due.After(end) {
				total = total.Add(obligation.Amount)
			}
			continue
		}
		switch obligation.Cadence {
		case entities.ObligationCadenceWeekly:
			total = total.Add(obligation.Amount.Mul(decimal.NewFromInt(int64(days / 7))))
		case entities.ObligationCadenceBiweekly:
			total = total.Add(obligation.Amount.Mul(decimal.NewFromInt(int64(days / 14))))
		case entities.ObligationCadenceMonthly:
			total = total.Add(obligation.Amount)
		}
	}
	return total
}

func (s *Service) profileDaysToPlan(ctx context.Context, userID uuid.UUID) int {
	if s.profiles == nil {
		return 0
	}
	profile, err := s.profiles.GetByUserID(ctx, userID)
	if err != nil || profile == nil {
		return 0
	}
	switch strings.ToLower(profile.IncomeFrequency) {
	case "weekly":
		return 7
	case "biweekly":
		return 14
	case "monthly":
		return 30
	default:
		return 0
	}
}

func (s *Service) decisionMessage(input TransactionInput, safe entities.SafeToSpendSnapshot, caps []entities.SpendingCap) string {
	if len(caps) > 0 {
		return fmt.Sprintf("$%s at %s crossed a money guard cap. Safe-to-spend is now $%s/day.",
			input.Amount.StringFixed(2), fallback(input.Merchant, "this merchant"), safe.DailySafeToSpend.StringFixed(2))
	}
	if safe.Status == "danger" {
		return fmt.Sprintf("$%s spent. Safe-to-spend is now $0.00/day. Recovery mode should start now.", input.Amount.StringFixed(2))
	}
	if safe.Status == "tight" {
		return fmt.Sprintf("$%s spent. Safe-to-spend is tight at $%s/day.", input.Amount.StringFixed(2), safe.DailySafeToSpend.StringFixed(2))
	}
	return fmt.Sprintf("$%s spent. Safe-to-spend is $%s/day.", input.Amount.StringFixed(2), safe.DailySafeToSpend.StringFixed(2))
}

func (s *Service) recordDecision(ctx context.Context, userID uuid.UUID, input TransactionInput, decision *entities.MoneyGuardDecision) {
	metadata, _ := json.Marshal(map[string]interface{}{
		"reasons":       decision.Reasons,
		"triggered_cap": len(decision.TriggeredCaps),
		"safe_status":   decision.SafeToSpend.Status,
	})
	_ = s.repo.RecordEvent(ctx, &entities.MoneyGuardEvent{
		ID: uuid.New(), UserID: userID, EventType: "card_transaction", Severity: decision.Severity,
		Amount: input.Amount, Currency: fallback(input.Currency, "USD"), Merchant: input.Merchant,
		Category: input.Category, Action: decision.Action, Message: decision.Message,
		Metadata: metadata, OccurredAt: time.Now().UTC(),
	})
}

func defaultSettings(userID uuid.UUID) *entities.MoneyGuardSettings {
	return &entities.MoneyGuardSettings{
		UserID: userID, GuardianMode: entities.GuardianModeNudge, DecimalSweepEnabled: true,
		StashRaidLimitPerMonth: 2, CardCooldownMinutes: 30,
		SafeToSpendFloor: decimal.NewFromInt(10),
	}
}

func validGuardianMode(mode string) bool {
	return mode == entities.GuardianModeObserve || mode == entities.GuardianModeNudge || mode == entities.GuardianModeStrict
}

func validCapScope(scope string) bool {
	switch scope {
	case entities.CapScopeDaily, entities.CapScopeWeekly, entities.CapScopeMonthly, entities.CapScopeCategory, entities.CapScopeMerchant, entities.CapScopeWithdrawal:
		return true
	default:
		return false
	}
}

func validCapPeriod(period string) bool {
	return period == entities.CapPeriodDay || period == entities.CapPeriodWeek || period == entities.CapPeriodMonth
}

func validCapAction(action string) bool {
	switch action {
	case entities.CapActionWarn, entities.CapActionRequirePin, entities.CapActionPauseCard, entities.CapActionDecline:
		return true
	default:
		return false
	}
}

func periodStart(now time.Time, period string) time.Time {
	switch period {
	case entities.CapPeriodDay:
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	case entities.CapPeriodWeek:
		weekday := int(now.Weekday())
		return time.Date(now.Year(), now.Month(), now.Day()-weekday, 0, 0, 0, 0, time.UTC)
	default:
		return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	}
}

func daysToPlan(now time.Time) int {
	nextMonth := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	return maxInt(1, int(nextMonth.Sub(now).Hours()/24))
}

func strongerAction(current, candidate string) string {
	rank := map[string]int{
		entities.CapActionWarn:       1,
		entities.CapActionRequirePin: 2,
		entities.CapActionPauseCard:  3,
		entities.CapActionDecline:    4,
	}
	if rank[candidate] > rank[current] {
		return candidate
	}
	return current
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func fallback(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}
