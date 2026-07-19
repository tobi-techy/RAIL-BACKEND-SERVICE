package investing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/internal/domain/entities"
	aiservice "github.com/rail-service/rail_service/internal/domain/services/ai"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
	"golang.org/x/image/draw"
)

// ReceiptMemoryStore stores receipt-related memories for long-term recall.
// eventDate is "YYYY-MM-DD" (empty if unknown) so recalls can be time-ranked;
// metadata carries structured tags (type/category/merchant) for filtered search.
type ReceiptMemoryStore interface {
	StoreMemory(ctx context.Context, userID string, content string, eventDate string, metadata map[string]string) error
}

// ImageAnalysisHandler handles receipt/image analysis via vision-capable LLM.
type ImageAnalysisHandler struct {
	apiKey        string
	visionURL     string  // e.g. https://api.openai.com/v1 or https://api.moonshot.ai/v1
	visionModel   string  // e.g. gpt-4o or moonshot-v1-8k
	visionTemp    float64 // temperature for vision calls (Kimi requires 1.0)
	orchestrator  aiservice.ChatEngine
	receiptRepo   *repositories.ReceiptRepository
	budgetRepo    *repositories.BudgetRepository
	spendingRepo  *repositories.LedgerSpendingRepository
	bankStmtRepo  *repositories.BankStatementRepository
	memoryStore   ReceiptMemoryStore
	convPersister ConversationPersister
	logger        *zap.Logger
}

// ConversationPersister saves image messages to conversations.
type ConversationPersister interface {
	RecordImageExchange(ctx context.Context, convID uuid.UUID, userMsg, assistantMsg string, thumbnail string, tokens int, model string) error
}

func NewImageAnalysisHandler(apiKey string, orchestrator aiservice.ChatEngine, receiptRepo *repositories.ReceiptRepository, logger *zap.Logger) *ImageAnalysisHandler {
	return &ImageAnalysisHandler{
		apiKey:       apiKey,
		visionURL:    "https://api.openai.com/v1",
		visionModel:  "gpt-4o",
		visionTemp:   0.1,
		orchestrator: orchestrator,
		receiptRepo:  receiptRepo,
		logger:       logger,
	}
}

// NewImageAnalysisHandlerWithVision creates a handler using any OpenAI-compatible vision endpoint.
func NewImageAnalysisHandlerWithVision(apiKey, baseURL, model string, orchestrator aiservice.ChatEngine, receiptRepo *repositories.ReceiptRepository, logger *zap.Logger) *ImageAnalysisHandler {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	if model == "" {
		model = "gpt-4o"
	}
	// Kimi moonshot models require temperature=1.0
	temp := 0.1
	if strings.Contains(model, "moonshot") || strings.Contains(model, "kimi") {
		temp = 1.0
	}
	return &ImageAnalysisHandler{
		apiKey:       apiKey,
		visionURL:    baseURL,
		visionModel:  model,
		visionTemp:   temp,
		orchestrator: orchestrator,
		receiptRepo:  receiptRepo,
		logger:       logger,
	}
}

// SetBudgetRepo sets the budget repository for budget impact lookups.
func (h *ImageAnalysisHandler) SetBudgetRepo(b *repositories.BudgetRepository) {
	h.budgetRepo = b
}

// SetConversationPersister sets the conversation persister for image message persistence.
func (h *ImageAnalysisHandler) SetConversationPersister(cp ConversationPersister) {
	h.convPersister = cp
}

// SetSpendingRepo sets the spending repository for category spending lookups.
func (h *ImageAnalysisHandler) SetSpendingRepo(s *repositories.LedgerSpendingRepository) {
	h.spendingRepo = s
}

// SetBankStatementRepo sets the bank statement repository for receipt-transaction matching.
func (h *ImageAnalysisHandler) SetBankStatementRepo(r *repositories.BankStatementRepository) {
	h.bankStmtRepo = r
}

// SetMemoryStore sets the memory store for persisting receipt match memories.
func (h *ImageAnalysisHandler) SetMemoryStore(m ReceiptMemoryStore) {
	h.memoryStore = m
}

