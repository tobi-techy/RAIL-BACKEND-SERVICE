package platform

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

// VoiceTranscoder synthesizes Miriam's replies to speech and transcribes inbound
// voice notes. Optional — when nil, voice notes are handled as text.
type VoiceTranscoder interface {
	Available() bool
	Synthesize(ctx context.Context, text string) (audio []byte, mime string, err error)
	Transcribe(ctx context.Context, audio []byte, mime string) (string, error)
}

var handshakeTokenPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// retryableError marks a failure as transient (infrastructure) so the consumer
// requeues the delivery. Anything not wrapped this way is treated as permanent
// and dead-lettered rather than requeued forever.
type retryableError struct{ err error }

func (e *retryableError) Error() string { return e.err.Error() }
func (e *retryableError) Unwrap() error { return e.err }

// Retryable wraps a transient error so the delivery is requeued.
func Retryable(err error) error {
	if err == nil {
		return nil
	}
	return &retryableError{err: err}
}

// IsRetryable reports whether an error should cause the delivery to be requeued.
func IsRetryable(err error) bool {
	var r *retryableError
	return errors.As(err, &r)
}

type InboundMessage struct {
	Platform entities.Platform `json:"platform"`
	UserID   string            `json:"user_id"`
	ThreadID string            `json:"thread_id,omitempty"`
	Text     string            `json:"text"`
	SpaceID  string            `json:"space_id,omitempty"`
	MsgID    string            `json:"msg_id,omitempty"`

	// voice note (transcribed into Text before the AI sees it)
	IsVoice   bool   `json:"is_voice,omitempty"`
	AudioB64  string `json:"audio_b64,omitempty"`
	AudioMime string `json:"audio_mime,omitempty"`
}

// ActionPostback is a poll vote — how a user confirms/cancels an action, since
// iMessage has no tap buttons. Correlation is by (authenticated sender + space →
// conversation + the single pending action staged there); no payload token is
// needed or possible on a poll vote.
type ActionPostback struct {
	Action    string `json:"action"`     // "confirm" | "cancel" (derived from the chosen option)
	PollTitle string `json:"poll_title"` // the question voted on (logging/correlation)
	UserID    string `json:"user_id"`    // platform sender id (authenticated via resolver)
	SpaceID   string `json:"space_id"`   // thread the vote came from
	Platform  string `json:"platform"`
}

// PlatformReply is a structured response Miriam wants delivered over a platform.
// The processor turns it into the appropriate Spectrum content type(s).
type PlatformReply struct {
	Text    string          // markdown body
	Effect  string          // optional iMessage effect id (e.g. "celebration")
	Confirm *ConfirmRequest // if set, render a Confirm/Cancel poll
	OpenApp *OpenAppRequest // if set, action must be authorized in-app (fund moves)
}

// ConfirmRequest describes a Confirm/Cancel prompt rendered as a poll.
type ConfirmRequest struct {
	Summary string // the question shown as the poll title
}

// OpenAppRequest describes an action that needs in-app authorization.
type OpenAppRequest struct {
	Title string // card title
	URL   string // app deep link
}

type Orchestrator interface {
	// HandlePlatformMessage processes a user message within the thread's
	// conversation and returns a structured reply.
	HandlePlatformMessage(ctx context.Context, userID, platformIdentityID, message, threadID string, platform entities.Platform) (*PlatformReply, error)
	// ConfirmPlatformAction executes the pending action staged in the thread.
	ConfirmPlatformAction(ctx context.Context, userID, platformIdentityID, threadID string, platform entities.Platform) (*PlatformReply, error)
	// CancelPlatformAction discards the pending action staged in the thread.
	CancelPlatformAction(ctx context.Context, userID, platformIdentityID, threadID string, platform entities.Platform) (*PlatformReply, error)
}

type Processor struct {
	resolver        *UserResolver
	orchestrator    Orchestrator
	responseBuilder *ResponseBuilder
	linking         *LinkingService
	onboarder       *ChatOnboarder
	voice           VoiceTranscoder
	sendFunc        func(ctx context.Context, msg *OutboundMessage) error
}

