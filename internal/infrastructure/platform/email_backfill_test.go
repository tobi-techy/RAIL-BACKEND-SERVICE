package platform

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

// WS4: an account created by a phone-first chat signup still carries an opaque
// placeholder address, so it can receive no receipts and cannot reset anything
// by email. Those accounts are asked ONCE, the next time they message, and the
// proven address is written onto their own account.

type fakeAccountReader struct {
	email string
	err   error
}

func (f *fakeAccountReader) EmailForUser(_ context.Context, _ uuid.UUID) (string, error) {
	return f.email, f.err
}

func TestIsPlaceholderEmail(t *testing.T) {
	placeholder := placeholderEmail()
	if !IsPlaceholderEmail(placeholder) {
		t.Fatalf("the generated placeholder must be recognised: %q", placeholder)
	}
	for _, real := range []string{
		"ada@example.com",
		"PHONE+abc@placeholder.invalid.example.com", // suffix must match exactly
		"someone@placeholder.invalid",               // missing the phone+ prefix
		"",
	} {
		if IsPlaceholderEmail(real) {
			t.Fatalf("%q must not be treated as a placeholder", real)
		}
	}
}

type fakeUserGetter struct {
	profile *entities.UserProfile
	err     error
}

func (f *fakeUserGetter) GetByID(_ context.Context, _ uuid.UUID) (*entities.UserProfile, error) {
	return f.profile, f.err
}

func TestAccountReader_EmailForUser(t *testing.T) {
	ctx := context.Background()

	reader := NewAccountReader(&fakeUserGetter{
		profile: &entities.UserProfile{Email: "ada@example.com"},
	})
	got, err := reader.EmailForUser(ctx, uuid.New())
	if err != nil || got != "ada@example.com" {
		t.Fatalf("got %q, %v", got, err)
	}

	// A deleted account is an empty address, not an error: it simply never gets
	// asked.
	reader = NewAccountReader(&fakeUserGetter{profile: nil})
	if got, err := reader.EmailForUser(ctx, uuid.New()); err != nil || got != "" {
		t.Fatalf("missing user must read as empty, got %q, %v", got, err)
	}

	reader = NewAccountReader(&fakeUserGetter{err: errors.New("db down")})
	if _, err := reader.EmailForUser(ctx, uuid.New()); err == nil {
		t.Fatal("a lookup failure must be reported so the caller can skip the ask")
	}
}

// TestEmailBackfill_AsksOnceAfterAnsweringTheMessage pins the UX rules: the ask
// must not replace the answer to what the person actually asked, and it must
// happen exactly once.
func TestEmailBackfill_AsksOnceAfterAnsweringTheMessage(t *testing.T) {
	repo := newFakeRepo()
	sender := "+15559101"
	linkedIdentity(repo, sender)
	ob, _, _, _, _, _ := newTestOnboarder()
	orch := &fakeOrchestrator{reply: &PlatformReply{Text: "your balance is $10"}}
	proc, sent, _ := newTestProcessor(repo, orch)
	proc.SetOnboarder(ob)
	proc.SetEmailBackfill(&fakeAccountReader{email: placeholderEmail()})

	// First message: answered, then asked.
	if err := proc.Process(context.Background(), inboundJSON(t, sender, "how am I doing")); err != nil {
		t.Fatalf("Process: %v", err)
	}
	// First message: answered, then asked. Asserted by order rather than index,
	// because the reply path also emits a typing indicator.
	answerIdx, askIdx := -1, -1
	for i, m := range *sent {
		low := strings.ToLower(m.Text)
		if strings.Contains(m.Text, "your balance is $10") && answerIdx == -1 {
			answerIdx = i
		}
		if strings.Contains(low, "what email") && askIdx == -1 {
			askIdx = i
		}
	}
	if answerIdx == -1 {
		t.Fatalf("expected the person's answer to be sent, got %v", *sent)
	}
	if askIdx == -1 {
		t.Fatalf("expected the email ask to be sent, got %v", *sent)
	}
	if askIdx < answerIdx {
		t.Fatalf("the ask must follow the answer, never replace it: %v", *sent)
	}

	// Second message: no repeat ask.
	*sent = nil
	if err := proc.Process(context.Background(), inboundJSON(t, sender, "and now")); err != nil {
		t.Fatalf("Process: %v", err)
	}
	for _, m := range *sent {
		if strings.Contains(strings.ToLower(m.Text), "what email") {
			t.Fatalf("the ask must happen once, got a repeat: %q", m.Text)
		}
	}
}