type imageRequest struct {
	Image          string `json:"image" binding:"required"` // base64-encoded image
	Message        string `json:"message"`
	ConversationID string `json:"conversation_id"`
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

	if h.orchestrator != nil && h.orchestrator.IsUserOverCostCeiling(c.Request.Context(), userID) {
		c.JSON(http.StatusOK, gin.H{"data": gin.H{
			"content":      "You've hit your monthly AI limit. You can still check balances and transactions in the app tabs — Miriam will be back at full power next month.",
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

	raw, tokensUsed, err := h.callVisionAPI(c.Request.Context(), req.Image, req.Message)
	if err != nil {
		h.logger.Error("vision API failed", zap.Error(err), zap.String("user_id", userID.String()))
		c.JSON(http.StatusOK, gin.H{"data": gin.H{
			"content":     "I couldn't analyze that image — try a clearer photo",
			"tokens_used": 0,
			"fallback":    true,
		}})
		return
	}

	// Track vision API usage
	if h.orchestrator != nil && tokensUsed > 0 {
		h.orchestrator.TrackVisionUsage(c.Request.Context(), userID, tokensUsed)
	}

	// Strip markdown code fences if present (some models wrap JSON in ```json ... ```)
	cleanRaw := strings.TrimSpace(raw)
	if strings.HasPrefix(cleanRaw, "```") {
		if idx := strings.Index(cleanRaw, "\n"); idx != -1 {
			cleanRaw = cleanRaw[idx+1:]
		}
		cleanRaw = strings.TrimSuffix(strings.TrimSpace(cleanRaw), "```")
		cleanRaw = strings.TrimSpace(cleanRaw)
	}

	// Try to parse structured receipt data
	var parsed visionReceiptResponse
	if err := json.Unmarshal([]byte(cleanRaw), &parsed); err != nil || !parsed.IsReceipt {
		// Not a receipt or couldn't parse — return structured card
		summary := raw
		if parsed.Summary != "" {
			summary = parsed.Summary
		}
		c.JSON(http.StatusOK, gin.H{"data": gin.H{
			"content":     summary,
			"tokens_used": tokensUsed,
			"provider":    "openai-vision",
			"cards": []map[string]interface{}{
				{
					"type":  "image_analysis",
					"title": "Image Analysis",
					"data": map[string]interface{}{
						"summary":    summary,
						"is_receipt": false,
					},
				},
			},
		}})
		return
	}

	// Persist the receipt
	scan := h.buildReceiptScan(userID, req.Image, &parsed, raw)
	saved := false
	if h.receiptRepo != nil {
		// Check for duplicate
		if scan.ImageHash != nil {
			exists, err := h.receiptRepo.ExistsByImageHash(c.Request.Context(), userID, *scan.ImageHash)
			if err == nil && exists {
				existing, _ := h.receiptRepo.GetByImageHash(c.Request.Context(), userID, *scan.ImageHash)
				if existing != nil {
					c.JSON(http.StatusOK, gin.H{"data": gin.H{
						"content":    "This receipt has already been scanned",
						"receipt_id": existing.ID.String(),
						"duplicate":  true,
						"provider":   "openai-vision",
					}})
					return
				}
			}
		}

		// Only persist if amount is valid
		if scan.Amount.IsPositive() {
			if dbErr := h.receiptRepo.Create(c.Request.Context(), scan); dbErr != nil {
				// Handle unique constraint violation (concurrent duplicate)
				if strings.Contains(dbErr.Error(), "uq_receipt_scans_user_hash") || strings.Contains(dbErr.Error(), "duplicate key") {
					existing, _ := h.receiptRepo.GetByImageHash(c.Request.Context(), userID, *scan.ImageHash)
					if existing != nil {
						c.JSON(http.StatusOK, gin.H{"data": gin.H{
							"content":    "This receipt has already been scanned",
							"receipt_id": existing.ID.String(),
							"duplicate":  true,
							"provider":   "openai-vision",
						}})
						return
					}
				}
				h.logger.Warn("failed to persist receipt scan", zap.Error(dbErr))
			} else {
				saved = true
				// Persist receipt to long-term memory so it survives conversation
				// summarization and can be recalled in future conversations.
				if h.memoryStore != nil {
					dateStr := "unknown date"
					if scan.ReceiptDate != nil {
						dateStr = scan.ReceiptDate.Format("Jan 2, 2006")
					}
					memContent := fmt.Sprintf("Scanned receipt: %s %s at %s on %s (%s)",
						scan.Currency, scan.Amount.StringFixed(0), parsed.Merchant, dateStr, parsed.Category)
					eventDate := ""
					if scan.ReceiptDate != nil {
						eventDate = scan.ReceiptDate.Format("2006-01-02")
					}
					meta := map[string]string{
						"type":     "receipt",
						"category": scan.Category,
						"merchant": parsed.Merchant,
						"currency": scan.Currency,
					}
					go func(uid uuid.UUID, content, ev string, md map[string]string) {
						smCtx, smCancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer smCancel()
						_ = h.memoryStore.StoreMemory(smCtx, uid.String(), content, ev, md)
					}(userID, memContent, eventDate, meta)
				}
			}
		} else {
			h.logger.Warn("receipt amount invalid, skipping persistence", zap.String("raw_amount", parsed.Amount))
		}
	}

	// Receipt ↔ Statement Transaction Matching
	var statementMatch *string
	if saved && h.bankStmtRepo != nil && scan.ReceiptDate != nil {
		tolerance := 3 * 24 * time.Hour
		if txn, err := h.bankStmtRepo.FindMatchingTransaction(c.Request.Context(), userID, scan.Amount, *scan.ReceiptDate, tolerance); err == nil && txn != nil {
			msg := fmt.Sprintf("This receipt matches a transaction in your bank statement: %s %s to %s on %s",
				txn.Currency, txn.Amount.StringFixed(0), txn.Description, txn.TransactionDate.Format("Jan 2, 2006"))
			statementMatch = &msg
			// Store match as Supermemory memory
			if h.memoryStore != nil {
				memoryContent := fmt.Sprintf("Receipt from %s for %s %s on %s matched to bank statement transaction: %s",
					parsed.Merchant, scan.Currency, scan.Amount.StringFixed(0), scan.ReceiptDate.Format("Jan 2, 2006"), txn.Description)
				eventDate := scan.ReceiptDate.Format("2006-01-02")
				meta := map[string]string{
					"type":     "receipt_match",
					"category": scan.Category,
					"merchant": parsed.Merchant,
					"currency": scan.Currency,
				}
				go func(uid uuid.UUID, content, ev string, md map[string]string) {
					smCtx, smCancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer smCancel()
					_ = h.memoryStore.StoreMemory(smCtx, uid.String(), content, ev, md)
				}(userID, memoryContent, eventDate, meta)
			}
		}
	}

	// Budget impact lookup
	var budgetImpact map[string]interface{}
	if saved && h.budgetRepo != nil && h.spendingRepo != nil {
		if budget, err := h.budgetRepo.GetByUserID(c.Request.Context(), userID); err == nil && budget != nil {
			now := time.Now().UTC()
			monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
			monthEnd := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
			cats, err := h.spendingRepo.GetSpendingByCategory(c.Request.Context(), userID, monthStart, monthEnd)
			if err == nil {
				var catSpend decimal.Decimal
				for _, cat := range cats {
					if strings.EqualFold(cat.Category, parsed.Category) {
						catSpend = cat.Total
						break
					}
				}
				remaining := budget.MonthlyLimit.Sub(catSpend)
				pctUsed := decimal.Zero
				if budget.MonthlyLimit.IsPositive() {
					pctUsed = catSpend.Div(budget.MonthlyLimit).Mul(decimal.NewFromInt(100))
				}
				budgetImpact = map[string]interface{}{
					"category":         parsed.Category,
					"spent_this_month": catSpend.StringFixed(2),
					"budget_limit":     budget.MonthlyLimit.StringFixed(2),
					"remaining":        remaining.StringFixed(2),
					"percent_used":     pctUsed.StringFixed(1),
					"message":          fmt.Sprintf("This puts you at %s%% of your %s budget. $%s left.", pctUsed.StringFixed(0), parsed.Category, remaining.StringFixed(2)),
				}
			}
		}
	}

	// Build rich response
	cards := []map[string]interface{}{
		{
			"type":  "receipt",
			"title": parsed.Merchant,
			"data": map[string]interface{}{
				"merchant":      parsed.Merchant,
				"amount":        parsed.Amount,
				"currency":      parsed.Currency,
				"date":          parsed.Date,
				"category":      parsed.Category,
				"items":         parsed.Items,
				"saved":         saved,
				"budget_impact": budgetImpact,
			},
		},
	}

	resp := gin.H{
		"content":     parsed.Summary,
		"cards":       cards,
		"tokens_used": tokensUsed,
		"provider":    "openai-vision",
	}
	if saved {
		resp["receipt_id"] = scan.ID.String()
		// Suggest follow-up actions so the user knows what they can do next
		suggestions := []map[string]string{
			{"label": "Split with friends", "prompt": "Split this receipt with my friends"},
			{"label": "Add to budget", "prompt": "Add this to my budget tracking"},
		}
		if parsed.Category != "" {
			suggestions = append(suggestions, map[string]string{
				"label": "Category spending", "prompt": fmt.Sprintf("How much have I spent on %s this month?", parsed.Category),
			})
		}
		resp["suggestions"] = suggestions
	} else {
		resp["warning"] = "Could not extract a valid amount from this receipt"
	}
	if statementMatch != nil {
		resp["statement_match"] = *statementMatch
	}
	// Include thumbnail so frontend can display the image in chat history
	if scan.Thumbnail != nil {
		resp["image_url"] = "data:image/jpeg;base64," + *scan.Thumbnail
	}

	// Persist to conversation if conversation_id provided
	if req.ConversationID != "" && h.convPersister != nil {
		if convID, err := uuid.Parse(req.ConversationID); err == nil {
			resp["conversation_id"] = req.ConversationID
			thumbValue := ""
			if scan.Thumbnail != nil {
				thumbValue = "data:image/jpeg;base64," + *scan.Thumbnail
			}
			// Build a rich assistant message for conversation context so follow-up
			// questions ("what was the total?", "split this") have full receipt data.
			richSummary := parsed.Summary + "\n\n"
			richSummary += fmt.Sprintf("Receipt details:\n- Merchant: %s\n- Amount: %s %s\n- Date: %s\n- Category: %s",
				parsed.Merchant, parsed.Amount, parsed.Currency, parsed.Date, parsed.Category)
			if len(parsed.Items) > 0 {
				richSummary += "\n- Items:"
				for _, item := range parsed.Items {
					richSummary += fmt.Sprintf("\n  • %s (x%d) — %s", item.Name, item.Quantity, item.Price)
				}
			}
			if saved {
				richSummary += fmt.Sprintf("\n- Receipt ID: %s", scan.ID.String())
			}
			go func(convID uuid.UUID, userMsg, summary, thumb string, tokens int, model string) {
				persistCtx, persistCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer persistCancel()
				if err := h.convPersister.RecordImageExchange(
					persistCtx, convID, userMsg, summary, thumb, tokens, model,
				); err != nil {
					h.logger.Warn("failed to persist image exchange to conversation", zap.Error(err))
				}
			}(convID, req.Message, richSummary, thumbValue, tokensUsed, h.visionModel)
		}
	}

	c.JSON(http.StatusOK, gin.H{"data": resp})
}

func normalizeCurrency(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "naira", "₦", "ngn":
		return "NGN"
	case "dollar", "$", "usd":
		return "USD"
	case "pound", "£", "gbp":
		return "GBP"
	case "euro", "€", "eur":
		return "EUR"
	case "":
		return ""
	default:
		return strings.ToUpper(strings.TrimSpace(raw))
	}
}

// inferCurrencyFromAmount detects currency symbols embedded in the amount string.
func inferCurrencyFromAmount(amount string) string {
	if strings.Contains(amount, "₦") || strings.Contains(amount, "NGN") {
		return "NGN"
	}
	if strings.Contains(amount, "£") || strings.Contains(amount, "GBP") {
		return "GBP"
	}
	if strings.Contains(amount, "€") || strings.Contains(amount, "EUR") {
		return "EUR"
	}
	if strings.Contains(amount, "$") || strings.Contains(amount, "USD") {
		return "USD"
	}
	return ""
}

func (h *ImageAnalysisHandler) buildReceiptScan(userID uuid.UUID, base64Image string, parsed *visionReceiptResponse, rawText string) *entities.ReceiptScan {
	amount, _ := decimal.NewFromString(parsed.Amount)
	currency := normalizeCurrency(parsed.Currency)
	if currency == "" {
		currency = inferCurrencyFromAmount(parsed.Amount)
	}
	if currency == "" {
		currency = "USD"
	}
	category := parsed.Category
	if category == "" {
		category = "Uncategorized"
	}

	itemsJSON, _ := json.Marshal(parsed.Items)
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(base64Image)))

	var receiptDate *time.Time
	if parsed.Date != "" {
		// Priority: YYYY-MM-DD (the format we request from the LLM)
		// Avoid ambiguous day/month formats like MM/DD/YYYY vs DD/MM/YYYY
		for _, layout := range []string{"2006-01-02", "Jan 2, 2006", "2 Jan 2006", "January 2, 2006", "2006/01/02"} {
			if t, err := time.Parse(layout, parsed.Date); err == nil {
				receiptDate = &t
				break
			}
		}
		// Sanity check: if the parsed date is more than 1 year in the past, discard it
		if receiptDate != nil && time.Since(*receiptDate) > 365*24*time.Hour {
			receiptDate = nil
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
		Thumbnail:   generateThumbnail(base64Image),
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

func (h *ImageAnalysisHandler) callVisionAPI(ctx context.Context, base64Image, userMessage string) (string, int, error) {
	textContent := "Analyze this image and extract receipt details."
	if userMessage != "" {
		textContent = userMessage + "\n\nAlso extract structured receipt details if this is a receipt."
	}

	body := map[string]interface{}{
		"model":       h.visionModel,
		"max_tokens":  1000,
		"temperature": h.visionTemp,
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
		return "", 0, fmt.Errorf("marshal request: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(reqCtx, "POST", strings.TrimRight(h.visionURL, "/")+"/chat/completions", bytes.NewReader(jsonBody))
	if err != nil {
		return "", 0, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+h.apiKey)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return "", 0, fmt.Errorf("vision API call: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		return "", 0, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("vision API error %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", 0, fmt.Errorf("parse response: %w", err)
	}

	if len(result.Choices) == 0 {
		return "", 0, fmt.Errorf("no choices in response")
	}

	return result.Choices[0].Message.Content, result.Usage.TotalTokens, nil
}

func generateThumbnail(b64 string) *string {
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return truncateBase64(b64)
	}
	// Guard against decoding extremely large images into memory
	const maxDecodedSize = 20 * 1024 * 1024 // 20MB decoded
	if len(data) > maxDecodedSize {
		return truncateBase64(b64)
	}
	// Peek at image dimensions before full decode to prevent decompression bombs
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return truncateBase64(b64)
	}
	const maxDimPixels = 8192
	if cfg.Width > maxDimPixels || cfg.Height > maxDimPixels || cfg.Width <= 0 || cfg.Height <= 0 {
		return truncateBase64(b64)
	}
	// Also guard total pixel count (e.g. 8192x8192 = 64M pixels * 4 bytes = 256MB)
	const maxTotalPixels = 16 * 1024 * 1024 // 16 megapixels
	if int64(cfg.Width)*int64(cfg.Height) > maxTotalPixels {
		return truncateBase64(b64)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return truncateBase64(b64)
	}
	bounds := img.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()
	if w <= 0 || h <= 0 {
		return truncateBase64(b64)
	}
	const maxWidth = 200
	if w > maxWidth {
		newH := h * maxWidth / w
		if newH <= 0 {
			newH = 1
		}
		dst := image.NewRGBA(image.Rect(0, 0, maxWidth, newH))
		draw.BiLinear.Scale(dst, dst.Bounds(), img, bounds, draw.Over, nil)
		img = dst
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 60}); err != nil {
		return truncateBase64(b64)
	}
	thumb := base64.StdEncoding.EncodeToString(buf.Bytes())
	return &thumb
}

func truncateBase64(b64 string) *string {
	return nil
}

// GetReceipts handles GET /v1/ai/receipts
func (h *ImageAnalysisHandler) GetReceipts(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if offset < 0 {
		offset = 0
	}
	if h.receiptRepo == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "receipt service unavailable"})
		return
	}
	scans, err := h.receiptRepo.GetByUserIDPaginated(c.Request.Context(), userID, limit, offset)
	if err != nil {
		h.logger.Error("failed to get receipts", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get receipts"})
		return
	}
	// Strip internal fields before returning to client
	type safeReceipt struct {
		ID          string          `json:"id"`
		Merchant    string          `json:"merchant"`
		Amount      string          `json:"amount"`
		Currency    string          `json:"currency"`
		ReceiptDate *string         `json:"receipt_date,omitempty"`
		Category    string          `json:"category"`
		Items       json.RawMessage `json:"items"`
		CreatedAt   string          `json:"created_at"`
	}
	safe := make([]safeReceipt, 0, len(scans))
	for _, s := range scans {
		r := safeReceipt{
			ID:        s.ID.String(),
			Merchant:  s.Merchant,
			Amount:    s.Amount.StringFixed(2),
			Currency:  s.Currency,
			Category:  s.Category,
			Items:     s.Items,
			CreatedAt: s.CreatedAt.Format(time.RFC3339),
		}
		if s.ReceiptDate != nil {
			d := s.ReceiptDate.Format("2006-01-02")
			r.ReceiptDate = &d
		}
		safe = append(safe, r)
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"receipts": safe, "limit": limit, "offset": offset}})
}

