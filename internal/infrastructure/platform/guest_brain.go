package platform

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"go.uber.org/zap"
)

// GuestMessage is one turn of the pre-signup conversation. Role is "user" or
// "assistant".
type GuestMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// GuestToolDef describes a tool the guest model may call. Mirrors the
// infrastructure ai.Tool shape so the DI adapter can convert trivially, but
// keeps the platform package free of that dependency.
type GuestToolDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// GuestToolCall is a tool invocation returned by the model.
type GuestToolCall struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// GuestResult is one completion from the guest model: the reply text plus any
// tool calls it wants executed.
type GuestResult struct {
	Text      string
	ToolCalls []GuestToolCall
}

// GuestCompleter is the minimal LLM surface the guest brain needs. Satisfied
// by an adapter over ai.AIProvider, wired in DI. When nil, the onboarder runs
// its deterministic fallback flow.
type GuestCompleter interface {
	CompleteGuest(ctx context.Context, systemPrompt string, messages []GuestMessage, tools []GuestToolDef) (*GuestResult, error)
}

// CompletionBudget is optionally implemented by a GuestCompleter whose single
// turn needs more than guestCompletionTimeout — for example a Python brain that
// runs a full agent turn (agent loop plus model call) rather than one bare model
// completion. A custom budget trades the default two-short-attempts for one
// longer, careful attempt so the whole turn still fits the bridge deadline; the
// adapter is expected to own its own retries/fallback at that point.
type CompletionBudget interface {
	CompletionTimeout() time.Duration
}

// guestCompletionTimeout bounds a single completion attempt. Two attempts plus
// the retry gap must stay under the bridge's 15s inbound POST timeout, or the
// bridge gives up on a turn we are about to answer.
const guestCompletionTimeout = 6 * time.Second

// guestCompletionAttempts is how many times one completion is tried before the
// turn is declared transiently failed. Provider blips on this path cost the
// user a visible apology, so one retry is worth the latency.
const guestCompletionAttempts = 2

// guestRetryGap is the pause between completion attempts.
const guestRetryGap = 400 * time.Millisecond

// errGuestNoReply means the model produced tool calls but never any reply text,
// even after the follow-up pass. It is transient in the same sense a provider
// blip is: the same input usually yields text on the next try.
var errGuestNoReply = errors.New("guest model returned no reply text")

// isTransientGuestErr reports whether a failed turn is worth retrying rather
// than apologising for. Every completion failure qualifies — the guest
// conversation has no side effects to unwind, so a retry is always safe.
func isTransientGuestErr(err error) bool {
	return err != nil && !errors.Is(err, errNoGuestCompleter)
}

// errNoGuestCompleter means the guest brain has no provider wired at all. That
// is a configuration state, not a blip, so retrying it is pointless.
var errNoGuestCompleter = errors.New("no guest completer configured")

