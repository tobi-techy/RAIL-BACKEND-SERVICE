package platform

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

// runToConsentConsented drives a fresh session through the phone path up to and
// including an affirmative consent reply, returning the final reply text.
func runToConsentConsented(t *testing.T, ob *ChatOnboarder, sender, phone string) string {
	t.Helper()
	step(t, ob, sender, "hi")
	step(t, ob, sender, "Ada")
	step(t, ob, sender, phone)
	consent := step(t, ob, sender, "123456")
	if !strings.Contains(consent, "I agree") {
		t.Fatalf("expected consent prompt, got: %q", consent)
	}
	return step(t, ob, sender, "yes")
}

// TestOnboarding_AccountExistsAndUsableAtPhoneVerification pins the instant
// account: the row is provisioned (Tier 1) the moment the phone is proven,
// before the terms poll, so a mid-conversation failure can never leave a
// verified-but-unprovisioned account that every read treats as missing.
func TestOnboarding_AccountExistsAndUsableAtPhoneVerification(t *testing.T) {
	ob, _, _, users, prov, _ := newTestOnboarder()
	sender := "+15557701"

	step(t, ob, sender, "hi")
	step(t, ob, sender, "Ada")
	step(t, ob, sender, "+2348012345678")
	step(t, ob, sender, "123456")

	if prov.calls != 1 {
		t.Fatalf("account must be provisioned at phone verification, prov calls=%d", prov.calls)
	}
	if len(users.created) != 1 {
		t.Fatalf("account row must exist after phone verification, created=%d", len(users.created))
	}
	if !strings.HasPrefix(users.created[0].Email, "phone+") {
		t.Fatalf("expected opaque placeholder until the email is collected, got %q", users.created[0].Email)
	}
}

// TestOnboarding_EmailAttachedToNewAccount pins the "collect the email after the
// conversation" flow: a verified address is written onto the session-created
// account, the session completes, and nothing duplicates the account.
func TestOnboarding_EmailAttachedToNewAccount(t *testing.T) {
	ob, _, ver, users, _, linker := newTestOnboarder()
	sender := "+15557702"

	askEmail := runToConsentConsented(t, ob, sender, "+2348012345678")
	if !strings.Contains(strings.ToLower(askEmail), "email") {
		t.Fatalf("expected email-attach prompt, got: %q", askEmail)
	}

	otpPrompt := step(t, ob, sender, "ada@example.com")
	if !strings.Contains(otpPrompt, "emailed") || len(ver.sentTo) != 2 {
		t.Fatalf("expected email OTP for the new address, got %q sent=%v", otpPrompt, ver.sentTo)
	}
	done := step(t, ob, sender, "123456")
	if len(users.emailUpdated) != 1 {
		t.Fatalf("expected the verified email attached to the new account, got %+v", users.emailUpdated)
	}
	got := users.emailUpdated[0]
	if got.id != users.created[0].ID || got.email != "ada@example.com" {
		t.Fatalf("email attached to the wrong account: %+v (created %+v)", got, users.created[0])
	}
	if users.byEmail["ada@example.com"] == nil || users.byEmail["ada@example.com"].ID != users.created[0].ID {
		t.Fatalf("email lookup should now resolve to the new account, got %+v", users.byEmail)
	}
	if linker.calls != 1 {
		t.Fatalf("expected exactly one link (consent), got %d", linker.calls)
	}
	if !strings.Contains(strings.ToLower(done), "you're in") {
		t.Fatalf("expected completion, got: %q", done)
	}
	if ob.HasSession(context.Background(), entities.PlatformIMessage, sender) {
		t.Fatal("session should be cleared after the email is attached")
	}
}

