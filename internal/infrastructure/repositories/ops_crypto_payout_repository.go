package repositories

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

// ErrOpsPayoutSuppressionConflict means the source transaction already has a
// deposit row, so it cannot be bridged off-ledger without a human decision.
// Defined on the entity so both the repository and the API layer can match it
// without the API importing persistence.
var ErrOpsPayoutSuppressionConflict = entities.ErrOpsPayoutSuppressionConflict

const opsPayoutColumns = `
	id, user_id, source_chain, source_wallet_id, source_circle_wallet_id, source_address,
	source_circle_tx_id, source_tx_hash, destination_chain, destination_address,
	amount, funding_amount, fee_amount, chainrails_intent_id, intent_address,
	circle_transfer_id, circle_tx_hash, status, suppression_deposit_id, attempts,
	reason, actor, error_message, created_at, updated_at, completed_at`

// OpsCryptoPayoutRepository persists operator-driven, off-ledger crypto
// payouts. See entities.OpsCryptoPayout for what the rows mean.
type OpsCryptoPayoutRepository struct {
	db *sql.DB
}

// NewOpsCryptoPayoutRepository builds the Postgres-backed payout repository.
func NewOpsCryptoPayoutRepository(db *sql.DB) *OpsCryptoPayoutRepository {
	return &OpsCryptoPayoutRepository{db: db}
}

// CreatePayoutWithSuppression writes the payout row and the suppression deposit
// in one transaction, BEFORE any provider call.
//
// It returns (false, nil) when a payout already exists for the source Circle
// transaction: the caller should load and resume that row rather than start a
// second path to move the same money.
func (r *OpsCryptoPayoutRepository) CreatePayoutWithSuppression(ctx context.Context, p *entities.OpsCryptoPayout) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin payout tx: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO ops_crypto_payouts (
			id, user_id, source_chain, source_wallet_id, source_circle_wallet_id, source_address,
			source_circle_tx_id, source_tx_hash, destination_chain, destination_address,
			amount, funding_amount, fee_amount, chainrails_intent_id, intent_address,
			circle_transfer_id, circle_tx_hash, status, suppression_deposit_id, attempts,
			reason, actor, error_message, created_at, updated_at, completed_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26
		)`,
		p.ID, p.UserID, p.SourceChain, p.SourceWalletID, p.SourceCircleWalletID, p.SourceAddress,
		p.SourceCircleTxID, p.SourceTxHash, p.DestinationChain, p.DestinationAddress,
		p.Amount, p.FundingAmount, p.FeeAmount, p.ChainRailsIntentID, p.IntentAddress,
		p.CircleTransferID, p.CircleTxHash, p.Status, p.SuppressionDepositID, p.Attempts,
		p.Reason, p.Actor, p.ErrorMessage, p.CreatedAt, p.UpdatedAt, p.CompletedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return false, nil
		}
		return false, fmt.Errorf("insert ops payout: %w", err)
	}

	depositID := uuid.New()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO deposits (id, idempotency_key, user_id, amount, status, tx_hash, chain, token, created_at)
		VALUES ($1, $2, $3, $4, 'bridged', $5, $6, 'USDC', NOW())`,
		depositID, "ops-payout:"+p.ID.String(), p.UserID, p.Amount, p.SourceTxHash, p.SourceChain,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return false, fmt.Errorf("%w (tx %s)", ErrOpsPayoutSuppressionConflict, p.SourceTxHash)
		}
		return false, fmt.Errorf("insert payout suppression deposit: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE ops_crypto_payouts SET suppression_deposit_id = $2 WHERE id = $1`, p.ID, depositID,
	); err != nil {
		return false, fmt.Errorf("link suppression deposit: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit payout: %w", err)
	}
	p.SuppressionDepositID = &depositID
	return true, nil
}

// GetPayoutByID loads one payout, or nil when it does not exist.
func (r *OpsCryptoPayoutRepository) GetPayoutByID(ctx context.Context, id uuid.UUID) (*entities.OpsCryptoPayout, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+opsPayoutColumns+` FROM ops_crypto_payouts WHERE id = $1`, id)
	p, err := scanOpsPayout(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get ops payout: %w", err)
	}
	return p, nil
}

// GetPayoutBySourceTxID loads the payout for a Circle transaction, or nil.
func (r *OpsCryptoPayoutRepository) GetPayoutBySourceTxID(ctx context.Context, sourceTxID string) (*entities.OpsCryptoPayout, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+opsPayoutColumns+` FROM ops_crypto_payouts WHERE source_circle_tx_id = $1`, sourceTxID)
	p, err := scanOpsPayout(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get ops payout by source tx: %w", err)
	}
	return p, nil
}

