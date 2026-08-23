package daily_pulse

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// UserRepo fetches active users.
type UserRepo interface {
	GetAllActiveUsers(ctx context.Context) ([]struct {
		ID      uuid.UUID
		Country string
	}, error)
}

// BalanceProvider reads account balances.
type BalanceProvider interface {
	GetAccountBalance(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (decimal.Decimal, error)
}

// SpendingProvider reads spending data.
type SpendingProvider interface {
	GetMoneyFlow(ctx context.Context, userID uuid.UUID, start, end time.Time) (*entities.MoneyFlowSummary, error)
}

// BudgetProvider reads budget data.
type BudgetProvider interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.SpendingBudget, error)
}

// StreakProvider reads streak data.
type StreakProvider interface {
	GetStreak(ctx context.Context, userID uuid.UUID) (*entities.InvestmentStreak, error)
}

// PushSender sends push notifications.
type PushSender interface {
	SendToUser(ctx context.Context, userID uuid.UUID, title, body string, data map[string]interface{}) error
}

// BriefProvider returns the same canonical Miriam brief exposed by /api/v1/ai/miriam-brief.
type BriefProvider interface {
	GetMiriamBrief(ctx context.Context, userID uuid.UUID, country string) (map[string]interface{}, error)
}

// NudgeGenerator generates AI-powered nudge text. Nil-safe — returns "" if not set.
type NudgeGenerator interface {
	GenerateNudge(ctx context.Context, snapshot string) string
}

// PulsePreferencesReader returns per-user briefing prefs for the daily pulse.
// Nil-safe — falls back to country 09:00 defaults.
type PulsePreferencesReader interface {
	// ShouldSendBriefing reports whether the user wants morning briefs.
	ShouldSendBriefing(ctx context.Context, userID uuid.UUID) bool
	// BriefingHourLocal returns the preferred local hour (0-23); default 9.
	BriefingHourLocal(ctx context.Context, userID uuid.UUID) int
	// TimezoneOverride returns IANA tz if set, else "".
	TimezoneOverride(ctx context.Context, userID uuid.UUID) string
}

// Worker sends a daily personalized money pulse notification from Miriam.
type Worker struct {
	users    UserRepo
	balances BalanceProvider
	spending SpendingProvider
	budgets  BudgetProvider
	streaks  StreakProvider
	push     PushSender
	brief    BriefProvider
	nudger   NudgeGenerator
	prefs    PulsePreferencesReader
	logger   *zap.Logger
	sentDate map[string]string
}

func NewWorker(
	users UserRepo,
	balances BalanceProvider,
	spending SpendingProvider,
	budgets BudgetProvider,
	streaks StreakProvider,
	push PushSender,
	logger *zap.Logger,
) *Worker {
	return &Worker{
		users: users, balances: balances, spending: spending,
		budgets: budgets, streaks: streaks, push: push, logger: logger,
		sentDate: make(map[string]string),
	}
}

// SetBriefProvider makes daily notifications consume Miriam's canonical brief.
func (w *Worker) SetBriefProvider(p BriefProvider) { w.brief = p }

// SetNudger sets an optional AI nudge generator for personalized pulse messages.
func (w *Worker) SetNudger(n NudgeGenerator) { w.nudger = n }

// SetPreferences attaches per-user briefing prefs (optional).
func (w *Worker) SetPreferences(p PulsePreferencesReader) { w.prefs = p }

// Start runs the daily pulse loop. Sends once per user in their local 9am window.
func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("Daily pulse worker started")
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("Daily pulse worker stopped")
			return
		case <-ticker.C:
			w.sendPulses(ctx)
		}
	}
}

