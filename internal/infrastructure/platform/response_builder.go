package platform

import (
	"encoding/json"
	"fmt"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

// ContentType selects how the bridge renders an outbound message. Each value maps
// to a spectrum-ts content builder (see cmd/spectrum-bridge/src/handler.ts).
type ContentType string

const (
	ContentTypeText     ContentType = "text"     // plain text() / string
	ContentTypeMarkdown ContentType = "markdown" // markdown()
	ContentTypeReply    ContentType = "reply"    // reply() threaded under a message
	ContentTypeTyping   ContentType = "typing"   // typing() indicator
	ContentTypeEffect   ContentType = "effect"   // iMessage effect() wrapping markdown
	ContentTypeAppCard  ContentType = "appcard"  // app() tappable app-url card
	ContentTypeRichLink ContentType = "richlink" // richlink() Open Graph preview
	ContentTypePoll     ContentType = "poll"     // poll() — Confirm/Cancel prompt
	ContentTypeVoice    ContentType = "voice"    // voice() — spoken note (TTS)
)

// iMessage message-effect ids supported by spectrum-ts (imessage.effect.message.*).
// Unknown values fall back to a plain send on the bridge.
const (
	EffectCelebration = "celebration"
	EffectConfetti    = "confetti"
	EffectFireworks   = "fireworks"
	EffectBalloons    = "balloons"
	EffectHeart       = "heart"
	EffectLasers      = "lasers"
	EffectSparkles    = "sparkles"
	EffectSpotlight   = "spotlight"
	EffectEcho        = "echo"
)

// OutboundMessage is the wire contract with the bridge. Fields are populated per
// ContentType; unused fields are omitted.
type OutboundMessage struct {
	Platform entities.Platform `json:"platform"`
	UserID   string            `json:"user_id"`
	ThreadID string            `json:"thread_id"`
	Text     string            `json:"text"`

	ContentType ContentType `json:"content_type,omitempty"`

	// reply
	ReplyTo string `json:"reply_to,omitempty"`

	// effect (iMessage only — Spectrum ignores on other platforms)
	Effect string `json:"effect,omitempty"`

	// app card / rich link
	CardTitle string `json:"card_title,omitempty"`
	CardURL   string `json:"card_url,omitempty"`

	// poll (Confirm / Cancel prompt)
	PollTitle   string   `json:"poll_title,omitempty"`
	PollOptions []string `json:"poll_options,omitempty"`

	// voice note (base64 audio synthesized via TTS)
	AudioB64    string `json:"audio_b64,omitempty"`
	AudioMime   string `json:"audio_mime,omitempty"`
	DurationSec int    `json:"duration_sec,omitempty"`
}

type ResponseBuilder struct{}

func NewResponseBuilder() *ResponseBuilder {
	return &ResponseBuilder{}
}

func (b *ResponseBuilder) base(identity *entities.PlatformIdentity, threadID string) *OutboundMessage {
	return &OutboundMessage{
		Platform: identity.Platform,
		UserID:   identity.PlatformUserID,
		ThreadID: threadID,
	}
}

func (b *ResponseBuilder) TextResponse(identity *entities.PlatformIdentity, text string) *OutboundMessage {
	m := b.base(identity, "")
	m.Text = text
	m.ContentType = ContentTypeText
	return m
}

func (b *ResponseBuilder) MarkdownResponse(identity *entities.PlatformIdentity, text, threadID string) *OutboundMessage {
	m := b.base(identity, threadID)
	m.Text = text
	m.ContentType = ContentTypeMarkdown
	return m
}

func (b *ResponseBuilder) ReplyResponse(identity *entities.PlatformIdentity, text, threadID, replyTo string) *OutboundMessage {
	m := b.base(identity, threadID)
	m.Text = text
	m.ContentType = ContentTypeReply
	m.ReplyTo = replyTo
	return m
}

func (b *ResponseBuilder) TypingResponse(identity *entities.PlatformIdentity, threadID string) *OutboundMessage {
	m := b.base(identity, threadID)
	m.ContentType = ContentTypeTyping
	return m
}

func (b *ResponseBuilder) EffectResponse(identity *entities.PlatformIdentity, text, threadID, effect string) *OutboundMessage {
	m := b.base(identity, threadID)
	m.Text = text
	m.ContentType = ContentTypeEffect
	m.Effect = effect
	return m
}

// AppCardResponse renders a tappable app-style card (appLinkCards) — used to send
// the user into the RAIL app to authorize a fund-moving action with Face ID.
func (b *ResponseBuilder) AppCardResponse(identity *entities.PlatformIdentity, text, threadID, replyTo, title, url string) *OutboundMessage {
	m := b.base(identity, threadID)
	m.Text = text
	m.ContentType = ContentTypeAppCard
	m.ReplyTo = replyTo
	m.CardTitle = title
	m.CardURL = url
	return m
}

// RichLinkResponse renders a URL as a rich Open Graph preview (richLinks).
func (b *ResponseBuilder) RichLinkResponse(identity *entities.PlatformIdentity, text, threadID, url string) *OutboundMessage {
	m := b.base(identity, threadID)
	m.Text = text
	m.ContentType = ContentTypeRichLink
	m.CardURL = url
	return m
}

// VoiceResponse renders a spoken note (voice()) from synthesized audio.
func (b *ResponseBuilder) VoiceResponse(identity *entities.PlatformIdentity, threadID, audioB64, mime string, durationSec int) *OutboundMessage {
	m := b.base(identity, threadID)
	m.ContentType = ContentTypeVoice
	m.AudioB64 = audioB64
	m.AudioMime = mime
	m.DurationSec = durationSec
	return m
}

// PollResponse renders a Confirm/Cancel poll (poll()). The user's vote returns
// as an inbound poll_option the processor correlates by sender + thread.
func (b *ResponseBuilder) PollResponse(identity *entities.PlatformIdentity, title, threadID string, options []string) *OutboundMessage {
	m := b.base(identity, threadID)
	m.ContentType = ContentTypePoll
	m.PollTitle = title
	m.PollOptions = options
	return m
}

func (b *ResponseBuilder) JSON(msg *OutboundMessage) ([]byte, error) {
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("marshal outbound: %w", err)
	}
	return data, nil
}
