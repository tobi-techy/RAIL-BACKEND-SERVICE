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

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/rail-service/rail_service/internal/infrastructure/database"
	"github.com/rail-service/rail_service/internal/infrastructure/di"
	"github.com/rail-service/rail_service/pkg/logger"
)

type stubVerificationEmailSender struct {
	messages []struct {
		Email string
		Code  string
	}
}

func (s *stubVerificationEmailSender) SendVerificationEmail(_ context.Context, email, code string) error {
	s.messages = append(s.messages, struct {
		Email string
		Code  string
	}{Email: email, Code: code})
	return nil
}

type stubVerificationSMSSender struct {
	messages []struct {
		Phone string
		Code  string
	}
}

func (s *stubVerificationSMSSender) SendVerificationSMS(_ context.Context, phone, code string) error {
	s.messages = append(s.messages, struct {
		Phone string
		Code  string
	}{Phone: phone, Code: code})
	return nil
}

// TestSignUpFlow tests the complete signup flow with verification
func TestSignUpFlow(t *testing.T) {
	// Setup test environment
	cfg := &config.Config{
		Environment: "test",
		Database: config.DatabaseConfig{
			URL: "postgres://test:test@localhost:5432/stack_test?sslmode=disable",
		},
		Redis: config.RedisConfig{
			Host: "localhost",
			Port: 6379,
			DB:   1, // Use different DB for tests
		},
		JWT: config.JWTConfig{
			Secret:     "test-secret-key",
			AccessTTL:  604800,  // 7 days
			RefreshTTL: 2592000, // 30 days
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
			RateLimitPerHour: 3,
		},
	}

	// Initialize logger
	log := logger.NewLogger("test")
	zapLog := log.Zap()

	// Initialize Redis client
	redisClient, err := cache.NewRedisClient(cfg.Redis, zapLog)
	require.NoError(t, err)
	defer redisClient.Close()

	// Initialize database (assuming test DB is set up)
	db, err := database.NewConnection(cfg.Database)
	require.NoError(t, err)
	defer db.Close()

	// Initialize DI container
	container, err := di.NewContainer(cfg, db, log)
	require.NoError(t, err)

	// Override verification service with stubbed senders to avoid external dependencies
	emailStub := &stubVerificationEmailSender{}
	smsStub := &stubVerificationSMSSender{}
	container.VerificationService = services.NewVerificationService(
		container.RedisClient,
		emailStub,
		smsStub,
		container.ZapLog,
		container.Config,
	)

	// Setup test router
	router := setupTestRouter(container)

	t.Run("Email Signup Flow", func(t *testing.T) {
		testEmailSignupFlow(t, router, redisClient)
	})

	t.Run("Phone Signup Flow", func(t *testing.T) {
		testPhoneSignupFlow(t, router, redisClient)
	})

	t.Run("Rate Limiting", func(t *testing.T) {
		testRateLimiting(t, router, redisClient)
	})

	t.Run("Invalid Verification Code", func(t *testing.T) {
		testInvalidVerificationCode(t, router, redisClient)
	})
}

func testEmailSignupFlow(t *testing.T, router *gin.Engine, redisClient cache.RedisClient) {
	// Step 1: Sign up with email
	signupReq := entities.SignUpRequest{
		Email:    stringPtr("test@example.com"),
		Password: "password123",
	}

	signupBody, _ := json.Marshal(signupReq)
	req := httptest.NewRequest("POST", "/api/v1/auth/signup", bytes.NewBuffer(signupBody))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var signupResp entities.SignUpResponse
	err := json.Unmarshal(w.Body.Bytes(), &signupResp)
	require.NoError(t, err)
	assert.Equal(t, "Verification code sent", signupResp.Message)
	assert.Contains(t, signupResp.Identifier, "***")

	// Step 2: Verify code (get the actual code from Redis)
	ctx := context.Background()
	key := fmt.Sprintf("verify:email:%s", "test@example.com")

	var codeData entities.VerificationCodeData
	err = redisClient.GetJSON(ctx, key, &codeData)
	require.NoError(t, err)

	verifyReq := entities.VerifyCodeRequest{
		Email: stringPtr("test@example.com"),
		Code:  codeData.Code,
	}

	verifyBody, _ := json.Marshal(verifyReq)
	req = httptest.NewRequest("POST", "/api/v1/auth/verify-code", bytes.NewBuffer(verifyBody))
	req.Header.Set("Content-Type", "application/json")

	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var verifyResp entities.VerifyCodeResponse
	err = json.Unmarshal(w.Body.Bytes(), &verifyResp)
	require.NoError(t, err)
	assert.NotEmpty(t, verifyResp.AccessToken)
	assert.NotEmpty(t, verifyResp.RefreshToken)
	assert.True(t, verifyResp.User.EmailVerified)
}

