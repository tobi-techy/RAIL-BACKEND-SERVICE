// Package main backfills historical data into Mixpanel.
// It reads from the production database and pushes all past events + user profiles
// so that Mixpanel has complete analytics history.
//
// Usage: go run scripts/mixpanel_backfill/main.go
// Requires: DATABASE_URL and MIXPANEL_TOKEN env vars (or .env file in project root).
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
	"github.com/mixpanel/mixpanel-go"
	"github.com/shopspring/decimal"
)

const batchSize = 50 // Mixpanel import API limit per request

type mpClient struct {
	api *mixpanel.ApiClient
}

func main() {
	_ = godotenv.Load() // load .env if present

	token := os.Getenv("MIXPANEL_TOKEN")
	dbURL := os.Getenv("DATABASE_URL")
	if token == "" || dbURL == "" {
		log.Fatal("MIXPANEL_TOKEN and DATABASE_URL are required")
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatalf("db open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(5)

	ctx := context.Background()
	mp := &mpClient{api: mixpanel.NewApiClient(token, mixpanel.EuResidency())}

	log.Println("=== Mixpanel Backfill Starting ===")

	mp.backfillUserProfiles(ctx, db)
	mp.backfillSignups(ctx, db)
	mp.backfillKYC(ctx, db)
	mp.backfillDeposits(ctx, db)
	mp.backfillWithdrawals(ctx, db)
	mp.backfillAllocations(ctx, db)
	mp.backfillOrders(ctx, db)
	mp.backfillP2PTransfers(ctx, db)
	mp.backfillSubscriptions(ctx, db)

	log.Println("=== Backfill Complete ===")
}

// ─── User Profiles ──────────────────────────────────────────────────────────────

func (m *mpClient) backfillUserProfiles(ctx context.Context, db *sql.DB) {
	log.Println("[profiles] starting...")

	rows, err := db.QueryContext(ctx, `
		SELECT
			u.id, u.email, u.first_name, u.last_name, u.kyc_status,
			u.onboarding_status, u.created_at,
			COALESCE(dep.cnt, 0) AS deposit_count,
			COALESCE(dep.total, 0) AS total_deposited,
			COALESCE(w.cnt, 0) AS withdrawal_count,
			COALESCE(w.total, 0) AS total_withdrawn,
			COALESCE(b.buying_power, 0) AS buying_power,
			dep.last_deposit_at
		FROM users u
		LEFT JOIN (
			SELECT user_id, COUNT(*) AS cnt, SUM(amount) AS total, MAX(created_at) AS last_deposit_at
			FROM deposits WHERE status = 'confirmed' GROUP BY user_id
		) dep ON dep.user_id = u.id
		LEFT JOIN (
			SELECT user_id, COUNT(*) AS cnt, SUM(amount) AS total
			FROM withdrawals WHERE status = 'completed' GROUP BY user_id
		) w ON w.user_id = u.id
		LEFT JOIN balances b ON b.user_id = u.id
	`)
	if err != nil {
		log.Printf("[profiles] query failed: %v", err)
		return
	}
	defer rows.Close()

	var batch []*mixpanel.PeopleProperties
	var count int

	for rows.Next() {
		var (
			id, email, kycStatus, onboardingStatus string
			firstName, lastName                    sql.NullString
			createdAt                              time.Time
			depositCount, withdrawalCount          int
			totalDeposited, totalWithdrawn         decimal.Decimal
			buyingPower                            decimal.Decimal
			lastDepositAt                          sql.NullTime
		)
		if err := rows.Scan(&id, &email, &firstName, &lastName, &kycStatus,
			&onboardingStatus, &createdAt, &depositCount, &totalDeposited,
			&withdrawalCount, &totalWithdrawn, &buyingPower, &lastDepositAt); err != nil {
			log.Printf("[profiles] scan: %v", err)
			continue
		}

		props := map[string]any{
			"$email":             email,
			"$created":          createdAt.Format(time.RFC3339),
			"kyc_status":        kycStatus,
			"onboarding_status": onboardingStatus,
			"total_deposits":    depositCount,
			"total_deposited":   totalDeposited.InexactFloat64(),
			"total_withdrawals": withdrawalCount,
			"total_withdrawn":   totalWithdrawn.InexactFloat64(),
			"buying_power":      buyingPower.InexactFloat64(),
			"net_inflow":        totalDeposited.Sub(totalWithdrawn).InexactFloat64(),
		}
		if firstName.Valid {
			props["$first_name"] = firstName.String
		}
		if lastName.Valid {
			props["$last_name"] = lastName.String
		}
		if lastDepositAt.Valid {
			props["last_deposit_at"] = lastDepositAt.Time.Format(time.RFC3339)
		}

		batch = append(batch, mixpanel.NewPeopleProperties(id, props))
		count++

		if len(batch) >= batchSize {
			m.flushProfiles(ctx, batch)
			batch = nil
		}
	}
	if len(batch) > 0 {
		m.flushProfiles(ctx, batch)
	}
	log.Printf("[profiles] sent %d user profiles", count)
}

func (m *mpClient) flushProfiles(ctx context.Context, batch []*mixpanel.PeopleProperties) {
	if err := m.api.PeopleSet(ctx, batch); err != nil {
		log.Printf("[profiles] flush error: %v", err)
	}
}

// ─── Signup Events ──────────────────────────────────────────────────────────────

func (m *mpClient) backfillSignups(ctx context.Context, db *sql.DB) {
	log.Println("[signups] starting...")
	count := m.backfillSimpleQuery(ctx, db,
		`SELECT id, created_at FROM users ORDER BY created_at`,
		"signup_completed", func(rows *sql.Rows) (string, time.Time, map[string]any, error) {
			var id string
			var ts time.Time
			err := rows.Scan(&id, &ts)
			return id, ts, nil, err
		})
	log.Printf("[signups] sent %d events", count)
}

// ─── KYC Events ─────────────────────────────────────────────────────────────────

func (m *mpClient) backfillKYC(ctx context.Context, db *sql.DB) {
	log.Println("[kyc] starting...")
	count := m.backfillSimpleQuery(ctx, db,
		`SELECT user_id, status, provider, submitted_at FROM kyc_submissions WHERE submitted_at IS NOT NULL ORDER BY submitted_at`,
		"", func(rows *sql.Rows) (string, time.Time, map[string]any, error) {
			var uid, status, provider string
			var ts time.Time
			err := rows.Scan(&uid, &status, &provider, &ts)
			event := "kyc_started"
			if status == "approved" {
				event = "kyc_completed"
			} else if status == "rejected" {
				event = "kyc_failed"
			}
			return uid, ts, map[string]any{"_event": event, "provider": provider, "status": status}, err
		})
	log.Printf("[kyc] sent %d events", count)
}

// ─── Deposit Events ─────────────────────────────────────────────────────────────

func (m *mpClient) backfillDeposits(ctx context.Context, db *sql.DB) {
	log.Println("[deposits] starting...")
	count := m.backfillSimpleQuery(ctx, db,
		`SELECT user_id, amount, chain, status, created_at FROM deposits ORDER BY created_at`,
		"deposit_completed", func(rows *sql.Rows) (string, time.Time, map[string]any, error) {
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
	log.Printf("[deposits] sent %d events", count)
}

// ─── Withdrawal Events ──────────────────────────────────────────────────────────

func (m *mpClient) backfillWithdrawals(ctx context.Context, db *sql.DB) {
	log.Println("[withdrawals] starting...")
	count := m.backfillSimpleQuery(ctx, db,
		`SELECT user_id, amount, status, fee_amount, created_at FROM withdrawals ORDER BY created_at`,
		"withdrawal_completed", func(rows *sql.Rows) (string, time.Time, map[string]any, error) {
			var uid, status string
			var amount, fee decimal.Decimal
			var ts time.Time
			err := rows.Scan(&uid, &amount, &status, &fee, &ts)
			evName := "withdrawal_completed"
			if status == "failed" {
				evName = "withdrawal_failed"
			}
			return uid, ts, map[string]any{"_event": evName, "amount": amount.InexactFloat64(), "fee": fee.InexactFloat64(), "status": status}, err
		})
	log.Printf("[withdrawals] sent %d events", count)
}

// ─── Allocation Events ──────────────────────────────────────────────────────────

func (m *mpClient) backfillAllocations(ctx context.Context, db *sql.DB) {
	log.Println("[allocations] starting...")
	count := m.backfillSimpleQuery(ctx, db,
		`SELECT user_id, event_type, deposit_amount, spend_amount, invest_amount, save_amount, created_at
		 FROM allocation_events ORDER BY created_at`,
		"allocation_executed", func(rows *sql.Rows) (string, time.Time, map[string]any, error) {
			var uid, eventType string
			var deposit, spend, invest, save decimal.Decimal
			var ts time.Time
			err := rows.Scan(&uid, &eventType, &deposit, &spend, &invest, &save, &ts)
			return uid, ts, map[string]any{
				"event_type":    eventType,
				"deposit_amount": deposit.InexactFloat64(),
				"spend_amount":  spend.InexactFloat64(),
				"invest_amount": invest.InexactFloat64(),
				"save_amount":   save.InexactFloat64(),
			}, err
		})
	log.Printf("[allocations] sent %d events", count)
}

// ─── Order Events ───────────────────────────────────────────────────────────────

func (m *mpClient) backfillOrders(ctx context.Context, db *sql.DB) {
	log.Println("[orders] starting...")
	count := m.backfillSimpleQuery(ctx, db,
		`SELECT o.user_id, o.side, o.amount, o.status, b.name, o.created_at
		 FROM orders o LEFT JOIN baskets b ON b.id = o.basket_id ORDER BY o.created_at`,
		"first_investment", func(rows *sql.Rows) (string, time.Time, map[string]any, error) {
			var uid, side, status string
			var basketName sql.NullString
			var amount decimal.Decimal
			var ts time.Time
			err := rows.Scan(&uid, &side, &amount, &status, &basketName, &ts)
			return uid, ts, map[string]any{
				"side": side, "amount": amount.InexactFloat64(),
				"status": status, "basket": basketName.String,
			}, err
		})
	log.Printf("[orders] sent %d events", count)
}

// ─── P2P Transfers ──────────────────────────────────────────────────────────────

func (m *mpClient) backfillP2PTransfers(ctx context.Context, db *sql.DB) {
	log.Println("[p2p] starting...")
	count := m.backfillSimpleQuery(ctx, db,
		`SELECT sender_id, amount, status, transfer_method, created_at FROM p2p_transfers ORDER BY created_at`,
		"p2p_transfer", func(rows *sql.Rows) (string, time.Time, map[string]any, error) {
			var uid, status, method string
			var amount decimal.Decimal
			var ts time.Time
			err := rows.Scan(&uid, &amount, &status, &method, &ts)
			return uid, ts, map[string]any{"amount": amount.InexactFloat64(), "status": status, "method": method}, err
		})
	log.Printf("[p2p] sent %d events", count)
}

// ─── Subscriptions ──────────────────────────────────────────────────────────────

func (m *mpClient) backfillSubscriptions(ctx context.Context, db *sql.DB) {
	log.Println("[subscriptions] starting...")
	count := m.backfillSimpleQuery(ctx, db,
		`SELECT user_id, plan, status, created_at FROM subscriptions ORDER BY created_at`,
		"premium_converted", func(rows *sql.Rows) (string, time.Time, map[string]any, error) {
			var uid, plan, status string
			var ts time.Time
			err := rows.Scan(&uid, &plan, &status, &ts)
			return uid, ts, map[string]any{"plan": plan, "status": status}, err
		})
	log.Printf("[subscriptions] sent %d events", count)
}

// ─── Generic Helpers ────────────────────────────────────────────────────────────

type rowScanner func(*sql.Rows) (userID string, ts time.Time, props map[string]any, err error)

func (m *mpClient) backfillSimpleQuery(ctx context.Context, db *sql.DB, query, defaultEvent string, scan rowScanner) int {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		log.Printf("  query failed: %v", err)
		return 0
	}
	defer rows.Close()

	var batch []*mixpanel.Event
	var count int

	for rows.Next() {
		uid, ts, props, err := scan(rows)
		if err != nil {
			log.Printf("  scan error: %v", err)
			continue
		}

		eventName := defaultEvent
		if props != nil {
			if override, ok := props["_event"].(string); ok && override != "" {
				eventName = override
				delete(props, "_event")
			}
		}
		if eventName == "" {
			continue
		}
		if props == nil {
			props = map[string]any{}
		}
		props["$time"] = ts.Unix()
		props["backfill"] = true

		batch = append(batch, m.api.NewEvent(eventName, uid, props))
		count++

		if len(batch) >= batchSize {
			m.flushEvents(ctx, batch)
			batch = nil
		}
	}
	if len(batch) > 0 {
		m.flushEvents(ctx, batch)
	}
	return count
}

func (m *mpClient) flushEvents(ctx context.Context, batch []*mixpanel.Event) {
	opts := mixpanel.ImportOptions{Strict: false}
	if _, err := m.api.Import(ctx, batch, opts); err != nil {
		log.Printf("  import batch error: %v", err)
		time.Sleep(2 * time.Second)
		if _, err2 := m.api.Import(ctx, batch, opts); err2 != nil {
			log.Printf("  import retry failed: %v", err2)
		}
	}
	time.Sleep(100 * time.Millisecond)
}

// Ensure decimal import is used
var _ = fmt.Sprintf
