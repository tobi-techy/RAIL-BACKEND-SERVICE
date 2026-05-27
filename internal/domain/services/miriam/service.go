package miriam

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

const (
	EventWorkerSweep          = "worker_sweep"
	EventIdleSpend            = "idle_spend"
	EventSpendingSpike        = "spending_spike"
	EventBillPressure         = "bill_pressure"
	EventIncomeLowerThanUsual = "income_lower_than_usual"
)

type Repository interface {
	UpsertMoneyState(ctx context.Context, state *entities.MiriamMoneyState) error
	GetMoneyState(ctx context.Context, userID uuid.UUID) (*entities.MiriamMoneyState, error)
	CreateMandate(ctx context.Context, mandate *entities.MiriamAutopilotMandate) error
	ListMandates(ctx context.Context, userID uuid.UUID) ([]entities.MiriamAutopilotMandate, error)
	ListActiveMandates(ctx context.Context, userID uuid.UUID) ([]entities.MiriamAutopilotMandate, error)
	UpdateMandateStatus(ctx context.Context, userID, id uuid.UUID, status string) error
	MarkMandateExecuted(ctx context.Context, id uuid.UUID, at time.Time) error
	MandateExecutedAmountSince(ctx context.Context, mandateID uuid.UUID, since time.Time) (decimal.Decimal, error)
	CreateReceipt(ctx context.Context, receipt *entities.MiriamDecisionReceipt) error
	ListReceipts(ctx context.Context, userID uuid.UUID, limit int) ([]entities.MiriamDecisionReceipt, error)
	CreateEvent(ctx context.Context, event *entities.MiriamEvent) error
	CreateLearningSignal(ctx context.Context, signal *entities.MiriamLearningSignal) error
	RecentLearningBias(ctx context.Context, userID uuid.UUID, since time.Time) (decimal.Decimal, error)
	SavePredictionOutcomes(ctx context.Context, outcomes []entities.MiriamPredictionOutcome) error
	GetPendingPredictionOutcomes(ctx context.Context, userID uuid.UUID) ([]entities.MiriamPredictionOutcome, error)
	MarkPredictionOutcome(ctx context.Context, id uuid.UUID, outcome bool, observedAt time.Time) error
	GetPredictionHitRate(ctx context.Context, userID uuid.UUID, predictionType string, since time.Time) (float64, error)
}

type BalanceProvider interface {
	GetAccountBalance(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (decimal.Decimal, error)
}

type SpendingProvider interface {
	GetMoneyFlow(ctx context.Context, userID uuid.UUID, start, end time.Time) (*entities.MoneyFlowSummary, error)
}

type ObligationProvider interface {
	ListActive(ctx context.Context, userID uuid.UUID) ([]entities.FinancialObligation, error)
}

type FinancialProfileProvider interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.FinancialProfile, error)
}

type SafeToSpendProvider interface {
	SafeToSpend(ctx context.Context, userID uuid.UUID) (entities.SafeToSpendSnapshot, error)
}

type TransferExecutor interface {
	TransferSpendingToStash(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, idempotencyKey string) error
	TransferStashToSpending(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, idempotencyKey string) error
}

type Notifier interface {
	SendGenericNotification(ctx context.Context, userID uuid.UUID, title, message string) error
}

type Service struct {
	repo        Repository
	balances    BalanceProvider
	spending    SpendingProvider
	obligations ObligationProvider
	profiles    FinancialProfileProvider
	safeSpend   SafeToSpendProvider
	transfer    TransferExecutor
	notifier    Notifier
	logger      *zap.Logger
}

func NewService(
	repo Repository,
	balances BalanceProvider,
	spending SpendingProvider,
	obligations ObligationProvider,
	profiles FinancialProfileProvider,
	safeSpend SafeToSpendProvider,
	transfer TransferExecutor,
	notifier Notifier,
	logger *zap.Logger,
) *Service {
	return &Service{
		repo: repo, balances: balances, spending: spending, obligations: obligations,
		profiles: profiles, safeSpend: safeSpend, transfer: transfer, notifier: notifier,
		logger: logger,
	}
}

