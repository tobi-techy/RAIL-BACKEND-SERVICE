package alpaca

import (
	"context"

	"github.com/stack-service/stack_service/internal/domain/entities"
	"github.com/stack-service/stack_service/pkg/logger"
)

// Adapter implements the AlpacaAdapter interface for the funding service
type Adapter struct {
	client *Client
	logger *logger.Logger
}

// NewAdapter creates a new Alpaca adapter
func NewAdapter(client *Client, logger *logger.Logger) *Adapter {
	return &Adapter{
		client: client,
		logger: logger,
	}
}

// GetAccount retrieves an Alpaca account by ID
func (a *Adapter) GetAccount(ctx context.Context, accountID string) (*entities.AlpacaAccountResponse, error) {
	return a.client.GetAccount(ctx, accountID)
}