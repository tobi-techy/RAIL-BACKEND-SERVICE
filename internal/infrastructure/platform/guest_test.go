package platform

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

// fakeCompleter scripts guest-brain completions. Each queued response is used
// in order; when the queue is empty the default response fires. Every call is
// recorded so tests can assert on the system prompt the brain sent.
type fakeCompleter struct {
	responses []fakeCompletion
	calls     []fakeCall
	err       error
	default_  fakeCompletion
}

type fakeCompletion struct {
	text      string
	toolCalls []GuestToolCall
}

type fakeCall struct {
	systemPrompt string
	messages     []GuestMessage
	toolNames    []string
}

func (f *fakeCompleter) CompleteGuest(_ context.Context, systemPrompt string, messages []GuestMessage, tools []GuestToolDef) (*GuestResult, error) {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.Name)
	}
	f.calls = append(f.calls, fakeCall{systemPrompt: systemPrompt, messages: messages, toolNames: names})
	if f.err != nil {
		return nil, f.err
	}
	var next fakeCompletion
	if len(f.responses) > 0 {
		next = f.responses[0]
		f.responses = f.responses[1:]
	} else {
		next = f.default_
	}
	return &GuestResult{Text: next.text, ToolCalls: next.toolCalls}, nil
}

func newBrainOnboarder(fc *fakeCompleter) (*ChatOnboarder, *fakeStore, *fakeVerifier, *fakeUsers, *fakeProvisioner, *fakeLinker) {
	ob, store, ver, users, prov, linker := newTestOnboarder()
	ob.SetGuestCompleter(fc)
	return ob, store, ver, users, prov, linker
}

func TestGuestBrain_FirstTurnUsesModelReply(t *testing.T) {
	fc := &fakeCompleter{default_: fakeCompletion{text: "Hey, I'm Miriam. What are we here for?"}}
	ob, _, _, _, _, _ := newBrainOnboarder(fc)

	reply := step(t, ob, "+15552100", "hey")
	if reply != "Hey, I'm Miriam. What are we here for?" {
		t.Fatalf("expected the model's words verbatim, got: %q", reply)
	}
	if len(fc.calls) != 1 {
		t.Fatalf("expected one completion, got %d", len(fc.calls))
	}
	if !strings.Contains(fc.calls[0].systemPrompt, "no account yet") {
		t.Fatalf("system prompt should carry the guest rules, got: %.120q", fc.calls[0].systemPrompt)
	}
}

func TestGuestBrain_NoteDetailStoresSlots(t *testing.T) {
	fc := &fakeCompleter{responses: []fakeCompletion{
		{text: "Tobi, love it. What's the money actually for?",
			toolCalls: []GuestToolCall{{Name: "note_detail", Arguments: map[string]interface{}{"field": "first_name", "value": "tobi"}}}},
		{text: "A Zanzibar trip. Specific. What does it cost, roughly?",
			toolCalls: []GuestToolCall{{Name: "note_detail", Arguments: map[string]interface{}{"field": "goal", "value": "trip to Zanzibar"}}}},
	}}
	ob, store, _, _, _, _ := newBrainOnboarder(fc)
	sender := "+15552101"

	step(t, ob, sender, "I'm Tobi")
	step(t, ob, sender, "want to save for a trip to Zanzibar")

	var st guestState
	if err := store.Get(context.Background(), onboardingKey(entities.PlatformIMessage, sender), &st); err != nil {
		t.Fatalf("load state: %v", err)
	}
	if st.FirstName != "Tobi" || st.Goal != "trip to Zanzibar" {
		t.Fatalf("expected slots stored, got name=%q goal=%q", st.FirstName, st.Goal)
	}
	// The second completion's system prompt must already carry the name.
	if !strings.Contains(fc.calls[1].systemPrompt, "name: Tobi") {
		t.Fatalf("state block should include the stored name, got: %.200q", fc.calls[1].systemPrompt)
	}
}

func TestGuestBrain_GreetingNoteIgnored(t *testing.T) {
	fc := &fakeCompleter{responses: []fakeCompletion{
		{text: "Hey! What should I call you?",
			toolCalls: []GuestToolCall{{Name: "note_detail", Arguments: map[string]interface{}{"field": "first_name", "value": "Hi"}}}},
	}}
	ob, store, _, _, _, _ := newBrainOnboarder(fc)
	sender := "+15552102"

	step(t, ob, sender, "hi")

	var st guestState
	if err := store.Get(context.Background(), onboardingKey(entities.PlatformIMessage, sender), &st); err != nil {
		t.Fatalf("load state: %v", err)
	}
	if st.FirstName != "" {
		t.Fatalf("greeting must never become a name, got %q", st.FirstName)
	}
}

