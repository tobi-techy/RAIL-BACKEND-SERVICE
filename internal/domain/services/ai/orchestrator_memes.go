package ai

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/google/uuid"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
)

// ToolSendMeme lets Miriam drop a relevant, freshly-generated meme into the chat,
// the way a real person texts a meme to make a point land. Memes are rendered on the
// fly by memegen.link (https://memegen.link) — Miriam picks a classic template and
// writes the top/bottom text so the joke fits the conversation. The meme rides the
// existing "cards" channel as an InsightCard of type "meme"; the app renders it as an
// image bubble.
const ToolSendMeme = "send_meme"

// memegenBaseURLEnv overrides the meme image host (default: the public memegen API).
// Self-host memegen for full brand control by pointing this at your instance.
const memegenBaseURLEnv = "MIRIAM_MEMEGEN_BASE_URL"

const defaultMemegenBase = "https://api.memegen.link"

// memeTemplate is one entry in Miriam's curated set of meme templates. The list is
// intentionally small and built from widely-recognized, brand-safe formats so the
// generated memes always read as "real memes" rather than noise.
type memeTemplate struct {
	ID            string // memegen.link template id
	Vibe          string // when the model should reach for this one (shown in the tool schema)
	DefaultTop    string // fallback top text if the model omits it
	DefaultBottom string // fallback bottom text if the model omits it
	Sentiment     string // "positive" | "negative" | "neutral" — drives bubble accent
}

// memeTemplates: confident, well-known memegen template ids paired with the money-
// conversation moment they fit. Keep curated — quality over count.
var memeTemplates = []memeTemplate{
	{ID: "drake", Vibe: "Rejecting one option for a better one — 'don't do X, do Y'. Top = the bad move, bottom = the smart move.", DefaultTop: "spending it all", DefaultBottom: "stashing 30%", Sentiment: "positive"},
	{ID: "success", Vibe: "A small win nailed perfectly — hit a goal, saved on time, stuck to budget.", DefaultTop: "didn't touch stash", DefaultBottom: "all week", Sentiment: "positive"},
	{ID: "doge", Vibe: "Wholesome hype over growth — 'such savings, very stash, wow'.", DefaultTop: "such savings", DefaultBottom: "very growth", Sentiment: "positive"},
	{ID: "grumpycat", Vibe: "Dry, sarcastic unimpressed reaction to a questionable spend.", DefaultTop: "you saved money", DefaultBottom: "good. now stop", Sentiment: "neutral"},
	{ID: "mordor", Vibe: "Caution — 'one does not simply' do the risky money thing.", DefaultTop: "one does not simply", DefaultBottom: "spend their whole salary in week 1", Sentiment: "neutral"},
	{ID: "rollsafe", Vibe: "Smart-guy logic, playful — 'can't overspend if you don't have the app open'.", DefaultTop: "can't overspend", DefaultBottom: "if you move it to stash first", Sentiment: "positive"},
	{ID: "fry", Vibe: "Suspicion / 'not sure if X or Y'.", DefaultTop: "not sure if broke", DefaultBottom: "or just disciplined", Sentiment: "neutral"},
	{ID: "buzz", Vibe: "'X, X everywhere' — when something is showing up a lot in their spending.", DefaultTop: "food deliveries", DefaultBottom: "food deliveries everywhere", Sentiment: "neutral"},
	{ID: "aag", Vibe: "'I'm not saying it's X, but...' playful over-explanation (Ancient Aliens guy).", DefaultTop: "i'm not saying you're rich", DefaultBottom: "but the stash is talking", Sentiment: "positive"},
	{ID: "oprah", Vibe: "'You get an X, everybody gets an X' — abundance / everything's covered.", DefaultTop: "you get a stash", DefaultBottom: "everybody gets a stash", Sentiment: "positive"},
	{ID: "pooh", Vibe: "The fancy / refined version of a basic habit — 'saving' vs 'building generational wealth'.", DefaultTop: "saving money", DefaultBottom: "letting it earn yield", Sentiment: "positive"},
	{ID: "pigeon", Vibe: "'Is this a budget?' — mislabeling something obvious, gently teasing.", DefaultTop: "", DefaultBottom: "is this a budget?", Sentiment: "neutral"},
	{ID: "disastergirl", Vibe: "Mischievous 'oops' energy after a small money L.", DefaultTop: "me", DefaultBottom: "after one impulse buy", Sentiment: "negative"},
	{ID: "fine", Vibe: "'This is fine' — coping with a thin balance, light and self-aware.", DefaultTop: "balance is low", DefaultBottom: "this is fine", Sentiment: "negative"},
}

