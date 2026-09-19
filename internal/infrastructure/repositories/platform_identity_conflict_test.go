package repositories

import (
	"errors"
	"fmt"
	"testing"

	"github.com/lib/pq"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

// TestMapPlatformIdentityConflict pins the conversion of the unique-constraint
// violations on platform_identities into a domain error.
//
// Without it a concurrent claim (two chats finishing a handshake for the same
// handle) surfaced a raw pq error, which onboarding could neither recognise nor
// explain to the person.
func TestMapPlatformIdentityConflict(t *testing.T) {
	t.Run("unique violation becomes a domain conflict", func(t *testing.T) {
		raw := &pq.Error{Code: "23505", Constraint: "uq_platform_user"}
		got := mapPlatformIdentityConflict(raw)
		if !errors.Is(got, entities.ErrIdentityAlreadyLinked) {
			t.Fatalf("expected ErrIdentityAlreadyLinked, got %v", got)
		}
	})

	t.Run("wrapped unique violation is still recognised", func(t *testing.T) {
		wrapped := fmt.Errorf("insert identity: %w", &pq.Error{Code: "23505"})
		if !errors.Is(mapPlatformIdentityConflict(wrapped), entities.ErrIdentityAlreadyLinked) {
			t.Fatal("a wrapped unique violation must still map to the domain conflict")
		}
	})

	t.Run("other database errors pass through untouched", func(t *testing.T) {
		raw := &pq.Error{Code: "42P01"} // undefined_table
		got := mapPlatformIdentityConflict(raw)
		if errors.Is(got, entities.ErrIdentityAlreadyLinked) {
			t.Fatal("an unrelated database error must not be reported as a link conflict")
		}
		if !errors.Is(got, raw) {
			t.Fatalf("the original error must be preserved, got %v", got)
		}
	})

	t.Run("nil passes through", func(t *testing.T) {
		if err := mapPlatformIdentityConflict(nil); err != nil {
			t.Fatalf("nil must stay nil, got %v", err)
		}
	})

	t.Run("non-pq errors pass through", func(t *testing.T) {
		raw := errors.New("connection reset")
		if got := mapPlatformIdentityConflict(raw); !errors.Is(got, raw) {
			t.Fatalf("non-pq error must be preserved, got %v", got)
		}
	})
}
