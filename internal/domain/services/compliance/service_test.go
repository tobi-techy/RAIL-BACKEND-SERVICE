package compliance

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/didit"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ── Mocks ────────────────────────────────────────────────────────

type mockDiditClient struct {
	createTxResp *didit.TransactionResponse
	createTxErr  error
	getTxResp    *didit.TransactionResponse
	getTxErr     error
	amlResp      *didit.AMLScreeningResponse
	amlErr       error
}

func (m *mockDiditClient) CreateTransaction(_ context.Context, _ *didit.CreateTransactionRequest) (*didit.TransactionResponse, error) {
	return m.createTxResp, m.createTxErr
}
func (m *mockDiditClient) GetTransaction(_ context.Context, _ string) (*didit.TransactionResponse, error) {
	return m.getTxResp, m.getTxErr
}
func (m *mockDiditClient) ScreenAML(_ context.Context, _ *didit.AMLScreeningRequest) (*didit.AMLScreeningResponse, error) {
	return m.amlResp, m.amlErr
}

type mockRepo struct {
	screenings []*entities.ComplianceScreening
	alerts     []*entities.ComplianceAlert
	updateErr  error
	getResult  *entities.ComplianceScreening
	getErr     error
}

func (m *mockRepo) CreateScreening(_ context.Context, s *entities.ComplianceScreening) error {
	m.screenings = append(m.screenings, s)
	return nil
}
func (m *mockRepo) CreateAlert(_ context.Context, a *entities.ComplianceAlert) error {
	m.alerts = append(m.alerts, a)
	return nil
}
func (m *mockRepo) UpdateScreeningStatus(_ context.Context, _, _, _ string, _ int) error {
	return m.updateErr
}
func (m *mockRepo) GetScreeningByDiditUUID(_ context.Context, _ string) (*entities.ComplianceScreening, error) {
	return m.getResult, m.getErr
}

type mockUserLookup struct {
	profile *entities.UserProfile
	err     error
}

func (m *mockUserLookup) GetByID(_ context.Context, _ uuid.UUID) (*entities.UserProfile, error) {
	return m.profile, m.err
}

type mockFreezer struct {
	frozenUsers []uuid.UUID
	reasons     []string
}

func (m *mockFreezer) FreezeUser(_ context.Context, userID uuid.UUID, reason string) error {
	m.frozenUsers = append(m.frozenUsers, userID)
	m.reasons = append(m.reasons, reason)
	return nil
}

func newTestService(dc *mockDiditClient, repo *mockRepo) *Service {
	return NewService(dc, repo, zap.NewNop())
}

var testUserID = uuid.New()

// ── ScreenTransaction Tests ──────────────────────────────────────

func TestScreenTransaction_Approved(t *testing.T) {
	dc := &mockDiditClient{createTxResp: &didit.TransactionResponse{UUID: "tx-1", Status: "APPROVED", Score: 10, Severity: "LOW"}}
	repo := &mockRepo{}
	svc := newTestService(dc, repo)

	status, err := svc.ScreenTransaction(context.Background(), testUserID, "ref-1", "inbound", decimal.NewFromInt(100), "USD", "John Doe")
	require.NoError(t, err)
	assert.Equal(t, "APPROVED", status)
	assert.Len(t, repo.screenings, 1)
	assert.Len(t, repo.alerts, 0) // No alert for approved
}

func TestScreenTransaction_Declined(t *testing.T) {
	dc := &mockDiditClient{createTxResp: &didit.TransactionResponse{UUID: "tx-2", Status: "DECLINED", Score: 95, Severity: "CRITICAL"}}
	repo := &mockRepo{}
	svc := newTestService(dc, repo)

	status, err := svc.ScreenTransaction(context.Background(), testUserID, "ref-2", "inbound", decimal.NewFromInt(100), "USD", "John Doe")
	require.NoError(t, err)
	assert.Equal(t, "DECLINED", status)
	assert.Len(t, repo.screenings, 1)
	assert.Len(t, repo.alerts, 1)
	assert.Equal(t, "transaction_declined", repo.alerts[0].AlertType)
}

