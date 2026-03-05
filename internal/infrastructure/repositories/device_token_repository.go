package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// DeviceToken represents a push notification device token
type DeviceToken struct {
	ID          uuid.UUID  `db:"id"`
	UserID      uuid.UUID  `db:"user_id"`
	Token       string     `db:"token"`
	Platform    string     `db:"platform"`
	EndpointARN *string    `db:"endpoint_arn"`
	AppVersion  *string    `db:"app_version"`
	DeviceModel *string    `db:"device_model"`
	OSVersion   *string    `db:"os_version"`
	IsActive    bool       `db:"is_active"`
	LastUsedAt  time.Time  `db:"last_used_at"`
	CreatedAt   time.Time  `db:"created_at"`
	UpdatedAt   time.Time  `db:"updated_at"`
}

// DeviceTokenRepository handles device token persistence
type DeviceTokenRepository struct {
	db *sql.DB
}

// NewDeviceTokenRepository creates a new device token repository
func NewDeviceTokenRepository(db *sql.DB) *DeviceTokenRepository {
	return &DeviceTokenRepository{db: db}
}

// RegisterToken upserts a device token for a user
func (r *DeviceTokenRepository) RegisterToken(ctx context.Context, userID uuid.UUID, token, platform string, appVersion, deviceModel, osVersion *string) (*DeviceToken, error) {
	query := `
		INSERT INTO device_tokens (user_id, token, platform, app_version, device_model, os_version, is_active, last_used_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, true, NOW(), NOW())
		ON CONFLICT (user_id, token) DO UPDATE SET
			platform = EXCLUDED.platform,
			app_version = EXCLUDED.app_version,
			device_model = EXCLUDED.device_model,
			os_version = EXCLUDED.os_version,
			is_active = true,
			last_used_at = NOW(),
			updated_at = NOW()
		RETURNING id, user_id, token, platform, endpoint_arn, app_version, device_model, os_version, is_active, last_used_at, created_at, updated_at`

	dt := &DeviceToken{}
	err := r.db.QueryRowContext(ctx, query, userID, token, platform, appVersion, deviceModel, osVersion).Scan(
		&dt.ID, &dt.UserID, &dt.Token, &dt.Platform, &dt.EndpointARN,
		&dt.AppVersion, &dt.DeviceModel, &dt.OSVersion, &dt.IsActive,
		&dt.LastUsedAt, &dt.CreatedAt, &dt.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return dt, nil
}

// GetUserTokens returns all active tokens for a user
func (r *DeviceTokenRepository) GetUserTokens(ctx context.Context, userID uuid.UUID) ([]*DeviceToken, error) {
	query := `
		SELECT id, user_id, token, platform, endpoint_arn, app_version, device_model, os_version, is_active, last_used_at, created_at, updated_at
		FROM device_tokens
		WHERE user_id = $1 AND is_active = true
		ORDER BY last_used_at DESC`

	rows, err := r.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tokens []*DeviceToken
	for rows.Next() {
		dt := &DeviceToken{}
		if err := rows.Scan(&dt.ID, &dt.UserID, &dt.Token, &dt.Platform, &dt.EndpointARN,
			&dt.AppVersion, &dt.DeviceModel, &dt.OSVersion, &dt.IsActive,
			&dt.LastUsedAt, &dt.CreatedAt, &dt.UpdatedAt); err != nil {
			return nil, err
		}
		tokens = append(tokens, dt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}
	return tokens, nil
}

// GetUserDeviceTokens returns just the token strings for push notifications
func (r *DeviceTokenRepository) GetUserDeviceTokens(ctx context.Context, userID uuid.UUID) ([]string, error) {
	tokens, err := r.GetUserTokens(ctx, userID)
	if err != nil {
		return nil, err
	}
	result := make([]string, len(tokens))
	for i, t := range tokens {
		result[i] = t.Token
	}
	return result, nil
}

// DeleteToken deactivates a token
func (r *DeviceTokenRepository) DeleteToken(ctx context.Context, token string) error {
	query := `UPDATE device_tokens SET is_active = false, updated_at = NOW() WHERE token = $1`
	_, err := r.db.ExecContext(ctx, query, token)
	return err
}

// DeleteUserToken deactivates a specific token for a user
func (r *DeviceTokenRepository) DeleteUserToken(ctx context.Context, userID uuid.UUID, token string) error {
	query := `UPDATE device_tokens SET is_active = false, updated_at = NOW() WHERE user_id = $1 AND token = $2`
	_, err := r.db.ExecContext(ctx, query, userID, token)
	return err
}

// UpdateEndpointARN updates the SNS endpoint ARN for a token
func (r *DeviceTokenRepository) UpdateEndpointARN(ctx context.Context, tokenID uuid.UUID, endpointARN string) error {
	query := `UPDATE device_tokens SET endpoint_arn = $1, updated_at = NOW() WHERE id = $2`
	_, err := r.db.ExecContext(ctx, query, endpointARN, tokenID)
	return err
}

// DeactivateAllUserTokens deactivates all tokens for a user (used during account deletion)
func (r *DeviceTokenRepository) DeactivateAllUserTokens(ctx context.Context, userID uuid.UUID) error {
	query := `UPDATE device_tokens SET is_active = false, updated_at = NOW() WHERE user_id = $1`
	_, err := r.db.ExecContext(ctx, query, userID)
	return err
}
