package investing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	aiservice "github.com/rail-service/rail_service/internal/domain/services/ai"
	"go.uber.org/zap"
)

// ImageAnalysisHandler handles receipt/image analysis via GPT-4o vision.
type ImageAnalysisHandler struct {
	apiKey       string
	orchestrator *aiservice.Orchestrator
	logger       *zap.Logger
}

func NewImageAnalysisHandler(apiKey string, orchestrator *aiservice.Orchestrator, logger *zap.Logger) *ImageAnalysisHandler {
	return &ImageAnalysisHandler{apiKey: apiKey, orchestrator: orchestrator, logger: logger}
}

type imageRequest struct {
	Image   string `json:"image" binding:"required"` // base64-encoded image
	Message string `json:"message"`
}

// AnalyzeImage handles POST /v1/ai/chat/image
func (h *ImageAnalysisHandler) AnalyzeImage(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	if h.orchestrator.IsUserOverCostCeiling(c.Request.Context(), userID) {
		c.JSON(http.StatusOK, gin.H{"data": gin.H{
			"content":      "You've reached your monthly AI limit 💡",
			"over_ceiling": true,
			"tokens_used":  0,
		}})
		return
	}

	var req imageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "image is required"})
		return
	}

	if len(req.Image) > 20*1024*1024 { // 20MB base64 limit
		c.JSON(http.StatusBadRequest, gin.H{"error": "image too large (max 20MB)"})
		return
	}

	msg := req.Message
	if msg == "" {
		msg = "Analyze this receipt or transaction image. Extract: merchant name, amount, date, currency, and category. Format as a clear summary."
	}

	content, err := h.callVisionAPI(c.Request.Context(), req.Image, msg)
	if err != nil {
		h.logger.Error("vision API failed", zap.Error(err), zap.String("user_id", userID.String()))
		c.JSON(http.StatusOK, gin.H{"data": gin.H{
			"content":     "I couldn't analyze that image — try a clearer photo 📸",
			"tokens_used": 0,
			"fallback":    true,
		}})
		return
	}

	// Build receipt insight card
	cards := []map[string]interface{}{
		{
			"type":  "highlight",
			"title": "Receipt Analysis",
			"data":  map[string]interface{}{"source": "vision"},
		},
	}

	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"content":     content,
		"cards":       cards,
		"tokens_used": 0,
		"provider":    "openai-vision",
	}})
}

func (h *ImageAnalysisHandler) callVisionAPI(ctx context.Context, base64Image, message string) (string, error) {
	body := map[string]interface{}{
		"model":      "gpt-4o",
		"max_tokens": 1000,
		"messages": []map[string]interface{}{
			{
				"role": "system",
				"content": "You are Miriam, a financial assistant for Rail. When analyzing receipts or transaction images, extract key details (merchant, amount, date, currency, category) and present them clearly. If it's not a receipt, describe what you see and how it relates to the user's finances.",
			},
			{
				"role": "user",
				"content": []map[string]interface{}{
					{"type": "text", "text": message},
					{"type": "image_url", "image_url": map[string]string{
						"url":    "data:image/jpeg;base64," + base64Image,
						"detail": "low",
					}},
				},
			},
		},
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(reqCtx, "POST", "https://api.openai.com/v1/chat/completions", bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+h.apiKey)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("vision API call: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("vision API error %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}

	return result.Choices[0].Message.Content, nil
}
