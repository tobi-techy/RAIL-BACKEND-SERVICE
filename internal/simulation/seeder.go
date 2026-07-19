package simulation

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	ledgersvc "github.com/rail-service/rail_service/internal/domain/services/ledger"
	miriamsvc "github.com/rail-service/rail_service/internal/domain/services/miriam"
	userrepo "github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/shopspring/decimal"
)

// Seeder materializes a Scenario's SeedSpec into the database so the real Miriam
// reads real state. Current balances are set through the ledger service (exact,
// admin-reconciled); trailing history (income/spend) and side data (obligations,
// facts, health scores) are written as backdated rows so the money-flow math has
// something to compute over.
type Seeder struct {
	db     *sql.DB
	ledger *ledgersvc.Service
	miriam *miriamsvc.Service
	users  *userrepo.UserRepository
}

// NewSeeder builds a Seeder from the pieces the harness pulls off the DI container.
func NewSeeder(db *sql.DB, ledger *ledgersvc.Service, miriam *miriamsvc.Service, users *userrepo.UserRepository) *Seeder {
	return &Seeder{db: db, ledger: ledger, miriam: miriam, users: users}
}

// SeededUser is the handle returned after seeding one scenario.
type SeededUser struct {
	UserID       uuid.UUID
	Email        string
	SpendBalance decimal.Decimal
	StashBalance decimal.Decimal
}

// Seed creates a fresh synthetic user and populates the scenario's financial state.
// It returns the user handle; call Teardown when the run is complete.
func (s *Seeder) Seed(ctx context.Context, sc *Scenario) (*SeededUser, error) {
	email := fmt.Sprintf("sim-%s-%d@rail.sim", sc.ID, time.Now().UnixNano())
	user, err := s.users.CreateUserWithHash(ctx, email, nil, "!sim-no-login!")
	if err != nil {
		return nil, fmt.Errorf("seed user: %w", err)
	}
	uid := user.ID

	// Ensure the user's ledger accounts exist before we touch balances or history.
	spendAcct, err := s.ledger.GetOrCreateUserAccount(ctx, uid, entities.AccountTypeSpendingBalance)
	if err != nil {
		return nil, fmt.Errorf("seed spend account: %w", err)
	}
	if _, err := s.ledger.GetOrCreateUserAccount(ctx, uid, entities.AccountTypeStashBalance); err != nil {
		return nil, fmt.Errorf("seed stash account: %w", err)
	}

	sysBufID, err := s.systemBufferAccountID(ctx)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()

	// Trailing income history: backdated, double-entry-balanced deposits.
	for i, ev := range sc.Seed.Income {
		amt, derr := decimal.NewFromString(ev.Amount)
		if derr != nil {
			return nil, fmt.Errorf("scenario %s income[%d] amount: %w", sc.ID, i, derr)
		}
		when := backdated(now, ev.DaysAgo, i)
		if err := s.insertLedgerMovement(ctx, uid, "deposit", spendAcct.ID, sysBufID, amt, when, ev.Note); err != nil {
			return nil, fmt.Errorf("seed income[%d]: %w", i, err)
		}
	}

	// Trailing spend history: backdated withdrawals (counted as outflow by money-flow).
	for i, ev := range sc.Seed.Spend {
		amt, derr := decimal.NewFromString(ev.Amount)
		if derr != nil {
			return nil, fmt.Errorf("scenario %s spend[%d] amount: %w", sc.ID, i, derr)
		}
		when := backdated(now, ev.DaysAgo, i)
		if err := s.insertLedgerMovement(ctx, uid, "withdrawal", spendAcct.ID, sysBufID, amt, when, ev.Note); err != nil {
			return nil, fmt.Errorf("seed spend[%d]: %w", i, err)
		}
		// Also seed enriched transaction so GetEnrichmentSummary has data.
		if ev.Category != "" {
			if err := s.insertEnrichedTransaction(ctx, uid, amt, when, ev.Category, ev.Note); err != nil {
				return nil, fmt.Errorf("seed enriched spend[%d]: %w", i, err)
			}
		}
	}

	// Card charges: backdated completed card_transactions with merchant + category.
	// These (not ledger withdrawals) are what the anomaly duplicate-charge and
	// merchant-pattern detectors inspect.
	if len(sc.Seed.CardSpend) > 0 {
		cardID, cerr := s.ensureCard(ctx, uid)
		if cerr != nil {
			return nil, fmt.Errorf("seed card: %w", cerr)
		}
		for i, ev := range sc.Seed.CardSpend {
			amt, derr := decimal.NewFromString(ev.Amount)
			if derr != nil {
				return nil, fmt.Errorf("scenario %s card_spend[%d] amount: %w", sc.ID, i, derr)
			}
			when := backdated(now, ev.DaysAgo, i)
			if err := s.insertCardTransaction(ctx, cardID, uid, amt, when, ev.Merchant, ev.Category); err != nil {
				return nil, fmt.Errorf("seed card_spend[%d]: %w", i, err)
			}
			// Also seed enriched transaction with merchant info from card event.
			cat := ev.Category
			if cat == "" {
				cat = "shopping"
			}
			note := ev.Merchant
			if note == "" {
				note = "card purchase"
			}
			if err := s.insertEnrichedTransaction(ctx, uid, amt, when, cat, note); err != nil {
				return nil, fmt.Errorf("seed enriched card_spend[%d]: %w", i, err)
			}
		}
	}

	for i, ob := range sc.Seed.Obligations {
		if err := s.insertObligation(ctx, uid, ob, now); err != nil {
			return nil, fmt.Errorf("seed obligation[%d]: %w", i, err)
		}
	}

	for i, f := range sc.Seed.Facts {
		if err := s.insertFact(ctx, uid, f); err != nil {
			return nil, fmt.Errorf("seed fact[%d]: %w", i, err)
		}
	}

	if sc.Seed.HealthScore != nil {
		if err := s.insertHealthScore(ctx, uid, *sc.Seed.HealthScore, now); err != nil {
			return nil, fmt.Errorf("seed health score: %w", err)
		}
	}

	// Set the exact current balances last so they are deterministic regardless of the
	// backdated history that also moved them.
	spendBal, err := sc.Seed.SpendBalanceDec()
	if err != nil {
		return nil, fmt.Errorf("scenario %s spend_balance: %w", sc.ID, err)
	}
	stashBal, err := sc.Seed.StashBalanceDec()
	if err != nil {
		return nil, fmt.Errorf("scenario %s stash_balance: %w", sc.ID, err)
	}
	if err := s.ledger.ReconcileBalance(ctx, uid, entities.AccountTypeSpendingBalance, spendBal); err != nil {
		return nil, fmt.Errorf("set spend balance: %w", err)
	}
	if err := s.ledger.ReconcileBalance(ctx, uid, entities.AccountTypeStashBalance, stashBal); err != nil {
		return nil, fmt.Errorf("set stash balance: %w", err)
	}

	// Derive Miriam's money state from the freshly seeded reality.
	if _, err := s.miriam.RefreshMoneyState(ctx, uid); err != nil {
		return nil, fmt.Errorf("refresh money state: %w", err)
	}

	return &SeededUser{UserID: uid, Email: email, SpendBalance: spendBal, StashBalance: stashBal}, nil
}

