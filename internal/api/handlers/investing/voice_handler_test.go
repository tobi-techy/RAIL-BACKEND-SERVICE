package investing

import (
	"encoding/base64"
	"testing"

	"github.com/gorilla/websocket"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/stretchr/testify/require"
)

func TestNormalizeClientVoiceEventConvertsOpenAIAudioAppend(t *testing.T) {
	event, audioBytes, ok := normalizeClientVoiceEvent(websocket.TextMessage, []byte(`{"type":"input_audio_buffer.append","audio":"abc123"}`))

	require.True(t, ok)
	require.Equal(t, 3, audioBytes)
	audio, ok := event.(infraai.ELAudioChunk)
	require.True(t, ok)
	require.Equal(t, "abc123", audio.UserAudioChunk)
}

func TestNormalizeClientVoiceEventWrapsBinaryAudio(t *testing.T) {
	event, audioBytes, ok := normalizeClientVoiceEvent(websocket.BinaryMessage, []byte{0x01, 0x02, 0x03})

	require.True(t, ok)
	require.Equal(t, 3, audioBytes)
	audio, ok := event.(infraai.ELAudioChunk)
	require.True(t, ok)
	require.Equal(t, base64.StdEncoding.EncodeToString([]byte{0x01, 0x02, 0x03}), audio.UserAudioChunk)
}

func TestNormalizeClientVoiceEventPreservesInputAudio(t *testing.T) {
	raw := []byte(`{"type":"input.audio","audio":"abc123"}`)
	event, audioBytes, ok := normalizeClientVoiceEvent(websocket.TextMessage, raw)

	require.True(t, ok)
	require.Equal(t, 3, audioBytes)
	audio, ok := event.(infraai.ELAudioChunk)
	require.True(t, ok)
	require.Equal(t, "abc123", audio.UserAudioChunk)
}

func TestNormalizeClientVoiceEventDropsOpenAIControlEvents(t *testing.T) {
	event, audioBytes, ok := normalizeClientVoiceEvent(websocket.TextMessage, []byte(`{"type":"response.create"}`))

	require.False(t, ok)
	require.Nil(t, event)
	require.Zero(t, audioBytes)
}

func TestNormalizeClientVoiceEventDropsPingAndReturnsNil(t *testing.T) {
	event, audioBytes, ok := normalizeClientVoiceEvent(websocket.TextMessage, []byte(`{"type":"ping"}`))

	require.False(t, ok)
	require.Nil(t, event)
	require.Zero(t, audioBytes)
}

func TestVoiceAudioDurationHelpers(t *testing.T) {
	require.Equal(t, 4, decodedBase64Len("AQIDBA=="))
	require.Equal(t, 1000, pcm16DurationMS(48000, voiceSampleRateHz))
}


