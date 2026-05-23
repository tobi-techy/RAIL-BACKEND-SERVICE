package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"go.uber.org/zap"
)

type AnalyticsRepository struct {
	db     *sql.DB
	logger *zap.Logger
}

func NewAnalyticsRepository(db *sql.DB, logger *zap.Logger) *AnalyticsRepository {
	return &AnalyticsRepository{db: db, logger: logger}
}

// Unified deposit sum across all sources (deposits, bridge, paj onramp, funding events)
const allDepositsSum = `SELECT COALESCE(
	(SELECT SUM(amount) FROM deposits WHERE status = 'confirmed'), 0) +
	COALESCE((SELECT SUM(amount) FROM bridge_transactions WHERE status = 'completed'), 0) +
	COALESCE((SELECT SUM(token_amount) FROM paj_orders WHERE order_type = 'onramp' AND status = 'completed'), 0) +
	COALESCE((SELECT SUM(amount) FROM funding_event_jobs WHERE status = 'completed'), 0)`

const allDepositsSumBefore30d = `SELECT COALESCE(
	(SELECT SUM(amount) FROM deposits WHERE status = 'confirmed' AND created_at < NOW() - INTERVAL '30 days'), 0) +
	COALESCE((SELECT SUM(amount) FROM bridge_transactions WHERE status = 'completed' AND created_at < NOW() - INTERVAL '30 days'), 0) +
	COALESCE((SELECT SUM(token_amount) FROM paj_orders WHERE order_type = 'onramp' AND status = 'completed' AND created_at < NOW() - INTERVAL '30 days'), 0) +
	COALESCE((SELECT SUM(amount) FROM funding_event_jobs WHERE status = 'completed' AND first_seen_at < NOW() - INTERVAL '30 days'), 0)`

const allWithdrawalsSum = `SELECT COALESCE(
	(SELECT SUM(amount) FROM withdrawals WHERE status = 'completed'), 0) +
	COALESCE((SELECT SUM(token_amount) FROM paj_orders WHERE order_type = 'offramp' AND status = 'completed'), 0)`

// Monthly deposits from all sources
const monthlyDepositsQuery = `
	SELECT TO_CHAR(date_trunc('month', m), 'Mon'),
		COALESCE((SELECT SUM(amount) FROM deposits WHERE status='confirmed' AND date_trunc('month', created_at) = date_trunc('month', m)), 0) +
		COALESCE((SELECT SUM(amount) FROM bridge_transactions WHERE status='completed' AND date_trunc('month', created_at) = date_trunc('month', m)), 0) +
		COALESCE((SELECT SUM(token_amount) FROM paj_orders WHERE order_type='onramp' AND status='completed' AND date_trunc('month', created_at) = date_trunc('month', m)), 0) +
		COALESCE((SELECT SUM(amount) FROM funding_event_jobs WHERE status='completed' AND date_trunc('month', first_seen_at) = date_trunc('month', m)), 0),
		COALESCE((SELECT SUM(amount) FROM withdrawals WHERE status='completed' AND date_trunc('month', created_at) = date_trunc('month', m)), 0) +
		COALESCE((SELECT SUM(token_amount) FROM paj_orders WHERE order_type='offramp' AND status='completed' AND date_trunc('month', created_at) = date_trunc('month', m)), 0)
	FROM generate_series(NOW() - INTERVAL '5 months', NOW(), '1 month') AS m
	ORDER BY m`

type TimeSeriesPoint struct {
	Label string  `json:"label"`
	Value float64 `json:"value"`
}

type TwoSeriesPoint struct {
	Label  string  `json:"label"`
	Value1 float64 `json:"value1"`
	Value2 float64 `json:"value2"`
}

type KPI struct {
	Value   float64 `json:"value"`
	Prev    float64 `json:"prev"`
	Change  float64 `json:"change"`
	IsCount bool    `json:"is_count,omitempty"`
}

// ---- OVERVIEW ----

type ActivityEvent struct {
	Type      string  `json:"type"`
	UserName  string  `json:"user_name"`
	Amount    float64 `json:"amount,omitempty"`
	CreatedAt string  `json:"created_at"`
}

