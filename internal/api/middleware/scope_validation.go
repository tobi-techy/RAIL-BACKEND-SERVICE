package middleware

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RequireScope validates that the API key has the required scope
func RequireScope(requiredScope string) gin.HandlerFunc {
	return func(c *gin.Context) {
		scopes, exists := c.Get("api_key_scopes")
		if !exists {
			c.JSON(http.StatusForbidden, gin.H{
				"error":      "No API key scopes found",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		scopeList, ok := scopes.([]string)
		if !ok {
			c.JSON(http.StatusForbidden, gin.H{
				"error":      "Invalid scope format",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		// Check if required scope is present
		hasScope := false
		for _, scope := range scopeList {
			if scope == requiredScope || scope == "*" {
				hasScope = true
				break
			}
		}

		if !hasScope {
			c.JSON(http.StatusForbidden, gin.H{
				"error":      "Insufficient permissions",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// Require2FA validates that the user has completed 2FA verification
func Require2FA(twofaService TwoFAValidator) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := getUserIDFromContext(c)
		if userID == nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":      "User not authenticated",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		status, err := twofaService.GetStatus(c.Request.Context(), *userID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":      "Failed to check 2FA status",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		if !status.IsEnabled {
			c.JSON(http.StatusForbidden, gin.H{
				"error":      "2FA required for this operation",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// TwoFAValidator interface for 2FA validation
type TwoFAValidator interface {
	GetStatus(ctx context.Context, userID uuid.UUID) (*TwoFAStatus, error)
}

// TwoFAStatus represents 2FA status
type TwoFAStatus struct {
	IsEnabled bool `json:"is_enabled"`
}

func getUserIDFromContext(c *gin.Context) *uuid.UUID {
	userIDVal, exists := c.Get("user_id")
	if !exists {
		return nil
	}

	switch v := userIDVal.(type) {
	case uuid.UUID:
		return &v
	case string:
		if parsed, err := uuid.Parse(v); err == nil {
			return &parsed
		}
	}

	return nil
}