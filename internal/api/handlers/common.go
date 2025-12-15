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

// UserContext holds extracted user information from the request context
type UserContext struct {
	UserID uuid.UUID
	Email  string
	Role   string
}

// ExtractUserContext extracts user context from gin context, returns error if unauthorized
func ExtractUserContext(c *gin.Context) (*UserContext, error) {
	userID, err := getUserID(c)
	if err != nil {
		return nil, fmt.Errorf("unauthorized: %w", err)
	}

	return &UserContext{
		UserID: userID,
		Email:  c.GetString("user_email"),
		Role:   c.GetString("user_role"),
	}, nil
}

// RequireUserContext extracts user context or sends unauthorized error
func RequireUserContext(c *gin.Context) *UserContext {
	ctx, err := ExtractUserContext(c)
	if err != nil {
		respondUnauthorized(c, "User not authenticated")
		return nil
	}
	return ctx
}

// RequireAdminContext extracts user context and verifies admin role
func RequireAdminContext(c *gin.Context) *UserContext {
	ctx := RequireUserContext(c)
	if ctx == nil {
		return nil
	}

	if ctx.Role != "admin" && ctx.Role != "super_admin" {
		respondForbidden(c, "Admin privileges required")
		return nil
	}

	return ctx
}

// RequireSuperAdminContext extracts user context and verifies super admin role
func RequireSuperAdminContext(c *gin.Context) *UserContext {
	ctx := RequireUserContext(c)
	if ctx == nil {
		return nil
	}

	if ctx.Role != "super_admin" {
		respondForbidden(c, "Super admin privileges required")
		return nil
	}

	return ctx
}

// PaginationParams holds pagination parameters
type PaginationParams struct {
	Limit  int
	Offset int
}

// ExtractPagination extracts pagination parameters from query
func ExtractPagination(c *gin.Context, defaultLimit, maxLimit int) PaginationParams {
	limit := parseIntParam(c, "limit", defaultLimit)
	if limit > maxLimit {
		limit = maxLimit
	}
	if limit < 1 {
		limit = defaultLimit
	}

	offset := parseIntParam(c, "offset", 0)
	if offset < 0 {
		offset = 0
	}

	// Also support cursor-based pagination
	if cursor := c.Query("cursor"); cursor != "" {
		if o, err := parseInt(cursor); err == nil && o >= 0 {
			offset = o
		}
	}

	return PaginationParams{
		Limit:  limit,
		Offset: offset,
	}
}

// BindAndValidate binds JSON to a struct and validates it
// Returns true if successful, false if error was sent
func BindAndValidate(c *gin.Context, req interface{}) bool {
	if err := c.ShouldBindJSON(req); err != nil {
		respondBadRequest(c, "Invalid request format", map[string]interface{}{"error": err.Error()})
		return false
	}
	return true
}

// ParsePathUUID parses a UUID from path parameter
// Returns true if successful, false if error was sent
func ParsePathUUID(c *gin.Context, param string) (uuid.UUID, bool) {
	str := c.Param(param)
	if str == "" {
		respondBadRequest(c, fmt.Sprintf("Missing %s parameter", param), nil)
		return uuid.Nil, false
	}

	id, err := uuid.Parse(str)
	if err != nil {
		respondBadRequest(c, fmt.Sprintf("Invalid %s format", param), map[string]interface{}{"value": str})
		return uuid.Nil, false
	}

	return id, true
}

// HandleServiceError handles common service errors and sends appropriate HTTP response
// Returns true if error was handled, false if no error
func HandleServiceError(c *gin.Context, err error, resourceName string) bool {
	if err == nil {
		return false
	}

	errMsg := err.Error()

	// Check for common error patterns
	switch {
	case errMsg == "not found" || errMsg == resourceName+" not found" || errMsg == "sql: no rows in result set":
		respondNotFound(c, fmt.Sprintf("%s not found", resourceName))
	case containsCI(errMsg, "already exists"):
		respondConflict(c, fmt.Sprintf("%s already exists", resourceName))
	case containsCI(errMsg, "unauthorized"):
		respondUnauthorized(c, errMsg)
	case containsCI(errMsg, "forbidden") || containsCI(errMsg, "permission"):
		respondForbidden(c, errMsg)
	case containsCI(errMsg, "invalid"):
		respondBadRequest(c, errMsg, nil)
	case containsCI(errMsg, "insufficient"):
		respondBadRequest(c, errMsg, nil)
	default:
		respondInternalError(c, "An unexpected error occurred")
	}

	return true
}

// containsCI checks if substr is in s (case-insensitive)
func containsCI(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && containsLowerStr(toLowerStr(s), toLowerStr(substr))))
}

func containsLowerStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func toLowerStr(s string) string {
	b := make([]byte, len(s))
	for i := range s {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		} else {
			b[i] = c
		}
	}
	return string(b)
}