// UpdateReceipt handles PUT /v1/ai/receipts/:id
func (h *ImageAnalysisHandler) UpdateReceipt(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	receiptID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid receipt id"})
		return
	}
	if h.receiptRepo == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "receipt service unavailable"})
		return
	}
	existing, err := h.receiptRepo.GetByID(c.Request.Context(), userID, receiptID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "receipt not found"})
		return
	}
	var req struct {
		Merchant    string  `json:"merchant"`
		Amount      string  `json:"amount"`
		Currency    string  `json:"currency"`
		Category    string  `json:"category"`
		ReceiptDate *string `json:"receipt_date"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if req.Merchant != "" {
		existing.Merchant = req.Merchant
	}
	if req.Amount != "" {
		if a, err := decimal.NewFromString(req.Amount); err == nil {
			existing.Amount = a
		}
	}
	if req.Currency != "" {
		existing.Currency = strings.ToUpper(req.Currency)
	}
	if req.Category != "" {
		existing.Category = req.Category
	}
	if req.ReceiptDate != nil {
		if *req.ReceiptDate == "" {
			existing.ReceiptDate = nil
		} else if t, err := time.Parse("2006-01-02", *req.ReceiptDate); err == nil {
			existing.ReceiptDate = &t
		}
	}
	if err := h.receiptRepo.Update(c.Request.Context(), existing); err != nil {
		h.logger.Error("failed to update receipt", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update receipt"})
		return
	}
	receiptDateStr := ""
	if existing.ReceiptDate != nil {
		receiptDateStr = existing.ReceiptDate.Format("2006-01-02")
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"id":           existing.ID.String(),
		"merchant":     existing.Merchant,
		"amount":       existing.Amount.StringFixed(2),
		"currency":     existing.Currency,
		"category":     existing.Category,
		"receipt_date": receiptDateStr,
		"items":        existing.Items,
	}})
}

// DeleteReceipt handles DELETE /v1/ai/receipts/:id
func (h *ImageAnalysisHandler) DeleteReceipt(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	receiptID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid receipt id"})
		return
	}
	if h.receiptRepo == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "receipt service unavailable"})
		return
	}
	// Verify ownership
	if _, err := h.receiptRepo.GetByID(c.Request.Context(), userID, receiptID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "receipt not found"})
		return
	}
	if err := h.receiptRepo.Delete(c.Request.Context(), userID, receiptID); err != nil {
		h.logger.Error("failed to delete receipt", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete receipt"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"deleted": true}})
}

// BatchAnalyzeImages handles POST /v1/ai/chat/images (batch)
func (h *ImageAnalysisHandler) BatchAnalyzeImages(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	if h.orchestrator != nil && h.orchestrator.IsUserOverCostCeiling(c.Request.Context(), userID) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": gin.H{"code": "AI_LIMIT_REACHED", "message": "You've hit your monthly AI limit. You can still check balances and transactions in the app tabs — Miriam will be back at full power next month."}})
		return
	}

	var req struct {
		Images []imageRequest `json:"images" binding:"required"`
		Max    int            `json:"max"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "images array is required"})
		return
	}

	maxImages := 5
	if req.Max > 0 && req.Max < maxImages {
		maxImages = req.Max
	}
	if len(req.Images) > maxImages {
		req.Images = req.Images[:maxImages]
	}

	type batchResult struct {
		Index     int    `json:"index"`
		ReceiptID string `json:"receipt_id,omitempty"`
		Merchant  string `json:"merchant,omitempty"`
		Amount    string `json:"amount,omitempty"`
		Saved     bool   `json:"saved"`
		Error     string `json:"error,omitempty"`
	}

	results := make([]batchResult, len(req.Images))
	tokens := make([]int, len(req.Images))

	var wg sync.WaitGroup
	for i, img := range req.Images {
		wg.Add(1)
		go func(i int, img imageRequest) {
			defer wg.Done()

			if len(img.Image) > 20*1024*1024 {
				results[i] = batchResult{Index: i, Error: "image too large", Saved: false}
				return
			}

			raw, toks, err := h.callVisionAPI(c.Request.Context(), img.Image, img.Message)
			tokens[i] = toks
			if err != nil {
				h.logger.Warn("batch vision API failed", zap.Int("index", i), zap.Error(err))
				results[i] = batchResult{Index: i, Error: "Could not analyze image", Saved: false}
				return
			}

			var parsed visionReceiptResponse
			if err := json.Unmarshal([]byte(raw), &parsed); err != nil || !parsed.IsReceipt {
				results[i] = batchResult{Index: i, Error: "Could not parse receipt", Saved: false}
				return
			}

			scan := h.buildReceiptScan(userID, img.Image, &parsed, raw)
			saved := false

			if h.receiptRepo != nil && scan.Amount.IsPositive() {
				if scan.ImageHash != nil {
					if exists, err := h.receiptRepo.ExistsByImageHash(c.Request.Context(), userID, *scan.ImageHash); err == nil && exists {
						results[i] = batchResult{Index: i, Error: "duplicate receipt", Saved: false}
						return
					}
				}
				if dbErr := h.receiptRepo.Create(c.Request.Context(), scan); dbErr != nil {
					if strings.Contains(dbErr.Error(), "uq_receipt_scans_user_hash") || strings.Contains(dbErr.Error(), "duplicate key") {
						results[i] = batchResult{Index: i, Error: "duplicate receipt", Saved: false}
						return
					}
					h.logger.Warn("batch: failed to persist receipt", zap.Int("index", i), zap.Error(dbErr))
				} else {
					saved = true
				}
			}

			r := batchResult{Index: i, Merchant: parsed.Merchant, Amount: parsed.Amount, Saved: saved}
			if saved {
				r.ReceiptID = scan.ID.String()
			} else if !scan.Amount.IsPositive() {
				r.Error = "invalid amount"
			}
			results[i] = r
		}(i, img)
	}
	wg.Wait()

	var (
		totalTokens int
		savedCount  int
		failedCount int
		totalAmount decimal.Decimal
	)
	for i := range results {
		totalTokens += tokens[i]
		if results[i].Saved {
			savedCount++
			if amt, err := decimal.NewFromString(results[i].Amount); err == nil {
				totalAmount = totalAmount.Add(amt)
			}
		} else if results[i].Error != "" {
			failedCount++
		}
	}

	if h.orchestrator != nil && totalTokens > 0 {
		h.orchestrator.TrackVisionUsage(c.Request.Context(), userID, totalTokens)
	}

	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"results": results,
		"summary": gin.H{
			"total_scanned": len(req.Images),
			"saved":         savedCount,
			"failed":        failedCount,
			"total_amount":  totalAmount.StringFixed(2),
			"tokens_used":   totalTokens,
		},
	}})
}

