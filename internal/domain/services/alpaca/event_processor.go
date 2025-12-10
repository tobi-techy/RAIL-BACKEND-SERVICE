package alpaca

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// OrderRepository interface for order updates
type OrderRepository interface {
	GetByAlpacaOrderID(ctx context.Context, alpacaOrderID string) (*entities.InvestmentOrder, error)
	UpdateFromAlpaca(ctx context.Context, alpacaOrderID string, status entities.AlpacaOrderStatus, filledQty, filledAvgPrice *string, filledAt *time.Time) error
}

// PositionRepository interface for position updates
type PositionRepository interface {
	Upsert(ctx context.Context, pos *entities.InvestmentPosition) error
	GetByUserAndSymbol(ctx context.Context, userID uuid.UUID, symbol string) (*entities.InvestmentPosition, error)
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.InvestmentPosition, error)
	DeleteByUserAndSymbol(ctx context.Context, userID uuid.UUID, symbol string) error
}

// EventRepository interface for event persistence
type EventRepository interface {
	Create(ctx context.Context, event *entities.AlpacaEvent) error
	MarkProcessed(ctx context.Context, id uuid.UUID, errorMsg *string) error
	GetUnprocessed(ctx context.Context, limit int) ([]*entities.AlpacaEvent, error)
}

// EventProcessor handles Alpaca webhook and SSE events
type EventProcessor struct {
	accountRepo  AccountRepository
	orderRepo    OrderRepository
	positionRepo PositionRepository
	eventRepo    EventRepository
	balanceRepo  BalanceRepository
	logger       *zap.Logger
}

func NewEventProcessor(
	accountRepo AccountRepository,
	orderRepo OrderRepository,
	positionRepo PositionRepository,
	eventRepo EventRepository,
	balanceRepo BalanceRepository,
	logger *zap.Logger,
) *EventProcessor {
	return &EventProcessor{
		accountRepo:  accountRepo,
		orderRepo:    orderRepo,
		positionRepo: positionRepo,
		eventRepo:    eventRepo,
		balanceRepo:  balanceRepo,
		logger:       logger,
	}
}

// ProcessOrderFill handles order fill events from Alpaca
func (p *EventProcessor) ProcessOrderFill(ctx context.Context, event *entities.AlpacaOrderFillEvent) error {
	p.logger.Info("Processing order fill event",
		zap.String("order_id", event.OrderID),
		zap.String("symbol", event.Symbol),
		zap.String("status", event.Status))

	// Get the order from our database
	order, err := p.orderRepo.GetByAlpacaOrderID(ctx, event.OrderID)
	if err != nil {
		return fmt.Errorf("get order: %w", err)
	}
	if order == nil {
		p.logger.Warn("Order not found in database", zap.String("alpaca_order_id", event.OrderID))
		return nil // Not an error, might be an order we don't track
	}

	// Update order status
	filledQty := event.FilledQty.String()
	filledAvgPrice := event.FilledAvgPrice.String()
	status := entities.AlpacaOrderStatus(event.Status)

	if err := p.orderRepo.UpdateFromAlpaca(ctx, event.OrderID, status, &filledQty, &filledAvgPrice, &event.FilledAt); err != nil {
		return fmt.Errorf("update order: %w", err)
	}

	// If order is filled, update position
	if status == entities.AlpacaOrderStatusFilled || status == entities.AlpacaOrderStatusPartiallyFilled {
		if err := p.updatePositionFromFill(ctx, order, event); err != nil {
			p.logger.Error("Failed to update position from fill", zap.Error(err))
		}
	}

	p.logger.Info("Order fill processed",
		zap.String("order_id", event.OrderID),
		zap.String("filled_qty", filledQty),
		zap.String("filled_avg_price", filledAvgPrice))

	return nil
}

// ProcessAccountUpdate handles account status change events
func (p *EventProcessor) ProcessAccountUpdate(ctx context.Context, event *entities.AlpacaAccountEvent) error {
	p.logger.Info("Processing account update event",
		zap.String("account_id", event.AccountID),
		zap.String("status", string(event.Status)))

	// Find account by Alpaca ID
	account, err := p.accountRepo.GetByAlpacaID(ctx, event.AccountID)
	if err != nil {
		return fmt.Errorf("get account: %w", err)
	}
	if account == nil {
		p.logger.Warn("Account not found", zap.String("alpaca_account_id", event.AccountID))
		return nil
	}

	// Update account status
	if err := p.accountRepo.UpdateStatus(ctx, account.UserID, event.Status); err != nil {
		return fmt.Errorf("update account status: %w", err)
	}

	p.logger.Info("Account status updated",
		zap.String("user_id", account.UserID.String()),
		zap.String("new_status", string(event.Status)))

	return nil
}

