package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// PortfolioSnapshotRepository handles portfolio snapshot persistence
type PortfolioSnapshotRepository struct {
	db *sqlx.DB
}

func NewPortfolioSnapshotRepository(db *sqlx.DB) *PortfolioSnapshotRepository {
	return &PortfolioSnapshotRepository{db: db}
}

func (r *PortfolioSnapshotRepository) Create(ctx context.Context, snapshot *entities.PortfolioSnapshot) error {
	query := `
		INSERT INTO portfolio_snapshots (
			id, user_id, total_value, cash_value, invested_value, total_cost_basis,
			total_gain_loss, total_gain_loss_pct, day_gain_loss, day_gain_loss_pct, snapshot_date, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (user_id, snapshot_date) DO UPDATE SET
			total_value = EXCLUDED.total_value, cash_value = EXCLUDED.cash_value,
			invested_value = EXCLUDED.invested_value, total_cost_basis = EXCLUDED.total_cost_basis,
			total_gain_loss = EXCLUDED.total_gain_loss, total_gain_loss_pct = EXCLUDED.total_gain_loss_pct,
			day_gain_loss = EXCLUDED.day_gain_loss, day_gain_loss_pct = EXCLUDED.day_gain_loss_pct`
	_, err := r.db.ExecContext(ctx, query,
		snapshot.ID, snapshot.UserID, snapshot.TotalValue, snapshot.CashValue, snapshot.InvestedValue,
		snapshot.TotalCostBasis, snapshot.TotalGainLoss, snapshot.TotalGainLossPct,
		snapshot.DayGainLoss, snapshot.DayGainLossPct, snapshot.SnapshotDate, snapshot.CreatedAt)
	return err
}

func (r *PortfolioSnapshotRepository) GetByUserIDAndDateRange(ctx context.Context, userID uuid.UUID, startDate, endDate time.Time) ([]*entities.PortfolioSnapshot, error) {
	var snapshots []*entities.PortfolioSnapshot
	query := `SELECT * FROM portfolio_snapshots WHERE user_id = $1 AND snapshot_date BETWEEN $2 AND $3 ORDER BY snapshot_date ASC`
	err := r.db.SelectContext(ctx, &snapshots, query, userID, startDate, endDate)
	return snapshots, err
}