// guestTools are the only tools the guest model can call. The deterministic
// executor in onboarding.go owns the effects (slot writes, phase transitions);
// the model never touches identity verification directly.
var guestTools = []GuestToolDef{
	{
		Name: "note_detail",
		Description: "Silently record something learned about the person. Call it the moment they reveal " +
			"their first name, country, money goal, email, or the moment you have a read on their money style. " +
			"Never announce it to them.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"field": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"first_name", "country", "goal", "money_type", "money_dial", "email"},
					"description": "Which detail to record. money_type is your silent read: avoider, optimizer, worrier, or dreamer. money_dial is what they love spending on, in their words.",
				},
				"value": map[string]interface{}{
					"type":        "string",
					"description": "The value in their words (goal) or normalized (name, country, email, money_type).",
				},
			},
			"required": []string{"field", "value"},
		},
	},
	{
		Name: "start_signup",
		Description: "Open the Rail account and wallet after you have shown them one true thing about their money. " +
			"Call it in the same reply as that aha. Do not wait for a deposit, transfer, or bill. Do not call it " +
			"before the picture. Linking their bank needs no account. Your reply is the aha, then the account ask. " +
			"Never ask for a phone number.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"reason": map[string]interface{}{
					"type":        "string",
					"description": "The thing you just showed them about their money, in a few words.",
				},
			},
			"required": []string{"reason"},
		},
	},
	{
		Name: "connect_bank",
		Description: "Do not call. Bank linking is not available. If they agree to show their spending, " +
			"ask them to send a PDF of a recent bank statement. No account is needed for that.",
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
	{
		Name: "get_bank_statement_analysis",
		Description: "Fetch their linked bank's real spending picture. Call it the moment the state block says " +
			"mono_linked: true, then deliver the aha: one category, one comparison to income, one question. " +
			"Never dump an audit.",
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
	{
		Name: "send_poll",
		Description: "Attach a tappable choice to your reply. At most once per conversation, only when a choice " +
			"genuinely moves things forward. 3-4 concrete options.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"question": map[string]interface{}{"type": "string"},
				"options": map[string]interface{}{
					"type":  "array",
					"items": map[string]interface{}{"type": "string"},
				},
			},
			"required": []string{"question", "options"},
		},
	},
	{
		Name: "send_reaction",
		Description: "Tap back on their last message with one emoji: ❤️ (love), 👍 (like), 👎 (dislike), " +
			"😂 (laugh), ‼️ (emphasize), or ❓ (question). One reaction at most per reply, and only " +
			"as punctuation for what you already said, never instead of it.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"emoji": map[string]interface{}{
					"type": "string",
					"enum": []string{"❤️", "👍", "👎", "😂", "‼️", "❓"},
				},
			},
			"required": []string{"emoji"},
		},
	},
	{
		Name: "send_message",
		Description: "Send one more short bubble of a follow-up thought. At most two extra messages per " +
			"reply, each a sentence or two. Two short bursts of energy beat one wall of text.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"text": map[string]interface{}{"type": "string"},
			},
			"required": []string{"text"},
		},
	},
	{
		Name: "share_artifact",
		Description: "Share a single tappable link to a chart or the plan page when it would actually " +
			"help, once per reply. Never invent a URL — the link must be one you were given or built.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"kind":  map[string]interface{}{"type": "string", "description": "what it is: chart or plan"},
				"title": map[string]interface{}{"type": "string", "description": "one short line to send with the link"},
				"url":   map[string]interface{}{"type": "string", "description": "the absolute http(s) link"},
			},
			"required": []string{"kind", "title", "url"},
		},
	},
	{
		Name:        "end_conversation",
		Description: "The person clearly wants out. Close warmly, no guilt trip.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"reason": map[string]interface{}{"type": "string"},
			},
		},
	},
}

const (
	// MaxExtraMessages caps the extra short bubbles one turn may emit beyond
	// the main reply. Bounded so a buggy model (or a relayed Python turn) can
	// never spam a thread. Shared with the DI adapter that projects Python
	// responses, so both delivery paths agree on the wire budget.
	MaxExtraMessages = 2
)

// validReactions is the outbound reaction whitelist. It mirrors exactly the six
// universal emoji that iMessage converts to native tapbacks (love, like,
// dislike, laugh, emphasize, question); anything else would render as a sticker
// or a plain message, so it is dropped rather than surfaced.
var validReactions = map[string]bool{
	"❤️": true,
	"👍":  true,
	"👎":  true,
	"😂":  true,
	"‼️": true,
	"❓":  true,
}

// ValidReaction reports whether an emoji is on the outbound tapback whitelist.
// Used by the executor and by the DI adapter that projects Python responses.
func ValidReaction(emoji string) bool {
	return validReactions[strings.TrimSpace(emoji)]
}

// guestShare is a rich link Miriam wants to hand over (a chart, the plan page).
// The host allowlist is applied by the onboarder before anything is sent.
type guestShare struct {
	kind  string
	title string
	url   string
}

// isSafeShareURL accepts only absolute http(s) links with a real host. It is the
// syntactic gate; the onboarder additionally enforces the configured allowlist.
func isSafeShareURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

// guestOutcome is what one brain turn decided: the reply to send plus the
// state effects the executor must apply.
type guestOutcome struct {
	text         string
	poll         *PollRequest
	startSignup  bool
	signupReason string
	connectBank  bool
	analysis     bool
	end          bool
	endReason    string
	notes        []guestNote
	reaction     string
	extraTexts   []string
	share        *guestShare
}

type guestNote struct {
	field string
	value string
}

