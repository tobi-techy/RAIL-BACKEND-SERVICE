package market

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	alpacaAdapter "github.com/rail-service/rail_service/internal/adapters/alpaca"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// AlertRepository interface for market alerts
type AlertRepository interface {
	Create(ctx context.Context, alert *entities.MarketAlert) error
	GetByID(ctx context.Context, id uuid.UUID) (*entities.MarketAlert, error)
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.MarketAlert, error)
	GetActiveBySymbol(ctx context.Context, symbol string) ([]*entities.MarketAlert, error)
	GetAllActive(ctx context.Context) ([]*entities.MarketAlert, error)
	MarkTriggered(ctx context.Context, id uuid.UUID, currentPrice decimal.Decimal) error
	Delete(ctx context.Context, id uuid.UUID) error
}

// NotificationService interface for sending alerts
type NotificationService interface {
	SendPushNotification(ctx context.Context, userID uuid.UUID, title, message string) error
}

// MarketDataService handles real-time market data and alerts
type MarketDataService struct {
	alpacaClient *alpacaAdapter.Client
	alertRepo    AlertRepository
	notifier     NotificationService
	logger       *zap.Logger
	priceCache   map[string]*cachedQuote
}

type cachedQuote struct {
	quote     *entities.MarketQuote
	fetchedAt time.Time
}

func NewMarketDataService(
	alpacaClient *alpacaAdapter.Client,
	alertRepo AlertRepository,
	notifier NotificationService,
	logger *zap.Logger,
) *MarketDataService {
	return &MarketDataService{
		alpacaClient: alpacaClient,
		alertRepo:    alertRepo,
		notifier:     notifier,
		logger:       logger,
		priceCache:   make(map[string]*cachedQuote),
	}
}

// GetQuote returns real-time quote for a symbol
func (s *MarketDataService) GetQuote(ctx context.Context, symbol string) (*entities.MarketQuote, error) {
	// Check cache (5 second TTL)
	if cached, ok := s.priceCache[symbol]; ok && time.Since(cached.fetchedAt) < 5*time.Second {
		return cached.quote, nil
	}

	quote, err := s.alpacaClient.GetLatestQuote(ctx, symbol)
	if err != nil {
		return nil, fmt.Errorf("get quote: %w", err)
	}

	s.priceCache[symbol] = &cachedQuote{quote: quote, fetchedAt: time.Now()}
	return quote, nil
}

// GetQuotes returns quotes for multiple symbols
func (s *MarketDataService) GetQuotes(ctx context.Context, symbols []string) (map[string]*entities.MarketQuote, error) {
	result := make(map[string]*entities.MarketQuote)
	var toFetch []string

	// Check cache first
	for _, sym := range symbols {
		if cached, ok := s.priceCache[sym]; ok && time.Since(cached.fetchedAt) < 5*time.Second {
			result[sym] = cached.quote
		} else {
			toFetch = append(toFetch, sym)
		}
	}

	if len(toFetch) == 0 {
		return result, nil
	}

	quotes, err := s.alpacaClient.GetLatestQuotes(ctx, toFetch)
	if err != nil {
		return nil, fmt.Errorf("get quotes: %w", err)
	}

	now := time.Now()
	for sym, quote := range quotes {
		s.priceCache[sym] = &cachedQuote{quote: quote, fetchedAt: now}
		result[sym] = quote
	}

	return result, nil
}

// GetBars returns historical OHLCV data
func (s *MarketDataService) GetBars(ctx context.Context, symbol string, timeframe string, start, end time.Time) ([]*entities.MarketBar, error) {
	return s.alpacaClient.GetBars(ctx, symbol, timeframe, start, end)
}

