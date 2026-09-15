package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// RequireInteractiveSession rejects delegated agent callers.
//
// Investing has a hard rule: money can only leave a portfolio through an
// interactive, step-up-verified session in the Rail app. The Python MIRIAM agent
// authenticates with a delegated agent token, so it must be told plainly that
// the action is not available over a messaging channel instead of receiving a
// confusing downstream error. Read paths and confirmations stay available to the
// agent; only money-out endpoints use this guard.
func RequireInteractiveSession() gin.HandlerFunc {
	return func(c *gin.Context) {
		if isAgentContext(c) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error":      "INTERACTIVE_SESSION_REQUIRED",
				"code":       "INTERACTIVE_SESSION_REQUIRED",
				"message":    "This has to be confirmed by the user inside the Rail app, not over chat.",
				"request_id": c.GetString("request_id"),
			})
			return
		}
		c.Next()
	}
}

// isAgentContext reports whether the request was authenticated with a delegated
// agent token (the Python MIRIAM agent acting for a user).
func isAgentContext(c *gin.Context) bool {
	if value, exists := c.Get("is_agent"); exists {
		if isAgent, ok := value.(bool); ok {
			return isAgent
		}
	}
	return false
}
