package investing

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"go.uber.org/zap"
)

// HandleServerTool handles ElevenLabs server tool webhook calls.
// ElevenLabs calls this endpoint directly when the agent invokes a tool.
// Auth: Bearer token matching the configured webhook secret.
// The user_id is passed as a dynamic variable from the session start.
//
// POST /v1/ai/voice/server-tool/:tool_name
func (h *VoiceHandler) HandleServerTool(c *gin.Context) {
	// Authenticate via bearer token
	authHeader := c.GetHeader("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") || strings.TrimPrefix(authHeader, "Bearer ") != h.webhookSecret {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	toolName := c.Param("tool_name")
	if toolName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "tool_name required"})
		return
	}

	// ElevenLabs sends query params (dynamic variables + LLM prompt fields)
	// and optionally a JSON body for body parameters.
	params := make(map[string]interface{})
	// Read body params first (if any)
	var bodyParams map[string]interface{}
	if err := c.ShouldBindJSON(&bodyParams); err == nil {
		for k, v := range bodyParams {
			params[k] = v
		}
	}
	// Query params override / supplement body (ElevenLabs sends user_id, tool, period, etc. here)
	for key, values := range c.Request.URL.Query() {
		if len(values) > 0 && values[0] != "" {
			params[key] = values[0]
		}
	}

	// Extract user_id (set as dynamic variable on ElevenLabs)
	userIDStr, _ := params["user_id"].(string)
	if userIDStr == "" {
		h.logger.Warn("server tool call missing user_id", zap.String("tool", toolName))
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_id required"})
		return
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user_id"})
		return
	}

	// Remove user_id from params before passing to tool executor
	delete(params, "user_id")

	toolCtx, cancel := context.WithTimeout(c.Request.Context(), voiceToolTimeout)
	defer cancel()

	tc := infraai.ToolCall{Name: toolName, Arguments: params}
	result, execErr := h.orchestrator.ExecuteToolPublic(toolCtx, userID, tc)
	if execErr != nil {
		h.logger.Warn("server tool execution failed",
			zap.String("user_id", userID.String()),
			zap.String("tool", toolName),
			zap.Error(execErr))
		c.JSON(http.StatusOK, result)
		return
	}

	c.JSON(http.StatusOK, result)
}