func (w *Worker) sendPulses(ctx context.Context) {
	users, err := w.users.GetAllActiveUsers(ctx)
	if err != nil {
		w.logger.Error("daily pulse: failed to get users", zap.Error(err))
		return
	}

	w.logger.Info("daily pulse: sending", zap.Int("users", len(users)))
	sent, failed := 0, 0
	nowUTC := time.Now().UTC()

	for _, u := range users {
		if w.prefs != nil && !w.prefs.ShouldSendBriefing(ctx, u.ID) {
			continue
		}
		hour := 9
		tzOverride := ""
		if w.prefs != nil {
			hour = w.prefs.BriefingHourLocal(ctx, u.ID)
			tzOverride = w.prefs.TimezoneOverride(ctx, u.ID)
		}
		localDate, ok := dueForDailyPulseAt(u.Country, nowUTC, hour, tzOverride)
		if !ok {
			continue
		}
		key := u.ID.String()
		if w.sentDate[key] == localDate {
			continue
		}

		title, body, data := w.buildPulse(ctx, u.ID, u.Country)
		if body == "" {
			continue
		}
		if data == nil {
			data = map[string]interface{}{}
		}
		data["type"] = "daily_pulse"
		data["screen"] = "ai-chat"
		data["local_date"] = localDate
		if err := w.push.SendToUser(ctx, u.ID, title, body, data); err != nil {
			failed++
			continue
		}
		w.sentDate[key] = localDate
		sent++
		// Small delay to avoid hammering SNS
		time.Sleep(50 * time.Millisecond)
	}

	w.logger.Info("daily pulse: done", zap.Int("sent", sent), zap.Int("failed", failed))
}

