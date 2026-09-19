package platform

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

// These tests attack the email-first onboarding path. Email is now the identity
// anchor, so the properties worth proving are: nothing is created or claimed
// before the code checks out, a proven address is actually recorded as verified,
// and claiming an existing account neither creates a duplicate nor asks for a
// phone.

// TestEmailFirst_NothingIsCreatedBeforeTheCodeVerifies pins the ordering
// invariant: giving an address is not enough. No row, no verification, no
// provisioning until the emailed code is entered.
func TestEmailFirst_NothingIsCreatedBeforeTheCodeVerifies(t *testing.T) {
	ob, _, ver, users, prov, linker := newTestOnboarder()
	sender := "+15559001"

	step(t, ob, sender, "hi")
	step(t, ob, sender, "Ada")
	reply := step(t, ob, sender, "ada@example.com")

	if len(ver.sentTo) != 1 || ver.sentTo[0] != "ada@example.com" {
		t.Fatalf("expected the code to go to the address, got: %v", ver.sentTo)
	}
	if !strings.Contains(strings.ToLower(reply), "emailed") {
		t.Fatalf("expected an OTP prompt, got: %q", reply)
	}
	if len(users.created) != 0 {
		t.Fatalf("no account may exist before the code verifies, created=%d", len(users.created))
	}
	if len(users.emailVerified) != 0 {
		t.Fatalf("no address may be marked verified before the code, got %v", users.emailVerified)
	}
	if prov.calls != 0 || linker.calls != 0 {
		t.Fatalf("nothing may be provisioned or linked yet, prov=%d link=%d", prov.calls, linker.calls)
	}
}

// TestEmailFirst_WrongCodeCreatesNothing pins that an unproven code is inert.
func TestEmailFirst_WrongCodeCreatesNothing(t *testing.T) {
	ob, _, _, users, _, linker := newTestOnboarder()
	sender := "+15559002"

	step(t, ob, sender, "hi")
	step(t, ob, sender, "Ada")
	step(t, ob, sender, "ada@example.com")
	reply := step(t, ob, sender, "999999") // not the valid code

	if !strings.Contains(strings.ToLower(reply), "didn't match") {
		t.Fatalf("expected a mismatch reply, got: %q", reply)
	}
	if len(users.created) != 0 || len(users.emailVerified) != 0 || linker.calls != 0 {
		t.Fatalf("a wrong code must change nothing: created=%d verified=%v link=%d",
			len(users.created), users.emailVerified, linker.calls)
	}
}

// TestEmailFirst_FreeAddressCreatesProvisionsAndVerifies pins the happy path of
// the anchor flip: a proven free address IS the signup, it is provisioned at the
// moment identity is proven (never a verified-but-unprovisioned row), the
// address is recorded as verified, and the chat is linked once.
func TestEmailFirst_FreeAddressCreatesProvisionsAndVerifies(t *testing.T) {
	ob, _, _, users, prov, linker := newTestOnboarder()
	sender := "+15559003"

	step(t, ob, sender, "hi")
	step(t, ob, sender, "Ada")
	step(t, ob, sender, "ada@example.com")
	consent := step(t, ob, sender, "123456")

	if !strings.Contains(consent, "I agree") {
		t.Fatalf("expected consent after the code, got: %q", consent)
	}
	if len(users.created) != 1 || users.created[0].Email != "ada@example.com" {
		t.Fatalf("expected exactly one account anchored on the address, got %+v", users.created)
	}
	// Provisioned at identity proof, not deferred to consent.
	if prov.calls != 1 {
		t.Fatalf("expected provisioning at email verification, got %d calls", prov.calls)
	}
	// The proven address is recorded as verified.
	if len(users.emailVerified) != 1 || users.emailVerified[0] != users.created[0].ID {
		t.Fatalf("expected the new account's address to be marked verified, got %v (created %v)",
			users.emailVerified, users.created[0].ID)
	}
	// An email-first signup has no phone to hand the provisioner.
	if prov.lastPhone != "" {
		t.Fatalf("an email-first signup must not invent a phone, got %q", prov.lastPhone)
	}

	done := step(t, ob, sender, "I agree")
	if linker.calls != 1 || linker.lastUser != users.created[0].ID {
		t.Fatalf("expected exactly one link to the new account, got %+v", linker)
	}
	if !strings.Contains(strings.ToLower(done), "you're in") {
		t.Fatalf("expected completion, got: %q", done)
	}
}