// ProcessPositionUpdate handles position update events
func (p *EventProcessor) ProcessPositionUpdate(ctx context.Context, event *entities.AlpacaPositionEvent) error {
	p.logger.Info("Processing position update event",
		zap.String("account_id", event.AccountID),
		zap.String("symbol", event.Symbol))

	// Find account
	account, err := p.accountRepo.GetByAlpacaID(ctx, event.AccountID)
	if err != nil {
		return fmt.Errorf("get account: %w", err)
	}
	if account == nil {
		return nil
	}

	// Get existing position
	existing, err := p.positionRepo.GetByUserAndSymbol(ctx, account.UserID, event.Symbol)
	if err != nil {
		return fmt.Errorf("get position: %w", err)
	}

	now := time.Now()

	// If qty is zero, position is closed
	if event.Qty.IsZero() {
		if existing != nil {
			if err := p.positionRepo.DeleteByUserAndSymbol(ctx, account.UserID, event.Symbol); err != nil {
				return fmt.Errorf("delete position: %w", err)
			}
		}
		return nil
	}

	// Update or create position
	pos := &entities.InvestmentPosition{
		UserID:          account.UserID,
		AlpacaAccountID: &account.ID,
		Symbol:          event.Symbol,
		Qty:             event.Qty,
		QtyAvailable:    event.Qty,
		MarketValue:     event.MarketValue,
		UnrealizedPL:    event.UnrealizedPL,
		Side:            "long",
		LastSyncedAt:    &now,
		UpdatedAt:       now,
	}

	if existing != nil {
		pos.ID = existing.ID
		pos.AvgEntryPrice = existing.AvgEntryPrice
		pos.CostBasis = existing.CostBasis
		pos.CreatedAt = existing.CreatedAt
	} else {
		pos.ID = uuid.New()
		pos.CreatedAt = now
	}

	if err := p.positionRepo.Upsert(ctx, pos); err != nil {
		return fmt.Errorf("upsert position: %w", err)
	}

	return nil
}

// StoreEvent stores a raw event for later processing
func (p *EventProcessor) StoreEvent(ctx context.Context, eventType, eventID string, payload []byte, userID *uuid.UUID, alpacaAccountID *uuid.UUID) error {
	event := &entities.AlpacaEvent{
		ID:              uuid.New(),
		UserID:          userID,
		AlpacaAccountID: alpacaAccountID,
		EventType:       eventType,
		EventID:         eventID,
		Payload:         payload,
		Processed:       false,
		CreatedAt:       time.Now(),
	}
	return p.eventRepo.Create(ctx, event)
}

// ProcessPendingEvents processes unprocessed events
func (p *EventProcessor) ProcessPendingEvents(ctx context.Context, limit int) error {
	events, err := p.eventRepo.GetUnprocessed(ctx, limit)
	if err != nil {
		return fmt.Errorf("get unprocessed events: %w", err)
	}

	for _, event := range events {
		var errMsg *string
		if err := p.processEvent(ctx, event); err != nil {
			msg := err.Error()
			errMsg = &msg
			p.logger.Error("Failed to process event", zap.String("event_id", event.ID.String()), zap.Error(err))
		}
		if err := p.eventRepo.MarkProcessed(ctx, event.ID, errMsg); err != nil {
			p.logger.Error("Failed to mark event processed", zap.Error(err))
		}
	}

	return nil
}

func (p *EventProcessor) processEvent(ctx context.Context, event *entities.AlpacaEvent) error {
	switch event.EventType {
	case "trade_update", "order_fill":
		var fillEvent entities.AlpacaOrderFillEvent
		if err := json.Unmarshal(event.Payload, &fillEvent); err != nil {
			return fmt.Errorf("unmarshal order fill: %w", err)
		}
		return p.ProcessOrderFill(ctx, &fillEvent)

	case "account_update":
		var accountEvent entities.AlpacaAccountEvent
		if err := json.Unmarshal(event.Payload, &accountEvent); err != nil {
			return fmt.Errorf("unmarshal account update: %w", err)
		}
		return p.ProcessAccountUpdate(ctx, &accountEvent)

	case "position_update":
		var posEvent entities.AlpacaPositionEvent
		if err := json.Unmarshal(event.Payload, &posEvent); err != nil {
			return fmt.Errorf("unmarshal position update: %w", err)
		}
		return p.ProcessPositionUpdate(ctx, &posEvent)

	default:
		p.logger.Debug("Unknown event type", zap.String("type", event.EventType))
		return nil
	}
}

func (p *EventProcessor) updatePositionFromFill(ctx context.Context, order *entities.InvestmentOrder, event *entities.AlpacaOrderFillEvent) error {
	existing, err := p.positionRepo.GetByUserAndSymbol(ctx, order.UserID, event.Symbol)
	if err != nil {
		return err
	}

	now := time.Now()
	var pos *entities.InvestmentPosition

	if existing == nil {
		// New position
		pos = &entities.InvestmentPosition{
			ID:            uuid.New(),
			UserID:        order.UserID,
			Symbol:        event.Symbol,
			Qty:           event.FilledQty,
			QtyAvailable:  event.FilledQty,
			AvgEntryPrice: event.FilledAvgPrice,
			CostBasis:     event.FilledQty.Mul(event.FilledAvgPrice),
			MarketValue:   event.FilledQty.Mul(event.FilledAvgPrice),
			Side:          "long",
			CreatedAt:     now,
			UpdatedAt:     now,
		}
	} else {
		pos = existing
		if order.Side == entities.AlpacaOrderSideBuy {
			// Add to position
			totalCost := pos.CostBasis.Add(event.FilledQty.Mul(event.FilledAvgPrice))
			totalQty := pos.Qty.Add(event.FilledQty)
			pos.Qty = totalQty
			pos.QtyAvailable = totalQty
			pos.CostBasis = totalCost
			if totalQty.GreaterThan(decimal.Zero) {
				pos.AvgEntryPrice = totalCost.Div(totalQty)
			}
		} else {
			// Reduce position
			pos.Qty = pos.Qty.Sub(event.FilledQty)
			pos.QtyAvailable = pos.Qty
			if pos.Qty.LessThanOrEqual(decimal.Zero) {
				return p.positionRepo.DeleteByUserAndSymbol(ctx, order.UserID, event.Symbol)
			}
			pos.CostBasis = pos.Qty.Mul(pos.AvgEntryPrice)
		}
		pos.UpdatedAt = now
	}

	pos.LastSyncedAt = &now
	return p.positionRepo.Upsert(ctx, pos)
}