func TestGuestBrain_StartSignupWithoutPhoneAsksNaturally(t *testing.T) {
	fc := &fakeCompleter{responses: []fakeCompletion{
		{text: "I can absolutely run that audit. Drop your number and I'll get you set up.",
			toolCalls: []GuestToolCall{{Name: "start_signup", Arguments: map[string]interface{}{"reason": "wants the spending audit"}}}},
	}}
	ob, store, ver, _, _, _ := newBrainOnboarder(fc)
	sender := "+15552103"

	reply := step(t, ob, sender, "can you look at my spending?")
	if !strings.Contains(reply, "Drop your number") {
		t.Fatalf("expected the model's natural phone ask, got: %q", reply)
	}
	if len(ver.sentTo) != 0 {
		t.Fatalf("no code should go out before a number exists, got: %v", ver.sentTo)
	}

	var st guestState
	if err := store.Get(context.Background(), onboardingKey(entities.PlatformIMessage, sender), &st); err != nil {
		t.Fatalf("load state: %v", err)
	}
	if st.Phase != phasePhone {
		t.Fatalf("expected phase awaiting_phone, got %q", st.Phase)
	}

	// The number arrives next turn — deterministic, no model involved.
	otp := step(t, ob, sender, "it's +2349164904178")
	if len(ver.sentTo) != 1 || ver.sentTo[0] != "+2349164904178" {
		t.Fatalf("expected OTP to the parsed number, got: %v", ver.sentTo)
	}
	if !strings.Contains(strings.ToLower(otp), "code") {
		t.Fatalf("expected code prompt, got: %q", otp)
	}
}

func TestGuestBrain_StartSignupWithCardPhoneSkipsToOTP(t *testing.T) {
	fc := &fakeCompleter{responses: []fakeCompletion{
		{text: "Got it, Ada. Let's get you in."},
		{text: "Let's do it.",
			toolCalls: []GuestToolCall{{Name: "start_signup", Arguments: map[string]interface{}{"reason": "first deposit"}}}},
	}}
	ob, _, ver, _, _, _ := newBrainOnboarder(fc)
	sender := "+15552104"

	stepContact(t, ob, sender, SharedContact{FirstName: "Ada", Phones: []string{"+2348012345678"}, Country: "NG"})
	reply := step(t, ob, sender, "I want to put money in")

	if len(ver.sentTo) != 1 || ver.sentTo[0] != "+2348012345678" {
		t.Fatalf("phone on file should go straight to OTP, got: %v", ver.sentTo)
	}
	if !strings.Contains(strings.ToLower(reply), "code") {
		t.Fatalf("expected code prompt, got: %q", reply)
	}
}

// TestGuestBrain_NoVerbatimRepeat pins the screenshot bug: the same sentence
// twice in a row must be regenerated, not re-sent.
func TestGuestBrain_NoVerbatimRepeat(t *testing.T) {
	fc := &fakeCompleter{responses: []fakeCompletion{
		{text: "Tell me what you want your money to do."},
		// Model repeats itself verbatim...
		{text: "Tell me what you want your money to do."},
		// ...the regen call (no tools) returns something different.
		{text: "New angle: what's the money thing you keep putting off?"},
	}}
	ob, _, _, _, _, _ := newBrainOnboarder(fc)
	sender := "+15552105"

	first := step(t, ob, sender, "hey")
	second := step(t, ob, sender, "not sure")
	if first == "" || second == "" {
		t.Fatalf("expected replies, got %q then %q", first, second)
	}
	if second == first {
		t.Fatalf("verbatim repeat must be regenerated, got %q twice", first)
	}
	if !strings.Contains(second, "New angle") {
		t.Fatalf("expected the regenerated reply, got: %q", second)
	}
}

