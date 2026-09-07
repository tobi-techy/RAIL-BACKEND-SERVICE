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
	"go.uber.org/zap"
)

// VoiceTranscoder synthesizes Miriam's replies to speech and transcribes inbound
// voice notes. Optional — when nil, voice notes are handled as text.
type VoiceTranscoder interface {
	Available() bool
	Synthesize(ctx context.Context, text string) (audio []byte, mime string, err error)
	Transcribe(ctx context.Context, audio []byte, mime string) (string, error)
}

var handshakeTokenPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

const maxStatementAttachmentBytes = 4 * 1024 * 1024

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

	// image attachment (e.g. a receipt photo — OCR'd and summarized before the
	// AI sees it, so she can log or split it)
	IsImage   bool   `json:"is_image,omitempty"`
	ImageB64  string `json:"image_b64,omitempty"`
	ImageMime string `json:"image_mime,omitempty"`

	// bank statement document attachment. Raw bytes are decoded only for the
	// current request and are never persisted in onboarding state or prompts.
	IsDocument   bool   `json:"is_document,omitempty"`
	DocumentB64  string `json:"document_b64,omitempty"`
	DocumentMime string `json:"document_mime,omitempty"`
	DocumentName string `json:"document_name,omitempty"`

	// iMessage contact card / vCard. Used during chat-first onboarding so the
	// user can skip typing name/phone/email.
	IsContact bool           `json:"is_contact,omitempty"`
	VCardText string         `json:"vcard_text,omitempty"`
	Contact   *SharedContact `json:"contact,omitempty"`

	// Poll vote: the selected option title arrives in Text. The bridge marks it
	// so a stray vote with no pending action is dropped instead of confusing
	// the orchestrator.
	IsPollVote bool `json:"is_poll_vote,omitempty"`

	// Tapback reaction on one of our messages. Affirmative reactions confirm a
	// staged action; anything else is dropped.
	IsReaction    bool   `json:"is_reaction,omitempty"`
	ReactionEmoji string `json:"reaction_emoji,omitempty"`

	// Threaded reply context: the user long-pressed one of our messages and
	// replied. ReplyToText (when the bridge could resolve the target) is folded
	// into the message so Miriam can see what "that" refers to.
	ReplyTo     string `json:"reply_to,omitempty"`
	ReplyToText string `json:"reply_to_text,omitempty"`

	// EditOf marks an edited message; the new text arrives in Text.
	EditOf string `json:"edit_of,omitempty"`

	// Delivery attempt counters from the bridge's signed body. Attempt is
	// 1-based. Both are zero on older bridges, which IsFinalAttempt reads as
	// "no redelivery is coming".
	Attempt     int `json:"attempt,omitempty"`
	MaxAttempts int `json:"max_attempts,omitempty"`
}

// IsFinalAttempt reports whether the bridge is out of redeliveries for this
// message. Handlers that can requeue a transient failure use it to decide
// between staying silent for the retry and answering the user now.
func (m InboundMessage) IsFinalAttempt() bool {
	if m.Attempt <= 0 || m.MaxAttempts <= 0 {
		return true
	}
	return m.Attempt >= m.MaxAttempts
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
	Text    string                 // markdown body
	Effect  string                 // optional iMessage effect id (e.g. "celebration")
	Confirm *ConfirmRequest        // if set, render a Confirm/Cancel poll
	Poll    *PollRequest           // if set, render a custom-option poll (onboarding, send_poll)
	OpenApp *OpenAppRequest        // if set, action must be authorized in-app (fund moves)
	Cards   []entities.InsightCard // structured insight cards to render after the text
}

// ConfirmRequest describes a Confirm/Cancel prompt rendered as a poll.
type ConfirmRequest struct {
	Summary string // the question shown as the poll title
}

// PollRequest is a tappable multi-choice prompt whose selected option is posted
// back as ordinary inbound text (the option title).
type PollRequest struct {
	Title   string
	Options []string
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
	// HasPendingPlatformAction reports whether the thread currently has a staged
	// pending action. Used to interpret bare YES/NO replies as confirm/cancel on
	// platforms without interactive polls (Telegram, WhatsApp).
	HasPendingPlatformAction(ctx context.Context, userID, platformIdentityID, threadID string, platform entities.Platform) bool
}