func (r *PortfolioSnapshotRepository) GetLatestByUserID(ctx context.Context, userID uuid.UUID) (*entities.PortfolioSnapshot, error) {
	var snapshot entities.PortfolioSnapshot
	query := `SELECT * FROM portfolio_snapshots WHERE user_id = $1 ORDER BY snapshot_date DESC LIMIT 1`
	err := r.db.GetContext(ctx, &snapshot, query, userID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &snapshot, err
}

func (r *PortfolioSnapshotRepository) GetByDate(ctx context.Context, userID uuid.UUID, date time.Time) (*entities.PortfolioSnapshot, error) {
	var snapshot entities.PortfolioSnapshot
	query := `SELECT * FROM portfolio_snapshots WHERE user_id = $1 AND snapshot_date = $2`
	err := r.db.GetContext(ctx, &snapshot, query, userID, date)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &snapshot, err
}

// ScheduledInvestmentRepository handles scheduled investment persistence
type ScheduledInvestmentRepository struct {
	db *sqlx.DB
}

func NewScheduledInvestmentRepository(db *sqlx.DB) *ScheduledInvestmentRepository {
	return &ScheduledInvestmentRepository{db: db}
}

func (r *ScheduledInvestmentRepository) Create(ctx context.Context, si *entities.ScheduledInvestment) error {
	query := `
		INSERT INTO scheduled_investments (
			id, user_id, name, symbol, basket_id, amount, frequency, day_of_week, day_of_month,
			next_execution_at, status, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`
	_, err := r.db.ExecContext(ctx, query,
		si.ID, si.UserID, si.Name, si.Symbol, si.BasketID, si.Amount, si.Frequency,
		si.DayOfWeek, si.DayOfMonth, si.NextExecutionAt, si.Status, si.CreatedAt, si.UpdatedAt)
	return err
}

func (r *ScheduledInvestmentRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.ScheduledInvestment, error) {
	var si entities.ScheduledInvestment
	err := r.db.GetContext(ctx, &si, `SELECT * FROM scheduled_investments WHERE id = $1`, id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &si, err
}

func (r *ScheduledInvestmentRepository) GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.ScheduledInvestment, error) {
	var investments []*entities.ScheduledInvestment
	query := `SELECT * FROM scheduled_investments WHERE user_id = $1 ORDER BY created_at DESC`
	err := r.db.SelectContext(ctx, &investments, query, userID)
	return investments, err
}

func (r *ScheduledInvestmentRepository) GetDueForExecution(ctx context.Context, before time.Time) ([]*entities.ScheduledInvestment, error) {
	var investments []*entities.ScheduledInvestment
	query := `SELECT * FROM scheduled_investments WHERE status = 'active' AND next_execution_at <= $1`
	err := r.db.SelectContext(ctx, &investments, query, before)
	return investments, err
}

func (r *ScheduledInvestmentRepository) Update(ctx context.Context, si *entities.ScheduledInvestment) error {
	query := `
		UPDATE scheduled_investments SET
			name = $2, amount = $3, frequency = $4, day_of_week = $5, day_of_month = $6,
			next_execution_at = $7, last_executed_at = $8, status = $9,
			total_invested = $10, execution_count = $11, updated_at = $12
		WHERE id = $1`
	_, err := r.db.ExecContext(ctx, query,
		si.ID, si.Name, si.Amount, si.Frequency, si.DayOfWeek, si.DayOfMonth,
		si.NextExecutionAt, si.LastExecutedAt, si.Status, si.TotalInvested, si.ExecutionCount, time.Now())
	return err
}

func (r *ScheduledInvestmentRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE scheduled_investments SET status = $2, updated_at = $3 WHERE id = $1`, id, status, time.Now())
	return err
}

func (r *ScheduledInvestmentRepository) CreateExecution(ctx context.Context, exec *entities.ScheduledInvestmentExecution) error {
	query := `INSERT INTO scheduled_investment_executions (id, scheduled_investment_id, order_id, amount, status, error_message, executed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`
	_, err := r.db.ExecContext(ctx, query, exec.ID, exec.ScheduledInvestmentID, exec.OrderID, exec.Amount, exec.Status, exec.ErrorMessage, exec.ExecutedAt)
	return err
}

func (r *ScheduledInvestmentRepository) GetExecutions(ctx context.Context, scheduleID uuid.UUID, limit int) ([]*entities.ScheduledInvestmentExecution, error) {
	var execs []*entities.ScheduledInvestmentExecution
	query := `SELECT * FROM scheduled_investment_executions WHERE scheduled_investment_id = $1 ORDER BY executed_at DESC LIMIT $2`
	err := r.db.SelectContext(ctx, &execs, query, scheduleID, limit)
	return execs, err
}

// RebalancingConfigRepository handles rebalancing config persistence
type RebalancingConfigRepository struct {
	db *sqlx.DB
}

func NewRebalancingConfigRepository(db *sqlx.DB) *RebalancingConfigRepository {
	return &RebalancingConfigRepository{db: db}
}

func (r *RebalancingConfigRepository) Create(ctx context.Context, config *entities.RebalancingConfig) error {
	allocJSON, _ := json.Marshal(config.TargetAllocations)
	query := `
		INSERT INTO rebalancing_configs (id, user_id, name, target_allocations, threshold_pct, frequency, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`
	_, err := r.db.ExecContext(ctx, query,
		config.ID, config.UserID, config.Name, allocJSON, config.ThresholdPct, config.Frequency, config.Status, config.CreatedAt, config.UpdatedAt)
	return err
}

func (r *RebalancingConfigRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.RebalancingConfig, error) {
	var config rebalancingConfigRow
	err := r.db.GetContext(ctx, &config, `SELECT * FROM rebalancing_configs WHERE id = $1`, id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return config.toEntity()
}

func (r *RebalancingConfigRepository) GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.RebalancingConfig, error) {
	var rows []rebalancingConfigRow
	err := r.db.SelectContext(ctx, &rows, `SELECT * FROM rebalancing_configs WHERE user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	configs := make([]*entities.RebalancingConfig, len(rows))
	for i, row := range rows {
		configs[i], _ = row.toEntity()
	}
	return configs, nil
}

func (r *RebalancingConfigRepository) Update(ctx context.Context, config *entities.RebalancingConfig) error {
	allocJSON, _ := json.Marshal(config.TargetAllocations)
	query := `UPDATE rebalancing_configs SET name = $2, target_allocations = $3, threshold_pct = $4, frequency = $5, last_rebalanced_at = $6, status = $7, updated_at = $8 WHERE id = $1`
	_, err := r.db.ExecContext(ctx, query, config.ID, config.Name, allocJSON, config.ThresholdPct, config.Frequency, config.LastRebalancedAt, config.Status, time.Now())
	return err
}

func (r *RebalancingConfigRepository) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM rebalancing_configs WHERE id = $1`, id)
	return err
}

type rebalancingConfigRow struct {
	ID                uuid.UUID       `db:"id"`
	UserID            uuid.UUID       `db:"user_id"`
	Name              string          `db:"name"`
	TargetAllocations []byte          `db:"target_allocations"`
	ThresholdPct      decimal.Decimal `db:"threshold_pct"`
	Frequency         *string         `db:"frequency"`
	LastRebalancedAt  *time.Time      `db:"last_rebalanced_at"`
	Status            string          `db:"status"`
	CreatedAt         time.Time       `db:"created_at"`
	UpdatedAt         time.Time       `db:"updated_at"`
}

func (r rebalancingConfigRow) toEntity() (*entities.RebalancingConfig, error) {
	var allocs map[string]decimal.Decimal
	if err := json.Unmarshal(r.TargetAllocations, &allocs); err != nil {
		return nil, err
	}
	return &entities.RebalancingConfig{
		ID: r.ID, UserID: r.UserID, Name: r.Name, TargetAllocations: allocs,
		ThresholdPct: r.ThresholdPct, Frequency: r.Frequency, LastRebalancedAt: r.LastRebalancedAt,
		Status: r.Status, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}, nil
}

// MarketAlertRepository handles market alert persistence
type MarketAlertRepository struct {
	db *sqlx.DB
}

func NewMarketAlertRepository(db *sqlx.DB) *MarketAlertRepository {
	return &MarketAlertRepository{db: db}
}

func (r *MarketAlertRepository) Create(ctx context.Context, alert *entities.MarketAlert) error {
	query := `INSERT INTO market_alerts (id, user_id, symbol, alert_type, condition_value, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	_, err := r.db.ExecContext(ctx, query, alert.ID, alert.UserID, alert.Symbol, alert.AlertType, alert.ConditionValue, alert.Status, alert.CreatedAt, alert.UpdatedAt)
	return err
}

func (r *MarketAlertRepository) GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.MarketAlert, error) {
	var alerts []*entities.MarketAlert
	err := r.db.SelectContext(ctx, &alerts, `SELECT * FROM market_alerts WHERE user_id = $1 ORDER BY created_at DESC`, userID)
	return alerts, err
}

func (r *MarketAlertRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.MarketAlert, error) {
	var alert entities.MarketAlert
	err := r.db.GetContext(ctx, &alert, `SELECT * FROM market_alerts WHERE id = $1`, id)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return nil, nil
		}
		return nil, err
	}
	return &alert, nil
}

func (r *MarketAlertRepository) GetActiveBySymbol(ctx context.Context, symbol string) ([]*entities.MarketAlert, error) {
	var alerts []*entities.MarketAlert
	err := r.db.SelectContext(ctx, &alerts, `SELECT * FROM market_alerts WHERE symbol = $1 AND status = 'active'`, symbol)
	return alerts, err
}

func (r *MarketAlertRepository) GetAllActive(ctx context.Context) ([]*entities.MarketAlert, error) {
	var alerts []*entities.MarketAlert
	err := r.db.SelectContext(ctx, &alerts, `SELECT * FROM market_alerts WHERE status = 'active'`)
	return alerts, err
}

func (r *MarketAlertRepository) MarkTriggered(ctx context.Context, id uuid.UUID, currentPrice decimal.Decimal) error {
	now := time.Now()
	query := `UPDATE market_alerts SET triggered = true, triggered_at = $2, current_price = $3, status = 'triggered', updated_at = $4 WHERE id = $1`
	_, err := r.db.ExecContext(ctx, query, id, now, currentPrice, now)
	return err
}

func (r *MarketAlertRepository) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM market_alerts WHERE id = $1`, id)
	return err
}
