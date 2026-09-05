package platform

import (
	"context"
	"fmt"
	"strings"
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

	reply := step(t, ob, sender, "hey")
	if !strings.Contains(strings.ToLower(reply), "miriam") {
		t.Fatalf("provider failure should degrade to the deterministic intro, got: %q", reply)
	}
	// And the fallback still completes signup.
	step(t, ob, sender, "Ada")
	step(t, ob, sender, "+2348012345678")
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
	moneyTypes []string
	turns      []GuestMessage
}

func (f *fakeHandoff) SetMoneyType(_ context.Context, _ uuid.UUID, moneyType string) error {
	f.moneyTypes = append(f.moneyTypes, moneyType)
	return nil
}

func (f *fakeHandoff) AppendGuestTranscript(_ context.Context, _ uuid.UUID, _ *entities.PlatformIdentity, _ string, turns []GuestMessage) error {
	f.turns = turns
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
		if len(handoff.moneyTypes) > 0 && len(handoff.turns) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(handoff.moneyTypes) != 1 || handoff.moneyTypes[0] != "worrier" {
		t.Fatalf("expected money type handoff, got %v", handoff.moneyTypes)
	}
	if len(handoff.turns) == 0 {
		t.Fatal("expected transcript handoff")
	}
}
