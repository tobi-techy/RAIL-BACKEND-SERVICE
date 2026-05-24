package investing

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/gorilla/websocket"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/stretchr/testify/require"
)

func TestNormalizeClientVoiceEventConvertsOpenAIAudioAppend(t *testing.T) {
	event, audioBytes, ok := normalizeClientVoiceEvent(websocket.TextMessage, []byte(`{"type":"input_audio_buffer.append","audio":"abc123"}`))

	require.True(t, ok)
	require.Equal(t, 3, audioBytes)
	audio, ok := event.(infraai.InputAudio)
	require.True(t, ok)
	require.Equal(t, "input.audio", audio.Type)
	require.Equal(t, "abc123", audio.Audio)
}

func TestNormalizeClientVoiceEventWrapsBinaryAudio(t *testing.T) {
	event, audioBytes, ok := normalizeClientVoiceEvent(websocket.BinaryMessage, []byte{0x01, 0x02, 0x03})

	require.True(t, ok)
	require.Equal(t, 3, audioBytes)
	audio, ok := event.(infraai.InputAudio)
	require.True(t, ok)
	require.Equal(t, "input.audio", audio.Type)
	require.Equal(t, base64.StdEncoding.EncodeToString([]byte{0x01, 0x02, 0x03}), audio.Audio)
}

func TestNormalizeClientVoiceEventPreservesAssemblyAIEvent(t *testing.T) {
	raw := []byte(`{"type":"input.audio","audio":"abc123"}`)
	event, audioBytes, ok := normalizeClientVoiceEvent(websocket.TextMessage, raw)

	require.True(t, ok)
	require.Equal(t, 3, audioBytes)
	msg, ok := event.(json.RawMessage)
	require.True(t, ok)
	require.JSONEq(t, string(raw), string(msg))
}

func TestNormalizeClientVoiceEventDropsOpenAIControlEvents(t *testing.T) {
	event, audioBytes, ok := normalizeClientVoiceEvent(websocket.TextMessage, []byte(`{"type":"response.create"}`))

	require.False(t, ok)
	require.Nil(t, event)
	require.Zero(t, audioBytes)
}

func TestVoiceAudioDurationHelpers(t *testing.T) {
	require.Equal(t, 4, decodedBase64Len("AQIDBA=="))
	require.Equal(t, 1000, pcm16DurationMS(48000, voiceSampleRateHz))
}

func TestVoiceToolDescriptionsStaySmallAndActionCapable(t *testing.T) {
	tools := voiceToolDescriptions()

	require.Len(t, tools, 19)
	require.Contains(t, tools, "voice_money_lookup")
	require.Contains(t, tools, "voice_money_action")
	require.Contains(t, tools, "transfer_funds")
	require.NotContains(t, tools, "initiate_withdrawal")
	require.Contains(t, tools, "create_automation")
	require.Contains(t, tools, "get_money_flow")
	require.Contains(t, tools, "get_budget")
	require.Contains(t, tools, "set_budget")
	require.Contains(t, tools, "get_financial_health")
	require.Contains(t, tools, "get_financial_audit")
	require.Contains(t, tools, "get_miriam_money_state")
	require.Contains(t, tools, "list_miriam_mandates")
	require.Contains(t, tools, "get_miriam_decision_receipts")
	require.NotContains(t, tools, "get_card_transactions")
	require.NotContains(t, tools, "get_deposit_history")
}
