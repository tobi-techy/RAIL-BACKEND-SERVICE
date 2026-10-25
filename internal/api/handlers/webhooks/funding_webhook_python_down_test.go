package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/funding"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// These tests drive a real Bridge deposit webhook through the unified funding
// handler, the real BridgeVirtualAccountService and the real Python client,
// against a Python brain whose /api/v1/money/inflow is gone (MIRIAM PR #51),
// erroring, or hanging. The webhook must answer 2xx every time: a 5xx makes
// Bridge redeliver the deposit forever.

// --- fakes for the funding service ---------------------------------------

type pdVARepo struct{ va *entities.VirtualAccount }

func (r *pdVARepo) Create(context.Context, *entities.VirtualAccount) error { return nil }
func (r *pdVARepo) Update(context.Context, *entities.VirtualAccount) error { return nil }
func (r *pdVARepo) UpdateWithVersion(context.Context, *entities.VirtualAccount, time.Time) error {
	return nil
}
func (r *pdVARepo) GetByID(context.Context, uuid.UUID) (*entities.VirtualAccount, error) {
	return r.va, nil
}
func (r *pdVARepo) GetByUserID(context.Context, uuid.UUID) ([]*entities.VirtualAccount, error) {
	return []*entities.VirtualAccount{r.va}, nil
}
func (r *pdVARepo) GetActiveByUserIDAndCurrency(context.Context, uuid.UUID, string) (*entities.VirtualAccount, error) {
	return r.va, nil
}
func (r *pdVARepo) UpdateStatus(context.Context, uuid.UUID, entities.VirtualAccountStatus) error {
	return nil
}
func (r *pdVARepo) GetByBridgeAccountID(_ context.Context, id string) (*entities.VirtualAccount, error) {
	if id == "va_bridge_1" {
		return r.va, nil
	}
	return nil, nil
}
func (r *pdVARepo) GetAccountsForMigration(context.Context, int) ([]*entities.VirtualAccount, error) {
	return nil, nil
}
func (r *pdVARepo) UpdateBridgeAccountID(context.Context, uuid.UUID, string) error { return nil }

type pdDepositRepo struct {
	mu    sync.Mutex
	byKey map[string]*entities.Deposit
}

func (r *pdDepositRepo) Create(_ context.Context, d *entities.Deposit) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byKey[d.IdempotencyKey]; ok {
		return errors.New("duplicate key value violates unique constraint")
	}
	r.byKey[d.IdempotencyKey] = d
	return nil
}
func (r *pdDepositRepo) GetByID(context.Context, uuid.UUID) (*entities.Deposit, error) {
	return nil, nil
}
func (r *pdDepositRepo) GetByUserID(context.Context, uuid.UUID, int, int) ([]*entities.Deposit, error) {
	return nil, nil
}
func (r *pdDepositRepo) UpdateStatus(_ context.Context, id uuid.UUID, status string, _ *time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, d := range r.byKey {
		if d.ID == id {
			d.Status = status
		}
	}
	return nil
}
func (r *pdDepositRepo) GetByTxHash(context.Context, string) (*entities.Deposit, error) {
	return nil, nil
}
func (r *pdDepositRepo) GetByIdempotencyKey(_ context.Context, key string) (*entities.Deposit, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.byKey[key], nil
}
func (r *pdDepositRepo) GetRecentByUserIDAndAmount(context.Context, uuid.UUID, decimal.Decimal, time.Duration) (*entities.Deposit, error) {
	return nil, nil
}
func (r *pdDepositRepo) DeletePendingDeposit(context.Context, uuid.UUID) error { return nil }
func (r *pdDepositRepo) UpdateDepositAmount(context.Context, uuid.UUID, decimal.Decimal) error {
	return nil
}
func (r *pdDepositRepo) GetLatestPendingNGNByUserID(context.Context, uuid.UUID) (*entities.Deposit, error) {
	return nil, nil
}
func (r *pdDepositRepo) UpdateConversionID(context.Context, uuid.UUID, string) error { return nil }

type pdLedger struct{ calls atomic.Int32 }

func (l *pdLedger) RecordDeposit(context.Context, uuid.UUID, decimal.Decimal, uuid.UUID, string, string) error {
	return nil
}
func (l *pdLedger) RecordDepositWithAllocation(context.Context, uuid.UUID, decimal.Decimal, uuid.UUID, string, string, decimal.Decimal, decimal.Decimal) error {
	l.calls.Add(1)
	return nil
}
func (l *pdLedger) CompensateDeposit(context.Context, uuid.UUID, decimal.Decimal, uuid.UUID) error {
	return nil
}
func (l *pdLedger) GetUserBalance(context.Context, uuid.UUID) (*funding.LedgerBalanceView, error) {
	return nil, nil
}

type pdAlloc struct{}

func (pdAlloc) ProcessIncomingFunds(context.Context, *entities.IncomingFundsRequest) error {
	return nil
}

// pdProcessor adapts the real funding service to the webhook processor, the
// same way di.BridgeVirtualAccountWebhookAdapter does in production.
type pdProcessor struct {
	svc      *funding.BridgeVirtualAccountService
	override error
}

func (p *pdProcessor) ProcessFiatDeposit(c *gin.Context, e *BridgeDepositEvent) error {
	if p.override != nil {
		return p.override
	}
	return p.svc.ProcessFiatDeposit(c.Request.Context(), &funding.BridgeFiatDepositEvent{
		VirtualAccountID: e.VirtualAccountID, CustomerID: e.CustomerID, Amount: e.Amount,
		Currency: e.Currency, TransactionRef: e.TransactionRef, Status: e.Status,
	})
}
func (p *pdProcessor) ProcessCryptoDeposit(context.Context, uuid.UUID, string, decimal.Decimal, string) error {
	return nil
}

