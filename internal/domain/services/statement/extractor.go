package statement

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"go.uber.org/zap"
)

// ExtractionStrategy defines a named approach to extracting text from documents.
type ExtractionStrategy string

const (
	StrategyPDFCPU    ExtractionStrategy = "pdfcpu"
	StrategyTextract  ExtractionStrategy = "textract"
	StrategyVisionLLM ExtractionStrategy = "vision_llm"
)

// ExtractionResult holds the output of any extraction strategy.
type ExtractionResult struct {
	Text      string
	Pages     []string
	PageCount int
	Strategy  ExtractionStrategy
	IsScanned bool
}

// DocumentExtractor cascades through strategies to extract text from PDFs and images.
type DocumentExtractor struct {
	textractClient TextractClient
	visionClient   VisionClient
	logger         *zap.Logger
}

// TextractClient abstracts AWS Textract calls.
type TextractClient interface {
	ExtractText(ctx context.Context, data []byte, contentType string) (string, error)
}

// VisionClient abstracts vision-capable LLM calls (GPT-4o, Claude, etc).
type VisionClient interface {
	ExtractTextFromImage(ctx context.Context, data []byte, contentType string) (string, error)
}

func NewDocumentExtractor(textract TextractClient, vision VisionClient, logger *zap.Logger) *DocumentExtractor {
	return &DocumentExtractor{textractClient: textract, visionClient: vision, logger: logger}
}

// Extract attempts to get text from a document using a cascade of strategies.
// For PDFs: pdfcpu → Textract → Vision LLM
// For images: Textract → Vision LLM
func (e *DocumentExtractor) Extract(ctx context.Context, data []byte, contentType string) (*ExtractionResult, error) {
	isImage := strings.HasPrefix(contentType, "image/")
	isPDF := contentType == "application/pdf" || (!isImage && len(data) > 5 && string(data[:5]) == "%PDF-")

	if isPDF {
		return e.extractPDF(ctx, data)
	}
	if isImage {
		return e.extractImage(ctx, data, contentType)
	}
	return nil, fmt.Errorf("unsupported content type: %s", contentType)
}

func (e *DocumentExtractor) extractPDF(ctx context.Context, data []byte) (*ExtractionResult, error) {
	// Strategy 1: pdfcpu (fast, free, works for digital PDFs)
	result, err := ExtractTextFromBytes(data)
	if err == nil && !result.IsScanned && len(strings.TrimSpace(result.Text)) > 50 {
		// Reject if pdfcpu extracted raw content stream noise (CID fonts, hex strings)
		if IsPDFContentNoise(result.Text) {
			e.logger.Info("pdfcpu extracted content stream noise, falling back",
				zap.Int("chars", len(result.Text)),
				zap.Int("lines", len(strings.Split(result.Text, "\n"))),
			)
		} else {
			// If we have substantial text (>1000 chars), use it even if some "garbage" characters
			// are present — the LLM can handle noise. Only reject if short AND garbage-heavy.
			isGarbage := IsGarbageText(result.Text)
			if !isGarbage || len(result.Text) > 1000 {
				if isGarbage {
					e.logger.Info("pdfcpu text has noise but is substantial, proceeding", zap.Int("chars", len(result.Text)))
				}
				return &ExtractionResult{
					Text:      result.Text,
					Pages:     result.Pages,
					PageCount: result.PageCount,
					Strategy:  StrategyPDFCPU,
				}, nil
			}
		}
	}

	// Strategy 2: AWS Textract (handles scans, custom fonts, encrypted text layers)
	if e.textractClient != nil {
		e.logger.Info("falling back to Textract",
			zap.Bool("scanned", result != nil && result.IsScanned),
			zap.Bool("garbage", result != nil && IsGarbageText(result.Text)),
		)
		text, err := e.textractClient.ExtractText(ctx, data, "application/pdf")
		if err == nil && len(strings.TrimSpace(text)) > 50 {
			pageCount := 1
			if result != nil {
				pageCount = result.PageCount
			}
			pages := strings.Split(text, "\n---PAGE BREAK---\n")
			return &ExtractionResult{
				Text:      text,
				Pages:     pages,
				PageCount: pageCount,
				Strategy:  StrategyTextract,
			}, nil
		}
		if err != nil {
			e.logger.Warn("textract sync extraction failed", zap.Error(err))
		}

		// Textract sync API doesn't support multi-page PDFs. Split into single pages
		// and retry per page.
		pageCount := 1
		if result != nil {
			pageCount = result.PageCount
		}
		if pageCount > 1 {
			e.logger.Info("splitting multi-page PDF for per-page Textract", zap.Int("pages", pageCount))
			pagesText, pErr := e.extractTextractByPage(ctx, data, pageCount)
			if pErr == nil && len(strings.TrimSpace(pagesText)) > 50 {
				pages := strings.Split(pagesText, "\n---PAGE BREAK---\n")
				return &ExtractionResult{
					Text:      pagesText,
					Pages:     pages,
					PageCount: pageCount,
					Strategy:  StrategyTextract,
				}, nil
			}
			if pErr != nil {
				e.logger.Warn("textract per-page extraction also failed", zap.Error(pErr))
			}
		}
	}

	// Strategy 3: Vision LLM (last resort — only works for images, not raw PDFs)
	// For PDFs, vision LLM is skipped since it requires image input
	if e.visionClient != nil && false {
		// Disabled for PDFs — OpenAI Vision API only accepts images.
		// To enable: convert PDF pages to images first (not implemented).
		e.logger.Info("vision LLM skipped for PDF (requires image conversion)")
	}

	// All strategies exhausted
	if result != nil && result.IsScanned {
		return nil, fmt.Errorf("scanned PDF and no OCR service available")
	}
	return nil, fmt.Errorf("all extraction strategies failed")
}

