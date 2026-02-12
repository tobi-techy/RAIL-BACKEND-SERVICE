package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// DeviceBoundClaims extends JWT claims with device binding
type DeviceBoundClaims struct {
	UserID            uuid.UUID `json:"user_id"`
	Email             string    `json:"email"`
	Role              string    `json:"role"`
	DeviceID          string    `json:"device_id"`
	DeviceFingerprint string    `json:"device_fingerprint"`
	SessionID         string    `json:"session_id"`
	BindingHash       string    `json:"binding_hash"`
	TokenType         string    `json:"token_type"`
	IssuedFromIP      string    `json:"issued_from_ip,omitempty"`
	jwt.RegisteredClaims
}

// DeviceBindingConfig holds device binding configuration
type DeviceBindingConfig struct {
	Enabled               bool
	MaxConcurrentSessions int
	SessionTTL            time.Duration
	StrictValidation      bool
}

// DefaultDeviceBindingConfig returns sensible defaults
func DefaultDeviceBindingConfig() DeviceBindingConfig {
	return DeviceBindingConfig{
		Enabled:               true,
		MaxConcurrentSessions: 3,
		SessionTTL:            24 * time.Hour,
		StrictValidation:      true,
	}
}

// SessionStore interface for session persistence
type SessionStore interface {
	CreateSession(ctx context.Context, session *DeviceSession) error
	GetSession(ctx context.Context, sessionID string) (*DeviceSession, error)
	GetSessionsByUser(ctx context.Context, userID uuid.UUID) ([]*DeviceSession, error)
	CountActiveSessions(ctx context.Context, userID uuid.UUID) (int, error)
	GetOldestSession(ctx context.Context, userID uuid.UUID) (*DeviceSession, error)
	RevokeSession(ctx context.Context, sessionID string) error
	RevokeAllUserSessions(ctx context.Context, userID uuid.UUID) error
	UpdateLastUsed(ctx context.Context, sessionID string) error
}

// DeviceSession represents a device-bound session
type DeviceSession struct {
	ID                uuid.UUID
	UserID            uuid.UUID
	DeviceID          string
	SessionID         string
	BindingHash       string
	IPAddress         string
	UserAgent         string
	DeviceFingerprint string
	IsActive          bool
	CreatedAt         time.Time
	ExpiresAt         time.Time
	LastUsedAt        time.Time
	RevokedAt         *time.Time
}

// AuditLogger interface for security audit logging
type AuditLogger interface {
	LogDeviceBinding(ctx context.Context, entry *DeviceBindingAuditEntry) error
}

// DeviceBindingAuditEntry represents an audit log entry
type DeviceBindingAuditEntry struct {
	UserID    uuid.UUID
	SessionID *uuid.UUID
	DeviceID  string
	Action    string
	IPAddress string
	UserAgent string
	RiskScore float64
	Metadata  map[string]interface{}
}

// DeviceBoundJWTService handles device-bound JWT operations
type DeviceBoundJWTService struct {
	jwtService   *JWTService
	sessionStore SessionStore
	auditLogger  AuditLogger
	config       DeviceBindingConfig
	logger       *zap.Logger
}

// NewDeviceBoundJWTService creates a new device-bound JWT service
func NewDeviceBoundJWTService(
	jwtService *JWTService,
	sessionStore SessionStore,
	auditLogger AuditLogger,
	config DeviceBindingConfig,
	logger *zap.Logger,
) *DeviceBoundJWTService {
	return &DeviceBoundJWTService{
		jwtService:   jwtService,
		sessionStore: sessionStore,
		auditLogger:  auditLogger,
		config:       config,
		logger:       logger,
	}
}

// GenerateBindingHash creates a device binding hash
func GenerateBindingHash(userID, deviceID, fingerprint, sessionID string) string {
	data := fmt.Sprintf("%s:%s:%s:%s:RAIL_DEVICE_BIND", userID, deviceID, fingerprint, sessionID)
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}

// GenerateDeviceBoundTokens creates tokens bound to a specific device
func (s *DeviceBoundJWTService) GenerateDeviceBoundTokens(
	ctx context.Context,
	userID uuid.UUID,
	email, role string,
	deviceFingerprint, ipAddress, userAgent string,
) (*EnhancedTokenPair, error) {
	// Enforce concurrent session limit
	activeSessions, err := s.sessionStore.CountActiveSessions(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to count sessions: %w", err)
	}

	if activeSessions >= s.config.MaxConcurrentSessions {
		oldest, err := s.sessionStore.GetOldestSession(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("failed to get oldest session: %w", err)
		}
		if oldest != nil {
			if err := s.sessionStore.RevokeSession(ctx, oldest.SessionID); err != nil {
				s.logger.Warn("Failed to revoke oldest session", zap.Error(err))
			}
			s.logAudit(ctx, userID, &oldest.ID, oldest.DeviceID, "session_revoked_limit", ipAddress, userAgent, 0, nil)
		}
	}

	// Create session
	sessionID := uuid.New().String()
	bindingHash := GenerateBindingHash(userID.String(), deviceFingerprint, deviceFingerprint, sessionID)

	session := &DeviceSession{
		ID:                uuid.New(),
		UserID:            userID,
		DeviceID:          deviceFingerprint,
		SessionID:         sessionID,
		BindingHash:       bindingHash,
		IPAddress:         ipAddress,
		UserAgent:         userAgent,
		DeviceFingerprint: deviceFingerprint,
		IsActive:          true,
		CreatedAt:         time.Now(),
		ExpiresAt:         time.Now().Add(s.config.SessionTTL),
		LastUsedAt:        time.Now(),
	}

	if err := s.sessionStore.CreateSession(ctx, session); err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}

	// Generate tokens with device binding claims
	tokens, err := s.generateTokensWithBinding(userID, email, role, deviceFingerprint, sessionID, bindingHash, ipAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to generate tokens: %w", err)
	}

	s.logAudit(ctx, userID, &session.ID, deviceFingerprint, "session_created", ipAddress, userAgent, 0, nil)

	return tokens, nil
}

