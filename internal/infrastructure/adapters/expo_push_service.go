package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

const expoPushURL = "https://exp.host/--/api/v2/push/send"

// ExpoPushMessage represents a single push notification
type ExpoPushMessage struct {
	To       string                 `json:"to"`
	Title    string                 `json:"title,omitempty"`
	Body     string                 `json:"body"`
	Data     map[string]interface{} `json:"data,omitempty"`
	Sound    string                 `json:"sound,omitempty"`
	Badge    *int                   `json:"badge,omitempty"`
	Priority string                 `json:"priority,omitempty"` // default, normal, high
	TTL      int                    `json:"ttl,omitempty"`
}

// ExpoPushResponse represents Expo's response
type ExpoPushResponse struct {
	Data []ExpoPushTicket `json:"data"`
}

// ExpoPushTicket represents a single ticket in the response
type ExpoPushTicket struct {
	Status  string `json:"status"` // ok or error
	ID      string `json:"id,omitempty"`
	Message string `json:"message,omitempty"`
	Details struct {
		Error string `json:"error,omitempty"`
	} `json:"details,omitempty"`
}

// DeviceTokenProvider retrieves device tokens for users
type DeviceTokenProvider interface {
	GetUserDeviceTokens(ctx context.Context, userID uuid.UUID) ([]string, error)
}

// ExpoPushService sends push notifications via Expo Push API
type ExpoPushService struct {
	httpClient    *http.Client
	tokenProvider DeviceTokenProvider
	logger        *zap.Logger
}

// NewExpoPushService creates a new Expo push service
func NewExpoPushService(tokenProvider DeviceTokenProvider, logger *zap.Logger) *ExpoPushService {
	return &ExpoPushService{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		tokenProvider: tokenProvider,
		logger:        logger,
	}
}

// SendToUser sends a push notification to all of a user's devices
func (s *ExpoPushService) SendToUser(ctx context.Context, userID uuid.UUID, title, body string, data map[string]interface{}) error {
	tokens, err := s.tokenProvider.GetUserDeviceTokens(ctx, userID)
	if err != nil {
		return fmt.Errorf("get device tokens: %w", err)
	}

	if len(tokens) == 0 {
		s.logger.Debug("No device tokens for user", zap.String("user_id", userID.String()))
		return nil
	}

	messages := make([]ExpoPushMessage, 0, len(tokens))
	for _, token := range tokens {
		messages = append(messages, ExpoPushMessage{
			To:       token,
			Title:    title,
			Body:     body,
			Data:     data,
			Sound:    "default",
			Priority: "high",
		})
	}

	return s.sendBatch(ctx, messages)
}

// sendBatch sends multiple messages in a single request
func (s *ExpoPushService) sendBatch(ctx context.Context, messages []ExpoPushMessage) error {
	if len(messages) == 0 {
		return nil
	}

	jsonBody, err := json.Marshal(messages)
	if err != nil {
		return fmt.Errorf("marshal messages: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, expoPushURL, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("expo push failed with status %d", resp.StatusCode)
	}

	var pushResp ExpoPushResponse
	if err := json.NewDecoder(resp.Body).Decode(&pushResp); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	// Log any errors
	for i, ticket := range pushResp.Data {
		if ticket.Status == "error" {
			s.logger.Warn("Push notification failed",
				zap.String("token", messages[i].To[:20]+"..."),
				zap.String("error", ticket.Details.Error),
				zap.String("message", ticket.Message))
		}
	}

	s.logger.Info("Push notifications sent",
		zap.Int("count", len(messages)),
		zap.Int("success", countSuccess(pushResp.Data)))

	return nil
}

func countSuccess(tickets []ExpoPushTicket) int {
	count := 0
	for _, t := range tickets {
		if t.Status == "ok" {
			count++
		}
	}
	return count
}
