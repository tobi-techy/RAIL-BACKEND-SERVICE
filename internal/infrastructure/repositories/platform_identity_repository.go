package repositories

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

type PlatformIdentityRepository struct {
	db     *sql.DB
	logger *zap.Logger
}

func NewPlatformIdentityRepository(db *sql.DB, logger *zap.Logger) *PlatformIdentityRepository {
	return &PlatformIdentityRepository{db: db, logger: logger}
}

func (r *PlatformIdentityRepository) Create(ctx context.Context, pi *entities.PlatformIdentity) error {
	query := `
		INSERT INTO platform_identities
			(id, user_id, platform, platform_user_id, platform_username,
			 handshake_token_hash, handshake_expires_at, linked_at, last_used_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING created_at, updated_at`
	return r.db.QueryRowContext(ctx, query,
		pi.ID, pi.UserID, pi.Platform, pi.PlatformUserID, pi.PlatformUsername,
		pi.HandshakeTokenHash, pi.HandshakeExpiresAt, pi.LinkedAt, pi.LastUsedAt,
	).Scan(&pi.CreatedAt, &pi.UpdatedAt)
}

func (r *PlatformIdentityRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.PlatformIdentity, error) {
	pi := &entities.PlatformIdentity{}
	query := `SELECT * FROM platform_identities WHERE id = $1`
	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&pi.ID, &pi.UserID, &pi.Platform, &pi.PlatformUserID, &pi.PlatformUsername,
		&pi.HandshakeTokenHash, &pi.HandshakeExpiresAt, &pi.LinkedAt, &pi.LastUsedAt,
		&pi.CreatedAt, &pi.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return pi, nil
}

func (r *PlatformIdentityRepository) GetByUserAndPlatform(ctx context.Context, userID uuid.UUID, platform entities.Platform) (*entities.PlatformIdentity, error) {
	pi := &entities.PlatformIdentity{}
	query := `SELECT * FROM platform_identities WHERE user_id = $1 AND platform = $2`
	err := r.db.QueryRowContext(ctx, query, userID, platform).Scan(
		&pi.ID, &pi.UserID, &pi.Platform, &pi.PlatformUserID, &pi.PlatformUsername,
		&pi.HandshakeTokenHash, &pi.HandshakeExpiresAt, &pi.LinkedAt, &pi.LastUsedAt,
		&pi.CreatedAt, &pi.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return pi, nil
}

func (r *PlatformIdentityRepository) GetByPlatformUser(ctx context.Context, platform entities.Platform, platformUserID string) (*entities.PlatformIdentity, error) {
	pi := &entities.PlatformIdentity{}
	query := `SELECT * FROM platform_identities WHERE platform = $1 AND platform_user_id = $2`
	err := r.db.QueryRowContext(ctx, query, platform, platformUserID).Scan(
		&pi.ID, &pi.UserID, &pi.Platform, &pi.PlatformUserID, &pi.PlatformUsername,
		&pi.HandshakeTokenHash, &pi.HandshakeExpiresAt, &pi.LinkedAt, &pi.LastUsedAt,
		&pi.CreatedAt, &pi.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return pi, nil
}

func (r *PlatformIdentityRepository) GetByHandshakeTokenHash(ctx context.Context, hash string) (*entities.PlatformIdentity, error) {
	pi := &entities.PlatformIdentity{}
	query := `SELECT * FROM platform_identities WHERE handshake_token_hash = $1`
	err := r.db.QueryRowContext(ctx, query, hash).Scan(
		&pi.ID, &pi.UserID, &pi.Platform, &pi.PlatformUserID, &pi.PlatformUsername,
		&pi.HandshakeTokenHash, &pi.HandshakeExpiresAt, &pi.LinkedAt, &pi.LastUsedAt,
		&pi.CreatedAt, &pi.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return pi, nil
}

func (r *PlatformIdentityRepository) ListByUser(ctx context.Context, userID uuid.UUID) ([]*entities.PlatformIdentity, error) {
	query := `SELECT * FROM platform_identities WHERE user_id = $1 ORDER BY created_at DESC`
	rows, err := r.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*entities.PlatformIdentity
	for rows.Next() {
		pi := &entities.PlatformIdentity{}
		if err := rows.Scan(
			&pi.ID, &pi.UserID, &pi.Platform, &pi.PlatformUserID, &pi.PlatformUsername,
			&pi.HandshakeTokenHash, &pi.HandshakeExpiresAt, &pi.LinkedAt, &pi.LastUsedAt,
			&pi.CreatedAt, &pi.UpdatedAt,
		); err != nil {
			return nil, err
		}
		results = append(results, pi)
	}
	return results, rows.Err()
}

func (r *PlatformIdentityRepository) SetHandshake(ctx context.Context, id uuid.UUID, tokenHash string, expiresAt time.Time) error {
	query := `UPDATE platform_identities SET handshake_token_hash = $1, handshake_expires_at = $2, updated_at = NOW() WHERE id = $3`
	_, err := r.db.ExecContext(ctx, query, tokenHash, expiresAt, id)
	return err
}

func (r *PlatformIdentityRepository) CompleteHandshake(ctx context.Context, id uuid.UUID, platformUserID string) error {
	query := `UPDATE platform_identities SET platform_user_id = $2, handshake_token_hash = NULL, handshake_expires_at = NULL, linked_at = NOW(), updated_at = NOW() WHERE id = $1`
	_, err := r.db.ExecContext(ctx, query, id, platformUserID)
	return err
}

func (r *PlatformIdentityRepository) TouchLastUsed(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `UPDATE platform_identities SET last_used_at = NOW(), updated_at = NOW() WHERE id = $1`, id)
	return err
}

func (r *PlatformIdentityRepository) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM platform_identities WHERE id = $1`, id)
	return err
}
