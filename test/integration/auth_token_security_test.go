//go:build integration
// +build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rail-service/rail_service/internal/api/handlers"
	"github.com/rail-service/rail_service/internal/api/middleware"
	routespkg "github.com/rail-service/rail_service/internal/api/routes"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/rail-service/rail_service/internal/infrastructure/database"
	"github.com/rail-service/rail_service/internal/infrastructure/di"
	"github.com/rail-service/rail_service/pkg/logger"
)

func setupAuthTokenTestRouter(t *testing.T) (*gin.Engine, cache.RedisClient, func()) {
	t.Helper()

	cfg := &config.Config{
		Environment: "test",
		Database: config.DatabaseConfig{
			URL: "postgres://test:test@localhost:5432/stack_test?sslmode=disable",
		},
		Redis: config.RedisConfig{
			Host: "localhost",
			Port: 6379,
			DB:   1,
		},
		JWT: config.JWTConfig{
			Secret:     "test-secret-key",
			AccessTTL:  604800,
			RefreshTTL: 2592000,
		},
		Email: config.EmailConfig{
			Provider:    "",
			Environment: "test",
		},
		SMS: config.SMSConfig{
			Provider:    "",
			Environment: "test",
		},
		Verification: config.VerificationConfig{
			CodeLength:       6,
			CodeTTLMinutes:   10,
			MaxAttempts:      3,
			RateLimitPerHour: 5,
		},
	}

	log := logger.New("debug", "test")
	zapLog := log.Zap()

	redisClient, err := cache.NewRedisClient(&cfg.Redis, zapLog)
	require.NoError(t, err)

	db, err := database.NewConnection(cfg.Database)
	require.NoError(t, err)

	container, err := di.NewContainer(cfg, db, log)
	require.NoError(t, err)

	emailStub := &stubVerificationEmailSender{}
	smsStub := &stubVerificationSMSSender{}
	container.VerificationService = services.NewVerificationService(
		container.RedisClient,
		emailStub,
		smsStub,
		container.ZapLog,
		container.Config,
	)

	cleanup := func() {
		redisClient.Close()
		db.Close()
	}

	router := gin.New()
	authHandlers := handlers.NewAuthHandlers(
		container.DB,
		container.Config,
		container.ZapLog,
		*container.UserRepo,
		container.GetVerificationService(),
		container.GetOnboardingService(),
		container.EmailService,
		container.GetSessionService(),
		container.GetTwoFAService(),
		container.GetPasscodeService(),
		container.RedisClient,
		container.GetAccountDeletionService(),
		"",
		nil,
	)
	securityHandlers := handlers.NewSecurityHandlers(
		container.GetPasscodeService(),
		container.GetOnboardingService(),
		container.UserRepo,
		container.GetSessionService(),
		container.Config,
		container.ZapLog,
	)
	sessionValidator := routespkg.NewSessionValidatorAdapter(container.GetSessionService())

	auth := router.Group("/api/v1/auth")
	{
		auth.POST("/register", authHandlers.Register)
		auth.POST("/verify", authHandlers.Verify)
		auth.POST("/resend-code", authHandlers.ResendCode)
		auth.POST("/refresh", authHandlers.RefreshToken)
		auth.POST("/passcode-login", authHandlers.PasscodeLogin)
	}

	protected := router.Group("/api/v1")
	protected.Use(middleware.Authentication(container.Config, container.Logger, sessionValidator))
	{
		protected.GET("/protected/ping", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"ok": true})
		})
		protected.POST("/security/passcode", securityHandlers.CreatePasscode)
	}

	return router, redisClient, cleanup
}

type authTokens struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
}