func TestScreenTransaction_InReview(t *testing.T) {
	dc := &mockDiditClient{createTxResp: &didit.TransactionResponse{UUID: "tx-3", Status: "IN_REVIEW", Score: 60, Severity: "MEDIUM"}}
	repo := &mockRepo{}
	svc := newTestService(dc, repo)

	status, err := svc.ScreenTransaction(context.Background(), testUserID, "ref-3", "inbound", decimal.NewFromInt(100), "USD", "John Doe")
	require.NoError(t, err)
	assert.Equal(t, "IN_REVIEW", status)
	assert.Len(t, repo.alerts, 1)
	assert.Equal(t, "transaction_flagged", repo.alerts[0].AlertType)
}

func TestScreenTransaction_DiditError_FailsClosed(t *testing.T) {
	dc := &mockDiditClient{createTxErr: fmt.Errorf("connection refused")}
	repo := &mockRepo{}
	svc := newTestService(dc, repo)

	status, err := svc.ScreenTransaction(context.Background(), testUserID, "ref-4", "inbound", decimal.NewFromInt(100), "USD", "John Doe")
	assert.Error(t, err)
	assert.Equal(t, "IN_REVIEW", status)
	assert.Len(t, repo.screenings, 0) // No screening persisted on Didit failure
}

func TestScreenTransaction_MediumTier_TimeoutApprovesWithRetry(t *testing.T) {
	// Simulate a context deadline exceeded
	dc := &mockDiditClient{createTxErr: context.DeadlineExceeded}
	repo := &mockRepo{}
	svc := newTestService(dc, repo)
	// Set mature account so it classifies as TierMedium
	matureProfile := &entities.UserProfile{CreatedAt: time.Now().Add(-120 * 24 * time.Hour)}
	svc.SetUserLookup(&mockUserLookup{profile: matureProfile})

	status, err := svc.ScreenTransaction(context.Background(), testUserID, "ref-5", "inbound", decimal.NewFromInt(200), "USD", "John Doe")
	require.NoError(t, err)
	assert.Equal(t, "APPROVED", status) // Timeout = approve with async retry
	// Give goroutine time to fire
	time.Sleep(50 * time.Millisecond)
}

func TestScreenTransaction_MediumTier_NonTimeoutError_FailsClosed(t *testing.T) {
	dc := &mockDiditClient{createTxErr: fmt.Errorf("500 internal server error")}
	repo := &mockRepo{}
	svc := newTestService(dc, repo)
	matureProfile := &entities.UserProfile{CreatedAt: time.Now().Add(-120 * 24 * time.Hour)}
	svc.SetUserLookup(&mockUserLookup{profile: matureProfile})

	status, err := svc.ScreenTransaction(context.Background(), testUserID, "ref-6", "inbound", decimal.NewFromInt(200), "USD", "John Doe")
	assert.Error(t, err)
	assert.Equal(t, "IN_REVIEW", status) // Non-timeout error = fail closed
}

func TestScreenTransaction_ResolvesEmptyName(t *testing.T) {
	firstName := "Jane"
	lastName := "Doe"
	dc := &mockDiditClient{createTxResp: &didit.TransactionResponse{UUID: "tx-name", Status: "APPROVED", Score: 5, Severity: "LOW"}}
	repo := &mockRepo{}
	svc := newTestService(dc, repo)
	svc.SetUserLookup(&mockUserLookup{profile: &entities.UserProfile{
		FirstName: &firstName,
		LastName:  &lastName,
		CreatedAt: time.Now(),
	}})

	status, err := svc.ScreenTransaction(context.Background(), testUserID, "ref-name", "inbound", decimal.NewFromInt(100), "USD", "")
	require.NoError(t, err)
	assert.Equal(t, "APPROVED", status)
}

// ── Tier Classification Tests ────────────────────────────────────

func TestClassifyRisk_OutboundAlwaysHigh(t *testing.T) {
	svc := newTestService(nil, nil)

	tier := svc.classifyRisk(context.Background(), testUserID, decimal.NewFromInt(10), "outbound")
	assert.Equal(t, TierHigh, tier)

	tier = svc.classifyRisk(context.Background(), testUserID, decimal.NewFromInt(100000), "outbound")
	assert.Equal(t, TierHigh, tier)
}

