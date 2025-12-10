package unit

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/pkg/crypto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type MockPasswordResetRepo struct {
	tokens map[string]tokenData
}

type tokenData struct {
	userID    uuid.UUID
	expiresAt time.Time
	used      bool
}

func (m *MockPasswordResetRepo) CreatePasswordResetToken(ctx context.Context, userID uuid.UUID, tokenHash string, expiresAt time.Time) error {
	if m.tokens == nil {
		m.tokens = make(map[string]tokenData)
	}
	m.tokens[tokenHash] = tokenData{userID: userID, expiresAt: expiresAt, used: false}
	return nil
}

func (m *MockPasswordResetRepo) ValidatePasswordResetToken(ctx context.Context, tokenHash string) (uuid.UUID, error) {
	data, exists := m.tokens[tokenHash]
	if !exists || data.used || time.Now().After(data.expiresAt) {
		return uuid.Nil, assert.AnError
	}
	data.used = true
	m.tokens[tokenHash] = data
	return data.userID, nil
}

func TestPasswordResetFlow(t *testing.T) {
	repo := &MockPasswordResetRepo{}
	ctx := context.Background()
	userID := uuid.New()

	// Generate token
	token, err := crypto.GenerateSecureToken()
	require.NoError(t, err)
	require.NotEmpty(t, token)

	// Hash and store token
	tokenHash, err := crypto.HashPassword(token)
	require.NoError(t, err)
	expiresAt := time.Now().Add(1 * time.Hour)
	err = repo.CreatePasswordResetToken(ctx, userID, tokenHash, expiresAt)
	require.NoError(t, err)

	// Validate token
	retrievedUserID, err := repo.ValidatePasswordResetToken(ctx, tokenHash)
	require.NoError(t, err)
	assert.Equal(t, userID, retrievedUserID)

	// Token should be marked as used
	_, err = repo.ValidatePasswordResetToken(ctx, tokenHash)
	assert.Error(t, err, "Token should not be reusable")
}

func TestPasswordResetTokenExpiry(t *testing.T) {
	repo := &MockPasswordResetRepo{}
	ctx := context.Background()
	userID := uuid.New()

	token, _ := crypto.GenerateSecureToken()
	tokenHash, _ := crypto.HashPassword(token)
	expiresAt := time.Now().Add(-1 * time.Hour) // Expired

	err := repo.CreatePasswordResetToken(ctx, userID, tokenHash, expiresAt)
	require.NoError(t, err)

	_, err = repo.ValidatePasswordResetToken(ctx, tokenHash)
	assert.Error(t, err, "Expired token should be rejected")
}