var memeTemplateByID = func() map[string]memeTemplate {
	m := make(map[string]memeTemplate, len(memeTemplates))
	for _, t := range memeTemplates {
		m[t.ID] = t
	}
	return m
}()

// SendMemeTool returns the LLM tool definition for sending a meme.
func SendMemeTool() infraai.Tool {
	ids := make([]string, 0, len(memeTemplates))
	var desc strings.Builder
	desc.WriteString("Send a freshly-generated meme to the user, like texting a friend a meme. Let the conversation's context decide — memes work for celebrating, commiserating over a rough week, playfully roasting an impulse buy, hyping them up, or coping with a thin balance. Full emotional range, not just happy moments. Don't overuse it (roughly one in a few replies feels natural), and never joke during genuine crisis or distress. Pick the template whose vibe fits, then write SHORT top_text and bottom_text that reference THIS conversation so the joke actually hits. Templates:\n")
	for _, t := range memeTemplates {
		ids = append(ids, t.ID)
		desc.WriteString("- ")
		desc.WriteString(t.ID)
		desc.WriteString(": ")
		desc.WriteString(t.Vibe)
		desc.WriteString("\n")
	}

	return infraai.Tool{
		Name:        ToolSendMeme,
		Description: desc.String(),
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"template": map[string]interface{}{
					"type":        "string",
					"enum":        ids,
					"description": "Which meme template to use. Choose the one whose vibe fits the moment.",
				},
				"top_text": map[string]interface{}{
					"type":        "string",
					"description": "Short top-line text (a few words). Reference the actual conversation. Can be empty.",
				},
				"bottom_text": map[string]interface{}{
					"type":        "string",
					"description": "Short bottom-line text (a few words). Reference the actual conversation. Can be empty.",
				},
				"caption": map[string]interface{}{
					"type":        "string",
					"description": "Optional one-line caption shown under the meme in Miriam's voice.",
				},
			},
			"required":             []string{"template"},
			"additionalProperties": false,
		},
	}
}

// ─── Voice Message Tool ──────────────────────────────────────────────────────

// ToolSendVoiceMessage lets Miriam occasionally send a short voice message
// (< 15 words) for genuinely special moments: first savings milestone, a big
// win, or a warm check-in. The audio is generated via ElevenLabs TTS on the
// backend and surfaced as a "voice_message" InsightCard.
//
// Requires env vars: ELEVENLABS_API_KEY, ELEVENLABS_VOICE_ID.
// When not configured the tool still fires but the card's audio_url will be
// empty, causing the frontend VoiceMessageBubble to render the caption as text.
const ToolSendVoiceMessage = "send_voice_message"

func SendVoiceMessageTool() infraai.Tool {
	return infraai.Tool{
		Name:        ToolSendVoiceMessage,
		Description: `Send Miriam's voice as a short audio message for very special moments ONLY — a savings milestone, a first big win, a heartfelt check-in. Maximum 15 words. Never use for routine info. Never use more than once per conversation. The audio is generated from the text and plays inline in the chat.`,
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"text": map[string]interface{}{
					"type":        "string",
					"description": "The words Miriam will say. Maximum 15 words. Warm, personal, spoken — not written.",
				},
			},
			"required":             []string{"text"},
			"additionalProperties": false,
		},
	}
}

