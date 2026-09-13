package platform

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

// fakeMonoLinker scripts the GuestMonoLinker surface.
type fakeMonoLinker struct {
	initiateURL string
	initiateErr error
	analysis    *entities.MonoSpendingAnalysis
	analysisErr error
	attached    []string
}

func (f *fakeMonoLinker) InitiateGuestLinking(_ context.Context, guestToken, _, _, _ string) (string, error) {
	if f.initiateErr != nil {
		return "", f.initiateErr
	}
	return f.initiateURL, nil
}

func (f *fakeMonoLinker) GetGuestSpendingAnalysis(_ context.Context, _ string, _ int) (*entities.MonoSpendingAnalysis, error) {
	if f.analysisErr != nil {
		return nil, f.analysisErr
	}
	return f.analysis, nil
}

func (f *fakeMonoLinker) AttachGuestAccountToUser(_ context.Context, guestToken string, _ uuid.UUID) (*entities.MonoLinkedAccount, error) {
	f.attached = append(f.attached, guestToken)
	return &entities.MonoLinkedAccount{MonoAccountID: "mono-1"}, nil
}

// TestGuestMono_ConnectBankSendsTappableLink pins the pre-signup bank link: the
// guest brain calls connect_bank, the executor initiates a Mono link, stores the
// URL + guest token, and returns the link inline. No account is required.
func TestGuestMono_ConnectBankSendsTappableLink(t *testing.T) {
	fc := &fakeCompleter{default_: fakeCompletion{
		text:      "Want me to look at your real spending so we're not guessing?",
		toolCalls: []GuestToolCall{{Name: "connect_bank"}},
	}}
	ob, store, _, _, _, _ := newBrainOnboarder(fc)
	linker := &fakeMonoLinker{initiateURL: "https://mono.example/connect/abc"}
	ob.SetGuestMonoLinker(linker)

	reply := step(t, ob, "+15552101", "yes, look at my spending")
	if !strings.Contains(reply, "https://mono.example/connect/abc") {
		t.Fatalf("expected the Mono link in the reply, got: %q", reply)
	}

	var st guestState
	if err := store.Get(context.Background(), onboardingKey(entities.PlatformIMessage, "+15552101"), &st); err != nil {
		t.Fatalf("session not persisted: %v", err)
	}
	if st.MonoLinkURL != "https://mono.example/connect/abc" {
		t.Fatalf("expected MonoLinkURL persisted, got %q", st.MonoLinkURL)
	}
	if st.GuestToken == "" {
		t.Fatal("expected a guest token to be generated")
	}
	if st.MonoLinked {
		t.Fatal("guest should not be marked linked before completing the flow")
	}
}

// TestGuestMono_MarkLinkedFlipsFlag pins that completing the bank link flips the
// session so the guest brain can deliver the aha on the next turn.
func TestGuestMono_MarkLinkedFlipsFlag(t *testing.T) {
	fc := &fakeCompleter{default_: fakeCompletion{
		text:      "Want me to look at your real spending?",
		toolCalls: []GuestToolCall{{Name: "connect_bank"}},
	}}
	ob, store, _, _, _, _ := newBrainOnboarder(fc)
	ob.SetGuestMonoLinker(&fakeMonoLinker{initiateURL: "https://mono.example/connect/abc"})

	step(t, ob, "+15552102", "yes")

	var st guestState
	key := onboardingKey(entities.PlatformIMessage, "+15552102")
	if err := store.Get(context.Background(), key, &st); err != nil {
		t.Fatalf("session not persisted: %v", err)
	}
	if st.GuestToken == "" {
		t.Fatal("expected a guest token")
	}
	if err := ob.MarkGuestMonoLinked(context.Background(), st.GuestToken); err != nil {
		t.Fatalf("MarkGuestMonoLinked: %v", err)
	}
	if err := store.Get(context.Background(), key, &st); err != nil {
		t.Fatalf("session reload: %v", err)
	}
	if !st.MonoLinked {
		t.Fatal("expected MonoLinked to be true after completion")
	}
}