type CreateMandateRequest struct {
	Name               string          `json:"name"`
	MaxAmountPerAction decimal.Decimal `json:"max_amount_per_action"`
	MaxAmountPerDay    decimal.Decimal `json:"max_amount_per_day"`
	MinSpendBalance    decimal.Decimal `json:"min_spend_balance"`
	MinSafeToSpend     decimal.Decimal `json:"min_safe_to_spend"`
	CooldownMinutes    int             `json:"cooldown_minutes"`
	ExpiresAt          *time.Time      `json:"expires_at"`
}

func (s *Service) CreateTransferToStashMandate(ctx context.Context, userID uuid.UUID, req CreateMandateRequest) (*entities.MiriamAutopilotMandate, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "Miriam quiet Stash moves"
	}
	if !req.MaxAmountPerAction.IsPositive() {
		return nil, fmt.Errorf("max_amount_per_action must be positive")
	}
	if !req.MaxAmountPerDay.IsPositive() {
		return nil, fmt.Errorf("max_amount_per_day must be positive")
	}
	if req.MaxAmountPerAction.GreaterThan(req.MaxAmountPerDay) {
		return nil, fmt.Errorf("max_amount_per_action cannot exceed max_amount_per_day")
	}
	if req.CooldownMinutes <= 0 {
		req.CooldownMinutes = 1440
	}
	mandate := &entities.MiriamAutopilotMandate{
		ID:                 uuid.New(),
		UserID:             userID,
		Name:               name,
		ActionType:         entities.MiriamMandateTransferToStash,
		Status:             entities.MiriamMandateStatusActive,
		MaxAmountPerAction: req.MaxAmountPerAction,
		MaxAmountPerDay:    req.MaxAmountPerDay,
		MinSpendBalance:    req.MinSpendBalance,
		MinSafeToSpend:     req.MinSafeToSpend,
		CooldownMinutes:    req.CooldownMinutes,
		ExpiresAt:          req.ExpiresAt,
	}
	if err := s.repo.CreateMandate(ctx, mandate); err != nil {
		return nil, err
	}
	return mandate, nil
}

func (s *Service) ListMandates(ctx context.Context, userID uuid.UUID) ([]entities.MiriamAutopilotMandate, error) {
	return s.repo.ListMandates(ctx, userID)
}

func (s *Service) UpdateMandateStatus(ctx context.Context, userID, id uuid.UUID, status string) error {
	switch status {
	case entities.MiriamMandateStatusActive, entities.MiriamMandateStatusPaused, entities.MiriamMandateStatusExpired:
	default:
		return fmt.Errorf("unsupported mandate status: %s", status)
	}
	return s.repo.UpdateMandateStatus(ctx, userID, id, status)
}

func (s *Service) GetMoneyState(ctx context.Context, userID uuid.UUID) (*entities.MiriamMoneyState, error) {
	state, err := s.repo.GetMoneyState(ctx, userID)
	if err != nil || state != nil {
		return state, err
	}
	return s.RefreshMoneyState(ctx, userID)
}

func (s *Service) ListReceipts(ctx context.Context, userID uuid.UUID, limit int) ([]entities.MiriamDecisionReceipt, error) {
	return s.repo.ListReceipts(ctx, userID, limit)
}

func (s *Service) RecordFeedback(ctx context.Context, userID, receiptID uuid.UUID, signal string) error {
	switch signal {
	case entities.MiriamFeedbackAccepted, entities.MiriamFeedbackIgnored, entities.MiriamFeedbackReversed, entities.MiriamFeedbackDismissed:
	default:
		return fmt.Errorf("unsupported feedback signal: %s", signal)
	}
	return s.repo.CreateLearningSignal(ctx, &entities.MiriamLearningSignal{
		ID:     receiptIDFromSignal(receiptID, signal),
		UserID: userID, ReceiptID: receiptID, Signal: signal,
		Weight: decimal.NewFromInt(1), CreatedAt: time.Now().UTC(),
	})
}

func (s *Service) RecordEvent(ctx context.Context, userID uuid.UUID, eventType, severity string, amount decimal.Decimal, metadata map[string]interface{}) error {
	raw := mustJSON(metadata)
	return s.repo.CreateEvent(ctx, &entities.MiriamEvent{
		ID: uuid.New(), UserID: userID, EventType: eventType, Severity: severity,
		Amount: amount, Currency: "USD", Metadata: raw, CreatedAt: time.Now().UTC(),
	})
}

