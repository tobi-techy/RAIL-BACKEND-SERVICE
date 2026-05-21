package ai

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestExecuteVoiceMoneyActionRejectsMalformedParams(t *testing.T) {
	o := &Orchestrator{logger: zap.NewNop()}

	for _, params := range []interface{}{"invalid", []interface{}{"invalid"}, nil} {
		result, err := o.executeVoiceMoneyAction(context.Background(), uuid.New(), map[string]interface{}{
			"action": ToolIgnoreSubscription,
			"params": params,
		})

		require.NoError(t, err)
		require.Equal(t, "params must be an object", result["error"])
	}
}

func TestExecuteVoiceMoneyActionFallsBackOnlyWhenParamsAbsent(t *testing.T) {
	o := &Orchestrator{logger: zap.NewNop()}

	result, err := o.executeVoiceMoneyAction(context.Background(), uuid.New(), map[string]interface{}{
		"action":          ToolIgnoreSubscription,
		"subscription_id": "sub_123",
	})

	require.NoError(t, err)
	require.Equal(t, true, result["success"])
}
