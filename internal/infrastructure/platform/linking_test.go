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
