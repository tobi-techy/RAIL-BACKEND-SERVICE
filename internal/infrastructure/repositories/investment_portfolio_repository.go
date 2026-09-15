package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

// ---------------------------------------------------------------------------
// Enrollments (user portfolios)
// ---------------------------------------------------------------------------

// InvestmentEnrollmentRepository persists user portfolio enrollments.
type InvestmentEnrollmentRepository struct {
	db *sqlx.DB
}

// NewInvestmentEnrollmentRepository creates a new enrollment repository.
func NewInvestmentEnrollmentRepository(db *sqlx.DB) *InvestmentEnrollmentRepository {
	return &InvestmentEnrollmentRepository{db: db}
}

const investmentEnrollmentColumns = `id, user_id, strategy_id, strategy_version, glider_portfolio_id,
	glider_strategy_id, chain, owner_account_id, agent_account_id, deposit_account_id, swig_role_id,
	status, automation_status, next_due_at, last_rebalance_at, total_value_usd, positions_as_of,
	last_sync_error, created_at, updated_at, closed_at`

// Create inserts a new enrollment.
func (r *InvestmentEnrollmentRepository) Create(ctx context.Context, enrollment *entities.InvestmentEnrollment) error {
	if enrollment == nil {
		return fmt.Errorf("investment enrollment is nil")
	}
	if enrollment.ID == uuid.Nil {
		enrollment.ID = uuid.New()
	}
	enrollment.CreatedAt = investmentTimeOrNow(enrollment.CreatedAt)
	enrollment.UpdatedAt = investmentTimeOrNow(enrollment.UpdatedAt)

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO investment_enrollments (id, user_id, strategy_id, strategy_version, glider_portfolio_id,
			glider_strategy_id, chain, owner_account_id, agent_account_id, deposit_account_id, swig_role_id,
			status, automation_status, next_due_at, last_rebalance_at, total_value_usd, positions_as_of,
			last_sync_error, created_at, updated_at, closed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)`,
		enrollment.ID, enrollment.UserID, enrollment.StrategyID, enrollment.StrategyVersion,
		enrollment.GliderPortfolioID, enrollment.GliderStrategyID, enrollment.Chain,
		enrollment.OwnerAccountID, enrollment.AgentAccountID, enrollment.DepositAccountID, enrollment.SwigRoleID,
		string(enrollment.Status), enrollment.AutomationStatus, enrollment.NextDueAt, enrollment.LastRebalanceAt,
		enrollment.TotalValueUSD, enrollment.PositionsAsOf, enrollment.LastSyncError,
		enrollment.CreatedAt, enrollment.UpdatedAt, enrollment.ClosedAt)
	if err != nil {
		return fmt.Errorf("create investment enrollment: %w", err)
	}
	return nil
}

// Update writes the mutable enrollment fields.
func (r *InvestmentEnrollmentRepository) Update(ctx context.Context, enrollment *entities.InvestmentEnrollment) error {
	if enrollment == nil {
		return fmt.Errorf("investment enrollment is nil")
	}
	enrollment.UpdatedAt = investmentTimeOrNow(enrollment.UpdatedAt)

	_, err := r.db.ExecContext(ctx, `
		UPDATE investment_enrollments SET
			strategy_version = $2, glider_portfolio_id = $3, glider_strategy_id = $4, chain = $5,
			owner_account_id = $6, agent_account_id = $7, deposit_account_id = $8, swig_role_id = $9,
			status = $10, automation_status = $11, next_due_at = $12, last_rebalance_at = $13,
			total_value_usd = $14, positions_as_of = $15, last_sync_error = $16,
			updated_at = $17, closed_at = $18
		WHERE id = $1`,
		enrollment.ID, enrollment.StrategyVersion, enrollment.GliderPortfolioID, enrollment.GliderStrategyID,
		enrollment.Chain, enrollment.OwnerAccountID, enrollment.AgentAccountID, enrollment.DepositAccountID,
		enrollment.SwigRoleID, string(enrollment.Status), enrollment.AutomationStatus, enrollment.NextDueAt,
		enrollment.LastRebalanceAt, enrollment.TotalValueUSD, enrollment.PositionsAsOf, enrollment.LastSyncError,
		enrollment.UpdatedAt, enrollment.ClosedAt)
	if err != nil {
		return fmt.Errorf("update investment enrollment: %w", err)
	}
	return nil
}

// GetByID returns an enrollment by id, or (nil, nil) when absent.
func (r *InvestmentEnrollmentRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.InvestmentEnrollment, error) {
	enrollment := &entities.InvestmentEnrollment{}
	err := r.db.GetContext(ctx, enrollment,
		`SELECT `+investmentEnrollmentColumns+` FROM investment_enrollments WHERE id = $1`, id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get investment enrollment: %w", err)
	}
	return enrollment, nil
}

// GetByUserAndStrategy returns the enrollment for one user/strategy pair.
func (r *InvestmentEnrollmentRepository) GetByUserAndStrategy(ctx context.Context, userID, strategyID uuid.UUID) (*entities.InvestmentEnrollment, error) {
	enrollment := &entities.InvestmentEnrollment{}
	err := r.db.GetContext(ctx, enrollment,
		`SELECT `+investmentEnrollmentColumns+`
		 FROM investment_enrollments WHERE user_id = $1 AND strategy_id = $2`, userID, strategyID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get investment enrollment by user/strategy: %w", err)
	}
	return enrollment, nil
}

// GetByPortfolioID resolves an enrollment from the provider portfolio id.
func (r *InvestmentEnrollmentRepository) GetByPortfolioID(ctx context.Context, portfolioID string) (*entities.InvestmentEnrollment, error) {
	enrollment := &entities.InvestmentEnrollment{}
	err := r.db.GetContext(ctx, enrollment,
		`SELECT `+investmentEnrollmentColumns+`
		 FROM investment_enrollments WHERE glider_portfolio_id = $1`, portfolioID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get investment enrollment by portfolio: %w", err)
	}
	return enrollment, nil
}

// ListByUser returns all of a user's enrollments.
func (r *InvestmentEnrollmentRepository) ListByUser(ctx context.Context, userID uuid.UUID) ([]*entities.InvestmentEnrollment, error) {
	enrollments := make([]*entities.InvestmentEnrollment, 0)
	err := r.db.SelectContext(ctx, &enrollments,
		`SELECT `+investmentEnrollmentColumns+`
		 FROM investment_enrollments WHERE user_id = $1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list investment enrollments: %w", err)
	}
	return enrollments, nil
}

