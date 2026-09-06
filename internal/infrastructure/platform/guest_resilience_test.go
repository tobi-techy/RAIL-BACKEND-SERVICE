package platform

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

// flakyCompleter fails its first n calls, then behaves like a healthy provider.
type flakyCompleter struct {
	failures int
	calls    int
	reply    string
}

func (f *flakyCompleter) CompleteGuest(_ context.Context, _ string, _ []GuestMessage, _ []GuestToolDef) (*GuestResult, error) {
	f.calls++
	if f.calls <= f.failures {
		return nil, fmt.Errorf("provider blip %d", f.calls)
	}
	return &GuestResult{Text: f.reply}, nil
}

// toolsThenSilenceCompleter reproduces the shape that used to surface an
// apology: the tool pass returns a tool call with no text, and the follow-up
// pass returns empty text too, until it doesn't.
type toolsThenSilenceCompleter struct {
	silentFollowUps int
	followUps       int
}

func (c *toolsThenSilenceCompleter) CompleteGuest(_ context.Context, _ string, _ []GuestMessage, tools []GuestToolDef) (*GuestResult, error) {
	if len(tools) > 0 {
		return &GuestResult{ToolCalls: []GuestToolCall{
			{Name: "note_detail", Arguments: map[string]interface{}{"field": "goal", "value": "spend freely without going broke"}},
		}}, nil
	}
	c.followUps++
	if c.followUps <= c.silentFollowUps {
		return &GuestResult{}, nil
	}
	return &GuestResult{Text: "So you want room to spend without the fear. That I can build."}, nil
}

// brainOnboarderWith wires an arbitrary completer, unlike newBrainOnboarder
// which is fixed to the scripted fake.
func brainOnboarderWith(c GuestCompleter) (*ChatOnboarder, *fakeStore) {
	ob, store, _, _, _, _ := newTestOnboarder()
	ob.SetGuestCompleter(c)
	return ob, store
}

func stepRedeliverable(t *testing.T, ob *ChatOnboarder, sender, text string) (*PlatformReply, error) {
	t.Helper()
	return ob.Handle(context.Background(), OnboardInput{
		Platform:      entities.PlatformIMessage,
		SenderID:      sender,
		Text:          text,
		Redeliverable: true,
	})
}

// A single provider blip must be invisible: the retry inside the brain answers
// the turn without the person ever seeing an apology.
func TestGuestBrain_TransientBlipRetriesInProcess(t *testing.T) {
	fc := &flakyCompleter{failures: 1, reply: "Rich life. What does that look like for you?"}
	ob, _ := brainOnboarderWith(fc)

	reply := step(t, ob, "+15552200", "build a rich life")

	if reply != "Rich life. What does that look like for you?" {
		t.Fatalf("expected the model reply after retry, got: %q", reply)
	}
	if fc.calls != 2 {
		t.Fatalf("expected one retry (2 calls), got %d", fc.calls)
	}
}

// Tool calls with no reply text used to be a hard failure. The follow-up pass
// gets the same retry, so the goal is still captured and the person gets words.
func TestGuestBrain_EmptyTextAfterToolsRecovers(t *testing.T) {
	fc := &toolsThenSilenceCompleter{silentFollowUps: 1}
	ob, store := brainOnboarderWith(fc)
	sender := "+15552201"

	// Use Redeliverable so the transient errGuestNoReply triggers an in-process retry
	reply, err := stepRedeliverable(t, ob, sender, "being able to spend on what I want and not go broke ever again")

	if err != nil {
		t.Fatalf("expected the turn to succeed after retry, got error: %v", err)
	}
	if !strings.Contains(reply.Text, "without the fear") {
		t.Fatalf("expected the recovered reply, got: %q", reply.Text)
	}
	var st guestState
	if err := store.Get(context.Background(), onboardingKey(entities.PlatformIMessage, sender), &st); err != nil {
		t.Fatalf("load state: %v", err)
	}
	if st.Goal != "spend freely without going broke" {
		t.Fatalf("expected the goal captured from the tool call, got %q", st.Goal)
	}
}

// While a redelivery is still coming, a failed turn must stay silent and ask to
// be requeued rather than spending the person's turn on an apology.
func TestGuestBrain_FailedTurnRequeuesWhileRedeliverable(t *testing.T) {
	fc := &fakeCompleter{err: fmt.Errorf("provider down")}
	ob, store := brainOnboarderWith(fc)
	sender := "+15552202"

	reply, err := stepRedeliverable(t, ob, sender, "build a rich life")

	if err == nil {
		t.Fatal("expected a retryable error so the bridge redelivers")
	}
	if !IsRetryable(err) {
		t.Fatalf("expected a retryable error, got: %v", err)
	}
	if reply != nil {
		t.Fatalf("nothing should be sent while a redelivery is pending, got: %+v", reply)
	}
	// The turn was never charged, so the retry starts from the same state.
	var st guestState
	if err := store.Get(context.Background(), onboardingKey(entities.PlatformIMessage, sender), &st); err == nil && st.TurnCount != 0 {
		t.Fatalf("failed turn must not be recorded, got TurnCount=%d", st.TurnCount)
	}
}

