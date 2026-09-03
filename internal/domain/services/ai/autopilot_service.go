package ai

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// --- Phase-specific interfaces ---

type AutopilotUserLister interface {
	GetAllActiveUsers(ctx context.Context) ([]struct {
		ID      uuid.UUID
		Country string
	}, error)
}

type MorningSpender interface {
	GetMoneyFlow(ctx context.Context, userID uuid.UUID, start, end time.Time) (*entities.MoneyFlowSummary, error)
}

type AutopilotBalanceReader interface {
	GetAccountBalance(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (decimal.Decimal, error)
}

type MiddayBudgetReader interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.SpendingBudget, error)
}

type ControlLevelReader interface {
	GetControlLevel(ctx context.Context, userID uuid.UUID) (string, error)
}

type MorningPushSender interface {
	SendToUser(ctx context.Context, userID uuid.UUID, title, body string, data map[string]interface{}) error
}

type AutopilotFundsTransferer interface {
	TransferSpendToStash(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, idempotencyKey string) error
	GetSpendBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error)
}

type AnomalyRunner interface {
	RunAllChecks(ctx context.Context, userID uuid.UUID, now time.Time) []AnomalyResult
}

// AutopilotEventDispatcher sends orchestrator-eligible events when the autopilot
// detects something worth acting on (anomalies, income changes, etc.). The
// implementation bridges to the intelligence orchestrator for mandate evaluation.
type AutopilotEventDispatcher interface {
	DispatchEvent(ctx context.Context, userID uuid.UUID, eventType string) error
}

// AutopilotMetrics exposes counters for monitoring.
type AutopilotMetrics struct {
	MorningAlertsSent     int64
	MiddayActionsQueued   int64
	EveningTransfersDone  int64
	EveningAlertsReported int64
	EveningErrors         int64
}

// AutopilotService runs the 3-phase daily autopilot loop for Full Autopilot users.
type AutopilotService struct {
	users      AutopilotUserLister
	control    ControlLevelReader
	queue      AutopilotQueue
	push       MorningPushSender
	spending   MorningSpender
	balances   AutopilotBalanceReader
	budgets    MiddayBudgetReader
	transferer AutopilotFundsTransferer
	anomaly      AnomalyRunner
	anomalyStore AnomalyStore
	eventDispatch AutopilotEventDispatcher
	logger       *zap.Logger

	redis    cache.RedisClient
	sentDate map[string]string

	// Configurable thresholds (USD-denominated; scaled per user by country PPP
	// factor so NGN/EUR/GBP users get locally meaningful "low balance" and
	// "surplus" cutoffs instead of values off by an order of magnitude).
	OvernightAnomalyThreshold decimal.Decimal
	LowBalanceThreshold       decimal.Decimal
	SurplusMinThreshold       decimal.Decimal

	metrics AutopilotMetrics
}

// countryPPPFactor approximates purchasing-power parity vs USD: how much local
// currency one US dollar's spending power represents. Thresholds denominated in
// USD are divided by this factor to get a locally meaningful cutoff. Countries
// not listed fall back to 1.0 (treat as USD). These are deliberately rough —
// they only steer alert sensitivity, never move money amounts.
var countryPPPFactor = map[string]float64{
	"NG": 1500, // Nigerian naira
	"KE": 130,  // Kenyan shilling
	"GH": 12,   // Ghanaian cedi
	"ZA": 18,   // South African rand
	"EG": 48,   // Egyptian pound
	"TZ": 2500, // Tanzanian shilling
	"UG": 3700, // Ugandan shilling
	"IN": 83,   // Indian rupee
	"PH": 56,   // Philippine peso
	"BR": 5,    // Brazilian real
	"MX": 17,   // Mexican peso
	"GB": 0.79, // British pound
	"IE": 0.92, // Euro (Ireland)
	"DE": 0.92, // Euro
	"FR": 0.92, // Euro
	"ES": 0.92, // Euro
	"NL": 0.92, // Euro
	"CA": 1.35, // Canadian dollar
	"AU": 1.5,  // Australian dollar
}

