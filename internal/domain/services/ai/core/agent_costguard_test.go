package core

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

// fakeCostGuard records Allow/Record invocations so we can assert the agent
// wires it correctly.
type fakeCostGuard struct {
	allowErr   error
	allowCalls int32
	recCalls   int32
	recCost    float64
}

func (f *fakeCostGuard) Allow(_ context.Context, _ uuid.UUID) error {
	atomic.AddInt32(&f.allowCalls, 1)
	return f.allowErr
}

func (f *fakeCostGuard) Record(_ context.Context, _ uuid.UUID, cost float64) {
	atomic.AddInt32(&f.recCalls, 1)
	f.recCost = cost
}

func TestChat_CostGuardAllowBlocksBeforeLLM(t *testing.T) {
	prov := &fakeProvider{responses: []*ai.ChatResponse{{Content: "should never run"}}}
	reg := &fakeRegistry{}
	deps := &Dependencies{
		AIProvider:   prov,
		ToolRegistry: reg,
		State:        fakeState{},
		Logger:       zap.NewNop(),
		CostGuard:    &fakeCostGuard{allowErr: errors.New("over daily ceiling")},
	}
	a := NewAgent(deps, DefaultConfig(), zap.NewNop())

	resp, err := a.Chat(context.Background(), uuid.New(), uuid.New(), "what's my balance?", ChatOptions{})
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	// The ceiling message is the same shape the legacy UsageService path returns.
	assert.Contains(t, resp.Content, "monthly AI usage limit", "should fall back to the ceiling response shape")
	// The provider's scripted "should never run" response was the first one;
	// call index must still be 0 because Allow blocked before the LLM call.
	assert.Equal(t, 0, prov.call)
}

func TestChat_CostGuardRecordsAfterSuccessfulCall(t *testing.T) {
	// Use a response that's >10 chars to pass the quality gate (which would
	// otherwise re-trigger the LLM and double the recorded cost).
	prov := &fakeProvider{responses: []*ai.ChatResponse{{Content: "Here's your full balance.", TokensUsed: 1000}}}
	reg := &fakeRegistry{}
	guard := &fakeCostGuard{}
	deps := &Dependencies{
		AIProvider:   prov,
		ToolRegistry: reg,
		State:        fakeState{},
		Logger:       zap.NewNop(),
		CostGuard:    guard,
	}
	a := NewAgent(deps, DefaultConfig(), zap.NewNop())

	_, err := a.Chat(context.Background(), uuid.New(), uuid.New(), "what's my balance?", ChatOptions{ModelHint: "gpt-4o-mini"})
	assert.NoError(t, err)
	assert.Equal(t, int32(1), atomic.LoadInt32(&guard.allowCalls))
	assert.Equal(t, int32(1), atomic.LoadInt32(&guard.recCalls), "Record should fire after a successful LLM call")
	// gpt-4o-mini is $0.60/1M tokens → 1000 tokens = $0.0006.
	assert.InDelta(t, 0.0006, guard.recCost, 0.0001)
}

func TestChat_NilCostGuardIsNoop(t *testing.T) {
	prov := &fakeProvider{responses: []*ai.ChatResponse{{Content: "ok"}}}
	reg := &fakeRegistry{}
	deps := &Dependencies{
		AIProvider:   prov,
		ToolRegistry: reg,
		State:        fakeState{},
		Logger:       zap.NewNop(),
		// CostGuard intentionally nil
	}
	a := NewAgent(deps, DefaultConfig(), zap.NewNop())
	resp, err := a.Chat(context.Background(), uuid.New(), uuid.New(), "what's my balance?", ChatOptions{})
	assert.NoError(t, err)
	assert.Equal(t, "ok", resp.Content)
}

func TestChat_CostGuardDoesNotRecordOnCeilingHit(t *testing.T) {
	prov := &fakeProvider{responses: []*ai.ChatResponse{{Content: "should never run"}}}
	reg := &fakeRegistry{}
	guard := &fakeCostGuard{allowErr: errors.New("over monthly ceiling")}
	deps := &Dependencies{
		AIProvider:   prov,
		ToolRegistry: reg,
		State:        fakeState{},
		Logger:       zap.NewNop(),
		CostGuard:    guard,
	}
	a := NewAgent(deps, DefaultConfig(), zap.NewNop())

	_, err := a.Chat(context.Background(), uuid.New(), uuid.New(), "what's my balance?", ChatOptions{})
	assert.NoError(t, err)
	// Allow fired (and refused); Record must NOT have fired because the
	// LLM call never happened.
	assert.Equal(t, int32(1), atomic.LoadInt32(&guard.allowCalls))
	assert.Equal(t, int32(0), atomic.LoadInt32(&guard.recCalls), "Record must not run when Allow refuses")
}

func TestChat_EstimateChatCost_FallbackPricingForUnknownModel(t *testing.T) {
	// Sanity-check the pricing table mirrors entities.ModelPricing.
	assert.InDelta(t, 0.0006, entities.EstimateCost("gpt-4o-mini", 1000).InexactFloat64(), 0.0001)
	assert.InDelta(t, 0.01, entities.EstimateCost("gpt-4o", 1000).InexactFloat64(), 0.0001)
	assert.InDelta(t, 0.0001, entities.EstimateCost("gemini-2.0-flash", 1000).InexactFloat64(), 0.0001)
	// Unknown model falls back to $10/M tokens.
	assert.InDelta(t, 0.01, entities.EstimateCost("never-existed", 1000).InexactFloat64(), 0.0001)
}
