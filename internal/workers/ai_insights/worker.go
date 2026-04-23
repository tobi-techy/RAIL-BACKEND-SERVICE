package ai_insights

import (
	"context"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// UserRepo fetches active users.
type UserRepo interface {
	GetAllActiveUsers(ctx context.Context) ([]repositories.NotificationUser, error)
}

// PushSender sends push notifications.
type PushSender interface {
	SendToUser(ctx context.Context, userID uuid.UUID, title, body string, data map[string]interface{}) error
}

// CooldownStore provides distributed deduplication for alerts.
type CooldownStore interface {
	SetNX(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.BoolCmd
}

// SpendingRepo provides spending data for alerts.
type SpendingRepo interface {
	GetSpendingTotal(ctx context.Context, userID uuid.UUID, start, end time.Time) (decimal.Decimal, int, error)
}

// BudgetProvider provides monthly budget data for pace alerts.
type BudgetProvider interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.SpendingBudget, error)
}

// BalanceProvider provides current balances.
type BalanceProvider interface {
	GetAccountBalance(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (decimal.Decimal, error)
}

// SubscriptionChecker checks if a user has Pro.
type SubscriptionChecker interface {
	IsProUser(ctx context.Context, userID uuid.UUID) (bool, error)
}

// Worker runs periodic proactive insight checks and sends push notifications.
type Worker struct {
	userRepo      UserRepo
	pushSender    PushSender
	cooldowns     CooldownStore
	spendingRepo  SpendingRepo
	budgets       BudgetProvider
	balances      BalanceProvider
	subChecker    SubscriptionChecker
	logger        *zap.Logger
	lastDigestDay int // day of year when last digest ran
}

func NewWorker(userRepo UserRepo, pushSender PushSender, cooldowns CooldownStore, spendingRepo SpendingRepo, budgets BudgetProvider, balances BalanceProvider, subChecker SubscriptionChecker, logger *zap.Logger) *Worker {
	return &Worker{
		userRepo:     userRepo,
		pushSender:   pushSender,
		cooldowns:    cooldowns,
		spendingRepo: spendingRepo,
		budgets:      budgets,
		balances:     balances,
		subChecker:   subChecker,
		logger:       logger,
	}
}

// Start runs the insight check loop. Checks hourly.
func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("AI insights worker started")
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("AI insights worker stopped")
			return
		case t := <-ticker.C:
			hour := t.UTC().Hour()
			weekday := t.UTC().Weekday()
			day := t.UTC().Day()
			lastDay := time.Date(t.Year(), t.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()

			// Morning greeting — runs every hour, sends only to users in their local 7am window
			w.runMorningGreeting(ctx)

			// Daily insights at 9am UTC (10am WAT)
			if hour == 9 {
				w.runDailyInsights(ctx)
			}

			// Month-end recap on last day of month at 6pm UTC
			if day == lastDay && hour == 18 {
				w.runMonthRecap(ctx)
			}

			// Weekly digest on Mondays at 8am UTC
			if weekday == time.Monday && hour == 8 && t.UTC().YearDay() != w.lastDigestDay {
				w.lastDigestDay = t.UTC().YearDay()
				w.runWeeklyDigest(ctx)
			}
		}
	}
}

func (w *Worker) runDailyInsights(ctx context.Context) {
	users, err := w.userRepo.GetAllActiveUsers(ctx)
	if err != nil {
		w.logger.Error("failed to get active users", zap.Error(err))
		return
	}

	w.logger.Info("Running daily insights", zap.Int("users", len(users)))
	for _, u := range users {
		if ctx.Err() != nil {
			return
		}
		userCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		w.checkSpendingAlert(userCtx, u.ID)
		w.checkBudgetPace(userCtx, u.ID)
		w.checkCashRunway(userCtx, u.ID)
		w.checkSavingsGrowth(userCtx, u.ID)
		w.checkIdleMoney(userCtx, u.ID)
		cancel()
	}
}