// --- the fake Python brain ------------------------------------------------

func pythonInflowServer(t *testing.T, status int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if status == 0 { // hang until the client gives up
			select {
			case <-r.Context().Done():
			case <-time.After(1500 * time.Millisecond):
			}
			return
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

const pdSecret = "bridge-test-secret"

func newUnifiedForTest(t *testing.T, pythonURL string, override error) (*UnifiedFundingWebhookHandler, *pdLedger) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	va := &entities.VirtualAccount{ID: uuid.New(), UserID: uuid.New(), Currency: "NGN", Status: entities.VirtualAccountStatusActive}
	ledger := &pdLedger{}
	svc := funding.NewBridgeVirtualAccountService(nil, &pdVARepo{va: va}, &pdDepositRepo{byKey: map[string]*entities.Deposit{}}, pdAlloc{}, ledger, logger.NewLogger(zap.NewNop()))
	if pythonURL != "" {
		// Wired exactly as production wires it when the inflow flag is on, with
		// a generous client timeout so only the service's budget bounds a hang.
		svc.SetInflowNotifier(ai.NewPythonAgentClient(ai.PythonAgentClientConfig{
			BaseURL: pythonURL, JWTSecret: "s", JWTTTL: time.Minute, Timeout: 60 * time.Second,
		}, zap.NewNop()))
	}
	proc := &pdProcessor{svc: svc, override: override}
	bridgeSvc := NewBridgeWebhookService(proc, nil, nil, nil, nil, nil, zap.NewNop(), nil)
	bh := NewBridgeWebhookHandler(bridgeSvc, nil, zap.NewNop(), pdSecret, false, "test")
	return NewUnifiedFundingWebhookHandler(bh, nil, nil, zap.NewNop()), ledger
}

func postBridgeDeposit(t *testing.T, h *UnifiedFundingWebhookHandler, ref string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]interface{}{
		"event_id":       "evt_" + ref,
		"event_category": "virtual_account",
		"event_type":     "deposit.completed",
		"event_object": map[string]interface{}{
			"virtual_account_id": "va_bridge_1",
			"amount":             "25000",
			"currency":           "NGN",
			"transaction_ref":    ref,
			"status":             "completed",
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	mac := hmac.New(sha256.New, []byte(pdSecret))
	mac.Write(body)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/funding", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Webhook-Source", "bridge")
	req.Header.Set("X-Webhook-Signature", hex.EncodeToString(mac.Sum(nil)))
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	h.HandleFundingWebhook(c)
	return w
}

func TestBridgeDepositWebhookIs2xxWhenPythonInflowFails(t *testing.T) {
	cases := map[string]int{"404 (endpoint removed)": http.StatusNotFound, "500": http.StatusInternalServerError, "503": http.StatusServiceUnavailable, "timeout": 0}
	for name, status := range cases {
		t.Run(name, func(t *testing.T) {
			srv, calls := pythonInflowServer(t, status)
			h, ledger := newUnifiedForTest(t, srv.URL, nil)
			ref := fmt.Sprintf("dep_%d", status)

			start := time.Now()
			w := postBridgeDeposit(t, h, ref)
			if w.Code < 200 || w.Code > 299 {
				t.Fatalf("webhook must be 2xx when Python fails, got %d: %s", w.Code, w.Body.String())
			}
			if elapsed := time.Since(start); elapsed > 7*time.Second {
				t.Fatalf("a hanging Python must not hold the webhook past the notify budget, took %v", elapsed)
			}
			if calls.Load() == 0 {
				t.Fatal("the best-effort report should still be attempted")
			}
			if ledger.calls.Load() != 1 {
				t.Fatalf("the credit must be booked once, got %d", ledger.calls.Load())
			}

			// Bridge redelivers the same event: still 2xx, still booked once.
			w = postBridgeDeposit(t, h, ref)
			if w.Code < 200 || w.Code > 299 {
				t.Fatalf("redelivery must be 2xx, got %d: %s", w.Code, w.Body.String())
			}
			if ledger.calls.Load() != 1 {
				t.Fatalf("a redelivery must not book again, got %d", ledger.calls.Load())
			}
		})
	}
}

func TestBridgeDepositWebhookWithInflowNotifierUnwired(t *testing.T) {
	// The default after this change: PYTHON_AGENT_NOTIFY_INFLOW_ENABLED=false
	// leaves the notifier unwired, so Python is never called.
	h, ledger := newUnifiedForTest(t, "", nil)
	if w := postBridgeDeposit(t, h, "dep_unwired"); w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if ledger.calls.Load() != 1 {
		t.Fatalf("credit not booked")
	}
}

func TestBridgeDepositWebhookNeverAnswers503ForAnInflowError(t *testing.T) {
	// Defence in depth: even if a service returns the old sentinel, the
	// webhook acks instead of asking Bridge to redeliver.
	h, _ := newUnifiedForTest(t, "", fmt.Errorf("report: %w", ai.ErrInflowNotRecorded))
	if w := postBridgeDeposit(t, h, "dep_sentinel"); w.Code != http.StatusOK {
		t.Fatalf("want 200 for ErrInflowNotRecorded, got %d", w.Code)
	}
}
