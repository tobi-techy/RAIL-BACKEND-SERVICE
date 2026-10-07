package di

import (
	"context"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/infrastructure/platform"
)

// travelMessengerAdapter delivers booking receipts through the platform bridge
// dispatcher, folded into the iMessage thread as a critical receipt.
type travelMessengerAdapter struct {
	dispatcher *platform.BridgeDispatcher
}

func (m *travelMessengerAdapter) SendMessage(ctx context.Context, userID uuid.UUID, text string) error {
	return m.dispatcher.SendGenericNotification(ctx, userID, "Flight ticket", text)
}