// ListActive returns active enrollments for the sync/rebalance sweeps.
func (r *InvestmentEnrollmentRepository) ListActive(ctx context.Context, limit int) ([]*entities.InvestmentEnrollment, error) {
	if limit <= 0 {
		limit = 25
	}
	if limit > 500 {
		limit = 500
	}
	enrollments := make([]*entities.InvestmentEnrollment, 0)
	err := r.db.SelectContext(ctx, &enrollments,
		`SELECT `+investmentEnrollmentColumns+`
		 FROM investment_enrollments
		 WHERE status = $1
		 ORDER BY updated_at ASC
		 LIMIT $2`, string(entities.InvestmentEnrollmentActive), limit)
	if err != nil {
		return nil, fmt.Errorf("list active investment enrollments: %w", err)
	}
	return enrollments, nil
}

// ---------------------------------------------------------------------------
// Holdings (normalized position read model)
// ---------------------------------------------------------------------------

// InvestmentHoldingRepository persists normalized positions.
type InvestmentHoldingRepository struct {
	db *sqlx.DB
}

// NewInvestmentHoldingRepository creates a new holding repository.
func NewInvestmentHoldingRepository(db *sqlx.DB) *InvestmentHoldingRepository {
	return &InvestmentHoldingRepository{db: db}
}

const investmentHoldingColumns = `id, user_id, enrollment_id, asset_id, caip19, symbol, name,
	balance, balance_raw, decimals, price_usd, value_usd, weight_pct, source, as_of, updated_at`