func (w *Worker) sendAlertOnce(ctx context.Context, key string, ttl time.Duration, fn func() error) bool {
	if w.cooldowns != nil {
		ok, err := w.cooldowns.SetNX(ctx, key, "1", ttl).Result()
		if err != nil {
			w.logger.Debug("alert cooldown check failed", zap.String("key", key), zap.Error(err))
			return false
		}
		if !ok {
			return false
		}
	}
	if err := fn(); err != nil {
		w.logger.Warn("failed to send alert", zap.String("key", key), zap.Error(err))
		return false
	}
	return true
}

func alertBucket(value decimal.Decimal, thresholds ...decimal.Decimal) string {
	for i, threshold := range thresholds {
		if value.LessThanOrEqual(threshold) {
			return fmt.Sprintf("tier_%d", i+1)
		}
	}
	return fmt.Sprintf("tier_%d", len(thresholds)+1)
}

// checkSpendingAlert compares this week's spending to last week's.
func (w *Worker) checkSpendingAlert(ctx context.Context, userID uuid.UUID) {
	now := time.Now().UTC()
	thisWeekStart := now.AddDate(0, 0, -7)
	lastWeekStart := now.AddDate(0, 0, -14)

	thisWeek, _, err := w.spendingRepo.GetSpendingTotal(ctx, userID, thisWeekStart, now)
	if err != nil || thisWeek.IsZero() {
		return
	}

	lastWeek, _, err := w.spendingRepo.GetSpendingTotal(ctx, userID, lastWeekStart, thisWeekStart)
	if err != nil || lastWeek.IsZero() {
		return
	}

	ratio := thisWeek.Div(lastWeek)

	if ratio.GreaterThan(decimal.NewFromFloat(1.5)) {
		bucket := alertBucket(ratio, decimal.NewFromFloat(1.75), decimal.NewFromFloat(2.5))
		pctIncrease := ratio.Sub(decimal.NewFromInt(1)).Mul(decimal.NewFromInt(100)).StringFixed(0)
		key := fmt.Sprintf("ai-insights:spending:%s:%s:%s", userID.String(), thisWeekStart.Format("2006-01-02"), bucket)
		_ = w.sendAlertOnce(ctx, key, 7*24*time.Hour, func() error {
			return w.pushSender.SendToUser(ctx, userID,
				"Spending Alert",
				"You've spent "+pctIncrease+"% more this week than last week. Tap to see where your money went.",
				map[string]interface{}{
					"type":         "spending_alert",
					"action":       "open_chat",
					"severity":     bucket,
					"data_used":    []string{"weekly_spending_comparison"},
					"why":          "This week vs last week spend crossed the rule threshold.",
					"pct_increase": pctIncrease,
				},
			)
		})
	}
}

// checkBudgetPace warns users before they exceed their monthly budget.
func (w *Worker) checkBudgetPace(ctx context.Context, userID uuid.UUID) {
	if w.budgets == nil {
		return
	}
	budget, err := w.budgets.GetByUserID(ctx, userID)
	if err != nil || budget == nil || !budget.MonthlyLimit.IsPositive() {
		return
	}

	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	lastDay := time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
	spent, _, err := w.spendingRepo.GetSpendingTotal(ctx, userID, monthStart, now)
	if err != nil || spent.IsZero() {
		return
	}

	actualPct := spent.Div(budget.MonthlyLimit).Mul(decimal.NewFromInt(100))
	expectedPct := decimal.NewFromInt(int64(now.Day())).Div(decimal.NewFromInt(int64(lastDay))).Mul(decimal.NewFromInt(100))
	if actualPct.GreaterThan(expectedPct.Add(decimal.NewFromInt(20))) && actualPct.GreaterThan(decimal.NewFromInt(50)) {
		bucket := alertBucket(actualPct.Sub(expectedPct), decimal.NewFromInt(20), decimal.NewFromInt(40))
		remaining := budget.MonthlyLimit.Sub(spent)
		displayRemaining := remaining
		if displayRemaining.IsNegative() {
			displayRemaining = decimal.Zero
		}
		body := fmt.Sprintf("You've used %s%% of your monthly budget by day %d. $%s left for the month.",
			actualPct.StringFixed(0), now.Day(), displayRemaining.StringFixed(2))
		key := fmt.Sprintf("ai-insights:budget-pace:%s:%s:%s", userID.String(), monthStart.Format("2006-01"), bucket)
		_ = w.sendAlertOnce(ctx, key, 7*24*time.Hour, func() error {
			return w.pushSender.SendToUser(ctx, userID,
				"Budget pace alert",
				body,
				map[string]interface{}{
					"type":         "budget_pace_alert",
					"action":       "open_chat",
					"severity":     bucket,
					"why":          "Budget usage is moving faster than the month calendar.",
					"data_used":    []string{"monthly_budget", "month_spending"},
					"remaining":    displayRemaining.StringFixed(2),
					"percent_used": actualPct.StringFixed(0),
				},
			)
		})
	}
}

