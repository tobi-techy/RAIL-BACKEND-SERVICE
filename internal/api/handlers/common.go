package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
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

// getUserIDFromContext is an alias for getUserID for compatibility
func getUserIDFromContext(c *gin.Context) (uuid.UUID, error) {
	return getUserID(c)
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
func respondBadRequest(c *gin.Context, message string, details ...map[string]interface{}) {
	var det map[string]interface{}
	if len(details) > 0 {
		det = details[0]
	}
	respondError(c, http.StatusBadRequest, "INVALID_REQUEST", message, det)
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

// parseDecimal parses a string to decimal.Decimal
func parseDecimal(s string) (decimal.Decimal, error) {
	if s == "" {
		return decimal.Zero, nil
	}
	return decimal.NewFromString(s)
}

// parseTime parses a string to time.Time (RFC3339 format)
func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, fmt.Errorf("empty time string")
	}
	return time.Parse(time.RFC3339, s)
}


// parseDecimalFloat converts float64 to decimal.Decimal
func parseDecimalFloat(f float64) decimal.Decimal {
	return decimal.NewFromFloat(f)
}

// respondForbidden sends a forbidden error
func respondForbidden(c *gin.Context, message string) {
	respondError(c, http.StatusForbidden, "FORBIDDEN", message, nil)
}

// respondConflict sends a conflict error
func respondConflict(c *gin.Context, message string) {
	respondError(c, http.StatusConflict, "CONFLICT", message, nil)
}

// respondSuccess sends a success response with data
func respondSuccess(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, data)
}

// respondCreated sends a created response with data
func respondCreated(c *gin.Context, data interface{}) {
	c.JSON(http.StatusCreated, data)
}

// respondNoContent sends a no content response
func respondNoContent(c *gin.Context) {
	c.Status(http.StatusNoContent)
}

// parseUUID parses a string to uuid.UUID
func parseUUID(s string) (uuid.UUID, error) {
	if s == "" {
		return uuid.Nil, fmt.Errorf("empty UUID string")
	}
	return uuid.Parse(s)
}

// parseIntParam parses a query parameter to int with default value
func parseIntParam(c *gin.Context, param string, defaultVal int) int {
	if val := c.Query(param); val != "" {
		if parsed, err := parseInt(val); err == nil {
			return parsed
		}
	}
	return defaultVal
}

// parseInt parses string to int
func parseInt(s string) (int, error) {
	var i int
	_, err := fmt.Sscanf(s, "%d", &i)
	return i, err
}

// parseBoolParam parses a query parameter to bool with default value
func parseBoolParam(c *gin.Context, param string, defaultVal bool) bool {
	if val := c.Query(param); val != "" {
		return val == "true" || val == "1" || val == "yes"
	}
	return defaultVal
}
