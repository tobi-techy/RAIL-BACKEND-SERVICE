package ai

import (
	"context"
	"fmt"
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
	logger       *zap.Logger

	redis    cache.RedisClient
	sentDate map[string]string

	// Configurable thresholds.
	OvernightAnomalyThreshold decimal.Decimal
	LowBalanceThreshold       decimal.Decimal
	SurplusMinThreshold       decimal.Decimal

	metrics AutopilotMetrics
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
func (s *AutopilotService) RunMorningScan(ctx context.Context) {
	if !s.tryLockPhase(ctx, "morning") {
		s.logger.Debug("autopilot morning: lock held by another replica")
		return
	}
	defer s.unlockPhase(ctx, "morning")

	users := s.loadFullAutopilotUsers(ctx)
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

		results = s.scanOvernightForAnomalies(ctx, u.ID, since, now)

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
			alerted++
			atomic.AddInt64(&s.metrics.MorningAlertsSent, 1)
		}
		s.markSent(ctx, u.ID, "morning", today)
	}

	s.logger.Info("autopilot morning scan complete", zap.Int("users", len(users)), zap.Int("alerted", alerted))
}

// RunMiddayCheck reviews spending pace vs budget and queues surplus transfers.
func (s *AutopilotService) RunMiddayCheck(ctx context.Context) {
	if !s.tryLockPhase(ctx, "midday") {
		s.logger.Debug("autopilot midday: lock held by another replica")
		return
	}
	defer s.unlockPhase(ctx, "midday")

	users := s.loadFullAutopilotUsers(ctx)
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
		if remaining.GreaterThan(s.SurplusMinThreshold) {
			balance, err := s.balances.GetAccountBalance(ctx, u.ID, entities.AccountTypeSpendingBalance)
			if err != nil {
				continue
			}
			surplus := decimal.Min(remaining, balance)
			if surplus.GreaterThan(s.SurplusMinThreshold) {
				_ = s.queue.Push(ctx, u.ID, AutopilotQueueAction{
					Tool:   ToolTransferFunds,
					Reason: fmt.Sprintf("Surplus detected: %s under budget this month", remaining.StringFixed(2)),
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
				amount, _ := act.Args["amount"].(float64)
				from, _ := act.Args["from"].(string)
				to, _ := act.Args["to"].(string)
				if amount <= 0 {
					continue
				}
				idemKey := fmt.Sprintf("autopilot:%s:%s:transfer:%.2f", u.ID.String(), today, amount)
				var txErr error
				if from == "spend" && to == "stash" {
					txErr = s.transferer.TransferSpendToStash(ctx, u.ID, decimal.NewFromFloat(amount), idemKey)
				}
				if txErr != nil {
					s.logger.Warn("autopilot evening: transfer failed", zap.String("user_id", u.ID.String()), zap.Error(txErr))
					hasError = true
					atomic.AddInt64(&s.metrics.EveningErrors, 1)
					summary = append(summary, fmt.Sprintf("Transfer $%.2f failed — will retry tomorrow", amount))
					if requeueErr := s.queue.Push(ctx, u.ID, *act); requeueErr != nil {
						s.logger.Warn("autopilot evening: requeue failed", zap.String("user_id", u.ID.String()), zap.Error(requeueErr))
					}
				} else {
					executed++
					atomic.AddInt64(&s.metrics.EveningTransfersDone, 1)
					summary = append(summary, fmt.Sprintf("Moved $%.2f to savings", amount))
				}

			case "alert_overspend":
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
	all, err := s.users.GetAllActiveUsers(ctx)
	if err != nil {
		s.logger.Error("autopilot: list users failed", zap.Error(err))
		return nil
	}

	filtered := all[:0]
	for _, u := range all {
		level, err := s.control.GetControlLevel(ctx, u.ID)
		if err != nil || level != entities.ControlLevelFull {
			continue
		}
		filtered = append(filtered, u)
	}
	return filtered
}

func (s *AutopilotService) scanOvernightForAnomalies(ctx context.Context, userID uuid.UUID, since, now time.Time) []AnomalyResult {
	var results []AnomalyResult

	flow, err := s.spending.GetMoneyFlow(ctx, userID, since, now)
	if err != nil || flow == nil {
		return nil
	}

	if flow.TotalCardSpend.GreaterThan(s.OvernightAnomalyThreshold) {
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
	if err == nil && balance.LessThan(s.LowBalanceThreshold) {
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
