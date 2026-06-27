package funding

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/rail-service/rail_service/internal/domain/services/ramp"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/ramphub"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestRampWebhookIntegration exercises the real webhook handler against a live
// Postgres: signature verification, delivery-id idempotency, and the status
// transition on ramphub_orders. Gated behind RUN_RAMPHUB_IT=1.
//
//	RUN_RAMPHUB_IT=1 go test ./internal/api/handlers/funding/ -run TestRampWebhookIntegration -v
func TestRampWebhookIntegration(t *testing.T) {
	if os.Getenv("RUN_RAMPHUB_IT") == "" {
		t.Skip("RUN_RAMPHUB_IT not set; skipping DB-backed webhook integration test")
	}
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://postgres:postgres@localhost:5432/rail_service_dev?sslmode=disable"
	}
	db, err := sqlx.Connect("postgres", dsn)
	require.NoError(t, err, "connect to test db")
	defer db.Close()

	ctx := context.Background()
	applyRampMigrations(t, db)

	// Need a real user (ramphub_orders.user_id is FK to users).
	var userID uuid.UUID
	if err := db.GetContext(ctx, &userID, `SELECT id FROM users LIMIT 1`); err != nil {
		t.Skipf("no users in dev db to attach order to: %v", err)
	}

	const secret = "whsec_integration_test"
	client := ramphub.NewClient(ramphub.Config{APIKey: "rh_test_x", WebhookSecret: secret}, zap.NewNop())
	svc := ramp.NewService(db, client, nil, nil, zap.NewNop())
	h := NewRampHandlers(svc, zap.NewNop())

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/webhooks/ramphub", h.HandleWebhook)

	txID := "RH-IT-" + uuid.NewString()[:8]
	// Seed a pending onramp order with an expected token amount.
	_, err = db.ExecContext(ctx, `
		INSERT INTO ramphub_orders (user_id, ramphub_transaction_id, order_type, status, fiat_amount, token_amount, asset, chain, currency, used_user_wallet)
		VALUES ($1,$2,'onramp','pending',10000,7.05,'USDC','solana','NGN',true)`, userID, txID)
	require.NoError(t, err)
	defer db.ExecContext(ctx, `DELETE FROM ramphub_orders WHERE ramphub_transaction_id = $1`, txID)
	defer db.ExecContext(ctx, `DELETE FROM ramphub_webhook_deliveries WHERE transaction_id = $1`, txID)

	post := func(deliveryID string, event ramphub.WebhookEvent, sign bool) *httptest.ResponseRecorder {
		body, _ := json.Marshal(event)
		req := httptest.NewRequest(http.MethodPost, "/webhooks/ramphub", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-ramphub-delivery", deliveryID)
		req.Header.Set("x-ramphub-event", event.Type)
		if sign {
			mac := hmac.New(sha256.New, []byte(secret))
			mac.Write(body)
			req.Header.Set("x-ramphub-signature", hex.EncodeToString(mac.Sum(nil)))
		} else {
			req.Header.Set("x-ramphub-signature", "deadbeef")
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	updated := ramphub.WebhookEvent{
		ID: "evt_" + uuid.NewString(), Type: "transaction.updated", CreatedAt: time.Now().Format(time.RFC3339),
		Data: ramphub.WebhookData{TransactionID: txID, Status: "Awaiting settlement"},
	}

	t.Run("bad signature rejected", func(t *testing.T) {
		w := post("del-bad", updated, false)
		require.Equal(t, http.StatusUnauthorized, w.Code, w.Body.String())
		require.Equal(t, "pending", currentStatus(t, db, txID), "status must be unchanged after rejected webhook")
	})

	t.Run("valid webhook transitions status", func(t *testing.T) {
		w := post("del-1", updated, true)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		require.Equal(t, "processing", currentStatus(t, db, txID))
		require.True(t, deliveryRecorded(t, db, "del-1"))
	})

	t.Run("duplicate delivery id is a no-op", func(t *testing.T) {
		// Manually flip the local status; a duplicate delivery must NOT re-apply.
		_, err := db.ExecContext(ctx, `UPDATE ramphub_orders SET status='paid' WHERE ramphub_transaction_id=$1`, txID)
		require.NoError(t, err)
		w := post("del-1", updated, true) // same delivery id
		require.Equal(t, http.StatusOK, w.Code)
		require.Equal(t, "paid", currentStatus(t, db, txID), "duplicate delivery must not re-apply status")
	})

	t.Run("completed event claims order for credit", func(t *testing.T) {
		// Reset to a non-terminal state so the completed event is processed.
		_, err := db.ExecContext(ctx, `UPDATE ramphub_orders SET status='processing', deposit_id=NULL WHERE ramphub_transaction_id=$1`, txID)
		require.NoError(t, err)
		done := ramphub.WebhookEvent{
			ID: "evt_" + uuid.NewString(), Type: "transaction.completed", CreatedAt: time.Now().Format(time.RFC3339),
			Data: ramphub.WebhookData{TransactionID: txID, Status: "completed"},
		}
		w := post("del-2", done, true)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		require.Equal(t, "completed", currentStatus(t, db, txID))
		// With no deposit ledger wired, the backstop claims the order (deposit_id set)
		// but performs no credit — verifying the claim path runs without error.
		var depositID *uuid.UUID
		require.NoError(t, db.GetContext(ctx, &depositID, `SELECT deposit_id FROM ramphub_orders WHERE ramphub_transaction_id=$1`, txID))
		require.NotNil(t, depositID, "completed onramp should be claimed for crediting")
	})
}

func applyRampMigrations(t *testing.T, db *sqlx.DB) {
	t.Helper()
	for _, f := range []string{
		"../../../../migrations/241_create_ramphub_orders.up.sql",
		"../../../../migrations/242_ramphub_webhook_deliveries.up.sql",
	} {
		sqlBytes, err := os.ReadFile(f)
		require.NoError(t, err, "read migration %s", f)
		_, err = db.Exec(string(sqlBytes))
		if err != nil {
			// Tables/indexes may already exist from a prior run; tolerate that.
			t.Logf("migration %s exec note: %v", f, err)
		}
	}
}

func currentStatus(t *testing.T, db *sqlx.DB, txID string) string {
	t.Helper()
	var s string
	require.NoError(t, db.Get(&s, `SELECT status FROM ramphub_orders WHERE ramphub_transaction_id=$1`, txID))
	return s
}

func deliveryRecorded(t *testing.T, db *sqlx.DB, deliveryID string) bool {
	t.Helper()
	var n int
	require.NoError(t, db.Get(&n, `SELECT COUNT(*) FROM ramphub_webhook_deliveries WHERE delivery_id=$1`, deliveryID))
	return n == 1
}
