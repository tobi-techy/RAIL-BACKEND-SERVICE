package investing

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// ScheduledInvestmentRepository interface
type ScheduledInvestmentRepository interface {
	Create(ctx context.Context, si *entities.ScheduledInvestment) error
	GetByID(ctx context.Context, id uuid.UUID) (*entities.ScheduledInvestment, error)
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.ScheduledInvestment, error)
	GetDueForExecution(ctx context.Context, before time.Time) ([]*entities.ScheduledInvestment, error)
	Update(ctx context.Context, si *entities.ScheduledInvestment) error
	UpdateStatus(ctx context.Context, id uuid.UUID, status string) error
	CreateExecution(ctx context.Context, exec *entities.ScheduledInvestmentExecution) error
	GetExecutions(ctx context.Context, scheduleID uuid.UUID, limit int) ([]*entities.ScheduledInvestmentExecution, error)
}

// OrderPlacer interface for placing orders
type OrderPlacer interface {
	PlaceMarketOrder(ctx context.Context, userID uuid.UUID, symbol string, notional decimal.Decimal) (*entities.InvestmentOrder, error)
}

// BasketOrderPlacer interface for placing basket orders
type BasketOrderPlacer interface {
	PlaceOrder(ctx context.Context, userID, basketID uuid.UUID, side entities.OrderSide, amount decimal.Decimal) (*BrokerageOrderResponse, error)
}

// ScheduledInvestmentService handles recurring investments and DCA
type ScheduledInvestmentService struct {
	repo              ScheduledInvestmentRepository
	orderPlacer       OrderPlacer
	basketOrderPlacer BasketOrderPlacer
	logger            *zap.Logger
}

func NewScheduledInvestmentService(
	repo ScheduledInvestmentRepository,
	orderPlacer OrderPlacer,
	basketOrderPlacer BasketOrderPlacer,
	logger *zap.Logger,
) *ScheduledInvestmentService {
	return &ScheduledInvestmentService{
		repo:              repo,
		orderPlacer:       orderPlacer,
		basketOrderPlacer: basketOrderPlacer,
		logger:            logger,
	}
}

