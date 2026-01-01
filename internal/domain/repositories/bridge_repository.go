package repositories

import (
	"context"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// BridgeRepository defines the interface for bridge transaction persistence
type BridgeRepository interface {
	Create(ctx context.Context, bridge *entities.BridgeTransaction) error
	GetByID(ctx context.Context, id uuid.UUID) (*entities.BridgeTransaction, error)
	GetBySourceTxHash(ctx context.Context, txHash string) (*entities.BridgeTransaction, error)
	GetPendingBridges(ctx context.Context) ([]*entities.BridgeTransaction, error)
	GetByStatus(ctx context.Context, status entities.BridgeStatus) ([]*entities.BridgeTransaction, error)
	Update(ctx context.Context, bridge *entities.BridgeTransaction) error
	UpdateStatus(ctx context.Context, id uuid.UUID, status entities.BridgeStatus, errorMsg string) error
}