func NewProcessor(
	resolver *UserResolver,
	orchestrator Orchestrator,
	responseBuilder *ResponseBuilder,
	linking *LinkingService,
	voice VoiceTranscoder,
	sendFunc func(ctx context.Context, msg *OutboundMessage) error,
) *Processor {
	return &Processor{
		resolver:        resolver,
		orchestrator:    orchestrator,
		responseBuilder: responseBuilder,
		linking:         linking,
		voice:           voice,
		sendFunc:        sendFunc,
	}
}

// SetOnboarder enables chat-first onboarding for unlinked senders. When unset,
// unlinked senders are asked to link from the app (legacy behavior).
func (p *Processor) SetOnboarder(o *ChatOnboarder) {
	p.onboarder = o
}

func (p *Processor) voiceEnabled() bool {
	return p.voice != nil && p.voice.Available()
}

func (p *Processor) Process(ctx context.Context, raw []byte) error {
	var msg InboundMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		log.Printf("drop unparseable inbound message: %v", err)
		return nil
	}

	// Transcribe a voice note into text before anything else sees it. If we can't,
	// tell the user rather than silently dropping.
	if msg.IsVoice {
		if !p.voiceEnabled() {
			return p.sendErrorMessage(ctx, msg, "I can't listen to voice notes just yet, but text me and I've got you.")
		}
		transcript, err := p.transcribe(ctx, msg)
		if err != nil {
			log.Printf("transcription failed for %s: %v", msg.UserID, err)
			return p.sendErrorMessage(ctx, msg, "I couldn't quite make that out. Mind sending it again, or typing it?")
		}
		if strings.TrimSpace(transcript) == "" {
			return p.sendErrorMessage(ctx, msg, "I didn't catch anything in that note. Try again?")
		}
		msg.Text = transcript
	}

	resolved, err := p.resolver.Resolve(ctx, msg.Platform, msg.UserID)
	if err == nil {
		return p.handleNormalMessage(ctx, msg, resolved)
	}

	// Unlinked sender. Chat-first onboarding takes over unless the message is a
	// handshake token for an app-first link with no onboarding already underway.
	if p.onboarder != nil {
		if p.onboarder.HasSession(ctx, msg.Platform, msg.UserID) || !handshakeTokenPattern.MatchString(msg.Text) {
			return p.handleOnboarding(ctx, msg)
		}
	}

	if handshakeTokenPattern.MatchString(msg.Text) {
		if hErr := p.tryCompleteHandshake(ctx, msg); hErr != nil {
			log.Printf("handshake completion failed for %s: %v", msg.UserID, hErr)
			if IsRetryable(hErr) {
				return hErr
			}
			return p.sendErrorMessage(ctx, msg, "That link code wasn't valid or has expired. Please try linking again from the RAIL app.")
		}
		return nil
	}

	return p.sendErrorMessage(ctx, msg, "Please link your account first. Open the RAIL app and tap 'Link iMessage'.")
}

// handleOnboarding drives one step of chat-first account creation and delivers
// the reply as a plain message (the sender has no linked identity yet).
func (p *Processor) handleOnboarding(ctx context.Context, msg InboundMessage) error {
	reply, err := p.onboarder.Handle(ctx, OnboardInput{
		Platform: msg.Platform,
		SenderID: msg.UserID,
		ThreadID: msg.ThreadID,
		Text:     msg.Text,
	})
	if err != nil {
		if IsRetryable(err) {
			return err
		}
		log.Printf("onboarding step failed for %s: %v", msg.UserID, err)
		return p.sendErrorMessage(ctx, msg, "Sorry, something went wrong. Mind trying that again?")
	}
	if reply == nil {
		return nil
	}
	return p.sendToSender(ctx, msg, reply.Text)
}