// TestEmailFirst_ExistingAccountClaimedWithoutAPhone is the existing-customer
// requirement: someone who already has an account texts Miriam, gives the
// address that owns it, and is let in. Previously this branch demanded a phone
// number before it would finish.
func TestEmailFirst_ExistingAccountClaimedWithoutAPhone(t *testing.T) {
	ob, _, ver, users, _, linker := newTestOnboarder()
	sender := "+15559004"
	ownerID := uuid.New()
	users.byEmail["back@example.com"] = &entities.UserProfile{
		ID: ownerID, Email: "back@example.com", IsActive: true,
	}

	step(t, ob, sender, "hi")
	step(t, ob, sender, "Zara")
	step(t, ob, sender, "back@example.com")
	consent := step(t, ob, sender, "123456")

	if !strings.Contains(consent, "I agree") {
		t.Fatalf("expected consent straight after the code (no phone step), got: %q", consent)
	}
	if len(ver.sentTo) != 1 {
		t.Fatalf("email alone must not trigger a second code, sent=%v", ver.sentTo)
	}
	if len(users.created) != 0 {
		t.Fatalf("claiming an existing account must not create a row, created=%d", len(users.created))
	}
	// An adopted account is never asked for an email it already owns.
	if strings.Contains(strings.ToLower(consent), "email") {
		t.Fatalf("must not ask an existing account for an email, got: %q", consent)
	}

	done := step(t, ob, sender, "yes")
	if linker.calls != 1 || linker.lastUser != ownerID {
		t.Fatalf("expected the chat linked to the existing account %s, got %+v", ownerID, linker)
	}
	if !strings.Contains(strings.ToLower(done), "you're in") {
		t.Fatalf("expected completion, got: %q", done)
	}
}

// TestEmailFirst_ClaimingAnAlreadyConnectedAccountSaysSo pins the honest
// failure. LinkVerified does not move an established link, so when a claimed
// account is already connected on the platform the flow must say so rather than
// celebrate a link that never happened (which would drop the person back into
// onboarding on their next message).
func TestEmailFirst_ClaimingAnAlreadyConnectedAccountSaysSo(t *testing.T) {
	ob, _, _, users, _, linker := newTestOnboarder()
	sender := "+15559006"
	ownerID := uuid.New()
	users.byEmail["taken@example.com"] = &entities.UserProfile{
		ID: ownerID, Email: "taken@example.com", IsActive: true,
	}
	// The account already belongs to a different chat handle.
	linker.alreadyLinkedTo = "+2348000000009"

	step(t, ob, sender, "hi")
	step(t, ob, sender, "Zara")
	step(t, ob, sender, "taken@example.com")
	step(t, ob, sender, "123456")
	reply := step(t, ob, sender, "yes")

	if !strings.Contains(strings.ToLower(reply), "already connected") {
		t.Fatalf("expected an 'already connected' message, got: %q", reply)
	}
	if !ob.HasSession(context.Background(), entities.PlatformIMessage, sender) {
		t.Fatal("the session must survive so the person can retry after disconnecting")
	}
}