func (w *Worker) buildPulse(ctx context.Context, userID uuid.UUID, country string) (string, string, map[string]interface{}) {
	fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	symbol := entities.CurrencySymbol(country)

	if w.brief != nil {
		if brief, err := w.brief.GetMiriamBrief(fetchCtx, userID, country); err == nil {
			if title, body, data := pulseFromBrief(brief); body != "" {
				return title, body, data
			}
		} else {
			w.logger.Debug("daily pulse: miriam brief unavailable, falling back", zap.String("user_id", userID.String()), zap.Error(err))
		}
	}

	loc := locationForCountry(country)
	now := time.Now().In(loc)
	yesterday := now.AddDate(0, 0, -1).UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc).UTC()
	nowUTC := now.UTC()

	// Gather data (all best-effort)
	spend, _ := w.balances.GetAccountBalance(fetchCtx, userID, entities.AccountTypeSpendingBalance)
	stash, _ := w.balances.GetAccountBalance(fetchCtx, userID, entities.AccountTypeStashBalance)
	total := spend.Add(stash)

	var daySpend, monthNet decimal.Decimal
	if flow, err := w.spending.GetMoneyFlow(fetchCtx, userID, yesterday, nowUTC); err == nil && flow != nil {
		daySpend = flow.TotalWithdrawals.Add(flow.TotalCardSpend).Add(flow.TotalP2P)
	}
	if flow, err := w.spending.GetMoneyFlow(fetchCtx, userID, monthStart, nowUTC); err == nil && flow != nil {
		totalOut := flow.TotalWithdrawals.Add(flow.TotalCardSpend).Add(flow.TotalP2P)
		monthNet = flow.TotalDeposits.Sub(totalOut)
	}

	var budgetRemaining decimal.Decimal
	var hasBudget bool
	if w.budgets != nil {
		if b, err := w.budgets.GetByUserID(fetchCtx, userID); err == nil && b != nil && b.MonthlyLimit.IsPositive() {
			hasBudget = true
			if flow, err := w.spending.GetMoneyFlow(fetchCtx, userID, monthStart, nowUTC); err == nil && flow != nil {
				totalOut := flow.TotalWithdrawals.Add(flow.TotalCardSpend).Add(flow.TotalP2P)
				budgetRemaining = b.MonthlyLimit.Sub(totalOut)
			}
		}
	}

	var streakDays int
	if w.streaks != nil {
		if s, err := w.streaks.GetStreak(fetchCtx, userID); err == nil && s != nil {
			streakDays = s.CurrentStreak
		}
	}

	// Try AI-generated nudge first (fast model, ~1s)
	if w.nudger != nil {
		snapshot := buildNudgeSnapshot(total, spend, stash, daySpend, monthNet, budgetRemaining, hasBudget, streakDays, now, symbol)
		if nudge := w.nudger.GenerateNudge(fetchCtx, snapshot); nudge != "" {
			return "Miriam checked the math", nudge, nil
		}
	}

	// Fallback to template-based pulse
	type pulse struct {
		title string
		body  string
		score int // higher = more relevant
	}
	var candidates []pulse

	// Yesterday's spending
	if daySpend.IsPositive() {
		candidates = append(candidates, pulse{
			title: "Miriam spotted a move",
			body:  fmt.Sprintf("%s%s left Spend yesterday. Want the quick read on what changed?", symbol, daySpend.StringFixed(2)),
			score: 3,
		})
	}

	// Budget progress
	if hasBudget && budgetRemaining.IsPositive() {
		daysLeft := daysRemainingInMonth(now)
		daily := budgetRemaining.Div(decimal.NewFromInt(maxInt64(int64(daysLeft), 1)))
		candidates = append(candidates, pulse{
			title: "Miriam likes this pace",
			body:  fmt.Sprintf("%s%s left this month. That's about %s%s/day if we keep it tidy.", symbol, budgetRemaining.StringFixed(2), symbol, daily.StringFixed(2)),
			score: 4,
		})
	}
	if hasBudget && budgetRemaining.IsNegative() {
		candidates = append(candidates, pulse{
			title: "Tiny budget reset?",
			body:  fmt.Sprintf("Budget is %s%s over. Not a crisis, just a course correction.", symbol, budgetRemaining.Abs().StringFixed(2)),
			score: 5,
		})
	}

	// Streak
	if streakDays >= 3 {
		candidates = append(candidates, pulse{
			title: "Streak is still alive",
			body:  fmt.Sprintf("Day %d. Your future self is quietly enjoying this consistency.", streakDays),
			score: 2,
		})
	}

	// Stash growth
	if stash.IsPositive() {
		candidates = append(candidates, pulse{
			title: "Stash check",
			body:  fmt.Sprintf("%s%s in Stash. Quiet money, doing useful things.", symbol, stash.StringFixed(2)),
			score: 1,
		})
	}

	// Net flow
	if monthNet.IsPositive() {
		candidates = append(candidates, pulse{
			title: "Miriam did a small nod",
			body:  fmt.Sprintf("You're up %s%s this month. More in than out is the whole trick.", symbol, monthNet.StringFixed(2)),
			score: 3,
		})
	}

	// Fallback
	if len(candidates) == 0 {
		if total.IsPositive() {
			return "Miriam checked in", fmt.Sprintf("%s%s across Rail. Your money clocked in before you did.", symbol, total.StringFixed(2)), nil
		}
		return "", "", nil // No data, skip this user
	}

	// Pick highest score, with random tiebreak for variety
	rand.Shuffle(len(candidates), func(i, j int) { candidates[i], candidates[j] = candidates[j], candidates[i] })
	best := candidates[0]
	for _, c := range candidates[1:] {
		if c.score > best.score {
			best = c
		}
	}

	return best.title, best.body, nil
}

