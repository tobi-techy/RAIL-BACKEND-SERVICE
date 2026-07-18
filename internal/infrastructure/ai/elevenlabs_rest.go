package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"

	"go.uber.org/zap"
)

const (
	elevenLabsRESTBase = "https://api.elevenlabs.io/v1"
	elTTSModel         = "eleven_multilingual_v2"
	elSTTModel         = "scribe_v1"
	elTTSOutputFormat  = "mp3_44100_128"
	elVoiceMimeType    = "audio/mpeg"
)

// ElevenLabsREST is a thin REST client for ElevenLabs text-to-speech and
// speech-to-text, used to let Miriam send and read voice notes over messaging.
type ElevenLabsREST struct {
	apiKey   string
	voiceID  string
	settings elVoiceSettings
	http     *http.Client
	logger   *zap.Logger
}

type elVoiceSettings struct {
	Stability       float64 `json:"stability"`
	SimilarityBoost float64 `json:"similarity_boost"`
	Style           float64 `json:"style"`
	UseSpeakerBoost bool    `json:"use_speaker_boost"`
}

// ELVoiceConfig configures the REST client (sourced from config.ElevenLabsConfig).
type ELVoiceConfig struct {
	APIKey          string
	VoiceID         string
	Stability       float64
	SimilarityBoost float64
	Style           float64
	UseSpeakerBoost bool
}

func NewElevenLabsREST(cfg ELVoiceConfig, logger *zap.Logger) *ElevenLabsREST {
	return &ElevenLabsREST{
		apiKey:  cfg.APIKey,
		voiceID: cfg.VoiceID,
		settings: elVoiceSettings{
			Stability:       cfg.Stability,
			SimilarityBoost: cfg.SimilarityBoost,
			Style:           cfg.Style,
			UseSpeakerBoost: cfg.UseSpeakerBoost,
		},
		http:   &http.Client{Timeout: 30 * time.Second},
		logger: logger,
	}
}

// Available reports whether the client is configured to make calls.
func (c *ElevenLabsREST) Available() bool {
	return c != nil && c.apiKey != "" && c.voiceID != ""
}

// TextToSpeech synthesizes speech from text and returns MP3 bytes.
func (c *ElevenLabsREST) TextToSpeech(ctx context.Context, text string) ([]byte, string, error) {
	body, err := json.Marshal(map[string]interface{}{
		"text":           text,
		"model_id":       elTTSModel,
		"voice_settings": c.settings,
	})
	if err != nil {
		return nil, "", err
	}

	url := fmt.Sprintf("%s/text-to-speech/%s?output_format=%s", elevenLabsRESTBase, c.voiceID, elTTSOutputFormat)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("xi-api-key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", elVoiceMimeType)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("elevenlabs tts: %w", err)
	}
	defer resp.Body.Close()

	audio, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20)) // 10 MB cap
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("elevenlabs tts status %d: %s", resp.StatusCode, truncate(string(audio), 300))
	}
	return audio, elVoiceMimeType, nil
}

// SpeechToText transcribes an audio clip and returns the recognized text.
func (c *ElevenLabsREST) SpeechToText(ctx context.Context, audio []byte, mimeType string) (string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	part, err := w.CreateFormFile("file", "note"+extensionForMime(mimeType))
	if err != nil {
		return "", err
	}
	if _, err := part.Write(audio); err != nil {
		return "", err
	}
	if err := w.WriteField("model_id", elSTTModel); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, elevenLabsRESTBase+"/speech-to-text", &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("xi-api-key", c.apiKey)
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("elevenlabs stt: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("elevenlabs stt status %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}

	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("parse stt response: %w", err)
	}
	return out.Text, nil
}

func extensionForMime(mimeType string) string {
	switch mimeType {
	case "audio/mp4", "audio/x-m4a", "audio/m4a":
		return ".m4a"
	case "audio/mpeg":
		return ".mp3"
	case "audio/wav", "audio/x-wav":
		return ".wav"
	case "audio/ogg":
		return ".ogg"
	default:
		return ".m4a"
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
