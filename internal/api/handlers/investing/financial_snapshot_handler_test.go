package investing

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	ledgersvc "github.com/rail-service/rail_service/internal/domain/services/ledger"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

type stubFlow struct {
	summary *entities.MoneyFlowSummary
}

func (s *stubFlow) GetMoneyFlow(_ context.Context, _ uuid.UUID, _, _ time.Time) (*entities.MoneyFlowSummary, error) {
	return s.summary, nil
}

type stubBalances struct {
	spend, stash decimal.Decimal
}

func (s *stubBalances) GetAccountBalance(_ context.Context, _ uuid.UUID, accountType entities.AccountType) (decimal.Decimal, error) {
	switch accountType {
	case entities.AccountTypeSpendingBalance:
		return s.spend, nil
	case entities.AccountTypeStashBalance:
		return s.stash, nil
	default:
		return decimal.Zero, errors.New("unexpected account type")
	}
}

type stubBudget struct {
	budget *entities.SpendingBudget
}

func (s *stubBudget) GetByUserID(_ context.Context, _ uuid.UUID) (*entities.SpendingBudget, error) {
	return s.budget, nil
}

type stubProfile struct {
	profile *entities.FinancialProfile
}

func (s *stubProfile) GetByUserID(_ context.Context, _ uuid.UUID) (*entities.FinancialProfile, error) {
	return s.profile, nil
}

func newSnapshotHandler() (*FinancialSnapshotHandler, *stubFlow, *stubBalances, *stubBudget, *stubProfile) {
	flow := &stubFlow{summary: &entities.MoneyFlowSummary{
		TotalDeposits:     decimal.NewFromFloat(1000),
		DepositCount:      3,
		TotalWithdrawals:  decimal.NewFromFloat(120),
		WithdrawalCount:   2,
		TotalCardSpend:    decimal.NewFromFloat(80),
		CardSpendCount:    4,
		TotalP2P:          decimal.NewFromFloat(40),
		P2PCount:          1,
		TotalReceipts:     decimal.NewFromFloat(30),
		ReceiptCount:      2,
	}}
	balances := &stubBalances{spend: decimal.NewFromFloat(250), stash: decimal.NewFromFloat(300)}
	budget := &stubBudget{budget: &entities.SpendingBudget{MonthlyLimit: decimal.NewFromFloat(500), Currency: "USD"}}
	profile := &stubProfile{profile: nil}
	return NewFinancialSnapshotHandler(flow, balances, budget, profile, logger.NewLogger(zap.NewNop())), flow, balances, budget, profile
}

func snapshotRouter(t *testing.T, handler *FinancialSnapshotHandler) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", uuid.New())
		c.Next()
	})
	router.GET("/snapshot", handler.GetFinancialSnapshot)
	return router
}

func TestFinancialSnapshotHandler(t *testing.T) {
	handler, _, _, _, _ := newSnapshotHandler()
	router := snapshotRouter(t, handler)

	req := httptest.NewRequest(http.MethodGet, "/snapshot?from=2026-09-01&to=2026-09-13", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	balances := body["balances"].(map[string]interface{})
	if balances["spending_balance"] != "250.00" || balances["stash_balance"] != "300.00" || balances["total_balance"] != "550.00" {
		t.Errorf("unexpected balances: %v", balances)
	}

	flow := body["money_flow"].(map[string]interface{})
	if flow["total_deposits"] != "1000.00" || flow["total_card_spend"] != "80.00" || flow["card_spend_count"] != float64(4) {
		t.Errorf("unexpected money flow: %v", flow)
	}

	budget := body["budget"].(map[string]interface{})
	if budget["set"] != true || budget["monthly_limit"] != "500.00" {
		t.Errorf("unexpected budget: %v", budget)
	}

	profile := body["profile"].(map[string]interface{})
	if profile["has_profile"] != false {
		t.Errorf("unexpected profile: %v", profile)
	}

	months := body["monthly_flow"].([]interface{})
	if len(months) != 1 {
		t.Fatalf("expected 1 monthly bucket (Sep 2026), got %d: %v", len(months), months)
	}
	first := months[0].(map[string]interface{})
	if first["month"] != "2026-09" || first["total_deposits"] != "1000.00" {
		t.Errorf("unexpected first month bucket: %v", first)
	}

	period := body["period"].(map[string]interface{})
	if period["from"] != "2026-09-01" || period["to"] != "2026-09-13" {
		t.Errorf("unexpected period: %v", period)
	}
}

func TestFinancialSnapshotHandlerDegrades(t *testing.T) {
	handler := NewFinancialSnapshotHandler(nil, nil, nil, nil, logger.NewLogger(zap.NewNop()))
	router := snapshotRouter(t, handler)

	req := httptest.NewRequest(http.MethodGet, "/snapshot", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	balances := body["balances"].(map[string]interface{})
	if balances["spending_balance"] != "0.00" || balances["total_balance"] != "0.00" {
		t.Errorf("nil balances provider should degrade to zeros: %v", balances)
	}
	if body["budget"].(map[string]interface{})["set"] != false {
		t.Errorf("nil budget provider should degrade to set=false")
	}
}

func TestFinancialSnapshotHandlerMissingAccountIsZero(t *testing.T) {
	flow := &stubFlow{summary: &entities.MoneyFlowSummary{}}
	handler := NewFinancialSnapshotHandler(
		flow,
		&notFoundBalances{},
		&stubBudget{budget: nil},
		&stubProfile{profile: nil},
		logger.NewLogger(zap.NewNop()),
	)
	router := snapshotRouter(t, handler)

	req := httptest.NewRequest(http.MethodGet, "/snapshot", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
}

type notFoundBalances struct{}

func (s *notFoundBalances) GetAccountBalance(_ context.Context, _ uuid.UUID, _ entities.AccountType) (decimal.Decimal, error) {
	return decimal.Zero, ledgersvc.ErrAccountNotFound
}