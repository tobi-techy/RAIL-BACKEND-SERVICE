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
	require.Len(t, update.Session.Tools, 1)
}
