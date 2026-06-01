package ledger

import (
	"testing"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

// TestOrderedEntriesForLocking verifies that entries are always returned in a
// deterministic, globally-consistent order (sorted by account ID) regardless
// of the order the caller supplied them. This is what prevents lock-ordering
// deadlocks between two transactions touching the same accounts.
func TestOrderedEntriesForLocking(t *testing.T) {
	a := uuid.MustParse("00000000-0000-0000-0000-0000000000aa")
	b := uuid.MustParse("00000000-0000-0000-0000-0000000000bb")
	c := uuid.MustParse("00000000-0000-0000-0000-0000000000cc")

	mk := func(ids ...uuid.UUID) []entities.CreateEntryRequest {
		out := make([]entities.CreateEntryRequest, 0, len(ids))
		for _, id := range ids {
			out = append(out, entities.CreateEntryRequest{
				AccountID: id,
				EntryType: entities.EntryTypeDebit,
				Amount:    decimal.NewFromInt(1),
				Currency:  "USD",
			})
		}
		return out
	}

	// Two callers supplying the same accounts in opposite orders must produce
	// the same lock-acquisition order.
	forward := orderedEntriesForLocking(mk(a, b, c))
	reverse := orderedEntriesForLocking(mk(c, b, a))

	if len(forward) != 3 || len(reverse) != 3 {
		t.Fatalf("expected 3 entries, got %d/%d", len(forward), len(reverse))
	}
	for i := range forward {
		if forward[i].AccountID != reverse[i].AccountID {
			t.Fatalf("lock order diverged at %d: %s vs %s", i, forward[i].AccountID, reverse[i].AccountID)
		}
	}
	// Confirm ascending order a < b < c.
	if forward[0].AccountID != a || forward[1].AccountID != b || forward[2].AccountID != c {
		t.Fatalf("unexpected order: %v", []uuid.UUID{forward[0].AccountID, forward[1].AccountID, forward[2].AccountID})
	}
}

// TestOrderedEntriesForLocking_DoesNotMutateInput ensures the helper does not
// reorder the caller's original slice (entry write order is preserved upstream).
func TestOrderedEntriesForLocking_DoesNotMutateInput(t *testing.T) {
	a := uuid.MustParse("00000000-0000-0000-0000-0000000000aa")
	b := uuid.MustParse("00000000-0000-0000-0000-0000000000bb")
	input := []entities.CreateEntryRequest{{AccountID: b}, {AccountID: a}}

	_ = orderedEntriesForLocking(input)

	if input[0].AccountID != b || input[1].AccountID != a {
		t.Fatal("orderedEntriesForLocking mutated the caller's slice")
	}
}
