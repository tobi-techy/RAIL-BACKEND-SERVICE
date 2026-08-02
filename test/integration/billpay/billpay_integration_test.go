//go:build integration
// +build integration

// BillPay integration tests require a running PostgreSQL database at the
// DSN below. Run migrations first: make migrate-up
//
// Usage:
//
//	go test -tags=integration -run TestBillPay ./test/integration/billpay/ -v -count=1

package billpay_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/services/billpay"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/airbills"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "github.com/lib/pq"
)

const billpayTestDSN = "postgres://test:test@localhost:5432/stack_test?sslmode=disable"

// --- helpers ---------------------------------------------------------------

type mockNotifier struct {
	pushes []mockPush
}

type mockPush struct {
	userID  uuid.UUID
	title   string
	message string
	data    map[string]interface{}
}

func (m *mockNotifier) SendPush(ctx context.Context, userID uuid.UUID, title, message string, data map[string]interface{}) error {
	m.pushes = append(m.pushes, mockPush{userID: userID, title: title, message: message, data: data})
	return nil
}

func newBillPayService(t *testing.T) (*sqlx.DB, *billpay.Service) {
	t.Helper()
	db, err := sqlx.Connect("postgres", billpayTestDSN)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	log := logger.New("debug", "test")
	zapLog := log.Zap()
	client, err := airbills.NewClient(airbills.Config{
		SecretKey:     "test-secret",
		WebhookSecret: "test-webhook-secret",
	}, zapLog)
	require.NoError(t, err)

	svc := billpay.NewService(db, client, billpay.Config{
		DeveloperFeePercent: 1.0,
		DefaultToken:        airbills.TokenUSDC,
		MaxAmountNGN:        100000,
	}, zapLog)
	return db, svc
}

func signWebhook(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func uniqueUserID() uuid.UUID {
	return uuid.Must(uuid.NewRandom())
}

func insertTestUser(ctx context.Context, t *testing.T, db *sqlx.DB, userID uuid.UUID) {
	t.Helper()
	_, err := db.ExecContext(ctx,
		`INSERT INTO users (id, email, onboarding_status) VALUES ($1, $2, 'started') ON CONFLICT (id) DO NOTHING`,
		userID, fmt.Sprintf("%s@test.rail", userID.String()))
	require.NoError(t, err)
}

func cleanupBillPayData(ctx context.Context, t *testing.T, db *sqlx.DB, userID uuid.UUID) {
	t.Helper()
	db.MustExecContext(ctx, `DELETE FROM airbills_webhook_deliveries WHERE airbills_id IN (SELECT airbills_id FROM airbills_orders WHERE user_id = $1)`, userID)
	db.MustExecContext(ctx, `DELETE FROM airbills_orders WHERE user_id = $1`, userID)
	db.MustExecContext(ctx, `DELETE FROM bill_pay_mandates WHERE user_id = $1`, userID)
	db.MustExecContext(ctx, `DELETE FROM bill_beneficiaries WHERE user_id = $1`, userID)
	db.MustExecContext(ctx, `DELETE FROM users WHERE id = $1`, userID)
}

// --- P0: Beneficiaries -----------------------------------------------------

func TestBillPay_Beneficiary_SaveAndList(t *testing.T) {
	db, svc := newBillPayService(t)
	ctx := context.Background()
	userID := uniqueUserID()
	insertTestUser(ctx, t, db, userID)
	t.Cleanup(func() { cleanupBillPayData(ctx, t, db, userID) })

	saved, err := svc.SaveBeneficiary(ctx, userID, billpay.Beneficiary{
		Label:     "Mum's phone",
		Category:  billpay.CategoryAirtime,
		Recipient: "08012345678",
		NetworkID: airbills.NetworkMTN,
	})
	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, saved.ID)

	list, err := svc.ListBeneficiaries(ctx, userID, "")
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "Mum's phone", list[0].Label)
	assert.Equal(t, billpay.CategoryAirtime, list[0].Category)

	// Upsert by unique key updates label instead of duplicating.
	_, err = svc.SaveBeneficiary(ctx, userID, billpay.Beneficiary{
		Label:     "Mum's airtime",
		Category:  billpay.CategoryAirtime,
		Recipient: "08012345678",
	})
	require.NoError(t, err)
	list, err = svc.ListBeneficiaries(ctx, userID, "")
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "Mum's airtime", list[0].Label)
}