// TestEmailBackfill_NeverAsksAccountsWithARealAddress is the guard against
// pestering everyone.
func TestEmailBackfill_NeverAsksAccountsWithARealAddress(t *testing.T) {
	repo := newFakeRepo()
	sender := "+15559102"
	linkedIdentity(repo, sender)
	ob, _, _, _, _, _ := newTestOnboarder()
	proc, sent, _ := newTestProcessor(repo, &fakeOrchestrator{})
	proc.SetOnboarder(ob)
	proc.SetEmailBackfill(&fakeAccountReader{email: "ada@example.com"})

	if err := proc.Process(context.Background(), inboundJSON(t, sender, "hello")); err != nil {
		t.Fatalf("Process: %v", err)
	}
	for _, m := range *sent {
		if strings.Contains(strings.ToLower(m.Text), "what email") {
			t.Fatalf("an account with a real address must never be asked: %q", m.Text)
		}
	}
}

// TestEmailBackfill_TurnRoutesToTheOnboarder pins that the address (and the code)
// go to the tool that can verify and attach them, not to the general agent.
func TestEmailBackfill_TurnRoutesToTheOnboarder(t *testing.T) {
	repo := newFakeRepo()
	sender := "+15559103"
	linkedIdentity(repo, sender)
	ob, _, _, _, _, _ := newTestOnboarder()
	orch := &fakeOrchestrator{}
	proc, _, _ := newTestProcessor(repo, orch)
	proc.SetOnboarder(ob)
	proc.SetEmailBackfill(&fakeAccountReader{email: placeholderEmail()})
	ctx := context.Background()

	// Starting the flow seeds the session.
	if _, err := ob.StartEmailBackfill(ctx, entities.PlatformIMessage, sender, uuid.New()); err != nil {
		t.Fatalf("StartEmailBackfill: %v", err)
	}
	if !ob.IsEmailBackfillSession(ctx, entities.PlatformIMessage, sender) {
		t.Fatal("expected an active backfill session")
	}

	if err := proc.Process(ctx, inboundJSON(t, sender, "ada@example.com")); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if orch.lastMessage != "" {
		t.Fatalf("a backfill turn must not reach the general agent, got %q", orch.lastMessage)
	}
}

// TestEmailBackfill_AttachesToTheirOwnAccount is the point of the feature: a free
// address is proven and written onto the account they are already inside.
func TestEmailBackfill_AttachesToTheirOwnAccount(t *testing.T) {
	ob, _, ver, users, _, linker := newTestOnboarder()
	ctx := context.Background()
	sender := "+15559104"
	ownID := uuid.New()

	if _, err := ob.StartEmailBackfill(ctx, entities.PlatformIMessage, sender, ownID); err != nil {
		t.Fatalf("StartEmailBackfill: %v", err)
	}
	otpPrompt := step(t, ob, sender, "ada@example.com")
	if !strings.Contains(otpPrompt, "emailed") {
		t.Fatalf("expected an email OTP, got %q", otpPrompt)
	}
	done := step(t, ob, sender, "123456")

	if len(users.emailUpdated) != 1 || users.emailUpdated[0].id != ownID {
		t.Fatalf("the address must attach to their own account %s, got %+v", ownID, users.emailUpdated)
	}
	if len(users.emailVerified) != 1 || users.emailVerified[0] != ownID {
		t.Fatalf("the proven address must be marked verified on their account, got %v", users.emailVerified)
	}
	if linker.calls != 0 || len(linker.unlinks) != 0 {
		t.Fatalf("backfill must not touch the link, got %+v", linker)
	}
	if !strings.Contains(strings.ToLower(done), "you're in") {
		t.Fatalf("expected completion, got %q", done)
	}
	if len(ver.sentTo) != 1 || ver.sentTo[0] != "ada@example.com" {
		t.Fatalf("expected one code to the new address, got %v", ver.sentTo)
	}
}