func (s *Service) EvaluateUser(ctx context.Context, userID uuid.UUID, eventType string) error {
	state, err := s.RefreshMoneyState(ctx, userID)
	if err != nil {
		return err
	}
	if eventType != EventWorkerSweep {
		if err := s.RecordEvent(ctx, userID, eventType, "info", decimal.Zero, map[string]interface{}{
			"source": "miriam_evaluation",
		}); err != nil && s.logger != nil {
			s.logger.Debug("miriam event recording failed", zap.String("user_id", userID.String()), zap.Error(err))
		}
	}
	if eventType != EventWorkerSweep && state.AnomalyCount > 0 {
		if err := s.recordDerivedEvents(ctx, state); err != nil && s.logger != nil {
			s.logger.Debug("miriam derived event recording failed", zap.String("user_id", userID.String()), zap.Error(err))
		}
	}
	return s.evaluateMandates(ctx, state, eventType)
}

func (s *Service) TriggerUser(ctx context.Context, userID uuid.UUID, eventType string, amount decimal.Decimal, metadata map[string]interface{}) error {
	if err := s.RecordEvent(ctx, userID, eventType, "info", amount, metadata); err != nil && s.logger != nil {
		s.logger.Debug("miriam derived event recording failed", zap.String("user_id", userID.String()), zap.Error(err))
	}
	return s.EvaluateUser(ctx, userID, eventType)
}