// --- P0: Mandates ----------------------------------------------------------

func TestBillPay_Mandate_SetAndGet(t *testing.T) {
	db, svc := newBillPayService(t)
	ctx := context.Background()
	userID := uniqueUserID()
	insertTestUser(ctx, t, db, userID)
	t.Cleanup(func() { cleanupBillPayData(ctx, t, db, userID) })

	m, err := svc.SetMandate(ctx, userID, billpay.Mandate{
		Category:         billpay.CategoryAirtime,
		PerPaymentCapNGN: 5000,
		DailyCapNGN:      10000,
		AllowAuto:        true,
	})
	require.NoError(t, err)
	assert.Equal(t, billpay.CategoryAirtime, m.Category)
	assert.True(t, m.AllowAuto)

	fetched, err := svc.GetMandate(ctx, userID, billpay.CategoryAirtime)
	require.NoError(t, err)
	require.NotNil(t, fetched)
	assert.Equal(t, float64(5000), fetched.PerPaymentCapNGN)
	assert.Equal(t, float64(10000), fetched.DailyCapNGN)

	// Unknown category is unsupported.
	_, err = svc.GetMandate(ctx, userID, "loans")
	assert.Error(t, err)
}

// --- P0: Mandate daily cap -------------------------------------------------

func TestBillPay_EvaluateMandate_DailyCap_CategorySpecific(t *testing.T) {
	db, svc := newBillPayService(t)
	ctx := context.Background()
	userID := uniqueUserID()
	insertTestUser(ctx, t, db, userID)
	t.Cleanup(func() { cleanupBillPayData(ctx, t, db, userID) })

	_, err := svc.SetMandate(ctx, userID, billpay.Mandate{
		Category:         billpay.CategoryAirtime,
		PerPaymentCapNGN: 5000,
		DailyCapNGN:      6000,
	})
	require.NoError(t, err)

	// No spend yet — allowed.
	dec, err := svc.EvaluateMandate(ctx, userID, billpay.CategoryAirtime, 3000)
	require.NoError(t, err)
	assert.True(t, dec.Allowed)

	// Seed a recent airtime order.
	_, err = db.ExecContext(ctx, `
		INSERT INTO airbills_orders (id, user_id, airbills_id, product_code, product_category, status, recipient, amount_ngn, token, amount_in_token, rail_fee_usdc, hold_amount, rate)
		VALUES ($1, $2, $3, $4, $5, 'completed', $6, $7, 'USDC', 5, 0.05, 5.05, 1500)`,
		uuid.New(), userID, "air-1", airbills.ProductAirtime, billpay.CategoryAirtime, "08012345678", float64(4000))
	require.NoError(t, err)

	// 4000 + 3000 would exceed daily cap of 6000.
	dec, err = svc.EvaluateMandate(ctx, userID, billpay.CategoryAirtime, 3000)
	require.NoError(t, err)
	assert.False(t, dec.Allowed)
	assert.True(t, dec.RequiresAuth)

	// Data is a separate category and should not count against airtime cap.
	dec, err = svc.EvaluateMandate(ctx, userID, billpay.CategoryData, 3000)
	require.NoError(t, err)
	assert.True(t, dec.Allowed)
}

func TestBillPay_EvaluateMandate_DailyCap_AllCategory(t *testing.T) {
	db, svc := newBillPayService(t)
	ctx := context.Background()
	userID := uniqueUserID()
	insertTestUser(ctx, t, db, userID)
	t.Cleanup(func() { cleanupBillPayData(ctx, t, db, userID) })

	_, err := svc.SetMandate(ctx, userID, billpay.Mandate{
		Category:         billpay.MandateAll,
		PerPaymentCapNGN: 5000,
		DailyCapNGN:      6000,
	})
	require.NoError(t, err)

	// Seed a recent data order.
	_, err = db.ExecContext(ctx, `
		INSERT INTO airbills_orders (id, user_id, airbills_id, product_code, product_category, status, recipient, amount_ngn, token, amount_in_token, rail_fee_usdc, hold_amount, rate)
		VALUES ($1, $2, $3, $4, $5, 'completed', $6, $7, 'USDC', 5, 0.05, 5.05, 1500)`,
		uuid.New(), userID, "data-1", airbills.ProductData, billpay.CategoryData, "08012345678", float64(4000))
	require.NoError(t, err)

	// A blanket 'all' mandate counts data spend against the airtime request too.
	dec, err := svc.EvaluateMandate(ctx, userID, billpay.CategoryAirtime, 3000)
	require.NoError(t, err)
	assert.False(t, dec.Allowed)
	assert.True(t, dec.RequiresAuth)
}