// TestEmailBackfill_MidFlowQuestionIsAnsweredNotSwallowed is the regression for
// a bug this test wrote the fix for: while the backfill waits for an address,
// routing every message to the onboarder meant a fresh question was answered
// with another email prompt and the question itself vanished.
func TestEmailBackfill_MidFlowQuestionIsAnsweredNotSwallowed(t *testing.T) {
	repo := newFakeRepo()
	sender := "+15559106"
	linkedIdentity(repo, sender)
	ob, _, _, _, _, _ := newTestOnboarder()
	orch := &fakeOrchestrator{reply: &PlatformReply{Text: "you have $42"}}
	proc, sent, _ := newTestProcessor(repo, orch)
	proc.SetOnboarder(ob)
	proc.SetEmailBackfill(&fakeAccountReader{email: placeholderEmail()})
	ctx := context.Background()

	if _, err := ob.StartEmailBackfill(ctx, entities.PlatformIMessage, sender, uuid.New()); err != nil {
		t.Fatalf("StartEmailBackfill: %v", err)
	}
	*sent = nil

	if err := proc.Process(ctx, inboundJSON(t, sender, "what is my balance")); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if orch.lastMessage != "what is my balance" {
		t.Fatalf("a fresh question must reach the agent, got %q", orch.lastMessage)
	}
	for _, m := range *sent {
		if strings.Contains(strings.ToLower(m.Text), "what email") {
			t.Fatalf("the email prompt must not replace the answer, got %q", m.Text)
		}
	}
}

// TestIsBackfillAnswer pins which messages belong to the exchange.
func TestIsBackfillAnswer(t *testing.T) {
	for _, yes := range []string{"ada@example.com", "123456", "skip", "no thanks"} {
		if !isBackfillAnswer(yes) {
			t.Errorf("%q should be part of the backfill exchange", yes)
		}
	}
	for _, no := range []string{"what is my balance", "hello", "12345", "yes"} {
		if isBackfillAnswer(no) {
			t.Errorf("%q is a fresh request, not part of the exchange", no)
		}
	}
}

// TestEmailBackfill_RefusesAnAddressOnAnotherAccount pins the guard that makes
// this safe for an already-linked person: typing an address that happens to
// belong to someone else must never move their chat onto that account.
func TestEmailBackfill_RefusesAnAddressOnAnotherAccount(t *testing.T) {
	ob, _, _, users, _, linker := newTestOnboarder()
	ctx := context.Background()
	sender := "+15559105"
	ownID := uuid.New()
	otherID := uuid.New()
	users.byEmail["someone-else@example.com"] = &entities.UserProfile{
		ID: otherID, Email: "someone-else@example.com", IsActive: true,
	}

	if _, err := ob.StartEmailBackfill(ctx, entities.PlatformIMessage, sender, ownID); err != nil {
		t.Fatalf("StartEmailBackfill: %v", err)
	}
	step(t, ob, sender, "someone-else@example.com")
	reply := step(t, ob, sender, "123456")

	if !strings.Contains(strings.ToLower(reply), "another rail account") {
		t.Fatalf("expected a refusal, got %q", reply)
	}
	if len(users.emailUpdated) != 0 {
		t.Fatalf("nothing may be attached, got %+v", users.emailUpdated)
	}
	if linker.calls != 0 || len(linker.unlinks) != 0 {
		t.Fatalf("the chat must never be handed to another account, got %+v", linker)
	}
	if ob.IsEmailBackfillSession(ctx, entities.PlatformIMessage, sender) {
		t.Fatal("the session should be cleared after the refusal")
	}
}