// TestOnboarding_EmailOwnerHandoffMovesChat pins the prod-database case: the
// person texts Miriam brand new, but the email they give already owns an account.
// The chat must move onto that account (phone attached, link moved) instead of
// creating a second account under the same address.
func TestOnboarding_EmailOwnerHandoffMovesChat(t *testing.T) {
	ob, _, _, users, prov, linker := newTestOnboarder()
	sender := "+15557703"
	ownerID := uuid.New()
	users.byEmail["back@example.com"] = &entities.UserProfile{ID: ownerID, Email: "back@example.com", IsActive: true}

	askEmail := runToConsentConsented(t, ob, sender, "+2348012345678")
	if !strings.Contains(strings.ToLower(askEmail), "email") {
		t.Fatalf("expected email-attach prompt, got: %q", askEmail)
	}

	otpPrompt := step(t, ob, sender, "back@example.com")
	if !strings.Contains(otpPrompt, "emailed") {
		t.Fatalf("expected email OTP for the existing address, got %q", otpPrompt)
	}
	done := step(t, ob, sender, "123456")

	// The verified phone was attached to the email-owned account: initial
	// provisioning (1) + consent (1) + attach-phone at handoff (1) = 3.
	if prov.calls != 3 || prov.lastPhone != "+2348012345678" {
		t.Fatalf("expected phone attached to the email account, prov=%+v", prov)
	}
	// The chat moved: placeholder link released, owner linked, no new row.
	if len(linker.unlinks) != 1 || linker.lastUser != ownerID || linker.lastSend != sender {
		t.Fatalf("expected handoff to the email account, linker=%+v", linker)
	}
	if linker.calls != 2 {
		t.Fatalf("expected consent link + handoff link, got %d", linker.calls)
	}
	if len(users.created) != 1 {
		t.Fatalf("handoff must not create a second account, created=%d", len(users.created))
	}
	if !strings.Contains(strings.ToLower(done), "you're in") {
		t.Fatalf("expected completion, got: %q", done)
	}
	if ob.HasSession(context.Background(), entities.PlatformIMessage, sender) {
		t.Fatal("session should be cleared after the handoff")
	}
}

// TestOnboarding_EmailAttachSkipFinishes pins the exit: declining the email
// question completes onboarding with the placeholder address instead of looping.
func TestOnboarding_EmailAttachSkipFinishes(t *testing.T) {
	ob, _, _, users, _, linker := newTestOnboarder()
	sender := "+15557704"

	askEmail := runToConsentConsented(t, ob, sender, "+2348012345678")
	if !strings.Contains(strings.ToLower(askEmail), "email") {
		t.Fatalf("expected email-attach prompt, got: %q", askEmail)
	}

	done := step(t, ob, sender, "no thanks")
	if len(users.emailUpdated) != 0 {
		t.Fatalf("a decline must not attach an email, got %+v", users.emailUpdated)
	}
	if linker.calls != 1 {
		t.Fatalf("expected the consent link to stand, got %d", linker.calls)
	}
	if !strings.Contains(strings.ToLower(done), "you're in") {
		t.Fatalf("expected completion, got: %q", done)
	}
	if ob.HasSession(context.Background(), entities.PlatformIMessage, sender) {
		t.Fatal("session should be cleared after skipping the email step")
	}
}

// TestOnboarding_AdoptedAccountNeverAskedForEmail pins the guard: when the chat
// adopts a pre-existing account (existing phone), it must not be re-pointed at a
// different email or asked to confirm one it already owns.
func TestOnboarding_AdoptedAccountNeverAskedForEmail(t *testing.T) {
	ob, _, _, users, _, _ := newTestOnboarder()
	sender := "+15557705"
	users.byPhone["+2348012345678"] = &entities.UserProfile{ID: uuid.New(), IsActive: true}

	done := runToConsentConsented(t, ob, sender, "+2348012345678")
	if strings.Contains(strings.ToLower(done), "email") {
		t.Fatalf("adopted account must not be asked for an email, got %q", done)
	}
	if !strings.Contains(strings.ToLower(done), "you're in") {
		t.Fatalf("expected completion, got: %q", done)
	}
}
