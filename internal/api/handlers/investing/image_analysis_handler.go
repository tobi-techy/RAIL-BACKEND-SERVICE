package investing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/internal/domain/entities"
	aiservice "github.com/rail-service/rail_service/internal/domain/services/ai"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// ImageAnalysisHandler handles receipt/image analysis via GPT-4o vision.
type ImageAnalysisHandler struct {
	apiKey       string
	orchestrator *aiservice.Orchestrator
	receiptRepo  *repositories.ReceiptRepository
	logger       *zap.Logger
}

func NewImageAnalysisHandler(apiKey string, orchestrator *aiservice.Orchestrator, receiptRepo *repositories.ReceiptRepository, logger *zap.Logger) *ImageAnalysisHandler {
	return &ImageAnalysisHandler{apiKey: apiKey, orchestrator: orchestrator, receiptRepo: receiptRepo, logger: logger}
}

type imageRequest struct {
	Image   string `json:"image" binding:"required"` // base64-encoded image
	Message string `json:"message"`
}

// visionReceiptResponse is the structured JSON we ask GPT-4o to return.
type visionReceiptResponse struct {
	IsReceipt bool   `json:"is_receipt"`
	Merchant  string `json:"merchant"`
	Amount    string `json:"amount"`
	Currency  string `json:"currency"`
	Date      string `json:"date"`
	Category  string `json:"category"`
	Items     []struct {
		Name     string `json:"name"`
		Quantity int    `json:"quantity"`
		Price    string `json:"price"`
	} `json:"items"`
	Summary string `json:"summary"`
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

	if len(req.Image) > 20*1024*1024 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "image too large (max 20MB)"})
		return
	}

	raw, err := h.callVisionAPI(c.Request.Context(), req.Image, req.Message)
	if err != nil {
		h.logger.Error("vision API failed", zap.Error(err), zap.String("user_id", userID.String()))
		c.JSON(http.StatusOK, gin.H{"data": gin.H{
			"content":     "I couldn't analyze that image — try a clearer photo 📸",
			"tokens_used": 0,
			"fallback":    true,
		}})
		return
	}

	// Try to parse structured receipt data
	var parsed visionReceiptResponse
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil || !parsed.IsReceipt {
		// Not a receipt or couldn't parse — return raw analysis
		c.JSON(http.StatusOK, gin.H{"data": gin.H{
			"content":     raw,
			"tokens_used": 0,
			"provider":    "openai-vision",
		}})
		return
	}

	// Persist the receipt
	scan := h.buildReceiptScan(userID, req.Image, &parsed, raw)
	if h.receiptRepo != nil {
		if dbErr := h.receiptRepo.Create(c.Request.Context(), scan); dbErr != nil {
			h.logger.Warn("failed to persist receipt scan", zap.Error(dbErr))
		}
	}

	// Build rich response
	cards := []map[string]interface{}{
		{
			"type":  "receipt",
			"title": parsed.Merchant,
			"data": map[string]interface{}{
				"merchant": parsed.Merchant,
				"amount":   parsed.Amount,
				"currency": parsed.Currency,
				"date":     parsed.Date,
				"category": parsed.Category,
				"items":    parsed.Items,
				"saved":    true,
			},
		},
	}

	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"content":     parsed.Summary,
		"cards":       cards,
		"receipt_id":  scan.ID.String(),
		"tokens_used": 0,
		"provider":    "openai-vision",
	}})
}

func (h *ImageAnalysisHandler) buildReceiptScan(userID uuid.UUID, base64Image string, parsed *visionReceiptResponse, rawText string) *entities.ReceiptScan {
	amount, _ := decimal.NewFromString(parsed.Amount)
	currency := parsed.Currency
	if currency == "" {
		currency = "USD"
	}
	category := parsed.Category
	if category == "" {
		category = "Uncategorized"
	}

	itemsJSON, _ := json.Marshal(parsed.Items)
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(base64Image[:min(1024, len(base64Image))])))

	var receiptDate *time.Time
	if parsed.Date != "" {
		for _, layout := range []string{"2006-01-02", "01/02/2006", "02/01/2006", "Jan 2, 2006", "2 Jan 2006", "January 2, 2006"} {
			if t, err := time.Parse(layout, parsed.Date); err == nil {
				receiptDate = &t
				break
			}
		}
	}

	return &entities.ReceiptScan{
		ID:          uuid.New(),
		UserID:      userID,
		Merchant:    parsed.Merchant,
		Amount:      amount,
		Currency:    currency,
		ReceiptDate: receiptDate,
		Category:    category,
		Items:       itemsJSON,
		RawText:     &rawText,
		ImageHash:   &hash,
		CreatedAt:   time.Now(),
	}
}

const visionSystemPrompt = `You are a receipt and financial document analyzer. Extract structured data from images.

ALWAYS respond with valid JSON in this exact format:
{
  "is_receipt": true/false,
  "merchant": "Store/Business name",
  "amount": "123.45",
  "currency": "USD",
  "date": "2025-01-15",
  "category": "one of: Food & Dining, Groceries, Transport, Shopping, Entertainment, Health, Utilities, Logistics, Education, Services, Subscriptions, Other",
  "items": [{"name": "Item name", "quantity": 1, "price": "12.99"}],
  "summary": "A friendly 1-2 sentence summary of this receipt for the user"
}

Rules:
- If the image is NOT a receipt/invoice/bill, set is_receipt to false and put a description in summary.
- Extract the TOTAL amount, not subtotals.
- Parse the date into YYYY-MM-DD format.
- Categorize accurately: fast food = "Food & Dining", supermarket = "Groceries", Uber/Bolt = "Transport", Amazon = "Shopping", etc.
- List individual items if visible on the receipt.
- Amount should be a plain number string without currency symbols.
- Currency should be a 3-letter code (USD, NGN, GBP, EUR).`

func (h *ImageAnalysisHandler) callVisionAPI(ctx context.Context, base64Image, userMessage string) (string, error) {
	textContent := "Analyze this image and extract receipt details."
	if userMessage != "" {
		textContent = userMessage + "\n\nAlso extract structured receipt details if this is a receipt."
	}

	body := map[string]interface{}{
		"model":      "gpt-4o",
		"max_tokens": 1000,
		"temperature": 0.1,
		"messages": []map[string]interface{}{
			{"role": "system", "content": visionSystemPrompt},
			{
				"role": "user",
				"content": []map[string]interface{}{
					{"type": "text", "text": textContent},
					{"type": "image_url", "image_url": map[string]string{
						"url":    "data:image/jpeg;base64," + base64Image,
						"detail": "high",
					}},
				},
			},
		},
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
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
