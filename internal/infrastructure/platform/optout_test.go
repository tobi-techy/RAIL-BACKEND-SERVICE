package platform

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

// fakeOptOutStore is an in-memory OptOutStore.
type fakeOptOutStore struct {
	optedOut map[string]bool
	reasons  []string
	resumes  []string
	err      error
}

func optOutKey(platform, senderID string) string { return platform + ":" + senderID }

func (f *fakeOptOutStore) IsOptedOut(_ context.Context, platform, senderID string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.optedOut[optOutKey(platform, senderID)], nil
}

func (f *fakeOptOutStore) OptOut(_ context.Context, platform, senderID, reason string) error {
	if f.err != nil {
		return f.err
	}
	if f.optedOut == nil {
		f.optedOut = map[string]bool{}
	}
	f.optedOut[optOutKey(platform, senderID)] = true
	f.reasons = append(f.reasons, reason)
	return nil
}

func (f *fakeOptOutStore) Resume(_ context.Context, platform, senderID string) error {
	if f.err != nil {
		return f.err
	}
	delete(f.optedOut, optOutKey(platform, senderID))
	f.resumes = append(f.resumes, senderID)
	return nil
}

// inboundJSON builds the same payload shape the bridge posts.
func inboundJSON(t *testing.T, sender, text string) []byte {
	t.Helper()
	raw, err := json.Marshal(InboundMessage{
		Platform: entities.PlatformIMessage,
		UserID:   sender,
		ThreadID: sender,
		Text:     text,
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestOptOut_StopSuppressesThenStaysSilent(t *testing.T) {
	proc, sent, _ := newTestProcessor(newFakeRepo(), &fakeOrchestrator{})
	store := &fakeOptOutStore{}
	proc.SetOptOutStore(store)
	ctx := context.Background()

	if err := proc.Process(ctx, inboundJSON(t, "+15559001", "STOP")); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(*sent) != 1 {
		t.Fatalf("expected exactly one acknowledgement, got %d", len(*sent))
	}
	if !strings.Contains(strings.ToLower((*sent)[0].Text), "won't message you again") {
		t.Fatalf("expected a clear acknowledgement, got: %q", (*sent)[0].Text)
	}
	if !store.optedOut[optOutKey("imessage", "+15559001")] {
		t.Fatal("the sender must be recorded as opted out")
	}

	// A repeat request must not be answered: replying to someone who asked us to
	// stop is the exact harm this control exists to prevent.
	*sent = nil
	if err := proc.Process(ctx, inboundJSON(t, "+15559001", "stop")); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(*sent) != 0 {
		t.Fatalf("a repeat STOP must stay silent, got %d messages", len(*sent))
	}
}

func TestOptOut_StartResumes(t *testing.T) {
	proc, sent, _ := newTestProcessor(newFakeRepo(), &fakeOrchestrator{})
	store := &fakeOptOutStore{optedOut: map[string]bool{optOutKey("imessage", "+15559002"): true}}
	proc.SetOptOutStore(store)

	if err := proc.Process(context.Background(), inboundJSON(t, "+15559002", "START")); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if store.optedOut[optOutKey("imessage", "+15559002")] {
		t.Fatal("START must clear the opt-out")
	}
	if len(*sent) != 1 || !strings.Contains(strings.ToLower((*sent)[0].Text), "back on") {
		t.Fatalf("expected a resume confirmation, got %v", *sent)
	}
}

// TestOptOut_OrdinaryMessageContainingStopIsNotAnOptOut guards the most damaging
// false positive: unsubscribing someone because they used the word in a sentence.
func TestOptOut_OrdinaryMessageContainingStopIsNotAnOptOut(t *testing.T) {
	for _, text := range []string{
		"stop the transfer to Ada",
		"how do I stop a subscription",
		"please stop sending me so many nudges",
		"stop",
	} {
		proc, _, _ := newTestProcessor(newFakeRepo(), &fakeOrchestrator{})
		store := &fakeOptOutStore{}
		proc.SetOptOutStore(store)

		if err := proc.Process(context.Background(), inboundJSON(t, "+15559003", text)); err != nil {
			t.Fatalf("Process(%q): %v", text, err)
		}

		optedOut := store.optedOut[optOutKey("imessage", "+15559003")]
		// A bare "stop" IS the opt-out; sentences merely containing the word are not.
		wantOptedOut := text == "stop"
		if optedOut != wantOptedOut {
			t.Fatalf("text %q: opted out = %v, want %v", text, optedOut, wantOptedOut)
		}
	}
}

// TestOptOut_StopWhileAnActionIsStagedCancelsInstead pins the interaction with
// confirmations. On platforms without tap buttons a bare "stop" is how someone
// declines a payment, so while an action is pending it must cancel that action
// rather than silently unsubscribing them.
func TestOptOut_StopWhileAnActionIsStagedCancelsInstead(t *testing.T) {
	repo := newFakeRepo()
	linkedIdentity(repo, "+15559004")
	orch := &fakeOrchestrator{pendingAction: true}
	proc, _, _ := newTestProcessor(repo, orch)
	store := &fakeOptOutStore{}
	proc.SetOptOutStore(store)

	if err := proc.Process(context.Background(), inboundJSON(t, "+15559004", "stop")); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(store.optedOut) != 0 {
		t.Fatalf("a staged-action cancel must not opt the person out: %v", store.optedOut)
	}
	if orch.cancelCalls != 1 {
		t.Fatalf("expected the staged action to be cancelled, cancelCalls=%d", orch.cancelCalls)
	}
}

// TestOptOut_NilStoreLeavesBehaviourUnchanged keeps the feature opt-in: with no
// store the processor behaves exactly as before.
func TestOptOut_NilStoreLeavesBehaviourUnchanged(t *testing.T) {
	repo := newFakeRepo()
	linkedIdentity(repo, "+15559005")
	orch := &fakeOrchestrator{}
	proc, sent, _ := newTestProcessor(repo, orch)

	if err := proc.Process(context.Background(), inboundJSON(t, "+15559005", "STOP")); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(*sent) == 0 {
		t.Fatal("with no opt-out store the message must flow to the normal path")
	}
	if orch.lastMessage != "STOP" {
		t.Fatalf("expected the message to reach the orchestrator, got %q", orch.lastMessage)
	}
}

// TestOptOut_StoreFailureFailsOpenButStillAnswers pins the degraded-dependency
// policy: a lookup error must not silence messaging globally, and the record
// attempt is reported rather than swallowed.
func TestOptOut_StoreFailureFailsOpenButStillAnswers(t *testing.T) {
	proc, sent, _ := newTestProcessor(newFakeRepo(), &fakeOrchestrator{})
	store := &fakeOptOutStore{err: errors.New("database down")}
	proc.SetOptOutStore(store)

	if err := proc.Process(context.Background(), inboundJSON(t, "+15559006", "STOP")); err != nil {
		t.Fatalf("Process must not fail when the store is down: %v", err)
	}
	if len(*sent) == 0 {
		t.Fatal("a store failure must not suppress the acknowledgement")
	}
}

func TestNormaliseControlWord(t *testing.T) {
	cases := map[string]string{
		"STOP":                         "stop",
		"  Stop.  ":                    "stop",
		"stop!":                        "stop",
		"OPT OUT":                      "opt out",
		"stop the transfer to Ada":     "",
		"please stop":                  "please stop",
		"":                             "",
		"   ":                          "",
		"what is my balance?":          "",
		"stop and also do something x": "",
	}
	for input, want := range cases {
		if got := normaliseControlWord(input); got != want {
			t.Errorf("normaliseControlWord(%q) = %q, want %q", input, got, want)
		}
	}
}
