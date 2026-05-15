package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/shopspring/decimal"
)

// DepositSweep represents a pending or completed auto-sweep from a non-Solana chain to Solana.
type DepositSweep struct {
	ID                 uuid.UUID        `db:"id"`
	DepositID          uuid.UUID        `db:"deposit_id"`
	UserID             uuid.UUID        `db:"user_id"`
	SourceChain        string           `db:"source_chain"`
	Amount             decimal.Decimal  `db:"amount"`
	FeeAmount          *decimal.Decimal `db:"fee_amount"`
	IntentAddress      *string          `db:"intent_address"`
	ChainRailsIntentID *int             `db:"chainrails_intent_id"`
	Status             string           `db:"status"`
	TxHash             *string          `db:"tx_hash"`
	ErrorMessage       *string          `db:"error_message"`
	Attempts           int              `db:"attempts"`
	CreatedAt          time.Time        `db:"created_at"`
	UpdatedAt          time.Time        `db:"updated_at"`
	CompletedAt        *time.Time       `db:"completed_at"`
}

// DepositSweepRepository handles deposit sweep persistence.
type DepositSweepRepository struct {
	db *sqlx.DB
}

func NewDepositSweepRepository(db *sqlx.DB) *DepositSweepRepository {
	return &DepositSweepRepository{db: db}
}

func (r *DepositSweepRepository) Create(ctx context.Context, sweep *DepositSweep) error {
	query := `
		INSERT INTO deposit_sweeps (id, deposit_id, user_id, source_chain, amount, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`
	_, err := r.db.ExecContext(ctx, query,
		sweep.ID, sweep.DepositID, sweep.UserID, sweep.SourceChain,
		sweep.Amount, sweep.Status, sweep.CreatedAt, sweep.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("create deposit sweep: %w", err)
	}
	return nil
}

func (r *DepositSweepRepository) GetPending(ctx context.Context, maxAttempts int) ([]*DepositSweep, error) {
	// Atomic claim: SELECT FOR UPDATE SKIP LOCKED + immediate status update in one transaction.
	// This prevents multiple instances from processing the same sweep.
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	query := `
		SELECT id, deposit_id, user_id, source_chain, amount, fee_amount, intent_address,
		       chainrails_intent_id, status, tx_hash, error_message, attempts,
		       created_at, updated_at, completed_at
		FROM deposit_sweeps
		WHERE status = 'pending' AND attempts < $1
		ORDER BY created_at ASC
		LIMIT 20
		FOR UPDATE SKIP LOCKED
	`
	var sweeps []*DepositSweep
	if err := tx.SelectContext(ctx, &sweeps, query, maxAttempts); err != nil {
		return nil, fmt.Errorf("get pending sweeps: %w", err)
	}
	if len(sweeps) == 0 {
		return nil, nil
	}

	// Mark all claimed rows as 'claiming' to prevent re-pickup
	ids := make([]uuid.UUID, len(sweeps))
	for i, s := range sweeps {
		ids[i] = s.ID
	}
	_, err = tx.ExecContext(ctx,
		`UPDATE deposit_sweeps SET status = 'in_progress', updated_at = NOW() WHERE id = ANY($1)`,
		pq.Array(ids),
	)
	if err != nil {
		return nil, fmt.Errorf("claim sweeps: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit claim: %w", err)
	}
	return sweeps, nil
}

func (r *DepositSweepRepository) MarkInProgress(ctx context.Context, id uuid.UUID, intentAddress string, intentID int, feeAmount *decimal.Decimal) error {
	query := `
		UPDATE deposit_sweeps
		SET intent_address = $2, chainrails_intent_id = $3, fee_amount = $4, updated_at = NOW()
		WHERE id = $1
	`
	_, err := r.db.ExecContext(ctx, query, id, intentAddress, intentID, feeAmount)
	if err != nil {
		return fmt.Errorf("mark sweep in_progress: %w", err)
	}
	return nil
}

func (r *DepositSweepRepository) MarkCompleted(ctx context.Context, id uuid.UUID, txHash string) error {
	query := `
		UPDATE deposit_sweeps
		SET status = 'completed', tx_hash = $2, completed_at = NOW(), updated_at = NOW()
		WHERE id = $1
	`
	_, err := r.db.ExecContext(ctx, query, id, txHash)
	if err != nil {
		return fmt.Errorf("mark sweep completed: %w", err)
	}
	return nil
}

