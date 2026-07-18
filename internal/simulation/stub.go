package simulation

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	aiservice "github.com/rail-service/rail_service/internal/domain/services/ai"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
)

// stubChat is a deterministic, offline stand-in for the real orchestrator. It exists
// so the harness plumbing (seeding, grading, reporting) can be smoke-tested without
// API keys or cost. It is NOT a model — it echoes canned, quality-gate-friendly text
// and never moves money. Runs using it do not reflect real Miriam quality.
type stubChat struct{}

// NewStubChat returns a deterministic offline Miriam stand-in.
func NewStubChat() MiriamChat { return &stubChat{} }

func (s *stubChat) ChatWithConversationWithOptions(
	_ context.Context,
	_ uuid.UUID,
	_ *entities.AIConversation,
	message string,
	_ aiservice.ChatOptions,
) (*aiservice.ChatResponse, error) {
	// A short, personable, non-committal reply that passes the basic quality gate and
	// never claims to have moved money. Deterministic given the input.
	reply := "Yeah I hear you.\n\nWant me to walk through the numbers with you before we decide anything?"
	if strings.Contains(strings.ToLower(message), "withdraw") {
		reply = "Got it — moving money needs Face ID in the RAIL app for security.\n\nOpen the app and I'll have it ready to authorize. Sound good?"
	}
	return &aiservice.ChatResponse{
		Content:    reply,
		TokensUsed: len(message) / 4,
		Provider:   "stub",
	}, nil
}

// stubLLM is a deterministic offline LLM for the persona and judge in stub mode.
type stubLLM struct{}

// NewStubLLM returns a deterministic offline LLM.
func NewStubLLM() LLM { return &stubLLM{} }

func (s *stubLLM) Name() string { return "stub" }

func (s *stubLLM) ChatCompletion(_ context.Context, req *infraai.ChatRequest) (*infraai.ChatResponse, error) {
	// Persona prompts ask for a next user message; judge prompts ask for JSON. We
	// detect the judge by its system prompt and emit a neutral mid-scale verdict.
	if strings.Contains(req.SystemPrompt, "JSON object") {
		return &infraai.ChatResponse{
			Content:  `{"helpfulness":3,"understanding":3,"actionability":3,"impact":3,"rationale":"stub judge — deterministic mid-scale verdict"}`,
			Provider: "stub",
		}, nil
	}
	// Persona: end the conversation immediately so stub runs are single-turn.
	return &infraai.ChatResponse{
		Content:  "thanks, that's all i needed " + personaDoneToken,
		Provider: "stub",
	}, nil
}

// compile-time assurances.
var (
	_ MiriamChat = (*stubChat)(nil)
	_ LLM        = (*stubLLM)(nil)
)
