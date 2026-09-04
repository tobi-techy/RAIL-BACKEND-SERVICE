package ai

import (
	"testing"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

func TestConversationalTransferDescription(t *testing.T) {
	amount := decimal.NewFromFloat(50)
	preview := &entities.EmergencyWithdrawalPreviewResponse{FeeAmount: decimal.NewFromFloat(2.5), NetAmount: decimal.NewFromFloat(47.5)}

	assert.Equal(t, "Move $50.00 from spend to stash", conversationalTransferDescription("spend", "stash", amount, nil))
	assert.Equal(t, "Move $50.00 from stash to spend", conversationalTransferDescription("stash", "spend", amount, nil))
	assert.Equal(t, "Early withdrawal: move $50.00 from stash to spend (fee $2.50, net $47.50)", conversationalTransferDescription("stash", "spend", amount, preview))
}

func TestConversationalGoalDescription(t *testing.T) {
	target := decimal.NewFromFloat(1000)
	assert.Equal(t, "Set savings goal 'Trip' for $1000.00", conversationalGoalDescription("Trip", target, ""))
	assert.Equal(t, "Set savings goal 'Trip' for $1000.00 by 2026-12-01", conversationalGoalDescription("Trip", target, "2026-12-01"))
}

func TestPostActionFollowUp(t *testing.T) {
	assert.Equal(t, "Want me to set up the same move automatically next time?", postActionFollowUp("transfer", map[string]interface{}{"amount": "50"}))
	assert.Equal(t, "Want me to auto-save toward 'Trip' every week?", postActionFollowUp("goal", map[string]interface{}{"name": "Trip"}))
	assert.Equal(t, "", postActionFollowUp("unknown", nil))
}

func TestActionConfirmationMessage(t *testing.T) {
	action := &entities.PendingAction{
		Action:            ToolTransferFunds,
		SuggestedFollowUp: "Want me to set up the same move automatically next time?",
	}
	assert.Contains(t, actionConfirmationMessage(action), "I'll move the money once you approve in-app")

	goalAction := &entities.PendingAction{
		Action:            ToolSetSavingsGoal,
		SuggestedFollowUp: "Want me to auto-save toward this every week?",
		Params:            map[string]interface{}{"name": "Emergency Fund"},
	}
	assert.Contains(t, actionConfirmationMessage(goalAction), "Emergency Fund")
}