func TestGuestBrain_EndConversationClearsSession(t *testing.T) {
	fc := &fakeCompleter{responses: []fakeCompletion{
		{text: "All good. I'm here when you need me.",
			toolCalls: []GuestToolCall{{Name: "end_conversation", Arguments: map[string]interface{}{"reason": "not interested"}}}},
	}}
	ob, _, _, _, _, _ := newBrainOnboarder(fc)
	sender := "+15552106"

	reply := step(t, ob, sender, "leave me alone")
	if !strings.Contains(reply, "I'm here") {
		t.Fatalf("expected warm close, got: %q", reply)
	}
	if ob.HasSession(context.Background(), entities.PlatformIMessage, sender) {
		t.Fatal("session should be cleared after end_conversation")
	}
}

func TestGuestBrain_DailyCapStopsModelTurns(t *testing.T) {
	fc := &fakeCompleter{default_: fakeCompletion{text: "hello"}}
	ob, store, _, _, _, _ := newBrainOnboarder(fc)
	sender := "+15552107"

	// Burn the daily counter directly.
	if err := store.Set(context.Background(), dailyTurnsKey(entities.PlatformIMessage, sender), maxGuestDailyTurns, 0); err != nil {
		t.Fatal(err)
	}
	// Verify the counter was actually set.
	var counter int
	if err := store.Get(context.Background(), dailyTurnsKey(entities.PlatformIMessage, sender), &counter); err != nil {
		t.Fatalf("verify counter: %v", err)
	}
	if counter != maxGuestDailyTurns {
		t.Fatalf("expected counter=%d, got %d", maxGuestDailyTurns, counter)
	}
	callsBefore := len(fc.calls)
	reply := step(t, ob, sender, "one more thing")
	if len(fc.calls) != callsBefore {
		t.Fatal("over-cap turn must not hit the model")
	}
	if !strings.Contains(strings.ToLower(reply), "limit") {
		t.Fatalf("expected cap message, got: %q", reply)
	}
}

func TestGuestBrain_TurnCapSteersToSignup(t *testing.T) {
	fc := &fakeCompleter{default_: fakeCompletion{text: "sure, tell me more"}}
	ob, store, _, _, _, _ := newBrainOnboarder(fc)
	sender := "+15552108"

	// Pre-load a session at the turn cap.
	st := guestState{Phase: phaseConverse, TurnCount: maxGuestTurns, FirstName: "Ada"}
	if err := store.Set(context.Background(), onboardingKey(entities.PlatformIMessage, sender), st, 0); err != nil {
		t.Fatal(err)
	}
	callsBefore := len(fc.calls)
	reply := step(t, ob, sender, "still chatting")
	if len(fc.calls) != callsBefore {
		t.Fatal("turn-capped conversation must not hit the model")
	}
	if !strings.Contains(strings.ToLower(reply), "number") {
		t.Fatalf("expected the signup steer, got: %q", reply)
	}
}

func TestGuestBrain_ProviderDownFallsBack(t *testing.T) {
	fc := &fakeCompleter{err: fmt.Errorf("provider down")}
	ob, _, _, _, _, _ := newBrainOnboarder(fc)
	sender := "+15552109"

	// First message is a greeting; fallback should prompt for name.
	reply := step(t, ob, sender, "hey")
	if !strings.Contains(strings.ToLower(reply), "call you") {
		t.Fatalf("provider failure should prompt for name on greeting, got: %q", reply)
	}
	// Provide name.
	reply = step(t, ob, sender, "Ada")
	if !strings.Contains(strings.ToLower(reply), "number") {
		t.Fatalf("expected phone prompt after name, got: %q", reply)
	}
	// Provide phone.
	step(t, ob, sender, "+2348012345678")
	// Provide OTP.
	consent := step(t, ob, sender, "123456")
	if !strings.Contains(consent, "I agree") {
		t.Fatalf("fallback flow should reach consent, got: %q", consent)
	}
}

func TestGuestBrain_ConsentQuestionAnsweredNotPolled(t *testing.T) {
	fc := &fakeCompleter{responses: []fakeCompletion{
		{text: "Fair question. The terms cover how Rail holds and moves your money. Tap I agree when you're comfortable."},
	}}
	ob, store, ver, _, prov, _ := newBrainOnboarder(fc)
	sender := "+15552110"

	// Drive to consent deterministically.
	st := guestState{Phase: phaseOTP, Phone: "+2348012345678", FirstName: "Ada"}
	if err := store.Set(context.Background(), onboardingKey(entities.PlatformIMessage, sender), st, 0); err != nil {
		t.Fatal(err)
	}
	_ = ver
	consent := step(t, ob, sender, "123456")
	if !strings.Contains(consent, "I agree") {
		t.Fatalf("expected consent prompt, got: %q", consent)
	}

	reply := step(t, ob, sender, "wait, what am I agreeing to?")
	if prov.calls != 0 {
		t.Fatal("a question must not provision")
	}
	if !strings.Contains(reply, "Fair question") {
		t.Fatalf("expected the model's answer, not a re-poll, got: %q", reply)
	}
	if strings.Contains(reply, "Last thing") {
		t.Fatalf("a question must not trigger the consent poll again, got: %q", reply)
	}
}

