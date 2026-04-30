package activity

import (
	"context"
	"database/sql"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

// Service provides a unified activity feed across all transaction types.
type Service struct {
	db     *sqlx.DB
	logger *zap.Logger
}

func NewService(db *sqlx.DB, logger *zap.Logger) *Service {
	return &Service{db: db, logger: logger}
}

// GetActivityFeed returns a unified, time-sorted activity feed for a user.
// PAJ onramp orders suppress their linked deposit to avoid duplicate entries.
func (s *Service) GetActivityFeed(ctx context.Context, userID uuid.UUID, limit, offset int) (*entities.ActivityFeedResponse, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	// Fetch all sources in parallel using goroutines
	type result struct {
		items []entities.ActivityItem
		err   error
	}
	ch := make(chan result, 4)

	go func() {
		items, err := s.fetchDepositsAndPajOrders(ctx, userID, limit+offset+10)
		ch <- result{items, err}
	}()
	go func() {
		items, err := s.fetchWithdrawals(ctx, userID, limit+offset+10)
		ch <- result{items, err}
	}()
	go func() {
		items, err := s.fetchP2PTransfers(ctx, userID, limit+offset+10)
		ch <- result{items, err}
	}()
	go func() {
		items, err := s.fetchPajOfframpOrders(ctx, userID, limit+offset+10)
		ch <- result{items, err}
	}()

	var all []entities.ActivityItem
	for i := 0; i < 4; i++ {
		r := <-ch
		if r.err != nil {
			s.logger.Warn("activity source fetch failed", zap.Error(r.err))
			continue // partial results are better than no results
		}
		all = append(all, r.items...)
	}

	// Sort by time descending
	sort.Slice(all, func(i, j int) bool {
		return all[i].CreatedAt.After(all[j].CreatedAt)
	})

	// Apply offset + limit
	if offset >= len(all) {
		return &entities.ActivityFeedResponse{Items: []entities.ActivityItem{}, HasMore: false}, nil
	}
	end := offset + limit
	hasMore := end < len(all)
	if end > len(all) {
		end = len(all)
	}
	page := all[offset:end]

	nextCursor := ""
	if hasMore {
		nextCursor = page[len(page)-1].CreatedAt.Format(time.RFC3339Nano)
	}

	return &entities.ActivityFeedResponse{
		Items:      page,
		NextCursor: nextCursor,
		HasMore:    hasMore,
	}, nil
}

// fetchDepositsAndPajOrders fetches deposits and PAJ onramp orders, suppressing
// deposits that were created by a PAJ onramp (to avoid showing both).
func (s *Service) fetchDepositsAndPajOrders(ctx context.Context, userID uuid.UUID, limit int) ([]entities.ActivityItem, error) {
	// Get PAJ onramp orders first to know which deposit_ids to suppress
	pajRows, err := s.db.QueryContext(ctx, `
		SELECT paj_order_id, order_type, status, fiat_amount, COALESCE(token_amount,0),
		       currency, COALESCE(rate,0), COALESCE(fee,0), pay_account_name, deposit_id, created_at
		FROM paj_orders
		WHERE user_id = $1 AND order_type = 'onramp'
		ORDER BY created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer pajRows.Close()

	suppressDepositIDs := make(map[string]bool)
	var items []entities.ActivityItem

	for pajRows.Next() {
		var o entities.PajOrderForActivity
		var depositID *uuid.UUID
		var bankName *string
		if err := pajRows.Scan(&o.ID, &o.OrderType, &o.Status, &o.FiatAmount, &o.TokenAmount,
			&o.Currency, &o.Rate, &o.Fee, &bankName, &depositID, &o.CreatedAt); err != nil {
			s.logger.Warn("scan paj order", zap.Error(err))
			continue
		}
		o.BankAccountName = bankName
		o.DepositID = depositID
		items = append(items, entities.NormalizePajOrderToActivity(&o))
		if depositID != nil {
			suppressDepositIDs[depositID.String()] = true
		}
	}

	// Fetch deposits, excluding those linked to PAJ onramp orders
	depRows, err := s.db.QueryContext(ctx, `
		SELECT id, chain, tx_hash, token, amount, status, confirmed_at, created_at
		FROM deposits
		WHERE user_id = $1
		ORDER BY created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return items, err // return PAJ items even if deposits fail
	}
	defer depRows.Close()

	for depRows.Next() {
		var d entities.Deposit
		var confirmedAt sql.NullTime
		if err := depRows.Scan(&d.ID, &d.Chain, &d.TxHash, &d.Token, &d.Amount, &d.Status, &confirmedAt, &d.CreatedAt); err != nil {
			s.logger.Warn("scan deposit", zap.Error(err))
			continue
		}
		if confirmedAt.Valid {
			d.ConfirmedAt = &confirmedAt.Time
		}
		// Suppress deposits that are part of a PAJ onramp
		if suppressDepositIDs[d.ID.String()] {
			continue
		}
		items = append(items, entities.NormalizeDepositToActivity(&d))
	}

	return items, nil
}

func (s *Service) fetchWithdrawals(ctx context.Context, userID uuid.UUID, limit int) ([]entities.ActivityItem, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, withdrawal_type, currency, amount, fee_amount, destination_chain,
		       destination_address, tx_hash, status, created_at, completed_at
		FROM withdrawals
		WHERE user_id = $1
		ORDER BY created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []entities.ActivityItem
	for rows.Next() {
		var w entities.Withdrawal
		var destAddr, txHash *string
		var completedAt sql.NullTime
		if err := rows.Scan(&w.ID, &w.WithdrawalType, &w.Currency, &w.Amount, &w.FeeAmount,
			&w.DestinationChain, &destAddr, &txHash, &w.Status, &w.CreatedAt, &completedAt); err != nil {
			s.logger.Warn("scan withdrawal", zap.Error(err))
			continue
		}
		w.DestinationAddress = destAddr
		w.TxHash = txHash
		if completedAt.Valid {
			w.CompletedAt = &completedAt.Time
		}
		items = append(items, entities.NormalizeWithdrawalToActivity(&w))
	}
	return items, nil
}

func (s *Service) fetchP2PTransfers(ctx context.Context, userID uuid.UUID, limit int) ([]entities.ActivityItem, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, sender_id, recipient_identifier, amount, note, status, created_at, completed_at
		FROM p2p_transfers
		WHERE sender_id = $1 OR recipient_id = $1
		ORDER BY created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []entities.ActivityItem
	for rows.Next() {
		var t entities.P2PTransferForActivity
		var note *string
		var completedAt sql.NullTime
		var amount decimal.Decimal
		if err := rows.Scan(&t.ID, &t.SenderID, &t.RecipientIdentifier, &amount, &note, &t.Status, &t.CreatedAt, &completedAt); err != nil {
			s.logger.Warn("scan p2p transfer", zap.Error(err))
			continue
		}
		t.Amount = amount
		t.Note = note
		if completedAt.Valid {
			t.CompletedAt = &completedAt.Time
		}
		items = append(items, entities.NormalizeP2PToActivity(&t, userID))
	}
	return items, nil
}

func (s *Service) fetchPajOfframpOrders(ctx context.Context, userID uuid.UUID, limit int) ([]entities.ActivityItem, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT paj_order_id, order_type, status, fiat_amount, COALESCE(token_amount,0),
		       currency, COALESCE(rate,0), COALESCE(fee,0), pay_account_name, created_at
		FROM paj_orders
		WHERE user_id = $1 AND order_type = 'offramp'
		ORDER BY created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []entities.ActivityItem
	for rows.Next() {
		var o entities.PajOrderForActivity
		var bankName *string
		if err := rows.Scan(&o.ID, &o.OrderType, &o.Status, &o.FiatAmount, &o.TokenAmount,
			&o.Currency, &o.Rate, &o.Fee, &bankName, &o.CreatedAt); err != nil {
			s.logger.Warn("scan paj offramp", zap.Error(err))
			continue
		}
		o.BankAccountName = bankName
		items = append(items, entities.NormalizePajOrderToActivity(&o))
	}
	return items, nil
}
