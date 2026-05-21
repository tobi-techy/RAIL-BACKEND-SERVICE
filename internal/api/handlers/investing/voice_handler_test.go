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
	event := normalizeClientVoiceEvent(websocket.TextMessage, []byte(`{"type":"input_audio_buffer.append","audio":"abc123"}`))

	audio, ok := event.(infraai.InputAudio)
	require.True(t, ok)
	require.Equal(t, "input.audio", audio.Type)
	require.Equal(t, "abc123", audio.Audio)
}

func TestNormalizeClientVoiceEventWrapsBinaryAudio(t *testing.T) {
	event := normalizeClientVoiceEvent(websocket.BinaryMessage, []byte{0x01, 0x02, 0x03})

	audio, ok := event.(infraai.InputAudio)
	require.True(t, ok)
	require.Equal(t, "input.audio", audio.Type)
	require.Equal(t, base64.StdEncoding.EncodeToString([]byte{0x01, 0x02, 0x03}), audio.Audio)
}

func TestNormalizeClientVoiceEventPreservesAssemblyAIEvent(t *testing.T) {
	raw := []byte(`{"type":"input.audio","audio":"abc123"}`)
	event := normalizeClientVoiceEvent(websocket.TextMessage, raw)

	msg, ok := event.(json.RawMessage)
	require.True(t, ok)
	require.JSONEq(t, string(raw), string(msg))
}

func TestVoiceToolDescriptionsStaySmallAndActionCapable(t *testing.T) {
	tools := voiceToolDescriptions()

	require.Len(t, tools, 12)
	require.Contains(t, tools, "transfer_funds")
	require.Contains(t, tools, "create_automation")
	require.Contains(t, tools, "get_money_flow")
	require.Contains(t, tools, "get_financial_health")
	require.Contains(t, tools, "get_financial_audit")
	require.NotContains(t, tools, "get_card_transactions")
}