func TestGuestBrain_TranscriptBounded(t *testing.T) {
	fc := &fakeCompleter{default_: fakeCompletion{text: "ok"}}
	ob, store, _, _, _, _ := newBrainOnboarder(fc)
	sender := "+15552111"

	for i := 0; i < maxTranscriptTurns+6; i++ {
		step(t, ob, sender, fmt.Sprintf("message %d", i))
	}
	var st guestState
	if err := store.Get(context.Background(), onboardingKey(entities.PlatformIMessage, sender), &st); err != nil {
		t.Fatalf("load state: %v", err)
	}
	if len(st.Turns) > maxTranscriptTurns {
		t.Fatalf("transcript must be bounded at %d, got %d", maxTranscriptTurns, len(st.Turns))
	}
}

// fakeHandoff records guest-handoff writes.
type fakeHandoff struct {
	mu         sync.Mutex
	moneyTypes []string
	turns      []GuestMessage
}

func (f *fakeHandoff) SetMoneyType(_ context.Context, _ uuid.UUID, moneyType string) error {
	f.mu.Lock()
	f.moneyTypes = append(f.moneyTypes, moneyType)
	f.mu.Unlock()
	return nil
}

func (f *fakeHandoff) SetMoneyDials(_ context.Context, _ uuid.UUID, dials string) error {
	return nil
}

func (f *fakeHandoff) AppendGuestTranscript(_ context.Context, _ uuid.UUID, _ *entities.PlatformIdentity, _ string, turns []GuestMessage) error {
	f.mu.Lock()
	f.turns = turns
	f.mu.Unlock()
	return nil
}

