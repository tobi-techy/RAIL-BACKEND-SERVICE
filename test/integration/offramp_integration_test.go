package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/rail-service/rail_service/internal/adapters/due"
	"github.com/rail-service/rail_service/internal/api/handlers"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/stretchr/testify/assert"
)

func TestWebhookHandler_TransferStatusChanged(t *testing.T) {
	log := logger.New("debug", "test")
	
	// Mock off-ramp service
	mockService := &MockOffRampService{}
	handler := handlers.NewDueWebhookHandler(mockService, log)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/webhooks/due", handler.HandleDepositEvent)

	// Create webhook payload
	payload := due.WebhookEvent{
		Type: "transfer.status_changed",
		Data: map[string]interface{}{
			"id":     "transfer_123",
			"status": "completed",
			"source": map[string]interface{}{
				"amount":   "100.00",
				"currency": "USDC",
			},
			"destination": map[string]interface{}{
				"amount":   "99.50",
				"currency": "USD",
			},
		},
	}

	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/webhooks/due", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestWebhookHandler_VirtualAccountDeposit(t *testing.T) {
	log := logger.New("debug", "test")
	
	mockService := &MockOffRampService{}
	handler := handlers.NewDueWebhookHandler(mockService, log)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/webhooks/due", handler.HandleDepositEvent)

	payload := due.WebhookEvent{
		Type: "virtual_account.deposit",
		Data: map[string]interface{}{
			"virtual_account_id": "va_123",
			"amount":             "100.00",
			"currency":           "USDC",
		},
	}

	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/webhooks/due", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

type MockOffRampService struct{}

func (m *MockOffRampService) InitiateOffRamp(ctx context.Context, virtualAccountID, amount string) error {
	return nil
}

func (m *MockOffRampService) HandleTransferCompleted(ctx context.Context, transferID string) error {
	return nil
}