// checkCashRunway warns when spend balance may not last the week at current pace.
func (w *Worker) checkCashRunway(ctx context.Context, userID uuid.UUID) {
	spendBalance, err := w.balances.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil || spendBalance.IsZero() {
		return
	}
	now := time.Now().UTC()
	weekAgo := now.AddDate(0, 0, -7)
	spent, txCount, err := w.spendingRepo.GetSpendingTotal(ctx, userID, weekAgo, now)
	if err != nil || txCount < 3 || spent.IsZero() {
		return
	}
	dailyBurn := spent.Div(decimal.NewFromInt(7))
	if dailyBurn.IsZero() {
		return
	}
	runwayDays := spendBalance.Div(dailyBurn)
	if runwayDays.LessThan(decimal.NewFromInt(7)) {
		bucket := alertBucket(runwayDays, decimal.NewFromInt(3), decimal.NewFromInt(7))
		key := fmt.Sprintf("ai-insights:runway:%s:%s:%s", userID.String(), weekAgo.Format("2006-01-02"), bucket)
		_ = w.sendAlertOnce(ctx, key, 7*24*time.Hour, func() error {
			return w.pushSender.SendToUser(ctx, userID,
				"Spend balance may run low",
				fmt.Sprintf("At your current $%s/day pace, your Spend balance may last about %s days.",
					dailyBurn.StringFixed(2), runwayDays.StringFixed(0)),
				map[string]interface{}{
					"type":        "cash_runway_alert",
					"action":      "open_chat",
					"severity":    bucket,
					"why":         "Spend balance runway fell below the warning threshold.",
					"data_used":   []string{"spend_balance", "weekly_spending"},
					"runway_days": runwayDays.StringFixed(0),
				},
			)
		})
	}
}

// checkSavingsGrowth sends an encouraging notification when stash grows.
func (w *Worker) checkSavingsGrowth(ctx context.Context, userID uuid.UUID) {
	balance, err := w.balances.GetAccountBalance(ctx, userID, entities.AccountTypeStashBalance)
	if err != nil || balance.IsZero() {
		return
	}

	// Milestone notifications at $100, $500, $1000, $5000
	milestones := []decimal.Decimal{
		decimal.NewFromInt(100),
		decimal.NewFromInt(500),
		decimal.NewFromInt(1000),
		decimal.NewFromInt(5000),
	}

	for _, m := range milestones {
		if balance.GreaterThanOrEqual(m) && balance.Sub(m).LessThan(decimal.NewFromInt(10)) {
			key := fmt.Sprintf("ai-insights:savings-milestone:%s:%s", userID.String(), m.String())
			_ = w.sendAlertOnce(ctx, key, 30*24*time.Hour, func() error {
				return w.pushSender.SendToUser(ctx, userID,
					"Savings Milestone!",
					"Your stash just crossed $"+m.String()+". Your money is working for you.",
					map[string]interface{}{
						"type":      "savings_milestone",
						"amount":    m.String(),
						"data_used": []string{"stash_balance"},
					},
				)
			})
			return
		}
	}
}