func (r *DepositSweepRepository) MarkFailed(ctx context.Context, id uuid.UUID, errMsg string) error {
	query := `
		UPDATE deposit_sweeps
		SET status = 'pending', error_message = $2, attempts = attempts + 1, updated_at = NOW()
		WHERE id = $1
	`
	_, err := r.db.ExecContext(ctx, query, id, errMsg)
	if err != nil {
		return fmt.Errorf("mark sweep failed: %w", err)
	}
	return nil
}

func (r *DepositSweepRepository) GetBySweepID(ctx context.Context, id uuid.UUID) (*DepositSweep, error) {
	query := `
		SELECT id, deposit_id, user_id, source_chain, amount, fee_amount, intent_address,
		       chainrails_intent_id, status, tx_hash, error_message, attempts,
		       created_at, updated_at, completed_at
		FROM deposit_sweeps
		WHERE id = $1
	`
	var sweep DepositSweep
	if err := r.db.GetContext(ctx, &sweep, query, id); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("deposit sweep not found")
		}
		return nil, fmt.Errorf("get deposit sweep: %w", err)
	}
	return &sweep, nil
}

func (r *DepositSweepRepository) GetByIntentAddress(ctx context.Context, intentAddress string) (*DepositSweep, error) {
	query := `
		SELECT id, deposit_id, user_id, source_chain, amount, fee_amount, intent_address,
		       chainrails_intent_id, status, tx_hash, error_message, attempts,
		       created_at, updated_at, completed_at
		FROM deposit_sweeps
		WHERE intent_address = $1
	`
	var sweep DepositSweep
	if err := r.db.GetContext(ctx, &sweep, query, intentAddress); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get sweep by intent address: %w", err)
	}
	return &sweep, nil
}

func (r *DepositSweepRepository) GetByDepositID(ctx context.Context, depositID uuid.UUID) (*DepositSweep, error) {
	query := `
		SELECT id, deposit_id, user_id, source_chain, amount, fee_amount, intent_address,
		       chainrails_intent_id, status, tx_hash, error_message, attempts,
		       created_at, updated_at, completed_at
		FROM deposit_sweeps
		WHERE deposit_id = $1
	`
	var sweep DepositSweep
	if err := r.db.GetContext(ctx, &sweep, query, depositID); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get sweep by deposit_id: %w", err)
	}
	return &sweep, nil
}

// GetStale returns sweeps stuck in 'in_progress' for longer than the given duration.
func (r *DepositSweepRepository) GetStale(ctx context.Context, olderThan time.Duration) ([]*DepositSweep, error) {
	query := `
		SELECT id, deposit_id, user_id, source_chain, amount, fee_amount, intent_address,
		       chainrails_intent_id, status, tx_hash, error_message, attempts,
		       created_at, updated_at, completed_at
		FROM deposit_sweeps
		WHERE status = 'in_progress' AND updated_at < $1
		ORDER BY updated_at ASC
		LIMIT 10
	`
	cutoff := time.Now().Add(-olderThan)
	var sweeps []*DepositSweep
	if err := r.db.SelectContext(ctx, &sweeps, query, cutoff); err != nil {
		return nil, fmt.Errorf("get stale sweeps: %w", err)
	}
	return sweeps, nil
}

// CreateSweep is a convenience method that implements the funding.DepositSweepCreator interface.
func (r *DepositSweepRepository) CreateSweep(ctx context.Context, depositID, userID uuid.UUID, sourceChain string, amount decimal.Decimal) error {
	now := time.Now()
	sweep := &DepositSweep{
		ID:          uuid.New(),
		DepositID:   depositID,
		UserID:      userID,
		SourceChain: sourceChain,
		Amount:      amount,
		Status:      "pending",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	return r.Create(ctx, sweep)
}

// CompleteSweep marks a sweep as completed by intent address (implements ChainRailsSweepService).
func (r *DepositSweepRepository) CompleteSweep(ctx context.Context, intentAddress, txHash string) error {
	sweep, err := r.GetByIntentAddress(ctx, intentAddress)
	if err != nil {
		return err
	}
	if sweep == nil {
		return fmt.Errorf("sweep not found for intent_address %s", intentAddress)
	}
	return r.MarkCompleted(ctx, sweep.ID, txHash)
}

// FailSweep marks a sweep as failed by intent address (implements ChainRailsSweepService).
func (r *DepositSweepRepository) FailSweep(ctx context.Context, intentAddress, reason string) error {
	sweep, err := r.GetByIntentAddress(ctx, intentAddress)
	if err != nil {
		return err
	}
	if sweep == nil {
		return fmt.Errorf("sweep not found for intent_address %s", intentAddress)
	}
	return r.MarkFailed(ctx, sweep.ID, reason)
}