type OverviewData struct {
	TotalUsers         KPI               `json:"total_users"`
	NetDeposits        KPI               `json:"net_deposits"`
	KYCCompletion      KPI               `json:"kyc_completion"`
	ChurnRate          KPI               `json:"churn_rate"`
	DepositTrend       []TwoSeriesPoint  `json:"deposit_trend"`
	Funnel             []TimeSeriesPoint `json:"funnel"`
	RecentActivity     []ActivityEvent   `json:"recent_activity"`
	ActivationFunnel   []TimeSeriesPoint `json:"activation_funnel"`
	WaitlistConversion KPI               `json:"waitlist_conversion"`
}

func (r *AnalyticsRepository) GetOverview(ctx context.Context) (*OverviewData, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	d := &OverviewData{}

	// Total users
	r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&d.TotalUsers.Value)
	r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE created_at < NOW() - INTERVAL '30 days'`).Scan(&d.TotalUsers.Prev)

	// Net deposits (all sources: deposits, bridge, paj, funding events)
	r.db.QueryRowContext(ctx, allDepositsSum).Scan(&d.NetDeposits.Value)
	r.db.QueryRowContext(ctx, allDepositsSumBefore30d).Scan(&d.NetDeposits.Prev)

	// KYC completion rate
	var totalUsers, kycDone float64
	r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&totalUsers)
	r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE kyc_status = 'approved'`).Scan(&kycDone)
	if totalUsers > 0 {
		d.KYCCompletion.Value = (kycDone / totalUsers) * 100
	}

	// Churn rate (users inactive 30+ days / total active)
	var active, churned float64
	r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE updated_at > NOW() - INTERVAL '30 days'`).Scan(&active)
	r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE updated_at < NOW() - INTERVAL '30 days' AND updated_at > NOW() - INTERVAL '60 days'`).Scan(&churned)
	if active+churned > 0 {
		d.ChurnRate.Value = (churned / (active + churned)) * 100
	}

	// Deposit vs withdrawal trend (last 6 months, all sources)
	rows, err := r.db.QueryContext(ctx, monthlyDepositsQuery)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var p TwoSeriesPoint
			rows.Scan(&p.Label, &p.Value1, &p.Value2)
			d.DepositTrend = append(d.DepositTrend, p)
		}
	}

	// Funnel
	var waitlistCount, signupCount, kycCount, depositedCount float64
	r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM waitlist_users`).Scan(&waitlistCount)
	r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&signupCount)
	r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE kyc_status = 'approved'`).Scan(&kycCount)
	r.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT user_id) FROM deposits WHERE status = 'confirmed'`).Scan(&depositedCount)
	d.Funnel = []TimeSeriesPoint{
		{Label: "Waitlist", Value: waitlistCount},
		{Label: "Signups", Value: signupCount},
		{Label: "KYC Done", Value: kycCount},
		{Label: "Deposited", Value: depositedCount},
	}

	// Recent activity feed
	actRows, err := r.db.QueryContext(ctx, `
		(SELECT 'signup' as type, COALESCE(first_name, email) as user_name, 0::float as amount, created_at FROM users ORDER BY created_at DESC LIMIT 5)
		UNION ALL
		(SELECT 'deposit', COALESCE(u.first_name, u.email), d.amount, d.created_at FROM deposits d JOIN users u ON u.id = d.user_id WHERE d.status='confirmed' ORDER BY d.created_at DESC LIMIT 5)
		ORDER BY created_at DESC LIMIT 10`)
	if err == nil {
		defer actRows.Close()
		for actRows.Next() {
			var ev ActivityEvent
			var createdAt time.Time
			actRows.Scan(&ev.Type, &ev.UserName, &ev.Amount, &createdAt)
			ev.CreatedAt = timeAgo(createdAt)
			d.RecentActivity = append(d.RecentActivity, ev)
		}
	}

	// Activation funnel
	var kycStarted, allocated float64
	r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE kyc_status != 'pending'`).Scan(&kycStarted)
	r.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT user_id) FROM ledger_entries`).Scan(&allocated)
	d.ActivationFunnel = []TimeSeriesPoint{
		{Label: "Signups", Value: signupCount},
		{Label: "KYC Started", Value: kycStarted},
		{Label: "KYC Approved", Value: kycDone},
		{Label: "First Deposit", Value: depositedCount},
		{Label: "Allocated", Value: allocated},
	}

	// Waitlist conversion
	var converted float64
	r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM waitlist_users WHERE status = 'converted'`).Scan(&converted)
	if waitlistCount > 0 {
		d.WaitlistConversion.Value = (converted / waitlistCount) * 100
	}

	return d, nil
}