// runWeeklyDigest sends a weekly financial summary to each user.
func (w *Worker) runWeeklyDigest(ctx context.Context) {
	w.logger.Info("Running weekly digest")

	users, err := w.userRepo.GetAllActiveUsers(ctx)
	if err != nil {
		w.logger.Error("weekly digest: failed to get users", zap.Error(err))
		return
	}

	now := time.Now().UTC()
	weekStart := now.AddDate(0, 0, -7)

	for _, u := range users {
		if ctx.Err() != nil {
			return
		}

		// Weekly report is Pro-only
		if w.subChecker != nil {
			isPro, _ := w.subChecker.IsProUser(ctx, u.ID)
			if !isPro {
				continue
			}
		}

		spent, txCount, err := w.spendingRepo.GetSpendingTotal(ctx, u.ID, weekStart, now)
		if err != nil {
			continue
		}

		stash, err := w.balances.GetAccountBalance(ctx, u.ID, entities.AccountTypeStashBalance)
		if err != nil {
			continue
		}

		spend, err := w.balances.GetAccountBalance(ctx, u.ID, entities.AccountTypeSpendingBalance)
		if err != nil {
			continue
		}

		total := spend.Add(stash)
		if total.IsZero() && spent.IsZero() {
			continue // Skip inactive users
		}

		body := "This week: "
		if txCount > 0 {
			body += "$" + spent.StringFixed(2) + " spent across " + fmt.Sprintf("%d", txCount) + " transactions. "
		} else {
			body += "No spending this week. "
		}
		body += "Stash: $" + stash.StringFixed(2) + ". Total: $" + total.StringFixed(2) + "."

		isoYear, isoWeek := now.ISOWeek()
		key := fmt.Sprintf("ai-insights:weekly-digest:%s:%04d-w%02d", u.ID.String(), isoYear, isoWeek)
		_ = w.sendAlertOnce(ctx, key, 8*24*time.Hour, func() error {
			return w.pushSender.SendToUser(ctx, u.ID,
				"Your Weekly Money Recap",
				body,
				map[string]interface{}{
					"type":      "weekly_digest",
					"action":    "open_chat",
					"data_used": []string{"weekly_spending", "stash_balance", "spend_balance"},
				},
			)
		})
	}

	w.logger.Info("Weekly digest complete", zap.Int("users", len(users)))
}

// runMorningGreeting sends a brief morning balance update.
// Only sends to users whose local time is between 7-8am.
func (w *Worker) runMorningGreeting(ctx context.Context) {
	users, err := w.userRepo.GetAllActiveUsers(ctx)
	if err != nil {
		return
	}

	nowUTC := time.Now().UTC()
	for _, u := range users {
		if ctx.Err() != nil {
			return
		}
		// Check if it's morning (7-8am) in the user's local timezone
		if !isLocalMorning(u.Country, nowUTC) {
			continue
		}
		spend, err := w.balances.GetAccountBalance(ctx, u.ID, entities.AccountTypeSpendingBalance)
		if err != nil || spend.IsZero() {
			continue
		}
		stash, _ := w.balances.GetAccountBalance(ctx, u.ID, entities.AccountTypeStashBalance)

		body := fmt.Sprintf("You have $%s to spend today. Stash: $%s and growing 📈", spend.StringFixed(2), stash.StringFixed(2))

		key := fmt.Sprintf("ai-insights:morning:%s:%s", u.ID.String(), nowUTC.In(time.UTC).Format("2006-01-02"))
		_ = w.sendAlertOnce(ctx, key, 26*time.Hour, func() error {
			return w.pushSender.SendToUser(ctx, u.ID,
				"Good morning ☀️",
				body,
				map[string]interface{}{
					"type":      "morning_greeting",
					"action":    "open_home",
					"data_used": []string{"current_balances"},
				},
			)
		})
	}
}

// countryTimezones maps ISO 3166-1 alpha-2 country codes to primary IANA timezone.
var countryTimezones = map[string]string{
	"NG": "Africa/Lagos",        // WAT (UTC+1)
	"GH": "Africa/Accra",        // GMT (UTC+0)
	"KE": "Africa/Nairobi",      // EAT (UTC+3)
	"ZA": "Africa/Johannesburg", // SAST (UTC+2)
	"GB": "Europe/London",       // GMT/BST
	"US": "America/New_York",    // EST/EDT (default for US)
	"CA": "America/Toronto",     // EST/EDT
	"DE": "Europe/Berlin",       // CET/CEST
	"FR": "Europe/Paris",        // CET/CEST
}

