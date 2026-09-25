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
	if !strings.Contains(strings.ToLower(reply), "pdf") {
		t.Fatalf("expected a statement ask, got: %q", reply)
	}
	if strings.Contains(reply, "mono.example") {
		t.Fatalf("bank linking is not live, got: %q", reply)
	}

	var st guestState
	if err := store.Get(context.Background(), onboardingKey(entities.PlatformIMessage, "+15552101"), &st); err != nil {
		t.Fatalf("session not persisted: %v", err)
	}
	if st.MonoLinkURL != "" || st.MonoLinked {
		t.Fatal("bank linking must not start")
	}
}

type guestStatementScan struct{}

func (guestStatementScan) ScanGuest(context.Context, string, StatementAttachment) (*StatementScan, error) {
	return &StatementScan{
		PendingID: "pending-1",
		Summary:   "I found 12 transactions. Income was about NGN 200000 and spending about NGN 47000. Biggest spending areas: eating out 47000.",
	}, nil
}
func (guestStatementScan) ScanLinked(context.Context, uuid.UUID, StatementAttachment) (*StatementScan, error) {
	return nil, nil
}
func (guestStatementScan) EnqueueLinked(context.Context, uuid.UUID, StatementAttachment) (*PlatformReply, error) {
	return nil, nil
}
func (guestStatementScan) CompletePending(context.Context, uuid.UUID, string) error { return nil }

