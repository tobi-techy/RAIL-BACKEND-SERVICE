package alpaca

import (
	"context"
	"fmt"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// Service provides high-level Alpaca operations
type Service struct {
	client  *Client
	funding *FundingAdapter
	sse     *SSEListener
	logger  *zap.Logger
}

// NewService creates a new Alpaca service
func NewService(client *Client, logger *zap.Logger) *Service {
	return &Service{
		client:  client,
		funding: NewFundingAdapter(client, logger),
		sse:     NewSSEListener(client, logger),
		logger:  logger,
	}
}

// Account operations
func (s *Service) CreateAccount(ctx context.Context, req *entities.AlpacaCreateAccountRequest) (*entities.AlpacaAccountResponse, error) {
	return s.client.CreateAccount(ctx, req)
}

func (s *Service) GetAccount(ctx context.Context, accountID string) (*entities.AlpacaAccountResponse, error) {
	return s.client.GetAccount(ctx, accountID)
}

// Trading operations
func (s *Service) CreateOrder(ctx context.Context, accountID string, req *entities.AlpacaCreateOrderRequest) (*entities.AlpacaOrderResponse, error) {
	// Validate order before submission
	if err := ValidateOrderRequest(req); err != nil {
		return nil, fmt.Errorf("order validation failed: %w", err)
	}
	return s.client.CreateOrder(ctx, accountID, req)
}

func (s *Service) GetOrder(ctx context.Context, accountID, orderID string) (*entities.AlpacaOrderResponse, error) {
	return s.client.GetOrder(ctx, accountID, orderID)
}

func (s *Service) ListOrders(ctx context.Context, accountID string, query map[string]string) ([]entities.AlpacaOrderResponse, error) {
	return s.client.ListOrders(ctx, accountID, query)
}

// Position operations
func (s *Service) ListPositions(ctx context.Context, accountID string) ([]entities.AlpacaPositionResponse, error) {
	return s.client.ListPositions(ctx, accountID)
}

// Account activities
func (s *Service) GetAccountActivities(ctx context.Context, accountID string, query map[string]string) ([]entities.AlpacaActivityResponse, error) {
	return s.client.GetAccountActivities(ctx, accountID, query)
}

// Portfolio history
func (s *Service) GetPortfolioHistory(ctx context.Context, accountID string, query map[string]string) (*entities.AlpacaPortfolioHistoryResponse, error) {
	return s.client.GetPortfolioHistory(ctx, accountID, query)
}

// Funding operations
func (s *Service) CreateJournal(ctx context.Context, req *entities.AlpacaJournalRequest) (*entities.AlpacaJournalResponse, error) {
	return s.funding.CreateJournal(ctx, req)
}

func (s *Service) GetAccountBalance(ctx context.Context, accountID string) (*entities.AlpacaAccountResponse, error) {
	return s.funding.GetAccountBalance(ctx, accountID)
}

// Event streaming
func (s *Service) ListenAccountEvents(ctx context.Context, handler func(SSEEvent) error) error {
	return s.sse.ListenAccountEvents(ctx, handler)
}

func (s *Service) ListenTradeEvents(ctx context.Context, handler func(SSEEvent) error) error {
	return s.sse.ListenTradeEvents(ctx, handler)
}
