package automation

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type mockNotifier struct {
	pushes []pushCall
}

type pushCall struct {
	title string
	body  string
	data  map[string]interface{}
}

func (m *mockNotifier) SendPush(_ context.Context, _ uuid.UUID, title, message string, data map[string]interface{}) error {
	m.pushes = append(m.pushes, pushCall{title: title, body: message, data: data})
	return nil
}

// An obligation-linked transfer to Spend (the "rent's handled" moment) must
// close the loop with a "nothing for you to do" push naming the bill.
func TestNotifyTransferHandled_ObligationLinkedPushesHandledMessage(t *testing.T) {
	userID := uuid.New()
	obligationID := uuid.New()
	due := time.Now().UTC().Add(48 * time.Hour)

	transfer := &mockTransferExecutor{}
	notifier := &mockNotifier{}
	obligations := &mockObligationProvider{obligations: []entities.FinancialObligation{
		{ID: obligationID, Name: "Rent", DueDate: &due},
	}}

	svc := &Service{
		transfer:    transfer,
		notifier:    notifier,
		obligations: obligations,
		logger:      zap.NewNop(),
	}

	now := time.Now().UTC()
	cfg := StampTransferConsent(map[string]interface{}{"amount": float64(1400)}, now)
	cfgBytes, _ := json.Marshal(cfg)

	automation := &entities.MiriamAutomation{
		ID:           uuid.New(),
		UserID:       userID,
		TriggerType:  entities.TriggerObligationDue,
		ActionType:   entities.ActionTransferToSpend,
		ActionConfig: cfgBytes,
		ObligationID: &obligationID,
	}

	require.NoError(t, svc.executeAction(context.Background(), automation))

	require.Len(t, transfer.calls, 1)
	require.Len(t, notifier.pushes, 1, "obligation-linked transfer must close the loop")
	push := notifier.pushes[0]
	assert.Contains(t, push.title, "Rent")
	assert.Contains(t, push.body, "1400")
	assert.Contains(t, strings.ToLower(push.body), "nothing for you to do")
	assert.Equal(t, "transfer_handled", push.data["automation_type"])
}

// A routine savings sweep to Stash with no obligation behind it must stay
// silent — proactive pushes have to clear a "worth saying" bar.
func TestNotifyTransferHandled_RoutineSweepStaysSilent(t *testing.T) {
	transfer := &mockTransferExecutor{}
	notifier := &mockNotifier{}

	svc := &Service{
		transfer: transfer,
		notifier: notifier,
		logger:   zap.NewNop(),
	}

	now := time.Now().UTC()
	cfg := StampTransferConsent(map[string]interface{}{"amount": float64(50)}, now)
	cfgBytes, _ := json.Marshal(cfg)

	automation := &entities.MiriamAutomation{
		ID:           uuid.New(),
		UserID:       uuid.New(),
		TriggerType:  entities.TriggerSchedule,
		ActionType:   entities.ActionTransferToStash,
		ActionConfig: cfgBytes,
	}

	require.NoError(t, svc.executeAction(context.Background(), automation))

	require.Len(t, transfer.calls, 1, "the money still moves")
	assert.Empty(t, notifier.pushes, "routine sweep must not notify")
}
