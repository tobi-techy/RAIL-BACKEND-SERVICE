package alpaca

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

const (
	sseEventsEndpoint = "/v1/events/accounts/status"
	sseTradeEventsEndpoint = "/v1/events/trades"
)

// SSEEventType represents the type of SSE event
type SSEEventType string

const (
	SSEEventTypeAccountStatus SSEEventType = "account.status_updated"
	SSEEventTypeTradeUpdate   SSEEventType = "trade_updates"
	SSEEventTypeOrderFill     SSEEventType = "fill"
	SSEEventTypeOrderPartialFill SSEEventType = "partial_fill"
	SSEEventTypeOrderCanceled SSEEventType = "canceled"
	SSEEventTypeOrderRejected SSEEventType = "rejected"
)

// SSEEvent represents a Server-Sent Event from Alpaca
type SSEEvent struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
}

// SSEListener handles Server-Sent Events from Alpaca
type SSEListener struct {
	client *Client
	logger *zap.Logger
}

// NewSSEListener creates a new SSE listener
func NewSSEListener(client *Client, logger *zap.Logger) *SSEListener {
	return &SSEListener{
		client: client,
		logger: logger,
	}
}

// ListenAccountEvents listens for account status events
func (l *SSEListener) ListenAccountEvents(ctx context.Context, handler func(SSEEvent) error) error {
	return l.listen(ctx, sseEventsEndpoint, handler)
}

// ListenTradeEvents listens for trade update events
func (l *SSEListener) ListenTradeEvents(ctx context.Context, handler func(SSEEvent) error) error {
	return l.listen(ctx, sseTradeEventsEndpoint, handler)
}

// listen establishes SSE connection and processes events
func (l *SSEListener) listen(ctx context.Context, endpoint string, handler func(SSEEvent) error) error {
	fullURL := l.client.config.BaseURL + endpoint

	req, err := http.NewRequestWithContext(ctx, "GET", fullURL, nil)
	if err != nil {
		return fmt.Errorf("create SSE request: %w", err)
	}

	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Connection", "keep-alive")
	req.SetBasicAuth(l.client.config.ClientID, l.client.config.SecretKey)

	l.logger.Info("Connecting to Alpaca SSE stream", zap.String("endpoint", endpoint))

	resp, err := l.client.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("SSE connection failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("SSE connection failed with status: %d", resp.StatusCode)
	}

	l.logger.Info("Connected to Alpaca SSE stream")

	scanner := bufio.NewScanner(resp.Body)
	var eventType string
	var eventData strings.Builder

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			l.logger.Info("SSE listener stopped")
			return ctx.Err()
		default:
		}

		line := scanner.Text()

		if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			eventData.WriteString(data)
		} else if line == "" && eventData.Len() > 0 {
			event := SSEEvent{
				Event: eventType,
				Data:  json.RawMessage(eventData.String()),
			}

			if err := handler(event); err != nil {
				l.logger.Error("Event handler error",
					zap.String("event", eventType),
					zap.Error(err))
			}

			eventType = ""
			eventData.Reset()
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("SSE scanner error: %w", err)
	}

	return nil
}

// ListenWithReconnect listens with automatic reconnection
func (l *SSEListener) ListenWithReconnect(ctx context.Context, endpoint string, handler func(SSEEvent) error) {
	backoff := time.Second

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		err := l.listen(ctx, endpoint, handler)
		if err != nil && ctx.Err() == nil {
			l.logger.Warn("SSE connection lost, reconnecting",
				zap.Error(err),
				zap.Duration("backoff", backoff))

			time.Sleep(backoff)
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
		} else {
			return
		}
	}
}
