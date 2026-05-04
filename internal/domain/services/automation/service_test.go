package automation

import (
	"testing"
	"time"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/stretchr/testify/require"
)

func TestValidateCreateRequestRequiresConsentForTransferAutomation(t *testing.T) {
	req := &CreateAutomationRequest{
		Name:          "Monthly stash",
		TriggerType:   entities.TriggerSchedule,
		TriggerConfig: map[string]interface{}{"hour": float64(9)},
		ActionType:    entities.ActionTransferToStash,
		ActionConfig:  map[string]interface{}{"amount": float64(100)},
	}

	err := validateCreateRequest(req)
	require.Error(t, err)
	require.Contains(t, err.Error(), "acknowledged_future_transfer")

	StampTransferConsent(req.ActionConfig, time.Now().UTC())
	require.NoError(t, validateCreateRequest(req))
}

func TestValidateCreateRequestCapsTransferAutomationAmount(t *testing.T) {
	req := &CreateAutomationRequest{
		Name:          "Large stash",
		TriggerType:   entities.TriggerSchedule,
		TriggerConfig: map[string]interface{}{"hour": float64(9)},
		ActionType:    entities.ActionTransferToStash,
		ActionConfig: map[string]interface{}{
			"amount":                       float64(10001),
			"acknowledged_future_transfer": true,
			"passcode_session_verified_at": time.Now().UTC().Format(time.RFC3339),
			"reauthorization_due_at":       time.Now().UTC().Add(transferAutomationReauthorizationWindow).Format(time.RFC3339),
		},
	}

	err := validateCreateRequest(req)
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds maximum")
}

func TestEnsureTransferReauthorizationExpiresAfterWindow(t *testing.T) {
	now := time.Now().UTC()
	config := StampTransferConsent(map[string]interface{}{"amount": float64(100)}, now.Add(-transferAutomationReauthorizationWindow-2*time.Hour))

	err := ensureTransferReauthorization(config, now)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrTransferAutomationReauthorizationRequired)
}

func TestValidateCreateRequestAllowsNotifyAutomationWithoutTransferConsent(t *testing.T) {
	req := &CreateAutomationRequest{
		Name:          "Bill reminder",
		TriggerType:   entities.TriggerSchedule,
		TriggerConfig: map[string]interface{}{"hour": float64(9)},
		ActionType:    entities.ActionNotify,
		ActionConfig:  map[string]interface{}{"message": "Rent due"},
	}

	require.NoError(t, validateCreateRequest(req))
}

func TestValidateCreateRequestAcceptsNewTriggerTypes(t *testing.T) {
	tests := []struct {
		name        string
		triggerType string
		actionType  string
		actionCfg   map[string]interface{}
	}{
		{"obligation_due + notify", entities.TriggerObligationDue, entities.ActionNotify, map[string]interface{}{"message": "Bill due soon"}},
		{"life_event + notify", entities.TriggerLifeEvent, entities.ActionNotify, map[string]interface{}{"message": "Income changed"}},
		{"spending_spike + pause_card_cooldown", entities.TriggerSpendingSpike, entities.ActionPauseCardCooldown, map[string]interface{}{"cooldown_minutes": float64(30)}},
		{"balance_threshold + pause_card", entities.TriggerBalanceThreshold, entities.ActionPauseCard, map[string]interface{}{}},
		{"schedule + resume_card", entities.TriggerSchedule, entities.ActionResumeCard, map[string]interface{}{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &CreateAutomationRequest{
				Name:          "Test automation",
				TriggerType:   tt.triggerType,
				TriggerConfig: map[string]interface{}{"hour": float64(9)},
				ActionType:    tt.actionType,
				ActionConfig:  tt.actionCfg,
			}
			require.NoError(t, validateCreateRequest(req))
		})
	}
}

func TestValidateCreateRequestRejectsInvalidTriggerType(t *testing.T) {
	req := &CreateAutomationRequest{
		Name:          "Bad trigger",
		TriggerType:   "nonexistent",
		TriggerConfig: map[string]interface{}{},
		ActionType:    entities.ActionNotify,
		ActionConfig:  map[string]interface{}{"message": "test"},
	}
	err := validateCreateRequest(req)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported trigger type")
}

func TestValidateCreateRequestRejectsInvalidActionType(t *testing.T) {
	req := &CreateAutomationRequest{
		Name:          "Bad action",
		TriggerType:   entities.TriggerSchedule,
		TriggerConfig: map[string]interface{}{"hour": float64(9)},
		ActionType:    "nonexistent",
		ActionConfig:  map[string]interface{}{},
	}
	err := validateCreateRequest(req)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported action type")
}

func TestValidateCreateRequestTransferToSpendRequiresConsent(t *testing.T) {
	req := &CreateAutomationRequest{
		Name:          "Stash to spend",
		TriggerType:   entities.TriggerBalanceThreshold,
		TriggerConfig: map[string]interface{}{"wallet": "stash", "operator": "above", "threshold": float64(1000)},
		ActionType:    entities.ActionTransferToSpend,
		ActionConfig:  map[string]interface{}{"amount": float64(50)},
	}
	err := validateCreateRequest(req)
	require.Error(t, err)
	require.Contains(t, err.Error(), "acknowledged_future_transfer")

	StampTransferConsent(req.ActionConfig, time.Now().UTC())
	require.NoError(t, validateCreateRequest(req))
}

func TestValidateCreateRequestPauseCardCooldownNoAmountRequired(t *testing.T) {
	req := &CreateAutomationRequest{
		Name:          "Spike cooldown",
		TriggerType:   entities.TriggerSpendingSpike,
		TriggerConfig: map[string]interface{}{},
		ActionType:    entities.ActionPauseCardCooldown,
		ActionConfig:  map[string]interface{}{"cooldown_minutes": float64(30), "message": "Spending spike detected"},
	}
	require.NoError(t, validateCreateRequest(req))
}

func TestStampTransferConsentSetsAllFields(t *testing.T) {
	now := time.Now().UTC()
	cfg := StampTransferConsent(nil, now)

	require.Equal(t, true, cfg["acknowledged_future_transfer"])
	require.NotEmpty(t, cfg["passcode_session_verified_at"])
	require.NotEmpty(t, cfg["reauthorization_due_at"])
	require.Equal(t, 90, cfg["reauthorization_window_days"])
}

func TestAutomationPushDataIncludesDeepLinkFields(t *testing.T) {
	data := automationPushData("bill_shield", "Bill Shield just moved $50 to cover my bills.")

	require.Equal(t, "automation", data["type"])
	require.Equal(t, "ai-chat", data["screen"])
	require.Equal(t, "bill_shield", data["automation_type"])
	require.Equal(t, "Bill Shield just moved $50 to cover my bills.", data["preloaded_message"])
}