// ---- USERS ----

type UsersData struct {
	TotalUsers  KPI              `json:"total_users"`
	ActiveUsers KPI              `json:"active_users"`
	Churned     KPI              `json:"churned"`
	AvgDeposit  KPI              `json:"avg_deposit"`
	Growth      []TimeSeriesPoint `json:"growth"`
	UserList    []UserRow        `json:"user_list"`
}

type UserRow struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Email      string  `json:"email"`
	Status     string  `json:"status"`
	KYC        string  `json:"kyc"`
	Deposit    float64 `json:"deposit"`
	LastActive string  `json:"last_active"`
	CreatedAt  string  `json:"created_at"`
	Chats      int     `json:"chats"`
}

func (r *AnalyticsRepository) GetUsers(ctx context.Context, limit, offset int) (*UsersData, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	d := &UsersData{}

	r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&d.TotalUsers.Value)
	r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE updated_at > NOW() - INTERVAL '30 days'`).Scan(&d.ActiveUsers.Value)
	r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE updated_at < NOW() - INTERVAL '90 days'`).Scan(&d.Churned.Value)
	r.db.QueryRowContext(ctx, `SELECT COALESCE(AVG(total), 0) FROM (SELECT SUM(amount) AS total FROM deposits WHERE status='confirmed' GROUP BY user_id) sub`).Scan(&d.AvgDeposit.Value)

	// Growth (last 6 months cumulative)
	rows, err := r.db.QueryContext(ctx, `
		SELECT TO_CHAR(date_trunc('month', m), 'Mon'),
			(SELECT COUNT(*) FROM users WHERE created_at <= m)
		FROM generate_series(NOW() - INTERVAL '5 months', NOW(), '1 month') AS m
		ORDER BY m`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var p TimeSeriesPoint
			rows.Scan(&p.Label, &p.Value)
			d.Growth = append(d.Growth, p)
		}
	}

	// User list - ordered by most recent activity, includes all deposit sources
	userRows, err := r.db.QueryContext(ctx, `
		SELECT u.id, COALESCE(u.first_name || ' ' || u.last_name, u.first_name, ''), u.email,
			CASE WHEN u.updated_at > NOW() - INTERVAL '30 days' THEN 'Active'
				 WHEN u.updated_at > NOW() - INTERVAL '90 days' THEN 'Inactive'
				 ELSE 'Churned' END,
			u.kyc_status,
			COALESCE((SELECT SUM(amount) FROM deposits WHERE user_id = u.id AND status='confirmed'), 0) +
			COALESCE((SELECT SUM(amount) FROM bridge_transactions WHERE user_id = u.id AND status='completed'), 0) +
			COALESCE((SELECT SUM(token_amount) FROM paj_orders WHERE user_id = u.id AND order_type='onramp' AND status='completed'), 0),
			u.updated_at,
			u.created_at,
			COALESCE((SELECT SUM(message_count) FROM ai_conversations WHERE user_id = u.id), 0)
		FROM users u ORDER BY u.updated_at DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err == nil {
		defer userRows.Close()
		for userRows.Next() {
			var row UserRow
			var lastActive, createdAt time.Time
			userRows.Scan(&row.ID, &row.Name, &row.Email, &row.Status, &row.KYC, &row.Deposit, &lastActive, &createdAt, &row.Chats)
			row.LastActive = timeAgo(lastActive)
			row.CreatedAt = createdAt.Format("Jan 2, 2006")
			d.UserList = append(d.UserList, row)
		}
	}

	return d, nil
}

// ---- WAITLIST ----

type WaitlistData struct {
	Total        KPI               `json:"total"`
	ReferralRate KPI               `json:"referral_rate"`
	Growth       []TimeSeriesPoint  `json:"growth"`
	Daily        []TwoSeriesPoint   `json:"daily"`
	Users        []WaitlistUserRow  `json:"users"`
}

type WaitlistUserRow struct {
	Name         string `json:"name"`
	Email        string `json:"email"`
	Status       string `json:"status"`
	ReferralCode string `json:"referral_code"`
	Position     int    `json:"position"`
	CreatedAt    string `json:"created_at"`
}

func (r *AnalyticsRepository) GetWaitlist(ctx context.Context) (*WaitlistData, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	d := &WaitlistData{}

	r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM waitlist_users`).Scan(&d.Total.Value)
	var referred float64
	r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM waitlist_users WHERE referred_by IS NOT NULL`).Scan(&referred)
	if d.Total.Value > 0 {
		d.ReferralRate.Value = (referred / d.Total.Value) * 100
	}

	// Weekly growth
	rows, err := r.db.QueryContext(ctx, `
		SELECT TO_CHAR(date_trunc('week', w), 'Mon DD'),
			(SELECT COUNT(*) FROM waitlist_users WHERE created_at <= w)
		FROM generate_series(
			(SELECT MIN(created_at) FROM waitlist_users),
			NOW(), '1 week') AS w
		ORDER BY w`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var p TimeSeriesPoint
			rows.Scan(&p.Label, &p.Value)
			d.Growth = append(d.Growth, p)
		}
	}

	// Daily signups vs referrals (last 7 days)
	dRows, err := r.db.QueryContext(ctx, `
		SELECT TO_CHAR(d, 'Dy'),
			(SELECT COUNT(*) FROM waitlist_users WHERE created_at::date = d::date),
			(SELECT COUNT(*) FROM waitlist_users WHERE created_at::date = d::date AND referred_by IS NOT NULL)
		FROM generate_series(NOW() - INTERVAL '6 days', NOW(), '1 day') AS d
		ORDER BY d`)
	if err == nil {
		defer dRows.Close()
		for dRows.Next() {
			var p TwoSeriesPoint
			dRows.Scan(&p.Label, &p.Value1, &p.Value2)
			d.Daily = append(d.Daily, p)
		}
	}

	// All waitlist users
	uRows, err := r.db.QueryContext(ctx, `
		SELECT full_name, email, status, referral_code, position, created_at
		FROM waitlist_users ORDER BY position ASC`)
	if err == nil {
		defer uRows.Close()
		for uRows.Next() {
			var row WaitlistUserRow
			var createdAt time.Time
			uRows.Scan(&row.Name, &row.Email, &row.Status, &row.ReferralCode, &row.Position, &createdAt)
			row.CreatedAt = createdAt.Format("Jan 2, 2006")
			d.Users = append(d.Users, row)
		}
	}

	return d, nil
}

// ---- MIRIAM ----

type MiriamData struct {
	TotalMessages  KPI               `json:"total_messages"`
	AvgSession     KPI               `json:"avg_session"`
	TotalSessions  KPI               `json:"total_sessions"`
	ActiveUsers    KPI               `json:"active_users"`
	DailyMessages  []TwoSeriesPoint  `json:"daily_messages"`
	TopUsers       []MiriamUserRow   `json:"top_users"`
}

type MiriamUserRow struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Messages int    `json:"messages"`
	Sessions int    `json:"sessions"`
}

func (r *AnalyticsRepository) GetMiriam(ctx context.Context) (*MiriamData, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	d := &MiriamData{}

	r.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(message_count), 0) FROM ai_conversations`).Scan(&d.TotalMessages.Value)
	r.db.QueryRowContext(ctx, `SELECT COALESCE(AVG(message_count), 0) FROM ai_conversations WHERE message_count > 0`).Scan(&d.AvgSession.Value)
	r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ai_conversations`).Scan(&d.TotalSessions.Value)
	r.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT user_id) FROM ai_conversations`).Scan(&d.ActiveUsers.Value)

	// Daily messages (last 7 days)
	rows, err := r.db.QueryContext(ctx, `
		SELECT TO_CHAR(d, 'Dy'),
			COALESCE((SELECT SUM(message_count) FROM ai_conversations WHERE created_at::date = d::date), 0),
			COALESCE((SELECT COUNT(*) FROM ai_conversations WHERE created_at::date = d::date), 0)
		FROM generate_series(NOW() - INTERVAL '6 days', NOW(), '1 day') AS d
		ORDER BY d`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var p TwoSeriesPoint
			rows.Scan(&p.Label, &p.Value1, &p.Value2)
			d.DailyMessages = append(d.DailyMessages, p)
		}
	}

	// Top users by messages
	topRows, err := r.db.QueryContext(ctx, `
		SELECT COALESCE(u.first_name || ' ' || u.last_name, u.first_name, u.email),
			u.email, COALESCE(SUM(c.message_count), 0), COUNT(c.id)
		FROM ai_conversations c JOIN users u ON u.id = c.user_id
		GROUP BY u.id, u.first_name, u.last_name, u.email
		ORDER BY SUM(c.message_count) DESC LIMIT 15`)
	if err == nil {
		defer topRows.Close()
		for topRows.Next() {
			var row MiriamUserRow
			topRows.Scan(&row.Name, &row.Email, &row.Messages, &row.Sessions)
			d.TopUsers = append(d.TopUsers, row)
		}
	}

	return d, nil
}

