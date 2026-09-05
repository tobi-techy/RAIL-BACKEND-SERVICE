package di

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	platform "github.com/rail-service/rail_service/internal/infrastructure/platform"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
)

// guestCompleterAdapter adapts the shared AI provider to the guest brain's
// minimal completion surface, so the pre-signup conversation uses the same
// provider stack as the rest of Miriam without the platform package importing
// the AI infrastructure.
type guestCompleterAdapter struct {
	provider ai.AIProvider
}

func (a *guestCompleterAdapter) CompleteGuest(ctx context.Context, systemPrompt string, messages []platform.GuestMessage, tools []platform.GuestToolDef) (*platform.GuestResult, error) {
	infraMessages := make([]ai.Message, 0, len(messages))
	for _, m := range messages {
		infraMessages = append(infraMessages, ai.Message{Role: m.Role, Content: m.Content})
	}
	infraTools := make([]ai.Tool, 0, len(tools))
	for _, t := range tools {
		infraTools = append(infraTools, ai.Tool{Name: t.Name, Description: t.Description, Parameters: t.Parameters})
	}

	req := &ai.ChatRequest{
		Messages:     infraMessages,
		SystemPrompt: systemPrompt,
		// Guest turns are short and frequent; the fast tier keeps the first
		// conversation snappy and cheap.
		ModelHint: "fast",
	}
	resp, err := a.provider.ChatCompletionWithTools(ctx, req, infraTools)
	if err != nil {
		return nil, fmt.Errorf("guest completion: %w", err)
	}

	out := &platform.GuestResult{Text: resp.Content}
	for _, tc := range resp.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, platform.GuestToolCall{Name: tc.Name, Arguments: tc.Arguments})
	}
	return out, nil
}

// guestTranscriptAdapter replays the pre-signup conversation into the user's
// first platform conversation so the authenticated Miriam opens with full
// context instead of a cold start.
type guestTranscriptAdapter struct {
	convRepo *repositories.ConversationRepository
}

func (a *guestTranscriptAdapter) AppendGuestTranscript(ctx context.Context, userID uuid.UUID, identity *entities.PlatformIdentity, threadID string, turns []platform.GuestMessage) error {
	if identity == nil || len(turns) == 0 {
		return nil
	}
	convID, _, err := a.convRepo.GetOrCreatePlatformConversation(ctx, userID, identity.Platform.String(), threadID, identity.ID)
	if err != nil {
		return fmt.Errorf("guest transcript conversation: %w", err)
	}
	for _, turn := range turns {
		msg := &entities.AIMessage{
			ConversationID: convID,
			Role:           turn.Role,
			Content:        turn.Content,
			Metadata:       map[string]interface{}{"guest": true},
		}
		if err := a.convRepo.CreateMessage(ctx, msg); err != nil {
			return fmt.Errorf("guest transcript message: %w", err)
		}
	}
	return nil
}
