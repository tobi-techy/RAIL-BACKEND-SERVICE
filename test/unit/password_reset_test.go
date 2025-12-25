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
	tokens map[string]tokenData // key is selector
}

type tokenData struct {
	userID       uuid.UUID
	verifierHash string
	expiresAt    time.Time
	used         bool
}

func (m *MockPasswordResetRepo) CreatePasswordResetToken(ctx context.Context, userID uuid.UUID, selector, verifierHash string, expiresAt time.Time) error {
	if m.tokens == nil {
		m.tokens = make(map[string]tokenData)
	}
	m.tokens[selector] = tokenData{userID: userID, verifierHash: verifierHash, expiresAt: expiresAt, used: false}
	return nil
}

// ValidatePasswordResetToken uses selector-verifier pattern
func (m *MockPasswordResetRepo) ValidatePasswordResetToken(ctx context.Context, rawToken string) (uuid.UUID, error) {
	selector, verifier, err := crypto.ParseSelectorVerifierToken(rawToken)
	if err != nil {
		return uuid.Nil, assert.AnError
	}

	data, exists := m.tokens[selector]
	if !exists || data.used || time.Now().After(data.expiresAt) {
		return uuid.Nil, assert.AnError
	}

	// Single bcrypt comparison of verifier
	if bcrypt.CompareHashAndPassword([]byte(data.verifierHash), []byte(verifier)) != nil {
		return uuid.Nil, assert.AnError
	}

	data.used = true
	m.tokens[selector] = data
	return data.userID, nil
}

func TestPasswordResetFlow(t *testing.T) {
	repo := &MockPasswordResetRepo{}
	ctx := context.Background()
	userID := uuid.New()

	// Generate selector-verifier token
	svToken, err := crypto.GenerateSelectorVerifierToken()
	require.NoError(t, err)
	require.NotEmpty(t, svToken.Selector)
	require.NotEmpty(t, svToken.Verifier)
	require.NotEmpty(t, svToken.FullToken)

	// Store token (simulates ForgotPassword)
	expiresAt := time.Now().Add(1 * time.Hour)
	err = repo.CreatePasswordResetToken(ctx, userID, svToken.Selector, svToken.VerifierHash, expiresAt)
	require.NoError(t, err)

	// Validate with full token (simulates ResetPassword)
	retrievedUserID, err := repo.ValidatePasswordResetToken(ctx, svToken.FullToken)
	require.NoError(t, err)
	assert.Equal(t, userID, retrievedUserID)

	// Token should be marked as used
	_, err = repo.ValidatePasswordResetToken(ctx, svToken.FullToken)
	assert.Error(t, err, "Token should not be reusable")
}

func TestPasswordResetTokenExpiry(t *testing.T) {
	repo := &MockPasswordResetRepo{}
	ctx := context.Background()
	userID := uuid.New()

	svToken, _ := crypto.GenerateSelectorVerifierToken()
	expiresAt := time.Now().Add(-1 * time.Hour) // Expired

	err := repo.CreatePasswordResetToken(ctx, userID, svToken.Selector, svToken.VerifierHash, expiresAt)
	require.NoError(t, err)

	_, err = repo.ValidatePasswordResetToken(ctx, svToken.FullToken)
	assert.Error(t, err, "Expired token should be rejected")
}

func TestPasswordResetWrongToken(t *testing.T) {
	repo := &MockPasswordResetRepo{}
	ctx := context.Background()
	userID := uuid.New()

	svToken, _ := crypto.GenerateSelectorVerifierToken()
	expiresAt := time.Now().Add(1 * time.Hour)

	err := repo.CreatePasswordResetToken(ctx, userID, svToken.Selector, svToken.VerifierHash, expiresAt)
	require.NoError(t, err)

	// Try with wrong token
	wrongToken, _ := crypto.GenerateSelectorVerifierToken()
	_, err = repo.ValidatePasswordResetToken(ctx, wrongToken.FullToken)
	assert.Error(t, err, "Wrong token should be rejected")
}

func TestSelectorVerifierTokenGeneration(t *testing.T) {
	svToken, err := crypto.GenerateSelectorVerifierToken()
	require.NoError(t, err)

	// Verify selector is 32 hex chars (16 bytes)
	assert.Len(t, svToken.Selector, 32)

	// Verify verifier is 64 hex chars (32 bytes)
	assert.Len(t, svToken.Verifier, 64)

	// Verify full token format
	assert.Equal(t, svToken.Selector+":"+svToken.Verifier, svToken.FullToken)

	// Verify parsing works
	selector, verifier, err := crypto.ParseSelectorVerifierToken(svToken.FullToken)
	require.NoError(t, err)
	assert.Equal(t, svToken.Selector, selector)
	assert.Equal(t, svToken.Verifier, verifier)
}

func TestParseSelectorVerifierTokenInvalid(t *testing.T) {
	tests := []struct {
		name  string
		token string
	}{
		{"no separator", "abc123def456"},
		{"empty selector", ":verifier"},
		{"empty verifier", "selector:"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := crypto.ParseSelectorVerifierToken(tt.token)
			assert.Error(t, err)
		})
	}
}