func (s *Service) RefreshMoneyState(ctx context.Context, userID uuid.UUID) (*entities.MiriamMoneyState, error) {
	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	lastMonthStart := monthStart.AddDate(0, -1, 0)
	weekStart := now.AddDate(0, 0, -7)

	spend, err := s.balances.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil {
		return nil, fmt.Errorf("refresh miriam state: spend balance: %w", err)
	}
	stash, err := s.balances.GetAccountBalance(ctx, userID, entities.AccountTypeStashBalance)
	if err != nil {
		return nil, fmt.Errorf("refresh miriam state: stash balance: %w", err)
	}
	trailingStart := monthStart.AddDate(0, -6, 0)
	monthFlow := s.moneyFlow(ctx, userID, monthStart, now)
	lastMonthFlow := s.moneyFlow(ctx, userID, lastMonthStart, monthStart)
	trailingFlow := s.moneyFlow(ctx, userID, trailingStart, monthStart)
	weekFlow := s.moneyFlow(ctx, userID, weekStart, now)

	monthOut := totalOut(monthFlow)
	lastMonthOut := totalOut(lastMonthFlow)
	trailingOut := totalOut(trailingFlow)
	weekOut := totalOut(weekFlow)
	dailyOut := decimal.Zero
	if weekOut.IsPositive() {
		dailyOut = weekOut.Div(decimal.NewFromInt(7))
	}

	safeDaily := decimal.Zero
	if s.safeSpend != nil {
		if safe, err := s.safeSpend.SafeToSpend(ctx, userID); err == nil {
			safeDaily = safe.DailySafeToSpend
		} else if s.logger != nil {
			s.logger.Warn("safe-to-spend fetch failed, falling back to zero", zap.Error(err))
		}
	}

	upcoming := s.upcomingObligations(ctx, userID, now, now.AddDate(0, 0, 30))
	recurring := s.recurringFromObligations(ctx, userID)
	runway := 0
	if dailyOut.IsPositive() {
		runway = int(math.Floor(spend.Div(dailyOut).InexactFloat64()))
	}

	profileIncome, profileCadence, stashTarget := s.profileTargets(ctx, userID)

	// Count estimated active months from trailing deposit density.
	// Conservative: assumes at least 1 deposit per 2 months.
	trailingMonths := 6
	activeMonths := trailingMonths
	if trailingFlow.DepositCount > 0 {
		if est := (trailingFlow.DepositCount + 1) / 2; est > 0 && est < activeMonths {
			activeMonths = est
		}
	}
	hasTrailingData := trailingFlow.TotalDeposits.IsPositive() || trailingOut.IsPositive()

	avgIncome := decimal.Zero
	if trailingFlow.TotalDeposits.IsPositive() && activeMonths > 0 {
		avgIncome = trailingFlow.TotalDeposits.Div(decimal.NewFromInt(int64(activeMonths)))
	} else if monthFlow.TotalDeposits.IsPositive() {
		avgIncome = monthFlow.TotalDeposits
	} else if profileIncome.IsPositive() {
		avgIncome = profileIncome
	}

	// Monthly spend: trailing average of outflow over the 6-month window.
	monthlySpend := decimal.Zero
	if hasTrailingData && activeMonths > 0 {
		monthlySpend = trailingOut.Div(decimal.NewFromInt(int64(activeMonths)))
	}

	// Monthly savings: trailing average of (deposits − outflow), floored at 0.
	monthlySavings := decimal.Zero
	if avgIncome.IsPositive() && monthlySpend.IsPositive() {
		monthlySavings = avgIncome.Sub(monthlySpend)
		if monthlySavings.IsNegative() {
			monthlySavings = decimal.Zero
		}
	}

	incomeCadence := profileCadence
	if incomeCadence == "" || incomeCadence == "unknown" {
		// Use last month's deposit count when current month is less than 2 weeks old
		// to avoid inferring "unknown" from partial-month data
		depositCount := monthFlow.DepositCount
		if now.Day() < 14 && lastMonthFlow.DepositCount > 0 {
			depositCount = lastMonthFlow.DepositCount
		}
		incomeCadence = inferCadence(depositCount)
	}
	if stashTarget.IsZero() {
		stashTarget = avgIncome.Mul(decimal.NewFromFloat(0.30)).RoundBank(2)
	}

	// Anomaly detection: compare against 6-month trailing baseline for robustness.
	anomalies := 0
	trailingAvgOut := decimal.Zero
	if hasTrailingData && activeMonths > 0 {
		trailingAvgOut = trailingOut.Div(decimal.NewFromInt(int64(activeMonths)))
	}
	if trailingAvgOut.IsPositive() && monthOut.GreaterThan(trailingAvgOut.Mul(decimal.NewFromFloat(1.50))) {
		anomalies++
	}
	if avgIncome.IsPositive() && monthFlow.TotalDeposits.LessThan(avgIncome.Mul(decimal.NewFromFloat(0.50))) && now.Day() >= 15 {
		anomalies++
	}
	if upcoming.IsPositive() && spend.LessThan(upcoming) {
		anomalies++
	}

	// Confidence: data-density-aware score over trailing window (0–100).
	confidenceScore := 0

	// Income signal (0–35): how much deposit history backs the income estimate.
	if trailingFlow.TotalDeposits.IsPositive() {
		switch {
		case activeMonths >= 6:
			confidenceScore += 35
		case activeMonths >= 3:
			confidenceScore += 25
		case activeMonths >= 1:
			confidenceScore += 15
		}
	} else if monthFlow.TotalDeposits.IsPositive() {
		confidenceScore += 10
	}

	// Spending signal (0–25): consistent outflow data across the window.
	if trailingOut.IsPositive() {
		switch {
		case activeMonths >= 6:
			confidenceScore += 25
		case activeMonths >= 3:
			confidenceScore += 20
		case activeMonths >= 1:
			confidenceScore += 10
		}
	} else if monthOut.IsPositive() {
		confidenceScore += 10
	}

	// Obligation signal (0–20).
	if recurring.IsPositive() {
		confidenceScore += 10
	}
	if upcoming.IsPositive() {
		confidenceScore += 10
	}

	// Balance signal (0–20): we have real account balances.
	if spend.IsPositive() {
		confidenceScore += 10
	}
	if stash.IsPositive() {
		confidenceScore += 10
	}

	if confidenceScore > 100 {
		confidenceScore = 100
	}

	// Calibrate confidence by prediction accuracy (hit rate over last 90 days).
	calibrationScore := 100
	if s.repo != nil {
		hitRate, err := s.repo.GetPredictionHitRate(ctx, userID, "", time.Now().UTC().AddDate(0, -3, 0))
		if err == nil && hitRate > 0 {
			calibrated := int(float64(confidenceScore) * hitRate)
			if calibrated < confidenceScore {
				confidenceScore = calibrated
			}
			calibrationScore = int(hitRate * 100)
		}
	}

	confidenceLevel := "low"
	if confidenceScore >= 75 {
		confidenceLevel = "high"
	} else if confidenceScore >= 50 {
		confidenceLevel = "medium"
	}

	obligationVsActual := "aligned"
	if monthlySpend.IsPositive() && recurring.IsPositive() {
		ratio := recurring.Div(monthlySpend).InexactFloat64()
		switch {
		case ratio > 1.5:
			obligationVsActual = "obligations_greatly_exceed_actual"
		case ratio > 1.2:
			obligationVsActual = "obligations_moderately_exceed_actual"
		case ratio < 0.5:
			obligationVsActual = "obligations_greatly_below_actual"
		case ratio < 0.8:
			obligationVsActual = "obligations_moderately_below_actual"
		}
	}

	snapshot := map[string]interface{}{
		"spend_balance":            spend.StringFixed(2),
		"stash_balance":            stash.StringFixed(2),
		"month_income":             monthFlow.TotalDeposits.StringFixed(2),
		"month_outflow":            monthOut.StringFixed(2),
		"last_month_outflow":       lastMonthOut.StringFixed(2),
		"last_7_days_outflow":      weekOut.StringFixed(2),
		"daily_outflow_average":    dailyOut.StringFixed(2),
		"monthly_spend_trailing":   monthlySpend.StringFixed(2),
		"monthly_savings_trailing": monthlySavings.StringFixed(2),
		"trailing_window_months":   trailingMonths,
		"active_months":            activeMonths,
		"obligation_vs_actual":     obligationVsActual,
		"data_used":                []string{"balances", "money_flow", "obligations", "financial_profile", "money_guard_safe_to_spend"},
	}
	state := &entities.MiriamMoneyState{
		UserID: userID, IncomeCadence: incomeCadence, AvgMonthlyIncome: avgIncome.RoundBank(2),
		UpcomingObligations: upcoming.RoundBank(2), SafeToSpendDaily: safeDaily.RoundBank(2),
		LiquidityRunwayDays: runway, StashTarget: stashTarget.RoundBank(2),
		RecurringSpendMonthly: recurring.RoundBank(2), AnomalyCount: anomalies,
		ConfidenceLevel: confidenceLevel, ConfidenceScore: confidenceScore,
		LastEvaluatedAt: now, Snapshot: mustJSON(snapshot),
		MonthlySpend: monthlySpend.RoundBank(2), MonthlySavings: monthlySavings.RoundBank(2),
		SpendBalance: spend.RoundBank(2), StashBalance: stash.RoundBank(2),
		CalibrationScore: calibrationScore,
	}
	if err := s.repo.UpsertMoneyState(ctx, state); err != nil {
		return nil, err
	}
	return state, nil
}

