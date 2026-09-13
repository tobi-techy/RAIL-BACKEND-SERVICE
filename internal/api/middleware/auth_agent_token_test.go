package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/session"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/rail-service/rail_service/pkg/auth"
	"github.com/stretchr/testify/require"
)

type stubSessionValidator struct {
	called int
	err    error
	info   *SessionInfo
}

func (s *stubSessionValidator) ValidateSession(context.Context, string) (*SessionInfo, error) {
	s.called++
	if s.err != nil {
		return nil, s.err
	}
	if s.info != nil {
		return s.info, nil
	}
	return &SessionInfo{ID: uuid.New(), UserID: uuid.New()}, nil
}

func TestAuthenticationAgentTokenSkipsSessionLookup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	secret := "middleware-test-secret"
	cfg := &config.Config{JWT: config.JWTConfig{Secret: secret}}
	userID := uuid.New()

	agentToken, _, err := auth.GenerateAgentToken(userID, "agent@example.com", "verified", secret, 120)
	require.NoError(t, err)
	accessToken, _, err := auth.GenerateAccessToken(userID, "user@example.com", "user", secret, 3600)
	require.NoError(t, err)

	missingType, err := signRawClaims(auth.Claims{
		UserID: userID,
		Email:  "user@example.com",
		Role:   "user",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			Issuer:    "rail_service",
			Subject:   userID.String(),
			ID:        uuid.NewString(),
		},
	}, secret)
	require.NoError(t, err)

	garbageType, err := signRawClaims(auth.Claims{
		UserID:    userID,
		Email:     "user@example.com",
		Role:      "user",
		TokenType: "not-a-real-type",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			Issuer:    "rail_service",
			Subject:   userID.String(),
			ID:        uuid.NewString(),
		},
	}, secret)
	require.NoError(t, err)

	tests := []struct {
		name           string
		token          string
		sessionErr     error
		wantStatus     int
		wantSessionHit bool
		wantError      string
	}{
		{
			name:           "agent token skips session table",
			token:          agentToken,
			sessionErr:     session.ErrSessionNotFound,
			wantStatus:     http.StatusOK,
			wantSessionHit: false,
		},
		{
			name:           "access token still requires a session row",
			token:          accessToken,
			sessionErr:     session.ErrSessionNotFound,
			wantStatus:     http.StatusUnauthorized,
			wantSessionHit: true,
			wantError:      "Session invalid or expired",
		},
		{
			name:       "missing token_type rejected before session lookup",
			token:      missingType,
			sessionErr: nil,
			wantStatus: http.StatusUnauthorized,
			wantError:  "Invalid token",
		},
		{
			name:       "garbage token_type rejected before session lookup",
			token:      garbageType,
			sessionErr: nil,
			wantStatus: http.StatusUnauthorized,
			wantError:  "Invalid token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sess := &stubSessionValidator{err: tt.sessionErr}
			router := gin.New()
			router.GET("/protected", Authentication(cfg, nil, sess), func(c *gin.Context) {
				got, _ := c.Get("user_id")
				c.JSON(http.StatusOK, gin.H{"user_id": got})
			})

			req := httptest.NewRequest(http.MethodGet, "/protected", nil)
			req.Header.Set("Authorization", "Bearer "+tt.token)
			res := httptest.NewRecorder()
			router.ServeHTTP(res, req)

			require.Equal(t, tt.wantStatus, res.Code)
			require.Equal(t, tt.wantSessionHit, sess.called > 0)
			if tt.wantError != "" {
				var body map[string]any
				require.NoError(t, json.Unmarshal(res.Body.Bytes(), &body))
				require.Equal(t, tt.wantError, body["error"])
			}
			if tt.wantStatus == http.StatusOK {
				var body map[string]any
				require.NoError(t, json.Unmarshal(res.Body.Bytes(), &body))
				require.Equal(t, userID.String(), body["user_id"])
			}
		})
	}
}

func signRawClaims(claims auth.Claims, secret string) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}