// scaledThreshold converts a USD-denominated threshold into a user's local
// equivalent via their country's PPP factor. Unknown/empty country => factor 1.
func scaledThreshold(usd decimal.Decimal, country string) decimal.Decimal {
	factor := countryPPPFactor[strings.ToUpper(strings.TrimSpace(country))]
	if factor <= 0 {
		factor = 1
	}
	if factor == 1 {
		return usd
	}
	return usd.Mul(decimal.NewFromFloat(factor))
}

// SetEventDispatcher wires the event dispatcher so the autopilot can trigger
// orchestrator evaluations when anomalies are detected.
func (s *AutopilotService) SetEventDispatcher(d AutopilotEventDispatcher) {
	s.eventDispatch = d
}

func NewAutopilotService(
	users AutopilotUserLister,
	control ControlLevelReader,
	queue AutopilotQueue,
	push MorningPushSender,
	spending MorningSpender,
	balances AutopilotBalanceReader,
	budgets MiddayBudgetReader,
	transferer AutopilotFundsTransferer,
	redis cache.RedisClient,
	logger *zap.Logger,
	anomaly AnomalyRunner,
	store AnomalyStore,
) *AutopilotService {
	return &AutopilotService{
		users:      users,
		control:    control,
		queue:      queue,
		push:       push,
		spending:   spending,
		balances:   balances,
		budgets:    budgets,
		transferer: transferer,
		anomaly:    anomaly,
		anomalyStore: store,
		redis:      redis,
		logger:     logger,
		sentDate:   make(map[string]string),

		OvernightAnomalyThreshold: decimal.NewFromInt(500),
		LowBalanceThreshold:       decimal.NewFromInt(20),
		SurplusMinThreshold:       decimal.NewFromInt(50),
	}
}

func (s *AutopilotService) Metrics() AutopilotMetrics {
	return AutopilotMetrics{
		MorningAlertsSent:     atomic.LoadInt64(&s.metrics.MorningAlertsSent),
		MiddayActionsQueued:   atomic.LoadInt64(&s.metrics.MiddayActionsQueued),
		EveningTransfersDone:  atomic.LoadInt64(&s.metrics.EveningTransfersDone),
		EveningAlertsReported: atomic.LoadInt64(&s.metrics.EveningAlertsReported),
		EveningErrors:         atomic.LoadInt64(&s.metrics.EveningErrors),
	}
}

// RunMorningScan checks overnight activity and alerts on anomalies.
// Alerts go to full and guided users (monitor is alerts-only product-wise but we
// still surface risk). No money moves here.
func (s *AutopilotService) RunMorningScan(ctx context.Context) {
	if !s.tryLockPhase(ctx, "morning") {
		s.logger.Debug("autopilot morning: lock held by another replica")
		return
	}
	defer s.unlockPhase(ctx, "morning")

	// Morning is alerts-only: include full + guided so default guided users still
	// get risk signals. Monitor users opt out of autonomous action, not safety pings.
	users := s.loadUsersAtLevels(ctx, entities.ControlLevelFull, entities.ControlLevelGuided)
	if len(users) == 0 {
		return
	}

	now := time.Now().UTC()
	today := now.Format("2006-01-02")
	since := now.Add(-24 * time.Hour)
	var alerted int

	for _, u := range users {
		if s.alreadySent(ctx, u.ID, "morning", today) {
			continue
		}

		var results []AnomalyResult

		results = s.scanOvernightForAnomalies(ctx, u.ID, u.Country, since, now)

		if s.anomaly != nil {
			results = append(results, s.anomaly.RunAllChecks(ctx, u.ID, now)...)
		}

		if len(results) > 0 {
			// Persist to anomaly store so Miriam can answer "what was that anomaly?"
			if s.anomalyStore != nil {
				_ = s.anomalyStore.Set(ctx, u.ID, results, 24*time.Hour)
			}
			title, body := BuildAlertText(results)
			data := make([]map[string]any, len(results))
			for i, r := range results {
				data[i] = map[string]any{
					"type":        string(r.Type),
					"severity":    string(r.Severity),
					"title":       r.Title,
					"description": r.Description,
				}
			}
			if err := s.push.SendToUser(ctx, u.ID, title, body, map[string]interface{}{
				"type":    "autopilot_morning",
				"results": data,
			}); err != nil {
				s.logger.Warn("autopilot morning: push failed", zap.String("user_id", u.ID.String()), zap.Error(err))
				continue
			}
			// Dispatch spending_spike event to the orchestrator so mandate-capable
			// users get automatic bill-pay / surplus-sweep evaluations.
			if s.eventDispatch != nil {
				for _, r := range results {
					if r.Type == AnomalyBillSpike || r.Type == AnomalySpendingAccel {
						if err := s.eventDispatch.DispatchEvent(ctx, u.ID, "spending_spike"); err != nil {
							s.logger.Debug("autopilot: dispatch spending_spike failed", zap.Error(err))
						}
						break
					}
				}
			}
			alerted++
			atomic.AddInt64(&s.metrics.MorningAlertsSent, 1)
		}
		s.markSent(ctx, u.ID, "morning", today)
	}

	s.logger.Info("autopilot morning scan complete", zap.Int("users", len(users)), zap.Int("alerted", alerted))
}