// systemBufferAccountID returns the platform USDC buffer account id, the counter-party
// for user deposits/withdrawals (matches the app's own ledger polarity).
func (s *Seeder) systemBufferAccountID(ctx context.Context) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM ledger_accounts WHERE account_type = 'system_buffer_usdc' AND user_id IS NULL LIMIT 1`,
	).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("locate system_buffer_usdc account: %w", err)
	}
	return id, nil
}

// insertLedgerMovement writes one backdated, double-entry-balanced transaction. For a
// deposit the user account is debited (balance up) and the buffer credited; for a
// withdrawal the polarity flips — mirroring internal/domain/services/ledger.
func (s *Seeder) insertLedgerMovement(
	ctx context.Context,
	userID uuid.UUID,
	txType string,
	userAcct, bufferAcct uuid.UUID,
	amount decimal.Decimal,
	when time.Time,
	note string,
) error {
	userEntry := entities.EntryTypeDebit
	bufEntry := entities.EntryTypeCredit
	if txType == "withdrawal" {
		userEntry = entities.EntryTypeCredit
		bufEntry = entities.EntryTypeDebit
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	txnID := uuid.New()
	idem := fmt.Sprintf("sim-%s-%s", txType, txnID)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO ledger_transactions (id, user_id, transaction_type, status, idempotency_key, description, created_at, completed_at)
		 VALUES ($1, $2, $3, 'completed', $4, $5, $6, $6)`,
		txnID, userID, txType, idem, note, when,
	); err != nil {
		return fmt.Errorf("insert ledger_transaction: %w", err)
	}

	// Insert both entries; the double-entry validation trigger checks balance on the
	// second insert, so amounts must match exactly.
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO ledger_entries (id, transaction_id, account_id, entry_type, amount, currency, created_at)
		 VALUES ($1, $2, $3, $4, $5, 'USDC', $6)`,
		uuid.New(), txnID, userAcct, string(userEntry), amount, when,
	); err != nil {
		return fmt.Errorf("insert user entry: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO ledger_entries (id, transaction_id, account_id, entry_type, amount, currency, created_at)
		 VALUES ($1, $2, $3, $4, $5, 'USDC', $6)`,
		uuid.New(), txnID, bufferAcct, string(bufEntry), amount, when,
	); err != nil {
		return fmt.Errorf("insert buffer entry: %w", err)
	}

	return tx.Commit()
}

