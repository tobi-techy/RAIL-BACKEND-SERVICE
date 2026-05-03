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
