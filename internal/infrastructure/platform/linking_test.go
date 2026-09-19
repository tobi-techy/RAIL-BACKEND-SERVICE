package platform

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

func TestLinking_ConfirmBindsActualSender(t *testing.T) {
	repo := newFakeRepo()
	ls := NewLinkingService(repo, 900)
	userID := uuid.New()

	res, err := ls.InitiateHandshake(context.Background(), userID, entities.PlatformIMessage)
	if err != nil {
		t.Fatalf("initiate: %v", err)
	}

	// The identity starts with a placeholder platform_user_id, not a real one.
	if got := res.Identity.PlatformUserID; got == "+15550000" {
		t.Fatalf("identity should not be bound before confirm")
	}

	identity, err := ls.ConfirmHandshake(context.Background(), res.Token, entities.PlatformIMessage, "+15550000")
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if identity.PlatformUserID != "+15550000" {
		t.Fatalf("expected identity bound to sender, got %q", identity.PlatformUserID)
	}
	if identity.LinkedAt == nil {
		t.Fatal("expected LinkedAt set after confirm")
	}
}

func TestLinking_ConfirmRejectsExpiredToken(t *testing.T) {
	repo := newFakeRepo()
	ls := NewLinkingService(repo, 900)
	now := time.Now()
	ls.now = func() time.Time { return now }

	res, err := ls.InitiateHandshake(context.Background(), uuid.New(), entities.PlatformIMessage)
	if err != nil {
		t.Fatalf("initiate: %v", err)
	}

	ls.now = func() time.Time { return now.Add(16 * time.Minute) }
	if _, err := ls.ConfirmHandshake(context.Background(), res.Token, entities.PlatformIMessage, "+15550000"); err == nil {
		t.Fatal("expected expired token to be rejected")
	}
}

func TestLinking_ConfirmRejectsAlreadyLinkedElsewhere(t *testing.T) {
	repo := newFakeRepo()
	ls := NewLinkingService(repo, 900)

	// An existing user already owns this iMessage account.
	linkedIdentity(repo, "+15550000")

	res, err := ls.InitiateHandshake(context.Background(), uuid.New(), entities.PlatformIMessage)
	if err != nil {
		t.Fatalf("initiate: %v", err)
	}
	if _, err := ls.ConfirmHandshake(context.Background(), res.Token, entities.PlatformIMessage, "+15550000"); err == nil {
		t.Fatal("expected rejection when sender already linked to another user")
	}
}

func TestLinking_ConfirmRejectsWrongPlatform(t *testing.T) {
	repo := newFakeRepo()
	ls := NewLinkingService(repo, 900)

	res, err := ls.InitiateHandshake(context.Background(), uuid.New(), entities.PlatformIMessage)
	if err != nil {
		t.Fatalf("initiate: %v", err)
	}
	if _, err := ls.ConfirmHandshake(context.Background(), res.Token, entities.PlatformWhatsApp, "+15550000"); err == nil {
		t.Fatal("expected rejection for platform mismatch")
	}
}

func TestLinking_LinkVerifiedBindsWithoutToken(t *testing.T) {
	repo := newFakeRepo()
	ls := NewLinkingService(repo, 900)
	userID := uuid.New()

	identity, err := ls.LinkVerified(context.Background(), userID, entities.PlatformIMessage, "+2348012345678")
	if err != nil {
		t.Fatalf("LinkVerified: %v", err)
	}
	if identity.LinkedAt == nil || identity.PlatformUserID != "+2348012345678" {
		t.Fatalf("expected bound identity, got %+v", identity)
	}

	// Resolver should now find the user by the verified sender id.
	resolver := NewUserResolver(repo)
	resolved, err := resolver.Resolve(context.Background(), entities.PlatformIMessage, "+2348012345678")
	if err != nil {
		t.Fatalf("resolve after LinkVerified: %v", err)
	}
	if resolved.UserID != userID {
		t.Fatalf("expected resolved user %s, got %s", userID, resolved.UserID)
	}
}

func TestLinking_LinkVerifiedIdempotentForSameUser(t *testing.T) {
	repo := newFakeRepo()
	ls := NewLinkingService(repo, 900)
	userID := uuid.New()

	if _, err := ls.LinkVerified(context.Background(), userID, entities.PlatformIMessage, "+2348012345678"); err != nil {
		t.Fatalf("first link: %v", err)
	}
	if _, err := ls.LinkVerified(context.Background(), userID, entities.PlatformIMessage, "+2348012345678"); err != nil {
		t.Fatalf("second link should be idempotent, got: %v", err)
	}
}

func TestLinking_LinkVerifiedRejectsSenderLinkedElsewhere(t *testing.T) {
	repo := newFakeRepo()
	ls := NewLinkingService(repo, 900)

	linkedIdentity(repo, "+2348012345678") // owned by some other user

	if _, err := ls.LinkVerified(context.Background(), uuid.New(), entities.PlatformIMessage, "+2348012345678"); err == nil {
		t.Fatal("expected rejection when sender already linked to another user")
	}
}

// TestLinking_LinkVerifiedNeverRebindsAnAlreadyLinkedAccount pins the invariant
// the email-only claim path depends on.
//
// Claiming an existing account on email alone is safe precisely because this
// method refuses to move a link that is already established: when the target
// account already owns an identity on the platform, a different sender gets the
// existing identity back and the stored platform_user_id is left alone. If this
// ever started rebinding, a proven email address would be sufficient to move
// someone else's chat link onto an attacker's handle.
func TestLinking_LinkVerifiedNeverRebindsAnAlreadyLinkedAccount(t *testing.T) {
	repo := newFakeRepo()
	ls := NewLinkingService(repo, 900)
	owner := uuid.New()
	const ownersHandle = "+2348000000001"
	const claimantsHandle = "+2348000000002"

	if _, err := ls.LinkVerified(context.Background(), owner, entities.PlatformIMessage, ownersHandle); err != nil {
		t.Fatalf("owner link: %v", err)
	}

	// A different handle "claims" the same account.
	identity, err := ls.LinkVerified(context.Background(), owner, entities.PlatformIMessage, claimantsHandle)
	if err != nil {
		t.Fatalf("claim returned an error (callers now handle this): %v", err)
	}
	if identity.PlatformUserID != ownersHandle {
		t.Fatalf("the established link must not be rebound: got %q, want %q",
			identity.PlatformUserID, ownersHandle)
	}

	// The claimant's handle must not resolve to the account.
	resolver := NewUserResolver(repo)
	if _, err := resolver.Resolve(context.Background(), entities.PlatformIMessage, claimantsHandle); err == nil {
		t.Fatal("a claimant's handle must not resolve to the account")
	}
	// ...and the owner's handle still must.
	resolved, err := resolver.Resolve(context.Background(), entities.PlatformIMessage, ownersHandle)
	if err != nil || resolved.UserID != owner {
		t.Fatalf("the owner's link must survive the claim attempt, got %v %v", resolved, err)
	}
}