// CreateAlert creates a new market alert
func (s *MarketDataService) CreateAlert(ctx context.Context, userID uuid.UUID, symbol, alertType string, conditionValue decimal.Decimal) (*entities.MarketAlert, error) {
	// Validate alert type
	validTypes := map[string]bool{
		entities.AlertTypePriceAbove: true,
		entities.AlertTypePriceBelow: true,
		entities.AlertTypePctChange:  true,
	}
	if !validTypes[alertType] {
		return nil, fmt.Errorf("invalid alert type: %s", alertType)
	}

	now := time.Now()
	alert := &entities.MarketAlert{
		ID:             uuid.New(),
		UserID:         userID,
		Symbol:         symbol,
		AlertType:      alertType,
		ConditionValue: conditionValue,
		Status:         entities.ScheduleStatusActive,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if err := s.alertRepo.Create(ctx, alert); err != nil {
		return nil, fmt.Errorf("create alert: %w", err)
	}

	s.logger.Info("Market alert created",
		zap.String("user_id", userID.String()),
		zap.String("symbol", symbol),
		zap.String("type", alertType))

	return alert, nil
}

// GetUserAlerts returns all alerts for a user
func (s *MarketDataService) GetUserAlerts(ctx context.Context, userID uuid.UUID) ([]*entities.MarketAlert, error) {
	return s.alertRepo.GetByUserID(ctx, userID)
}

// DeleteAlert removes an alert after verifying ownership
func (s *MarketDataService) DeleteAlert(ctx context.Context, userID, alertID uuid.UUID) error {
	alert, err := s.alertRepo.GetByID(ctx, alertID)
	if err != nil {
		return fmt.Errorf("get alert: %w", err)
	}
	if alert == nil {
		return fmt.Errorf("alert not found")
	}
	if alert.UserID != userID {
		return fmt.Errorf("forbidden")
	}
	return s.alertRepo.Delete(ctx, alertID)
}

// CheckAlerts evaluates all active alerts against current prices
func (s *MarketDataService) CheckAlerts(ctx context.Context) error {
	alerts, err := s.alertRepo.GetAllActive(ctx)
	if err != nil {
		return fmt.Errorf("get active alerts: %w", err)
	}

	if len(alerts) == 0 {
		return nil
	}

	// Collect unique symbols
	symbolSet := make(map[string]bool)
	for _, alert := range alerts {
		symbolSet[alert.Symbol] = true
	}
	symbols := make([]string, 0, len(symbolSet))
	for sym := range symbolSet {
		symbols = append(symbols, sym)
	}

	// Fetch quotes
	quotes, err := s.GetQuotes(ctx, symbols)
	if err != nil {
		return fmt.Errorf("get quotes for alerts: %w", err)
	}

	// Check each alert
	for _, alert := range alerts {
		quote, ok := quotes[alert.Symbol]
		if !ok {
			continue
		}

		triggered := s.evaluateAlert(alert, quote)
		if triggered {
			if err := s.triggerAlert(ctx, alert, quote.Price); err != nil {
				s.logger.Error("Failed to trigger alert", zap.Error(err))
			}
		}
	}

	return nil
}

func (s *MarketDataService) evaluateAlert(alert *entities.MarketAlert, quote *entities.MarketQuote) bool {
	switch alert.AlertType {
	case entities.AlertTypePriceAbove:
		return quote.Price.GreaterThanOrEqual(alert.ConditionValue)
	case entities.AlertTypePriceBelow:
		return quote.Price.LessThanOrEqual(alert.ConditionValue)
	case entities.AlertTypePctChange:
		if quote.PreviousClose.IsZero() {
			return false
		}
		pctChange := quote.Price.Sub(quote.PreviousClose).Div(quote.PreviousClose).Mul(decimal.NewFromInt(100)).Abs()
		return pctChange.GreaterThanOrEqual(alert.ConditionValue)
	}
	return false
}

func (s *MarketDataService) triggerAlert(ctx context.Context, alert *entities.MarketAlert, currentPrice decimal.Decimal) error {
	if err := s.alertRepo.MarkTriggered(ctx, alert.ID, currentPrice); err != nil {
		return err
	}

	// Send notification
	if s.notifier != nil {
		title := fmt.Sprintf("%s Alert Triggered", alert.Symbol)
		var message string
		switch alert.AlertType {
		case entities.AlertTypePriceAbove:
			message = fmt.Sprintf("%s reached $%s (above $%s)", alert.Symbol, currentPrice.StringFixed(2), alert.ConditionValue.StringFixed(2))
		case entities.AlertTypePriceBelow:
			message = fmt.Sprintf("%s dropped to $%s (below $%s)", alert.Symbol, currentPrice.StringFixed(2), alert.ConditionValue.StringFixed(2))
		case entities.AlertTypePctChange:
			message = fmt.Sprintf("%s moved %s%% (threshold: %s%%)", alert.Symbol, currentPrice.StringFixed(2), alert.ConditionValue.StringFixed(2))
		}
		_ = s.notifier.SendPushNotification(ctx, alert.UserID, title, message)
	}

	s.logger.Info("Alert triggered",
		zap.String("alert_id", alert.ID.String()),
		zap.String("symbol", alert.Symbol),
		zap.String("price", currentPrice.String()))

	return nil
}
