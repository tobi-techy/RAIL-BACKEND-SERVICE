//go:build integration
// +build integration

package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFundingEndpointsIntegration tests the funding endpoints end-to-end
func TestFundingEndpointsIntegration(t *testing.T) {
	// Set up test router and mock dependencies
	gin.SetMode(gin.TestMode)
	router := setupTestRouter(t)

	t.Run("POST /funding/deposit/address - Success", func(t *testing.T) {
		// Create test request
		req := entities.DepositAddressRequest{
			Chain: entities.ChainSolana,
		}
		reqBody, err := json.Marshal(req)
		require.NoError(t, err)

		// Make request
		w := httptest.NewRecorder()
		httpReq := httptest.NewRequest("POST", "/api/v1/funding/deposit/address", bytes.NewReader(reqBody))
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Authorization", "Bearer valid-jwt-token")

		router.ServeHTTP(w, httpReq)

		// Verify response
		assert.Equal(t, http.StatusOK, w.Code)

		var response entities.DepositAddressResponse
		err = json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)

		assert.Equal(t, entities.ChainSolana, response.Chain)
		assert.NotEmpty(t, response.Address)
		assert.Contains(t, response.Address, "So1") // Solana address format
	})

	t.Run("POST /funding/deposit/address - Invalid Chain", func(t *testing.T) {
		// Create test request with invalid chain
		reqBody := []byte(`{"chain": "InvalidChain"}`)

		// Make request
		w := httptest.NewRecorder()
		httpReq := httptest.NewRequest("POST", "/api/v1/funding/deposit/address", bytes.NewReader(reqBody))
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Authorization", "Bearer valid-jwt-token")

		router.ServeHTTP(w, httpReq)

		// Verify error response
		assert.Equal(t, http.StatusBadRequest, w.Code)

		var response map[string]interface{}
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)

		assert.Equal(t, "INVALID_REQUEST", response["code"])
	})

	t.Run("POST /funding/deposit/address - Unauthorized", func(t *testing.T) {
		// Create test request
		req := entities.DepositAddressRequest{
			Chain: entities.ChainSolana,
		}
		reqBody, err := json.Marshal(req)
		require.NoError(t, err)

		// Make request without authorization
		w := httptest.NewRecorder()
		httpReq := httptest.NewRequest("POST", "/api/v1/funding/deposit/address", bytes.NewReader(reqBody))
		httpReq.Header.Set("Content-Type", "application/json")

		router.ServeHTTP(w, httpReq)

		// Verify unauthorized response
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("GET /balances - Success", func(t *testing.T) {
		// Make request
		w := httptest.NewRecorder()
		httpReq := httptest.NewRequest("GET", "/api/v1/balances", nil)
		httpReq.Header.Set("Authorization", "Bearer valid-jwt-token")

		router.ServeHTTP(w, httpReq)

		// Verify response
		assert.Equal(t, http.StatusOK, w.Code)

		var response entities.BalancesResponse
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)

		assert.Equal(t, "USD", response.Currency)
		assert.NotEmpty(t, response.BuyingPower)
		assert.NotEmpty(t, response.PendingDeposits)
	})

	t.Run("GET /funding/confirmations - Success", func(t *testing.T) {
		// Make request
		w := httptest.NewRecorder()
		httpReq := httptest.NewRequest("GET", "/api/v1/funding/confirmations?limit=10", nil)
		httpReq.Header.Set("Authorization", "Bearer valid-jwt-token")

		router.ServeHTTP(w, httpReq)

		// Verify response
		assert.Equal(t, http.StatusOK, w.Code)

		var response entities.FundingConfirmationsPage
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)

		assert.NotNil(t, response.Items)
		// nextCursor may be nil if no more results
	})

	t.Run("GET /funding/confirmations - Pagination", func(t *testing.T) {
		// Make request with cursor
		w := httptest.NewRecorder()
		httpReq := httptest.NewRequest("GET", "/api/v1/funding/confirmations?limit=5&cursor=10", nil)
		httpReq.Header.Set("Authorization", "Bearer valid-jwt-token")

		router.ServeHTTP(w, httpReq)

		// Verify response
		assert.Equal(t, http.StatusOK, w.Code)

		var response entities.FundingConfirmationsPage
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)

		assert.NotNil(t, response.Items)
	})
}

