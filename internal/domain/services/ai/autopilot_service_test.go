package ai

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// --- mocks ---

type mockAutopilotUsers struct {
	users []struct {
		ID      uuid.UUID
		Country string
	}
	err error
}

func (m *mockAutopilotUsers) GetAllActiveUsers(_ context.Context) ([]struct {
	ID      uuid.UUID
	Country string
}, error) {
	return m.users, m.err
}

type mockAutopilotControl struct {
	level  string
	err    error
	levels map[uuid.UUID]string
}

func (m *mockAutopilotControl) GetControlLevel(_ context.Context, uid uuid.UUID) (string, error) {
	if m.levels != nil {
		if l, ok := m.levels[uid]; ok {
			return l, nil
		}
	}
	return m.level, m.err
}

type mockAutopilotQueue struct {
	mu      sync.Mutex
	actions map[uuid.UUID][]AutopilotQueueAction
}

func (m *mockAutopilotQueue) Push(_ context.Context, userID uuid.UUID, action AutopilotQueueAction) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.actions == nil {
		m.actions = make(map[uuid.UUID][]AutopilotQueueAction)
	}
	m.actions[userID] = append(m.actions[userID], action)
	return nil
}

func (m *mockAutopilotQueue) Pop(_ context.Context, userID uuid.UUID) (*AutopilotQueueAction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	actions := m.actions[userID]
	if len(actions) == 0 {
		return nil, nil
	}
	act := actions[0]
	m.actions[userID] = actions[1:]
	return &act, nil
}

func (m *mockAutopilotQueue) List(_ context.Context, userID uuid.UUID) ([]AutopilotQueueAction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.actions[userID], nil
}

func (m *mockAutopilotQueue) Clear(_ context.Context, userID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.actions, userID)
	return nil
}

func (m *mockAutopilotQueue) Len(_ context.Context, userID uuid.UUID) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.actions[userID]), nil
}

type mockPushSender struct {
	mu   sync.Mutex
	sent []pushRecord
}

type pushRecord struct {
	userID uuid.UUID
	title  string
	body   string
	data   map[string]interface{}
}

func (m *mockPushSender) SendToUser(_ context.Context, userID uuid.UUID, title, body string, data map[string]interface{}) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, pushRecord{userID: userID, title: title, body: body, data: data})
	return nil
}

type mockMoneySpender struct {
	flow *entities.MoneyFlowSummary
	err  error
}

func (m *mockMoneySpender) GetMoneyFlow(_ context.Context, _ uuid.UUID, _, _ time.Time) (*entities.MoneyFlowSummary, error) {
	return m.flow, m.err
}

type mockBalanceReader struct {
	balance decimal.Decimal
	err     error
}

func (m *mockBalanceReader) GetAccountBalance(_ context.Context, _ uuid.UUID, _ entities.AccountType) (decimal.Decimal, error) {
	return m.balance, m.err
}

type mockBudgetReader struct {
	budget *entities.SpendingBudget
	err    error
}

func (m *mockBudgetReader) GetByUserID(_ context.Context, _ uuid.UUID) (*entities.SpendingBudget, error) {
	return m.budget, m.err
}

type mockTransferer struct {
	transferErr error
	spendBal    decimal.Decimal
	calls       int
}

func (m *mockTransferer) TransferSpendToStash(_ context.Context, _ uuid.UUID, _ decimal.Decimal, _ string) error {
	m.calls++
	return m.transferErr
}

func (m *mockTransferer) GetSpendBalance(_ context.Context, _ uuid.UUID) (decimal.Decimal, error) {
	return m.spendBal, nil
}

// --- helpers ---

func newTestAutopilotService(
	users *mockAutopilotUsers,
	control *mockAutopilotControl,
	queue *mockAutopilotQueue,
	push *mockPushSender,
	spending *mockMoneySpender,
	balances *mockBalanceReader,
	budgets *mockBudgetReader,
	transferer *mockTransferer,
) *AutopilotService {
	return NewAutopilotService(users, control, queue, push, spending, balances, budgets, transferer, nil, zap.NewNop(), nil, nil)
}