func TestClassifyRisk_LargeAmountAlwaysHigh(t *testing.T) {
	svc := newTestService(nil, nil)
	matureProfile := &entities.UserProfile{CreatedAt: time.Now().Add(-120 * 24 * time.Hour)}
	svc.SetUserLookup(&mockUserLookup{profile: matureProfile})

	tier := svc.classifyRisk(context.Background(), testUserID, decimal.NewFromInt(6000), "inbound")
	assert.Equal(t, TierHigh, tier)
}

func TestClassifyRisk_MatureAccount_SmallAmount_Medium(t *testing.T) {
	svc := newTestService(nil, nil)
	matureProfile := &entities.UserProfile{CreatedAt: time.Now().Add(-120 * 24 * time.Hour)}
	svc.SetUserLookup(&mockUserLookup{profile: matureProfile})

	tier := svc.classifyRisk(context.Background(), testUserID, decimal.NewFromInt(200), "inbound")
	assert.Equal(t, TierMedium, tier)
}

func TestClassifyRisk_NewAccount_SmallAmount_High(t *testing.T) {
	svc := newTestService(nil, nil)
	newProfile := &entities.UserProfile{CreatedAt: time.Now().Add(-7 * 24 * time.Hour)}
	svc.SetUserLookup(&mockUserLookup{profile: newProfile})

	tier := svc.classifyRisk(context.Background(), testUserID, decimal.NewFromInt(200), "inbound")
	assert.Equal(t, TierHigh, tier)
}

func TestClassifyRisk_NoUserLookup_DefaultsHigh(t *testing.T) {
	svc := newTestService(nil, nil)
	// No SetUserLookup called

	tier := svc.classifyRisk(context.Background(), testUserID, decimal.NewFromInt(200), "inbound")
	assert.Equal(t, TierHigh, tier)
}

// ── Webhook Handler Tests ────────────────────────────────────────

func TestHandleWebhook_Idempotent_SkipsSameStatus(t *testing.T) {
	screeningID := uuid.New()
	repo := &mockRepo{
		getResult: &entities.ComplianceScreening{ID: screeningID, UserID: testUserID, Status: "DECLINED"},
	}
	svc := newTestService(nil, repo)

	err := svc.HandleTransactionWebhook(context.Background(), &didit.TransactionWebhookPayload{
		UUID: "tx-idem", Status: "DECLINED", Score: 90, Severity: "HIGH",
	})
	require.NoError(t, err)
	assert.Len(t, repo.alerts, 0) // Skipped — already at DECLINED
}

func TestHandleWebhook_StatusChange_CreatesAlert(t *testing.T) {
	screeningID := uuid.New()
	repo := &mockRepo{
		getResult: &entities.ComplianceScreening{ID: screeningID, UserID: testUserID, Status: "IN_REVIEW"},
	}
	svc := newTestService(nil, repo)

	err := svc.HandleTransactionWebhook(context.Background(), &didit.TransactionWebhookPayload{
		UUID: "tx-change", Status: "DECLINED", Score: 95, Severity: "CRITICAL",
	})
	require.NoError(t, err)
	assert.Len(t, repo.alerts, 1)
	assert.Equal(t, "webhook_status_change", repo.alerts[0].AlertType)
}

func TestHandleWebhook_Declined_FreezesUser(t *testing.T) {
	screeningID := uuid.New()
	repo := &mockRepo{
		getResult: &entities.ComplianceScreening{ID: screeningID, UserID: testUserID, Status: "IN_REVIEW"},
	}
	freezer := &mockFreezer{}
	svc := newTestService(nil, repo)
	svc.SetUserFreezer(freezer)

	err := svc.HandleTransactionWebhook(context.Background(), &didit.TransactionWebhookPayload{
		UUID: "tx-freeze", Status: "DECLINED", Score: 99, Severity: "CRITICAL",
	})
	require.NoError(t, err)
	assert.Len(t, freezer.frozenUsers, 1)
	assert.Equal(t, testUserID, freezer.frozenUsers[0])
}