func TestGuestBrain_HandoffCarriesMoneyTypeAndTranscript(t *testing.T) {
	fc := &fakeCompleter{responses: []fakeCompletion{
		{text: "Noted. What else is on your mind?",
			toolCalls: []GuestToolCall{{Name: "note_detail", Arguments: map[string]interface{}{"field": "money_type", "value": "worrier"}}}},
	}}
	ob, store, _, _, _, _ := newBrainOnboarder(fc)
	sender := "+15552112"
	handoff := &fakeHandoff{}
	ob.SetGuestHandoff(handoff, handoff)

	step(t, ob, sender, "I worry about money constantly")

	var st guestState
	if err := store.Get(context.Background(), onboardingKey(entities.PlatformIMessage, sender), &st); err != nil {
		t.Fatalf("load state: %v", err)
	}
	if st.MoneyType != "worrier" {
		t.Fatalf("expected money type stored, got %q", st.MoneyType)
	}

	// Finish signup and confirm the handoff fires.
	st.Phase = phaseOTP
	st.Phone = "+2348012345678"
	if err := store.Set(context.Background(), onboardingKey(entities.PlatformIMessage, sender), st, 0); err != nil {
		t.Fatal(err)
	}
	step(t, ob, sender, "123456")
	step(t, ob, sender, "I agree")

	// Handoff is async — poll with a deadline.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		handoff.mu.Lock()
		ready := len(handoff.moneyTypes) > 0 && len(handoff.turns) > 0
		handoff.mu.Unlock()
		if ready {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	handoff.mu.Lock()
	mt := len(handoff.moneyTypes)
	first := ""
	if mt > 0 {
		first = handoff.moneyTypes[0]
	}
	turnCount := len(handoff.turns)
	handoff.mu.Unlock()
	if mt != 1 || first != "worrier" {
		t.Fatalf("expected money type handoff, got %d entries", mt)
	}
	if turnCount == 0 {
		t.Fatal("expected transcript handoff")
	}
}

// stepFull returns the full structured reply so tests can assert on the
// reaction/extra-bubbles/share the executor projected.
func stepFull(t *testing.T, ob *ChatOnboarder, sender, text string) *PlatformReply {
	t.Helper()
	reply, err := ob.Handle(context.Background(), OnboardInput{
		Platform: entities.PlatformIMessage,
		SenderID: sender,
		Text:     text,
	})
	if err != nil {
		t.Fatalf("Handle(%q) error: %v", text, err)
	}
	return reply
}

func TestGuestBrain_ChattyGesturesFlowThrough(t *testing.T) {
	fc := &fakeCompleter{default_: fakeCompletion{
		text: "That genuinely helps. Let's get it moving.",
		toolCalls: []GuestToolCall{
			{Name: "send_reaction", Arguments: map[string]interface{}{"emoji": "❤️"}},
			{Name: "send_message", Arguments: map[string]interface{}{"text": "One thing at a time though."}},
			{Name: "send_message", Arguments: map[string]interface{}{"text": "Your number and we're off."}},
			{Name: "share_artifact", Arguments: map[string]interface{}{"kind": "plan", "title": "Your plan", "url": "https://miriam.example/plans/abc"}},
		},
	}}
	ob, _, _, _, _, _ := newBrainOnboarder(fc)
	ob.SetShareAllowlist([]string{"miriam.example"})
	sender := "+15552130"

	reply := stepFull(t, ob, sender, "ok let's do it")
	if reply.Reaction != "❤️" {
		t.Fatalf("expected the tapback reaction, got %q", reply.Reaction)
	}
	if len(reply.ExtraTexts) != 2 || reply.ExtraTexts[0] != "One thing at a time though." || reply.ExtraTexts[1] != "Your number and we're off." {
		t.Fatalf("expected the two extra bubbles, got %#v", reply.ExtraTexts)
	}
	if reply.Share == nil || reply.Share.URL != "https://miriam.example/plans/abc" || reply.Share.Kind != "plan" {
		t.Fatalf("expected the share, got %#v", reply.Share)
	}
}

func TestGuestBrain_ReactionWhitelistApplied(t *testing.T) {
	fc := &fakeCompleter{default_: fakeCompletion{
		text: "Solid.",
		toolCalls: []GuestToolCall{
			{Name: "send_reaction", Arguments: map[string]interface{}{"emoji": "🎉"}},
			{Name: "send_reaction", Arguments: map[string]interface{}{"emoji": "👍"}},
		},
	}}
	ob, _, _, _, _, _ := newBrainOnboarder(fc)
	sender := "+15552131"

	reply := stepFull(t, ob, sender, "nice")
	if reply.Reaction != "👍" {
		t.Fatalf("off-whitelist emoji must be dropped in favor of the whitelisted one, got %q", reply.Reaction)
	}
}

func TestGuestBrain_ExtraMessageBudgetCapped(t *testing.T) {
	toolCalls := make([]GuestToolCall, 0, 5)
	for i := 0; i < 5; i++ {
		toolCalls = append(toolCalls, GuestToolCall{Name: "send_message", Arguments: map[string]interface{}{"text": fmt.Sprintf("bubble %d", i)}})
	}
	fc := &fakeCompleter{default_: fakeCompletion{text: "Here's the thing.", toolCalls: toolCalls}}
	ob, _, _, _, _, _ := newBrainOnboarder(fc)
	sender := "+15552132"

	reply := stepFull(t, ob, sender, "go on")
	if len(reply.ExtraTexts) != MaxExtraMessages {
		t.Fatalf("extra bubbles must be capped at %d, got %d (%#v)", MaxExtraMessages, len(reply.ExtraTexts), reply.ExtraTexts)
	}
}

func TestGuestBrain_ShareGatedByHostAllowlist(t *testing.T) {
	share := GuestToolCall{Name: "share_artifact", Arguments: map[string]interface{}{"kind": "chart", "title": "Spending", "url": "https://evil.example/phish"}}

	t.Run("no allowlist admits nothing", func(t *testing.T) {
		fc := &fakeCompleter{default_: fakeCompletion{text: "Here's your picture.", toolCalls: []GuestToolCall{share}}}
		ob, _, _, _, _, _ := newBrainOnboarder(fc)
		if reply := stepFull(t, ob, "+15552133", "show me"); reply.Share != nil {
			t.Fatalf("share must be dropped without an allowlist, got %#v", reply.Share)
		}
	})
	t.Run("host not on allowlist is dropped", func(t *testing.T) {
		fc := &fakeCompleter{default_: fakeCompletion{text: "Here's your picture.", toolCalls: []GuestToolCall{share}}}
		ob, _, _, _, _, _ := newBrainOnboarder(fc)
		ob.SetShareAllowlist([]string{"miriam.example"})
		if reply := stepFull(t, ob, "+15552134", "show me"); reply.Share != nil {
			t.Fatalf("off-allowlist host must be dropped, got %#v", reply.Share)
		}
	})
	t.Run("host on allowlist is delivered", func(t *testing.T) {
		fc := &fakeCompleter{default_: fakeCompletion{text: "Here's your picture.", toolCalls: []GuestToolCall{share}}}
		ob, _, _, _, _, _ := newBrainOnboarder(fc)
		ob.SetShareAllowlist([]string{"evil.example"})
		if reply := stepFull(t, ob, "+15552135", "show me"); reply.Share == nil || reply.Share.URL != "https://evil.example/phish" {
			t.Fatalf("allowlisted host must be delivered, got %#v", reply.Share)
		}
	})
	t.Run("non-http URL is syntactically rejected", func(t *testing.T) {
		bad := GuestToolCall{Name: "share_artifact", Arguments: map[string]interface{}{"url": "javascript:alert(1)"}}
		fc := &fakeCompleter{default_: fakeCompletion{text: "Here.", toolCalls: []GuestToolCall{bad}}}
		ob, _, _, _, _, _ := newBrainOnboarder(fc)
		ob.SetShareAllowlist([]string{"javascript"})
		if reply := stepFull(t, ob, "+15552136", "show me"); reply.Share != nil {
			t.Fatalf("non-http URL must never be shared, got %#v", reply.Share)
		}
	})
}

// TestGuestSystemPrompt_Tone pins the tone contract from the guest-prompt
// retune: texting-length replies, one question at most, no
// acknowledgment-then-question rhythm, no filler, plain punctuation.
func TestGuestSystemPrompt_Tone(t *testing.T) {
	for _, want := range []string{
		"no account yet",
		"1-4 sentences, one question at most",
		"Never make every reply an acknowledgment followed by a question",
		"\"That makes sense\"",
		"Have a point of view",
		"No em dashes or en dashes",
	} {
		if !strings.Contains(guestSystemPrompt, want) {
			t.Errorf("guestSystemPrompt missing %q from tone retune", want)
		}
	}
	if idx := strings.IndexAny(guestSystemPrompt, "\u2013\u2014"); idx >= 0 {
		t.Errorf("guestSystemPrompt contains an em/en dash near %q", guestSystemPrompt[max(0, idx-30):idx+30])
	}
}

// TestGuestBrain_MoneyDialCapturedAndHandedOff pins the richer person model:
// the guest brain notes what the person loves spending on (their money dial),
// and it is carried into the authenticated relationship at signup.
func TestGuestBrain_MoneyDialCapturedAndHandedOff(t *testing.T) {
	fc := &fakeCompleter{responses: []fakeCompletion{
		{text: "Got it.", toolCalls: []GuestToolCall{{Name: "note_detail", Arguments: map[string]interface{}{"field": "money_dial", "value": "eating out with friends"}}}},
		{text: "drop your number and I'll get your split running", toolCalls: []GuestToolCall{{Name: "start_signup", Arguments: map[string]interface{}{"reason": "first deposit"}}}},
	}}
	ob, store, _, _, prov, _ := newBrainOnboarder(fc)
	handoff := &fakeHandoff{}
	ob.SetGuestHandoff(handoff, handoff)

	key := onboardingKey(entities.PlatformIMessage, "+15552120")
	step(t, ob, "+15552120", "I love eating out, honestly that's where my money goes")
	var st guestState
	if err := store.Get(context.Background(), key, &st); err != nil {
		t.Fatalf("session: %v", err)
	}
	if st.MoneyDial == "" {
		t.Fatal("expected the money dial to be captured")
	}

	step(t, ob, "+15552120", "I want to make my first deposit")
	step(t, ob, "+15552120", "+15551234567")
	step(t, ob, "+15552120", "123456")
	step(t, ob, "+15552120", "I agree")

	if prov.calls != 1 {
		t.Fatalf("expected provisioning to run, got %d", prov.calls)
	}
}