// ensureCard creates a single minimal active card for the user (idempotent per user)
// so card_transactions have a valid FK parent. Uses synthetic bridge identifiers.
func (s *Seeder) ensureCard(ctx context.Context, userID uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM cards WHERE user_id = $1 LIMIT 1`, userID).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return uuid.Nil, fmt.Errorf("lookup card: %w", err)
	}
	id = uuid.New()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO cards (id, user_id, bridge_card_id, bridge_customer_id, type, status, last_4, expiry, currency, chain, wallet_address)
		 VALUES ($1, $2, $3, $4, 'virtual', 'active', '4242', '12/30', 'usd', 'solana', $5)`,
		id, userID,
		fmt.Sprintf("sim-card-%s", id),
		fmt.Sprintf("sim-cust-%s", userID),
		fmt.Sprintf("sim-wallet-%s", userID),
	)
	if err != nil {
		return uuid.Nil, fmt.Errorf("insert card: %w", err)
	}
	return id, nil
}

// insertCardTransaction writes one backdated completed capture. merchant_category maps
// to the outflow "category" and merchant_name to "source", which is what the anomaly
// detectors compare on.
func (s *Seeder) insertCardTransaction(ctx context.Context, cardID, userID uuid.UUID, amount decimal.Decimal, when time.Time, merchant, category string) error {
	transID := uuid.New()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO card_transactions (id, card_id, user_id, bridge_trans_id, type, amount, currency, merchant_name, merchant_category, status, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, 'capture', $5, 'usd', $6, $7, 'completed', $8, $8)`,
		transID, cardID, userID, fmt.Sprintf("sim-txn-%s", transID), amount, merchant, category, when,
	)
	return err
}

func (s *Seeder) insertObligation(ctx context.Context, userID uuid.UUID, ob ObligationSpec, now time.Time) error {
	amt, err := decimal.NewFromString(ob.Amount)
	if err != nil {
		return fmt.Errorf("obligation amount %q: %w", ob.Amount, err)
	}
	obType := ob.Type
	if obType == "" {
		obType = "other"
	}
	cadence := ob.Cadence
	if cadence == "" {
		cadence = "monthly"
	}
	due := now.Add(time.Duration(ob.DueInDays) * 24 * time.Hour)
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO financial_obligations (id, user_id, type, name, amount, currency, cadence, due_date, priority, status, metadata, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'medium', 'active', '{}'::jsonb, NOW(), NOW())`,
		uuid.New(), userID, obType, ob.Name, amt, ob.Currency, cadence, due,
	)
	return err
}

