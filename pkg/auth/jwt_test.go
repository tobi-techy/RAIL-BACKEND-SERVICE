package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func TestGenerateTokenPairCreatesUniqueTokensWithinSameSecond(t *testing.T) {
	userID := uuid.New()

	first, err := GenerateTokenPair(userID, "user@example.com", "user", "test-secret", 3600, 86400)
	if err != nil {
		t.Fatalf("GenerateTokenPair first call failed: %v", err)
	}
	second, err := GenerateTokenPair(userID, "user@example.com", "user", "test-secret", 3600, 86400)
	if err != nil {
		t.Fatalf("GenerateTokenPair second call failed: %v", err)
	}

	if first.AccessToken == second.AccessToken {
		t.Fatal("access tokens should be unique even when generated in the same second")
	}
	if first.RefreshToken == second.RefreshToken {
		t.Fatal("refresh tokens should be unique even when generated in the same second")
	}

	assertTokenID(t, first.AccessToken, "test-secret")
	assertTokenID(t, first.RefreshToken, "test-secret")
}

func TestGenerateAccessTokenCreatesUniqueTokensWithinSameSecond(t *testing.T) {
	userID := uuid.New()

	first, _, err := GenerateAccessToken(userID, "user@example.com", "user", "test-secret", 3600)
	if err != nil {
		t.Fatalf("GenerateAccessToken first call failed: %v", err)
	}
	second, _, err := GenerateAccessToken(userID, "user@example.com", "user", "test-secret", 3600)
	if err != nil {
		t.Fatalf("GenerateAccessToken second call failed: %v", err)
	}

	if first == second {
		t.Fatal("access tokens should be unique even when generated in the same second")
	}

	assertTokenID(t, first, "test-secret")
}

func TestVoiceSessionTokenValidatesOnlyAsVoiceSession(t *testing.T) {
	userID := uuid.New()

	token, expiresAt, err := GenerateVoiceSessionToken(userID, "test-secret", time.Minute)
	if err != nil {
		t.Fatalf("GenerateVoiceSessionToken failed: %v", err)
	}
	if time.Until(expiresAt) <= 0 {
		t.Fatal("voice session token should expire in the future")
	}

	got, err := ValidateVoiceSessionToken(token, "test-secret")
	if err != nil {
		t.Fatalf("ValidateVoiceSessionToken failed: %v", err)
	}
	if got != userID {
		t.Fatalf("expected user ID %s, got %s", userID, got)
	}
	if _, err := ValidateToken(token, "test-secret"); err == nil {
		t.Fatal("voice session token must not validate as an access token")
	}
}

// TestVoiceSessionTokenRejectsExpiredTokens verifies the JWT library's built-in
// expiration validation rejects tokens past their exp claim.
func TestVoiceSessionTokenRejectsExpiredTokens(t *testing.T) {
	userID := uuid.New()
	token, _, err := GenerateVoiceSessionToken(userID, "test-secret", 50*time.Millisecond)
	if err != nil {
		t.Fatalf("GenerateVoiceSessionToken failed: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	_, err = ValidateVoiceSessionToken(token, "test-secret")
	if err == nil {
		t.Fatal("expected error for expired token")
	}
	errLower := strings.ToLower(err.Error())
	if !strings.Contains(errLower, "expired") && !strings.Contains(errLower, "exp") {
		t.Fatalf("expected error containing 'expired' or 'exp', got: %v", err)
	}
}

func assertTokenID(t *testing.T, tokenString string, secret string) {
	t.Helper()

	token, err := jwt.ParseWithClaims(tokenString, jwt.MapClaims{}, func(token *jwt.Token) (interface{}, error) {
		return []byte(secret), nil
	})
	if err != nil {
		t.Fatalf("failed to parse token: %v", err)
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		t.Fatal("token claims should be valid")
	}

	jti, ok := claims["jti"].(string)
	if !ok || jti == "" {
		t.Fatal("token should include a non-empty jti claim")
	}
}