func (p *Processor) ProcessAction(ctx context.Context, raw []byte) error {
	var pb ActionPostback
	if err := json.Unmarshal(raw, &pb); err != nil {
		log.Printf("drop unparseable action postback: %v", err)
		return nil
	}

	// Authenticate the sender against the linked identity — never trust the
	// user id carried in the payload for authorization. The pending action is
	// then resolved by (user + thread), and its ownership re-checked downstream.
	resolved, err := p.resolver.Resolve(ctx, entities.Platform(pb.Platform), pb.UserID)
	if err != nil {
		log.Printf("vote from unlinked/unknown sender %s: %v", pb.UserID, err)
		return nil
	}
	railUserID := resolved.UserID.String()
	pidStr := resolved.Identity.ID.String()

	var reply *PlatformReply
	switch pb.Action {
	case "confirm":
		reply, err = p.orchestrator.ConfirmPlatformAction(ctx, railUserID, pidStr, pb.SpaceID, entities.Platform(pb.Platform))
	case "cancel":
		reply, err = p.orchestrator.CancelPlatformAction(ctx, railUserID, pidStr, pb.SpaceID, entities.Platform(pb.Platform))
	default:
		log.Printf("drop unknown vote verb: %q", pb.Action)
		return nil
	}
	if err != nil {
		log.Printf("resolve vote %s for %s: %v", pb.Action, railUserID, err)
		p.sendPlainTo(ctx, resolved.Identity, pb.SpaceID, friendlyActionError(err))
		return nil
	}

	return p.deliverReply(ctx, resolved.Identity, pb.SpaceID, "", reply, false)
}

func (p *Processor) tryCompleteHandshake(ctx context.Context, msg InboundMessage) error {
	// Bind the identity to the ACTUAL sender captured from the inbound message.
	identity, err := p.linking.ConfirmHandshake(ctx, msg.Text, msg.Platform, msg.UserID)
	if err != nil {
		return err
	}
	out := p.responseBuilder.EffectResponse(identity,
		"Your iMessage is now linked to RAIL! Ask me about your balances, spending, savings — anything.",
		msg.ThreadID, EffectCelebration)
	if err := p.sendFunc(ctx, out); err != nil {
		return Retryable(fmt.Errorf("send handshake confirmation: %w", err))
	}
	return nil
}

func (p *Processor) handleNormalMessage(ctx context.Context, msg InboundMessage, resolved *ResolvedUser) error {
	// Show a typing indicator immediately for a responsive feel.
	if err := p.sendFunc(ctx, p.responseBuilder.TypingResponse(resolved.Identity, msg.ThreadID)); err != nil {
		log.Printf("typing indicator send failed (non-fatal): %v", err)
	}

	reply, err := p.orchestrator.HandlePlatformMessage(ctx, resolved.UserID.String(), resolved.Identity.ID.String(), msg.Text, msg.ThreadID, msg.Platform)
	if err != nil {
		// Don't requeue: re-running the orchestrator would double-bill AI usage.
		log.Printf("orchestrator error for %s: %v", resolved.UserID, err)
		return p.sendErrorMessage(ctx, msg, "Sorry, something went wrong on my end. Mind trying that again?")
	}

	return p.deliverReply(ctx, resolved.Identity, msg.ThreadID, msg.MsgID, reply, msg.IsVoice)
}