// ClaimForIntent atomically moves a row into intent_creating so only one
// request creates the ChainRails intent. A claim older than five minutes is
// treated as abandoned and may be taken over, which keeps a crashed request
// from stranding the payout.
func (r *OpsCryptoPayoutRepository) ClaimForIntent(ctx context.Context, id uuid.UUID) (bool, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE ops_crypto_payouts
		   SET status = $2, updated_at = NOW()
		 WHERE id = $1
		   AND (status = $3
		        OR (status = $2 AND updated_at < NOW() - INTERVAL '5 minutes'))`,
		id, entities.OpsPayoutStatusIntentCreating, entities.OpsPayoutStatusCreated,
	)
	if err != nil {
		return false, fmt.Errorf("claim payout for intent: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("claim payout rows: %w", err)
	}
	return n > 0, nil
}

// ResetFailedForRetry reopens a definitively-failed payout for one more
// attempt. It re-installs the suppression deposit, because a failed payout
// released it (the money had not moved).
func (r *OpsCryptoPayoutRepository) ResetFailedForRetry(ctx context.Context, id uuid.UUID, amount decimal.Decimal, destination, reason, actor string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin payout retry tx: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		UPDATE ops_crypto_payouts
		   SET status = $2,
		       amount = $3,
		       destination_address = $4,
		       reason = $5,
		       actor = $6,
		       funding_amount = NULL,
		       fee_amount = NULL,
		       chainrails_intent_id = NULL,
		       intent_address = NULL,
		       circle_transfer_id = NULL,
		       circle_tx_hash = NULL,
		       error_message = NULL,
		       completed_at = NULL,
		       attempts = attempts + 1,
		       updated_at = NOW()
		 WHERE id = $1 AND status = $7`,
		id, entities.OpsPayoutStatusCreated, amount, destination, reason, actor, entities.OpsPayoutStatusFailed,
	)
	if err != nil {
		return fmt.Errorf("reset failed payout: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("reset failed payout rows: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("payout %s is not in a failed state", id)
	}

	var existing *uuid.UUID
	if err := tx.QueryRowContext(ctx,
		`SELECT suppression_deposit_id FROM ops_crypto_payouts WHERE id = $1`, id,
	).Scan(&existing); err != nil {
		return fmt.Errorf("read suppression link: %w", err)
	}

	var depositID uuid.UUID
	if existing != nil {
		depositID = *existing
		// The row normally survives only if the earlier delete failed. Put it
		// back into the suppression state rather than failing the retry.
		if _, err := tx.ExecContext(ctx,
			`UPDATE deposits SET status = 'bridged', amount = $2 WHERE id = $1 AND status = 'bridged'`,
			depositID, amount,
		); err != nil {
			return fmt.Errorf("restore suppression deposit: %w", err)
		}
	} else {
		depositID = uuid.New()
		_, err := tx.ExecContext(ctx, `
			INSERT INTO deposits (id, idempotency_key, user_id, amount, status, tx_hash, chain, token, created_at)
			SELECT $1, 'ops-payout:' || id::text, user_id, $2, 'bridged', source_tx_hash, source_chain, 'USDC', NOW()
			  FROM ops_crypto_payouts WHERE id = $3`,
			depositID, amount, id,
		)
		if err != nil {
			if isUniqueViolation(err) {
				return fmt.Errorf("%w", ErrOpsPayoutSuppressionConflict)
			}
			return fmt.Errorf("re-create suppression deposit: %w", err)
		}
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE ops_crypto_payouts SET suppression_deposit_id = $2 WHERE id = $1`, id, depositID,
	); err != nil {
		return fmt.Errorf("relink suppression deposit: %w", err)
	}

	return tx.Commit()
}

// MarkIntentCreated persists the intent address and exact funding amount before
// any money moves, so a crash can never fund an intent we did not record.
func (r *OpsCryptoPayoutRepository) MarkIntentCreated(ctx context.Context, id uuid.UUID, intentID int, intentAddress string, fundingAmount, feeAmount decimal.Decimal) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE ops_crypto_payouts
		   SET status = $2,
		       chainrails_intent_id = $3,
		       intent_address = $4,
		       funding_amount = $5,
		       fee_amount = $6,
		       updated_at = NOW()
		 WHERE id = $1`,
		id, entities.OpsPayoutStatusIntentCreated, intentID, intentAddress, fundingAmount, feeAmount,
	)
	if err != nil {
		return fmt.Errorf("mark intent created: %w", err)
	}
	return nil
}