// ReplaceForEnrollment atomically swaps an enrollment's holdings for the
// supplied snapshot.
func (r *InvestmentHoldingRepository) ReplaceForEnrollment(ctx context.Context, enrollmentID uuid.UUID, holdings []*entities.InvestmentHolding) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin holdings replacement: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM investment_holdings WHERE enrollment_id = $1`, enrollmentID); err != nil {
		return fmt.Errorf("delete investment holdings: %w", err)
	}

	for _, holding := range holdings {
		if holding == nil {
			continue
		}
		holding.EnrollmentID = enrollmentID
		if holding.ID == uuid.Nil {
			holding.ID = uuid.New()
		}
		holding.UpdatedAt = investmentTimeOrNow(holding.UpdatedAt)
		holding.AsOf = investmentTimeOrNow(holding.AsOf)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO investment_holdings (id, user_id, enrollment_id, asset_id, caip19, symbol, name,
				balance, balance_raw, decimals, price_usd, value_usd, weight_pct, source, as_of, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`,
			holding.ID, holding.UserID, holding.EnrollmentID, holding.AssetID, holding.CAIP19,
			holding.Symbol, holding.Name, holding.Balance, holding.BalanceRaw, holding.Decimals,
			holding.PriceUSD, holding.ValueUSD, holding.WeightPct, holding.Source, holding.AsOf, holding.UpdatedAt); err != nil {
			return fmt.Errorf("insert investment holding: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit holdings replacement: %w", err)
	}
	return nil
}

