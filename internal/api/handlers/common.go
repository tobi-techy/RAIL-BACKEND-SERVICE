package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
)



// getUserID extracts and validates user ID from context
func getUserID(c *gin.Context) (uuid.UUID, error) {
	userIDVal, exists := c.Get("user_id")
	if !exists {
		return uuid.Nil, fmt.Errorf("user ID not found in context")
	}

	switch v := userIDVal.(type) {
	case uuid.UUID:
		return v, nil
	case string:
		return uuid.Parse(v)
	default:
		return uuid.Nil, fmt.Errorf("invalid user ID type in context")
	}
}

// getRequestID extracts request ID from context
func getRequestID(c *gin.Context) string {
	if reqID, exists := c.Get("request_id"); exists {
		if id, ok := reqID.(string); ok {
			return id
		}
	}
	return ""
}

// respondError sends a standardized error response
func respondError(c *gin.Context, status int, code, message string, details map[string]interface{}) {
	c.JSON(status, entities.ErrorResponse{
		Code:    code,
		Message: message,
		Details: details,
	})
}

// respondUnauthorized sends an unauthorized error
func respondUnauthorized(c *gin.Context, message string) {
	respondError(c, http.StatusUnauthorized, "UNAUTHORIZED", message, nil)
}

// respondBadRequest sends a bad request error
func respondBadRequest(c *gin.Context, message string, details map[string]interface{}) {
	respondError(c, http.StatusBadRequest, "INVALID_REQUEST", message, details)
}

// respondInternalError sends an internal server error
func respondInternalError(c *gin.Context, message string) {
	respondError(c, http.StatusInternalServerError, "INTERNAL_ERROR", message, nil)
}

// respondNotFound sends a not found error
func respondNotFound(c *gin.Context, message string) {
	respondError(c, http.StatusNotFound, "NOT_FOUND", message, nil)
}

// isUserNotFoundError checks if error is a user not found error
func isUserNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return msg == "user not found" || msg == "sql: no rows in result set"
}