// isLocalMorning returns true if the current UTC time falls in the 7-8am window
// for the user's country. Defaults to true for unknown countries (sends at UTC 7am).
func isLocalMorning(country string, nowUTC time.Time) bool {
	tzName, ok := countryTimezones[country]
	if !ok {
		return nowUTC.Hour() == 7 // fallback: UTC 7am
	}
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		return nowUTC.Hour() == 7
	}
	localHour := nowUTC.In(loc).Hour()
	return localHour == 7
}

// runMonthRecap sends a month-end financial summary.
func (w *Worker) runMonthRecap(ctx context.Context) {
	w.logger.Info("Running month-end recap")

	users, err := w.userRepo.GetAllActiveUsers(ctx)
	if err != nil {
		w.logger.Error("month recap: failed to get users", zap.Error(err))
		return
	}

	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	for _, u := range users {
		if ctx.Err() != nil {
			return
		}
		spent, txCount, err := w.spendingRepo.GetSpendingTotal(ctx, u.ID, monthStart, now)
		if err != nil {
			continue
		}

		stash, _ := w.balances.GetAccountBalance(ctx, u.ID, entities.AccountTypeStashBalance)
		spend, _ := w.balances.GetAccountBalance(ctx, u.ID, entities.AccountTypeSpendingBalance)
		total := spend.Add(stash)

		if total.IsZero() && spent.IsZero() {
			continue
		}

		body := fmt.Sprintf("This month: $%s spent across %d transactions. Your stash is at $%s. Total balance: $%s. Ask Miriam for your full breakdown 💬",
			spent.StringFixed(2), txCount, stash.StringFixed(2), total.StringFixed(2))

		key := fmt.Sprintf("ai-insights:month-recap:%s:%04d-%02d", u.ID.String(), now.Year(), now.Month())
		_ = w.sendAlertOnce(ctx, key, 32*24*time.Hour, func() error {
			return w.pushSender.SendToUser(ctx, u.ID,
				"Your Month in Review 📊",
				body,
				map[string]interface{}{
					"type":      "month_recap",
					"action":    "open_chat",
					"data_used": []string{"month_spending", "stash_balance", "spend_balance"},
				},
			)
		})
	}

	w.logger.Info("Month recap complete")
}

// checkIdleMoney alerts when spend balance is high with no recent activity.
func (w *Worker) checkIdleMoney(ctx context.Context, userID uuid.UUID) {
	spend, err := w.balances.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil || spend.LessThan(decimal.NewFromInt(50)) {
		return
	}

	now := time.Now().UTC()
	weekAgo := now.AddDate(0, 0, -7)
	spent, txCount, _ := w.spendingRepo.GetSpendingTotal(ctx, userID, weekAgo, now)

	// If balance > $50 and fewer than 2 transactions in a week, suggest moving to stash
	if txCount < 2 && spent.LessThan(decimal.NewFromInt(20)) {
		suggestAmt := spend.Mul(decimal.NewFromFloat(0.3)).StringFixed(2)
		key := fmt.Sprintf("ai-insights:idle-money:%s:%s", userID.String(), weekAgo.Format("2006-01-02"))
		_ = w.sendAlertOnce(ctx, key, 7*24*time.Hour, func() error {
			return w.pushSender.SendToUser(ctx, userID,
				"Money sitting idle 💤",
				"$"+spend.StringFixed(2)+" in your spend wallet with barely any activity. Want to move $"+suggestAmt+" to stash where it earns yield?",
				map[string]interface{}{
					"type":           "idle_money",
					"action":         "open_chat",
					"suggest_amount": suggestAmt,
					"data_used":      []string{"spend_balance", "weekly_spending"},
				},
			)
		})
	}
}
