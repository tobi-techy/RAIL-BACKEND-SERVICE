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

func TestGenerateAgentTokenAndValidateAnyAccessToken(t *testing.T) {
	userID := uuid.New()
	secret := "test-secret"

	agentToken, expiresAt, err := GenerateAgentToken(userID, "agent@example.com", "verified", secret, 120)
	if err != nil {
		t.Fatalf("GenerateAgentToken failed: %v", err)
	}
	if time.Until(expiresAt) <= 0 {
		t.Fatal("agent token should expire in the future")
	}

	accessToken, _, err := GenerateAccessToken(userID, "user@example.com", "user", secret, 3600)
	if err != nil {
		t.Fatalf("GenerateAccessToken failed: %v", err)
	}

	refreshPair, err := GenerateTokenPair(userID, "user@example.com", "user", secret, 3600, 86400)
	if err != nil {
		t.Fatalf("GenerateTokenPair failed: %v", err)
	}

	missingType, err := signClaims(Claims{
		UserID: userID,
		Email:  "user@example.com",
		Role:   "user",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "rail_service",
			Subject:   userID.String(),
			ID:        uuid.NewString(),
		},
	}, secret)
	if err != nil {
		t.Fatalf("sign missing-type token: %v", err)
	}

	garbageType, err := signClaims(Claims{
		UserID:    userID,
		Email:     "user@example.com",
		Role:      "user",
		TokenType: "not-a-real-type",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "rail_service",
			Subject:   userID.String(),
			ID:        uuid.NewString(),
		},
	}, secret)
	if err != nil {
		t.Fatalf("sign garbage-type token: %v", err)
	}

	tests := []struct {
		name       string
		token      string
		wantAnyErr bool
		wantAgent  bool
		wantUserID uuid.UUID
	}{
		{name: "agent accepted by ValidateAnyAccessToken", token: agentToken, wantAgent: true, wantUserID: userID},
		{name: "access accepted by ValidateAnyAccessToken", token: accessToken, wantAgent: false, wantUserID: userID},
		{name: "missing token_type rejected", token: missingType, wantAnyErr: true},
		{name: "garbage token_type rejected", token: garbageType, wantAnyErr: true},
		{name: "refresh rejected by ValidateAnyAccessToken", token: refreshPair.RefreshToken, wantAnyErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims, isAgent, err := ValidateAnyAccessToken(tt.token, secret)
			if tt.wantAnyErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateAnyAccessToken: %v", err)
			}
			if isAgent != tt.wantAgent {
				t.Fatalf("isAgent=%v, want %v", isAgent, tt.wantAgent)
			}
			if claims.UserID != tt.wantUserID {
				t.Fatalf("user id %s, want %s", claims.UserID, tt.wantUserID)
			}
		})
	}

	if _, err := ValidateToken(agentToken, secret); err == nil {
		t.Fatal("ValidateToken must reject agent tokens so interactive-session callers stay strict")
	}
	if _, err := ValidateToken(accessToken, secret); err != nil {
		t.Fatalf("ValidateToken should still accept access tokens: %v", err)
	}
	if _, err := ValidateToken(missingType, secret); err == nil {
		t.Fatal("ValidateToken must reject tokens with missing token_type")
	}
	if _, err := ValidateToken(garbageType, secret); err == nil {
		t.Fatal("ValidateToken must reject tokens with garbage token_type")
	}
}

func signClaims(claims Claims, secret string) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
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
