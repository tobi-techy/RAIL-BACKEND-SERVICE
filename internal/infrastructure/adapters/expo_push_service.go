package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	expoPushURL       = "https://exp.host/--/api/v2/push/send"
	expoBatchLimit    = 100
	maxRetries        = 3
	retryBaseDelay    = 500 * time.Millisecond
)

// ExpoPushMessage represents a single push notification
type ExpoPushMessage struct {
	To       string                 `json:"to"`
	Title    string                 `json:"title,omitempty"`
	Body     string                 `json:"body"`
	Data     map[string]interface{} `json:"data,omitempty"`
	Sound    string                 `json:"sound,omitempty"`
	Badge    *int                   `json:"badge,omitempty"`
	Priority string                 `json:"priority,omitempty"`
	TTL      int                    `json:"ttl,omitempty"`
}

// ExpoPushResponse represents Expo's response
type ExpoPushResponse struct {
	Data []ExpoPushTicket `json:"data"`
}

// ExpoPushTicket represents a single ticket in the response
type ExpoPushTicket struct {
	Status  string `json:"status"`
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

// StaleTokenCleaner deactivates tokens that Expo reports as invalid.
type StaleTokenCleaner interface {
	DeleteToken(ctx context.Context, token string) error
}

// ExpoPushService sends push notifications via Expo Push API
type ExpoPushService struct {
	httpClient    *http.Client
	tokenProvider DeviceTokenProvider
	tokenCleaner  StaleTokenCleaner
	logger        *zap.Logger
}

// NewExpoPushService creates a new Expo push service.
// tokenProvider is required. If it also implements StaleTokenCleaner, stale tokens will be auto-cleaned.
func NewExpoPushService(tokenProvider DeviceTokenProvider, logger *zap.Logger) *ExpoPushService {
	cleaner, _ := tokenProvider.(StaleTokenCleaner)
	return &ExpoPushService{
		httpClient:    &http.Client{Timeout: 30 * time.Second},
		tokenProvider: tokenProvider,
		tokenCleaner:  cleaner,
		logger:        logger,
	}
}

// SetTokenCleaner allows explicitly setting a stale token cleaner.
func (s *ExpoPushService) SetTokenCleaner(cleaner StaleTokenCleaner) {
	s.tokenCleaner = cleaner
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
			To: token, Title: title, Body: body, Data: data,
			Sound: "default", Priority: "high",
		})
	}

	return s.sendBatch(ctx, messages)
}

// sendBatch sends messages in chunks of expoBatchLimit with retry.
func (s *ExpoPushService) sendBatch(ctx context.Context, messages []ExpoPushMessage) error {
	for i := 0; i < len(messages); i += expoBatchLimit {
		end := i + expoBatchLimit
		if end > len(messages) {
			end = len(messages)
		}
		chunk := messages[i:end]
		if err := s.sendChunkWithRetry(ctx, chunk); err != nil {
			return err
		}
	}
	return nil
}

// retryableError wraps errors that should be retried.
type retryableError struct{ error }

func (s *ExpoPushService) sendChunkWithRetry(ctx context.Context, messages []ExpoPushMessage) error {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			delay := retryBaseDelay * time.Duration(math.Pow(2, float64(attempt-1)))
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}

		err := s.sendChunk(ctx, messages)
		if err == nil {
			return nil
		}
		// Only retry transient errors
		if _, ok := err.(retryableError); !ok {
			return err
		}
		lastErr = err
		s.logger.Warn("Expo push attempt failed, retrying",
			zap.Int("attempt", attempt+1), zap.Error(err))
	}
	return fmt.Errorf("expo push failed after %d attempts: %w", maxRetries+1, lastErr)
}

func (s *ExpoPushService) sendChunk(ctx context.Context, messages []ExpoPushMessage) error {
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
		return retryableError{fmt.Errorf("send request: %w", err)}
	}
	defer resp.Body.Close()

	// Retry on server errors and rate limits
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return retryableError{fmt.Errorf("expo push returned status %d", resp.StatusCode)}
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("expo push failed with status %d (non-retryable)", resp.StatusCode)
	}

	var pushResp ExpoPushResponse
	if err := json.NewDecoder(resp.Body).Decode(&pushResp); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	// Process tickets: log errors and clean up stale tokens
	successCount := 0
	for i, ticket := range pushResp.Data {
		if ticket.Status == "ok" {
			successCount++
			continue
		}
		token := messages[i].To
		s.logger.Warn("Push notification failed",
			zap.String("token_prefix", truncateToken(token)),
			zap.String("error", ticket.Details.Error),
			zap.String("message", ticket.Message))

		// Clean up stale tokens
		if ticket.Details.Error == "DeviceNotRegistered" && s.tokenCleaner != nil {
			if cleanErr := s.tokenCleaner.DeleteToken(context.Background(), token); cleanErr != nil {
				s.logger.Warn("Failed to deactivate stale token", zap.Error(cleanErr))
			} else {
				s.logger.Info("Deactivated stale device token", zap.String("token_prefix", truncateToken(token)))
			}
		}
	}

	s.logger.Info("Push notifications sent",
		zap.Int("count", len(messages)), zap.Int("success", successCount))
	return nil
}

func truncateToken(token string) string {
	if len(token) > 20 {
		return token[:20] + "..."
	}
	return token
}
