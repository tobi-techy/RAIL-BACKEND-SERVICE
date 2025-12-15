package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/pkg/tracing"
	"go.uber.org/zap"
)

// IdempotencyKey represents an idempotency key record
type IdempotencyKey struct {
	ID              uuid.UUID       `db:"id"`
	IdempotencyKey  string          `db:"idempotency_key"`
	RequestPath     string          `db:"request_path"`
	RequestMethod   string          `db:"request_method"`
	RequestHash     string          `db:"request_hash"`
	UserID          *uuid.UUID      `db:"user_id"`
	ResponseStatus  int             `db:"response_status"`
	ResponseBody    json.RawMessage `db:"response_body"`
	CreatedAt       time.Time       `db:"created_at"`
	ExpiresAt       time.Time       `db:"expires_at"`
}

// IdempotencyRepository handles idempotency key operations
type IdempotencyRepository struct {
	db     *sql.DB
	logger *zap.Logger
}

// NewIdempotencyRepository creates a new idempotency repository
func NewIdempotencyRepository(db *sql.DB, logger *zap.Logger) *IdempotencyRepository {
	return &IdempotencyRepository{
		db:     db,
		logger: logger,
	}
}

// Get retrieves an idempotency key record
func (r *IdempotencyRepository) Get(ctx context.Context, key string) (*IdempotencyKey, error) {
	ctx, span := tracing.StartDBSpan(ctx, tracing.DBSpanConfig{
		Operation: "SELECT",
		Table:     "idempotency_keys",
	})
	defer span.End()

	query := `
		SELECT id, idempotency_key, request_path, request_method, request_hash,
		       user_id, response_status, response_body, created_at, expires_at
		FROM idempotency_keys
		WHERE idempotency_key = $1 AND expires_at > NOW()
	`

	var record IdempotencyKey
	err := r.db.QueryRowContext(ctx, query, key).Scan(
		&record.ID,
		&record.IdempotencyKey,
		&record.RequestPath,
		&record.RequestMethod,
		&record.RequestHash,
		&record.UserID,
		&record.ResponseStatus,
		&record.ResponseBody,
		&record.CreatedAt,
		&record.ExpiresAt,
	)

	if err == sql.ErrNoRows {
		tracing.EndDBSpan(span, nil, 0)
		return nil, nil // Not found is not an error
	}

	tracing.EndDBSpan(span, err, 1)

	if err != nil {
		r.logger.Error("Failed to get idempotency key",
			zap.String("key", key),
			zap.Error(err))
		return nil, err
	}

	return &record, nil
}

// Create stores a new idempotency key record
func (r *IdempotencyRepository) Create(ctx context.Context, record *IdempotencyKey) error {
	ctx, span := tracing.StartDBSpan(ctx, tracing.DBSpanConfig{
		Operation: "INSERT",
		Table:     "idempotency_keys",
	})
	defer span.End()

	query := `
		INSERT INTO idempotency_keys (
			idempotency_key, request_path, request_method, request_hash,
			user_id, response_status, response_body, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, created_at
	`

	err := r.db.QueryRowContext(ctx, query,
		record.IdempotencyKey,
		record.RequestPath,
		record.RequestMethod,
		record.RequestHash,
		record.UserID,
		record.ResponseStatus,
		record.ResponseBody,
		record.ExpiresAt,
	).Scan(&record.ID, &record.CreatedAt)

	tracing.EndDBSpan(span, err, 1)

	if err != nil {
		r.logger.Error("Failed to create idempotency key",
			zap.String("key", record.IdempotencyKey),
			zap.Error(err))
		return err
	}

	return nil
}

// DeleteExpired removes expired idempotency keys
func (r *IdempotencyRepository) DeleteExpired(ctx context.Context) (int64, error) {
	ctx, span := tracing.StartDBSpan(ctx, tracing.DBSpanConfig{
		Operation: "DELETE",
		Table:     "idempotency_keys",
	})
	defer span.End()

	query := `DELETE FROM idempotency_keys WHERE expires_at <= NOW()`

	result, err := r.db.ExecContext(ctx, query)
	if err != nil {
		tracing.EndDBSpan(span, err, -1)
		r.logger.Error("Failed to delete expired idempotency keys", zap.Error(err))
		return 0, err
	}

	rowsAffected, _ := result.RowsAffected()
	tracing.EndDBSpan(span, nil, rowsAffected)

	r.logger.Info("Deleted expired idempotency keys", zap.Int64("count", rowsAffected))
	return rowsAffected, nil
}

// GetStats returns statistics about idempotency keys
func (r *IdempotencyRepository) GetStats(ctx context.Context) (total, expired int64, err error) {
	ctx, span := tracing.StartDBSpan(ctx, tracing.DBSpanConfig{
		Operation: "SELECT",
		Table:     "idempotency_keys",
	})
	defer span.End()

	query := `
		SELECT 
			COUNT(*) as total,
			COUNT(*) FILTER (WHERE expires_at <= NOW()) as expired
		FROM idempotency_keys
	`

	err = r.db.QueryRowContext(ctx, query).Scan(&total, &expired)
	tracing.EndDBSpan(span, err, 1)

	if err != nil {
		r.logger.Error("Failed to get idempotency key stats", zap.Error(err))
		return 0, 0, err
	}

	return total, expired, nil
}