// deliverReply renders a PlatformReply into the appropriate Spectrum content type
// and sends it. replyTo threads under the user's message; voiceReply asks for a
// spoken reply (used when the user sent a voice note — Miriam mirrors modality).
func (p *Processor) deliverReply(ctx context.Context, identity *entities.PlatformIdentity, threadID, replyTo string, reply *PlatformReply, voiceReply bool) error {
	if reply == nil {
		return nil
	}

	// Interactive prompts stay visual — a poll or app card can't be a voice note.
	switch {
	case reply.Confirm != nil:
		// iMessage has no buttons — render a Confirm/Cancel poll. The vote comes
		// back as an inbound poll_option we correlate by sender + thread.
		title := reply.Confirm.Summary
		if title == "" {
			title = reply.Text
		}
		return p.send(ctx, p.responseBuilder.PollResponse(identity, title, threadID, []string{"Confirm", "Cancel"}))
	case reply.OpenApp != nil:
		return p.send(ctx, p.responseBuilder.AppCardResponse(identity, reply.Text, threadID, replyTo, reply.OpenApp.Title, reply.OpenApp.URL))
	}

	// Mirror modality: a voice note in gets a voice note back when TTS is available.
	if voiceReply && p.voiceEnabled() && strings.TrimSpace(reply.Text) != "" {
		if out, err := p.synthesizeVoice(ctx, identity, threadID, reply.Text); err == nil {
			return p.send(ctx, out)
		} else {
			log.Printf("voice synthesis failed, falling back to text: %v", err)
		}
	}

	var out *OutboundMessage
	switch {
	case reply.Effect != "":
		out = p.responseBuilder.EffectResponse(identity, reply.Text, threadID, reply.Effect)
	case replyTo != "":
		out = p.responseBuilder.ReplyResponse(identity, reply.Text, threadID, replyTo)
	default:
		out = p.responseBuilder.MarkdownResponse(identity, reply.Text, threadID)
	}
	return p.send(ctx, out)
}

func (p *Processor) send(ctx context.Context, out *OutboundMessage) error {
	if err := p.sendFunc(ctx, out); err != nil {
		return Retryable(fmt.Errorf("send reply: %w", err))
	}
	return nil
}

func (p *Processor) transcribe(ctx context.Context, msg InboundMessage) (string, error) {
	audio, err := base64.StdEncoding.DecodeString(msg.AudioB64)
	if err != nil {
		return "", fmt.Errorf("decode audio: %w", err)
	}
	if len(audio) == 0 {
		return "", fmt.Errorf("empty audio")
	}
	return p.voice.Transcribe(ctx, audio, msg.AudioMime)
}

func (p *Processor) synthesizeVoice(ctx context.Context, identity *entities.PlatformIdentity, threadID, text string) (*OutboundMessage, error) {
	audio, mime, err := p.voice.Synthesize(ctx, text)
	if err != nil {
		return nil, err
	}
	b64 := base64.StdEncoding.EncodeToString(audio)
	return p.responseBuilder.VoiceResponse(identity, threadID, b64, mime, estimateDurationSec(text)), nil
}

// estimateDurationSec approximates a voice note's length from its text (~160 wpm)
// for the waveform UI. Optional metadata; a rough value is fine.
func estimateDurationSec(text string) int {
	words := len(strings.Fields(text))
	d := words * 10 / 27
	if d < 1 {
		d = 1
	}
	return d
}

func (p *Processor) sendPlainTo(ctx context.Context, identity *entities.PlatformIdentity, threadID, text string) {
	out := p.responseBuilder.MarkdownResponse(identity, text, threadID)
	if err := p.sendFunc(ctx, out); err != nil {
		log.Printf("failed to send message: %v", err)
	}
}

func (p *Processor) sendErrorMessage(ctx context.Context, msg InboundMessage, text string) error {
	return p.sendToSender(ctx, msg, text)
}

// sendToSender delivers a plain text message to a sender by platform id, used
// before a linked identity exists (errors and onboarding prompts).
func (p *Processor) sendToSender(ctx context.Context, msg InboundMessage, text string) error {
	out := &OutboundMessage{
		Platform: msg.Platform,
		UserID:   msg.UserID,
		ThreadID: msg.ThreadID,
		Text:     text,
	}
	if err := p.sendFunc(ctx, out); err != nil {
		return Retryable(fmt.Errorf("send message: %w", err))
	}
	return nil
}

// friendlyActionError converts an execution error into user-facing copy without
// leaking internals. The orchestrator prefixes timeouts with "action_expired:".
func friendlyActionError(err error) string {
	msg := err.Error()
	if idx := len("action_expired:"); len(msg) >= idx && msg[:idx] == "action_expired:" {
		return "That request timed out. Just ask me again and I'll set it up fresh."
	}
	return "I couldn't complete that just now. Please try again in a moment."
}
