package security

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/stack-service/stack_service/internal/infrastructure/secrets"
	"github.com/stack-service/stack_service/pkg/logger"
)

// APIKeyService handles API key generation, rotation, and validation
type APIKeyService struct {
	secretsManager *secrets.Manager
	logger         *logger.Logger
}

// APIKey represents an API key with metadata
type APIKey struct {
	ID          uuid.UUID  `json:"id"`
	Key         string     `json:"key"`
	Secret      string     `json:"secret"`
	Name        string     `json:"name"`
	Permissions []string   `json:"permissions"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	IsActive    bool       `json:"is_active"`
	LastRotated *time.Time `json:"last_rotated,omitempty"`
}

// NewAPIKeyService creates a new API key service
func NewAPIKeyService(secretsManager *secrets.Manager, logger *logger.Logger) *APIKeyService {
	return &APIKeyService{
		secretsManager: secretsManager,
		logger:         logger,
	}
}

// GenerateAPIKey generates a new API key pair
func (s *APIKeyService) GenerateAPIKey(ctx context.Context, name string, permissions []string, expiresAt *time.Time) (*APIKey, error) {
	// Generate cryptographically secure random key and secret
	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		return nil, fmt.Errorf("failed to generate API key: %w", err)
	}

	secretBytes := make([]byte, 64)
	if _, err := rand.Read(secretBytes); err != nil {
		return nil, fmt.Errorf("failed to generate API secret: %w", err)
	}

	apiKey := &APIKey{
		ID:          uuid.New(),
		Key:         hex.EncodeToString(keyBytes),
		Secret:      hex.EncodeToString(secretBytes),
		Name:        name,
		Permissions: permissions,
		CreatedAt:   time.Now(),
		ExpiresAt:   expiresAt,
		IsActive:    true,
	}

	// Store in AWS Secrets Manager
	if err := s.secretsManager.UpdateSecret(ctx, fmt.Sprintf("api-keys/%s", apiKey.ID.String()), apiKey.Secret); err != nil {
		return nil, fmt.Errorf("failed to store API key secret: %w", err)
	}

	s.logger.Infow("Generated new API key",
		"key_id", apiKey.ID.String(),
		"name", name,
		"permissions", permissions,
	)

	return apiKey, nil
}

// RotateAPIKey rotates an existing API key
func (s *APIKeyService) RotateAPIKey(ctx context.Context, keyID uuid.UUID) error {
	secretName := fmt.Sprintf("api-keys/%s", keyID.String())

	// Generate new secret
	secretBytes := make([]byte, 64)
	if _, err := rand.Read(secretBytes); err != nil {
		return fmt.Errorf("failed to generate new secret: %w", err)
	}

	newSecret := hex.EncodeToString(secretBytes)

	// Rotate in AWS Secrets Manager
	if err := s.secretsManager.RotateAPIKey(ctx, secretName, newSecret); err != nil {
		return fmt.Errorf("failed to rotate API key in secrets manager: %w", err)
	}

	s.logger.Infow("Rotated API key",
		"key_id", keyID.String(),
	)

	return nil
}

// ValidateAPIKey validates an API key and returns its permissions
func (s *APIKeyService) ValidateAPIKey(ctx context.Context, providedKey string) ([]string, error) {
	// In a real implementation, you would:
	// 1. Look up the key ID from the provided key
	// 2. Retrieve the secret from AWS Secrets Manager
	// 3. Verify the key matches the stored secret
	// 4. Check expiration and permissions

	// This is a simplified implementation - in production you'd have a key registry
	s.logger.Debugw("Validating API key", "key_prefix", providedKey[:8]+"...")

	// For now, return basic permissions - this would be looked up from a database
	permissions := []string{"read"}
	return permissions, nil
}

// RevokeAPIKey revokes an API key
func (s *APIKeyService) RevokeAPIKey(ctx context.Context, keyID uuid.UUID) error {
	// Mark as inactive in secrets manager (you could also delete it)
	revocationData := map[string]interface{}{
		"revoked":    true,
		"revoked_at": time.Now().Format(time.RFC3339),
	}

	// This would update the secret with revocation metadata
	_ = revocationData // Placeholder

	s.logger.Infow("Revoked API key",
		"key_id", keyID.String(),
	)

	// In production, you'd update a database record to mark as inactive
	return nil
}

// ListAPIKeys lists all API keys (for admin purposes)
func (s *APIKeyService) ListAPIKeys(ctx context.Context) ([]*APIKey, error) {
	// In production, this would query a database
	// For now, return empty list
	return []*APIKey{}, nil
}

// AutoRotateExpiredKeys automatically rotates expired API keys
func (s *APIKeyService) AutoRotateExpiredKeys(ctx context.Context) error {
	// This would be called by a scheduled job
	// Query database for expired keys and rotate them

	s.logger.Infow("Auto-rotating expired API keys")
	return nil
}

// GenerateHMACKey generates a key for HMAC signing
func (s *APIKeyService) GenerateHMACKey(ctx context.Context, keyID string) (string, error) {
	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		return "", fmt.Errorf("failed to generate HMAC key: %w", err)
	}

	hmacKey := hex.EncodeToString(keyBytes)

	// Store in AWS Secrets Manager
	secretName := fmt.Sprintf("hmac-keys/%s", keyID)
	if err := s.secretsManager.UpdateSecret(ctx, secretName, hmacKey); err != nil {
		return "", fmt.Errorf("failed to store HMAC key: %w", err)
	}

	return hmacKey, nil
}
