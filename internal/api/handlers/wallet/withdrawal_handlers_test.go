package wallet

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	domainerrors "github.com/rail-service/rail_service/internal/domain/errors"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type stubWithdrawalService struct {
	emergencyCalled         bool
	emergencyIdempotencyKey string
	fundStashCalled         bool
	fundStashIdempotencyKey string
}

func (s *stubWithdrawalService) InitiateCryptoWithdrawal(context.Context, *entities.InitiateCryptoWithdrawalRequest) (*entities.InitiateWithdrawalResponse, error) {
	return nil, nil
}

func (s *stubWithdrawalService) InitiateFiatWithdrawal(context.Context, *entities.InitiateFiatWithdrawalRequest) (*entities.InitiateWithdrawalResponse, error) {
	return nil, nil
}

func (s *stubWithdrawalService) GetWithdrawal(context.Context, uuid.UUID, uuid.UUID) (*entities.Withdrawal, error) {
	return nil, nil
}

func (s *stubWithdrawalService) GetUserWithdrawals(context.Context, uuid.UUID, int, int) ([]*entities.Withdrawal, error) {
	return nil, nil
}

func (s *stubWithdrawalService) CancelWithdrawal(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}

func (s *stubWithdrawalService) GetWithdrawalFee(context.Context, entities.WithdrawalType, decimal.Decimal, entities.WithdrawalCurrency, string, string) (*entities.WithdrawalFee, error) {
	return nil, nil
}

func (s *stubWithdrawalService) EmergencyWithdrawalPreview(context.Context, uuid.UUID, decimal.Decimal) (*entities.EmergencyWithdrawalPreviewResponse, error) {
	return nil, nil
}

func (s *stubWithdrawalService) EmergencyStashToSpending(_ context.Context, _ uuid.UUID, amount decimal.Decimal, idempotencyKey string) (*entities.EmergencyWithdrawalResult, error) {
	s.emergencyCalled = true
	s.emergencyIdempotencyKey = idempotencyKey
	return &entities.EmergencyWithdrawalResult{
		Amount:     amount,
		Fee:        decimal.RequireFromString("0.03"),
		FeePercent: decimal.RequireFromString("0.03"),
		NetAmount:  amount.Sub(decimal.RequireFromString("0.03")),
		TransferID: uuid.New(),
	}, nil
}

func (s *stubWithdrawalService) FundStash(_ context.Context, _ uuid.UUID, amount decimal.Decimal, idempotencyKey string) (*entities.FundStashResult, error) {
	s.fundStashCalled = true
	s.fundStashIdempotencyKey = idempotencyKey
	return &entities.FundStashResult{
		TransferID: uuid.New(),
		Amount:     amount,
		Status:     "completed",
	}, nil
}

func TestEmergencyStashToSpendingGeneratesIdempotencyKeyWhenHeaderMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)

	svc := &stubWithdrawalService{}
	handler := NewWithdrawalHandlers(svc, nil, testLogger())
	router := gin.New()
	userID := uuid.New()
	router.POST("/emergency", func(c *gin.Context) {
		c.Set("user_id", userID)
		handler.EmergencyStashToSpending(c)
	})

	req := httptest.NewRequest(http.MethodPost, "/emergency", bytes.NewBufferString(`{"amount":"1.00"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	require.Equal(t, http.StatusOK, res.Code)
	require.True(t, svc.emergencyCalled)
	require.NotEmpty(t, svc.emergencyIdempotencyKey)
	require.NoError(t, uuid.Validate(svc.emergencyIdempotencyKey))
}

func TestEmergencyStashToSpendingRejectsOversizedIdempotencyKey(t *testing.T) {
	gin.SetMode(gin.TestMode)

	svc := &stubWithdrawalService{}
	handler := NewWithdrawalHandlers(svc, nil, testLogger())
	router := gin.New()
	router.POST("/emergency", func(c *gin.Context) {
		c.Set("user_id", uuid.New())
		handler.EmergencyStashToSpending(c)
	})

	req := httptest.NewRequest(http.MethodPost, "/emergency", bytes.NewBufferString(`{"amount":"1.00"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", strings.Repeat("x", 256))
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	require.Equal(t, http.StatusBadRequest, res.Code)
	require.False(t, svc.emergencyCalled)
}

func TestFundStashRejectsOversizedIdempotencyKey(t *testing.T) {
	gin.SetMode(gin.TestMode)

	svc := &stubWithdrawalService{}
	handler := NewWithdrawalHandlers(svc, nil, testLogger())
	router := gin.New()
	router.POST("/stash", func(c *gin.Context) {
		c.Set("user_id", uuid.New())
		handler.FundStash(c)
	})

	req := httptest.NewRequest(http.MethodPost, "/stash", bytes.NewBufferString(`{"amount":"1.00"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", strings.Repeat("x", 256))
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	require.Equal(t, http.StatusBadRequest, res.Code)
	require.False(t, svc.fundStashCalled)
}

func TestHandleWithdrawalErrorMapsComplianceReviewToConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := NewWithdrawalHandlers(&stubWithdrawalService{}, nil, testLogger())
	res := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(res)

	err := domainerrors.NewDomainError(
		domainerrors.ErrConflict,
		"COMPLIANCE_REVIEW",
		"Withdrawal is pending compliance review.",
	).WithDetails(map[string]interface{}{
		"status": "IN_REVIEW",
	})

	handler.handleWithdrawalError(c, err, uuid.New(), "1.00")

	require.Equal(t, http.StatusConflict, res.Code)
	require.JSONEq(t, `{"code":"COMPLIANCE_REVIEW","message":"Withdrawal is pending compliance review.","details":{"status":"IN_REVIEW"}}`, res.Body.String())
}

func TestHandleWithdrawalErrorMapsComplianceUnavailableToServiceUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := NewWithdrawalHandlers(&stubWithdrawalService{}, nil, testLogger())
	res := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(res)

	err := domainerrors.NewDomainError(
		domainerrors.ErrServiceUnavailable,
		"COMPLIANCE_UNAVAILABLE",
		"Compliance screening is temporarily unavailable. Please try again.",
	).WithRetryable(true)

	handler.handleWithdrawalError(c, err, uuid.New(), "1.00")

	require.Equal(t, http.StatusServiceUnavailable, res.Code)
	require.JSONEq(t, `{"code":"COMPLIANCE_UNAVAILABLE","message":"Compliance screening is temporarily unavailable. Please try again."}`, res.Body.String())
}

func testLogger() *logger.Logger {
	return &logger.Logger{SugaredLogger: zap.NewNop().Sugar()}
}
