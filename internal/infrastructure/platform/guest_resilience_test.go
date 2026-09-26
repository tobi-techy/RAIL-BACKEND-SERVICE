package platform

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
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

// budgetCompleter fails every call and declares a custom completion budget,
// standing in for the Python brain adapter whose turn is configurable.
type budgetCompleter struct {
	calls   int
	timeout time.Duration
}

func (c *budgetCompleter) CompleteGuest(_ context.Context, _ string, _ []GuestMessage, _ []GuestToolDef) (*GuestResult, error) {
	c.calls++
	return nil, fmt.Errorf("brain unavailable")
}

func (c *budgetCompleter) CompletionTimeout() time.Duration { return c.timeout }

// toolThenTextCompleter makes a tool call with no text on its first pass — which
// forces the brain's follow-up pass — and records each pass's deadline.
type toolThenTextCompleter struct {
	calls     int
	deadlines []time.Time
	sleep     time.Duration
}

func (c *toolThenTextCompleter) CompleteGuest(ctx context.Context, _ string, _ []GuestMessage, _ []GuestToolDef) (*GuestResult, error) {
	if d, ok := ctx.Deadline(); ok {
		c.deadlines = append(c.deadlines, d)
	} else {
		c.deadlines = append(c.deadlines, time.Time{})
	}
	c.calls++
	if c.calls == 1 {
		time.Sleep(c.sleep)
		return &GuestResult{ToolCalls: []GuestToolCall{
			{Name: "note_detail", Arguments: map[string]interface{}{"field": "goal", "value": "spend freely"}},
		}}, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &GuestResult{Text: "So you want room to spend without the fear."}, nil
}

// budgetedToolThenText adds a declared whole-turn budget.
type budgetedToolThenText struct {
	*toolThenTextCompleter
	budget time.Duration
}

func (c budgetedToolThenText) CompletionTimeout() time.Duration { return c.budget }

// A declared budget is the budget for the TURN, so the follow-up pass runs on
// what is left of it. Otherwise a two-pass turn takes twice the declared time
// and blows past the bridge's inbound deadline — the user then waits out a
// redelivery instead of reading a reply.
func TestGuestBrain_TurnBudgetCoversBothPasses(t *testing.T) {
	inner := &toolThenTextCompleter{sleep: 40 * time.Millisecond}
	brain := newGuestBrain(budgetedToolThenText{toolThenTextCompleter: inner, budget: 9 * time.Second}, zap.NewNop())

	out, err := brain.respond(context.Background(), &guestState{}, "build a rich life")
	if err != nil {
		t.Fatalf("respond failed: %v", err)
	}
	if out.text != "So you want room to spend without the fear." {
		t.Fatalf("unexpected reply %q", out.text)
	}
	if len(inner.deadlines) != 2 {
		t.Fatalf("expected a tool pass and a follow-up, got %d passes", len(inner.deadlines))
	}
	if !inner.deadlines[1].Equal(inner.deadlines[0]) {
		t.Fatalf("follow-up deadline %v extends past the turn's %v: the budget must cover the whole turn",
			inner.deadlines[1], inner.deadlines[0])
	}
	if left := time.Until(inner.deadlines[0]); left <= 0 || left > 9*time.Second {
		t.Fatalf("turn deadline is %v away, want it inside the declared 9s budget", left)
	}
}

// Without a declared budget, each pass gets its own fresh window — the retry
// behavior small-model completions rely on.
func TestGuestBrain_NoBudgetGivesEachPassItsOwnWindow(t *testing.T) {
	inner := &toolThenTextCompleter{sleep: 40 * time.Millisecond}
	brain := newGuestBrain(inner, zap.NewNop())

	if _, err := brain.respond(context.Background(), &guestState{}, "build a rich life"); err != nil {
		t.Fatalf("respond failed: %v", err)
	}
	if len(inner.deadlines) != 2 {
		t.Fatalf("expected two passes, got %d", len(inner.deadlines))
	}
	if !inner.deadlines[1].After(inner.deadlines[0]) {
		t.Fatalf("follow-up deadline %v should be its own window after %v",
			inner.deadlines[1], inner.deadlines[0])
	}
}

// A raised budget must buy ONE longer attempt, not two that together overrun the
// bridge's inbound deadline. The DI wiring leans on this: it sets a handful of
// seconds under the bridge timeout on the assumption that it is the whole turn's
// budget.
func TestGuestBrain_RaisedBudgetUsesASingleAttempt(t *testing.T) {
	bc := &budgetCompleter{timeout: 9 * time.Second}
	brain := newGuestBrain(bc, zap.NewNop())

	if _, err := brain.complete(context.Background(), "sys", nil, nil); err == nil {
		t.Fatal("expected the failing completer to surface an error")
	}
	if bc.calls != 1 {
		t.Fatalf("completion attempts = %d, want 1 — a custom budget owns its own retries", bc.calls)
	}
}

// A completer that declares no budget keeps the default two short attempts, so a
// single provider blip stays invisible.
func TestGuestBrain_DefaultBudgetStillRetries(t *testing.T) {
	fc := &fakeCompleter{err: fmt.Errorf("provider down")}
	brain := newGuestBrain(fc, zap.NewNop())

	if _, err := brain.complete(context.Background(), "sys", nil, nil); err == nil {
		t.Fatal("expected the failing completer to surface an error")
	}
	if len(fc.calls) != 2 {
		t.Fatalf("completion attempts = %d, want the default 2", len(fc.calls))
	}
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