func (s *Service) evaluateMandates(ctx context.Context, state *entities.MiriamMoneyState, eventType string) error {
	mandates, err := s.repo.ListActiveMandates(ctx, state.UserID)
	if err != nil {
		return err
	}
	for _, mandate := range mandates {
		if mandate.ActionType != entities.MiriamMandateTransferToStash {
			continue
		}
		if err := s.evaluateTransferToStash(ctx, state, mandate, eventType); err != nil && s.logger != nil {
			s.logger.Warn("miriam mandate evaluation failed", zap.String("user_id", state.UserID.String()), zap.String("mandate_id", mandate.ID.String()), zap.Error(err))
		}
	}
	return nil
}

func (s *Service) evaluateTransferToStash(ctx context.Context, state *entities.MiriamMoneyState, mandate entities.MiriamAutopilotMandate, eventType string) error {
	now := time.Now().UTC()
	if mandate.LastExecutedAt != nil && now.Sub(*mandate.LastExecutedAt) < time.Duration(mandate.CooldownMinutes)*time.Minute {
		return nil
	}
	spend, err := s.balances.GetAccountBalance(ctx, state.UserID, entities.AccountTypeSpendingBalance)
	if err != nil {
		return err
	}
	if spend.LessThanOrEqual(mandate.MinSpendBalance) || state.SafeToSpendDaily.LessThan(mandate.MinSafeToSpend) {
		if eventType == EventWorkerSweep {
			return nil
		}
		return s.createReceipt(ctx, state.UserID, &mandate, eventType, entities.MiriamReceiptStatusSkipped, decimal.Zero,
			"Skipped quiet Stash move because Spend or safe-to-spend was below the approved floor.", nil)
	}
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	usedToday, err := s.repo.MandateExecutedAmountSince(ctx, mandate.ID, todayStart)
	if err != nil {
		return err
	}
	remainingToday := mandate.MaxAmountPerDay.Sub(usedToday)
	if !remainingToday.IsPositive() {
		return nil
	}
	availableAboveFloor := spend.Sub(mandate.MinSpendBalance)
	amount := minDecimal(mandate.MaxAmountPerAction, remainingToday, availableAboveFloor)
	amount = s.applyLearning(ctx, state.UserID, amount)
	if amount.LessThan(decimal.NewFromInt(1)) {
		return nil
	}
	amount = amount.RoundBank(2)
	reason := fmt.Sprintf("Moved $%s to Stash because Spend was $%s above the approved floor and safe-to-spend was $%s/day.",
		amount.StringFixed(2), availableAboveFloor.StringFixed(2), state.SafeToSpendDaily.StringFixed(2))
	// Idempotency key uses cooldown-window granularity to dedupe concurrent/retried
	// evaluations while allowing multiple transfers per day (capped by MaxAmountPerDay).
	windowSec := int64(mandate.CooldownMinutes) * 60
	if windowSec <= 0 {
		windowSec = 300 // 5-minute default window
	}
	idempotencyKey := fmt.Sprintf("miriam-autopilot-%s-%d", mandate.ID.String(), now.Unix()/windowSec)
	if err := s.transfer.TransferSpendingToStash(ctx, state.UserID, amount, idempotencyKey); err != nil {
		errMsg := err.Error()
		return s.createReceipt(ctx, state.UserID, &mandate, eventType, entities.MiriamReceiptStatusFailed, amount, reason, &errMsg)
	}
	if err := s.repo.MarkMandateExecuted(ctx, mandate.ID, now); err != nil {
		return err
	}
	if err := s.createReceipt(ctx, state.UserID, &mandate, eventType, entities.MiriamReceiptStatusExecuted, amount, reason, nil); err != nil {
		return err
	}
	if s.notifier != nil {
		_ = s.notifier.SendGenericNotification(ctx, state.UserID, "Miriam moved money forward", reason)
	}
	return nil
}

