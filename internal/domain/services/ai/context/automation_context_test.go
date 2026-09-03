package context

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

func TestBuildAutomationContext_NilFn(t *testing.T) {
	b := NewBuilder(&ContextDeps{})
	got := b.buildAutomationContext(context.Background(), uuid.New())
	if got != "" {
		t.Fatalf("expected empty string for nil ListAutomationsFn, got %q", got)
	}
}

func TestBuildAutomationContext_NoAutomations(t *testing.T) {
	b := NewBuilder(&ContextDeps{
		ListAutomationsFn: func(ctx context.Context, userID uuid.UUID) ([]entities.MiriamAutomation, error) {
			return nil, nil
		},
	})
	got := b.buildAutomationContext(context.Background(), uuid.New())
	if got != "" {
		t.Fatalf("expected empty string for no automations, got %q", got)
	}
}

func TestBuildAutomationContext_OnlyInactive(t *testing.T) {
	b := NewBuilder(&ContextDeps{
		ListAutomationsFn: func(ctx context.Context, userID uuid.UUID) ([]entities.MiriamAutomation, error) {
			return []entities.MiriamAutomation{
				{Name: "paused rule", TriggerType: "schedule", ActionType: "notify", IsActive: false},
			}, nil
		},
	})
	got := b.buildAutomationContext(context.Background(), uuid.New())
	if got != "" {
		t.Fatalf("expected empty string when only inactive automations, got %q", got)
	}
}

func TestBuildAutomationContext_ActiveAutomations(t *testing.T) {
	lastRun := time.Date(2024, 3, 15, 10, 0, 0, 0, time.UTC)
	b := NewBuilder(&ContextDeps{
		ListAutomationsFn: func(ctx context.Context, userID uuid.UUID) ([]entities.MiriamAutomation, error) {
			return []entities.MiriamAutomation{
				{Name: "Friday Stash", TriggerType: "schedule", ActionType: "transfer_funds", IsActive: true, LastTriggeredAt: &lastRun},
				{Name: "Airtime Top-up", TriggerType: "schedule", ActionType: "pay_bill", IsActive: true},
			}, nil
		},
	})
	got := b.buildAutomationContext(context.Background(), uuid.New())
	if got == "" {
		t.Fatal("expected non-empty context for active automations")
	}
	if !strings.Contains(got, "ACTIVE AUTOMATIONS") {
		t.Errorf("expected header in output, got %q", got)
	}
	if !strings.Contains(got, "Friday Stash") {
		t.Errorf("expected automation name 'Friday Stash' in output, got %q", got)
	}
	if !strings.Contains(got, "Airtime Top-up") {
		t.Errorf("expected automation name 'Airtime Top-up' in output, got %q", got)
	}
	if !strings.Contains(got, "last ran Mar 15") {
		t.Errorf("expected last triggered date in output, got %q", got)
	}
	if !strings.Contains(got, "2 active rule") {
		t.Errorf("expected count in output, got %q", got)
	}
}

func TestBuildAutomationContext_MixedActiveInactive(t *testing.T) {
	b := NewBuilder(&ContextDeps{
		ListAutomationsFn: func(ctx context.Context, userID uuid.UUID) ([]entities.MiriamAutomation, error) {
			return []entities.MiriamAutomation{
				{Name: "Active Rule", TriggerType: "schedule", ActionType: "notify", IsActive: true},
				{Name: "Paused Rule", TriggerType: "schedule", ActionType: "notify", IsActive: false},
			}, nil
		},
	})
	got := b.buildAutomationContext(context.Background(), uuid.New())
	if !strings.Contains(got, "Active Rule") {
		t.Errorf("expected active rule in output, got %q", got)
	}
	if strings.Contains(got, "Paused Rule") {
		t.Errorf("inactive rule should not appear in output, got %q", got)
	}
	if !strings.Contains(got, "1 active rule") {
		t.Errorf("expected count of 1 active rule, got %q", got)
	}
}

func TestSummarizeAutomationConfig_Schedule(t *testing.T) {
	triggerConfig := json.RawMessage(`{"schedule":"every friday at 9am"}`)
	actionConfig := json.RawMessage(`{"amount":5000,"direction":"spend_to_stash"}`)
	summary := summarizeAutomationConfig("schedule", triggerConfig, "transfer_funds", actionConfig)
	if !strings.Contains(summary, "schedule: every friday at 9am") {
		t.Errorf("expected schedule in summary, got %q", summary)
	}
	if !strings.Contains(summary, "amount: 5000") {
		t.Errorf("expected amount in summary, got %q", summary)
	}
	if !strings.Contains(summary, "direction: spend_to_stash") {
		t.Errorf("expected direction in summary, got %q", summary)
	}
}

func TestSummarizeAutomationConfig_Empty(t *testing.T) {
	summary := summarizeAutomationConfig("schedule", nil, "notify", nil)
	if summary != "" {
		t.Errorf("expected empty summary for nil configs, got %q", summary)
	}
}

func TestSummarizeAutomationConfig_PaydayDay(t *testing.T) {
	triggerConfig := json.RawMessage(`{"payday_day":25}`)
	summary := summarizeAutomationConfig("payday", triggerConfig, "notify", nil)
	if !strings.Contains(summary, "payday: day 25") {
		t.Errorf("expected payday day in summary, got %q", summary)
	}
}
