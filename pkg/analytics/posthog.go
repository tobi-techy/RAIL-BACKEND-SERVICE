package analytics

import (
	"time"

	posthog "github.com/posthog/posthog-go"
	"go.uber.org/zap"
)

// PostHogClient wraps the PostHog SDK for server-side event tracking.
// Events are buffered in memory and flushed on a configurable interval.
type PostHogClient struct {
	client posthog.Client
	logger *zap.Logger
}

// NewPostHog creates a PostHog analytics client.
// host should be "https://us.i.posthog.com" for US or "https://eu.i.posthog.com" for EU.
func NewPostHog(apiKey, host string, logger *zap.Logger) *PostHogClient {
	if host == "" {
		host = "https://us.i.posthog.com"
	}
	client, err := posthog.NewWithConfig(apiKey, posthog.Config{
		Endpoint:        host,
		Interval:        10 * time.Second,
		BatchSize:       50,
		MaxRetries:      posthog.Ptr(3),
		ShutdownTimeout: 5 * time.Second,
	})
	if err != nil {
		logger.Error("failed to create posthog client", zap.Error(err))
		return &PostHogClient{logger: logger}
	}
	logger.Info("PostHog analytics initialized", zap.String("host", host))
	return &PostHogClient{client: client, logger: logger}
}

// Track sends an event to PostHog.
func (c *PostHogClient) Track(userID, event string, props map[string]any) {
	if c.client == nil {
		return
	}
	evt := posthog.Capture{
		DistinctId: userID,
		Event:      event,
		Properties: posthog.NewProperties(),
		Timestamp:  time.Now(),
	}
	for k, v := range props {
		evt.Properties.Set(k, v)
	}
	if err := c.client.Enqueue(evt); err != nil {
		c.logger.Error("posthog track failed", zap.String("event", event), zap.Error(err))
	}
}

// Identify sets user profile properties in PostHog.
func (c *PostHogClient) Identify(userID string, props map[string]any) {
	if c.client == nil {
		return
	}
	evt := posthog.Identify{
		DistinctId: userID,
		Properties: posthog.NewProperties(),
		Timestamp:  time.Now(),
	}
	for k, v := range props {
		evt.Properties.Set(k, v)
	}
	if err := c.client.Enqueue(evt); err != nil {
		c.logger.Error("posthog identify failed", zap.String("user_id", userID), zap.Error(err))
	}
}

// Close flushes remaining events and shuts down the client.
func (c *PostHogClient) Close() {
	if c.client == nil {
		return
	}
	if err := c.client.Close(); err != nil {
		c.logger.Error("posthog close failed", zap.Error(err))
	}
}
