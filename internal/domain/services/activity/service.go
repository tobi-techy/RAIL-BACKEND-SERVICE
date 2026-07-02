package activity

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
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
	const sourceCount = 7
	ch := make(chan result, sourceCount)

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
	go func() {
		items, err := s.fetchMiriamActions(ctx, userID, limit+offset+10)
		ch <- result{items, err}
	}()
	go func() {
		items, err := s.fetchRampOrders(ctx, userID, limit+offset+10)
		ch <- result{items, err}
	}()
	go func() {
		items, err := s.fetchStashTransfers(ctx, userID, limit+offset+10)
		ch <- result{items, err}
	}()

	var all []entities.ActivityItem
	for i := 0; i < sourceCount; i++ {
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
	// Get PAJ onramp order IDs to suppress their linked deposits
	pajRows, err := s.db.QueryContext(ctx, `
		SELECT paj_order_id, order_type, status, fiat_amount, COALESCE(token_amount,0),
		       currency, COALESCE(rate,0), COALESCE(fee,0), pay_account_name, created_at
		FROM paj_orders
		WHERE user_id = $1 AND order_type = 'onramp'
		ORDER BY created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer pajRows.Close()

	suppressDepositKeys := make(map[string]bool)
	var items []entities.ActivityItem

	for pajRows.Next() {
		var o entities.PajOrderForActivity
		var bankName *string
		if err := pajRows.Scan(&o.ID, &o.OrderType, &o.Status, &o.FiatAmount, &o.TokenAmount,
			&o.Currency, &o.Rate, &o.Fee, &bankName, &o.CreatedAt); err != nil {
			s.logger.Warn("scan paj order", zap.Error(err))
			continue
		}
		o.BankAccountName = bankName
		items = append(items, entities.NormalizePajOrderToActivity(&o))
		// Deposits created by PAJ onramp use idempotency_key = "paj-onramp-{orderID}"
		suppressDepositKeys["paj-onramp-"+o.ID] = true
	}

	// Fetch deposits
	depRows, err := s.db.QueryContext(ctx, `
		SELECT id, chain, tx_hash, token, amount, status, confirmed_at, created_at, COALESCE(idempotency_key, '')
		FROM deposits
		WHERE user_id = $1
		ORDER BY created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return items, err
	}
	defer depRows.Close()

	for depRows.Next() {
		var d entities.Deposit
		var confirmedAt sql.NullTime
		var idempotencyKey string
		if err := depRows.Scan(&d.ID, &d.Chain, &d.TxHash, &d.Token, &d.Amount, &d.Status, &confirmedAt, &d.CreatedAt, &idempotencyKey); err != nil {
			s.logger.Warn("scan deposit", zap.Error(err))
			continue
		}
		if confirmedAt.Valid {
			d.ConfirmedAt = &confirmedAt.Time
		}
		// Suppress deposits that are part of a PAJ onramp, and RampHub-onramp
		// backstop credits — the ramp order row is the user-facing entry.
		if suppressDepositKeys[idempotencyKey] || strings.HasPrefix(idempotencyKey, "ramphub-onramp-") {
			continue
		}
		items = append(items, entities.NormalizeDepositToActivity(&d))
	}

	return items, nil
}

// fetchRampOrders returns RampHub NGN on/off ramp orders (RampHub primary,
// Paj-fallback orders live in paj_orders and are fetched separately).
func (s *Service) fetchRampOrders(ctx context.Context, userID uuid.UUID, limit int) ([]entities.ActivityItem, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT ramphub_transaction_id, order_type, status, COALESCE(fiat_amount,0), COALESCE(token_amount,0),
		       COALESCE(currency,'NGN'), COALESCE(rate,0), account_name, account_number, bank_name, created_at
		FROM ramphub_orders
		WHERE user_id = $1
		ORDER BY created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []entities.ActivityItem
	for rows.Next() {
		var o entities.PajOrderForActivity
		var acctName, acctNumber, bankName *string
		if err := rows.Scan(&o.ID, &o.OrderType, &o.Status, &o.FiatAmount, &o.TokenAmount,
			&o.Currency, &o.Rate, &acctName, &acctNumber, &bankName, &o.CreatedAt); err != nil {
			s.logger.Warn("scan ramphub order", zap.Error(err))
			continue
		}
		o.BankAccountName = acctName
		o.BankAccountNumber = acctNumber
		o.BankName = bankName
		o.SourceType = "ramphub_order"
		items = append(items, entities.NormalizePajOrderToActivity(&o))
	}
	if err := rows.Err(); err != nil {
		return items, fmt.Errorf("iterate ramphub orders: %w", err)
	}
	return items, nil
}