func (s *Seeder) insertFact(ctx context.Context, userID uuid.UUID, f FactSpec) error {
	if f.Importance == 0 {
		f.Importance = 5 // default importance
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO miriam_user_facts (id, user_id, category, fact, source, confidence, importance, first_observed_at, last_confirmed_at, created_at)
		 VALUES ($1, $2, $3, $4, 'conversation', $5, $6, NOW(), NOW(), NOW())`,
		uuid.New(), userID, f.Category, f.Fact, f.Conf, f.Importance,
	)
	return err
}

// categoryEnrichment maps seed spend categories to realistic enriched fields.
var categoryEnrichment = map[string]struct {
	Counterparty  string
	CategoryL1    string
	CategoryL2    string
	IsEssential   bool
	PlainDesc     string
	MerchantCtx   string
	Tags          []string
}{
	"groceries":   {"Shoprite", "essentials", "groceries", true, "Groceries at supermarket", "Supermarket chain, weekly shopping", []string{"recurring_essential", "consistent_merchant"}},
	"food":        {"Restaurant", "food_dining", "restaurants", false, "Restaurant meal", "Dining out", []string{"discretionary", "food_spend"}},
	"dining":      {"Cafe", "food_dining", "cafes", false, "Cafe visit", "Coffee shop", []string{"discretionary", "habitual"}},
	"transport":   {"Uber", "transport", "ride_sharing", true, "Ride-hailing trip", "Transport service", []string{"commute", "recurring_pattern"}},
	"rent":        {"Landlord", "housing", "rent", true, "Monthly rent payment", "Housing cost", []string{"recurring_essential", "fixed_cost"}},
	"salary":      {"Employer", "income", "salary", true, "Salary deposit", "Primary income", []string{"income_pattern", "salary_day"}},
	"utilities":   {"Power Company", "housing", "utilities", true, "Utility bill", "Electricity provider", []string{"recurring_essential", "bill_cycle"}},
	"entertainment": {"Netflix", "leisure", "streaming", false, "Streaming subscription", "Entertainment service", []string{"subscription", "discretionary"}},
	"health":      {"Pharmacy", "health", "pharmacy", true, "Pharmacy purchase", "Health-related spend", []string{"essential", "health"}},
	"shopping":    {"Amazon", "shopping", "online", false, "Online purchase", "E-commerce", []string{"discretionary", "online_shopping"}},
	"education":   {"Udemy", "education", "courses", false, "Online course", "Learning platform", []string{"self_investment", "discretionary"}},
}

// insertEnrichedTransaction writes one row into miriam_enriched_transactions so
// GetUserEnrichmentSummary returns data for the simulation's agent.
func (s *Seeder) insertEnrichedTransaction(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, when time.Time, category, note string) error {
	enrich, ok := categoryEnrichment[category]
	if !ok {
		enrich = categoryEnrichment["shopping"] // fallback
	}
	tags, _ := json.Marshal([]entities.BehaviorTag{
		{Tag: enrich.Tags[0], Confidence: 0.9},
		{Tag: enrich.Tags[1], Confidence: 0.85},
	})
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO miriam_enriched_transactions
			(id, transaction_id, user_id, raw_description, amount, currency,
			 transaction_date, direction, counterparty, category_l1, category_l2,
			 is_essential, is_recurring, plain_description, merchant_context,
			 behavior_tags, facts, embedding,
			 classification_layer, confidence, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)`,
		uuid.New(), uuid.New(), userID, note, amount, "USD",
		when, "outflow", enrich.Counterparty, enrich.CategoryL1, enrich.CategoryL2,
		enrich.IsEssential, false, enrich.PlainDesc, enrich.MerchantCtx,
		string(tags), "[]", "{}", "rule", 1.0, when,
	)
	return err
}

func (s *Seeder) insertHealthScore(ctx context.Context, userID uuid.UUID, score int, now time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO miriam_financial_health_scores
		   (id, user_id, overall_score, budget_score, savings_score, debt_score, runway_score, stability_score, reasoning, created_at)
		 VALUES ($1, $2, $3, $3, $3, $3, $3, $3, 'seeded by simulation harness', $4)`,
		uuid.New(), userID, score, now,
	)
	return err
}

// Teardown removes the synthetic user's data. It deletes ledger rows in dependency
// order first because ledger_entries.account_id is ON DELETE RESTRICT — a bare
// DELETE users cascade can trip that restriction. Best-effort in a disposable sim DB.
func (s *Seeder) Teardown(ctx context.Context, userID uuid.UUID) error {
	stmts := []string{
		`DELETE FROM miriam_enriched_transactions WHERE user_id = $1`,
		`DELETE FROM miriam_user_facts WHERE user_id = $1`,
		`DELETE FROM miriam_financial_health_scores WHERE user_id = $1`,
		`DELETE FROM card_transactions WHERE user_id = $1`,
		`DELETE FROM cards WHERE user_id = $1`,
		`DELETE FROM financial_obligations WHERE user_id = $1`,
		`DELETE FROM ledger_entries WHERE transaction_id IN (SELECT id FROM ledger_transactions WHERE user_id = $1)`,
		`DELETE FROM ledger_transactions WHERE user_id = $1`,
		`DELETE FROM ledger_accounts WHERE user_id = $1`,
		`DELETE FROM users WHERE id = $1`,
	}
	for _, q := range stmts {
		if _, err := s.db.ExecContext(ctx, q, userID); err != nil {
			return fmt.Errorf("teardown: %w", err)
		}
	}
	return nil
}