// fullAutopilotUser returns a USD-denominated user so the existing threshold
// tests (written with USD-magnitude values) stay valid. NGN/PPP scaling is
// covered separately by TestAutopilotService_ScanOvernight_NGNThresholdsScaled
// and TestScaledThreshold.
func fullAutopilotUser(id uuid.UUID) struct {
	ID      uuid.UUID
	Country string
} {
	return struct {
		ID      uuid.UUID
		Country string
	}{ID: id, Country: "US"}
}

// --- tests ---

func TestAutopilotService_WeeklyAuditSendsOncePerWeek(t *testing.T) {
	uid := uuid.New()
	users := &mockAutopilotUsers{users: []struct {
		ID      uuid.UUID
		Country string
	}{fullAutopilotUser(uid)}}
	push := &mockPushSender{}
	svc := newTestAutopilotService(
		users,
		&mockAutopilotControl{level: entities.ControlLevelGuided},
		&mockAutopilotQueue{},
		push,
		&mockMoneySpender{flow: &entities.MoneyFlowSummary{
			TotalDeposits:  decimal.NewFromInt(1000),
			TotalCardSpend: decimal.NewFromInt(350),
		}},
		&mockBalanceReader{},
		&mockBudgetReader{},
		&mockTransferer{},
	)

	svc.RunWeeklyAudit(context.Background())
	svc.RunWeeklyAudit(context.Background())

	require.Len(t, push.sent, 1)
	assert.Empty(t, push.sent[0].title)
	assert.Contains(t, push.sent[0].body, "$1000.00")
	assert.Contains(t, push.sent[0].body, "$350.00")
	assert.Equal(t, "weekly_audit", push.sent[0].data["type"])
	assert.Equal(t, int64(1), svc.Metrics().WeeklyAuditsSent)
}

func TestAutopilotService_MorningScan_AlertOnHighSpend(t *testing.T) {
	uid := uuid.New()
	users := &mockAutopilotUsers{users: []struct {
		ID      uuid.UUID
		Country string
	}{fullAutopilotUser(uid)}}
	control := &mockAutopilotControl{level: entities.ControlLevelFull}
	push := &mockPushSender{}
	spending := &mockMoneySpender{
		flow: &entities.MoneyFlowSummary{
			TotalCardSpend: decimal.NewFromInt(600),
		},
	}
	balances := &mockBalanceReader{balance: decimal.NewFromInt(100)}
	queue := &mockAutopilotQueue{}
	svc := newTestAutopilotService(users, control, queue, push, spending, balances, &mockBudgetReader{}, &mockTransferer{})

	svc.RunMorningScan(context.Background())

	require.Len(t, push.sent, 1)
	assert.Empty(t, push.sent[0].title)
	assert.Contains(t, push.sent[0].body, "$600")
}

func TestAutopilotService_MorningScan_AlertOnLowBalance(t *testing.T) {
	uid := uuid.New()
	users := &mockAutopilotUsers{users: []struct {
		ID      uuid.UUID
		Country string
	}{fullAutopilotUser(uid)}}
	control := &mockAutopilotControl{level: entities.ControlLevelFull}
	push := &mockPushSender{}
	spending := &mockMoneySpender{
		flow: &entities.MoneyFlowSummary{},
	}
	balances := &mockBalanceReader{balance: decimal.NewFromInt(10)}
	queue := &mockAutopilotQueue{}
	svc := newTestAutopilotService(users, control, queue, push, spending, balances, &mockBudgetReader{}, &mockTransferer{})

	svc.RunMorningScan(context.Background())

	require.Len(t, push.sent, 1)
	assert.Empty(t, push.sent[0].title)
	assert.Contains(t, push.sent[0].body, "$10")
}

