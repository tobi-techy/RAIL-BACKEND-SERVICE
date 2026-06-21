package admin

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mixpanel/mixpanel-go"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

const backfillBatch = 50

// MixpanelBackfillHandler triggers a full historical data push to Mixpanel.
type MixpanelBackfillHandler struct {
	db     *sql.DB
	logger *zap.Logger
}

func NewMixpanelBackfillHandler(db *sql.DB, logger *zap.Logger) *MixpanelBackfillHandler {
	return &MixpanelBackfillHandler{db: db, logger: logger}
}

// TriggerBackfill pushes all historical events and user profiles to Mixpanel.
// POST /admin/analytics/backfill
func (h *MixpanelBackfillHandler) TriggerBackfill(c *gin.Context) {
	token := c.Query("token")
	if token == "" {
		token = "0b994888c114125c4434814ca5a81ce2"
	}

	mp := mixpanel.NewApiClient(token, mixpanel.EuResidency())

	// Run in background so we don't block the request
	go func() {
		ctx := context.Background()
		h.logger.Info("Mixpanel backfill started")

		h.backfillProfiles(ctx, mp)
		h.backfillSignups(ctx, mp)
		h.backfillKYC(ctx, mp)
		h.backfillDeposits(ctx, mp)
		h.backfillWithdrawals(ctx, mp)
		h.backfillAllocations(ctx, mp)
		h.backfillOrders(ctx, mp)
		h.backfillP2P(ctx, mp)
		h.backfillSubscriptions(ctx, mp)

		h.logger.Info("Mixpanel backfill completed")
	}()

	c.JSON(http.StatusOK, gin.H{"status": "backfill_started", "message": "Historical data is being pushed to Mixpanel in the background"})
}

func (h *MixpanelBackfillHandler) backfillProfiles(ctx context.Context, mp *mixpanel.ApiClient) {
	rows, err := h.db.QueryContext(ctx, `
		SELECT
			u.id, u.email, u.first_name, u.last_name, u.kyc_status,
			u.onboarding_status, u.created_at,
			COALESCE(dep.cnt, 0), COALESCE(dep.total, 0),
			COALESCE(w.cnt, 0), COALESCE(w.total, 0),
			COALESCE(b.buying_power, 0), dep.last_at
		FROM users u
		LEFT JOIN (SELECT user_id, COUNT(*) cnt, SUM(amount) total, MAX(created_at) last_at FROM deposits WHERE status='confirmed' GROUP BY user_id) dep ON dep.user_id = u.id
		LEFT JOIN (SELECT user_id, COUNT(*) cnt, SUM(amount) total FROM withdrawals WHERE status='completed' GROUP BY user_id) w ON w.user_id = u.id
		LEFT JOIN balances b ON b.user_id = u.id
	`)
	if err != nil {
		h.logger.Error("backfill profiles query", zap.Error(err))
		return
	}
	defer rows.Close()

	var batch []*mixpanel.PeopleProperties
	count := 0
	for rows.Next() {
		var (
			id, email, kycStatus, onboardingStatus string
			firstName, lastName                    sql.NullString
			createdAt                              time.Time
			depCnt, wCnt                           int
			depTotal, wTotal, bp                   decimal.Decimal
			lastDep                                sql.NullTime
		)
		if err := rows.Scan(&id, &email, &firstName, &lastName, &kycStatus, &onboardingStatus, &createdAt, &depCnt, &depTotal, &wCnt, &wTotal, &bp, &lastDep); err != nil {
			continue
		}
		props := map[string]any{
			"$email":             email,
			"$created":           createdAt.Format(time.RFC3339),
			"kyc_status":         kycStatus,
			"onboarding_status":  onboardingStatus,
			"total_deposits":     depCnt,
			"total_deposited":    depTotal.InexactFloat64(),
			"total_withdrawals":  wCnt,
			"total_withdrawn":    wTotal.InexactFloat64(),
			"buying_power":       bp.InexactFloat64(),
			"net_inflow":         depTotal.Sub(wTotal).InexactFloat64(),
		}
		if firstName.Valid {
			props["$first_name"] = firstName.String
		}
		if lastName.Valid {
			props["$last_name"] = lastName.String
		}
		if lastDep.Valid {
			props["last_deposit_at"] = lastDep.Time.Format(time.RFC3339)
		}
		batch = append(batch, mixpanel.NewPeopleProperties(id, props))
		count++
		if len(batch) >= backfillBatch {
			_ = mp.PeopleSet(ctx, batch)
			batch = nil
			time.Sleep(100 * time.Millisecond)
		}
	}
	if len(batch) > 0 {
		_ = mp.PeopleSet(ctx, batch)
	}
	h.logger.Info("backfill profiles done", zap.Int("count", count))
}