// fetchStashTransfers returns spend↔stash moves (app fund-stash, emergency
// withdrawals). Miriam-initiated moves are audited separately in ai_action_audit
// and do not write stash_transfers rows, so there is no double counting.
func (s *Service) fetchStashTransfers(ctx context.Context, userID uuid.UUID, limit int) ([]entities.ActivityItem, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, amount, COALESCE(direction,'spending_to_stash'), status, created_at, completed_at
		FROM stash_transfers
		WHERE user_id = $1
		ORDER BY created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []entities.ActivityItem
	for rows.Next() {
		var t entities.StashTransfer
		var completedAt sql.NullTime
		if err := rows.Scan(&t.ID, &t.UserID, &t.Amount, &t.Direction, &t.Status, &t.CreatedAt, &completedAt); err != nil {
			s.logger.Warn("scan stash transfer", zap.Error(err))
			continue
		}
		if completedAt.Valid {
			t.CompletedAt = &completedAt.Time
		}
		items = append(items, entities.NormalizeStashTransferToActivity(&t))
	}
	if err := rows.Err(); err != nil {
		return items, fmt.Errorf("iterate stash transfers: %w", err)
	}
	return items, nil
}

func (s *Service) fetchWithdrawals(ctx context.Context, userID uuid.UUID, limit int) ([]entities.ActivityItem, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT w.id, w.withdrawal_type, w.currency, w.amount, w.fee_amount, w.destination_chain,
		       w.destination_address, w.tx_hash, w.status, w.created_at, w.completed_at,
		       w.narration, ba.bank_name
		FROM withdrawals w
		LEFT JOIN bank_accounts ba ON ba.id = w.bank_account_id
		WHERE w.user_id = $1
		ORDER BY w.created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []entities.ActivityItem
	for rows.Next() {
		var w entities.Withdrawal
		var destAddr, txHash, narration, bankName *string
		var completedAt sql.NullTime
		if err := rows.Scan(&w.ID, &w.WithdrawalType, &w.Currency, &w.Amount, &w.FeeAmount,
			&w.DestinationChain, &destAddr, &txHash, &w.Status, &w.CreatedAt, &completedAt,
			&narration, &bankName); err != nil {
			s.logger.Warn("scan withdrawal", zap.Error(err))
			continue
		}
		w.DestinationAddress = destAddr
		w.TxHash = txHash
		w.Narration = narration
		if completedAt.Valid {
			w.CompletedAt = &completedAt.Time
		}
		item := entities.NormalizeWithdrawalToActivity(&w)
		if bankName != nil {
			item.BankName = *bankName
			item.ReceiverName = *bankName
		}
		items = append(items, item)
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

// fetchMiriamActions returns money-moving Miriam (financial agent) actions for
// the transaction feed. Only internal transfer_funds entries are surfaced —
// initiate_withdrawal actions are intentionally excluded because the
// corresponding row in the `withdrawals` table is already surfaced via
// fetchWithdrawals, and showing both would produce duplicate feed entries.
func (s *Service) fetchMiriamActions(ctx context.Context, userID uuid.UUID, limit int) ([]entities.ActivityItem, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, action, status, error_message, params, created_at
		FROM ai_action_audit
		WHERE user_id = $1
		  AND action = 'transfer_funds'
		  AND status IN ('executed', 'failed')
		ORDER BY created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			s.logger.Warn("fetchMiriamActions: rows.Close failed", zap.Error(err))
		}
	}()

	var items []entities.ActivityItem
	for rows.Next() {
		var (
			id         uuid.UUID
			action     string
			status     string
			errMsg     string
			paramsJSON []byte
			createdAt  time.Time
		)
		if err := rows.Scan(&id, &action, &status, &errMsg, &paramsJSON, &createdAt); err != nil {
			s.logger.Warn("scan miriam action", zap.Error(err))
			continue
		}
		params := map[string]interface{}{}
		if len(paramsJSON) > 0 {
			if err := json.Unmarshal(paramsJSON, &params); err != nil {
				// Corrupt or unexpectedly-shaped params — log and surface a
				// degraded entry rather than silently mislabel it $0.00.
				s.logger.Warn("miriam action params unmarshal failed",
					zap.String("action_id", id.String()),
					zap.Error(err))
			}
		}

		amount := parseDecimalParam(params, "amount", id, s.logger)
		from, _ := params["from"].(string)
		to, _ := params["to"].(string)
		currency, _ := params["currency"].(string)
		emergency := miriamActionIsEmergency(params)

		entry := &entities.MiriamActionForActivity{
			ID:           id,
			Action:       action,
			Status:       status,
			ErrorMessage: errMsg,
			From:         from,
			To:           to,
			Amount:       amount,
			Currency:     currency,
			Emergency:    emergency,
			CreatedAt:    createdAt,
		}
		items = append(items, entities.NormalizeMiriamActionToActivity(entry))
	}
	return items, nil
}