// On the last delivery attempt the person has to hear something, and it must be
// Miriam re-asking her question — not a scripted engine-trouble line.
func TestGuestBrain_FinalAttemptApologisesAndReAsks(t *testing.T) {
	fc := &fakeCompleter{}
	ob, store := brainOnboarderWith(fc)
	sender := "+15552203"

	// A conversation already in flight: she asked something, they answered.
	st := guestState{
		Phase:     phaseConverse,
		FirstName: "Tobi",
		Turns: []GuestMessage{
			{Role: "user", Content: "build a rich life"},
			{Role: "assistant", Content: "Rich life. What does that look like for you specifically?"},
		},
	}
	if err := store.Set(context.Background(), onboardingKey(entities.PlatformIMessage, sender), st, 0); err != nil {
		t.Fatal(err)
	}
	fc.err = fmt.Errorf("provider down")

	reply := step(t, ob, sender, "being able to spend on what I want and not go broke ever again")

	if strings.Contains(strings.ToLower(reply), "conversation engine") {
		t.Fatalf("the engine-trouble line must be gone, got: %q", reply)
	}
	if !strings.Contains(reply, "What does that look like for you specifically?") {
		t.Fatalf("expected her last question re-asked, got: %q", reply)
	}
}

// A failed completion (and every redelivery of it) must not eat the sender's
// daily allowance.
func TestGuestBrain_FailedTurnDoesNotBurnDailyQuota(t *testing.T) {
	fc := &fakeCompleter{err: fmt.Errorf("provider down")}
	ob, store := brainOnboarderWith(fc)
	sender := "+15552204"
	key := dailyTurnsKey(entities.PlatformIMessage, sender)

	if _, err := stepRedeliverable(t, ob, sender, "build a rich life"); err == nil {
		t.Fatal("expected the turn to fail")
	}

	var used int
	if err := store.Get(context.Background(), key, &used); err == nil && used != 0 {
		t.Fatalf("failed turn charged the daily cap: %d", used)
	}

	// A turn that lands is charged exactly once.
	fc.err = nil
	fc.default_ = fakeCompletion{text: "Rich life. What does that look like?"}
	step(t, ob, sender, "build a rich life")

	if err := store.Get(context.Background(), key, &used); err != nil {
		t.Fatalf("read daily cap: %v", err)
	}
	if used != 1 {
		t.Fatalf("expected exactly one charged turn, got %d", used)
	}
}

// The cap still stops a runaway sender.
func TestGuestBrain_DailyCapStillEnforced(t *testing.T) {
	fc := &fakeCompleter{default_: fakeCompletion{text: "go on"}}
	ob, store := brainOnboarderWith(fc)
	sender := "+15552205"

	if err := store.Set(context.Background(), dailyTurnsKey(entities.PlatformIMessage, sender), maxGuestDailyTurns, time.Hour); err != nil {
		t.Fatal(err)
	}
	callsBefore := len(fc.calls)

	reply := step(t, ob, sender, "one more thing")

	if len(fc.calls) != callsBefore {
		t.Fatal("a capped sender must not reach the model")
	}
	if !strings.Contains(strings.ToLower(reply), "chat limit") {
		t.Fatalf("expected the cap message, got: %q", reply)
	}
}

// Statement state has to survive the phase branches: the consent path used to
// return before saving, dropping the pending id and losing the document.
func TestGuestBrain_StatementScanPersistsBeforePhaseDispatch(t *testing.T) {
	fc := &fakeCompleter{default_: fakeCompletion{text: "unused"}}
	ob, store := brainOnboarderWith(fc)
	sender := "+15552206"
	ob.SetStatementAttachmentHandler(&fakeStatementHandler{})

	// Sitting at consent: the reply is the deterministic consent poll, which
	// returns without touching the conversational save path.
	st := guestState{Phase: phaseConsent, Phone: "+2348012345678", FirstName: "Tobi", UserID: ""}
	if err := store.Set(context.Background(), onboardingKey(entities.PlatformIMessage, sender), st, 0); err != nil {
		t.Fatal(err)
	}

	_, err := ob.Handle(context.Background(), OnboardInput{
		Platform:  entities.PlatformIMessage,
		SenderID:  sender,
		Statement: &StatementAttachment{Data: []byte("%PDF-1.4 fake"), MIMEType: "application/pdf", Name: "statement.pdf"},
	})
	if err != nil {
		t.Fatalf("Handle(statement) error: %v", err)
	}

	var saved guestState
	if err := store.Get(context.Background(), onboardingKey(entities.PlatformIMessage, sender), &saved); err != nil {
		t.Fatalf("load state: %v", err)
	}
	if saved.PendingStatementID != "pending-1" {
		t.Fatalf("pending statement id must survive the consent branch, got %q", saved.PendingStatementID)
	}
	if !strings.Contains(saved.StatementSummary, "groceries") {
		t.Fatalf("statement summary must survive the consent branch, got %q", saved.StatementSummary)
	}
}

func TestLastQuestion(t *testing.T) {
	cases := []struct {
		name  string
		reply string
		want  string
	}{
		{"single question", "What does that look like for you?", "What does that look like for you?"},
		{"trailing question after a statement", "Rich life. Love it. What does that mean specifically?", "What does that mean specifically?"},
		{"no question", "Got it. I'll keep that in mind.", ""},
		{"question mid-reply only", "What now? Actually, hold on.", ""},
		{"empty", "", ""},
		{"bare punctuation", "?", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := lastQuestion(tc.reply); got != tc.want {
				t.Fatalf("lastQuestion(%q) = %q, want %q", tc.reply, got, tc.want)
			}
		})
	}
}

func TestInboundMessage_IsFinalAttempt(t *testing.T) {
	cases := []struct {
		name    string
		msg     InboundMessage
		isFinal bool
	}{
		{"older bridge sends no counters", InboundMessage{}, true},
		{"first of three", InboundMessage{Attempt: 1, MaxAttempts: 3}, false},
		{"last of three", InboundMessage{Attempt: 3, MaxAttempts: 3}, true},
		{"beyond the max", InboundMessage{Attempt: 4, MaxAttempts: 3}, true},
		{"max missing", InboundMessage{Attempt: 2}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.msg.IsFinalAttempt(); got != tc.isFinal {
				t.Fatalf("IsFinalAttempt() = %v, want %v", got, tc.isFinal)
			}
		})
	}
}
