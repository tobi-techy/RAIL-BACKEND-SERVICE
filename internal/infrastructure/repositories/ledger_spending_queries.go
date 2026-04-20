package repositories

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

// LedgerSpendingRepository queries ALL outflows: ledger withdrawals, card transactions, p2p transfers.
type LedgerSpendingRepository struct {
	db *sqlx.DB
}

func NewLedgerSpendingRepository(db *sqlx.DB) *LedgerSpendingRepository {
	return &LedgerSpendingRepository{db: db}
}

// allOutflows is a CTE that unions all spending sources into a single view.
const allOutflows = `WITH outflows AS (
	-- Ledger withdrawals
	SELECT t.created_at, e.amount,
		CASE WHEN t.metadata->>'provider' = 'paj' THEN 'NGN Withdrawal'
		     ELSE 'Withdrawal' END AS category,
		CASE WHEN t.metadata->>'provider' = 'paj' THEN 'Naira Withdrawal (₦' || COALESCE(t.metadata->>'fiat_amount', '?') || ')'
		     ELSE COALESCE(t.description, 'Crypto/Fiat Withdrawal') END AS source
	FROM ledger_entries e
	JOIN ledger_transactions t ON t.id = e.transaction_id
	JOIN ledger_accounts a ON a.id = e.account_id
	WHERE a.user_id = $1 AND e.entry_type = 'credit'
	  AND t.transaction_type = 'withdrawal' AND t.status = 'completed'
	  AND e.created_at >= $2 AND e.created_at < $3

	UNION ALL

	-- Card transactions
	SELECT created_at, amount, COALESCE(merchant_category, 'Card Payment') AS category, COALESCE(merchant_name, 'Card Payment') AS source
	FROM card_transactions
	WHERE user_id = $1 AND status = 'completed'
	  AND created_at >= $2 AND created_at < $3

	UNION ALL

	-- P2P transfers (sent) — smart categorization by recipient
	SELECT created_at, amount,
		CASE WHEN LOWER(recipient_identifier) SIMILAR TO '%(store|shop|pay|mart|market|delivery|logistics|food|restaurant|cafe|hotel|travel|uber|bolt|taxi)%'
		     THEN 'P2P Merchant' ELSE 'P2P Transfer' END AS category,
		COALESCE(recipient_identifier, 'P2P Send') AS source
	FROM p2p_transfers
	WHERE sender_id = $1 AND status = 'completed'
	  AND created_at >= $2 AND created_at < $3

	UNION ALL

	-- Scanned receipts (offline/cash spending)
	SELECT created_at, amount, category, merchant AS source
	FROM receipt_scans
	WHERE user_id = $1
	  AND created_at >= $2 AND created_at < $3
)`

func (r *LedgerSpendingRepository) GetSpendingByCategory(ctx context.Context, userID uuid.UUID, start, end time.Time) ([]entities.SpendingByCategory, error) {
	query := allOutflows + `
		SELECT category AS merchant_category, SUM(amount) AS total, COUNT(*) AS count
		FROM outflows GROUP BY category ORDER BY total DESC`

	var results []entities.SpendingByCategory
	if err := r.db.SelectContext(ctx, &results, query, userID, start, end); err != nil {
		return nil, err
	}
	return results, nil
}

func (r *LedgerSpendingRepository) GetSpendingByMerchant(ctx context.Context, userID uuid.UUID, start, end time.Time, limit int) ([]entities.SpendingByMerchant, error) {
	if limit <= 0 {
		limit = 10
	}
	query := allOutflows + `
		SELECT source AS merchant_name, SUM(amount) AS total, COUNT(*) AS count
		FROM outflows GROUP BY source ORDER BY total DESC LIMIT $4`

	var results []entities.SpendingByMerchant
	if err := r.db.SelectContext(ctx, &results, query, userID, start, end, limit); err != nil {
		return nil, err
	}
	return results, nil
}

