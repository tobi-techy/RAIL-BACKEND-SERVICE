package alpaca_events

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
)

// OrderRepository interface for order operations
type OrderRepository interface {
	UpdateStatus(ctx context.Context, orderID string, status string) error
}

// PositionRepository interface for position operations
type PositionRepository interface {
	// Add methods as needed
}

type SSEListener struct {
	baseURL      string
	apiKey       string
	apiSecret    string
	orderRepo    OrderRepository
	positionRepo PositionRepository
	logger       *zap.Logger
	stopCh       chan struct{}
}

func NewSSEListener(
	baseURL, apiKey, apiSecret string,
	orderRepo OrderRepository,
	positionRepo PositionRepository,
	logger *zap.Logger,
) *SSEListener {
	return &SSEListener{
		baseURL:      baseURL,
		apiKey:       apiKey,
		apiSecret:    apiSecret,
		orderRepo:    orderRepo,
		positionRepo: positionRepo,
		logger:       logger,
		stopCh:       make(chan struct{}),
	}
}

type TradeEvent struct {
	EventID int    `json:"event_id"`
	At      string `json:"at"`
	Order   struct {
		ID             string `json:"id"`
		Status         string `json:"status"`
		FilledQty      string `json:"filled_qty"`
		FilledAvgPrice string `json:"filled_avg_price"`
	} `json:"order"`
}

func (l *SSEListener) Start(ctx context.Context) error {
	l.logger.Info("Starting Alpaca SSE listener")
	go l.listenToTrades(ctx)
	return nil
}

func (l *SSEListener) Stop() {
	close(l.stopCh)
}

func (l *SSEListener) listenToTrades(ctx context.Context) {
	endpoint := fmt.Sprintf("%s/v1/events/trades", l.baseURL)
	
	for {
		select {
		case <-l.stopCh:
			return
		case <-ctx.Done():
			return
		default:
			if err := l.connectSSE(ctx, endpoint, l.handleTradeEvent); err != nil {
				l.logger.Error("SSE error", zap.Error(err))
				time.Sleep(5 * time.Second)
			}
		}
	}
}

func (l *SSEListener) connectSSE(ctx context.Context, endpoint string, handler func([]byte) error) error {
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return err
	}

	req.SetBasicAuth(l.apiKey, l.apiSecret)
	req.Header.Set("Accept", "text/event-stream")

	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			handler([]byte(data))
		}
	}
	return scanner.Err()
}

func (l *SSEListener) handleTradeEvent(data []byte) error {
	var event TradeEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return err
	}

	l.logger.Info("Trade event",
		zap.String("order_id", event.Order.ID),
		zap.String("status", event.Order.Status))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	l.orderRepo.UpdateStatus(ctx, event.Order.ID, event.Order.Status)
	return nil
}