func daysRemainingInMonth(now time.Time) int {
	nextMonth := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	return int(nextMonth.Sub(now).Hours() / 24)
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func buildNudgeSnapshot(total, spend, stash, daySpend, monthNet, budgetRemaining decimal.Decimal, hasBudget bool, streakDays int, now time.Time, symbol string) string {
	var parts []string
	if total.IsPositive() {
		parts = append(parts, fmt.Sprintf("Total: %s%s (spend %s%s, stash %s%s)", symbol, total.StringFixed(2), symbol, spend.StringFixed(2), symbol, stash.StringFixed(2)))
	}
	if daySpend.IsPositive() {
		parts = append(parts, fmt.Sprintf("Yesterday spent: %s%s", symbol, daySpend.StringFixed(2)))
	}
	if !monthNet.IsZero() {
		parts = append(parts, fmt.Sprintf("Month net: %s%s", symbol, monthNet.StringFixed(2)))
	}
	if hasBudget {
		parts = append(parts, fmt.Sprintf("Budget remaining: %s%s", symbol, budgetRemaining.StringFixed(2)))
	}
	if streakDays > 0 {
		parts = append(parts, fmt.Sprintf("Saving streak: %d days", streakDays))
	}
	parts = append(parts, fmt.Sprintf("Day: %s", now.Format("Monday")))
	return fmt.Sprintf("%s", strings.Join(parts, ". "))
}

func pulseFromBrief(brief map[string]interface{}) (string, string, map[string]interface{}) {
	insights := mapSlice(brief["insights"])
	if len(insights) == 0 {
		return "", "", nil
	}
	top := insights[0]
	title := stringValue(top["title"])
	body := stringValue(top["body"])
	if title == "" || body == "" {
		return "", "", nil
	}
	body = truncateNotification(body, 150)

	actions := mapSlice(brief["next_actions"])
	data := map[string]interface{}{
		"miriam_brief": brief,
		"insight_id":   stringValue(top["id"]),
		"insight_type": stringValue(top["type"]),
		"severity":     stringValue(top["severity"]),
	}
	if len(actions) > 0 {
		data["next_action_id"] = stringValue(actions[0]["id"])
		data["next_action_type"] = stringValue(actions[0]["type"])
	}
	return title, body, data
}

func mapSlice(v interface{}) []map[string]interface{} {
	switch s := v.(type) {
	case []map[string]interface{}:
		return s
	case []interface{}:
		out := make([]map[string]interface{}, 0, len(s))
		for _, item := range s {
			if m, ok := item.(map[string]interface{}); ok {
				out = append(out, m)
			}
		}
		return out
	default:
		return nil
	}
}

func stringValue(v interface{}) string {
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func truncateNotification(s string, max int) string {
	runes := []rune(strings.TrimSpace(s))
	if len(runes) <= max {
		return string(runes)
	}
	cut := string(runes[:max])
	if idx := strings.LastIndex(cut, " "); idx > 40 {
		cut = cut[:idx]
	}
	return strings.TrimSpace(cut) + "."
}

func dueForDailyPulse(country string, nowUTC time.Time) (string, bool) {
	return dueForDailyPulseAt(country, nowUTC, 9, "")
}

// dueForDailyPulseAt is true when local hour matches briefingHour (0-23).
// tzOverride is an IANA timezone; empty uses country mapping.
func dueForDailyPulseAt(country string, nowUTC time.Time, briefingHour int, tzOverride string) (string, bool) {
	if briefingHour < 0 || briefingHour > 23 {
		briefingHour = 9
	}
	var loc *time.Location
	if tzOverride != "" {
		if l, err := time.LoadLocation(tzOverride); err == nil && l != nil {
			loc = l
		}
	}
	if loc == nil {
		loc = locationForCountry(country)
	}
	local := nowUTC.In(loc)
	if local.Hour() != briefingHour {
		return "", false
	}
	return local.Format("2006-01-02"), true
}

func locationForCountry(country string) *time.Location {
	tz := timezoneForCountry(country)
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.UTC
	}
	return loc
}

func timezoneForCountry(country string) string {
	switch strings.ToUpper(strings.TrimSpace(country)) {
	case "NG":
		return "Africa/Lagos"
	case "GH":
		return "Africa/Accra"
	case "KE":
		return "Africa/Nairobi"
	case "ZA":
		return "Africa/Johannesburg"
	case "GB", "UK":
		return "Europe/London"
	case "US":
		return "America/New_York"
	case "CA":
		return "America/Toronto"
	case "DE":
		return "Europe/Berlin"
	case "FR":
		return "Europe/Paris"
	default:
		return "UTC"
	}
}
