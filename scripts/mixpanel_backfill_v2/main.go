// Mixpanel backfill v2: adds geo properties, session-based retention events, and richer user profiles.
// Run: go run scripts/mixpanel_backfill_v2/main.go
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

const batch = 50

type mp struct{ api *mixpanel.ApiClient }

func main() {
	_ = godotenv.Load()
	token := os.Getenv("MIXPANEL_TOKEN")
	dbURL := os.Getenv("DATABASE_URL")
	if token == "" || dbURL == "" {
		log.Fatal("MIXPANEL_TOKEN and DATABASE_URL required")
	}
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(5)

	ctx := context.Background()
	c := &mp{api: mixpanel.NewApiClient(token, mixpanel.EuResidency())}

	log.Println("=== Mixpanel Backfill v2 Starting ===")
	c.enrichProfiles(ctx, db)
	c.backfillSessions(ctx, db)
	c.backfillRepeatDeposits(ctx, db)
	c.backfillFirstDeposits(ctx, db)
	log.Println("=== Backfill v2 Complete ===")
}

// enrichProfiles sets geo properties ($city, $region, $country_code) and computed retention metrics.
func (c *mp) enrichProfiles(ctx context.Context, db *sql.DB) {
	log.Println("[profiles-geo] starting...")

	rows, err := db.QueryContext(ctx, `
		SELECT
			u.id, u.country, u.address_city, u.address_state, u.address_country,
			u.created_at, u.last_login_at,
			COALESCE(s.session_count, 0),
			s.first_session, s.last_session,
			COALESCE(dep.cnt, 0), dep.first_deposit, dep.last_deposit,
			COALESCE(dep.total, 0)
		FROM users u
		LEFT JOIN (
			SELECT user_id, COUNT(*) session_count, MIN(created_at) first_session, MAX(created_at) last_session
			FROM sessions GROUP BY user_id
		) s ON s.user_id = u.id
		LEFT JOIN (
			SELECT user_id, COUNT(*) cnt, MIN(created_at) first_deposit, MAX(created_at) last_deposit, SUM(amount) total
			FROM deposits WHERE status = 'confirmed' GROUP BY user_id
		) dep ON dep.user_id = u.id
	`)
	if err != nil {
		log.Printf("[profiles-geo] query: %v", err)
		return
	}
	defer rows.Close()

	var profiles []*mixpanel.PeopleProperties
	count := 0
	for rows.Next() {
		var (
			id                                                  string
			country, city, state, addrCountry                   sql.NullString
			createdAt                                           time.Time
			lastLogin, firstSession, lastSession                sql.NullTime
			firstDeposit, lastDeposit                           sql.NullTime
			sessionCount, depCount                              int
			totalDeposited                                      decimal.Decimal
		)
		if err := rows.Scan(&id, &country, &city, &state, &addrCountry,
			&createdAt, &lastLogin, &sessionCount, &firstSession, &lastSession,
			&depCount, &firstDeposit, &lastDeposit, &totalDeposited); err != nil {
			continue
		}

		props := map[string]any{}

		// Geo: use address fields if available, fall back to country
		if city.Valid && city.String != "" {
			props["$city"] = city.String
		}
		if state.Valid && state.String != "" {
			props["$region"] = state.String
		}
		cc := ""
		if addrCountry.Valid && addrCountry.String != "" {
			cc = addrCountry.String
		} else if country.Valid && country.String != "" {
			cc = country.String
		}
		if cc != "" {
			props["$country_code"] = cc
			props["country"] = cc
		}

		// Retention metrics
		props["session_count"] = sessionCount
		if firstSession.Valid {
			props["first_session_at"] = firstSession.Time.Format(time.RFC3339)
		}
		if lastSession.Valid {
			props["last_session_at"] = lastSession.Time.Format(time.RFC3339)
		}
		if lastLogin.Valid {
			props["last_login_at"] = lastLogin.Time.Format(time.RFC3339)
		}

		// Deposit retention
		props["deposit_count"] = depCount
		props["total_deposited"] = totalDeposited.InexactFloat64()
		if firstDeposit.Valid {
			props["first_deposit_at"] = firstDeposit.Time.Format(time.RFC3339)
		}
		if lastDeposit.Valid {
			props["last_deposit_at"] = lastDeposit.Time.Format(time.RFC3339)
		}

		// Days since signup
		daysSinceSignup := int(time.Since(createdAt).Hours() / 24)
		props["days_since_signup"] = daysSinceSignup

		// Activated (has at least 1 deposit)
		props["activated"] = depCount > 0

		// Retained (session in last 7 days)
		if lastSession.Valid {
			props["retained_7d"] = time.Since(lastSession.Time).Hours() < 168
			props["retained_30d"] = time.Since(lastSession.Time).Hours() < 720
		}

		profiles = append(profiles, mixpanel.NewPeopleProperties(id, props))
		count++
		if len(profiles) >= batch {
			if err := c.api.PeopleSet(ctx, profiles); err != nil {
				log.Printf("[profiles-geo] PeopleSet error: %v", err)
			}
			profiles = nil
			time.Sleep(100 * time.Millisecond)
		}
	}
	if err := rows.Err(); err != nil {
		log.Printf("[profiles-geo] iteration error: %v", err)
		return
	}
	if len(profiles) > 0 {
		if err := c.api.PeopleSet(ctx, profiles); err != nil {
			log.Printf("[profiles-geo] PeopleSet error: %v", err)
		}
	}
	log.Printf("[profiles-geo] enriched %d profiles", count)
}

