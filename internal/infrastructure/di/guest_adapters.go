package di

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	platform "github.com/rail-service/rail_service/internal/infrastructure/platform"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
)

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
