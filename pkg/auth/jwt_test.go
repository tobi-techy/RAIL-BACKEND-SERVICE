package auth

import (
	"testing"

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
