package repositories

import (
	"context"
	"errors"
	"testing"

	"github.com/lib/pq"
)

func TestIsSerializationFailure(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"serialization_failure 40001", &pq.Error{Code: "40001"}, true},
		{"deadlock_detected 40P01", &pq.Error{Code: "40P01"}, true},
		{"unique_violation 23505", &pq.Error{Code: "23505"}, false},
		{"errors.Wrap 40001", wrapPq(&pq.Error{Code: "40001"}), true},
		{"non-pq error", errors.New("boom"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsSerializationFailure(tt.err); got != tt.want {
				t.Fatalf("IsSerializationFailure(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestIsUniqueViolation(t *testing.T) {
	uErr := &pq.Error{Code: "23505", Constraint: "ledger_transactions_idempotency_key_key"}
	if !IsUniqueViolation(uErr, "") {
		t.Fatal("expected unique violation match with empty constraint")
	}
	if !IsUniqueViolation(uErr, "ledger_transactions_idempotency_key_key") {
		t.Fatal("expected unique violation match with exact constraint")
	}
	if IsUniqueViolation(uErr, "some_other_constraint") {
		t.Fatal("did not expect match against wrong constraint")
	}
	if IsUniqueViolation(&pq.Error{Code: "40001"}, "") {
		t.Fatal("serialization failure should not be a unique violation")
	}
	if IsUniqueViolation(nil, "") {
		t.Fatal("nil should not be a unique violation")
	}
	// Wrapped error should still be detected via errors.As.
	if !IsUniqueViolation(wrapPq(uErr), "") {
		t.Fatal("expected wrapped unique violation to be detected")
	}
}

func TestHasTx(t *testing.T) {
	if HasTx(context.Background()) {
		t.Fatal("background context should not carry a tx")
	}
	if HasTx(nil) { //nolint:staticcheck // intentionally testing nil ctx guard
		t.Fatal("nil context should not carry a tx")
	}
}

// wrapPq wraps a pq error using fmt-style %w semantics so errors.As can unwrap it.
func wrapPq(err error) error {
	return errorsJoinShim{inner: err}
}

type errorsJoinShim struct{ inner error }

func (e errorsJoinShim) Error() string { return "wrapped: " + e.inner.Error() }
func (e errorsJoinShim) Unwrap() error { return e.inner }
