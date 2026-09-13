package wallet

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

type stubStashTransferer struct {
	spendToStashErr error
	stashToSpendErr error
	lastUser        uuid.UUID
	lastAmount      decimal.Decimal
	lastKey         string
	spendCalls      int
	stashCalls      int
}

func (s *stubStashTransferer) TransferSpendingToStash(_ context.Context, userID uuid.UUID, amount decimal.Decimal, key string) error {
	s.spendCalls++
	s.lastUser = userID
	s.lastAmount = amount
	s.lastKey = key
	return s.spendToStashErr
}

func (s *stubStashTransferer) TransferStashToSpending(_ context.Context, userID uuid.UUID, amount decimal.Decimal, key string) error {
	s.stashCalls++
	s.lastUser = userID
	s.lastAmount = amount
	s.lastKey = key
	return s.stashToSpendErr
}

func TestStashTransferHandlers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	userID := uuid.New()

	tests := []struct {
		name           string
		path           string
		handler        func(*StashTransferHandlers) gin.HandlerFunc
		body           string
		idempotencyKey string
		ledgerErr      error
		wantStatus     int
		wantCode       string
		wantSpendCalls int
		wantStashCalls int
	}{
		{
			name:           "spending to stash success",
			path:           "/from-spending",
			handler:        func(h *StashTransferHandlers) gin.HandlerFunc { return h.TransferSpendingToStash },
			body:           `{"amount":"12.50"}`,
			idempotencyKey: "k-spend-1",
			wantStatus:     http.StatusOK,
			wantSpendCalls: 1,
		},
		{
			name:           "stash to spending success",
			path:           "/to-spending",
			handler:        func(h *StashTransferHandlers) gin.HandlerFunc { return h.TransferStashToSpending },
			body:           `{"amount":"5.00"}`,
			idempotencyKey: "k-stash-1",
			wantStatus:     http.StatusOK,
			wantStashCalls: 1,
		},
		{
			name:           "insufficient funds",
			path:           "/from-spending",
			handler:        func(h *StashTransferHandlers) gin.HandlerFunc { return h.TransferSpendingToStash },
			body:           `{"amount":"99.00"}`,
			idempotencyKey: "k-insuf",
			ledgerErr:      fmt.Errorf("insufficient balance: current=1.00, adjustment=99.00 USD"),
			wantStatus:     http.StatusBadRequest,
			wantCode:       "INSUFFICIENT_FUNDS",
			wantSpendCalls: 1,
		},
		{
			name:           "stash locked",
			path:           "/to-spending",
			handler:        func(h *StashTransferHandlers) gin.HandlerFunc { return h.TransferStashToSpending },
			body:           `{"amount":"5.00"}`,
			idempotencyKey: "k-lock",
			ledgerErr:      fmt.Errorf("stash funds are locked: no active withdrawal window (funds lock for 90 days, then a 7-day window opens)"),
			wantStatus:     http.StatusBadRequest,
			wantCode:       "STASH_LOCKED",
			wantStashCalls: 1,
		},
		{
			name:       "missing idempotency key",
			path:       "/from-spending",
			handler:    func(h *StashTransferHandlers) gin.HandlerFunc { return h.TransferSpendingToStash },
			body:       `{"amount":"1.00"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "INVALID_REQUEST",
		},
		{
			name:           "invalid amount",
			path:           "/from-spending",
			handler:        func(h *StashTransferHandlers) gin.HandlerFunc { return h.TransferSpendingToStash },
			body:           `{"amount":"-1.00"}`,
			idempotencyKey: "k-neg",
			wantStatus:     http.StatusBadRequest,
			wantCode:       "INVALID_AMOUNT",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := &stubStashTransferer{}
			if strings.Contains(tt.path, "from-spending") {
				stub.spendToStashErr = tt.ledgerErr
			} else {
				stub.stashToSpendErr = tt.ledgerErr
			}
			h := NewStashTransferHandlers(stub, testLogger())
			router := gin.New()
			router.POST(tt.path, func(c *gin.Context) {
				c.Set("user_id", userID)
				tt.handler(h)(c)
			})

			req := httptest.NewRequest(http.MethodPost, tt.path, bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			if tt.idempotencyKey != "" {
				req.Header.Set("Idempotency-Key", tt.idempotencyKey)
			}
			res := httptest.NewRecorder()
			router.ServeHTTP(res, req)

			require.Equal(t, tt.wantStatus, res.Code)
			require.Equal(t, tt.wantSpendCalls, stub.spendCalls)
			require.Equal(t, tt.wantStashCalls, stub.stashCalls)
			if tt.wantStatus == http.StatusOK {
				var body map[string]any
				require.NoError(t, json.Unmarshal(res.Body.Bytes(), &body))
				require.Equal(t, "completed", body["status"])
				require.Equal(t, userID, stub.lastUser)
				require.Equal(t, tt.idempotencyKey, stub.lastKey)
			}
			if tt.wantCode != "" {
				var body map[string]any
				require.NoError(t, json.Unmarshal(res.Body.Bytes(), &body))
				require.Equal(t, tt.wantCode, body["code"])
			}
		})
	}
}
