package ai

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsFundMovingAction_IncludesP2PAndSplit(t *testing.T) {
	assert.True(t, IsFundMovingAction("send_money"))
	assert.True(t, IsFundMovingAction(ToolSplitReceipt))
	assert.True(t, IsFundMovingAction(ToolPayBill))
	assert.True(t, IsFundMovingAction(ToolTransferFunds))
	assert.True(t, IsFundMovingAction("send_to_bank"))
	assert.True(t, IsFundMovingAction("send_crypto"))
	assert.False(t, IsFundMovingAction("get_account_summary"))
	assert.False(t, IsFundMovingAction("list_automations"))
	assert.False(t, IsFundMovingAction("pause_automation"))
	assert.False(t, IsFundMovingAction("delete_automation"))
}

func TestParticipantsFromParams(t *testing.T) {
	assert.Equal(t, []string{"@john", "jane@x.com"}, participantsFromParams("@john, jane@x.com"))
	assert.Equal(t, []string{"@a", "@b"}, participantsFromParams([]interface{}{"@a", "@b"}))
	assert.Equal(t, []string{"@a"}, participantsFromParams([]string{"@a", "  "}))
	assert.Empty(t, participantsFromParams(nil))
}