// RunMiddayCheck reviews spending pace vs budget and queues surplus/overspend alerts.
// Money is not moved here — silent transfers require Act (full) + an active mandate
// via the intelligence orchestrator. Autopilot only surfaces suggestions.
func (s *AutopilotService) RunMiddayCheck(ctx context.Context) {
	if !s.tryLockPhase(ctx, "midday") {
		s.logger.Debug("autopilot midday: lock held by another replica")
		return
	}
	defer s.unlockPhase(ctx, "midday")

	// Full users get surplus suggestions; guided get overspend alerts only via morning
	// and here for budget path. Use full for surplus CTAs that used to auto-transfer.
	users := s.loadUsersAtLevels(ctx, entities.ControlLevelFull, entities.ControlLevelGuided)
	if len(users) == 0 {
		return
	}

	now := time.Now().UTC()
	today := now.Format("2006-01-02")
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	var queued int

	for _, u := range users {
		if s.alreadySent(ctx, u.ID, "midday", today) {
			continue
		}

		budget, err := s.budgets.GetByUserID(ctx, u.ID)
		if err != nil || budget == nil {
			continue
		}

		spend, err := s.spending.GetMoneyFlow(ctx, u.ID, monthStart, now)
		if err != nil || spend == nil {
			continue
		}

		spentSoFar := spend.TotalCardSpend.Add(spend.TotalP2P)
		daysElapsed := int(now.Sub(monthStart).Hours() / 24)
		if daysElapsed < 1 {
			daysElapsed = 1
		}
		projectedDaily := spentSoFar.Div(decimal.NewFromInt(int64(daysElapsed)))
		daysLeft := int(time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, time.UTC).Sub(now).Hours() / 24)
		if daysLeft < 1 {
			daysLeft = 1
		}
		projectedTotal := spentSoFar.Add(projectedDaily.Mul(decimal.NewFromInt(int64(daysLeft))))

		if projectedTotal.GreaterThan(budget.MonthlyLimit) {
			excess := projectedTotal.Sub(budget.MonthlyLimit)
			_ = s.queue.Push(ctx, u.ID, AutopilotQueueAction{
				Tool:   "alert_overspend",
				Reason: fmt.Sprintf("On track to exceed budget by %s this month", excess.StringFixed(2)),
			})
			queued++
			s.markSent(ctx, u.ID, "midday", today)
			continue
		}

		remaining := budget.MonthlyLimit.Sub(projectedTotal)
		surplusThreshold := scaledThreshold(s.SurplusMinThreshold, u.Country)
		if remaining.GreaterThan(surplusThreshold) {
			balance, err := s.balances.GetAccountBalance(ctx, u.ID, entities.AccountTypeSpendingBalance)
			if err != nil {
				continue
			}
			surplus := decimal.Min(remaining, balance)
			if surplus.GreaterThan(surplusThreshold) {
				// Alert only — do not auto-transfer. Mandate path owns silent moves.
				_ = s.queue.Push(ctx, u.ID, AutopilotQueueAction{
					Tool: "alert_surplus",
					Reason: fmt.Sprintf(
						"You have about $%s spare under budget. I can quietly move surplus to Stash when you accept a mandate and turn on Act.",
						surplus.StringFixed(2),
					),
					Args: map[string]interface{}{
						"from":   "spend",
						"to":     "stash",
						"amount": surplus.InexactFloat64(),
					},
				})
				queued++
			}
		}
		s.markSent(ctx, u.ID, "midday", today)
	}

	atomic.AddInt64(&s.metrics.MiddayActionsQueued, int64(queued))
	s.logger.Info("autopilot midday check complete", zap.Int("users", len(users)), zap.Int("queued", queued))
}

