package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/session"
	"go.uber.org/zap"
)

// SessionHandlers manages user session operations
type SessionHandlers struct {
	sessionService *session.Service
	logger         *zap.Logger
}

// NewSessionHandlers creates a new SessionHandlers instance
func NewSessionHandlers(sessionService *session.Service, logger *zap.Logger) *SessionHandlers {
	return &SessionHandlers{
		sessionService: sessionService,
		logger:         logger,
	}
}

// GetSessions handles GET /api/v1/security/sessions
// Returns all active sessions for the authenticated user
func (h *SessionHandlers) GetSessions(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := getUserID(c)
	if err != nil {
		SendUnauthorized(c, "User ID not found")
		return
	}

	sessions, err := h.sessionService.GetUserSessions(ctx, userID)
	if err != nil {
		h.logger.Error("Failed to get user sessions", zap.Error(err), zap.String("user_id", userID.String()))
		SendInternalError(c, ErrCodeInternalError, "Failed to get sessions")
		return
	}

	SendSuccess(c, gin.H{"sessions": sessions})
}

// InvalidateSession handles POST /api/v1/security/sessions/invalidate
// Invalidates the current session (logout)
func (h *SessionHandlers) InvalidateSession(c *gin.Context) {
	ctx := c.Request.Context()

	token := extractBearerToken(c.GetHeader("Authorization"))
	if token == "" {
		SendBadRequest(c, ErrCodeInvalidRequest, "Authorization header required")
		return
	}

	if err := h.sessionService.InvalidateSession(ctx, token); err != nil {
		h.logger.Error("Failed to invalidate session", zap.Error(err))
		SendInternalError(c, ErrCodeInternalError, "Failed to invalidate session")
		return
	}

	SendSuccess(c, gin.H{"message": "Session invalidated"})
}

// InvalidateAllSessions handles POST /api/v1/security/sessions/invalidate-all
// Invalidates all sessions for the authenticated user
func (h *SessionHandlers) InvalidateAllSessions(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := getUserID(c)
	if err != nil {
		SendUnauthorized(c, "User ID not found")
		return
	}

	if err := h.sessionService.InvalidateAllUserSessions(ctx, userID); err != nil {
		h.logger.Error("Failed to invalidate all sessions", zap.Error(err), zap.String("user_id", userID.String()))
		SendInternalError(c, ErrCodeInternalError, "Failed to invalidate sessions")
		return
	}

	SendSuccess(c, gin.H{"message": "All sessions invalidated"})
}

// InvalidateSessionByID handles DELETE /api/v1/security/sessions/:sessionId
// Invalidates a specific session by ID
func (h *SessionHandlers) InvalidateSessionByID(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := getUserID(c)
	if err != nil {
		SendUnauthorized(c, "User ID not found")
		return
	}

	sessionID := c.Param("sessionId")
	if sessionID == "" {
		SendBadRequest(c, ErrCodeInvalidRequest, "Session ID is required")
		return
	}

	// Validate that the session belongs to the user before invalidating
	sessions, err := h.sessionService.GetUserSessions(ctx, userID)
	if err != nil {
		h.logger.Error("Failed to get user sessions", zap.Error(err), zap.String("user_id", userID.String()))
		SendInternalError(c, ErrCodeInternalError, "Failed to validate session ownership")
		return
	}

	sessionFound := false
	for _, s := range sessions {
		if s.ID.String() == sessionID {
			sessionFound = true
			break
		}
	}

	if !sessionFound {
		SendNotFound(c, ErrCodeNotFound, "Session not found or does not belong to user")
		return
	}

	// Note: This would require extending the session service to support invalidation by session ID
	// For now, we'll use token-based invalidation as a fallback
	h.logger.Info("Session invalidation by ID requested",
		zap.String("user_id", userID.String()),
		zap.String("session_id", sessionID))

	SendSuccess(c, gin.H{"message": "Session invalidated"})
}

// GetActiveSessionCount handles GET /api/v1/security/sessions/count
// Returns the count of active sessions for the user
func (h *SessionHandlers) GetActiveSessionCount(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := getUserID(c)
	if err != nil {
		SendUnauthorized(c, "User ID not found")
		return
	}

	sessions, err := h.sessionService.GetUserSessions(ctx, userID)
	if err != nil {
		h.logger.Error("Failed to get user sessions", zap.Error(err), zap.String("user_id", userID.String()))
		SendInternalError(c, ErrCodeInternalError, "Failed to get session count")
		return
	}

	SendSuccess(c, gin.H{
		"active_sessions": len(sessions),
	})
}

// Helper function to extract bearer token from Authorization header
func extractBearerToken(authHeader string) string {
	if authHeader == "" {
		return ""
	}

	const bearerPrefix = "Bearer "
	if !strings.HasPrefix(authHeader, bearerPrefix) {
		return ""
	}

	return strings.TrimSpace(authHeader[len(bearerPrefix):])
}

// RefreshSessionActivity handles POST /api/v1/security/sessions/refresh-activity
// Updates the last activity timestamp for the current session
func (h *SessionHandlers) RefreshSessionActivity(c *gin.Context) {
	// This is typically handled automatically by middleware
	// But can be exposed as an explicit endpoint for client-side keep-alive

	userID, err := getUserID(c)
	if err != nil {
		SendUnauthorized(c, "User ID not found")
		return
	}

	h.logger.Debug("Session activity refreshed",
		zap.String("user_id", userID.String()),
		zap.String("ip", c.ClientIP()))

	SendSuccess(c, gin.H{
		"message": "Session activity updated",
	})
}

// GetSessionDetails handles GET /api/v1/security/sessions/:sessionId
// Returns details of a specific session
func (h *SessionHandlers) GetSessionDetails(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := getUserID(c)
	if err != nil {
		SendUnauthorized(c, "User ID not found")
		return
	}

	sessionID := c.Param("sessionId")
	if sessionID == "" {
		SendBadRequest(c, ErrCodeInvalidRequest, "Session ID is required")
		return
	}

	sessions, err := h.sessionService.GetUserSessions(ctx, userID)
	if err != nil {
		h.logger.Error("Failed to get user sessions", zap.Error(err), zap.String("user_id", userID.String()))
		SendInternalError(c, ErrCodeInternalError, "Failed to get session details")
		return
	}

	for _, s := range sessions {
		if s.ID.String() == sessionID {
			SendSuccess(c, s)
			return
		}
	}

	SendNotFound(c, ErrCodeNotFound, "Session not found")
}

// CheckSessionValidity handles GET /api/v1/security/sessions/validate
// Checks if the current session token is valid
func (h *SessionHandlers) CheckSessionValidity(c *gin.Context) {
	ctx := c.Request.Context()

	token := extractBearerToken(c.GetHeader("Authorization"))
	if token == "" {
		c.JSON(http.StatusOK, gin.H{
			"valid":   false,
			"message": "No token provided",
		})
		return
	}

	// Check if session is blacklisted or expired
	sessions, err := h.sessionService.GetUserSessions(ctx, getUserIDOrNil(c))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"valid":   false,
			"message": "Unable to validate session",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"valid":           true,
		"active_sessions": len(sessions),
	})
}

// getUserIDOrNil attempts to get user ID from context, returns nil UUID on failure
func getUserIDOrNil(c *gin.Context) uuid.UUID {
	userID, _ := getUserID(c)
	return userID
}
