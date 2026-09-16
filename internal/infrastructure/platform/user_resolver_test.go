package platform

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

func TestUserResolver_UnlinkedIsNotRetryable(t *testing.T) {
	repo := newFakeRepo()
	_, err := NewUserResolver(repo).Resolve(context.Background(), entities.PlatformIMessage, "+2347000000001")
	if err == nil {
		t.Fatal("expected an error for an unknown sender")
	}
	if IsRetryable(err) {
		t.Fatalf("unknown sender must be a definitive unlinked, not retryable: %v", err)
	}
	if !strings.Contains(err.Error(), "unlinked") {
		t.Fatalf("expected a definitive unlinked error, got %v", err)
	}
}

func TestUserResolver_HandshakePendingIsNotRetryable(t *testing.T) {
	repo := newFakeRepo()
	pi := &entities.PlatformIdentity{
		ID:             uuid.New(),
		UserID:         uuid.New(),
		Platform:       entities.PlatformIMessage,
		PlatformUserID: "+2347000000002",
		LinkedAt:       nil,
	}
	if err := repo.Create(context.Background(), pi); err != nil {
		t.Fatal(err)
	}
	_, err := NewUserResolver(repo).Resolve(context.Background(), entities.PlatformIMessage, pi.PlatformUserID)
	if err == nil {
		t.Fatal("expected an error while handshake is pending")
	}
	if IsRetryable(err) {
		t.Fatalf("pending handshake must not be retryable: %v", err)
	}
}

func TestUserResolver_LookupFailureIsRetryable(t *testing.T) {
	// A transient DB blip must NOT be classified as "unlinked". The processor
	// requeues it so a linked user is never demoted to the guest path
	// mid-conversation.
	repo := newFakeRepo()
	repo.lookupErr = errors.New("connection reset")
	_, err := NewUserResolver(repo).Resolve(context.Background(), entities.PlatformIMessage, "+2347000000003")
	if err == nil {
		t.Fatal("expected the lookup error to surface")
	}
	if !IsRetryable(err) {
		t.Fatalf("a lookup failure must be Retryable so the message is requeued, got %T %v", err, err)
	}
	if strings.Contains(err.Error(), "unlinked") {
		t.Fatalf("a DB blip must never be labelled unlinked: %v", err)
	}
}

func TestUserResolver_TouchLastUsedFailureStillResolves(t *testing.T) {
	repo := newFakeRepo()
	pi := linkedIdentity(repo, "+2347000000004")
	repo.touchErr = errors.New("redis down")
	resolved, err := NewUserResolver(repo).Resolve(context.Background(), entities.PlatformIMessage, pi.PlatformUserID)
	if err != nil {
		t.Fatalf("TouchLastUsed blip must not un-resolve a verified user: %v", err)
	}
	if resolved == nil || resolved.UserID != pi.UserID {
		t.Fatalf("expected the linked user to resolve, got %+v", resolved)
	}
}