func TestAutopilotService_MorningScan_IncludesGuidedUsers(t *testing.T) {
	// Guided users get risk alerts; they just don't auto-move money.
	uid := uuid.New()
	users := &mockAutopilotUsers{users: []struct {
		ID      uuid.UUID
		Country string
	}{fullAutopilotUser(uid)}}
	control := &mockAutopilotControl{level: entities.ControlLevelGuided}
	push := &mockPushSender{}
	spending := &mockMoneySpender{
		flow: &entities.MoneyFlowSummary{TotalCardSpend: decimal.NewFromInt(600)},
	}
	svc := newTestAutopilotService(users, control, &mockAutopilotQueue{}, push, spending, &mockBalanceReader{balance: decimal.NewFromInt(100)}, &mockBudgetReader{}, &mockTransferer{})

	svc.RunMorningScan(context.Background())

	require.Len(t, push.sent, 1)
	assert.Empty(t, push.sent[0].title)
}

func TestAutopilotService_MorningScan_SkipsMonitorUsers(t *testing.T) {
	uid := uuid.New()
	users := &mockAutopilotUsers{users: []struct {
		ID      uuid.UUID
		Country string
	}{fullAutopilotUser(uid)}}
	control := &mockAutopilotControl{level: entities.ControlLevelMonitor}
	push := &mockPushSender{}
	spending := &mockMoneySpender{
		flow: &entities.MoneyFlowSummary{TotalCardSpend: decimal.NewFromInt(600)},
	}
	svc := newTestAutopilotService(users, control, &mockAutopilotQueue{}, push, spending, &mockBalanceReader{balance: decimal.NewFromInt(100)}, &mockBudgetReader{}, &mockTransferer{})

	svc.RunMorningScan(context.Background())

	assert.Empty(t, push.sent)
}

func TestAutopilotService_MorningScan_DeduplicatesSameDay(t *testing.T) {
	uid := uuid.New()
	users := &mockAutopilotUsers{users: []struct {
		ID      uuid.UUID
		Country string
	}{fullAutopilotUser(uid)}}
	control := &mockAutopilotControl{level: entities.ControlLevelFull}
	push := &mockPushSender{}
	spending := &mockMoneySpender{
		flow: &entities.MoneyFlowSummary{TotalCardSpend: decimal.NewFromInt(600)},
	}
	balances := &mockBalanceReader{balance: decimal.NewFromInt(100)}
	queue := &mockAutopilotQueue{}
	svc := newTestAutopilotService(users, control, queue, push, spending, balances, &mockBudgetReader{}, &mockTransferer{})

	svc.RunMorningScan(context.Background())
	svc.RunMorningScan(context.Background())

	require.Len(t, push.sent, 1)
}

func TestAutopilotService_MorningScan_NoAlertWhenFine(t *testing.T) {
	uid := uuid.New()
	users := &mockAutopilotUsers{users: []struct {
		ID      uuid.UUID
		Country string
	}{fullAutopilotUser(uid)}}
	control := &mockAutopilotControl{level: entities.ControlLevelFull}
	push := &mockPushSender{}
	spending := &mockMoneySpender{
		flow: &entities.MoneyFlowSummary{TotalCardSpend: decimal.NewFromInt(30)},
	}
	balances := &mockBalanceReader{balance: decimal.NewFromInt(500)}
	queue := &mockAutopilotQueue{}
	svc := newTestAutopilotService(users, control, queue, push, spending, balances, &mockBudgetReader{}, &mockTransferer{})

	svc.RunMorningScan(context.Background())

	assert.Empty(t, push.sent)
}

