package simulation

import (
	"context"
	"fmt"
	"strings"

	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
)

// LLM is the minimal completion surface the persona and judge need. The DI
// container's *ai.ProviderManager satisfies it; stubLLM provides a deterministic
// offline implementation for --stub-miriam self-tests.
type LLM interface {
	ChatCompletion(ctx context.Context, req *infraai.ChatRequest) (*infraai.ChatResponse, error)
	Name() string
}

// PersonaDriver generates the simulated user's next message from the conversation so
// far. It is deliberately kept "in character": it pursues a hidden goal, reacts to
// what Miriam actually said, and stops when the goal is met (emitting the DONE token).
type PersonaDriver struct {
	llm LLM
}

// NewPersonaDriver builds a persona driver over the given LLM.
func NewPersonaDriver(llm LLM) *PersonaDriver { return &PersonaDriver{llm: llm} }

const personaDoneToken = "[DONE]"

// Next returns the next user message and whether the persona considers the
// conversation complete. transcript is the ordered dialogue so far.
func (p *PersonaDriver) Next(ctx context.Context, sc *Scenario, transcript []Turn) (string, bool, error) {
	if p.llm == nil {
		// No LLM available: single-shot conversation, opening only.
		return "", true, nil
	}

	sys := buildPersonaSystemPrompt(sc)
	msgs := []infraai.Message{}
	// From the persona's point of view, Miriam's turns are the "user" it responds to
	// and its own turns are "assistant". Map accordingly.
	for _, t := range transcript {
		role := "assistant" // the persona's own prior lines
		if t.Role == roleMiriam {
			role = "user"
		}
		msgs = append(msgs, infraai.Message{Role: role, Content: t.Text})
	}

	resp, err := p.llm.ChatCompletion(ctx, &infraai.ChatRequest{
		SystemPrompt: sys,
		Messages:     msgs,
		MaxTokens:    150,
		Temperature:  infraai.Float64(0.4),
		ModelHint:    "fast",
	})
	if err != nil {
		return "", false, fmt.Errorf("persona completion: %w", err)
	}
	text := strings.TrimSpace(resp.Content)
	if text == "" {
		return "", true, nil
	}
	if strings.Contains(text, personaDoneToken) {
		text = strings.TrimSpace(strings.ReplaceAll(text, personaDoneToken, ""))
		return text, true, nil
	}
	return text, false, nil
}

func buildPersonaSystemPrompt(sc *Scenario) string {
	var b strings.Builder
	b.WriteString("You are role-playing a real person texting a personal-finance assistant named Miriam. ")
	b.WriteString("Stay fully in character as the user — never break character, never mention you are an AI or a simulation.\n\n")
	if sc.Persona.Profile != "" {
		b.WriteString("WHO YOU ARE: " + sc.Persona.Profile + "\n")
	}
	if sc.Persona.Goal != "" {
		b.WriteString("WHAT YOU WANT FROM THIS CHAT (your hidden goal): " + sc.Persona.Goal + "\n")
	}
	b.WriteString("\nRULES:\n")
	b.WriteString("- Write like a real person texting: short, casual, one or two sentences.\n")
	b.WriteString("- React to what Miriam actually said. Push back or ask a follow-up if she was vague.\n")
	b.WriteString("- Do not invent specific balances or numbers; ask Miriam instead.\n")
	b.WriteString("- When your goal is satisfied (or Miriam clearly can't help), reply with a brief closing line and append " + personaDoneToken + " on the same line.\n")
	b.WriteString("- Never output anything except your next text message.\n")
	return b.String()
}
