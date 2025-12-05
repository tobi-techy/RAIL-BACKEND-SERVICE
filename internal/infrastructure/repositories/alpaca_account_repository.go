package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// AlpacaAccountRepository handles Alpaca account persistence
type AlpacaAccountRepository struct {
	db *sqlx.DB
}

func NewAlpacaAccountRepository(db *sqlx.DB) *AlpacaAccountRepository {
	return &AlpacaAccountRepository{db: db}
}

func (r *AlpacaAccountRepository) Create(ctx context.Context, account *entities.AlpacaAccount) error {
	query := `
		INSERT INTO alpaca_accounts (
			id, user_id, alpaca_account_id, alpaca_account_number, status, account_type,
			currency, buying_power, cash, portfolio_value, trading_blocked, transfers_blocked,
			account_blocked, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`

	_, err := r.db.ExecContext(ctx, query,
		account.ID, account.UserID, account.AlpacaAccountID, account.AlpacaAccountNumber,
		account.Status, account.AccountType, account.Currency, account.BuyingPower,
		account.Cash, account.PortfolioValue, account.TradingBlocked, account.TransfersBlocked,
		account.AccountBlocked, account.CreatedAt, account.UpdatedAt)
	return err
}

func (r *AlpacaAccountRepository) GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.AlpacaAccount, error) {
	var account entities.AlpacaAccount
	query := `SELECT * FROM alpaca_accounts WHERE user_id = $1`
	err := r.db.GetContext(ctx, &account, query, userID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &account, err
}

func (r *AlpacaAccountRepository) GetByAlpacaID(ctx context.Context, alpacaAccountID string) (*entities.AlpacaAccount, error) {
	var account entities.AlpacaAccount
	query := `SELECT * FROM alpaca_accounts WHERE alpaca_account_id = $1`
	err := r.db.GetContext(ctx, &account, query, alpacaAccountID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &account, err
}

func (r *AlpacaAccountRepository) Update(ctx context.Context, account *entities.AlpacaAccount) error {
	query := `
		UPDATE alpaca_accounts SET
			status = $2, buying_power = $3, cash = $4, portfolio_value = $5,
			trading_blocked = $6, transfers_blocked = $7, account_blocked = $8,
			last_synced_at = $9, updated_at = $10
		WHERE id = $1`

	account.UpdatedAt = time.Now()
	_, err := r.db.ExecContext(ctx, query,
		account.ID, account.Status, account.BuyingPower, account.Cash, account.PortfolioValue,
		account.TradingBlocked, account.TransfersBlocked, account.AccountBlocked,
		account.LastSyncedAt, account.UpdatedAt)
	return err
}

func (r *AlpacaAccountRepository) UpdateStatus(ctx context.Context, userID uuid.UUID, status entities.AlpacaAccountStatus) error {
	query := `UPDATE alpaca_accounts SET status = $2, updated_at = $3 WHERE user_id = $1`
	_, err := r.db.ExecContext(ctx, query, userID, status, time.Now())
	return err
}

// InvestmentOrderRepository handles investment order persistence
type InvestmentOrderRepository struct {
	db *sqlx.DB
}

func NewInvestmentOrderRepository(db *sqlx.DB) *InvestmentOrderRepository {
	return &InvestmentOrderRepository{db: db}
}

func (r *InvestmentOrderRepository) Create(ctx context.Context, order *entities.InvestmentOrder) error {
	query := `
		INSERT INTO investment_orders (
			id, user_id, alpaca_account_id, alpaca_order_id, client_order_id, basket_id,
			symbol, side, order_type, time_in_force, qty, notional, filled_qty,
			filled_avg_price, limit_price, stop_price, status, commission,
			submitted_at, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)`

	_, err := r.db.ExecContext(ctx, query,
		order.ID, order.UserID, order.AlpacaAccountID, order.AlpacaOrderID, order.ClientOrderID,
		order.BasketID, order.Symbol, order.Side, order.OrderType, order.TimeInForce,
		order.Qty, order.Notional, order.FilledQty, order.FilledAvgPrice, order.LimitPrice,
		order.StopPrice, order.Status, order.Commission, order.SubmittedAt, order.CreatedAt, order.UpdatedAt)
	return err
}

func (r *InvestmentOrderRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.InvestmentOrder, error) {
	var order entities.InvestmentOrder
	err := r.db.GetContext(ctx, &order, `SELECT * FROM investment_orders WHERE id = $1`, id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &order, err
}

func (r *InvestmentOrderRepository) GetByAlpacaOrderID(ctx context.Context, alpacaOrderID string) (*entities.InvestmentOrder, error) {
	var order entities.InvestmentOrder
	err := r.db.GetContext(ctx, &order, `SELECT * FROM investment_orders WHERE alpaca_order_id = $1`, alpacaOrderID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &order, err
}

func (r *InvestmentOrderRepository) GetByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.InvestmentOrder, error) {
	var orders []*entities.InvestmentOrder
	query := `SELECT * FROM investment_orders WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`
	err := r.db.SelectContext(ctx, &orders, query, userID, limit, offset)
	return orders, err
}

func (r *InvestmentOrderRepository) UpdateFromAlpaca(ctx context.Context, alpacaOrderID string, status entities.AlpacaOrderStatus, filledQty, filledAvgPrice *string, filledAt *time.Time) error {
	query := `
		UPDATE investment_orders SET
			status = $2, filled_qty = COALESCE($3::decimal, filled_qty),
			filled_avg_price = COALESCE($4::decimal, filled_avg_price),
			filled_at = COALESCE($5, filled_at), updated_at = $6
		WHERE alpaca_order_id = $1`
	_, err := r.db.ExecContext(ctx, query, alpacaOrderID, status, filledQty, filledAvgPrice, filledAt, time.Now())
	return err
}

// InvestmentPositionRepository handles position persistence
type InvestmentPositionRepository struct {
	db *sqlx.DB
}

func NewInvestmentPositionRepository(db *sqlx.DB) *InvestmentPositionRepository {
	return &InvestmentPositionRepository{db: db}
}

func (r *InvestmentPositionRepository) Upsert(ctx context.Context, pos *entities.InvestmentPosition) error {
	query := `
		INSERT INTO investment_positions (
			id, user_id, alpaca_account_id, symbol, asset_id, qty, qty_available,
			avg_entry_price, market_value, cost_basis, unrealized_pl, unrealized_plpc,
			current_price, lastday_price, change_today, side, last_synced_at, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
		ON CONFLICT (user_id, symbol) DO UPDATE SET
			qty = EXCLUDED.qty, qty_available = EXCLUDED.qty_available,
			avg_entry_price = EXCLUDED.avg_entry_price, market_value = EXCLUDED.market_value,
			cost_basis = EXCLUDED.cost_basis, unrealized_pl = EXCLUDED.unrealized_pl,
			unrealized_plpc = EXCLUDED.unrealized_plpc, current_price = EXCLUDED.current_price,
			lastday_price = EXCLUDED.lastday_price, change_today = EXCLUDED.change_today,
			last_synced_at = EXCLUDED.last_synced_at, updated_at = EXCLUDED.updated_at`

	_, err := r.db.ExecContext(ctx, query,
		pos.ID, pos.UserID, pos.AlpacaAccountID, pos.Symbol, pos.AssetID, pos.Qty, pos.QtyAvailable,
		pos.AvgEntryPrice, pos.MarketValue, pos.CostBasis, pos.UnrealizedPL, pos.UnrealizedPLPC,
		pos.CurrentPrice, pos.LastdayPrice, pos.ChangeToday, pos.Side, pos.LastSyncedAt, pos.CreatedAt, pos.UpdatedAt)
	return err
}

func (r *InvestmentPositionRepository) GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.InvestmentPosition, error) {
	var positions []*entities.InvestmentPosition
	err := r.db.SelectContext(ctx, &positions, `SELECT * FROM investment_positions WHERE user_id = $1`, userID)
	return positions, err
}

func (r *InvestmentPositionRepository) GetByUserAndSymbol(ctx context.Context, userID uuid.UUID, symbol string) (*entities.InvestmentPosition, error) {
	var pos entities.InvestmentPosition
	err := r.db.GetContext(ctx, &pos, `SELECT * FROM investment_positions WHERE user_id = $1 AND symbol = $2`, userID, symbol)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &pos, err
}

func (r *InvestmentPositionRepository) DeleteByUserAndSymbol(ctx context.Context, userID uuid.UUID, symbol string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM investment_positions WHERE user_id = $1 AND symbol = $2`, userID, symbol)
	return err
}

func (r *InvestmentPositionRepository) DeleteAllByUser(ctx context.Context, userID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM investment_positions WHERE user_id = $1`, userID)
	return err
}

// AlpacaEventRepository handles event persistence
type AlpacaEventRepository struct {
	db *sqlx.DB
}

func NewAlpacaEventRepository(db *sqlx.DB) *AlpacaEventRepository {
	return &AlpacaEventRepository{db: db}
}

func (r *AlpacaEventRepository) Create(ctx context.Context, event *entities.AlpacaEvent) error {
	query := `
		INSERT INTO alpaca_events (id, user_id, alpaca_account_id, event_type, event_id, payload, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`
	_, err := r.db.ExecContext(ctx, query, event.ID, event.UserID, event.AlpacaAccountID, event.EventType, event.EventID, event.Payload, event.CreatedAt)
	return err
}

func (r *AlpacaEventRepository) MarkProcessed(ctx context.Context, id uuid.UUID, errorMsg *string) error {
	now := time.Now()
	query := `UPDATE alpaca_events SET processed = true, processed_at = $2, error_message = $3 WHERE id = $1`
	_, err := r.db.ExecContext(ctx, query, id, now, errorMsg)
	return err
}

func (r *AlpacaEventRepository) GetUnprocessed(ctx context.Context, limit int) ([]*entities.AlpacaEvent, error) {
	var events []*entities.AlpacaEvent
	query := `SELECT * FROM alpaca_events WHERE processed = false ORDER BY created_at ASC LIMIT $1`
	err := r.db.SelectContext(ctx, &events, query, limit)
	return events, err
}

// AlpacaInstantFundingRepository handles instant funding persistence
type AlpacaInstantFundingRepository struct {
	db *sqlx.DB
}

func NewAlpacaInstantFundingRepository(db *sqlx.DB) *AlpacaInstantFundingRepository {
	return &AlpacaInstantFundingRepository{db: db}
}

func (r *AlpacaInstantFundingRepository) Create(ctx context.Context, funding *entities.AlpacaInstantFunding) error {
	query := `
		INSERT INTO alpaca_instant_funding (
			id, user_id, alpaca_account_id, alpaca_transfer_id, source_account_no,
			amount, remaining_payable, total_interest, status, deadline, system_date, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`
	_, err := r.db.ExecContext(ctx, query,
		funding.ID, funding.UserID, funding.AlpacaAccountID, funding.AlpacaTransferID,
		funding.SourceAccountNo, funding.Amount, funding.RemainingPayable, funding.TotalInterest,
		funding.Status, funding.Deadline, funding.SystemDate, funding.CreatedAt, funding.UpdatedAt)
	return err
}

func (r *AlpacaInstantFundingRepository) GetPendingByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.AlpacaInstantFunding, error) {
	var fundings []*entities.AlpacaInstantFunding
	query := `SELECT * FROM alpaca_instant_funding WHERE user_id = $1 AND status IN ('PENDING', 'EXECUTED') ORDER BY created_at DESC`
	err := r.db.SelectContext(ctx, &fundings, query, userID)
	return fundings, err
}

func (r *AlpacaInstantFundingRepository) UpdateStatus(ctx context.Context, alpacaTransferID, status string, settlementID *string, settledAt *time.Time) error {
	query := `
		UPDATE alpaca_instant_funding SET
			status = $2, settlement_id = COALESCE($3, settlement_id),
			settled_at = COALESCE($4, settled_at), updated_at = $5
		WHERE alpaca_transfer_id = $1`
	_, err := r.db.ExecContext(ctx, query, alpacaTransferID, status, settlementID, settledAt, time.Now())
	return err
}

func (r *AlpacaInstantFundingRepository) GetByTransferID(ctx context.Context, transferID string) (*entities.AlpacaInstantFunding, error) {
	var funding entities.AlpacaInstantFunding
	err := r.db.GetContext(ctx, &funding, `SELECT * FROM alpaca_instant_funding WHERE alpaca_transfer_id = $1`, transferID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get instant funding: %w", err)
	}
	return &funding, nil
}