func (s *Service) createReceipt(ctx context.Context, userID uuid.UUID, mandate *entities.MiriamAutopilotMandate, eventType, status string, amount decimal.Decimal, reason string, errMsg *string) error {
	var mandateID *uuid.UUID
	actionType := ""
	if mandate != nil {
		mandateID = &mandate.ID
		actionType = mandate.ActionType
	}
	return s.repo.CreateReceipt(ctx, &entities.MiriamDecisionReceipt{
		ID: uuid.New(), UserID: userID, MandateID: mandateID, EventType: eventType,
		ActionType: actionType, Amount: amount, Currency: "USD", Status: status,
		Reason: reason, Evidence: mustJSON(map[string]interface{}{"generated_by": "miriam_worker"}),
		ErrorMessage: errMsg, CreatedAt: time.Now().UTC(),
	})
}

func (s *Service) recordDerivedEvents(ctx context.Context, state *entities.MiriamMoneyState) error {
	if state.AnomalyCount <= 0 {
		return nil
	}
	return s.RecordEvent(ctx, state.UserID, EventSpendingSpike, "warning", decimal.Zero, map[string]interface{}{
		"anomaly_count": state.AnomalyCount,
		"confidence":    state.ConfidenceLevel,
	})
}

// HasActiveMandate checks if the user has any active mandate of the given action type.
func (s *Service) HasActiveMandate(ctx context.Context, userID uuid.UUID, actionType string) (bool, error) {
	mandates, err := s.repo.ListActiveMandates(ctx, userID)
	if err != nil {
		return false, err
	}
	for _, m := range mandates {
		if m.ActionType == actionType {
			return true, nil
		}
	}
	return false, nil
}

func (s *Service) moneyFlow(ctx context.Context, userID uuid.UUID, start, end time.Time) *entities.MoneyFlowSummary {
	if s.spending == nil {
		return &entities.MoneyFlowSummary{}
	}
	flow, err := s.spending.GetMoneyFlow(ctx, userID, start, end)
	if err != nil || flow == nil {
		if err != nil && s.logger != nil {
			s.logger.Warn("moneyFlow: spending provider returned error, using empty summary", zap.String("user_id", userID.String()), zap.Error(err))
		}
		return &entities.MoneyFlowSummary{}
	}
	return flow
}