// extractTextractByPage splits a multi-page PDF into single pages and calls
// Textract's sync DetectDocumentText on each page (which only supports 1 page per call).
func (e *DocumentExtractor) extractTextractByPage(ctx context.Context, data []byte, pageCount int) (string, error) {
	conf := model.NewDefaultConfiguration()
	conf.ValidationMode = model.ValidationRelaxed

	spans, err := api.SplitRaw(bytes.NewReader(data), 1, conf)
	if err != nil {
		return "", fmt.Errorf("split pdf: %w", err)
	}

	var pageTexts []string
	for i, span := range spans {
		if span.Reader == nil {
			continue
		}
		pageData, rErr := io.ReadAll(span.Reader)
		if rErr != nil {
			e.logger.Warn("failed to read split page", zap.Int("page", i+1), zap.Error(rErr))
			continue
		}
		text, tErr := e.textractClient.ExtractText(ctx, pageData, "application/pdf")
		if tErr != nil {
			e.logger.Warn("textract failed on split page", zap.Int("page", i+1), zap.Error(tErr))
			continue
		}
		pageTexts = append(pageTexts, text)
	}

	if len(pageTexts) == 0 {
		return "", fmt.Errorf("textract per-page: no pages extracted")
	}

	return strings.Join(pageTexts, "\n---PAGE BREAK---\n"), nil
}

func (e *DocumentExtractor) extractImage(ctx context.Context, data []byte, contentType string) (*ExtractionResult, error) {
	// Strategy 1: Textract (best for images)
	if e.textractClient != nil {
		text, err := e.textractClient.ExtractText(ctx, data, contentType)
		if err == nil && len(strings.TrimSpace(text)) > 20 {
			return &ExtractionResult{
				Text:      text,
				Pages:     []string{text},
				PageCount: 1,
				Strategy:  StrategyTextract,
			}, nil
		}
		if err != nil {
			e.logger.Warn("textract image extraction failed", zap.Error(err))
		}
	}

	// Strategy 2: Vision LLM
	if e.visionClient != nil {
		text, err := e.visionClient.ExtractTextFromImage(ctx, data, contentType)
		if err == nil && len(strings.TrimSpace(text)) > 20 {
			return &ExtractionResult{
				Text:      text,
				Pages:     []string{text},
				PageCount: 1,
				Strategy:  StrategyVisionLLM,
			}, nil
		}
		if err != nil {
			e.logger.Warn("vision LLM image extraction failed", zap.Error(err))
		}
	}

	return nil, fmt.Errorf("no OCR service available for image processing")
}

// --- OpenAI Vision Implementation ---

type OpenAIVisionClient struct {
	apiKey  string
	baseURL string
	model   string
	logger  *zap.Logger
}

func NewOpenAIVisionClient(apiKey string, logger *zap.Logger) *OpenAIVisionClient {
	if apiKey == "" {
		return nil
	}
	return &OpenAIVisionClient{apiKey: apiKey, baseURL: "https://api.openai.com/v1", model: "gpt-4o-mini", logger: logger}
}

func NewOpenAIVisionClientWithConfig(apiKey, baseURL, model string, logger *zap.Logger) *OpenAIVisionClient {
	if apiKey == "" {
		return nil
	}
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	if model == "" {
		model = "gpt-4o-mini"
	}
	return &OpenAIVisionClient{apiKey: apiKey, baseURL: baseURL, model: model, logger: logger}
}

func (c *OpenAIVisionClient) ExtractTextFromImage(ctx context.Context, data []byte, contentType string) (string, error) {
	if contentType == "" {
		contentType = "image/png"
	}
	b64 := base64.StdEncoding.EncodeToString(data)
	dataURL := fmt.Sprintf("data:%s;base64,%s", contentType, b64)

	body := map[string]interface{}{
		"model":      c.model,
		"max_tokens": 8000,
		"messages": []map[string]interface{}{
			{
				"role": "user",
				"content": []map[string]interface{}{
					{"type": "text", "text": "Extract ALL text from this bank statement image exactly as it appears. Preserve the tabular structure (dates, descriptions, amounts, balances). Output plain text only, no markdown."},
					{"type": "image_url", "image_url": map[string]string{"url": dataURL, "detail": "high"}},
				},
			},
		},
	}

	jsonBody, _ := json.Marshal(body)
	reqCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, "POST", strings.TrimRight(c.baseURL, "/")+"/chat/completions", bytes.NewReader(jsonBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("vision API call: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
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
		return "", err
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no choices in vision response")
	}
	return result.Choices[0].Message.Content, nil
}
