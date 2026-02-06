package middleware

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// Default timeouts for different operation types
const (
	DefaultExternalAPITimeout = 30 * time.Second
	DefaultDatabaseTimeout    = 10 * time.Second
	DefaultCacheTimeout       = 5 * time.Second
)

func TimeoutMiddleware(timeout time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
		defer cancel()

		c.Request = c.Request.WithContext(ctx)

		done := make(chan struct{})
		go func() {
			c.Next()
			close(done)
		}()

		select {
		case <-done:
			return
		case <-ctx.Done():
			c.AbortWithStatusJSON(http.StatusGatewayTimeout, gin.H{
				"error":   "REQUEST_TIMEOUT",
				"message": "Request processing timeout",
			})
		}
	}
}

// WithExternalTimeout returns a context with timeout for external API calls.
// If the parent context already has a shorter deadline, it's preserved.
func WithExternalTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return withTimeoutIfNeeded(ctx, DefaultExternalAPITimeout)
}

// WithDatabaseTimeout returns a context with timeout for database operations.
func WithDatabaseTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return withTimeoutIfNeeded(ctx, DefaultDatabaseTimeout)
}

// WithCacheTimeout returns a context with timeout for cache operations.
func WithCacheTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return withTimeoutIfNeeded(ctx, DefaultCacheTimeout)
}

// withTimeoutIfNeeded adds a timeout only if the context doesn't already have a shorter deadline
func withTimeoutIfNeeded(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if deadline, ok := ctx.Deadline(); ok {
		if time.Until(deadline) < timeout {
			// Parent context has shorter deadline, use a no-op cancel
			return ctx, func() {}
		}
	}
	return context.WithTimeout(ctx, timeout)
}