// ---- MONEY MOVEMENT ----

type MoneyMovementData struct {
	TotalDeposits    KPI              `json:"total_deposits"`
	TotalWithdrawals KPI              `json:"total_withdrawals"`
	NetFlow          KPI              `json:"net_flow"`
	MonthlyFlow      []TwoSeriesPoint `json:"monthly_flow"`
	ByChain          []TwoSeriesPoint `json:"by_chain"`
}

func (r *AnalyticsRepository) GetMoneyMovement(ctx context.Context) (*MoneyMovementData, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	d := &MoneyMovementData{}

	r.db.QueryRowContext(ctx, allDepositsSum).Scan(&d.TotalDeposits.Value)
	r.db.QueryRowContext(ctx, allWithdrawalsSum).Scan(&d.TotalWithdrawals.Value)
	d.NetFlow.Value = d.TotalDeposits.Value - d.TotalWithdrawals.Value

	// Monthly flow (last 6 months, all sources)
	rows, err := r.db.QueryContext(ctx, monthlyDepositsQuery)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var p TwoSeriesPoint
			rows.Scan(&p.Label, &p.Value1, &p.Value2)
			d.MonthlyFlow = append(d.MonthlyFlow, p)
		}
	}

	// By chain
	chainRows, err := r.db.QueryContext(ctx, `
		SELECT chain,
			COALESCE(SUM(amount), 0),
			COALESCE((SELECT SUM(w.amount) FROM withdrawals w WHERE w.destination_chain = d.chain AND w.status='completed'), 0)
		FROM deposits d WHERE d.status = 'confirmed'
		GROUP BY chain ORDER BY SUM(amount) DESC`)
	if err == nil {
		defer chainRows.Close()
		for chainRows.Next() {
			var p TwoSeriesPoint
			chainRows.Scan(&p.Label, &p.Value1, &p.Value2)
			d.ByChain = append(d.ByChain, p)
		}
	}

	return d, nil
}

