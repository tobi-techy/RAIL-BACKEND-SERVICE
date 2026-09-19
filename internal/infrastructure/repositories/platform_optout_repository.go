package repositories

import (
	"context"
	"database/sql"
	"fmt"

	"go.uber.org/zap"
)

// PlatformOptOutRepository stores and reads messaging opt-outs.
//
// Durable by design (see migrations/307_platform_optouts.up.sql): the suppression
// check runs at the single outbound choke point, so replies and unsolicited
// outreach are both covered by one lookup.
type PlatformOptOutRepository struct {
	db     *sql.DB
	logger *zap.Logger
}

func NewPlatformOptOutRepository(db *sql.DB, logger *zap.Logger) *PlatformOptOutRepository {
	return &PlatformOptOutRepository{db: db, logger: logger}
}

// IsOptedOut reports whether this handle is currently suppressed.
func (r *PlatformOptOutRepository) IsOptedOut(ctx context.Context, platform, senderID string) (bool, error) {
	query := `SELECT EXISTS (
		SELECT 1 FROM platform_optouts
		 WHERE platform = $1 AND platform_user_id = $2 AND resumed_at IS NULL
	)`
	var optedOut bool
	if err := r.db.QueryRowContext(ctx, query, platform, senderID).Scan(&optedOut); err != nil {
		return false, fmt.Errorf("check platform opt-out: %w", err)
	}
	return optedOut, nil
}

// OptOut records a stop request. Re-requesting clears a previous resume, so a
// later STOP after a START is honoured again.
func (r *PlatformOptOutRepository) OptOut(ctx context.Context, platform, senderID, reason string) error {
	query := `
		INSERT INTO platform_optouts (platform, platform_user_id, reason)
		VALUES ($1, $2, $3)
		ON CONFLICT (platform, platform_user_id)
		DO UPDATE SET resumed_at = NULL, reason = EXCLUDED.reason, updated_at = NOW()`
	if _, err := r.db.ExecContext(ctx, query, platform, senderID, reason); err != nil {
		return fmt.Errorf("record platform opt-out: %w", err)
	}
	r.logger.Info("platform opt-out recorded",
		zap.String("platform", platform), zap.String("sender", senderID))
	return nil
}

// Resume clears an opt-out without erasing the history behind it.
func (r *PlatformOptOutRepository) Resume(ctx context.Context, platform, senderID string) error {
	query := `UPDATE platform_optouts
	             SET resumed_at = NOW(), updated_at = NOW()
	           WHERE platform = $1 AND platform_user_id = $2 AND resumed_at IS NULL`
	if _, err := r.db.ExecContext(ctx, query, platform, senderID); err != nil {
		return fmt.Errorf("resume platform messaging: %w", err)
	}
	return nil
}
