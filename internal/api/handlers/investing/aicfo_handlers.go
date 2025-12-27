// Deprecated AI-CFO handlers have been removed.
// This file contains stub implementations for compatibility.

package investing

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// AICfoHandler handles AI-CFO related requests (deprecated)
type AICfoHandler struct {
	aicfoService interface{} // Placeholder for deprecated service
	logger       *zap.Logger
}

// NewAICfoHandler creates a new AI-CFO handler (deprecated)
func NewAICfoHandler(logger *zap.Logger) *AICfoHandler {
	return &AICfoHandler{
		logger: logger,
	}
}

type GetWeeklySummaryRequest struct {
	WeekStart string `form:"week_start"` // Optional: ISO date format (YYYY-MM-DD)
}

// GetWeeklySummaryResponse represents the weekly summary response
type GetWeeklySummaryResponse struct {
	ID          string    `json:"id"`
	UserID      string    `json:"userId"`
	WeekStart   string    `json:"weekStart"`   // ISO date format
	SummaryMD   string    `json:"summaryMd"`   // Markdown content
	ArtifactURI string    `json:"artifactUri"` // URI to detailed analysis
	CreatedAt   string    `json:"createdAt"`   // ISO timestamp
}

// GenerateWeeklySummaryRequest represents the request to generate a new summary
type GenerateWeeklySummaryRequest struct {
	WeekStart string `json:"weekStart" binding:"required"` // ISO date format (YYYY-MM-DD)
}

// OnDemandAnalysisRequest represents the request for on-demand analysis
type OnDemandAnalysisRequest struct {
	AnalysisType string                 `json:"analysisType" binding:"required"` // diversification, risk, performance, allocation, rebalancing
	Parameters   map[string]interface{} `json:"parameters,omitempty"`            // Optional analysis parameters
}

// OnDemandAnalysisResponse represents the analysis response
type OnDemandAnalysisResponse struct {
	RequestID      string                 `json:"requestId"`
	Content        string                 `json:"content"`        // Generated content (markdown)
	ContentType    string                 `json:"contentType"`    // Content type
	Metadata       map[string]interface{} `json:"metadata"`       // Additional metadata
	TokensUsed     int                    `json:"-"`              // Internal use only
	ProcessingTime string                 `json:"processingTime"` // Duration in ms
	Model          string                 `json:"model"`
	CreatedAt      string                 `json:"createdAt"` // ISO timestamp
	ArtifactURI    string                 `json:"artifactUri,omitempty"`
}

// GetLatestWeeklySummary retrieves the latest weekly summary for the authenticated user (DEPRECATED)
func (h *AICfoHandler) GetLatestWeeklySummary(c *gin.Context) {
	h.logger.Warn("AI-CFO GetLatestWeeklySummary endpoint is deprecated")
	c.JSON(http.StatusGone, gin.H{"error": "AI-CFO feature has been deprecated"})
}

// GetWeeklySummary retrieves a weekly summary for a specific week (DEPRECATED)
func (h *AICfoHandler) GetWeeklySummary(c *gin.Context) {
	h.logger.Warn("AI-CFO GetWeeklySummary endpoint is deprecated")
	c.JSON(http.StatusGone, gin.H{"error": "AI-CFO feature has been deprecated"})
}

// GenerateWeeklySummary generates a new weekly summary for the specified week (DEPRECATED)
func (h *AICfoHandler) GenerateWeeklySummary(c *gin.Context) {
	h.logger.Warn("AI-CFO GenerateWeeklySummary endpoint is deprecated")
	c.JSON(http.StatusGone, gin.H{"error": "AI-CFO feature has been deprecated"})
}

// PerformOnDemandAnalysis performs on-demand portfolio analysis (DEPRECATED)
func (h *AICfoHandler) PerformOnDemandAnalysis(c *gin.Context) {
	h.logger.Warn("AI-CFO PerformOnDemandAnalysis endpoint is deprecated")
	c.JSON(http.StatusGone, gin.H{"error": "AI-CFO feature has been deprecated"})
}

// GetHealthStatus retrieves the health status of the AI-CFO service (DEPRECATED)
func (h *AICfoHandler) GetHealthStatus(c *gin.Context) {
	h.logger.Warn("AI-CFO GetHealthStatus endpoint is deprecated")
	c.JSON(http.StatusGone, gin.H{"error": "AI-CFO feature has been deprecated"})
}