// CreateScheduledInvestment creates a new recurring investment
func (s *ScheduledInvestmentService) CreateScheduledInvestment(ctx context.Context, req *CreateScheduledInvestmentRequest) (*entities.ScheduledInvestment, error) {
	if req.Amount.LessThanOrEqual(decimal.Zero) {
		return nil, fmt.Errorf("amount must be positive")
	}
	if req.Symbol == nil && req.BasketID == nil {
		return nil, fmt.Errorf("symbol or basket_id required")
	}

	nextExec := s.calculateNextExecution(req.Frequency, req.DayOfWeek, req.DayOfMonth, time.Now())

	now := time.Now()
	si := &entities.ScheduledInvestment{
		ID:              uuid.New(),
		UserID:          req.UserID,
		Name:            req.Name,
		Symbol:          req.Symbol,
		BasketID:        req.BasketID,
		Amount:          req.Amount,
		Frequency:       req.Frequency,
		DayOfWeek:       req.DayOfWeek,
		DayOfMonth:      req.DayOfMonth,
		NextExecutionAt: nextExec,
		Status:          entities.ScheduleStatusActive,
		TotalInvested:   decimal.Zero,
		ExecutionCount:  0,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	if err := s.repo.Create(ctx, si); err != nil {
		return nil, fmt.Errorf("create scheduled investment: %w", err)
	}

	s.logger.Info("Scheduled investment created",
		zap.String("id", si.ID.String()),
		zap.String("user_id", req.UserID.String()),
		zap.String("frequency", req.Frequency))

	return si, nil
}

// GetUserScheduledInvestments returns all scheduled investments for a user
func (s *ScheduledInvestmentService) GetUserScheduledInvestments(ctx context.Context, userID uuid.UUID) ([]*entities.ScheduledInvestment, error) {
	return s.repo.GetByUserID(ctx, userID)
}

// GetScheduledInvestment returns a specific scheduled investment
func (s *ScheduledInvestmentService) GetScheduledInvestment(ctx context.Context, id uuid.UUID) (*entities.ScheduledInvestment, error) {
	return s.repo.GetByID(ctx, id)
}

// PauseScheduledInvestment pauses a scheduled investment
func (s *ScheduledInvestmentService) PauseScheduledInvestment(ctx context.Context, id uuid.UUID) error {
	return s.repo.UpdateStatus(ctx, id, entities.ScheduleStatusPaused)
}

// ResumeScheduledInvestment resumes a paused scheduled investment
func (s *ScheduledInvestmentService) ResumeScheduledInvestment(ctx context.Context, id uuid.UUID) error {
	si, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if si == nil {
		return fmt.Errorf("scheduled investment not found")
	}

	si.Status = entities.ScheduleStatusActive
	si.NextExecutionAt = s.calculateNextExecution(si.Frequency, si.DayOfWeek, si.DayOfMonth, time.Now())
	return s.repo.Update(ctx, si)
}

// CancelScheduledInvestment cancels a scheduled investment
func (s *ScheduledInvestmentService) CancelScheduledInvestment(ctx context.Context, id uuid.UUID) error {
	return s.repo.UpdateStatus(ctx, id, entities.ScheduleStatusCancelled)
}

// UpdateScheduledInvestment updates amount or frequency
func (s *ScheduledInvestmentService) UpdateScheduledInvestment(ctx context.Context, id uuid.UUID, amount *decimal.Decimal, frequency *string) error {
	si, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if si == nil {
		return fmt.Errorf("scheduled investment not found")
	}

	if amount != nil {
		si.Amount = *amount
	}
	if frequency != nil {
		si.Frequency = *frequency
		si.NextExecutionAt = s.calculateNextExecution(si.Frequency, si.DayOfWeek, si.DayOfMonth, time.Now())
	}

	return s.repo.Update(ctx, si)
}

// GetExecutionHistory returns execution history for a scheduled investment
func (s *ScheduledInvestmentService) GetExecutionHistory(ctx context.Context, scheduleID uuid.UUID, limit int) ([]*entities.ScheduledInvestmentExecution, error) {
	return s.repo.GetExecutions(ctx, scheduleID, limit)
}

// ProcessDueInvestments executes all due scheduled investments
func (s *ScheduledInvestmentService) ProcessDueInvestments(ctx context.Context) error {
	due, err := s.repo.GetDueForExecution(ctx, time.Now())
	if err != nil {
		return fmt.Errorf("get due investments: %w", err)
	}

	s.logger.Info("Processing due scheduled investments", zap.Int("count", len(due)))

	for _, si := range due {
		if err := s.executeScheduledInvestment(ctx, si); err != nil {
			s.logger.Error("Failed to execute scheduled investment",
				zap.String("id", si.ID.String()),
				zap.Error(err))
		}
	}

	return nil
}

func (s *ScheduledInvestmentService) executeScheduledInvestment(ctx context.Context, si *entities.ScheduledInvestment) error {
	exec := &entities.ScheduledInvestmentExecution{
		ID:                    uuid.New(),
		ScheduledInvestmentID: si.ID,
		Amount:                si.Amount,
		ExecutedAt:            time.Now(),
	}

	// Place the order
	var order *entities.InvestmentOrder
	var execErr error

	if si.Symbol != nil {
		order, execErr = s.orderPlacer.PlaceMarketOrder(ctx, si.UserID, *si.Symbol, si.Amount)
	} else if si.BasketID != nil {
		// Place basket order via brokerage adapter
		if s.basketOrderPlacer != nil {
			orderResp, err := s.basketOrderPlacer.PlaceOrder(ctx, si.UserID, *si.BasketID, entities.OrderSideBuy, si.Amount)
			if err != nil {
				execErr = fmt.Errorf("failed to place basket order: %w", err)
			} else {
				// Create order record for tracking
				notional := si.Amount
				order = &entities.InvestmentOrder{
					ID:            uuid.New(),
					UserID:        si.UserID,
					BasketID:      si.BasketID,
					ClientOrderID: orderResp.OrderRef,
					Symbol:        "BASKET",
					Side:          entities.AlpacaOrderSideBuy,
					OrderType:     entities.AlpacaOrderTypeMarket,
					TimeInForce:   entities.AlpacaTimeInForceDay,
					Notional:      &notional,
					Status:        entities.AlpacaOrderStatusNew,
					CreatedAt:     time.Now(),
				}
			}
		} else {
			execErr = fmt.Errorf("basket order placer not configured")
		}
	}

	if execErr != nil {
		exec.Status = "failed"
		errMsg := execErr.Error()
		exec.ErrorMessage = &errMsg
	} else {
		exec.Status = "success"
		if order != nil {
			exec.OrderID = &order.ID
		}
	}

	// Log execution
	if err := s.repo.CreateExecution(ctx, exec); err != nil {
		s.logger.Error("Failed to log execution", zap.Error(err))
	}

	// Update schedule
	now := time.Now()
	si.LastExecutedAt = &now
	si.ExecutionCount++
	if exec.Status == "success" {
		si.TotalInvested = si.TotalInvested.Add(si.Amount)
	}
	si.NextExecutionAt = s.calculateNextExecution(si.Frequency, si.DayOfWeek, si.DayOfMonth, now)

	if err := s.repo.Update(ctx, si); err != nil {
		s.logger.Error("Failed to update schedule", zap.Error(err))
	}

	return execErr
}

func (s *ScheduledInvestmentService) calculateNextExecution(frequency string, dayOfWeek, dayOfMonth *int, from time.Time) time.Time {
	// Set execution time to 10:00 AM ET (market hours)
	next := time.Date(from.Year(), from.Month(), from.Day(), 14, 0, 0, 0, time.UTC) // 10 AM ET = 14:00 UTC

	switch frequency {
	case entities.FrequencyDaily:
		if from.After(next) {
			next = next.AddDate(0, 0, 1)
		}

	case entities.FrequencyWeekly:
		targetDay := time.Monday
		if dayOfWeek != nil {
			targetDay = time.Weekday(*dayOfWeek)
		}
		daysUntil := int(targetDay - next.Weekday())
		if daysUntil <= 0 {
			daysUntil += 7
		}
		next = next.AddDate(0, 0, daysUntil)

	case entities.FrequencyBiweekly:
		targetDay := time.Monday
		if dayOfWeek != nil {
			targetDay = time.Weekday(*dayOfWeek)
		}
		daysUntil := int(targetDay - next.Weekday())
		if daysUntil <= 0 {
			daysUntil += 14
		} else {
			daysUntil += 7
		}
		next = next.AddDate(0, 0, daysUntil)

	case entities.FrequencyMonthly:
		targetDay := 1
		if dayOfMonth != nil {
			targetDay = *dayOfMonth
		}
		next = time.Date(next.Year(), next.Month(), targetDay, 14, 0, 0, 0, time.UTC)
		if from.After(next) || from.Equal(next) {
			next = next.AddDate(0, 1, 0)
		}
	}

	return next
}

// CreateScheduledInvestmentRequest represents a request to create a scheduled investment
type CreateScheduledInvestmentRequest struct {
	UserID     uuid.UUID
	Name       *string
	Symbol     *string
	BasketID   *uuid.UUID
	Amount     decimal.Decimal
	Frequency  string // daily, weekly, biweekly, monthly
	DayOfWeek  *int   // 0-6 for weekly
	DayOfMonth *int   // 1-28 for monthly
}