// RunEveningReview executes queued actions and sends the daily summary.
func (s *AutopilotService) RunEveningReview(ctx context.Context) {
	if !s.tryLockPhase(ctx, "evening") {
		s.logger.Debug("autopilot evening: lock held by another replica")
		return
	}
	defer s.unlockPhase(ctx, "evening")

	users := s.loadFullAutopilotUsers(ctx)
	if len(users) == 0 {
		return
	}

	now := time.Now().UTC()
	today := now.Format("2006-01-02")
	var executed, alerted int

	for _, u := range users {
		if s.alreadySent(ctx, u.ID, "evening", today) {
			continue
		}

		actions, err := s.queue.List(ctx, u.ID)
		if err != nil {
			s.logger.Warn("autopilot evening: list actions failed", zap.String("user_id", u.ID.String()), zap.Error(err))
			continue
		}
		if len(actions) == 0 {
			s.markSent(ctx, u.ID, "evening", today)
			continue
		}

		var summary []string
		var hasError bool

		for range actions {
			act, popErr := s.queue.Pop(ctx, u.ID)
			if popErr != nil {
				s.logger.Warn("autopilot evening: pop failed", zap.String("user_id", u.ID.String()), zap.Error(popErr))
				continue
			}
			if act == nil {
				continue
			}

			switch act.Tool {
			case ToolTransferFunds:
				// For full-control users, dispatch to the orchestrator so queued
				// surplus transfers get evaluated through the mandate pipeline
				// (decision engine, cooldown, balance floor, day cap). For others,
				// surface as a suggestion.
				amount, _ := act.Args["amount"].(float64)
				if s.eventDispatch != nil && amount > 0 {
					if err := s.eventDispatch.DispatchEvent(ctx, u.ID, "worker_sweep"); err != nil {
						s.logger.Debug("autopilot evening: dispatch failed", zap.String("user_id", u.ID.String()), zap.Error(err))
						summary = append(summary, fmt.Sprintf(
							"I tried to act on $%.2f surplus but the system held back: %v",
							amount, err,
						))
					} else {
						summary = append(summary, fmt.Sprintf(
							"Evaluated $%.2f surplus through the mandate pipeline",
							amount,
						))
						executed++
					}
				} else {
					alerted++
					if amount > 0 {
						summary = append(summary, fmt.Sprintf(
							"I held off on moving $%.2f to Stash — silent moves need an approved mandate and Act mode",
							amount,
						))
					} else {
						summary = append(summary, "I held a pending surplus transfer until you approve a mandate")
					}
				}

			case "alert_overspend", "alert_surplus":
				alerted++
				summary = append(summary, act.Reason)
			}
		}

		if len(summary) > 0 {
			body := "Today's autopilot summary:"
			for _, line := range summary {
				body += "\n" + line
			}
			if !hasError {
				body += "\n\nNo issues. You're on track."
			}
			if err := s.push.SendToUser(ctx, u.ID, "Miriam Autopilot", body, map[string]interface{}{
				"type":     "autopilot_evening",
				"summary":  summary,
				"executed": executed,
			}); err != nil {
				s.logger.Warn("autopilot evening: push failed", zap.String("user_id", u.ID.String()), zap.Error(err))
			}
		}

		s.markSent(ctx, u.ID, "evening", today)
	}

	atomic.AddInt64(&s.metrics.EveningAlertsReported, int64(alerted))
	s.logger.Info("autopilot evening review complete", zap.Int("users", len(users)), zap.Int("executed", executed), zap.Int("alerted", alerted))
}

// --- helpers ---