// ---- RETENTION ----

type RetentionData struct {
	D30Retention KPI              `json:"d30_retention"`
	D90Retention KPI              `json:"d90_retention"`
	ChurnRate    KPI              `json:"churn_rate"`
	MonthlyCohorts []CohortRow   `json:"cohorts"`
	ChurnTrend   []TimeSeriesPoint `json:"churn_trend"`
}

type CohortRow struct {
	Cohort string    `json:"cohort"`
	Weeks  []float64 `json:"weeks"`
}

func (r *AnalyticsRepository) GetRetention(ctx context.Context) (*RetentionData, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	d := &RetentionData{}

	var total, d30, d90 float64
	r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE created_at < NOW() - INTERVAL '30 days'`).Scan(&total)
	r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE created_at < NOW() - INTERVAL '30 days' AND updated_at > NOW() - INTERVAL '30 days'`).Scan(&d30)
	r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE created_at < NOW() - INTERVAL '90 days' AND updated_at > NOW() - INTERVAL '30 days'`).Scan(&d90)
	if total > 0 {
		d.D30Retention.Value = (d30 / total) * 100
		d.ChurnRate.Value = ((total - d30) / total) * 100
	}
	var total90 float64
	r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE created_at < NOW() - INTERVAL '90 days'`).Scan(&total90)
	if total90 > 0 {
		d.D90Retention.Value = (d90 / total90) * 100
	}

	// Churn trend (last 6 months)
	rows, err := r.db.QueryContext(ctx, `
		SELECT TO_CHAR(date_trunc('month', m), 'Mon'),
			CASE WHEN (SELECT COUNT(*) FROM users WHERE created_at < m) = 0 THEN 0
			ELSE (SELECT COUNT(*) FROM users WHERE updated_at < m - INTERVAL '30 days' AND created_at < m - INTERVAL '30 days')::float /
				 NULLIF((SELECT COUNT(*) FROM users WHERE created_at < m), 0) * 100 END
		FROM generate_series(NOW() - INTERVAL '5 months', NOW(), '1 month') AS m
		ORDER BY m`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var p TimeSeriesPoint
			rows.Scan(&p.Label, &p.Value)
			d.ChurnTrend = append(d.ChurnTrend, p)
		}
	}

	return d, nil
}