// miriamActionIsEmergency detects an emergency stash-to-spend transfer by
// reading the `impact.emergency_withdrawal` flag set in orchestrator_actions.go
// when the user is in the stash lock window.
func miriamActionIsEmergency(params map[string]interface{}) bool {
	impact, ok := params["impact"].(map[string]interface{})
	if !ok {
		return false
	}
	_, isEmergency := impact["emergency_withdrawal"]
	return isEmergency
}

// parseDecimalParam reads a string/float/int decimal from a params map. Returns
// zero if the key is absent or unparseable; logs (at warn) when an existing
// value can't be parsed or is an unexpected type so data-quality regressions
// don't silently mislabel feed entries as $0.00.
func parseDecimalParam(params map[string]interface{}, key string, actionID uuid.UUID, logger *zap.Logger) decimal.Decimal {
	v, ok := params[key]
	if !ok {
		return decimal.Zero
	}
	switch x := v.(type) {
	case string:
		d, err := decimal.NewFromString(x)
		if err != nil {
			if logger != nil {
				logger.Warn("activity feed: decimal param parse failed",
					zap.String("action_id", actionID.String()),
					zap.String("key", key),
					zap.String("value", x),
					zap.Error(err))
			}
			return decimal.Zero
		}
		return d
	case float64:
		return decimal.NewFromFloat(x)
	case int:
		return decimal.NewFromInt(int64(x))
	case int64:
		return decimal.NewFromInt(x)
	}
	if logger != nil {
		logger.Warn("activity feed: decimal param unexpected type",
			zap.String("action_id", actionID.String()),
			zap.String("key", key),
			zap.String("go_type", fmt.Sprintf("%T", v)))
	}
	return decimal.Zero
}

func (s *Service) fetchPajOfframpOrders(ctx context.Context, userID uuid.UUID, limit int) ([]entities.ActivityItem, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT paj_order_id, order_type, status, fiat_amount, COALESCE(token_amount,0),
		       currency, COALESCE(rate,0), COALESCE(fee,0), bank_account_name, bank_account_number, bank_id, created_at
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
		var bankAcctName, bankAcctNumber, bankID *string
		if err := rows.Scan(&o.ID, &o.OrderType, &o.Status, &o.FiatAmount, &o.TokenAmount,
			&o.Currency, &o.Rate, &o.Fee, &bankAcctName, &bankAcctNumber, &bankID, &o.CreatedAt); err != nil {
			s.logger.Warn("scan paj offramp", zap.Error(err))
			continue
		}
		o.BankAccountName = bankAcctName
		o.BankAccountNumber = bankAcctNumber
		o.BankName = bankID // bankId maps to bank name via PAJ banks list
		items = append(items, entities.NormalizePajOrderToActivity(&o))
	}
	return items, nil
}