func testPhoneSignupFlow(t *testing.T, router *gin.Engine, redisClient cache.RedisClient) {
	// Step 1: Sign up with phone
	signupReq := entities.SignUpRequest{
		Phone:    stringPtr("+1234567890"),
		Password: "password123",
	}

	signupBody, _ := json.Marshal(signupReq)
	req := httptest.NewRequest("POST", "/api/v1/auth/signup", bytes.NewBuffer(signupBody))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var signupResp entities.SignUpResponse
	err := json.Unmarshal(w.Body.Bytes(), &signupResp)
	require.NoError(t, err)
	assert.Equal(t, "Verification code sent", signupResp.Message)

	// Step 2: Verify code
	ctx := context.Background()
	key := fmt.Sprintf("verify:phone:%s", "+1234567890")

	var codeData entities.VerificationCodeData
	err = redisClient.GetJSON(ctx, key, &codeData)
	require.NoError(t, err)

	verifyReq := entities.VerifyCodeRequest{
		Phone: stringPtr("+1234567890"),
		Code:  codeData.Code,
	}

	verifyBody, _ := json.Marshal(verifyReq)
	req = httptest.NewRequest("POST", "/api/v1/auth/verify-code", bytes.NewBuffer(verifyBody))
	req.Header.Set("Content-Type", "application/json")

	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var verifyResp entities.VerifyCodeResponse
	err = json.Unmarshal(w.Body.Bytes(), &verifyResp)
	require.NoError(t, err)
	assert.NotEmpty(t, verifyResp.AccessToken)
	assert.True(t, verifyResp.User.PhoneVerified)
}

func testRateLimiting(t *testing.T, router *gin.Engine, redisClient cache.RedisClient) {
	email := "ratelimit@example.com"

	// Send multiple signup requests to trigger rate limiting
	for i := 0; i < 4; i++ {
		signupReq := entities.SignUpRequest{
			Email:    stringPtr(email),
			Password: "password123",
		}

		signupBody, _ := json.Marshal(signupReq)
		req := httptest.NewRequest("POST", "/api/v1/auth/signup", bytes.NewBuffer(signupBody))
		req.Header.Set("Content-Type", "application/json")

		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if i < 3 {
			assert.Equal(t, http.StatusOK, w.Code)
		} else {
			// Fourth request should be rate limited
			assert.Equal(t, http.StatusTooManyRequests, w.Code)
		}
	}
}

func testInvalidVerificationCode(t *testing.T, router *gin.Engine, redisClient cache.RedisClient) {
	// First signup
	signupReq := entities.SignUpRequest{
		Email:    stringPtr("invalid@example.com"),
		Password: "password123",
	}

	signupBody, _ := json.Marshal(signupReq)
	req := httptest.NewRequest("POST", "/api/v1/auth/signup", bytes.NewBuffer(signupBody))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// Try to verify with invalid code
	verifyReq := entities.VerifyCodeRequest{
		Email: stringPtr("invalid@example.com"),
		Code:  "000000", // Invalid code
	}

	verifyBody, _ := json.Marshal(verifyReq)
	req = httptest.NewRequest("POST", "/api/v1/auth/verify-code", bytes.NewBuffer(verifyBody))
	req.Header.Set("Content-Type", "application/json")

	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)

	var errorResp entities.ErrorResponse
	err := json.Unmarshal(w.Body.Bytes(), &errorResp)
	require.NoError(t, err)
	assert.Equal(t, "INVALID_CODE", errorResp.Code)
}

// Helper functions
func stringPtr(s string) *string {
	return &s
}

func setupTestRouter(container *di.Container) *gin.Engine {
	// This would be similar to the main router setup
	// For now, return a basic router with the auth endpoints
	router := gin.New()

	// Add auth routes
	auth := router.Group("/api/v1/auth")
	{
		auth.POST("/signup", handlers.SignUp(
			container.DB,
			container.Config,
			container.Logger,
			container.GetVerificationService(),
			container.GetOnboardingJobService(),
		))
		auth.POST("/verify-code", handlers.VerifyCode(
			container.DB,
			container.Config,
			container.Logger,
			container.GetVerificationService(),
			container.GetOnboardingJobService(),
		))
		auth.POST("/resend-code", handlers.ResendCode(
			container.DB,
			container.Config,
			container.Logger,
			container.GetVerificationService(),
		))
	}

	return router
}
