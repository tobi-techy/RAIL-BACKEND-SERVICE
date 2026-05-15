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

// G returns the global analytics client.
func G() *Client {
	if global == nil {
		return &Client{} // no-op if not initialized
	}
	return global
}

// TrackEvent is a convenience function for the global client.
func TrackEvent(ctx context.Context, userID, event string, props map[string]any) {
	G().Track(ctx, userID, event, props)
}

// IdentifyUser is a convenience function for the global client.
func IdentifyUser(ctx context.Context, userID string, props map[string]any) {
	G().Identify(ctx, userID, props)
}