// GetReceiptGallery handles GET /v1/ai/receipts/gallery
func (h *ImageAnalysisHandler) GetReceiptGallery(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if offset < 0 {
		offset = 0
	}
	category := c.Query("category")

	if h.receiptRepo == nil {
		h.logger.Error("receipt repository not initialized")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "receipt service unavailable"})
		return
	}

	scans, err := h.receiptRepo.GetGallery(c.Request.Context(), userID, category, limit, offset)
	if err != nil {
		h.logger.Error("failed to get receipt gallery", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get gallery"})
		return
	}

	type galleryItem struct {
		ID        string  `json:"id"`
		Merchant  string  `json:"merchant"`
		Amount    string  `json:"amount"`
		Category  string  `json:"category"`
		Date      *string `json:"date,omitempty"`
		Thumbnail string  `json:"thumbnail"`
	}

	items := make([]galleryItem, 0, len(scans))
	for _, s := range scans {
		item := galleryItem{
			ID:       s.ID.String(),
			Merchant: s.Merchant,
			Amount:   s.Amount.StringFixed(2),
			Category: s.Category,
		}
		if s.ReceiptDate != nil {
			d := s.ReceiptDate.Format("2006-01-02")
			item.Date = &d
		}
		if s.Thumbnail != nil {
			item.Thumbnail = *s.Thumbnail
		}
		items = append(items, item)
	}

	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"gallery":  items,
		"limit":    limit,
		"offset":   offset,
		"category": category,
	}})
}