func TestAutopilotService_Midday_QueuesSurplusAlertNotTransfer(t *testing.T) {
	uid := uuid.New()
	users := &mockAutopilotUsers{users: []struct {
		ID      uuid.UUID
		Country string
	}{fullAutopilotUser(uid)}}
	control := &mockAutopilotControl{level: entities.ControlLevelFull}
	push := &mockPushSender{}
	spending := &mockMoneySpender{
		flow: &entities.MoneyFlowSummary{TotalCardSpend: decimal.NewFromInt(200)},
	}
	balances := &mockBalanceReader{balance: decimal.NewFromInt(1000)}
	budgets := &mockBudgetReader{
		budget: &entities.SpendingBudget{MonthlyLimit: decimal.NewFromInt(2000)},
	}
	queue := &mockAutopilotQueue{}
	svc := newTestAutopilotService(users, control, queue, push, spending, balances, budgets, &mockTransferer{})

	svc.RunMiddayCheck(context.Background())

	actions, _ := queue.List(context.Background(), uid)
	require.Len(t, actions, 1)
	assert.Equal(t, "alert_surplus", actions[0].Tool)
}

func TestAutopilotService_Midday_QueuesOverspendAlert(t *testing.T) {
	uid := uuid.New()
	users := &mockAutopilotUsers{users: []struct {
		ID      uuid.UUID
		Country string
	}{fullAutopilotUser(uid)}}
	control := &mockAutopilotControl{level: entities.ControlLevelFull}
	push := &mockPushSender{}
	spending := &mockMoneySpender{
		flow: &entities.MoneyFlowSummary{TotalCardSpend: decimal.NewFromInt(3000)},
	}
	balances := &mockBalanceReader{balance: decimal.NewFromInt(1000)}
	budgets := &mockBudgetReader{
		budget: &entities.SpendingBudget{MonthlyLimit: decimal.NewFromInt(2000)},
	}
	queue := &mockAutopilotQueue{}
	svc := newTestAutopilotService(users, control, queue, push, spending, balances, budgets, &mockTransferer{})

	svc.RunMiddayCheck(context.Background())

	actions, _ := queue.List(context.Background(), uid)
	require.Len(t, actions, 1)
	assert.Equal(t, "alert_overspend", actions[0].Tool)
}

func TestAutopilotService_Midday_SkipsNoBudget(t *testing.T) {
	uid := uuid.New()
	users := &mockAutopilotUsers{users: []struct {
		ID      uuid.UUID
		Country string
	}{fullAutopilotUser(uid)}}
	control := &mockAutopilotControl{level: entities.ControlLevelFull}
	queue := &mockAutopilotQueue{}
	svc := newTestAutopilotService(users, control, queue, &mockPushSender{}, &mockMoneySpender{}, &mockBalanceReader{}, &mockBudgetReader{}, &mockTransferer{})

	svc.RunMiddayCheck(context.Background())

	actions, _ := queue.List(context.Background(), uid)
	assert.Empty(t, actions)
}

func TestAutopilotService_Midday_SkipsNonFullAutopilot(t *testing.T) {
	uid := uuid.New()
	users := &mockAutopilotUsers{users: []struct {
		ID      uuid.UUID
		Country string
	}{fullAutopilotUser(uid)}}
	control := &mockAutopilotControl{level: entities.ControlLevelMonitor}
	queue := &mockAutopilotQueue{}
	svc := newTestAutopilotService(users, control, queue, &mockPushSender{}, &mockMoneySpender{}, &mockBalanceReader{}, &mockBudgetReader{}, &mockTransferer{})

	svc.RunMiddayCheck(context.Background())

	actions, _ := queue.List(context.Background(), uid)
	assert.Empty(t, actions)
}

func TestAutopilotService_Evening_HoldsLegacyTransferAsSuggestion(t *testing.T) {
	uid := uuid.New()
	users := &mockAutopilotUsers{users: []struct {
		ID      uuid.UUID
		Country string
	}{fullAutopilotUser(uid)}}
	control := &mockAutopilotControl{level: entities.ControlLevelFull}
	push := &mockPushSender{}
	queue := &mockAutopilotQueue{}
	_ = queue.Push(context.Background(), uid, AutopilotQueueAction{
		Tool: ToolTransferFunds,
		Args: map[string]interface{}{"amount": 100.0, "from": "spend", "to": "stash"},
	})
	transferer := &mockTransferer{}
	svc := newTestAutopilotService(users, control, queue, push, &mockMoneySpender{}, &mockBalanceReader{}, &mockBudgetReader{}, transferer)

	svc.RunEveningReview(context.Background())

	require.Len(t, push.sent, 1)
	assert.Contains(t, push.sent[0].body, "held off")
	assert.Equal(t, 0, transferer.calls)
}