func (s *AutopilotService) loadFullAutopilotUsers(ctx context.Context) []struct {
	ID      uuid.UUID
	Country string
} {
	return s.loadUsersAtLevels(ctx, entities.ControlLevelFull)
}

// loadUsersAtLevels returns active users whose control level is in the allow-list.
// Fail-closed: lookup errors skip the user.
func (s *AutopilotService) loadUsersAtLevels(ctx context.Context, levels ...string) []struct {
	ID      uuid.UUID
	Country string
} {
	all, err := s.users.GetAllActiveUsers(ctx)
	if err != nil {
		s.logger.Error("autopilot: list users failed", zap.Error(err))
		return nil
	}

	allowed := make(map[string]bool, len(levels))
	for _, l := range levels {
		allowed[l] = true
	}

	filtered := all[:0]
	for _, u := range all {
		level, err := s.control.GetControlLevel(ctx, u.ID)
		if err != nil || !allowed[level] {
			continue
		}
		filtered = append(filtered, u)
	}
	return filtered
}

func (s *AutopilotService) scanOvernightForAnomalies(ctx context.Context, userID uuid.UUID, country string, since, now time.Time) []AnomalyResult {
	var results []AnomalyResult

	overnightThreshold := scaledThreshold(s.OvernightAnomalyThreshold, country)
	lowBalanceThreshold := scaledThreshold(s.LowBalanceThreshold, country)

	flow, err := s.spending.GetMoneyFlow(ctx, userID, since, now)
	if err != nil || flow == nil {
		return nil
	}

	if flow.TotalCardSpend.GreaterThan(overnightThreshold) {
		results = append(results, AnomalyResult{
			Type:        "overnight_spend",
			Severity:    SeverityHigh,
			Title:       "High overnight spend",
			Description: fmt.Sprintf("High overnight spend: $%s in card transactions", flow.TotalCardSpend.StringFixed(2)),
			Details: map[string]any{
				"amount": flow.TotalCardSpend.StringFixed(2),
			},
			DetectedAt: now,
		})
	}

	balance, err := s.balances.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	if err == nil && balance.LessThan(lowBalanceThreshold) {
		results = append(results, AnomalyResult{
			Type:        "low_balance",
			Severity:    SeverityHigh,
			Title:       "Low spend balance",
			Description: fmt.Sprintf("Spend balance low: $%s", balance.StringFixed(2)),
			Details: map[string]any{
				"balance": balance.StringFixed(2),
			},
			DetectedAt: now,
		})
	}

	return results
}

const sentinelPrefix = "autopilot:sent:"
const lockPrefix = "autopilot:lock:"

func (s *AutopilotService) markSent(ctx context.Context, userID uuid.UUID, phase, date string) {
	if s.redis != nil {
		key := sentinelPrefix + userID.String() + ":" + phase + ":" + date
		_ = s.redis.Set(ctx, key, "1", 48*time.Hour)
		return
	}
	s.sentDate[userID.String()+":"+phase+":"+date] = date
}

func (s *AutopilotService) alreadySent(ctx context.Context, userID uuid.UUID, phase, date string) bool {
	if s.redis != nil {
		key := sentinelPrefix + userID.String() + ":" + phase + ":" + date
		exists, err := s.redis.Exists(ctx, key)
		if err != nil {
			s.logger.Warn("autopilot: alreadySent check failed", zap.String("key", key), zap.Error(err))
			return false
		}
		return exists
	}
	_, ok := s.sentDate[userID.String()+":"+phase+":"+date]
	return ok
}

func (s *AutopilotService) tryLockPhase(ctx context.Context, phase string) bool {
	if s.redis == nil {
		return true
	}
	key := lockPrefix + phase + ":" + time.Now().UTC().Format("2006-01-02")
	ok, err := s.redis.SetNX(ctx, key, "1", 3*time.Hour)
	if err != nil {
		s.logger.Warn("autopilot: lock acquire failed", zap.String("key", key), zap.Error(err))
		return true
	}
	return ok
}

func (s *AutopilotService) unlockPhase(ctx context.Context, phase string) {
	if s.redis == nil {
		return
	}
	key := lockPrefix + phase + ":" + time.Now().UTC().Format("2006-01-02")
	_ = s.redis.Del(ctx, key)
}