func signupAndVerifyForTokens(t *testing.T, router *gin.Engine, redisClient cache.RedisClient, email string) authTokens {
	t.Helper()

	signupReq := entities.SignUpRequest{
		Email: stringPtr(email),
	}
	signupBody, _ := json.Marshal(signupReq)
	req := httptest.NewRequest("POST", "/api/v1/auth/register", bytes.NewBuffer(signupBody))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusAccepted, w.Code)

	requestVerificationCode(t, router, entities.ResendCodeRequest{Email: stringPtr(email)})

	ctx := context.Background()
	key := fmt.Sprintf("verification:email:%s", email)
	var codeData entities.VerificationCodeData
	err := redisClient.Get(ctx, key, &codeData)
	require.NoError(t, err)

	verifyReq := entities.VerifyCodeRequest{
		Email: stringPtr(email),
		Code:  codeData.Code,
	}
	verifyBody, _ := json.Marshal(verifyReq)
	req = httptest.NewRequest("POST", "/api/v1/auth/verify", bytes.NewBuffer(verifyBody))
	req.Header.Set("Content-Type", "application/json")

	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var verifyResp struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
	}
	err = json.Unmarshal(w.Body.Bytes(), &verifyResp)
	require.NoError(t, err)
	require.NotEmpty(t, verifyResp.AccessToken)
	require.NotEmpty(t, verifyResp.RefreshToken)

	return authTokens{
		AccessToken:  verifyResp.AccessToken,
		RefreshToken: verifyResp.RefreshToken,
	}
}

func TestRefreshRejectsAccessToken(t *testing.T) {
	router, redisClient, cleanup := setupAuthTokenTestRouter(t)
	defer cleanup()

	email := generateTestEmail()
	tokens := signupAndVerifyForTokens(t, router, redisClient, email)

	// Attempt refresh with access token (must be rejected).
	body, _ := json.Marshal(entities.RefreshTokenRequest{RefreshToken: tokens.AccessToken})
	req := httptest.NewRequest("POST", "/api/v1/auth/refresh", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "INVALID_TOKEN")
}

func TestRefreshTokenReplayFailsAfterRotation(t *testing.T) {
	router, redisClient, cleanup := setupAuthTokenTestRouter(t)
	defer cleanup()

	email := generateTestEmail()
	tokens := signupAndVerifyForTokens(t, router, redisClient, email)

	refreshReqBody, _ := json.Marshal(entities.RefreshTokenRequest{RefreshToken: tokens.RefreshToken})

	// First refresh should rotate refresh token and succeed.
	req := httptest.NewRequest("POST", "/api/v1/auth/refresh", bytes.NewBuffer(refreshReqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var refreshResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	err := json.Unmarshal(w.Body.Bytes(), &refreshResp)
	require.NoError(t, err)
	require.NotEmpty(t, refreshResp.AccessToken)
	require.NotEmpty(t, refreshResp.RefreshToken)
	assert.NotEqual(t, tokens.RefreshToken, refreshResp.RefreshToken)

	// Reusing the old refresh token must now fail.
	req = httptest.NewRequest("POST", "/api/v1/auth/refresh", bytes.NewBuffer(refreshReqBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "INVALID_TOKEN")
}

func TestPasscodeLoginTokenAccessesProtectedRoute(t *testing.T) {
	router, redisClient, cleanup := setupAuthTokenTestRouter(t)
	defer cleanup()

	email := generateTestEmail()
	tokens := signupAndVerifyForTokens(t, router, redisClient, email)

	// Configure passcode while authenticated.
	passcodeBody, _ := json.Marshal(entities.PasscodeSetupRequest{
		Passcode:        "1234",
		ConfirmPasscode: "1234",
	})
	req := httptest.NewRequest("POST", "/api/v1/security/passcode", bytes.NewBuffer(passcodeBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	// Login with passcode-only endpoint (no bearer token).
	passcodeLoginBody, _ := json.Marshal(map[string]string{
		"email":    email,
		"passcode": "1234",
	})
	req = httptest.NewRequest("POST", "/api/v1/auth/passcode-login", bytes.NewBuffer(passcodeLoginBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var passcodeResp struct {
		AccessToken string `json:"accessToken"`
	}
	err := json.Unmarshal(w.Body.Bytes(), &passcodeResp)
	require.NoError(t, err)
	require.NotEmpty(t, passcodeResp.AccessToken)

	// Newly issued passcode-login token must pass protected auth/session middleware.
	req = httptest.NewRequest("GET", "/api/v1/protected/ping", nil)
	req.Header.Set("Authorization", "Bearer "+passcodeResp.AccessToken)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}
