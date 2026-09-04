package ai

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsFundMovingAction_IncludesCoreAndExecutionTools(t *testing.T) {
	for _, name := range []string{
		"transfer_funds", "initiate_withdrawal", "execute_investment",
		"optimize_yield", "copy_trader", "setup_bill_autopay", "pay_bill",
		"automate_bill", "send_money", "split_receipt", "create_automation",
		"book_flight", "request_flight_refund", "send_to_bank", "send_crypto",
	} {
		assert.True(t, IsFundMovingAction(name), "%q should be fund-moving", name)
	}
	for _, name := range []string{"get_account_summary", "list_automations", "pause_automation", "delete_automation"} {
		assert.False(t, IsFundMovingAction(name), "%q should not be fund-moving", name)
	}
}

func TestParticipantsFromParams(t *testing.T) {
	assert.Equal(t, []string{"@john", "jane@x.com"}, participantsFromParams("@john, jane@x.com"))
	assert.Equal(t, []string{"@a", "@b"}, participantsFromParams([]interface{}{"@a", "@b"}))
	assert.Equal(t, []string{"@a"}, participantsFromParams([]string{"@a", "  "}))
	assert.Empty(t, participantsFromParams(nil))
}
