package analytics

import (
	"context"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Middleware returns a Gin middleware that automatically tracks key API events.
// It fires Mixpanel events based on route + response status for the most critical flows.
func Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		status := c.Writer.Status()
		path := c.FullPath()
		method := c.Request.Method
		userID := c.GetString("user_id")
		if userID == "" {
			return // skip anonymous requests
		}

		latencyMs := time.Since(start).Milliseconds()
		ctx := context.Background()

		props := map[string]any{
			"method":     method,
			"path":       path,
			"status":     status,
			"latency_ms": latencyMs,
		}

		// Only track successful operations for product metrics
		if status >= 200 && status < 300 {
			trackSuccessEvent(ctx, userID, method, path, props)
		} else if status >= 400 {
			trackFailureEvent(ctx, userID, method, path, props)
		}
	}
}

func trackSuccessEvent(ctx context.Context, uid, method, path string, props map[string]any) {
	switch {
	// Growth: Signup
	case method == "POST" && contains(path, "/auth/register"):
		TrackEvent(ctx, uid, EventSignupCompleted, props)

	// Growth: KYC
	case method == "POST" && contains(path, "/kyc") && contains(path, "submit"):
		TrackEvent(ctx, uid, EventKYCCompleted, props)

	// Activation: Deposit
	case method == "POST" && (contains(path, "/deposit") || contains(path, "/funding")):
		TrackEvent(ctx, uid, EventDepositCompleted, props)

	// Money Movement: Withdrawal
	case method == "POST" && contains(path, "/withdraw"):
		TrackEvent(ctx, uid, EventWithdrawalCompleted, props)

	// Money Movement: P2P
	case method == "POST" && contains(path, "/p2p"):
		TrackEvent(ctx, uid, EventP2PTransfer, props)

	// Money Movement: Card transaction
	case method == "POST" && contains(path, "/card") && contains(path, "transaction"):
		TrackEvent(ctx, uid, EventCardTransaction, props)

	// Activation: Auto-invest enabled
	case method == "POST" && contains(path, "/auto-invest"):
		TrackEvent(ctx, uid, EventAutoInvestEnabled, props)

	// Activation: Allocation
	case method == "POST" && contains(path, "/allocation"):
		TrackEvent(ctx, uid, EventAllocationExecuted, props)

	// AI Agent: Conversation
	case method == "POST" && contains(path, "/ai") && contains(path, "chat"):
		TrackEvent(ctx, uid, EventAIQuestionAsked, props)

	// Financial Behavior: Budget
	case method == "POST" && contains(path, "/budget"):
		TrackEvent(ctx, uid, EventBudgetSet, props)

	// Financial Behavior: Stash transfer
	case method == "POST" && contains(path, "/stash"):
		TrackEvent(ctx, uid, EventStashTransfer, props)

	// Revenue: Premium conversion
	case method == "POST" && contains(path, "/subscription"):
		TrackEvent(ctx, uid, EventPremiumConverted, props)
	}
}

func trackFailureEvent(ctx context.Context, uid, method, path string, props map[string]any) {
	switch {
	case contains(path, "/deposit") || contains(path, "/funding"):
		TrackEvent(ctx, uid, EventDepositFailed, props)
	case contains(path, "/withdraw"):
		TrackEvent(ctx, uid, EventWithdrawalFailed, props)
	case contains(path, "/kyc"):
		TrackEvent(ctx, uid, EventKYCFailed, props)
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