// TestGuestMono_AnalysisDeliversAha pins the aha moment: once the bank is
// linked, the guest brain calls get_bank_statement_analysis, the executor pulls
// the real spending picture, stores it, and re-runs the brain so the reply is
// grounded in actual numbers.
func TestGuestMono_AnalysisDeliversAha(t *testing.T) {
	fc := &fakeCompleter{responses: []fakeCompletion{
		{text: "(link)", toolCalls: []GuestToolCall{{Name: "connect_bank"}}},
		{text: "(analysis)", toolCalls: []GuestToolCall{{Name: "get_bank_statement_analysis"}}},
		{text: "See that? You spent 47k on eating out. Worth it? Maybe. But now you know."},
	}}
	ob, store, _, _, _, _ := newBrainOnboarder(fc)
	ob.SetGuestMonoLinker(&fakeMonoLinker{
		initiateURL: "https://mono.example/connect/abc",
		analysis: &entities.MonoSpendingAnalysis{
			TotalCredits:     150000,
			TotalDebits:      120000,
			TransactionCount: 40,
			ByCategory:       []entities.MonoCategoryBreakdown{{Category: "Eating out", Amount: 47000, Count: 12, Percent: 0.39}},
		},
	})

	key := onboardingKey(entities.PlatformIMessage, "+15552103")
	step(t, ob, "+15552103", "yes, look at my spending")

	var st guestState
	if err := store.Get(context.Background(), key, &st); err != nil {
		t.Fatalf("session: %v", err)
	}
	if st.GuestToken == "" {
		t.Fatal("expected guest token")
	}
	if err := ob.MarkGuestMonoLinked(context.Background(), st.GuestToken); err != nil {
		t.Fatalf("mark linked: %v", err)
	}

	reply := step(t, ob, "+15552103", "ok what do you see")
	if !strings.Contains(reply, "47k") && !strings.Contains(reply, "eating out") {
		t.Fatalf("expected the aha reply grounded in the analysis, got: %q", reply)
	}

	if err := store.Get(context.Background(), key, &st); err != nil {
		t.Fatalf("session reload: %v", err)
	}
	if !strings.Contains(st.MonoSummary, "Eating out") {
		t.Fatalf("expected the spending picture persisted, got: %q", st.MonoSummary)
	}
}

// TestGuestMono_AttachOnSignup claims the guest-linked account for the new user
// at consent, so the financial picture carries into the authenticated
// relationship.
func TestGuestMono_AttachOnSignup(t *testing.T) {
	fc := &fakeCompleter{responses: []fakeCompletion{
		{text: "(link)", toolCalls: []GuestToolCall{{Name: "connect_bank"}}},
		{text: "drop your number and I'll get your split running", toolCalls: []GuestToolCall{{Name: "start_signup", Arguments: map[string]interface{}{"reason": "first deposit"}}}},
	}}
	ob, store, _, _, prov, linker := newBrainOnboarder(fc)
	mono := &fakeMonoLinker{initiateURL: "https://mono.example/connect/abc"}
	ob.SetGuestMonoLinker(mono)

	key := onboardingKey(entities.PlatformIMessage, "+15552104")
	step(t, ob, "+15552104", "yes, look at my spending")

	var st guestState
	if err := store.Get(context.Background(), key, &st); err != nil {
		t.Fatalf("session: %v", err)
	}
	if err := ob.MarkGuestMonoLinked(context.Background(), st.GuestToken); err != nil {
		t.Fatalf("mark linked: %v", err)
	}

	// start_signup moves to the phone ask.
	step(t, ob, "+15552104", "I want to make my first deposit")
	// Phone in its own message → OTP, then code, then consent.
	step(t, ob, "+15552104", "+15551234567")
	step(t, ob, "+15552104", "123456")
	step(t, ob, "+15552104", "I agree")

	if len(mono.attached) != 1 {
		t.Fatalf("expected the guest mono account to be attached at signup, got %d attaches", len(mono.attached))
	}
	if mono.attached[0] != st.GuestToken {
		t.Fatalf("attached with wrong token: %q", mono.attached[0])
	}
	if prov.calls != 1 {
		t.Fatalf("expected provisioning to run once, got %d", prov.calls)
	}
	if linker.calls != 1 {
		t.Fatalf("expected linking to run once, got %d", linker.calls)
	}
}