func TestAutopilotService_Evening_ReportsOverspend(t *testing.T) {
	uid := uuid.New()
	users := &mockAutopilotUsers{users: []struct {
		ID      uuid.UUID
		Country string
	}{fullAutopilotUser(uid)}}
	control := &mockAutopilotControl{level: entities.ControlLevelFull}
	push := &mockPushSender{}
	queue := &mockAutopilotQueue{}
	_ = queue.Push(context.Background(), uid, AutopilotQueueAction{
		Tool:   "alert_overspend",
		Reason: "On track to exceed budget by $500.00",
	})
	svc := newTestAutopilotService(users, control, queue, push, &mockMoneySpender{}, &mockBalanceReader{}, &mockBudgetReader{}, &mockTransferer{})

	svc.RunEveningReview(context.Background())

	require.Len(t, push.sent, 1)
	assert.Contains(t, push.sent[0].body, "$500")
}

func TestAutopilotService_Evening_EmptyQueueSkips(t *testing.T) {
	uid := uuid.New()
	users := &mockAutopilotUsers{users: []struct {
		ID      uuid.UUID
		Country string
	}{fullAutopilotUser(uid)}}
	control := &mockAutopilotControl{level: entities.ControlLevelFull}
	push := &mockPushSender{}
	queue := &mockAutopilotQueue{}
	svc := newTestAutopilotService(users, control, queue, push, &mockMoneySpender{}, &mockBalanceReader{}, &mockBudgetReader{}, &mockTransferer{})

	svc.RunEveningReview(context.Background())

	assert.Empty(t, push.sent)
}

func TestAutopilotService_LoadFullAutopilotUsers_FiltersByControlLevel(t *testing.T) {
	fullUser := uuid.New()
	guidedUser := uuid.New()
	users := &mockAutopilotUsers{
		users: []struct {
			ID      uuid.UUID
			Country string
		}{fullAutopilotUser(fullUser), fullAutopilotUser(guidedUser)},
	}
	control := &mockAutopilotControl{
		levels: map[uuid.UUID]string{
			fullUser:   entities.ControlLevelFull,
			guidedUser: entities.ControlLevelGuided,
		},
	}
	svc := newTestAutopilotService(users, control, &mockAutopilotQueue{}, &mockPushSender{}, &mockMoneySpender{}, &mockBalanceReader{}, &mockBudgetReader{}, &mockTransferer{})

	result := svc.loadFullAutopilotUsers(context.Background())

	require.Len(t, result, 1)
	assert.Equal(t, fullUser, result[0].ID)
}

func TestAutopilotService_ScanOvernight_HighSpend(t *testing.T) {
	uid := uuid.New()
	svc := newTestAutopilotService(&mockAutopilotUsers{}, &mockAutopilotControl{}, &mockAutopilotQueue{}, &mockPushSender{},
		&mockMoneySpender{flow: &entities.MoneyFlowSummary{TotalCardSpend: decimal.NewFromInt(600)}},
		&mockBalanceReader{balance: decimal.NewFromInt(100)},
		&mockBudgetReader{}, &mockTransferer{})

	results := svc.scanOvernightForAnomalies(context.Background(), uid, "US", time.Now().Add(-24*time.Hour), time.Now())

	require.Len(t, results, 1)
	assert.Contains(t, results[0].Description, "$600")
}

func TestAutopilotService_ScanOvernight_LowBalance(t *testing.T) {
	uid := uuid.New()
	svc := newTestAutopilotService(&mockAutopilotUsers{}, &mockAutopilotControl{}, &mockAutopilotQueue{}, &mockPushSender{},
		&mockMoneySpender{flow: &entities.MoneyFlowSummary{}},
		&mockBalanceReader{balance: decimal.NewFromInt(10)},
		&mockBudgetReader{}, &mockTransferer{})

	results := svc.scanOvernightForAnomalies(context.Background(), uid, "US", time.Now().Add(-24*time.Hour), time.Now())

	require.Len(t, results, 1)
	assert.Contains(t, results[0].Description, "$10")
}

