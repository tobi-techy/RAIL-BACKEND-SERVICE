package wallet

import (
	"bytes"
	"context"
	"encoding/json"
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
	cryptoCalled            bool
	cryptoReq               *entities.InitiateCryptoWithdrawalRequest
	cryptoErr               error
}

func (s *stubWithdrawalService) InitiateCryptoWithdrawal(_ context.Context, req *entities.InitiateCryptoWithdrawalRequest) (*entities.InitiateWithdrawalResponse, error) {
	s.cryptoCalled = true
	s.cryptoReq = req
	if s.cryptoErr != nil {
		return nil, s.cryptoErr
	}
	return &entities.InitiateWithdrawalResponse{
		WithdrawalID: uuid.New(),
		Status:       entities.WithdrawalStatusProcessing,
		Message:      "Withdrawal submitted and is processing",
	}, nil
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

type stubWalletProvider struct {
	wallet         *entities.ManagedWallet
	err            error
	requestedChain string
}

func (s *stubWalletProvider) GetUserWalletByChain(_ context.Context, _ uuid.UUID, chain string) (*entities.ManagedWallet, error) {
	s.requestedChain = chain
	return s.wallet, s.err
}

func newAdminRecoveryRouter(svc *stubWithdrawalService, wp *stubWalletProvider) *gin.Engine {
	gin.SetMode(gin.TestMode)
	handler := NewAdminWithdrawalHandlers(svc, wp, zap.NewNop())
	router := gin.New()
	router.POST("/recover", func(c *gin.Context) {
		c.Set("user_id", "11111111-1111-1111-1111-111111111111")
		handler.AdminRecoverCryptoWithdrawal(c)
	})
	return router
}

func recoveryBody(overrides map[string]string) string {
	body := map[string]string{
		"user_id":             uuid.New().String(),
		"amount":              "5",
		"destination_address": "3bXuBXLT9BKq7Ps41j8Mh4zNRfhnCqZoXRXDMLQpeN4H",
		"destination_chain":   "SOL",
		"reason":              "pivot fund return",
	}
	for k, v := range overrides {
		body[k] = v
	}
	out, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return string(out)
}

func TestAdminRecoverCryptoWithdrawalRequiresReason(t *testing.T) {
	svc := &stubWithdrawalService{}
	router := newAdminRecoveryRouter(svc, &stubWalletProvider{})

	req := httptest.NewRequest(http.MethodPost, "/recover", bytes.NewBufferString(recoveryBody(map[string]string{"reason": ""})))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	require.Equal(t, http.StatusBadRequest, res.Code)
	require.False(t, svc.cryptoCalled)
}

func TestAdminRecoverCryptoWithdrawalEnforcesCap(t *testing.T) {
	svc := &stubWithdrawalService{}
	router := newAdminRecoveryRouter(svc, &stubWalletProvider{})

	req := httptest.NewRequest(http.MethodPost, "/recover", bytes.NewBufferString(recoveryBody(map[string]string{"amount": "25000"})))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	require.Equal(t, http.StatusBadRequest, res.Code)
	require.False(t, svc.cryptoCalled)
}

func TestAdminRecoverCryptoWithdrawalRejectsBadDestination(t *testing.T) {
	svc := &stubWithdrawalService{}
	router := newAdminRecoveryRouter(svc, &stubWalletProvider{})

	req := httptest.NewRequest(http.MethodPost, "/recover", bytes.NewBufferString(recoveryBody(map[string]string{"destination_address": "0xnot-a-solana-address"})))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	require.Equal(t, http.StatusBadRequest, res.Code)
	require.False(t, svc.cryptoCalled)
}

func TestAdminRecoverCryptoWithdrawalNoWallet(t *testing.T) {
	svc := &stubWithdrawalService{}
	router := newAdminRecoveryRouter(svc, &stubWalletProvider{err: context.Canceled})

	req := httptest.NewRequest(http.MethodPost, "/recover", bytes.NewBufferString(recoveryBody(nil)))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	require.Equal(t, http.StatusBadRequest, res.Code)
	require.Contains(t, res.Body.String(), "NO_WALLET")
	require.False(t, svc.cryptoCalled)
}

func TestAdminRecoverCryptoWithdrawalHappyPath(t *testing.T) {
	targetUserID := uuid.New()
	svc := &stubWithdrawalService{}
	wp := &stubWalletProvider{wallet: &entities.ManagedWallet{
		UserID:         targetUserID,
		Chain:          entities.WalletChainBase,
		Address:        "0x52ec428e3d59fc311ba502f715a1c8cb2bc084f6",
		CircleWalletID: "circle-wallet-1",
	}}
	router := newAdminRecoveryRouter(svc, wp)

	req := httptest.NewRequest(http.MethodPost, "/recover", bytes.NewBufferString(recoveryBody(map[string]string{"user_id": targetUserID.String()})))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", uuid.New().String())
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	require.Equal(t, http.StatusAccepted, res.Code)
	require.True(t, svc.cryptoCalled)
	require.Equal(t, targetUserID, svc.cryptoReq.UserID)
	require.Equal(t, "3bXuBXLT9BKq7Ps41j8Mh4zNRfhnCqZoXRXDMLQpeN4H", svc.cryptoReq.DestinationAddress)
	require.Equal(t, "SOL", svc.cryptoReq.DestinationChain)
	require.Equal(t, string(entities.WalletChainBase), svc.cryptoReq.SourceChain)
	require.Equal(t, "circle-wallet-1", svc.cryptoReq.CircleWalletID)
	require.Equal(t, "admin_recovery", svc.cryptoReq.Category)
	require.Empty(t, svc.cryptoReq.BridgeWalletID)
	require.Equal(t, "5", svc.cryptoReq.Amount.String())
	require.Contains(t, res.Body.String(), "admin_id")
}

func newInternalRecoveryRouter(svc *stubWithdrawalService, wp *stubWalletProvider) *gin.Engine {
	gin.SetMode(gin.TestMode)
	handler := NewAdminWithdrawalHandlers(svc, wp, zap.NewNop())
	router := gin.New()
	router.POST("/recover", handler.InternalRecoverCryptoWithdrawal)
	return router
}

func TestInternalRecoverCryptoWithdrawalRequiresActor(t *testing.T) {
	svc := &stubWithdrawalService{}
	router := newInternalRecoveryRouter(svc, &stubWalletProvider{})

	req := httptest.NewRequest(http.MethodPost, "/recover", bytes.NewBufferString(recoveryBody(nil)))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	require.Equal(t, http.StatusBadRequest, res.Code)
	require.Contains(t, res.Body.String(), "X-Internal-Actor")
	require.False(t, svc.cryptoCalled)
}

func TestInternalRecoverCryptoWithdrawalRejectsOversizedActor(t *testing.T) {
	svc := &stubWithdrawalService{}
	router := newInternalRecoveryRouter(svc, &stubWalletProvider{})

	req := httptest.NewRequest(http.MethodPost, "/recover", bytes.NewBufferString(recoveryBody(nil)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Actor", strings.Repeat("a", maxAdminRecoveryActorLength+1))
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	require.Equal(t, http.StatusBadRequest, res.Code)
	require.False(t, svc.cryptoCalled)
}

func TestInternalRecoverCryptoWithdrawalHappyPathUsesInternalActor(t *testing.T) {
	targetUserID := uuid.New()
	svc := &stubWithdrawalService{}
	wp := &stubWalletProvider{wallet: &entities.ManagedWallet{
		UserID:         targetUserID,
		Chain:          entities.WalletChainBase,
		Address:        "0x52ec428e3d59fc311ba502f715a1c8cb2bc084f6",
		CircleWalletID: "circle-wallet-1",
	}}
	router := newInternalRecoveryRouter(svc, wp)

	req := httptest.NewRequest(http.MethodPost, "/recover", bytes.NewBufferString(recoveryBody(map[string]string{"user_id": targetUserID.String()})))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Actor", "ops-runner")
	req.Header.Set("Idempotency-Key", uuid.New().String())
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	require.Equal(t, http.StatusAccepted, res.Code)
	require.True(t, svc.cryptoCalled)
	require.Equal(t, targetUserID, svc.cryptoReq.UserID)
	require.Equal(t, "admin_recovery", svc.cryptoReq.Category)

	var body map[string]any
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &body))
	require.Equal(t, "ops-runner", body["actor"])
	require.Equal(t, "internal_key", body["actor_source"])
	require.NotContains(t, body, "admin_id")
}

// Base -> Solana is the case this parameter exists for: the funds leave from
// the user's Base wallet even though the destination address is on Solana.
func TestInternalRecoverCryptoWithdrawalSourceChainSelectsSourceWallet(t *testing.T) {
	targetUserID := uuid.New()
	svc := &stubWithdrawalService{}
	wp := &stubWalletProvider{wallet: &entities.ManagedWallet{
		UserID:         targetUserID,
		Chain:          entities.WalletChainBase,
		Address:        "0x52ec428e3d59fc311ba502f715a1c8cb2bc084f6",
		CircleWalletID: "circle-wallet-1",
	}}
	router := newInternalRecoveryRouter(svc, wp)

	req := httptest.NewRequest(http.MethodPost, "/recover", bytes.NewBufferString(recoveryBody(map[string]string{
		"user_id":      targetUserID.String(),
		"source_chain": "base",
	})))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Actor", "ops-runner")
	req.Header.Set("Idempotency-Key", uuid.New().String())
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	require.Equal(t, http.StatusAccepted, res.Code)
	require.Equal(t, "BASE", wp.requestedChain)
	require.Equal(t, string(entities.WalletChainBase), svc.cryptoReq.SourceChain)
	require.Equal(t, "SOL", svc.cryptoReq.DestinationChain)
}
