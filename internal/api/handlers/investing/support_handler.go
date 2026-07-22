package investing

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"go.uber.org/zap"
)

// SupportHandler handles the ElevenLabs support agent signed URL endpoint.
type SupportHandler struct {
	apiKey    string
	agentID   string
	logger    *zap.Logger
}

func NewSupportHandler(apiKey, agentID string, logger *zap.Logger) *SupportHandler {
	return &SupportHandler{
		apiKey:  apiKey,
		agentID: agentID,
		logger:  logger,
	}
}

// IssueSignedURL returns a signed URL and dynamic variables for the ElevenLabs support agent.
// The client (React Native WebView) uses this to connect to the support widget.
func (h *SupportHandler) IssueSignedURL(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	signedURL, err := infraai.FetchElevenLabsSignedURL(h.apiKey, h.agentID)
	if err != nil {
		h.logger.Error("failed to fetch support signed URL", zap.Error(err), zap.String("user_id", userID.String()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "support service unavailable"})
		return
	}

	// Build dynamic variables for the support agent
	email, _ := c.Get("user_email")
	emailStr, _ := email.(string)

	dynamicVars := map[string]string{
		"user_id":    userID.String(),
		"user_email": emailStr,
		"platform":   c.GetHeader("X-Platform"),
	}

	c.JSON(http.StatusOK, gin.H{
		"signed_url":        signedURL,
		"agent_id":          h.agentID,
		"dynamic_variables": dynamicVars,
	})
}