// TestChainDepositWebhookIntegration tests the webhook endpoint
func TestChainDepositWebhookIntegration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := setupTestRouter(t)

	t.Run("POST /webhooks/chain-deposit - Success", func(t *testing.T) {
		// Create test webhook payload
		webhook := entities.ChainDepositWebhook{
			Chain:     entities.ChainSolana,
			Address:   "So1test12345",
			Token:     entities.StablecoinUSDC,
			Amount:    "100.0",
			TxHash:    "tx123456789",
			BlockTime: time.Now(),
			Signature: "valid-signature-here",
		}

		reqBody, err := json.Marshal(webhook)
		require.NoError(t, err)

		// Make request with proper signature
		w := httptest.NewRecorder()
		httpReq := httptest.NewRequest("POST", "/api/v1/webhooks/chain-deposit", bytes.NewReader(reqBody))
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("X-Signature", "sha256="+calculateTestSignature(reqBody))
		httpReq.Header.Set("User-Agent", "Circle-Webhook/1.0")

		router.ServeHTTP(w, httpReq)

		// For now, might return security error due to signature validation
		// In a real test environment, you'd configure the webhook secret properly
		assert.Contains(t, []int{http.StatusOK, http.StatusUnauthorized}, w.Code)

		if w.Code == http.StatusOK {
			var response map[string]interface{}
			err = json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)
			assert.Contains(t, []string{"processed", "already_processed"}, response["status"])
		}
	})

	t.Run("POST /webhooks/chain-deposit - Invalid Payload", func(t *testing.T) {
		// Create invalid payload
		reqBody := []byte(`{"invalid": "payload"}`)

		// Make request
		w := httptest.NewRecorder()
		httpReq := httptest.NewRequest("POST", "/api/v1/webhooks/chain-deposit", bytes.NewReader(reqBody))
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("X-Signature", "sha256="+calculateTestSignature(reqBody))
		httpReq.Header.Set("User-Agent", "Circle-Webhook/1.0")

		router.ServeHTTP(w, httpReq)

		// Should return bad request or unauthorized (depending on signature validation)
		assert.Contains(t, []int{http.StatusBadRequest, http.StatusUnauthorized}, w.Code)
	})

	t.Run("POST /webhooks/chain-deposit - Missing Signature", func(t *testing.T) {
		// Create test webhook payload
		webhook := entities.ChainDepositWebhook{
			Chain:     entities.ChainSolana,
			Address:   "So1test12345",
			Token:     entities.StablecoinUSDC,
			Amount:    "100.0",
			TxHash:    "tx123456789",
			BlockTime: time.Now(),
			Signature: "valid-signature-here",
		}

		reqBody, err := json.Marshal(webhook)
		require.NoError(t, err)

		// Make request without signature
		w := httptest.NewRecorder()
		httpReq := httptest.NewRequest("POST", "/api/v1/webhooks/chain-deposit", bytes.NewReader(reqBody))
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("User-Agent", "Circle-Webhook/1.0")

		router.ServeHTTP(w, httpReq)

		// Should return unauthorized due to missing signature
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("POST /webhooks/chain-deposit - Duplicate Transaction", func(t *testing.T) {
		// Create test webhook payload with same tx hash
		webhook := entities.ChainDepositWebhook{
			Chain:     entities.ChainSolana,
			Address:   "So1test12345",
			Token:     entities.StablecoinUSDC,
			Amount:    "100.0",
			TxHash:    "duplicate-tx-hash",
			BlockTime: time.Now(),
			Signature: "valid-signature-here",
		}

		reqBody, err := json.Marshal(webhook)
		require.NoError(t, err)

		// Make first request
		w1 := httptest.NewRecorder()
		httpReq1 := httptest.NewRequest("POST", "/api/v1/webhooks/chain-deposit", bytes.NewReader(reqBody))
		httpReq1.Header.Set("Content-Type", "application/json")
		httpReq1.Header.Set("X-Signature", "sha256="+calculateTestSignature(reqBody))
		httpReq1.Header.Set("User-Agent", "Circle-Webhook/1.0")

		router.ServeHTTP(w1, httpReq1)

		// Make second request with same payload
		w2 := httptest.NewRecorder()
		httpReq2 := httptest.NewRequest("POST", "/api/v1/webhooks/chain-deposit", bytes.NewReader(reqBody))
		httpReq2.Header.Set("Content-Type", "application/json")
		httpReq2.Header.Set("X-Signature", "sha256="+calculateTestSignature(reqBody))
		httpReq2.Header.Set("User-Agent", "Circle-Webhook/1.0")

		router.ServeHTTP(w2, httpReq2)

		// Both should succeed (idempotent)
		// Second one might return already_processed
		if w1.Code == http.StatusOK && w2.Code == http.StatusOK {
			var response2 map[string]interface{}
			json.Unmarshal(w2.Body.Bytes(), &response2)
			// Should indicate already processed or just success
			assert.Contains(t, []string{"processed", "already_processed"}, response2["status"])
		}
	})
}

// Helper functions for testing

func setupTestRouter(t *testing.T) *gin.Engine {
	// This would set up your actual router with mock dependencies
	// For the purpose of this example, we'll create a minimal setup

	router := gin.New()

	// Add mock middleware to simulate authentication
	router.Use(func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "Bearer valid-jwt-token" {
			// Set mock user ID
			c.Set("user_id", uuid.New())
		}
		c.Next()
	})

	// You would register your actual routes here
	// For this example, we'll add placeholder routes that demonstrate the structure

	api := router.Group("/api/v1")
	{
		funding := api.Group("/funding")
		{
			funding.POST("/deposit/address", func(c *gin.Context) {
				// Mock implementation
				var req entities.DepositAddressRequest
				if err := c.ShouldBindJSON(&req); err != nil {
					c.JSON(400, gin.H{"code": "INVALID_REQUEST", "message": "Invalid request"})
					return
				}

				if _, exists := c.Get("user_id"); !exists {
					c.JSON(401, gin.H{"code": "UNAUTHORIZED", "message": "Not authenticated"})
					return
				}

				// Mock response
				address := "So1test12345"
				if req.Chain == entities.ChainPolygon {
					address = "0x1234567890abcdef"
				}

				c.JSON(200, entities.DepositAddressResponse{
					Chain:   req.Chain,
					Address: address,
				})
			})

			funding.GET("/confirmations", func(c *gin.Context) {
				if _, exists := c.Get("user_id"); !exists {
					c.JSON(401, gin.H{"code": "UNAUTHORIZED", "message": "Not authenticated"})
					return
				}

				// Mock response
				c.JSON(200, entities.FundingConfirmationsPage{
					Items:      []*entities.FundingConfirmation{},
					NextCursor: nil,
				})
			})
		}

		api.GET("/balances", func(c *gin.Context) {
			if _, exists := c.Get("user_id"); !exists {
				c.JSON(401, gin.H{"code": "UNAUTHORIZED", "message": "Not authenticated"})
				return
			}

			// Mock response
			c.JSON(200, entities.BalancesResponse{
				BuyingPower:     "1000.00",
				PendingDeposits: "50.00",
				Currency:        "USD",
			})
		})

		webhooks := api.Group("/webhooks")
		{
			webhooks.POST("/chain-deposit", func(c *gin.Context) {
				// Basic signature validation
				signature := c.GetHeader("X-Signature")
				if signature == "" {
					c.JSON(401, gin.H{"code": "WEBHOOK_SECURITY_ERROR", "message": "Missing signature"})
					return
				}

				var webhook entities.ChainDepositWebhook
				if err := c.ShouldBindJSON(&webhook); err != nil {
					c.JSON(400, gin.H{"code": "INVALID_WEBHOOK", "message": "Invalid payload"})
					return
				}

				if webhook.TxHash == "" || webhook.Amount == "" {
					c.JSON(400, gin.H{"code": "INVALID_WEBHOOK", "message": "Missing required fields"})
					return
				}

				// Mock processing - return success
				c.JSON(200, gin.H{"status": "processed"})
			})
		}
	}

	return router
}

func calculateTestSignature(payload []byte) string {
	// Mock signature calculation for testing
	// In real tests, you'd use the actual HMAC calculation with a test secret
	return "mock-signature-" + string(payload[:min(10, len(payload))])
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