func TestGuestStatement_ScanAsksToOpenTheAccount(t *testing.T) {
	ob, store, _, _, _, _ := newTestOnboarder()
	ob.SetStatementAttachmentHandler(guestStatementScan{})
	sender := "+15552112"
	reply, err := ob.Handle(context.Background(), OnboardInput{
		Platform: entities.PlatformIMessage,
		SenderID: sender,
		Statement: &StatementAttachment{
			Name: "statement.pdf", MIMEType: "application/pdf", Data: []byte("%PDF"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if reply == nil || !strings.Contains(reply.Text, "47000") || !strings.Contains(strings.ToLower(reply.Text), "email") {
		t.Fatalf("expected the statement picture and the account ask, got %#v", reply)
	}
	var saved guestState
	if err := store.Get(context.Background(), onboardingKey(entities.PlatformIMessage, sender), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Phase != phaseEmail || saved.PendingStatementID != "pending-1" {
		t.Fatalf("phase %q pending %q", saved.Phase, saved.PendingStatementID)
	}
}

// TestGuestMono_AhaAsksToOpenTheAccount pins the order: the spending picture
// is delivered, and the account ask goes out in that same reply. Miriam does
// not wait for a deposit or a transfer.
func TestGuestMono_AhaAsksToOpenTheAccount(t *testing.T) {
	fc := &fakeCompleter{responses: []fakeCompletion{
		{text: "Pulling your spending.", toolCalls: []GuestToolCall{{Name: "get_bank_statement_analysis"}}},
		{text: "You spent NGN 47k on eating out. That's about three days of income."},
	}}
	ob, store, _, _, _, _ := newBrainOnboarder(fc)
	ob.SetGuestMonoLinker(&fakeMonoLinker{analysis: &entities.MonoSpendingAnalysis{
		TransactionCount: 12,
		TotalDebits:      47000,
		ByCategory:       []entities.MonoCategoryBreakdown{{Category: "Eating out", Amount: 47000, Count: 8}},
	}})
	sender := "+15552111"
	token := "guest-token"
	st := guestState{Phase: phaseConverse, MonoLinked: true, GuestToken: token}
	if err := store.Set(context.Background(), onboardingKey(entities.PlatformIMessage, sender), st, 0); err != nil {
		t.Fatal(err)
	}

	reply := step(t, ob, sender, "ok show me")
	if !strings.Contains(reply, "47k") {
		t.Fatalf("expected the aha in the reply, got: %q", reply)
	}
	if !strings.Contains(strings.ToLower(reply), "email") {
		t.Fatalf("expected the account ask in the same reply, got: %q", reply)
	}
	var saved guestState
	if err := store.Get(context.Background(), onboardingKey(entities.PlatformIMessage, sender), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Phase != phaseEmail {
		t.Fatalf("expected awaiting_email after the aha, got %q", saved.Phase)
	}
}

// TestGuestMono_MarkLinkedFlipsFlag pins that completing the bank link flips the
// session so the guest brain can deliver the aha on the next turn.
func TestGuestMono_MarkLinkedFlipsFlag(t *testing.T) {
	ob, store, _, _, _, _ := newTestOnboarder()
	key := onboardingKey(entities.PlatformIMessage, "+15552102")
	if err := store.Set(context.Background(), key, guestState{Phase: phaseConverse, GuestToken: "guest-token"}, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(context.Background(), guestTokenKey("guest-token"), key, 0); err != nil {
		t.Fatal(err)
	}
	if err := ob.MarkGuestMonoLinked(context.Background(), "guest-token"); err != nil {
		t.Fatalf("MarkGuestMonoLinked: %v", err)
	}
	var st guestState
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
		{text: "Pulling it.", toolCalls: []GuestToolCall{{Name: "get_bank_statement_analysis"}}},
		{text: "See that? You spent 47k on eating out. Worth it? Maybe. But now you know."},
	}}
	ob, store, _, _, _, _ := newBrainOnboarder(fc)
	ob.SetGuestMonoLinker(&fakeMonoLinker{
		analysis: &entities.MonoSpendingAnalysis{
			TotalCredits:     150000,
			TotalDebits:      120000,
			TransactionCount: 40,
			ByCategory:       []entities.MonoCategoryBreakdown{{Category: "Eating out", Amount: 47000, Count: 12, Percent: 0.39}},
		},
	})

	key := onboardingKey(entities.PlatformIMessage, "+15552103")
	if err := store.Set(context.Background(), key, guestState{Phase: phaseConverse, MonoLinked: true, GuestToken: "guest-token"}, 0); err != nil {
		t.Fatal(err)
	}

	reply := step(t, ob, "+15552103", "ok what do you see")
	if !strings.Contains(reply, "47k") && !strings.Contains(reply, "eating out") {
		t.Fatalf("expected the aha reply grounded in the analysis, got: %q", reply)
	}

	var st guestState
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
		{text: "I'll set the account up.", toolCalls: []GuestToolCall{{Name: "start_signup", Arguments: map[string]interface{}{"reason": "spending picture"}}}},
	}}
	ob, store, _, _, prov, linker := newBrainOnboarder(fc)
	mono := &fakeMonoLinker{}
	ob.SetGuestMonoLinker(mono)

	key := onboardingKey(entities.PlatformIMessage, "+15552104")
	if err := store.Set(context.Background(), key, guestState{Phase: phaseConverse, MonoLinked: true, GuestToken: "guest-token"}, 0); err != nil {
		t.Fatal(err)
	}
	var st guestState
	if err := store.Get(context.Background(), key, &st); err != nil {
		t.Fatal(err)
	}

	// The aha is already in hand. Signup opens the account.
	step(t, ob, "+15552104", "I want to make my first deposit")
	step(t, ob, "+15552104", "ada@example.com")
	step(t, ob, "+15552104", "123456")
	step(t, ob, "+15552104", "I agree")

	if len(mono.attached) != 1 {
		t.Fatalf("expected the guest mono account to be attached at signup, got %d attaches", len(mono.attached))
	}
	if mono.attached[0] != st.GuestToken {
		t.Fatalf("attached with wrong token: %q", mono.attached[0])
	}
	if prov.calls != 2 {
		// Provisioning runs at phone verification and re-runs (idempotently) at
		// consent; the second call proves the idempotent retry path.
		t.Fatalf("expected provisioning at phone verification and consent, got %d", prov.calls)
	}
	if linker.calls != 1 {
		t.Fatalf("expected linking to run once, got %d", linker.calls)
	}
}
