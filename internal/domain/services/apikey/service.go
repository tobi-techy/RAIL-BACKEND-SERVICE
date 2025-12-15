package apikey

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

type Service struct {
	db     *sql.DB
	logger *zap.Logger
}

type APIKey struct {
	ID         uuid.UUID  `json:"id"`
	Name       string     `json:"name"`
	KeyPrefix  string     `json:"key_prefix"`
	UserID     *uuid.UUID `json:"user_id,omitempty"`
	Scopes     []string   `json:"scopes"`
	IsActive   bool       `json:"is_active"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

type CreateAPIKeyRequest struct {
	Name      string     `json:"name"`
	UserID    *uuid.UUID `json:"user_id,omitempty"`
	Scopes    []string   `json:"scopes"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type CreateAPIKeyResponse struct {
	APIKey *APIKey `json:"api_key"`
	Key    string  `json:"key"`
}

func NewService(db *sql.DB, logger *zap.Logger) *Service {
	return &Service{
		db:     db,
		logger: logger,
	}
}

// CreateAPIKey creates a new API key
func (s *Service) CreateAPIKey(ctx context.Context, req *CreateAPIKeyRequest) (*CreateAPIKeyResponse, error) {
	// Generate API key
	key, keyPrefix, keyHash, err := s.generateAPIKey()
	if err != nil {
		return nil, fmt.Errorf("failed to generate API key: %w", err)
	}

	apiKey := &APIKey{
		ID:        uuid.New(),
		Name:      req.Name,
		KeyPrefix: keyPrefix,
		UserID:    req.UserID,
		Scopes:    req.Scopes,
		IsActive:  true,
		ExpiresAt: req.ExpiresAt,
		CreatedAt: time.Now(),
	}

	// Store in database
	query := `
		INSERT INTO api_keys (id, name, key_hash, key_prefix, user_id, scopes, is_active, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`

	_, err = s.db.ExecContext(ctx, query,
		apiKey.ID, apiKey.Name, keyHash, apiKey.KeyPrefix,
		apiKey.UserID, apiKey.Scopes, apiKey.IsActive,
		apiKey.ExpiresAt, apiKey.CreatedAt)

	if err != nil {
		return nil, fmt.Errorf("failed to store API key: %w", err)
	}

	s.logger.Info("API key created",
		zap.String("key_id", apiKey.ID.String()),
		zap.String("name", apiKey.Name),
		zap.Strings("scopes", apiKey.Scopes))

	return &CreateAPIKeyResponse{
		APIKey: apiKey,
		Key:    key,
	}, nil
}

// ValidateAPIKey validates an API key and returns its details
func (s *Service) ValidateAPIKey(ctx context.Context, key string) (*APIKey, error) {
	keyHash := s.hashKey(key)

	query := `
		SELECT id, name, key_prefix, user_id, scopes, is_active, last_used_at, expires_at, created_at
		FROM api_keys 
		WHERE key_hash = $1 AND is_active = true AND (expires_at IS NULL OR expires_at > NOW())`

	apiKey := &APIKey{}
	err := s.db.QueryRowContext(ctx, query, keyHash).Scan(
		&apiKey.ID, &apiKey.Name, &apiKey.KeyPrefix, &apiKey.UserID,
		&apiKey.Scopes, &apiKey.IsActive, &apiKey.LastUsedAt,
		&apiKey.ExpiresAt, &apiKey.CreatedAt)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("invalid or expired API key")
		}
		return nil, fmt.Errorf("failed to validate API key: %w", err)
	}

	// Update last used timestamp
	s.updateLastUsed(ctx, apiKey.ID)

	return apiKey, nil
}