func (h *MixpanelBackfillHandler) backfillSignups(ctx context.Context, mp *mixpanel.ApiClient) {
	h.importQuery(ctx, mp, "signup_completed",
		`SELECT id, created_at FROM users ORDER BY created_at`,
		func(rows *sql.Rows) (string, time.Time, map[string]any, error) {
			var id string
			var ts time.Time
			err := rows.Scan(&id, &ts)
			return id, ts, nil, err
		})
}

func (h *MixpanelBackfillHandler) backfillKYC(ctx context.Context, mp *mixpanel.ApiClient) {
	h.importQuery(ctx, mp, "",
		`SELECT user_id, status, provider, submitted_at FROM kyc_submissions WHERE submitted_at IS NOT NULL ORDER BY submitted_at`,
		func(rows *sql.Rows) (string, time.Time, map[string]any, error) {
			var uid, status, provider string
			var ts time.Time
			err := rows.Scan(&uid, &status, &provider, &ts)
			ev := "kyc_started"
			if status == "approved" {
				ev = "kyc_completed"
			} else if status == "rejected" {
				ev = "kyc_failed"
			}
			return uid, ts, map[string]any{"_event": ev, "provider": provider, "status": status}, err
		})
}

func (h *MixpanelBackfillHandler) backfillDeposits(ctx context.Context, mp *mixpanel.ApiClient) {
	h.importQuery(ctx, mp, "deposit_completed",
		`SELECT user_id, amount, chain, status, created_at FROM deposits ORDER BY created_at`,
		func(rows *sql.Rows) (string, time.Time, map[string]any, error) {
			var uid, chain, status string
			var amount decimal.Decimal
			var ts time.Time
			err := rows.Scan(&uid, &amount, &chain, &status, &ts)
			props := map[string]any{"amount": amount.InexactFloat64(), "chain": chain, "status": status}
			if status == "failed" {
				props["_event"] = "deposit_failed"
			}
			return uid, ts, props, err
		})
}

func (h *MixpanelBackfillHandler) backfillWithdrawals(ctx context.Context, mp *mixpanel.ApiClient) {
	h.importQuery(ctx, mp, "withdrawal_completed",
		`SELECT user_id, amount, status, fee_amount, created_at FROM withdrawals ORDER BY created_at`,
		func(rows *sql.Rows) (string, time.Time, map[string]any, error) {
			var uid, status string
			var amount, fee decimal.Decimal
			var ts time.Time
			err := rows.Scan(&uid, &amount, &status, &fee, &ts)
			ev := "withdrawal_completed"
			if status == "failed" {
				ev = "withdrawal_failed"
			}
			return uid, ts, map[string]any{"_event": ev, "amount": amount.InexactFloat64(), "fee": fee.InexactFloat64(), "status": status}, err
		})
}

func (h *MixpanelBackfillHandler) backfillAllocations(ctx context.Context, mp *mixpanel.ApiClient) {
	h.importQuery(ctx, mp, "allocation_executed",
		`SELECT user_id, event_type, deposit_amount, spend_amount, invest_amount, save_amount, created_at FROM allocation_events ORDER BY created_at`,
		func(rows *sql.Rows) (string, time.Time, map[string]any, error) {
			var uid, eventType string
			var deposit, spend, invest, save decimal.Decimal
			var ts time.Time
			err := rows.Scan(&uid, &eventType, &deposit, &spend, &invest, &save, &ts)
			return uid, ts, map[string]any{
				"event_type": eventType, "deposit_amount": deposit.InexactFloat64(),
				"spend_amount": spend.InexactFloat64(), "invest_amount": invest.InexactFloat64(), "save_amount": save.InexactFloat64(),
			}, err
		})
}

