package core

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	"go.uber.org/zap"
)

// fakeProvider returns a scripted sequence of responses and captures the last request.
type fakeProvider struct {
	responses       []*ai.ChatResponse
	call            int
	lastReqMessages []ai.Message
}

func (f *fakeProvider) ChatCompletionWithTools(_ context.Context, req *ai.ChatRequest, _ []ai.Tool) (*ai.ChatResponse, error) {
	f.lastReqMessages = req.Messages
	r := f.responses[f.call]
	if f.call < len(f.responses)-1 {
		f.call++
	}
	return r, nil
}
func (f *fakeProvider) ChatCompletion(_ context.Context, req *ai.ChatRequest) (*ai.ChatResponse, error) {
	f.lastReqMessages = req.Messages
	return f.responses[len(f.responses)-1], nil
}
func (f *fakeProvider) Name() string                       { return "fake" }
func (f *fakeProvider) IsAvailable(_ context.Context) bool { return true }

// fakeRegistry records Execute calls and always offers a transfer_funds action tool.
type fakeRegistry struct{ executeCalls int }

func (r *fakeRegistry) Get(string) *Tool                   { return nil }
func (r *fakeRegistry) GetAll() []*Tool                    { return nil }
func (r *fakeRegistry) GetByCategory(ToolCategory) []*Tool { return nil }
func (r *fakeRegistry) GetByCategories([]ToolCategory) []*Tool {
	return []*Tool{{Name: "transfer_funds", Description: "move money", Category: CategoryFull}}
}
func (r *fakeRegistry) Execute(context.Context, uuid.UUID, string, map[string]interface{}, *Dependencies) (*ToolResult, error) {
	r.executeCalls++
	return &ToolResult{Data: map[string]interface{}{"ok": true}}, nil
}
func (r *fakeRegistry) ToInfrastructureTools() []map[string]interface{} { return nil }
func (r *fakeRegistry) Count() int                                      { return 1 }

type fakeState struct{}

func (fakeState) GetState(context.Context, uuid.UUID) (*UserState, error) {
	return &UserState{}, nil
}
func (fakeState) RefreshState(context.Context, uuid.UUID) error    { return nil }
func (fakeState) InvalidateCache(context.Context, uuid.UUID) error { return nil }

func newTestAgent(prov ai.AIProvider, reg ToolRegistry) *Agent {
	deps := &Dependencies{AIProvider: prov, ToolRegistry: reg, State: fakeState{}, Logger: zap.NewNop()}
	return NewAgent(deps, DefaultConfig(), zap.NewNop())
}

func actionThenFinal() []*ai.ChatResponse {
	return []*ai.ChatResponse{
		{ToolCalls: []ai.ToolCall{{ID: "1", Name: "transfer_funds", Arguments: map[string]interface{}{"amount": "50"}}}},
		{Content: "All handled for you."},
	}
}

func TestChat_MonitorBlocksActionExecution(t *testing.T) {
	reg := &fakeRegistry{}
	a := newTestAgent(&fakeProvider{responses: actionThenFinal()}, reg)

	_, err := a.Chat(context.Background(), uuid.New(), uuid.New(),
		"please transfer fifty dollars into my stash account right now",
		ChatOptions{ControlLevel: ControlLevelMonitor})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if reg.executeCalls != 0 {
		t.Fatalf("monitor mode must block action execution, got %d execute calls", reg.executeCalls)
	}
}

func TestChat_MonitorBlocksExecutionEngineTools(t *testing.T) {
	reg := &fakeRegistry{}
	responses := []*ai.ChatResponse{
		{ToolCalls: []ai.ToolCall{{ID: "1", Name: "block_merchant", Arguments: map[string]interface{}{"merchant": "DraftKings", "confirm": true}}}},
		{Content: "Blocked it for you."},
	}
	a := newTestAgent(&fakeProvider{responses: responses}, reg)

	_, err := a.Chat(context.Background(), uuid.New(), uuid.New(),
		"please block DraftKings on my card right now, I keep slipping",
		ChatOptions{ControlLevel: ControlLevelMonitor})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if reg.executeCalls != 0 {
		t.Fatalf("monitor mode must block execution-engine tools, got %d execute calls", reg.executeCalls)
	}
}

// In full mode a mutating action tool must NOT execute inline — it is staged
// for explicit user confirmation, and the model-supplied confirm flag is
// stripped. This is what keeps money from moving on a self-granted confirm over
// iMessage/WhatsApp.
func TestChat_FullStagesActionForConfirmation(t *testing.T) {
	reg := &fakeRegistry{}
	responses := []*ai.ChatResponse{
		{ToolCalls: []ai.ToolCall{{ID: "1", Name: "transfer_funds", Arguments: map[string]interface{}{"amount": "50", "confirm": true}}}},
		{Content: "All handled for you."},
	}
	a := newTestAgent(&fakeProvider{responses: responses}, reg)

	resp, err := a.Chat(context.Background(), uuid.New(), uuid.New(),
		"please transfer fifty dollars into my stash account right now",
		ChatOptions{ControlLevel: "full"})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if reg.executeCalls != 0 {
		t.Fatalf("action tools must be staged, not executed inline, got %d execute calls", reg.executeCalls)
	}
	if resp.PendingAction == nil {
		t.Fatal("expected a staged pending action for the fund move")
	}
	if resp.PendingAction.Type != "transfer_funds" {
		t.Fatalf("unexpected staged action: %s", resp.PendingAction.Type)
	}
	if _, ok := resp.PendingAction.Params["confirm"]; ok {
		t.Fatal("model-supplied confirm flag must be stripped from staged params")
	}
}

func TestChat_InjectsSystemContext(t *testing.T) {
	prov := &fakeProvider{responses: []*ai.ChatResponse{{Content: "You're doing solid this month, want the breakdown?"}}}
	a := newTestAgent(prov, &fakeRegistry{})

	marker := "[PERSONALITY MODE: roast]"
	_, err := a.Chat(context.Background(), uuid.New(), uuid.New(),
		"how am I doing with my money this month overall",
		ChatOptions{SystemContext: []string{marker}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	found := false
	for _, m := range prov.lastReqMessages {
		if m.Role == "system" && strings.Contains(m.Content, marker) {
			found = true
		}
	}
	if !found {
		t.Fatal("injected SystemContext was not passed to the provider")
	}
}
