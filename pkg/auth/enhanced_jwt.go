package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	// ShortAccessTokenTTL is 15 minutes for enhanced security
	ShortAccessTokenTTL = 15 * 60 // 15 minutes in seconds
	// StandardRefreshTokenTTL is 7 days
	StandardRefreshTokenTTL = 7 * 24 * 60 * 60 // 7 days in seconds
)

// EnhancedTokenPair includes additional metadata for token management
type EnhancedTokenPair struct {
	AccessToken       string    `json:"access_token"`
	RefreshToken      string    `json:"refresh_token"`
	AccessExpiresAt   time.Time `json:"access_expires_at"`
	RefreshExpiresAt  time.Time `json:"refresh_expires_at"`
	TokenID           string    `json:"token_id"`
	RefreshTokenID    string    `json:"refresh_token_id"`
}

// EnhancedClaims includes additional security fields
type EnhancedClaims struct {
	UserID   uuid.UUID `json:"user_id"`
	Email    string    `json:"email"`
	Role     string    `json:"role"`
	TokenID  string    `json:"jti"`      // JWT ID for revocation
	TokenType string   `json:"token_type"` // "access" or "refresh"
	jwt.RegisteredClaims
}

// JWTService handles JWT operations with enhanced security
type JWTService struct {
	secret       string
	accessTTL    int
	refreshTTL   int
	blacklist    *TokenBlacklist
}

// NewJWTService creates a new JWT service
func NewJWTService(secret string, accessTTL, refreshTTL int, blacklist *TokenBlacklist) *JWTService {
	// Use short-lived tokens if not specified
	if accessTTL == 0 {
		accessTTL = ShortAccessTokenTTL
	}
	if refreshTTL == 0 {
		refreshTTL = StandardRefreshTokenTTL
	}
	
	return &JWTService{
		secret:     secret,
		accessTTL:  accessTTL,
		refreshTTL: refreshTTL,
		blacklist:  blacklist,
	}
}

// GenerateTokenPairEnhanced generates tokens with rotation support
func (s *JWTService) GenerateTokenPairEnhanced(userID uuid.UUID, email, role string) (*EnhancedTokenPair, error) {
	now := time.Now()
	accessExp := now.Add(time.Duration(s.accessTTL) * time.Second)
	refreshExp := now.Add(time.Duration(s.refreshTTL) * time.Second)
	
	accessTokenID := uuid.New().String()
	refreshTokenID := uuid.New().String()

	// Access token
	accessClaims := EnhancedClaims{
		UserID:    userID,
		Email:     email,
		Role:      role,
		TokenID:   accessTokenID,
		TokenType: "access",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(accessExp),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			Issuer:    "rail_service",
			Subject:   userID.String(),
			ID:        accessTokenID,
		},
	}

	accessToken := jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims)
	accessTokenString, err := accessToken.SignedString([]byte(s.secret))
	if err != nil {
		return nil, fmt.Errorf("failed to sign access token: %w", err)
	}

	// Refresh token
	refreshClaims := EnhancedClaims{
		UserID:    userID,
		TokenID:   refreshTokenID,
		TokenType: "refresh",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(refreshExp),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			Issuer:    "rail_service",
			Subject:   userID.String(),
			ID:        refreshTokenID,
		},
	}

	refreshToken := jwt.NewWithClaims(jwt.SigningMethodHS256, refreshClaims)
	refreshTokenString, err := refreshToken.SignedString([]byte(s.secret))
	if err != nil {
		return nil, fmt.Errorf("failed to sign refresh token: %w", err)
	}

	// Store refresh token in blacklist service for rotation tracking
	if s.blacklist != nil {
		refreshHash := hashTokenString(refreshTokenString)
		s.blacklist.StoreRefreshToken(context.Background(), refreshHash, userID.String(), time.Duration(s.refreshTTL)*time.Second)
	}

	return &EnhancedTokenPair{
		AccessToken:      accessTokenString,
		RefreshToken:     refreshTokenString,
		AccessExpiresAt:  accessExp,
		RefreshExpiresAt: refreshExp,
		TokenID:          accessTokenID,
		RefreshTokenID:   refreshTokenID,
	}, nil
}

// RefreshTokensWithRotation refreshes tokens and invalidates the old refresh token
func (s *JWTService) RefreshTokensWithRotation(ctx context.Context, refreshToken string) (*EnhancedTokenPair, error) {
	// Parse and validate refresh token
	claims, err := s.ValidateEnhancedToken(refreshToken)
	if err != nil {
		return nil, fmt.Errorf("invalid refresh token: %w", err)
	}

	if claims.TokenType != "refresh" {
		return nil, fmt.Errorf("token is not a refresh token")
	}

	// Validate and consume refresh token (one-time use)
	if s.blacklist != nil {
		refreshHash := hashTokenString(refreshToken)
		_, err := s.blacklist.ValidateRefreshToken(ctx, refreshHash)
		if err != nil {
			return nil, fmt.Errorf("refresh token already used or invalid: %w", err)
		}
	}

	// Generate new token pair
	return s.GenerateTokenPairEnhanced(claims.UserID, claims.Email, claims.Role)
}

// ValidateEnhancedToken validates a token and returns enhanced claims
func (s *JWTService) ValidateEnhancedToken(tokenString string) (*EnhancedClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &EnhancedClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(s.secret), nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to parse token: %w", err)
	}

	if claims, ok := token.Claims.(*EnhancedClaims); ok && token.Valid {
		return claims, nil
	}

	return nil, fmt.Errorf("invalid token")
}

// RevokeToken adds a token to the blacklist
func (s *JWTService) RevokeToken(ctx context.Context, tokenString string, expiresAt time.Time) error {
	if s.blacklist == nil {
		return nil
	}
	tokenHash := hashTokenString(tokenString)
	return s.blacklist.Blacklist(ctx, tokenHash, expiresAt)
}

// RevokeAllUserTokens invalidates all tokens for a user
func (s *JWTService) RevokeAllUserTokens(ctx context.Context, userID string) error {
	if s.blacklist == nil {
		return nil
	}
	return s.blacklist.BlacklistUserTokens(ctx, userID, time.Duration(s.refreshTTL)*time.Second)
}

// IsTokenRevoked checks if a token has been revoked
func (s *JWTService) IsTokenRevoked(ctx context.Context, tokenString string) (bool, error) {
	if s.blacklist == nil {
		return false, nil
	}
	tokenHash := hashTokenString(tokenString)
	return s.blacklist.IsBlacklisted(ctx, tokenHash)
}

func hashTokenString(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}