type Processor struct {
	resolver         *UserResolver
	orchestrator     Orchestrator
	responseBuilder  *ResponseBuilder
	linking          *LinkingService
	onboarder        *ChatOnboarder
	babyStepsSeeder  BabyStepsSeeder
	voice            VoiceTranscoder
	vision           ReceiptVision
	statementHandler StatementAttachmentHandler
	sendFunc         func(ctx context.Context, msg *OutboundMessage) error
	logger           *zap.Logger
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
		logger:          zap.NewNop(),
	}
}

// SetLogger replaces the no-op logger with the real one. Wired from DI.
func (p *Processor) SetLogger(l *zap.Logger) {
	if l == nil {
		return
	}
	p.logger = l
}

// SetReceiptVision enables receipt-photo understanding. When unset, images get
// a graceful "I can't look at photos yet" reply.
func (p *Processor) SetReceiptVision(v ReceiptVision) {
	p.vision = v
}

// SetStatementAttachmentHandler enables PDF statement handling for linked and
// unlinked platform senders.
func (p *Processor) SetStatementAttachmentHandler(h StatementAttachmentHandler) {
	p.statementHandler = h
	if p.onboarder != nil {
		p.onboarder.SetStatementAttachmentHandler(h)
	}
}

func (p *Processor) visionEnabled() bool {
	return p.vision != nil && p.vision.Available()
}

// SetOnboarder enables chat-first onboarding for unlinked senders. When unset,
// unlinked senders are asked to link from the app (legacy behavior).
func (p *Processor) SetOnboarder(o *ChatOnboarder) {
	p.onboarder = o
}

// SetBabyStepsSeeder installs the first-login goal seeder. Called after a
// successful handshake so a freshly-linked iMessage/WhatsApp/Telegram user
// gets the 7-step Baby Steps ladder materialized into user_goals. Nil-safe:
// a nil seeder is a no-op (legacy behavior).
func (p *Processor) SetBabyStepsSeeder(s BabyStepsSeeder) {
	p.babyStepsSeeder = s
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

	// OCR a receipt/photo attachment into a quick summary before anything else
	// sees it. The summary becomes the message text so the resolver, onboarder,
	// and orchestrator all handle it like a normal turn.
	if msg.IsImage {
		if !p.visionEnabled() {
			return p.sendErrorMessage(ctx, msg, "I can't look at photos just yet, but tell me the merchant and total and I'll log or split it for you.")
		}
		summary, err := p.summarizeImage(ctx, msg)
		if err != nil {
			log.Printf("image OCR failed for %s: %v", msg.UserID, err)
			return p.sendErrorMessage(ctx, msg, "I couldn't read that photo. Try a clearer, top-down shot with the total visible.")
		}
		if strings.TrimSpace(summary) == "" {
			return p.sendErrorMessage(ctx, msg, "I couldn't make out a receipt in that photo. Try a clearer shot?")
		}
		msg.Text = summary
	}

	var statementAttachment *StatementAttachment
	if msg.IsDocument {
		attachment, err := decodeStatementAttachment(msg)
		if err != nil {
			return p.sendErrorMessage(ctx, msg, err.Error())
		}
		statementAttachment = &attachment
	}

	resolved, err := p.resolver.Resolve(ctx, msg.Platform, msg.UserID)
	if err == nil {
		if statementAttachment != nil {
			if p.statementHandler == nil {
				return p.sendErrorMessage(ctx, msg, "I can't scan statements from chat just yet. Please upload it in the RAIL app.")
			}
			reply, handlerErr := p.statementHandler.EnqueueLinked(ctx, resolved.UserID, *statementAttachment)
			if handlerErr != nil {
				p.logger.Warn("linked statement enqueue failed", zap.Error(handlerErr))
				_ = p.sendErrorMessage(ctx, msg, "I couldn't start that statement scan just now. Please try sending it again.")
				return Retryable(handlerErr)
			}
			return p.deliverReply(ctx, resolved.Identity, msg.ThreadID, msg.MsgID, reply, false)
		}
		if isContactPayload(msg) && strings.TrimSpace(msg.Text) == "" {
			p.sendPlainTo(ctx, resolved.Identity, msg.ThreadID, "You're already linked — I don't need the card. What do you want to look at?")
			return nil
		}
		if msg.IsReaction {
			return p.handleReaction(ctx, msg, resolved)
		}
		if msg.IsPollVote && !p.orchestrator.HasPendingPlatformAction(ctx, resolved.UserID.String(), resolved.Identity.ID.String(), msg.ThreadID, msg.Platform) {
			// A vote on an old poll (or the onboarding consent poll from a now-
			// linked sender) with nothing staged — feeding bare "Confirm" into
			// the model would only confuse it.
			p.logger.Debug("dropping stray poll vote with no pending action",
				zap.String("thread_id", msg.ThreadID), zap.String("text", msg.Text))
			return nil
		}
		return p.handleNormalMessage(ctx, msg, resolved)
	}

	// Unlinked sender. A handshake token always takes precedence — even if the
	// sender is in the middle of an onboarding conversation. This prevents the
	// token from being swallowed as a name/country/email reply.
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

	if statementAttachment != nil && p.statementHandler == nil {
		return p.sendErrorMessage(ctx, msg, "I can't scan statements from chat just yet. Please upload the PDF in the RAIL app.")
	}

	// No handshake token: chat-first onboarding takes over if enabled.
	if p.onboarder != nil {
		return p.handleOnboarding(ctx, msg)
	}

	linkHint := "Link iMessage"
	switch msg.Platform {
	case entities.PlatformTelegram:
		linkHint = "Link Telegram"
	case entities.PlatformWhatsApp:
		linkHint = "Link WhatsApp"
	}
	return p.sendErrorMessage(ctx, msg, "Please link your account first. Open the RAIL app and tap '"+linkHint+"'.")
}