// TestEmailFirst_LinkConflictIsReportedNotRetried pins the difference between a
// transient link failure and a definitive one. A unique-constraint conflict
// means the handle belongs to another account (or this account is already
// connected) — telling the person to "tap I agree to try again" would loop them
// forever. This is also the path a race between two concurrent claims takes.
func TestEmailFirst_LinkConflictIsReportedNotRetried(t *testing.T) {
	ob, _, _, users, _, linker := newTestOnboarder()
	sender := "+15559009"
	ownerID := uuid.New()
	users.byEmail["conflict@example.com"] = &entities.UserProfile{
		ID: ownerID, Email: "conflict@example.com", IsActive: true,
	}
	// The unique constraint on (platform, platform_user_id) fires.
	linker.linkErr = entities.ErrIdentityAlreadyLinked

	step(t, ob, sender, "hi")
	step(t, ob, sender, "Zara")
	step(t, ob, sender, "conflict@example.com")
	step(t, ob, sender, "123456")
	reply := step(t, ob, sender, "yes")

	if !strings.Contains(strings.ToLower(reply), "already connected") {
		t.Fatalf("a link conflict must be reported plainly, got: %q", reply)
	}
	if strings.Contains(strings.ToLower(reply), "try again") {
		t.Fatalf("retrying cannot resolve a conflict, so it must not be suggested: %q", reply)
	}
	if !ob.HasSession(context.Background(), entities.PlatformIMessage, sender) {
		t.Fatal("the session must survive so the person can disconnect and retry")
	}
}

// TestEmailFirst_OtpPromptDoesNotLeakAccountExistence pins the enumeration
// guard. The prompt used to read "I found your RAIL account…" on the
// existing-account path, which let anyone probe addresses to learn which ones
// are Rail customers. The wording must now be identical either way.
func TestEmailFirst_OtpPromptDoesNotLeakAccountExistence(t *testing.T) {
	promptFor := func(address string, known bool) string {
		ob, _, _, users, _, _ := newTestOnboarder()
		if known {
			users.byEmail[address] = &entities.UserProfile{
				ID: uuid.New(), Email: address, IsActive: true,
			}
		}
		sender := "+15559007"
		step(t, ob, sender, "hi")
		step(t, ob, sender, "Ada")
		return step(t, ob, sender, address)
	}

	free := promptFor("nobody@example.com", false)
	known := promptFor("customer@example.com", true)

	// The prompts necessarily contain the address the person just typed; only the
	// wording around it must be indistinguishable.
	norm := func(prompt, address string) string {
		return strings.ReplaceAll(prompt, address, "<address>")
	}
	if norm(free, "nobody@example.com") != norm(known, "customer@example.com") {
		t.Fatalf("the OTP prompt must not reveal whether an address is a customer:\n free  = %q\n known = %q",
			free, known)
	}
	if strings.Contains(strings.ToLower(free), "account") {
		t.Fatalf("prompt should not mention an account at all, got: %q", free)
	}
}

// TestEmailFirst_ClaimedAccountIsNotRepointed guards the handoff rule: an account
// this chat did NOT create must never be re-pointed at another address, because
// it may already hold money and history.
func TestEmailFirst_ClaimedAccountIsNotRepointed(t *testing.T) {
	ob, _, _, users, _, _ := newTestOnboarder()
	sender := "+15559005"
	ownerID := uuid.New()
	users.byEmail["owner@example.com"] = &entities.UserProfile{
		ID: ownerID, Email: "owner@example.com", IsActive: true,
	}

	step(t, ob, sender, "hi")
	step(t, ob, sender, "Zara")
	step(t, ob, sender, "owner@example.com")
	step(t, ob, sender, "123456")
	done := step(t, ob, sender, "yes")

	if len(users.emailUpdated) != 0 {
		t.Fatalf("an adopted account must never have its address rewritten, got %+v", users.emailUpdated)
	}
	if !strings.Contains(strings.ToLower(done), "you're in") {
		t.Fatalf("expected completion, got: %q", done)
	}
	if ob.HasSession(context.Background(), entities.PlatformIMessage, sender) {
		t.Fatal("session should be cleared once the claim completes")
	}
}
