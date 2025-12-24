package unit

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/pkg/crypto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

type MockPasswordResetRepo struct {
	tokens map[string]tokenData // key is token ID, not hash
}

type tokenData struct {
	userID    uuid.UUID
	tokenHash string
	expiresAt time.Time
	used      bool
}

func (m *MockPasswordResetRepo) CreatePasswordResetToken(ctx context.Context, userID uuid.UUID, tokenHash string, expiresAt time.Time) error {
	if m.tokens == nil {
		m.tokens = make(map[string]tokenData)
	}
	id := uuid.New().String()
	m.tokens[id] = tokenData{userID: userID, tokenHash: tokenHash, expiresAt: expiresAt, used: false}
	return nil
}

// ValidatePasswordResetToken now accepts raw token and uses bcrypt comparison
func (m *MockPasswordResetRepo) ValidatePasswordResetToken(ctx context.Context, rawToken string) (uuid.UUID, error) {
	for id, data := range m.tokens {
		if data.used || time.Now().After(data.expiresAt) {
			continue
		}
		// Use bcrypt comparison like the real implementation
		if bcrypt.CompareHashAndPassword([]byte(data.tokenHash), []byte(rawToken)) == nil {
			data.used = true
			m.tokens[id] = data
			return data.userID, nil
		}
	}
	return uuid.Nil, assert.AnError
}

func TestPasswordResetFlow(t *testing.T) {
	repo := &MockPasswordResetRepo{}
	ctx := context.Background()
	userID := uuid.New()

	// Generate token
	token, err := crypto.GenerateSecureToken()
	require.NoError(t, err)
	require.NotEmpty(t, token)

	// Hash and store token (simulates ForgotPassword)
	tokenHash, err := crypto.HashPassword(token)
	require.NoError(t, err)
	expiresAt := time.Now().Add(1 * time.Hour)
	err = repo.CreatePasswordResetToken(ctx, userID, tokenHash, expiresAt)
	require.NoError(t, err)

	// Validate with raw token (simulates ResetPassword)
	retrievedUserID, err := repo.ValidatePasswordResetToken(ctx, token)
	require.NoError(t, err)
	assert.Equal(t, userID, retrievedUserID)

	// Token should be marked as used
	_, err = repo.ValidatePasswordResetToken(ctx, token)
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

	_, err = repo.ValidatePasswordResetToken(ctx, token)
	assert.Error(t, err, "Expired token should be rejected")
}

func TestPasswordResetWrongToken(t *testing.T) {
	repo := &MockPasswordResetRepo{}
	ctx := context.Background()
	userID := uuid.New()

	token, _ := crypto.GenerateSecureToken()
	tokenHash, _ := crypto.HashPassword(token)
	expiresAt := time.Now().Add(1 * time.Hour)

	err := repo.CreatePasswordResetToken(ctx, userID, tokenHash, expiresAt)
	require.NoError(t, err)

	// Try with wrong token
	wrongToken, _ := crypto.GenerateSecureToken()
	_, err = repo.ValidatePasswordResetToken(ctx, wrongToken)
	assert.Error(t, err, "Wrong token should be rejected")
}