// handleOnboarding drives one step of chat-first account creation and delivers
// the reply as a plain message (the sender has no linked identity yet).
func isContactPayload(msg InboundMessage) bool {
	return msg.IsContact || strings.TrimSpace(msg.VCardText) != "" || msg.Contact != nil
}

func (p *Processor) handleOnboarding(ctx context.Context, msg InboundMessage) error {
	contact := msg.Contact
	if contact == nil && strings.TrimSpace(msg.VCardText) != "" {
		parsed := ParseVCard(msg.VCardText)
		contact = &parsed
	}
	reply, err := p.onboarder.Handle(ctx, OnboardInput{
		Platform:      msg.Platform,
		SenderID:      msg.UserID,
		ThreadID:      msg.ThreadID,
		Text:          msg.Text,
		Contact:       contact,
		Statement:     statementAttachmentFromMessage(msg),
		Redeliverable: !msg.IsFinalAttempt(),
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
	return p.sendOnboardingReply(ctx, msg, reply)
}

func decodeStatementAttachment(msg InboundMessage) (StatementAttachment, error) {
	if !strings.EqualFold(strings.TrimSpace(msg.DocumentMime), "application/pdf") ||
		!strings.HasSuffix(strings.ToLower(strings.TrimSpace(msg.DocumentName)), ".pdf") {
		return StatementAttachment{}, fmt.Errorf("I can scan PDF statements from chat. Please send a PDF file.")
	}
	data, err := base64.StdEncoding.DecodeString(msg.DocumentB64)
	if err != nil {
		return StatementAttachment{}, fmt.Errorf("I couldn't read that PDF. Please send it again.")
	}
	if len(data) == 0 {
		return StatementAttachment{}, fmt.Errorf("That PDF was empty. Please send the statement again.")
	}
	if len(data) > maxStatementAttachmentBytes {
		return StatementAttachment{}, fmt.Errorf("That PDF is too large for chat scanning. Please upload it in the RAIL app.")
	}
	return StatementAttachment{
		Name:     strings.TrimSpace(msg.DocumentName),
		MIMEType: "application/pdf",
		Data:     data,
	}, nil
}

func statementAttachmentFromMessage(msg InboundMessage) *StatementAttachment {
	if !msg.IsDocument {
		return nil
	}
	attachment, err := decodeStatementAttachment(msg)
	if err != nil {
		return nil
	}
	return &attachment
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
	// If the sender was in the middle of chat onboarding, drop it so they don't
	// get stuck in a half-finished onboarding conversation after linking.
	if p.onboarder != nil {
		if err := p.onboarder.ClearSession(ctx, msg.Platform, msg.UserID); err != nil {
			log.Printf("failed to clear onboarding session for %s: %v", msg.UserID, err)
		}
	}
	// Fire the first-login goal seeder so a freshly-linked user gets the 7-step
	// Baby Steps ladder materialized into user_goals. Async + recover so a
	// failure here can't break the link confirmation.
	SeedBabyStepsOnLink(p.babyStepsSeeder, identity.UserID, p.logger)
	out := p.responseBuilder.EffectResponse(identity,
		"Your iMessage is now linked to RAIL! Ask me about your balances, spending, savings — anything.",
		msg.ThreadID, EffectCelebration)
	if err := p.sendFunc(ctx, out); err != nil {
		return Retryable(fmt.Errorf("send handshake confirmation: %w", err))
	}
	return nil
}

// affirmativeVote / negativeVote are the bare replies treated as confirm/cancel
// on platforms without interactive polls. Kept deliberately short and
// unambiguous so ordinary chat never triggers them accidentally.
var affirmativeVote = map[string]bool{
	"yes": true, "y": true, "confirm": true, "ok": true, "okay": true, "sure": true,
}
var negativeVote = map[string]bool{
	"no": true, "n": true, "cancel": true, "nope": true, "stop": true,
}

// affirmativeTapback / negativeTapback map iMessage reactions to confirm/cancel
// when an action is staged. Anything else (or no staged action) is dropped —
// a random tapback on an old message should never reach the model.
var affirmativeTapback = map[string]bool{"❤️": true, "👍": true, "💯": true, "🙌": true, "yes": true}
var negativeTapback = map[string]bool{"👎": true, "no": true}

// handleReaction treats a tapback as a vote on the staged action in the thread,
// mirroring the bare YES/NO text fallback for poll-less platforms.
func (p *Processor) handleReaction(ctx context.Context, msg InboundMessage, resolved *ResolvedUser) error {
	railUserID := resolved.UserID.String()
	pidStr := resolved.Identity.ID.String()
	if !p.orchestrator.HasPendingPlatformAction(ctx, railUserID, pidStr, msg.ThreadID, msg.Platform) {
		return nil
	}
	var reply *PlatformReply
	var err error
	switch {
	case affirmativeTapback[msg.ReactionEmoji]:
		reply, err = p.orchestrator.ConfirmPlatformAction(ctx, railUserID, pidStr, msg.ThreadID, msg.Platform)
	case negativeTapback[msg.ReactionEmoji]:
		reply, err = p.orchestrator.CancelPlatformAction(ctx, railUserID, pidStr, msg.ThreadID, msg.Platform)
	default:
		return nil
	}
	if err != nil {
		log.Printf("reaction-vote %q failed for %s: %v", msg.ReactionEmoji, railUserID, err)
		p.sendPlainTo(ctx, resolved.Identity, msg.ThreadID, friendlyActionError(err))
		return nil
	}
	return p.deliverReply(ctx, resolved.Identity, msg.ThreadID, "", reply, false)
}

func (p *Processor) handleNormalMessage(ctx context.Context, msg InboundMessage, resolved *ResolvedUser) error {
	// Text-vote fallback: on platforms without polls, a bare YES/NO (or similar)
	// while a pending action is staged is treated as confirm/cancel. Guarded by
	// HasPendingPlatformAction so ordinary "yes" chatter never fires it.
	if !msg.IsVoice && !msg.IsImage {
		normalized := strings.ToLower(strings.TrimSpace(msg.Text))
		railUserID := resolved.UserID.String()
		pidStr := resolved.Identity.ID.String()
		if (affirmativeVote[normalized] || negativeVote[normalized]) &&
			p.orchestrator.HasPendingPlatformAction(ctx, railUserID, pidStr, msg.ThreadID, msg.Platform) {
			var reply *PlatformReply
			var err error
			if affirmativeVote[normalized] {
				reply, err = p.orchestrator.ConfirmPlatformAction(ctx, railUserID, pidStr, msg.ThreadID, msg.Platform)
			} else {
				reply, err = p.orchestrator.CancelPlatformAction(ctx, railUserID, pidStr, msg.ThreadID, msg.Platform)
			}
			if err != nil {
				log.Printf("text-vote %q failed for %s: %v", normalized, railUserID, err)
				p.sendPlainTo(ctx, resolved.Identity, msg.ThreadID, friendlyActionError(err))
				return nil
			}
			return p.deliverReply(ctx, resolved.Identity, msg.ThreadID, msg.MsgID, reply, false)
		}
	}

	// Show a typing indicator immediately for a responsive feel.
	if err := p.sendFunc(ctx, p.responseBuilder.TypingResponse(resolved.Identity, msg.ThreadID)); err != nil {
		log.Printf("typing indicator send failed (non-fatal): %v", err)
	}

	// A threaded reply carries its quoted message so "that one" / "yes, that"
	// resolves to what the user actually long-pressed.
	text := msg.Text
	if quoted := strings.TrimSpace(msg.ReplyToText); quoted != "" {
		if len(quoted) > 200 {
			quoted = quoted[:200]
		}
		text = fmt.Sprintf("[replying to your earlier message: %q]\n%s", quoted, msg.Text)
	}

	reply, err := p.orchestrator.HandlePlatformMessage(ctx, resolved.UserID.String(), resolved.Identity.ID.String(), text, msg.ThreadID, msg.Platform)
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
	case reply.Poll != nil && len(reply.Poll.Options) > 0:
		title := reply.Poll.Title
		if title == "" {
			title = reply.Text
		}
		return p.send(ctx, p.responseBuilder.PollResponse(identity, title, threadID, reply.Poll.Options))
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

	// Structured cards ride along in the same atomic outbound message: the bridge
	// sends the text, then renders each card per-platform. Cards are best-effort
	// enhancement — if the reply also asks for a poll/app hand-off we keep those
	// (they short-circuit above) and only attach cards to plain replies.
	if len(reply.Cards) > 0 && reply.Confirm == nil && reply.OpenApp == nil {
		out = p.responseBuilder.CardsResponse(identity, reply.Text, threadID, reply.Cards)
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

func (p *Processor) summarizeImage(ctx context.Context, msg InboundMessage) (string, error) {
	image, err := base64.StdEncoding.DecodeString(msg.ImageB64)
	if err != nil {
		return "", fmt.Errorf("decode image: %w", err)
	}
	if len(image) == 0 {
		return "", fmt.Errorf("empty image")
	}
	return p.vision.SummarizeReceipt(ctx, image, msg.ImageMime)
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
		Platform:    msg.Platform,
		UserID:      msg.UserID,
		ThreadID:    msg.ThreadID,
		Text:        text,
		ContentType: ContentTypeText,
	}
	if err := p.sendFunc(ctx, out); err != nil {
		return Retryable(fmt.Errorf("send message: %w", err))
	}
	return nil
}

// sendOnboardingReply delivers a structured onboarder reply (text, poll, effect)
// to an unlinked sender.
func (p *Processor) sendOnboardingReply(ctx context.Context, msg InboundMessage, reply *PlatformReply) error {
	if reply == nil {
		return nil
	}
	if reply.Poll != nil && len(reply.Poll.Options) > 0 {
		title := reply.Poll.Title
		if title == "" {
			title = reply.Text
		}
		out := &OutboundMessage{
			Platform:    msg.Platform,
			UserID:      msg.UserID,
			ThreadID:    msg.ThreadID,
			Text:        reply.Text,
			ContentType: ContentTypePoll,
			PollTitle:   title,
			PollOptions: reply.Poll.Options,
		}
		return p.send(ctx, out)
	}
	if reply.Effect != "" {
		out := &OutboundMessage{
			Platform:    msg.Platform,
			UserID:      msg.UserID,
			ThreadID:    msg.ThreadID,
			Text:        reply.Text,
			ContentType: ContentTypeEffect,
			Effect:      reply.Effect,
		}
		return p.send(ctx, out)
	}
	return p.sendToSender(ctx, msg, reply.Text)
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