// guestBrain turns the guest conversation over to the model and translates its
// tool calls into effects. All money/identity effects stay in onboarding.go.
type guestBrain struct {
	completer GuestCompleter
	logger    *zap.Logger
}

func newGuestBrain(completer GuestCompleter, logger *zap.Logger) *guestBrain {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &guestBrain{completer: completer, logger: logger}
}

// complete runs one completion with a bounded deadline, retrying once on
// failure. A blip here is otherwise visible to the person as an apology, so the
// retry happens before the turn is given up on.
func (b *guestBrain) complete(ctx context.Context, systemPrompt string, messages []GuestMessage, tools []GuestToolDef) (*GuestResult, error) {
	attempts := guestCompletionAttempts
	timeout := guestCompletionTimeout
	if cb, ok := b.completer.(CompletionBudget); ok {
		if t := cb.CompletionTimeout(); t > timeout {
			timeout = t
			attempts = 1 // one long careful attempt; budget adapters own their retries
		}
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, timeout)
		res, err := b.completer.CompleteGuest(attemptCtx, systemPrompt, messages, tools)
		cancel()
		if err == nil {
			return res, nil
		}
		lastErr = err
		// The caller's context being done means the whole turn is over —
		// another attempt would fail the same way.
		if ctx.Err() != nil {
			break
		}
		if attempt < attempts {
			b.logger.Warn("guest completion failed; retrying",
				zap.Int("attempt", attempt), zap.Error(err))
			select {
			case <-ctx.Done():
				return nil, lastErr
			case <-time.After(guestRetryGap):
			}
		}
	}
	return nil, lastErr
}

// respond runs one conversational turn. It makes at most two completions: the
// tool-enabled pass, and one follow-up when the model called tools without
// producing reply text.
func (b *guestBrain) respond(ctx context.Context, st *guestState, userText string) (*guestOutcome, error) {
	if b.completer == nil {
		return nil, errNoGuestCompleter
	}

	messages := make([]GuestMessage, 0, len(st.Turns)+1)
	messages = append(messages, st.Turns...)
	messages = append(messages, GuestMessage{Role: "user", Content: userText})

	systemPrompt := guestSystemPrompt + "\n\n" + guestStateBlock(st) + guestVoteNote(ctx)
	res, err := b.complete(ctx, systemPrompt, messages, guestTools)
	if err != nil {
		return nil, fmt.Errorf("guest completion: %w", err)
	}

	out := &guestOutcome{}
	for _, tc := range res.ToolCalls {
		b.applyToolCall(out, tc)
		// Feed the tool result back so the follow-up completion knows it landed.
		messages = append(messages,
			GuestMessage{Role: "assistant", Content: fmt.Sprintf("[called %s]", tc.Name)},
			GuestMessage{Role: "user", Content: fmt.Sprintf("[%s: done]", tc.Name)},
		)
	}

	out.text = strings.TrimSpace(res.Text)
	if out.text == "" && len(res.ToolCalls) > 0 {
		// Retry the follow-up on empty text, mirroring the completion retry logic.
		var followUp *GuestResult
		var ferr error
		for attempt := 1; attempt <= guestCompletionAttempts; attempt++ {
			followUp, ferr = b.complete(ctx, systemPrompt+"\nYour tool calls went through. Now say the reply out loud, in your own words.", messages, nil)
			if ferr != nil {
				continue
			}
			out.text = strings.TrimSpace(followUp.Text)
			if out.text != "" {
				break
			}
		}
		if ferr != nil {
			return nil, fmt.Errorf("guest follow-up completion: %w", ferr)
		}
	}
	if out.text == "" {
		return nil, errGuestNoReply
	}
	return out, nil
}

// regenerateDifferent re-asks for a differently-worded reply when the model
// repeated its previous message verbatim.
func (b *guestBrain) regenerateDifferent(ctx context.Context, st *guestState, userText string) (string, error) {
	messages := make([]GuestMessage, 0, len(st.Turns)+2)
	messages = append(messages, st.Turns...)
	messages = append(messages,
		GuestMessage{Role: "user", Content: userText},
		GuestMessage{Role: "user", Content: "[system note: your last reply was identical to the one before it. Say something different — move the conversation forward.]"},
	)
	res, err := b.complete(ctx, guestSystemPrompt+"\n\n"+guestStateBlock(st)+guestVoteNote(ctx), messages, nil)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(res.Text), nil
}

