package miriam

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// TestNudgeFromSubscriptions_PicksHighestAnnualCost pins the persistence loop
// for a charge Miriam already surfaced: it reopens the highest-annual-cost
// recurring subscription and names the yearly figure.
func TestNudgeFromSubscriptions_PicksHighestAnnualCost(t *testing.T) {
	e := NewProactiveNudgeEngine(nil, nil, nil, nil, nil, zap.NewNop())
	uid := uuid.New()
	subs := []entities.MonoRecurringSubscription{
		{Merchant: "Coffee Club", Amount: 3000, Count: 4},
		{Merchant: "Gym", Amount: 20000, Count: 2},
	}
	n := e.nudgeFromSubscriptions(context.Background(), uid, subs)
	if n == nil {
		t.Fatal("expected a subscription follow-up nudge")
	}
	if n.TriggerType != entities.NudgeTriggerSubscription {
		t.Fatalf("expected subscription trigger, got %s", n.TriggerType)
	}
	if !strings.Contains(n.Message, "Gym") || !strings.Contains(n.Message, "240000") {
		t.Fatalf("expected the gym at its annual cost in the message, got: %q", n.Message)
	}
}

// TestNudgeFromSubscriptions_NoSubs returns nil for no detected subscriptions.
func TestNudgeFromSubscriptions_NoSubs(t *testing.T) {
	e := NewProactiveNudgeEngine(nil, nil, nil, nil, nil, zap.NewNop())
	if n := e.nudgeFromSubscriptions(context.Background(), uuid.New(), nil); n != nil {
		t.Fatalf("expected no nudge with no subscriptions, got %+v", n)
	}
}