func (h *MixpanelBackfillHandler) backfillOrders(ctx context.Context, mp *mixpanel.ApiClient) {
	h.importQuery(ctx, mp, "first_investment",
		`SELECT o.user_id, o.side, o.amount, o.status, COALESCE(b.name,''), o.created_at FROM orders o LEFT JOIN baskets b ON b.id=o.basket_id ORDER BY o.created_at`,
		func(rows *sql.Rows) (string, time.Time, map[string]any, error) {
			var uid, side, status, basket string
			var amount decimal.Decimal
			var ts time.Time
			err := rows.Scan(&uid, &side, &amount, &status, &basket, &ts)
			return uid, ts, map[string]any{"side": side, "amount": amount.InexactFloat64(), "status": status, "basket": basket}, err
		})
}

func (h *MixpanelBackfillHandler) backfillP2P(ctx context.Context, mp *mixpanel.ApiClient) {
	h.importQuery(ctx, mp, "p2p_transfer",
		`SELECT sender_id, amount, status, transfer_method, created_at FROM p2p_transfers ORDER BY created_at`,
		func(rows *sql.Rows) (string, time.Time, map[string]any, error) {
			var uid, status, method string
			var amount decimal.Decimal
			var ts time.Time
			err := rows.Scan(&uid, &amount, &status, &method, &ts)
			return uid, ts, map[string]any{"amount": amount.InexactFloat64(), "status": status, "method": method}, err
		})
}

func (h *MixpanelBackfillHandler) backfillSubscriptions(ctx context.Context, mp *mixpanel.ApiClient) {
	h.importQuery(ctx, mp, "premium_converted",
		`SELECT user_id, plan, status, created_at FROM subscriptions ORDER BY created_at`,
		func(rows *sql.Rows) (string, time.Time, map[string]any, error) {
			var uid, plan, status string
			var ts time.Time
			err := rows.Scan(&uid, &plan, &status, &ts)
			return uid, ts, map[string]any{"plan": plan, "status": status}, err
		})
}

type rowScanner func(*sql.Rows) (uid string, ts time.Time, props map[string]any, err error)

func (h *MixpanelBackfillHandler) importQuery(ctx context.Context, mp *mixpanel.ApiClient, defaultEvent, query string, scan rowScanner) {
	rows, err := h.db.QueryContext(ctx, query)
	if err != nil {
		h.logger.Error("backfill query failed", zap.String("event", defaultEvent), zap.Error(err))
		return
	}
	defer rows.Close()

	var batch []*mixpanel.Event
	count := 0
	for rows.Next() {
		uid, ts, props, err := scan(rows)
		if err != nil {
			continue
		}
		ev := defaultEvent
		if props != nil {
			if override, ok := props["_event"].(string); ok && override != "" {
				ev = override
				delete(props, "_event")
			}
		}
		if ev == "" {
			continue
		}
		if props == nil {
			props = map[string]any{}
		}
		props["$time"] = ts.Unix()
		props["backfill"] = true

		batch = append(batch, mp.NewEvent(ev, uid, props))
		count++
		if len(batch) >= backfillBatch {
			if _, err := mp.Import(ctx, batch, mixpanel.ImportOptions{Strict: false}); err != nil {
				log.Printf("import error: %v", err)
			}
			batch = nil
			time.Sleep(100 * time.Millisecond)
		}
	}
	if len(batch) > 0 {
		mp.Import(ctx, batch, mixpanel.ImportOptions{Strict: false})
	}
	h.logger.Info("backfill events done", zap.String("event", defaultEvent), zap.Int("count", count))
}