func (s *Service) upcomingObligations(ctx context.Context, userID uuid.UUID, start, end time.Time) decimal.Decimal {
	if s.obligations == nil {
		return decimal.Zero
	}
	obligations, err := s.obligations.ListActive(ctx, userID)
	if err != nil {
		return decimal.Zero
	}
	total := decimal.Zero
	for _, obligation := range obligations {
		if !usdLike(obligation.Currency) {
			continue
		}
		if obligation.DueDate != nil && !obligation.DueDate.Before(start) && !obligation.DueDate.After(end) {
			total = total.Add(obligation.Amount)
			continue
		}
		if obligation.DueDay != nil {
			due := time.Date(start.Year(), start.Month(), *obligation.DueDay, 0, 0, 0, 0, time.UTC)
			if due.Before(start) {
				due = due.AddDate(0, 1, 0)
			}
			if !due.After(end) {
				total = total.Add(obligation.Amount)
			}
		}
	}
	return total
}

func (s *Service) recurringFromObligations(ctx context.Context, userID uuid.UUID) decimal.Decimal {
	if s.obligations == nil {
		return decimal.Zero
	}
	obligations, err := s.obligations.ListActive(ctx, userID)
	if err != nil {
		return decimal.Zero
	}
	total := decimal.Zero
	for _, obligation := range obligations {
		if !usdLike(obligation.Currency) {
			continue
		}
		switch strings.ToLower(obligation.Cadence) {
		case "weekly":
			total = total.Add(obligation.Amount.Mul(decimal.NewFromFloat(4.33)))
		case "biweekly":
			total = total.Add(obligation.Amount.Mul(decimal.NewFromFloat(2.165)))
		case "monthly":
			total = total.Add(obligation.Amount)
		case "quarterly":
			total = total.Add(obligation.Amount.Div(decimal.NewFromInt(3)))
		case "annual":
			total = total.Add(obligation.Amount.Div(decimal.NewFromInt(12)))
		}
	}
	return total
}

func (s *Service) profileTargets(ctx context.Context, userID uuid.UUID) (decimal.Decimal, string, decimal.Decimal) {
	if s.profiles == nil {
		return decimal.Zero, "unknown", decimal.Zero
	}
	profile, err := s.profiles.GetByUserID(ctx, userID)
	if err != nil || profile == nil {
		return decimal.Zero, "unknown", decimal.Zero
	}
	target := profile.EmergencyFundTarget
	if target.IsZero() {
		target = profile.MonthlySavingsTarget
	}
	return profile.MonthlyIncome, profile.IncomeFrequency, target
}

func (s *Service) applyLearning(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) decimal.Decimal {
	bias, err := s.repo.RecentLearningBias(ctx, userID, time.Now().UTC().AddDate(0, -1, 0))
	if err != nil {
		return amount
	}
	if bias.LessThan(decimal.NewFromInt(-2)) {
		return amount.Mul(decimal.NewFromFloat(0.50))
	}
	if bias.GreaterThan(decimal.NewFromInt(3)) {
		return amount.Mul(decimal.NewFromFloat(1.10))
	}
	return amount
}

func totalOut(flow *entities.MoneyFlowSummary) decimal.Decimal {
	if flow == nil {
		return decimal.Zero
	}
	return flow.TotalWithdrawals.Add(flow.TotalCardSpend).Add(flow.TotalP2P).Add(flow.TotalReceipts)
}

func inferCadence(depositCount int) string {
	switch {
	case depositCount >= 4:
		return "weekly"
	case depositCount >= 2:
		return "biweekly"
	case depositCount == 1:
		return "monthly"
	default:
		return "unknown"
	}
}

func usdLike(currency string) bool {
	c := strings.ToUpper(strings.TrimSpace(currency))
	return c == "" || c == "USD" || c == "USDC"
}

func minDecimal(values ...decimal.Decimal) decimal.Decimal {
	if len(values) == 0 {
		return decimal.Zero
	}
	min := values[0]
	for _, value := range values[1:] {
		if value.LessThan(min) {
			min = value
		}
	}
	return min
}

func maxDecimal(a, b decimal.Decimal) decimal.Decimal {
	if a.GreaterThan(b) {
		return a
	}
	return b
}

func mustJSON(v interface{}) json.RawMessage {
	if v == nil {
		return json.RawMessage(`{}`)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}

func receiptIDFromSignal(receiptID uuid.UUID, signal string) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("miriam-learning:"+receiptID.String()+":"+signal+":"+time.Now().UTC().Format(time.RFC3339Nano)))
}