func (s *DeviceBoundJWTService) generateTokensWithBinding(
	userID uuid.UUID,
	email, role, deviceFingerprint, sessionID, bindingHash, ipAddress string,
) (*EnhancedTokenPair, error) {
	now := time.Now()
	accessExp := now.Add(time.Duration(ShortAccessTokenTTL) * time.Second)
	refreshExp := now.Add(time.Duration(StandardRefreshTokenTTL) * time.Second)

	accessTokenID := uuid.New().String()
	refreshTokenID := uuid.New().String()

	// Access token with device binding
	accessClaims := DeviceBoundClaims{
		UserID:            userID,
		Email:             email,
		Role:              role,
		DeviceID:          deviceFingerprint,
		DeviceFingerprint: deviceFingerprint,
		SessionID:         sessionID,
		BindingHash:       bindingHash,
		TokenType:         "access",
		IssuedFromIP:      ipAddress,
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
	accessTokenString, err := accessToken.SignedString([]byte(s.jwtService.secret))
	if err != nil {
		return nil, fmt.Errorf("failed to sign access token: %w", err)
	}

	// Refresh token with device binding
	refreshClaims := DeviceBoundClaims{
		UserID:            userID,
		DeviceID:          deviceFingerprint,
		DeviceFingerprint: deviceFingerprint,
		SessionID:         sessionID,
		BindingHash:       bindingHash,
		TokenType:         "refresh",
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
	refreshTokenString, err := refreshToken.SignedString([]byte(s.jwtService.secret))
	if err != nil {
		return nil, fmt.Errorf("failed to sign refresh token: %w", err)
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

// ValidateDeviceBoundToken validates token and returns device-bound claims
func (s *DeviceBoundJWTService) ValidateDeviceBoundToken(tokenString string) (*DeviceBoundClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &DeviceBoundClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(s.jwtService.secret), nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to parse token: %w", err)
	}

	claims, ok := token.Claims.(*DeviceBoundClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}

	return claims, nil
}

// ValidateDeviceBinding verifies the current device matches the token's device
func (s *DeviceBoundJWTService) ValidateDeviceBinding(
	ctx context.Context,
	claims *DeviceBoundClaims,
	currentFingerprint string,
) error {
	if !s.config.Enabled {
		return nil
	}

	// Verify device fingerprint matches
	if s.config.StrictValidation && claims.DeviceFingerprint != currentFingerprint {
		s.logAudit(ctx, claims.UserID, nil, currentFingerprint, "device_mismatch",
			"", "", 0.8, map[string]interface{}{
				"expected_device": claims.DeviceFingerprint,
				"actual_device":   currentFingerprint,
			})
		return fmt.Errorf("device mismatch")
	}

	// Verify session is still active
	session, err := s.sessionStore.GetSession(ctx, claims.SessionID)
	if err != nil {
		return fmt.Errorf("session lookup failed: %w", err)
	}

	if session == nil || !session.IsActive {
		return fmt.Errorf("session invalid or revoked")
	}

	if time.Now().After(session.ExpiresAt) {
		return fmt.Errorf("session expired")
	}

	// Update last used
	_ = s.sessionStore.UpdateLastUsed(ctx, claims.SessionID)

	return nil
}

// RevokeSession revokes a specific session
func (s *DeviceBoundJWTService) RevokeSession(ctx context.Context, sessionID string) error {
	return s.sessionStore.RevokeSession(ctx, sessionID)
}

// RevokeAllUserSessions revokes all sessions for a user (security incident response)
func (s *DeviceBoundJWTService) RevokeAllUserSessions(ctx context.Context, userID uuid.UUID) error {
	s.logAudit(ctx, userID, nil, "", "all_sessions_revoked", "", "", 1.0, nil)
	return s.sessionStore.RevokeAllUserSessions(ctx, userID)
}

// GetUserSessions returns all active sessions for a user
func (s *DeviceBoundJWTService) GetUserSessions(ctx context.Context, userID uuid.UUID) ([]*DeviceSession, error) {
	return s.sessionStore.GetSessionsByUser(ctx, userID)
}

func (s *DeviceBoundJWTService) logAudit(
	ctx context.Context,
	userID uuid.UUID,
	sessionID *uuid.UUID,
	deviceID, action, ipAddress, userAgent string,
	riskScore float64,
	metadata map[string]interface{},
) {
	if s.auditLogger == nil {
		return
	}

	entry := &DeviceBindingAuditEntry{
		UserID:    userID,
		SessionID: sessionID,
		DeviceID:  deviceID,
		Action:    action,
		IPAddress: ipAddress,
		UserAgent: userAgent,
		RiskScore: riskScore,
		Metadata:  metadata,
	}

	if err := s.auditLogger.LogDeviceBinding(ctx, entry); err != nil {
		s.logger.Warn("Failed to log device binding audit", zap.Error(err))
	}
}
