package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// PasscodeSessionRepository persists short-lived passcode step-up sessions.
//
// These sessions authorise money movement (withdrawals, transfers, adding
// recipients). They used to live only in Redis, which coupled the money path to
// Redis availability; the row here is the source of truth.
type PasscodeSessionRepository struct {
	db     *sql.DB
	logger *zap.Logger
}

func NewPasscodeSessionRepository(db *sql.DB, logger *zap.Logger) *PasscodeSessionRepository {
	return &PasscodeSessionRepository{db: db, logger: logger}
}

// CreatePasscodeSession stores a new step-up session.
func (r *PasscodeSessionRepository) CreatePasscodeSession(ctx context.Context, userID uuid.UUID, tokenHash string, expiresAt time.Time) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO passcode_sessions (user_id, token_hash, expires_at)
		VALUES ($1, $2, $3)
	`, userID, tokenHash, expiresAt)
	if err != nil {
		return fmt.Errorf("create passcode session: %w", err)
	}
	return nil
}

// PasscodeSessionExists reports whether the token is a live session for the user.
func (r *PasscodeSessionRepository) PasscodeSessionExists(ctx context.Context, userID uuid.UUID, tokenHash string, now time.Time) (bool, error) {
	var exists bool
	err := r.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM passcode_sessions
			WHERE user_id = $1 AND token_hash = $2 AND expires_at > $3
		)
	`, userID, tokenHash, now).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check passcode session: %w", err)
	}
	return exists, nil
}

// DeletePasscodeSession consumes a session so the token is single-use.
func (r *PasscodeSessionRepository) DeletePasscodeSession(ctx context.Context, userID uuid.UUID, tokenHash string) error {
	_, err := r.db.ExecContext(ctx, `
		DELETE FROM passcode_sessions WHERE user_id = $1 AND token_hash = $2
	`, userID, tokenHash)
	if err != nil {
		return fmt.Errorf("delete passcode session: %w", err)
	}
	return nil
}

// DeleteExpiredPasscodeSessions prunes sessions past their TTL. Best effort:
// called opportunistically when a new session is issued.
func (r *PasscodeSessionRepository) DeleteExpiredPasscodeSessions(ctx context.Context, now time.Time) (int64, error) {
	result, err := r.db.ExecContext(ctx, `DELETE FROM passcode_sessions WHERE expires_at <= $1`, now)
	if err != nil {
		return 0, fmt.Errorf("delete expired passcode sessions: %w", err)
	}
	deleted, _ := result.RowsAffected()
	return deleted, nil
}
