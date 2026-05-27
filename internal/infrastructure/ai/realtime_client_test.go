package ai

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewSessionUpdateUsesConfiguredGreetingAndVoice(t *testing.T) {
	update := NewSessionUpdate("premium prompt", "ivy", "Hey Tobi. Miriam here.", []SessionTool{
		{Name: "get_account_summary", Description: "Get balances"},
	})

	require.Equal(t, "session.update", update.Type)
	require.Equal(t, "premium prompt", update.Session.SystemPrompt)
	require.Equal(t, "Hey Tobi. Miriam here.", update.Session.Greeting)
	require.Equal(t, "ivy", update.Session.Output.Voice)
	require.Equal(t, 100, update.Session.Output.Volume)
	require.NotNil(t, update.Session.Input.TurnDetection)
	require.Equal(t, 0.5, update.Session.Input.TurnDetection.VadThreshold)
	require.Equal(t, 1400, update.Session.Input.TurnDetection.MinSilence)
	require.Equal(t, 4000, update.Session.Input.TurnDetection.MaxSilence)
	require.NotNil(t, update.Session.Input.TurnDetection.InterruptResponse)
	require.True(t, *update.Session.Input.TurnDetection.InterruptResponse)
	require.Len(t, update.Session.Tools, 1)
}

func TestTurnDetectionUpdatesUseVoiceDefaults(t *testing.T) {
	baseline := NewBaselineTurnDetectionUpdate()
	require.Equal(t, "session.update", baseline.Type)
	require.Equal(t, 1400, baseline.Session.Input.TurnDetection.MinSilence)
	require.Equal(t, 4000, baseline.Session.Input.TurnDetection.MaxSilence)
	require.True(t, *baseline.Session.Input.TurnDetection.InterruptResponse)

	question := NewQuestionTurnDetectionUpdate()
	require.Equal(t, "session.update", question.Type)
	require.Equal(t, 2200, question.Session.Input.TurnDetection.MinSilence)
	require.Equal(t, 6000, question.Session.Input.TurnDetection.MaxSilence)
	require.True(t, *question.Session.Input.TurnDetection.InterruptResponse)
}

func TestNewReplyCreate(t *testing.T) {
	reply := NewReplyCreate("Say this out loud.")

	require.Equal(t, "reply.create", reply.Type)
	require.Equal(t, "Say this out loud.", reply.Instructions)
}