// ListAPIKeys returns API keys for a user or all keys if userID is nil
func (s *Service) ListAPIKeys(ctx context.Context, userID *uuid.UUID) ([]*APIKey, error) {
	var query string
	var args []interface{}

	if userID != nil {
		query = `
			SELECT id, name, key_prefix, user_id, scopes, is_active, last_used_at, expires_at, created_at
			FROM api_keys 
			WHERE user_id = $1
			ORDER BY created_at DESC`
		args = []interface{}{*userID}
	} else {
		query = `
			SELECT id, name, key_prefix, user_id, scopes, is_active, last_used_at, expires_at, created_at
			FROM api_keys 
			ORDER BY created_at DESC`
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list API keys: %w", err)
	}
	defer rows.Close()

	var apiKeys []*APIKey
	for rows.Next() {
		apiKey := &APIKey{}
		err := rows.Scan(
			&apiKey.ID, &apiKey.Name, &apiKey.KeyPrefix, &apiKey.UserID,
			&apiKey.Scopes, &apiKey.IsActive, &apiKey.LastUsedAt,
			&apiKey.ExpiresAt, &apiKey.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan API key: %w", err)
		}
		apiKeys = append(apiKeys, apiKey)
	}

	return apiKeys, nil
}

// RevokeAPIKey revokes an API key
func (s *Service) RevokeAPIKey(ctx context.Context, keyID uuid.UUID, userID *uuid.UUID) error {
	var query string
	var args []interface{}

	if userID != nil {
		query = "UPDATE api_keys SET is_active = false, updated_at = NOW() WHERE id = $1 AND user_id = $2"
		args = []interface{}{keyID, *userID}
	} else {
		query = "UPDATE api_keys SET is_active = false, updated_at = NOW() WHERE id = $1"
		args = []interface{}{keyID}
	}

	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to revoke API key: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("API key not found or not authorized")
	}

	s.logger.Info("API key revoked", zap.String("key_id", keyID.String()))
	return nil
}

// UpdateAPIKey updates an API key's properties
func (s *Service) UpdateAPIKey(ctx context.Context, keyID uuid.UUID, name string, scopes []string, userID *uuid.UUID) error {
	var query string
	var args []interface{}

	if userID != nil {
		query = "UPDATE api_keys SET name = $1, scopes = $2, updated_at = NOW() WHERE id = $3 AND user_id = $4"
		args = []interface{}{name, scopes, keyID, *userID}
	} else {
		query = "UPDATE api_keys SET name = $1, scopes = $2, updated_at = NOW() WHERE id = $3"
		args = []interface{}{name, scopes, keyID}
	}

	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to update API key: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("API key not found or not authorized")
	}

	return nil
}

// CleanupExpiredKeys removes expired API keys
func (s *Service) CleanupExpiredKeys(ctx context.Context) error {
	query := `DELETE FROM api_keys WHERE expires_at < NOW() OR (is_active = false AND updated_at < NOW() - INTERVAL '30 days')`
	
	result, err := s.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to cleanup expired API keys: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	s.logger.Info("Cleaned up expired API keys", zap.Int64("rows_affected", rowsAffected))

	return nil
}

// HasScope checks if an API key has a specific scope
func (s *Service) HasScope(apiKey *APIKey, requiredScope string) bool {
	for _, scope := range apiKey.Scopes {
		if scope == requiredScope || scope == "*" {
			return true
		}
	}
	return false
}

func (s *Service) generateAPIKey() (key, prefix, hash string, err error) {
	// Generate 32 random bytes
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", "", "", err
	}

	// Create key with prefix
	keyData := hex.EncodeToString(bytes)
	key = fmt.Sprintf("sk_%s", keyData)
	prefix = key[:12] // First 12 characters for display
	hash = s.hashKey(key)

	return key, prefix, hash, nil
}

func (s *Service) hashKey(key string) string {
	hash := sha256.Sum256([]byte(key))
	return hex.EncodeToString(hash[:])
}

func (s *Service) updateLastUsed(ctx context.Context, keyID uuid.UUID) {
	_, err := s.db.ExecContext(ctx, 
		"UPDATE api_keys SET last_used_at = NOW() WHERE id = $1", keyID)
	if err != nil {
		s.logger.Warn("Failed to update API key last used", zap.Error(err))
	}
}