// backfillSessions creates session_started events for retention analysis.
func (c *mp) backfillSessions(ctx context.Context, db *sql.DB) {
	log.Println("[sessions] starting...")

	rows, err := db.QueryContext(ctx, `
		SELECT user_id, created_at, last_used_at, ip_address, user_agent
		FROM sessions ORDER BY created_at
	`)
	if err != nil {
		log.Printf("[sessions] query: %v", err)
		return
	}
	defer rows.Close()

	var events []*mixpanel.Event
	count := 0
	for rows.Next() {
		var (
			uid       string
			createdAt time.Time
			lastUsed  sql.NullTime
			ip, ua    sql.NullString
		)
		if err := rows.Scan(&uid, &createdAt, &lastUsed, &ip, &ua); err != nil {
			continue
		}

		duration := 0.0
		if lastUsed.Valid {
			duration = lastUsed.Time.Sub(createdAt).Seconds()
		}

		props := map[string]any{
			"$time":            createdAt.Unix(),
			"session_duration": duration,
			"backfill":         true,
		}
		if ip.Valid && ip.String != "" {
			props["$ip"] = ip.String // Mixpanel resolves geo from IP
		}
		if ua.Valid && ua.String != "" {
			props["$user_agent"] = ua.String
		}

		events = append(events, c.api.NewEvent("session_started", uid, props))
		count++
		if len(events) >= batch {
			c.flush(ctx, events)
			events = nil
		}
	}
	if err := rows.Err(); err != nil {
		log.Printf("[sessions] iteration error: %v", err)
		return
	}
	if len(events) > 0 {
		c.flush(ctx, events)
	}
	log.Printf("[sessions] sent %d events", count)
}

// backfillRepeatDeposits marks 2nd+ deposits as deposit_repeated for retention cohorts.
func (c *mp) backfillRepeatDeposits(ctx context.Context, db *sql.DB) {
	log.Println("[repeat-deposits] starting...")

	rows, err := db.QueryContext(ctx, `
		SELECT user_id, amount, chain, created_at, rn FROM (
			SELECT user_id, amount, chain, created_at,
				ROW_NUMBER() OVER (PARTITION BY user_id ORDER BY created_at) as rn
			FROM deposits WHERE status = 'confirmed'
		) sub WHERE rn > 1 ORDER BY created_at
	`)
	if err != nil {
		log.Printf("[repeat-deposits] query: %v", err)
		return
	}
	defer rows.Close()

	var events []*mixpanel.Event
	count := 0
	for rows.Next() {
		var uid, chain string
		var amount decimal.Decimal
		var ts time.Time
		var rn int
		if err := rows.Scan(&uid, &amount, &chain, &ts, &rn); err != nil {
			continue
		}
		events = append(events, c.api.NewEvent("deposit_repeated", uid, map[string]any{
			"$time":          ts.Unix(),
			"amount":         amount.InexactFloat64(),
			"chain":          chain,
			"deposit_number": rn,
			"backfill":       true,
		}))
		count++
		if len(events) >= batch {
			c.flush(ctx, events)
			events = nil
		}
	}
	if err := rows.Err(); err != nil {
		log.Printf("[repeat-deposits] iteration error: %v", err)
		return
	}
	if len(events) > 0 {
		c.flush(ctx, events)
	}
	log.Printf("[repeat-deposits] sent %d events", count)
}

// backfillFirstDeposits marks the first confirmed deposit per user.
func (c *mp) backfillFirstDeposits(ctx context.Context, db *sql.DB) {
	log.Println("[first-deposits] starting...")

	rows, err := db.QueryContext(ctx, `
		SELECT user_id, amount, chain, created_at FROM (
			SELECT user_id, amount, chain, created_at,
				ROW_NUMBER() OVER (PARTITION BY user_id ORDER BY created_at) as rn
			FROM deposits WHERE status = 'confirmed'
		) sub WHERE rn = 1 ORDER BY created_at
	`)
	if err != nil {
		log.Printf("[first-deposits] query: %v", err)
		return
	}
	defer rows.Close()

	var events []*mixpanel.Event
	count := 0
	for rows.Next() {
		var uid, chain string
		var amount decimal.Decimal
		var ts time.Time
		if err := rows.Scan(&uid, &amount, &chain, &ts); err != nil {
			continue
		}
		events = append(events, c.api.NewEvent("first_deposit", uid, map[string]any{
			"$time":    ts.Unix(),
			"amount":   amount.InexactFloat64(),
			"chain":    chain,
			"backfill": true,
		}))
		count++
		if len(events) >= batch {
			c.flush(ctx, events)
			events = nil
		}
	}
	if err := rows.Err(); err != nil {
		log.Printf("[first-deposits] iteration error: %v", err)
		return
	}
	if len(events) > 0 {
		c.flush(ctx, events)
	}
	log.Printf("[first-deposits] sent %d events", count)
}

func (c *mp) flush(ctx context.Context, events []*mixpanel.Event) {
	if _, err := c.api.Import(ctx, events, mixpanel.ImportOptions{Strict: false}); err != nil {
		log.Printf("  import error: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
}

var _ = fmt.Sprintf
