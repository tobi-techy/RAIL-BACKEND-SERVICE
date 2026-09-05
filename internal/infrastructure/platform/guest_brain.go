package platform

import (
	"context"
	"fmt"
	"strings"

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
					"enum":        []string{"first_name", "country", "goal", "money_type", "email"},
					"description": "Which detail to record. money_type is your silent read: avoider, optimizer, worrier, or dreamer.",
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
		Description: "Begin account setup because the person wants something that needs one (the audit, a plan, " +
			"their first deposit, linking a bank, saving a goal). Your reply text must naturally ask for their " +
			"phone number unless the state block says you already have it.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"reason": map[string]interface{}{
					"type":        "string",
					"description": "What they want that needs an account, in a few words.",
				},
			},
			"required": []string{"reason"},
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

// guestOutcome is what one brain turn decided: the reply to send plus the
// state effects the executor must apply.
type guestOutcome struct {
	text         string
	poll         *PollRequest
	startSignup  bool
	signupReason string
	end          bool
	endReason    string
	notes        []guestNote
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

// respond runs one conversational turn. It makes at most two completions: the
// tool-enabled pass, and one follow-up when the model called tools without
// producing reply text.
func (b *guestBrain) respond(ctx context.Context, st *guestState, userText string) (*guestOutcome, error) {
	if b.completer == nil {
		return nil, fmt.Errorf("no guest completer configured")
	}

	messages := make([]GuestMessage, 0, len(st.Turns)+1)
	messages = append(messages, st.Turns...)
	messages = append(messages, GuestMessage{Role: "user", Content: userText})

	res, err := b.completer.CompleteGuest(ctx, guestSystemPrompt+"\n\n"+guestStateBlock(st), messages, guestTools)
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
		followUp, err := b.completer.CompleteGuest(ctx, guestSystemPrompt+"\n\n"+guestStateBlock(st)+"\nYour tool calls went through. Now say the reply out loud, in your own words.", messages, nil)
		if err != nil {
			return nil, fmt.Errorf("guest follow-up completion: %w", err)
		}
		out.text = strings.TrimSpace(followUp.Text)
	}
	if out.text == "" {
		return nil, fmt.Errorf("guest model returned no reply text")
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
	res, err := b.completer.CompleteGuest(ctx, guestSystemPrompt+"\n\n"+guestStateBlock(st), messages, nil)
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