func (o *AgentAdapter) executeSendVoiceMessage(ctx context.Context, _ uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	text := strings.TrimSpace(stringArg(args, "text"))
	if text == "" {
		return map[string]interface{}{"error": "empty text"}, nil
	}
	// Truncate to a safe spoken length
	words := strings.Fields(text)
	if len(words) > 15 {
		words = words[:15]
		text = strings.Join(words, " ")
	}

	result := map[string]interface{}{
		"text":             text,
		"caption":          text,
		"duration_seconds": nil,
		"audio_url":        "",
	}

	// ElevenLabs TTS — graceful no-op when not configured.
	// When ELEVENLABS_API_KEY + ELEVENLABS_VOICE_ID are set the audio is fetched
	// and stored; set the audio_url field so the frontend can play it.
	// TODO: integrate R2 upload for persistent audio hosting.
	apiKey := strings.TrimSpace(os.Getenv("ELEVENLABS_API_KEY"))
	voiceID := strings.TrimSpace(os.Getenv("ELEVENLABS_VOICE_ID"))
	if apiKey != "" && voiceID != "" {
		if audioURL, err := generateElevenLabsTTS(ctx, apiKey, voiceID, text); err == nil {
			result["audio_url"] = audioURL
		}
	}

	return result, nil
}

// generateElevenLabsTTS calls the ElevenLabs streaming TTS API and returns
// a data URL (base64 mp3) suitable for immediate playback via expo-audio.
func generateElevenLabsTTS(ctx context.Context, apiKey, voiceID, text string) (string, error) {
	body := fmt.Sprintf(`{"text":%q,"model_id":"eleven_multilingual_v2","voice_settings":{"stability":0.5,"similarity_boost":0.75}}`, text)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.elevenlabs.io/v1/text-to-speech/"+voiceID,
		strings.NewReader(body),
	)
	if err != nil {
		return "", err
	}
	req.Header.Set("xi-api-key", apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "audio/mpeg")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("elevenlabs tts: status %d", resp.StatusCode)
	}
	audio, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return "data:audio/mpeg;base64," + base64.StdEncoding.EncodeToString(audio), nil
}

func (o *AgentAdapter) executeSendMeme(_ context.Context, _ uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	id := strings.TrimSpace(strings.ToLower(stringArg(args, "template")))
	tmpl, ok := memeTemplateByID[id]
	if !ok {
		tmpl = memeTemplates[0] // safe, recognizable default
	}

	top := strings.TrimSpace(stringArg(args, "top_text"))
	bottom := strings.TrimSpace(stringArg(args, "bottom_text"))
	if top == "" && bottom == "" {
		top, bottom = tmpl.DefaultTop, tmpl.DefaultBottom
	}

	caption := strings.TrimSpace(stringArg(args, "caption"))

	return map[string]interface{}{
		"meme_id":     tmpl.ID,
		"template":    tmpl.ID,
		"top_text":    top,
		"bottom_text": bottom,
		"caption":     caption,
		"sentiment":   tmpl.Sentiment,
		"image_url":   buildMemegenURL(tmpl.ID, top, bottom),
		"alt":         strings.TrimSpace(top + " " + bottom),
	}, nil
}

// buildMemegenURL constructs a memegen.link image URL:
//
//	{base}/images/{template}/{top}/{bottom}.png
//
// memegen has its own path-segment escaping rules (spaces -> "_", "?" -> "~q", etc.).
func buildMemegenURL(template, top, bottom string) string {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv(memegenBaseURLEnv)), "/")
	if base == "" {
		base = defaultMemegenBase
	}
	// memegenEscape first (its special tokens use only URL-safe chars), then
	// PathEscape so any unicode (e.g. ₦) is percent-encoded for a valid URL.
	top = url.PathEscape(memegenEscape(top))
	bottom = url.PathEscape(memegenEscape(bottom))
	if top == "" {
		top = "_"
	}
	if bottom == "" {
		bottom = "_"
	}
	return base + "/images/" + url.PathEscape(template) + "/" + top + "/" + bottom + ".png"
}

// memegenEscape applies memegen.link's special-character encoding for path segments.
// See https://memegen.link/ — order matters (escape "~" first, then the rest).
func memegenEscape(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	replacer := strings.NewReplacer(
		"~", "~t",
		"_", "__",
		"-", "--",
		" ", "_",
		"?", "~q",
		"&", "~a",
		"%", "~p",
		"#", "~h",
		"/", "~s",
		"\\", "~b",
		"<", "~l",
		">", "~g",
		"\"", "''",
		"\n", "~n",
	)
	return replacer.Replace(s)
}