func TestAutopilotService_ScanOvernight_BothIssues(t *testing.T) {
	uid := uuid.New()
	svc := newTestAutopilotService(&mockAutopilotUsers{}, &mockAutopilotControl{}, &mockAutopilotQueue{}, &mockPushSender{},
		&mockMoneySpender{flow: &entities.MoneyFlowSummary{TotalCardSpend: decimal.NewFromInt(600)}},
		&mockBalanceReader{balance: decimal.NewFromInt(10)},
		&mockBudgetReader{}, &mockTransferer{})

	results := svc.scanOvernightForAnomalies(context.Background(), uid, "US", time.Now().Add(-24*time.Hour), time.Now())

	require.Len(t, results, 2)
}

func TestAutopilotService_ScanOvernight_NoIssues(t *testing.T) {
	uid := uuid.New()
	svc := newTestAutopilotService(&mockAutopilotUsers{}, &mockAutopilotControl{}, &mockAutopilotQueue{}, &mockPushSender{},
		&mockMoneySpender{flow: &entities.MoneyFlowSummary{TotalCardSpend: decimal.NewFromInt(30)}},
		&mockBalanceReader{balance: decimal.NewFromInt(500)},
		&mockBudgetReader{}, &mockTransferer{})

	results := svc.scanOvernightForAnomalies(context.Background(), uid, "US", time.Now().Add(-24*time.Hour), time.Now())

	assert.Empty(t, results)
}

func TestAutopilotService_ScanOvernight_NGNThresholdsScaled(t *testing.T) {
	uid := uuid.New()
	// ₦100,000 overnight spend is ~$67 — below the USD $500 anomaly bar but
	// above a properly scaled NGN cutoff would not trigger. A spend of
	// ₦900,000 (~$600) should trigger once thresholds are PPP-scaled.
	svc := newTestAutopilotService(&mockAutopilotUsers{}, &mockAutopilotControl{}, &mockAutopilotQueue{}, &mockPushSender{},
		&mockMoneySpender{flow: &entities.MoneyFlowSummary{TotalCardSpend: decimal.NewFromInt(900000)}},
		&mockBalanceReader{balance: decimal.NewFromInt(20000)}, // ₦20k ≈ $13 -> below scaled ₦30k low-balance bar
		&mockBudgetReader{}, &mockTransferer{})

	results := svc.scanOvernightForAnomalies(context.Background(), uid, "NG", time.Now().Add(-24*time.Hour), time.Now())

	require.Len(t, results, 2, "expected scaled NGN thresholds to flag both high spend and low balance")
}

func TestScaledThreshold(t *testing.T) {
	usd := decimal.NewFromInt(500)
	assert.True(t, scaledThreshold(usd, "NG").Equal(decimal.NewFromInt(750000)), "NG should scale by 1500")
	assert.True(t, scaledThreshold(usd, "US").Equal(usd), "US unlisted => 1x")
	assert.True(t, scaledThreshold(usd, "").Equal(usd), "empty => 1x")
	assert.True(t, scaledThreshold(usd, "ng").Equal(decimal.NewFromInt(750000)), "case-insensitive")
}

func TestAutopilotService_LoadFullAutopilotUsers_SkipsOnError(t *testing.T) {
	svc := newTestAutopilotService(&mockAutopilotUsers{err: assert.AnError}, &mockAutopilotControl{}, &mockAutopilotQueue{}, &mockPushSender{}, &mockMoneySpender{}, &mockBalanceReader{}, &mockBudgetReader{}, &mockTransferer{})

	result := svc.loadFullAutopilotUsers(context.Background())
	assert.Nil(t, result)
}
