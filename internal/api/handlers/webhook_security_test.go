package handlers

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestWebhookSignatureVerification_FailsClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	zapLogger, _ := zap.NewDevelopment()
	testLogger := logger.NewLogger(zapLogger)

	tests := []struct {
		name               string
		webhookSecret      string
		skipSignatureVerify bool
		expectedStatus     int
		expectedError      string
	}{
		{
			name:               "No secret, skip disabled - should reject",
			webhookSecret:      "",
			skipSignatureVerify: false,
			expectedStatus:     http.StatusUnauthorized,
			expectedError:      "WEBHOOK_NOT_CONFIGURED",
		},
		{
			name:               "No secret, skip enabled (dev mode) - should allow",
			webhookSecret:      "",
			skipSignatureVerify: true,
			expectedStatus:     http.StatusBadRequest, // Will fail on validation, not auth
			expectedError:      "",
		},
		{
			name:               "With secret, invalid signature - should reject",
			webhookSecret:      "test-secret",
			skipSignatureVerify: false,
			expectedStatus:     http.StatusUnauthorized,
			expectedError:      "INVALID_SIGNATURE",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &WalletFundingHandlers{
				webhookSecret:       tt.webhookSecret,
				skipSignatureVerify: tt.skipSignatureVerify,
				logger:              testLogger,
			}

			router := gin.New()
			router.POST("/webhook", h.ChainDepositWebhook)

			body := []byte(`{"tx_hash": "0x123", "amount": "100"}`)
			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Webhook-Signature", "invalid-signature")

			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.expectedError != "" {
				assert.Contains(t, w.Body.String(), tt.expectedError)
			}
		})
	}

	// Test Bridge webhook handler
	t.Run("Bridge webhook - no secret, skip disabled", func(t *testing.T) {
		h := NewBridgeWebhookHandler(nil, zapLogger, "", false)

		router := gin.New()
		router.POST("/webhook", h.HandleWebhook)

		body := []byte(`{"event_type": "test"}`)
		req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Bridge-Signature", "invalid")

		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("Bridge webhook - no secret, skip enabled (dev mode)", func(t *testing.T) {
		h := NewBridgeWebhookHandler(nil, zapLogger, "", true)

		router := gin.New()
		router.POST("/webhook", h.HandleWebhook)

		body := []byte(`{"event_type": "test"}`)
		req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Bridge-Signature", "invalid")

		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		// Should pass auth - returns 200 for unhandled events
		assert.NotEqual(t, http.StatusUnauthorized, w.Code)
	})
}

func TestWebhookSignatureVerification_ValidSignature(t *testing.T) {
	gin.SetMode(gin.TestMode)
	zapLogger, _ := zap.NewDevelopment()
	testLogger := logger.NewLogger(zapLogger)

	secret := "test-webhook-secret"
	body := []byte(`{"tx_hash": "0x123", "amount": "100", "chain": "ETH", "to_address": "0xabc"}`)

	// Generate valid signature
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	signature := hex.EncodeToString(mac.Sum(nil))

	h := &WalletFundingHandlers{
		webhookSecret:       secret,
		skipSignatureVerify: false,
		logger:              testLogger,
	}

	router := gin.New()
	router.POST("/webhook", h.ChainDepositWebhook)

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Webhook-Signature", signature)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Should pass signature verification (may fail on service call, but not on auth)
	assert.NotEqual(t, http.StatusUnauthorized, w.Code)
}