// --- P0: Order lookup -------------------------------------------------------

func TestBillPay_GetOrder(t *testing.T) {
	db, svc := newBillPayService(t)
	ctx := context.Background()
	userID := uniqueUserID()
	insertTestUser(ctx, t, db, userID)
	t.Cleanup(func() { cleanupBillPayData(ctx, t, db, userID) })

	orderID := uuid.New()
	airbillsID := "ab-123"
	_, err := db.ExecContext(ctx, `
		INSERT INTO airbills_orders (id, user_id, airbills_id, product_code, product_category, status, recipient, amount_ngn, token, amount_in_token, rail_fee_usdc, hold_amount, rate)
		VALUES ($1, $2, $3, $4, $5, 'held', $6, $7, 'USDC', 10, 0.1, 10.1, 1500)`,
		orderID, userID, airbillsID, airbills.ProductAirtime, billpay.CategoryAirtime, "08012345678", float64(15000))
	require.NoError(t, err)

	order, err := svc.GetOrder(ctx, userID, orderID)
	require.NoError(t, err)
	assert.Equal(t, orderID, order.ID)
	assert.Equal(t, airbillsID, order.AirbillsID)
	assert.Equal(t, "held", order.Status)
	assert.Equal(t, float64(15000), order.AmountNGN)
	assert.InDelta(t, 1500, order.Rate, 0.01)

	// Wrong user should not see the order.
	otherUser := uniqueUserID()
	insertTestUser(ctx, t, db, otherUser)
	t.Cleanup(func() { cleanupBillPayData(ctx, t, db, otherUser) })
	_, err = svc.GetOrder(ctx, otherUser, orderID)
	assert.Error(t, err)
}

// --- P0: Webhook callback notifications ------------------------------------

func TestBillPay_WebhookCallback_NotifiesUser(t *testing.T) {
	db, svc := newBillPayService(t)
	ctx := context.Background()
	userID := uniqueUserID()
	insertTestUser(ctx, t, db, userID)
	t.Cleanup(func() { cleanupBillPayData(ctx, t, db, userID) })

	notifier := &mockNotifier{}
	svc.SetNotifier(notifier)

	orderID := uuid.New()
	airbillsID := "ab-callback-1"
	_, err := db.ExecContext(ctx, `
		INSERT INTO airbills_orders (id, user_id, airbills_id, product_code, product_category, status, recipient, amount_ngn, token, amount_in_token, rail_fee_usdc, hold_amount, rate)
		VALUES ($1, $2, $3, $4, $5, 'sent', $6, $7, 'USDC', 2, 0.02, 2.02, 1500)`,
		orderID, userID, airbillsID, airbills.ProductAirtime, billpay.CategoryAirtime, "08012345678", float64(3000))
	require.NoError(t, err)

	body := []byte(fmt.Sprintf(`{"id":"%s","productCode":"100","status":"success"}`, airbillsID))
	sig := signWebhook(body, "test-webhook-secret")
	err = svc.HandleCallback(ctx, body, sig)
	require.NoError(t, err)

	// Verify the order was marked completed.
	var status string
	err = db.QueryRowContext(ctx, `SELECT status FROM airbills_orders WHERE id=$1`, orderID).Scan(&status)
	require.NoError(t, err)
	assert.Equal(t, "completed", status)

	// Verify a push notification was sent.
	require.Len(t, notifier.pushes, 1)
	assert.Equal(t, userID, notifier.pushes[0].userID)
	assert.Contains(t, notifier.pushes[0].title, "Airtime paid")
	assert.Contains(t, notifier.pushes[0].message, "08012345678")
}