// MarkFunding records that a Circle transfer has been initiated. Called before
// the transfer so a crash leaves the row in an in-flight state.
func (r *OpsCryptoPayoutRepository) MarkFunding(ctx context.Context, id uuid.UUID, transferID string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE ops_crypto_payouts
		   SET status = $2,
		       circle_transfer_id = COALESCE(NULLIF($3, ''), circle_transfer_id),
		       updated_at = NOW()
		 WHERE id = $1
		   AND status IN ($4, $2, $5)`,
		id, entities.OpsPayoutStatusFunding, transferID,
		entities.OpsPayoutStatusIntentCreated, entities.OpsPayoutStatusFundingUncertain,
	)
	if err != nil {
		return fmt.Errorf("mark funding: %w", err)
	}
	return nil
}

// MarkFunded records a confirmed Circle transfer.
func (r *OpsCryptoPayoutRepository) MarkFunded(ctx context.Context, id uuid.UUID, transferID, txHash string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE ops_crypto_payouts
		   SET status = $2,
		       circle_transfer_id = COALESCE(NULLIF($3, ''), circle_transfer_id),
		       circle_tx_hash = COALESCE(NULLIF($4, ''), circle_tx_hash),
		       updated_at = NOW()
		 WHERE id = $1
		   AND status IN ($5, $6, $2)`,
		id, entities.OpsPayoutStatusFunded, transferID, txHash,
		entities.OpsPayoutStatusFunding, entities.OpsPayoutStatusFundingUncertain,
	)
	if err != nil {
		return fmt.Errorf("mark funded: %w", err)
	}
	return nil
}

// MarkFundingUncertain records an ambiguous transfer outcome. Suppression stays
// held: the money may have moved.
func (r *OpsCryptoPayoutRepository) MarkFundingUncertain(ctx context.Context, id uuid.UUID, message string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE ops_crypto_payouts
		   SET status = $2, error_message = $3, updated_at = NOW()
		 WHERE id = $1 AND status IN ($4, $2)`,
		id, entities.OpsPayoutStatusFundingUncertain, message, entities.OpsPayoutStatusFunding,
	)
	if err != nil {
		return fmt.Errorf("mark funding uncertain: %w", err)
	}
	return nil
}

// MarkCompleted records that ChainRails settled the intent on the destination.
func (r *OpsCryptoPayoutRepository) MarkCompleted(ctx context.Context, id uuid.UUID, txHash string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE ops_crypto_payouts
		   SET status = $2,
		       circle_tx_hash = COALESCE(NULLIF($3, ''), circle_tx_hash),
		       completed_at = COALESCE(completed_at, NOW()),
		       updated_at = NOW()
		 WHERE id = $1
		   AND status IN ($4, $5, $2)`,
		id, entities.OpsPayoutStatusCompleted, txHash,
		entities.OpsPayoutStatusFunded, entities.OpsPayoutStatusFunding,
	)
	if err != nil {
		return fmt.Errorf("mark payout completed: %w", err)
	}
	return nil
}

// FailPayout records a definitive pre-transfer failure and releases the
// suppression deposit, because nothing left the wallet. Releasing it keeps the
// normal deposit credit path available as the fallback.
func (r *OpsCryptoPayoutRepository) FailPayout(ctx context.Context, id uuid.UUID, message string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin fail payout tx: %w", err)
	}
	defer tx.Rollback()

	var depositID *uuid.UUID
	if err := tx.QueryRowContext(ctx, `
		UPDATE ops_crypto_payouts
		   SET status = $2, error_message = $3, updated_at = NOW()
		 WHERE id = $1
		 RETURNING suppression_deposit_id`,
		id, entities.OpsPayoutStatusFailed, message,
	).Scan(&depositID); err != nil {
		return fmt.Errorf("mark payout failed: %w", err)
	}

	if depositID != nil {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM deposits WHERE id = $1 AND status = 'bridged'`, *depositID,
		); err != nil {
			return fmt.Errorf("release suppression deposit: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE ops_crypto_payouts SET suppression_deposit_id = NULL WHERE id = $1`, id,
		); err != nil {
			return fmt.Errorf("clear suppression link: %w", err)
		}
	}

	return tx.Commit()
}

func scanOpsPayout(row *sql.Row) (*entities.OpsCryptoPayout, error) {
	var p entities.OpsCryptoPayout
	err := row.Scan(
		&p.ID, &p.UserID, &p.SourceChain, &p.SourceWalletID, &p.SourceCircleWalletID, &p.SourceAddress,
		&p.SourceCircleTxID, &p.SourceTxHash, &p.DestinationChain, &p.DestinationAddress,
		&p.Amount, &p.FundingAmount, &p.FeeAmount, &p.ChainRailsIntentID, &p.IntentAddress,
		&p.CircleTransferID, &p.CircleTxHash, &p.Status, &p.SuppressionDepositID, &p.Attempts,
		&p.Reason, &p.Actor, &p.ErrorMessage, &p.CreatedAt, &p.UpdatedAt, &p.CompletedAt,
	)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// isUniqueViolation reports whether err is a Postgres 23505 unique violation.
func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		return pqErr.Code == "23505"
	}
	return false
}