func TestHandleWebhook_Approved_NoFreeze(t *testing.T) {
	screeningID := uuid.New()
	repo := &mockRepo{
		getResult: &entities.ComplianceScreening{ID: screeningID, UserID: testUserID, Status: "IN_REVIEW"},
	}
	freezer := &mockFreezer{}
	svc := newTestService(nil, repo)
	svc.SetUserFreezer(freezer)

	err := svc.HandleTransactionWebhook(context.Background(), &didit.TransactionWebhookPayload{
		UUID: "tx-ok", Status: "APPROVED", Score: 5, Severity: "LOW",
	})
	require.NoError(t, err)
	assert.Len(t, freezer.frozenUsers, 0)
}

func TestHandleWebhook_UpdateError_ReturnsError(t *testing.T) {
	repo := &mockRepo{
		getResult: &entities.ComplianceScreening{ID: uuid.New(), UserID: testUserID, Status: "IN_REVIEW"},
		updateErr: fmt.Errorf("db connection lost"),
	}
	svc := newTestService(nil, repo)

	err := svc.HandleTransactionWebhook(context.Background(), &didit.TransactionWebhookPayload{
		UUID: "tx-err", Status: "DECLINED", Score: 90, Severity: "HIGH",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "update screening status")
}

// ── AML Screening Tests ──────────────────────────────────────────

func TestScreenUserAML_Approved(t *testing.T) {
	dc := &mockDiditClient{amlResp: &didit.AMLScreeningResponse{
		RequestID: "aml-1",
		AML:       didit.AMLResult{Status: "Approved", TotalHits: 0, Score: 0},
	}}
	repo := &mockRepo{}
	svc := newTestService(dc, repo)

	status, err := svc.ScreenUserAML(context.Background(), testUserID, "John Doe", "1990-01-01", "NG", "")
	require.NoError(t, err)
	assert.Equal(t, "Approved", status)
	assert.Len(t, repo.screenings, 1)
	assert.Len(t, repo.alerts, 0)
}

func TestScreenUserAML_SanctionsMatch_CreatesAlert(t *testing.T) {
	dc := &mockDiditClient{amlResp: &didit.AMLScreeningResponse{
		RequestID: "aml-2",
		AML: didit.AMLResult{
			Status:    "Declined",
			TotalHits: 1,
			Score:     95,
			Hits:      []didit.AMLHit{{ID: "h1", Datasets: []string{"Sanctions"}, MatchScore: 98}},
		},
	}}
	repo := &mockRepo{}
	svc := newTestService(dc, repo)

	status, err := svc.ScreenUserAML(context.Background(), testUserID, "Bad Actor", "1985-06-15", "IR", "")
	require.NoError(t, err)
	assert.Equal(t, "Declined", status)
	assert.Len(t, repo.alerts, 1)
	assert.Equal(t, "sanctions_match", repo.alerts[0].AlertType)
}

func TestScreenUserAML_PEPHit_CreatesAMLAlert(t *testing.T) {
	dc := &mockDiditClient{amlResp: &didit.AMLScreeningResponse{
		RequestID: "aml-3",
		AML: didit.AMLResult{
			Status:    "In Review",
			TotalHits: 1,
			Score:     70,
			Hits:      []didit.AMLHit{{ID: "h2", Datasets: []string{"PEP"}, MatchScore: 80}},
		},
	}}
	repo := &mockRepo{}
	svc := newTestService(dc, repo)

	status, err := svc.ScreenUserAML(context.Background(), testUserID, "Politician Name", "1970-03-20", "NG", "")
	require.NoError(t, err)
	assert.Equal(t, "In Review", status)
	assert.Len(t, repo.alerts, 1)
	assert.Equal(t, "aml_hit", repo.alerts[0].AlertType) // PEP, not sanctions
}

func TestScreenUserAML_DiditError(t *testing.T) {
	dc := &mockDiditClient{amlErr: fmt.Errorf("timeout")}
	repo := &mockRepo{}
	svc := newTestService(dc, repo)

	_, err := svc.ScreenUserAML(context.Background(), testUserID, "John Doe", "", "", "")
	assert.Error(t, err)
}
