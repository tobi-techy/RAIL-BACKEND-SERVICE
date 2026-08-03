package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	oneSignalPushURL = "https://onesignal.com/api/v1/notifications"
	oneSignalTTL     = 86400
)

// oneSignalNotificationRequest is the payload for OneSignal's create-notification API.
// Delivery is keyed on external_user_id (the RAIL user UUID) rather than a device
// token, so the backend does not need to track device tokens at all.
type oneSignalNotificationRequest struct {
	AppID                  string                 `json:"app_id"`
	IncludeExternalUserIDs []string               `json:"include_external_user_ids"`
	Contents               map[string]string      `json:"contents"`
	Headings               map[string]string      `json:"headings"`
	Data                   map[string]interface{} `json:"data,omitempty"`
	IOSSound               string                 `json:"ios_sound,omitempty"`
	AndroidSound           string                 `json:"android_sound,omitempty"`
	AndroidChannelID       string                 `json:"android_channel_id,omitempty"`
	Priority               int                    `json:"priority,omitempty"`
	TTL                    int                    `json:"ttl,omitempty"`
}

// oneSignalNotificationResponse is the API's response to a create-notification call.
type oneSignalNotificationResponse struct {
	ID         string   `json:"id"`
	Recipients int      `json:"recipients"`
	Errors     []string `json:"errors"`
}

// retryableOneSignalError carries the wait duration for a retryable failure
// (rate limit or server error), derived from the Retry-After header.
type retryableOneSignalError struct {
	err   error
	delay time.Duration
}

func (e *retryableOneSignalError) Error() string { return e.err.Error() }

func (e *retryableOneSignalError) Unwrap() error { return e.err }

// OneSignalPushService sends push notifications via OneSignal's REST API,
// addressing recipients by external_user_id (the RAIL user UUID) so no device
// token registry is needed server-side. Device subscriptions are managed by the
// OneSignal SDK in the mobile app.
type OneSignalPushService struct {
	httpClient       *http.Client
	appID            string
	apiKey           string
	baseURL          string
	androidChannelID string
	logger           *zap.Logger
}

// NewOneSignalPushService creates a OneSignal push sender.
func NewOneSignalPushService(appID, apiKey string, logger *zap.Logger) *OneSignalPushService {
	return &OneSignalPushService{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		appID:      appID,
		apiKey:     apiKey,
		baseURL:    oneSignalPushURL,
		logger:     logger,
	}
}

// SetAndroidChannelID sets the Android notification channel for deliveries.
func (s *OneSignalPushService) SetAndroidChannelID(channelID string) {
	s.androidChannelID = channelID
}

// SendToUser sends a push notification to all of a user's devices via OneSignal.
func (s *OneSignalPushService) SendToUser(ctx context.Context, userID uuid.UUID, title, body string, data map[string]interface{}) error {
	if s.appID == "" || s.apiKey == "" {
		return fmt.Errorf("onesignal app_id/api_key not configured")
	}

	reqBody := oneSignalNotificationRequest{
		AppID:                  s.appID,
		IncludeExternalUserIDs: []string{userID.String()},
		Contents:               map[string]string{"en": body},
		Headings:               map[string]string{"en": title},
		Data:                   data,
		IOSSound:               "default",
		AndroidSound:           "default",
		AndroidChannelID:       s.androidChannelID,
		Priority:               10,
		TTL:                    oneSignalTTL,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			delay := retryBaseDelay * time.Duration(attempt)
			if retryErr, ok := lastErr.(*retryableOneSignalError); ok && retryErr.delay > delay {
				delay = retryErr.delay
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}

		notifID, recipients, retryErr, err := s.sendChunk(ctx, userID, jsonBody)
		if err != nil {
			return err
		}
		if retryErr != nil {
			lastErr = retryErr
			s.logger.Warn("OneSignal push attempt failed, retrying",
				zap.Int("attempt", attempt+1), zap.Error(retryErr))
			continue
		}

		s.logger.Info("OneSignal notification sent",
			zap.String("user_id", userID.String()),
			zap.String("notification_id", notifID),
			zap.Int("recipients", recipients))
		return nil
	}
	return fmt.Errorf("onesignal push failed after %d attempts: %w", maxRetries+1, lastErr)
}

// sendChunk performs a single create-notification call. A non-nil retryErr means
// the call failed transiently and should be retried after retryErr.delay.
func (s *OneSignalPushService) sendChunk(ctx context.Context, userID uuid.UUID, jsonBody []byte) (string, int, *retryableOneSignalError, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL, bytes.NewReader(jsonBody))
	if err != nil {
		return "", 0, nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Basic "+s.apiKey)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", 0, &retryableOneSignalError{err: fmt.Errorf("send request: %w", err)}, nil
	}
	defer resp.Body.Close()

	// Retry on rate limits (honor Retry-After) and 5xx.
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		delay := retryAfterDuration(resp.Header.Get("Retry-After"))
		return "", 0, &retryableOneSignalError{
			err:   fmt.Errorf("onesignal returned status %d", resp.StatusCode),
			delay: delay,
		}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return "", 0, nil, fmt.Errorf("onesignal failed with status %d (non-retryable)", resp.StatusCode)
	}

	var pushResp oneSignalNotificationResponse
	if err := json.NewDecoder(resp.Body).Decode(&pushResp); err != nil {
		return "", 0, nil, fmt.Errorf("decode response: %w", err)
	}

	if len(pushResp.Errors) > 0 {
		// OneSignal returns HTTP 200 even when a recipient can't be resolved
		// (e.g. the user has no active subscription with this external_id).
		s.logger.Warn("OneSignal reported per-recipient errors",
			zap.String("user_id", userID.String()),
			zap.Strings("errors", pushResp.Errors))
	}

	return pushResp.ID, pushResp.Recipients, nil, nil
}

// retryAfterDuration parses an HTTP Retry-After header (seconds or HTTP-date)
// and returns a wait duration. Falls back to the base backoff delay when the
// header is absent or malformed.
func retryAfterDuration(header string) time.Duration {
	if header != "" {
		if secs, err := strconv.Atoi(header); err == nil && secs >= 0 {
			return time.Duration(secs) * time.Second
		}
		if retryAt, err := http.ParseTime(header); err == nil {
			if d := time.Until(retryAt); d > 0 {
				return d
			}
		}
	}
	return retryBaseDelay
}
