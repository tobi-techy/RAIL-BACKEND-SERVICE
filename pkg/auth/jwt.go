package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Claims represents JWT claims
type Claims struct {
	UserID    uuid.UUID `json:"user_id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	TokenType string    `json:"token_type,omitempty"` // access
	jwt.RegisteredClaims
}

type RefreshClaims struct {
	TokenType string `json:"token_type,omitempty"` // refresh
	jwt.RegisteredClaims
}

type VoiceSessionClaims struct {
	UserID    uuid.UUID `json:"user_id"`
	TokenType string    `json:"token_type,omitempty"` // voice_session
	jwt.RegisteredClaims
}

// TokenPair represents access and refresh tokens
type TokenPair struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// GenerateTokenPair generates a new JWT token pair
func GenerateTokenPair(userID uuid.UUID, email, role, secret string, accessTTL, refreshTTL int) (*TokenPair, error) {
	now := time.Now()
	accessExp := now.Add(time.Duration(accessTTL) * time.Second)
	refreshExp := now.Add(time.Duration(refreshTTL) * time.Second)

	// Access token claims
	accessClaims := Claims{
		UserID:    userID,
		Email:     email,
		Role:      role,
		TokenType: "access",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(accessExp),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			Issuer:    "rail_service",
			Subject:   userID.String(),
			ID:        uuid.NewString(),
		},
	}

	// Refresh token claims (minimal)
	refreshClaims := RefreshClaims{
		TokenType: "refresh",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(refreshExp),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			Issuer:    "rail_service",
			Subject:   userID.String(),
			ID:        uuid.NewString(),
		},
	}

	// Create access token
	accessToken := jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims)
	accessTokenString, err := accessToken.SignedString([]byte(secret))
	if err != nil {
		return nil, fmt.Errorf("failed to sign access token: %w", err)
	}

	// Create refresh token
	refreshToken := jwt.NewWithClaims(jwt.SigningMethodHS256, refreshClaims)
	refreshTokenString, err := refreshToken.SignedString([]byte(secret))
	if err != nil {
		return nil, fmt.Errorf("failed to sign refresh token: %w", err)
	}

	return &TokenPair{
		AccessToken:  accessTokenString,
		RefreshToken: refreshTokenString,
		ExpiresAt:    accessExp,
	}, nil
}

// ValidateToken validates a JWT token and returns the claims
func ValidateToken(tokenString, secret string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(secret), nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to parse token: %w", err)
	}

	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		// SECURITY: Reject refresh tokens used as access tokens.
		// Both token types share the same signing key, so without this check
		// a 30-day refresh token could be used as a 1-hour access token.
		if claims.TokenType != "access" {
			return nil, fmt.Errorf("invalid token type: expected access token")
		}
		return claims, nil
	}

	return nil, fmt.Errorf("invalid token")
}

func GenerateVoiceSessionToken(userID uuid.UUID, secret string, ttl time.Duration) (string, time.Time, error) {
	if ttl <= 0 {
		ttl = time.Minute
	}
	now := time.Now()
	expiresAt := now.Add(ttl)
	claims := VoiceSessionClaims{
		UserID:    userID,
		TokenType: "voice_session",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			Issuer:    "rail_service",
			Subject:   userID.String(),
			Audience:  []string{"rail_voice_session"},
			ID:        uuid.NewString(),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(secret))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to sign voice session token: %w", err)
	}
	return tokenString, expiresAt, nil
}

func ValidateVoiceSessionToken(tokenString, secret string) (uuid.UUID, error) {
	token, err := jwt.ParseWithClaims(tokenString, &VoiceSessionClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(secret), nil
	}, jwt.WithAudience("rail_voice_session"))
	if err != nil {
		return uuid.Nil, fmt.Errorf("failed to parse voice session token: %w", err)
	}
	claims, ok := token.Claims.(*VoiceSessionClaims)
	if !ok || !token.Valid {
		return uuid.Nil, fmt.Errorf("invalid voice session token")
	}
	if claims.TokenType != "voice_session" {
		return uuid.Nil, fmt.Errorf("invalid token type: expected voice session token")
	}
	if claims.UserID == uuid.Nil {
		return uuid.Nil, fmt.Errorf("voice session token missing user ID")
	}
	if claims.NotBefore != nil && claims.NotBefore.Time.After(time.Now()) {
		return uuid.Nil, fmt.Errorf("voice session token not yet valid")
	}
	if claims.Issuer != "rail_service" {
		return uuid.Nil, fmt.Errorf("voice session token invalid issuer")
	}
	if claims.Subject != claims.UserID.String() {
		return uuid.Nil, fmt.Errorf("voice session token subject mismatch")
	}
	return claims.UserID, nil
}

// ValidateRefreshToken validates a refresh token and returns the user ID
func ValidateRefreshToken(refreshToken, secret string) (uuid.UUID, error) {
	token, err := jwt.ParseWithClaims(refreshToken, jwt.MapClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(secret), nil
	})

	if err != nil {
		return uuid.Nil, fmt.Errorf("failed to parse refresh token: %w", err)
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return uuid.Nil, fmt.Errorf("invalid refresh token")
	}

	tokenType, hasTokenType := claims["token_type"]
	if !hasTokenType {
		return uuid.Nil, fmt.Errorf("invalid refresh token: missing token_type claim")
	}
	if tokenTypeStr, ok := tokenType.(string); !ok || tokenTypeStr != "refresh" {
		return uuid.Nil, fmt.Errorf("invalid refresh token type")
	}

	sub, ok := claims["sub"].(string)
	if !ok || sub == "" {
		return uuid.Nil, fmt.Errorf("invalid user ID in token subject")
	}

	userID, err := uuid.Parse(sub)
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid user ID in token: %w", err)
	}

	return userID, nil
}

// GenerateAccessToken generates a new access token with the provided user details
func GenerateAccessToken(userID uuid.UUID, email, role, secret string, accessTTL int) (string, time.Time, error) {
	now := time.Now()
	accessExp := now.Add(time.Duration(accessTTL) * time.Second)

	accessClaims := Claims{
		UserID:    userID,
		Email:     email,
		Role:      role,
		TokenType: "access",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(accessExp),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			Issuer:    "rail_service",
			Subject:   userID.String(),
			ID:        uuid.NewString(),
		},
	}

	accessToken := jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims)
	accessTokenString, err := accessToken.SignedString([]byte(secret))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to sign access token: %w", err)
	}

	return accessTokenString, accessExp, nil
}