// ---- TRUST ----

type TrustData struct {
	FraudPrevented KPI              `json:"fraud_prevented"`
	FlagsTrend     []TwoSeriesPoint `json:"flags_trend"`
}

func (r *AnalyticsRepository) GetTrust(ctx context.Context) (*TrustData, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	d := &TrustData{}

	r.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(amount), 0) FROM fraud_alerts WHERE status != 'dismissed'`).Scan(&d.FraudPrevented.Value)

	// Flags per week (last 6 weeks)
	rows, err := r.db.QueryContext(ctx, `
		SELECT TO_CHAR(date_trunc('week', w), 'Mon DD'),
			COALESCE((SELECT COUNT(*) FROM fraud_alerts WHERE severity IN ('high','critical') AND created_at >= w AND created_at < w + INTERVAL '1 week'), 0),
			COALESCE((SELECT COUNT(*) FROM fraud_alerts WHERE severity IN ('low','medium') AND created_at >= w AND created_at < w + INTERVAL '1 week'), 0)
		FROM generate_series(NOW() - INTERVAL '5 weeks', NOW(), '1 week') AS w
		ORDER BY w`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var p TwoSeriesPoint
			rows.Scan(&p.Label, &p.Value1, &p.Value2)
			d.FlagsTrend = append(d.FlagsTrend, p)
		}
	}

	return d, nil
}

// ---- CHAINS ----

type ChainsData struct {
	TotalTVL    KPI              `json:"total_tvl"`
	ChainStats  []ChainStat      `json:"chain_stats"`
}

type ChainStat struct {
	Chain       string  `json:"chain"`
	Deposits    float64 `json:"deposits"`
	Withdrawals float64 `json:"withdrawals"`
	Users       int     `json:"users"`
	TVL         float64 `json:"tvl"`
}

func (r *AnalyticsRepository) GetChains(ctx context.Context) (*ChainsData, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	d := &ChainsData{}

	r.db.QueryRowContext(ctx, allDepositsSum).Scan(&d.TotalTVL.Value)

	rows, err := r.db.QueryContext(ctx, `
		SELECT chain,
			COALESCE(SUM(amount), 0) AS dep,
			COALESCE((SELECT SUM(w.amount) FROM withdrawals w WHERE w.destination_chain = d.chain AND w.status='completed'), 0) AS wd,
			COUNT(DISTINCT user_id),
			COALESCE(SUM(amount), 0) - COALESCE((SELECT SUM(w.amount) FROM withdrawals w WHERE w.destination_chain = d.chain AND w.status='completed'), 0)
		FROM deposits d WHERE d.status = 'confirmed'
		GROUP BY chain ORDER BY dep DESC`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var s ChainStat
			rows.Scan(&s.Chain, &s.Deposits, &s.Withdrawals, &s.Users, &s.TVL)
			d.ChainStats = append(d.ChainStats, s)
		}
	}

	return d, nil
}

// ---- helpers ----

func timeAgo(t time.Time) string {
	diff := time.Since(t)
	switch {
	case diff < time.Minute:
		return "just now"
	case diff < time.Hour:
		return fmt.Sprintf("%dm ago", int(diff.Minutes()))
	case diff < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(diff.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(diff.Hours()/24))
	}
}