func (r *LedgerSpendingRepository) GetSpendingByDay(ctx context.Context, userID uuid.UUID, start, end time.Time) ([]entities.SpendingByPeriod, error) {
	query := allOutflows + `
		SELECT TO_CHAR(created_at, 'YYYY-MM-DD') AS period, SUM(amount) AS total, COUNT(*) AS count
		FROM outflows GROUP BY period ORDER BY period`

	var results []entities.SpendingByPeriod
	if err := r.db.SelectContext(ctx, &results, query, userID, start, end); err != nil {
		return nil, err
	}
	return results, nil
}

func (r *LedgerSpendingRepository) GetSpendingTotal(ctx context.Context, userID uuid.UUID, start, end time.Time) (decimal.Decimal, int, error) {
	var total decimal.Decimal
	var count int
	err := r.db.QueryRowContext(ctx, allOutflows+`
		SELECT COALESCE(SUM(amount), 0), COUNT(*) FROM outflows`,
		userID, start, end).Scan(&total, &count)
	return total, count, err
}

func (r *LedgerSpendingRepository) GetRecentOutflows(ctx context.Context, userID uuid.UUID, start, end time.Time, limit int) ([]entities.SpendingTransaction, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	query := allOutflows + `
		SELECT TO_CHAR(created_at, 'YYYY-MM-DD') AS date, amount, category, source
		FROM outflows ORDER BY created_at DESC LIMIT $4`

	var results []entities.SpendingTransaction
	if err := r.db.SelectContext(ctx, &results, query, userID, start, end, limit); err != nil {
		return nil, err
	}
	return results, nil
}

func (r *LedgerSpendingRepository) GetMoneyFlow(ctx context.Context, userID uuid.UUID, start, end time.Time) (*entities.MoneyFlowSummary, error) {
	s := &entities.MoneyFlowSummary{}

	// Deposits (money in) — credit entries increase user balance via debit in this ledger
	err := r.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(e.amount), 0), COUNT(*)
		FROM ledger_entries e
		JOIN ledger_transactions t ON t.id = e.transaction_id
		JOIN ledger_accounts a ON a.id = e.account_id
		WHERE a.user_id = $1 AND e.entry_type = 'debit'
		  AND t.transaction_type = 'deposit' AND t.status = 'completed'
		  AND e.created_at >= $2 AND e.created_at < $3`,
		userID, start, end).Scan(&s.TotalDeposits, &s.DepositCount)
	if err != nil {
		return nil, err
	}

	// Withdrawals (money out via ledger)
	err = r.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(e.amount), 0), COUNT(*)
		FROM ledger_entries e
		JOIN ledger_transactions t ON t.id = e.transaction_id
		JOIN ledger_accounts a ON a.id = e.account_id
		WHERE a.user_id = $1 AND e.entry_type = 'credit'
		  AND t.transaction_type = 'withdrawal' AND t.status = 'completed'
		  AND e.created_at >= $2 AND e.created_at < $3`,
		userID, start, end).Scan(&s.TotalWithdrawals, &s.WithdrawalCount)
	if err != nil {
		return nil, err
	}

	// Card spend
	err = r.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(amount), 0), COUNT(*)
		FROM card_transactions
		WHERE user_id = $1 AND status = 'completed'
		  AND created_at >= $2 AND created_at < $3`,
		userID, start, end).Scan(&s.TotalCardSpend, &s.CardSpendCount)
	if err != nil {
		return nil, err
	}

	// P2P sent
	err = r.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(amount), 0), COUNT(*)
		FROM p2p_transfers
		WHERE sender_id = $1 AND status = 'completed'
		  AND created_at >= $2 AND created_at < $3`,
		userID, start, end).Scan(&s.TotalP2P, &s.P2PCount)
	if err != nil {
		return nil, err
	}

	// Scanned receipts (offline/cash spending)
	err = r.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(amount), 0), COUNT(*)
		FROM receipt_scans
		WHERE user_id = $1 AND created_at >= $2 AND created_at < $3`,
		userID, start, end).Scan(&s.TotalReceipts, &s.ReceiptCount)
	if err != nil {
		return nil, err
	}

	return s, nil
}