func (b *guestBrain) applyToolCall(out *guestOutcome, tc GuestToolCall) {
	switch tc.Name {
	case "note_detail":
		fieldRaw, ok := tc.Arguments["field"].(string)
		if !ok {
			b.logger.Warn("note_detail: missing or non-string 'field'", zap.String("tool", tc.Name))
			return
		}
		valueRaw, ok := tc.Arguments["value"].(string)
		if !ok {
			b.logger.Warn("note_detail: missing or non-string 'value'", zap.String("tool", tc.Name))
			return
		}
		field := strings.TrimSpace(fieldRaw)
		value := strings.TrimSpace(valueRaw)
		if field == "" || value == "" {
			return
		}
		out.notes = append(out.notes, guestNote{field: field, value: value})
	case "start_signup":
		out.startSignup = true
		if rRaw, ok := tc.Arguments["reason"].(string); ok {
			r := strings.TrimSpace(rRaw)
			if r != "" {
				out.signupReason = r
			}
		} else {
			b.logger.Warn("start_signup: missing or non-string 'reason'", zap.String("tool", tc.Name))
		}
	case "connect_bank":
		out.connectBank = true
	case "get_bank_statement_analysis":
		out.analysis = true
	case "send_poll":
		qRaw, ok := tc.Arguments["question"].(string)
		if !ok {
			b.logger.Warn("send_poll: missing or non-string 'question'", zap.String("tool", tc.Name))
			return
		}
		q := strings.TrimSpace(qRaw)
		opts := guestStringSlice(tc.Arguments["options"])
		if opts == nil || len(opts) < 2 {
			b.logger.Warn("send_poll: invalid or missing 'options' (need at least 2)", zap.String("tool", tc.Name), zap.Int("options_count", len(opts)))
			return
		}
		if q == "" {
			return
		}
		if len(opts) > 4 {
			opts = opts[:4]
		}
		out.poll = &PollRequest{Title: q, Options: opts}
	case "send_reaction":
		emojiRaw, ok := tc.Arguments["emoji"].(string)
		if !ok {
			b.logger.Warn("send_reaction: missing or non-string 'emoji'", zap.String("tool", tc.Name))
			return
		}
		emoji := strings.TrimSpace(emojiRaw)
		if !validReactions[emoji] {
			b.logger.Warn("send_reaction: emoji not on the tapback whitelist", zap.String("emoji", emoji))
			return
		}
		if out.reaction == "" {
			out.reaction = emoji
		}
	case "send_message":
		textRaw, ok := tc.Arguments["text"].(string)
		if !ok {
			b.logger.Warn("send_message: missing or non-string 'text'", zap.String("tool", tc.Name))
			return
		}
		text := strings.TrimSpace(textRaw)
		if text == "" {
			return
		}
		if len(out.extraTexts) < MaxExtraMessages {
			out.extraTexts = append(out.extraTexts, text)
		} else {
			b.logger.Warn("send_message: extra message budget exhausted", zap.String("tool", tc.Name))
		}
	case "share_artifact":
		urlRaw, _ := tc.Arguments["url"].(string)
		kind, _ := tc.Arguments["kind"].(string)
		title, _ := tc.Arguments["title"].(string)
		if !isSafeShareURL(urlRaw) {
			b.logger.Warn("share_artifact: not a safe absolute http(s) URL, dropping",
				zap.String("tool", tc.Name), zap.String("url", urlRaw))
			return
		}
		out.share = &guestShare{
			kind:  strings.TrimSpace(kind),
			title: strings.TrimSpace(title),
			url:   strings.TrimSpace(urlRaw),
		}
	case "end_conversation":
		out.end = true
		if rRaw, ok := tc.Arguments["reason"].(string); ok {
			r := strings.TrimSpace(rRaw)
			if r != "" {
				out.endReason = r
			}
		} else {
			b.logger.Warn("end_conversation: missing or non-string 'reason'", zap.String("tool", tc.Name))
		}
	default:
		b.logger.Warn("guest model called unknown tool", zap.String("tool", tc.Name))
	}
}

func guestStringSlice(v interface{}) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []interface{}:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	}
	return nil
}
