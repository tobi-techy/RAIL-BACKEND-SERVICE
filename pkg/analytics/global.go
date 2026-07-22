package analytics

import (
	"context"
	"os"
	"sync"

	"go.uber.org/zap"
)

var (
	global     *Client
	globalOnce sync.Once

	ph     *PostHogClient
	phOnce sync.Once
)

// Init initializes the global Mixpanel client. Call once at app startup.
func Init(logger *zap.Logger) {
	globalOnce.Do(func() {
		token := os.Getenv("MIXPANEL_TOKEN")
		if token == "" {
			logger.Warn("MIXPANEL_TOKEN not set, analytics disabled")
			global = &Client{} // no-op client
			return
		}
		global = New(token, logger)
		logger.Info("Mixpanel analytics initialized")
	})
}

// InitPostHog initializes the global PostHog client. Call once at app startup.
func InitPostHog(logger *zap.Logger) {
	phOnce.Do(func() {
		apiKey := os.Getenv("POSTHOG_API_KEY")
		if apiKey == "" {
			logger.Warn("POSTHOG_API_KEY not set, PostHog analytics disabled")
			return
		}
		host := os.Getenv("POSTHOG_HOST")
		ph = NewPostHog(apiKey, host, logger)
	})
}

// G returns the global Mixpanel client.
func G() *Client {
	if global == nil {
		return &Client{} // no-op if not initialized
	}
	return global
}

// PH returns the global PostHog client (nil-safe).
func PH() *PostHogClient {
	return ph
}

// TrackEvent sends an event to both Mixpanel and PostHog.
func TrackEvent(ctx context.Context, userID, event string, props map[string]any) {
	G().Track(ctx, userID, event, props)
	if ph != nil {
		ph.Track(userID, event, props)
	}
}

// TrackEventWithProps sends an event and sets person properties on the PostHog profile.
// setProps are set via $set (overwrite), setOnceProps via $set_once (first-write-wins).
// This is critical for building churn/retention cohorts in PostHog.
func TrackEventWithProps(ctx context.Context, userID, event string, eventProps map[string]any, setProps, setOnceProps map[string]any) {
	G().Track(ctx, userID, event, eventProps)
	if ph != nil {
		merged := make(map[string]any, len(eventProps)+2)
		for k, v := range eventProps {
			merged[k] = v
		}
		if len(setProps) > 0 {
			merged["$set"] = setProps
		}
		if len(setOnceProps) > 0 {
			merged["$set_once"] = setOnceProps
		}
		ph.Track(userID, event, merged)
	}
}

// IdentifyUser sets user profile properties on both Mixpanel and PostHog.
func IdentifyUser(ctx context.Context, userID string, props map[string]any) {
	G().Identify(ctx, userID, props)
	if ph != nil {
		ph.Identify(userID, props)
	}
}

// Increment increments numeric profile properties on Mixpanel.
// PostHog uses $set instead of increment; callers can use IdentifyUser for that.
func Increment(ctx context.Context, userID string, props map[string]int) {
	G().Increment(ctx, userID, props)
}

// TrackRevenue tracks a revenue event on Mixpanel and PostHog.
func TrackRevenue(ctx context.Context, userID string, amount float64, props map[string]any) {
	G().TrackRevenue(ctx, userID, amount, props)
	if ph != nil {
		if props == nil {
			props = map[string]any{}
		}
		props["$revenue"] = amount
		ph.Track(userID, "revenue_earned", props)
	}
}

// FlushPostHog flushes any buffered PostHog events. Call before shutdown.
func FlushPostHog() {
	if ph != nil {
		ph.Close()
	}
}