// ListByUser returns every holding a user owns across enrollments.
func (r *InvestmentHoldingRepository) ListByUser(ctx context.Context, userID uuid.UUID) ([]*entities.InvestmentHolding, error) {
	holdings := make([]*entities.InvestmentHolding, 0)
	err := r.db.SelectContext(ctx, &holdings,
		`SELECT `+investmentHoldingColumns+`
		 FROM investment_holdings WHERE user_id = $1 ORDER BY value_usd DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list investment holdings: %w", err)
	}
	return holdings, nil
}

// ListByEnrollment returns one enrollment's holdings.
func (r *InvestmentHoldingRepository) ListByEnrollment(ctx context.Context, enrollmentID uuid.UUID) ([]*entities.InvestmentHolding, error) {
	holdings := make([]*entities.InvestmentHolding, 0)
	err := r.db.SelectContext(ctx, &holdings,
		`SELECT `+investmentHoldingColumns+`
		 FROM investment_holdings WHERE enrollment_id = $1 ORDER BY value_usd DESC`, enrollmentID)
	if err != nil {
		return nil, fmt.Errorf("list investment holdings by enrollment: %w", err)
	}
	return holdings, nil
}

// ---------------------------------------------------------------------------
// Executions (auditable portfolio actions)
// ---------------------------------------------------------------------------

// InvestmentExecutionRepository persists auditable portfolio actions.
type InvestmentExecutionRepository struct {
	db *sqlx.DB
}

// NewInvestmentExecutionRepository creates a new execution repository.
func NewInvestmentExecutionRepository(db *sqlx.DB) *InvestmentExecutionRepository {
	return &InvestmentExecutionRepository{db: db}
}

const investmentExecutionColumns = `id, user_id, enrollment_id, strategy_id, strategy_version, kind,
	side, asset_id, symbol, requested_amount_usd, validated_amount_usd, status, idempotency_key,
	policy, provider, provider_operation_id, provider_tx_refs, failure_code, failure_reason,
	market_data_as_of, requested_by, confirmation_method, created_at, updated_at, completed_at`

// investmentExecutionRow carries the JSONB columns as raw bytes.
type investmentExecutionRow struct {
	ID                  uuid.UUID       `db:"id"`
	UserID              uuid.UUID       `db:"user_id"`
	EnrollmentID        *uuid.UUID      `db:"enrollment_id"`
	StrategyID          *uuid.UUID      `db:"strategy_id"`
	StrategyVersion     *int            `db:"strategy_version"`
	Kind                string          `db:"kind"`
	Side                string          `db:"side"`
	AssetID             *uuid.UUID      `db:"asset_id"`
	Symbol              string          `db:"symbol"`
	RequestedAmountUSD  decimal.Decimal `db:"requested_amount_usd"`
	ValidatedAmountUSD  decimal.Decimal `db:"validated_amount_usd"`
	Status              string          `db:"status"`
	IdempotencyKey      *string         `db:"idempotency_key"`
	Policy              []byte          `db:"policy"`
	Provider            string          `db:"provider"`
	ProviderOperationID string          `db:"provider_operation_id"`
	ProviderTxRefs      []byte          `db:"provider_tx_refs"`
	FailureCode         string          `db:"failure_code"`
	FailureReason       string          `db:"failure_reason"`
	MarketDataAsOf      *time.Time      `db:"market_data_as_of"`
	RequestedBy         string          `db:"requested_by"`
	ConfirmationMethod  string          `db:"confirmation_method"`
	CreatedAt           time.Time       `db:"created_at"`
	UpdatedAt           time.Time       `db:"updated_at"`
	CompletedAt         *time.Time      `db:"completed_at"`
}

func (row *investmentExecutionRow) toEntity() (*entities.InvestmentExecution, error) {
	execution := &entities.InvestmentExecution{
		ID:                  row.ID,
		UserID:              row.UserID,
		EnrollmentID:        row.EnrollmentID,
		StrategyID:          row.StrategyID,
		StrategyVersion:     row.StrategyVersion,
		Kind:                entities.InvestmentExecutionKind(row.Kind),
		Side:                row.Side,
		AssetID:             row.AssetID,
		Symbol:              row.Symbol,
		RequestedAmountUSD:  row.RequestedAmountUSD,
		ValidatedAmountUSD:  row.ValidatedAmountUSD,
		Status:              entities.InvestmentExecutionStatus(row.Status),
		Policy:              nil,
		Provider:            row.Provider,
		ProviderOperationID: row.ProviderOperationID,
		ProviderTxRefs:      []string{},
		FailureCode:         row.FailureCode,
		FailureReason:       row.FailureReason,
		MarketDataAsOf:      row.MarketDataAsOf,
		RequestedBy:         entities.InvestmentActor(row.RequestedBy),
		ConfirmationMethod:  row.ConfirmationMethod,
		CreatedAt:           row.CreatedAt,
		UpdatedAt:           row.UpdatedAt,
		CompletedAt:         row.CompletedAt,
	}
	if row.IdempotencyKey != nil {
		execution.IdempotencyKey = *row.IdempotencyKey
	}
	if len(row.Policy) > 0 {
		policy := &entities.InvestmentPolicyDecision{}
		if err := investmentDecodeJSON(row.Policy, policy); err != nil {
			return nil, fmt.Errorf("decode execution policy: %w", err)
		}
		execution.Policy = policy
	}
	if len(row.ProviderTxRefs) > 0 {
		if err := investmentDecodeJSON(row.ProviderTxRefs, &execution.ProviderTxRefs); err != nil {
			return nil, fmt.Errorf("decode execution tx refs: %w", err)
		}
	}
	return execution, nil
}

// Create inserts a new auditable action.
func (r *InvestmentExecutionRepository) Create(ctx context.Context, execution *entities.InvestmentExecution) error {
	if execution == nil {
		return fmt.Errorf("investment execution is nil")
	}
	if execution.ID == uuid.Nil {
		execution.ID = uuid.New()
	}
	execution.CreatedAt = investmentTimeOrNow(execution.CreatedAt)
	execution.UpdatedAt = investmentTimeOrNow(execution.UpdatedAt)

	var policyJSON []byte
	if execution.Policy != nil {
		encoded, err := investmentEncodeJSON(execution.Policy)
		if err != nil {
			return fmt.Errorf("encode execution policy: %w", err)
		}
		policyJSON = encoded
	}
	txRefsJSON, err := investmentEncodeJSON(execution.ProviderTxRefs)
	if err != nil {
		return fmt.Errorf("encode execution tx refs: %w", err)
	}
	txRefsJSON = investmentNonNullJSON(txRefsJSON, "[]")

	_, err = r.db.ExecContext(ctx, `
		INSERT INTO investment_executions (id, user_id, enrollment_id, strategy_id, strategy_version,
			kind, side, asset_id, symbol, requested_amount_usd, validated_amount_usd, status,
			idempotency_key, policy, provider, provider_operation_id, provider_tx_refs, failure_code,
			failure_reason, market_data_as_of, requested_by, confirmation_method, created_at, updated_at, completed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25)`,
		execution.ID, execution.UserID, execution.EnrollmentID, execution.StrategyID, execution.StrategyVersion,
		string(execution.Kind), execution.Side, execution.AssetID, execution.Symbol, execution.RequestedAmountUSD,
		execution.ValidatedAmountUSD, string(execution.Status), investmentNullString(execution.IdempotencyKey),
		policyJSON, execution.Provider, execution.ProviderOperationID, txRefsJSON, execution.FailureCode,
		execution.FailureReason, execution.MarketDataAsOf, string(execution.RequestedBy), execution.ConfirmationMethod,
		execution.CreatedAt, execution.UpdatedAt, execution.CompletedAt)
	if err != nil {
		return fmt.Errorf("create investment execution: %w", err)
	}
	return nil
}

// Update writes the mutable execution fields.
func (r *InvestmentExecutionRepository) Update(ctx context.Context, execution *entities.InvestmentExecution) error {
	if execution == nil {
		return fmt.Errorf("investment execution is nil")
	}
	execution.UpdatedAt = investmentTimeOrNow(execution.UpdatedAt)

	var policyJSON []byte
	if execution.Policy != nil {
		encoded, err := investmentEncodeJSON(execution.Policy)
		if err != nil {
			return fmt.Errorf("encode execution policy: %w", err)
		}
		policyJSON = encoded
	}
	txRefsJSON, err := investmentEncodeJSON(execution.ProviderTxRefs)
	if err != nil {
		return fmt.Errorf("encode execution tx refs: %w", err)
	}
	txRefsJSON = investmentNonNullJSON(txRefsJSON, "[]")

	_, err = r.db.ExecContext(ctx, `
		UPDATE investment_executions SET
			enrollment_id = $2, strategy_id = $3, strategy_version = $4, kind = $5, side = $6,
			asset_id = $7, symbol = $8, requested_amount_usd = $9, validated_amount_usd = $10,
			status = $11, idempotency_key = $12, policy = $13, provider = $14, provider_operation_id = $15,
			provider_tx_refs = $16, failure_code = $17, failure_reason = $18, market_data_as_of = $19,
			requested_by = $20, confirmation_method = $21, updated_at = $22, completed_at = $23
		WHERE id = $1`,
		execution.ID, execution.EnrollmentID, execution.StrategyID, execution.StrategyVersion, string(execution.Kind),
		execution.Side, execution.AssetID, execution.Symbol, execution.RequestedAmountUSD, execution.ValidatedAmountUSD,
		string(execution.Status), investmentNullString(execution.IdempotencyKey), policyJSON, execution.Provider,
		execution.ProviderOperationID, txRefsJSON, execution.FailureCode, execution.FailureReason,
		execution.MarketDataAsOf, string(execution.RequestedBy), execution.ConfirmationMethod,
		execution.UpdatedAt, execution.CompletedAt)
	if err != nil {
		return fmt.Errorf("update investment execution: %w", err)
	}
	return nil
}

// GetByID returns an execution by id, or (nil, nil) when absent.
func (r *InvestmentExecutionRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.InvestmentExecution, error) {
	row := investmentExecutionRow{}
	err := r.db.GetContext(ctx, &row,
		`SELECT `+investmentExecutionColumns+` FROM investment_executions WHERE id = $1`, id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get investment execution: %w", err)
	}
	return row.toEntity()
}

// FindByIdempotencyKey returns the execution recorded for a key, or (nil, nil).
func (r *InvestmentExecutionRepository) FindByIdempotencyKey(ctx context.Context, key string) (*entities.InvestmentExecution, error) {
	if key == "" {
		return nil, nil
	}
	row := investmentExecutionRow{}
	err := r.db.GetContext(ctx, &row,
		`SELECT `+investmentExecutionColumns+` FROM investment_executions WHERE idempotency_key = $1`, key)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find investment execution by idempotency key: %w", err)
	}
	return row.toEntity()
}

// ListByUser returns a user's actions, optionally filtered by status.
func (r *InvestmentExecutionRepository) ListByUser(ctx context.Context, userID uuid.UUID, status string, limit int) ([]*entities.InvestmentExecution, error) {
	if limit <= 0 {
		limit = 25
	}
	if limit > 100 {
		limit = 100
	}
	rows := make([]investmentExecutionRow, 0)
	err := r.db.SelectContext(ctx, &rows,
		`SELECT `+investmentExecutionColumns+`
		 FROM investment_executions
		 WHERE user_id = $1 AND ($2 = '' OR status = $2)
		 ORDER BY created_at DESC
		 LIMIT $3`, userID, status, limit)
	if err != nil {
		return nil, fmt.Errorf("list investment executions: %w", err)
	}
	executions := make([]*entities.InvestmentExecution, 0, len(rows))
	for i := range rows {
		execution, err := rows[i].toEntity()
		if err != nil {
			return nil, err
		}
		executions = append(executions, execution)
	}
	return executions, nil
}
