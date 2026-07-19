package document

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

	"go.uber.org/zap"
)

// PythonOCRClient calls the PaddleOCR sidecar over HTTP.
type PythonOCRClient struct {
	baseURL    string
	httpClient *http.Client
	logger     *zap.Logger
}

// NewPythonOCRClient builds a client for the OCR sidecar. Returns nil if no URL.
func NewPythonOCRClient(baseURL string, logger *zap.Logger) *PythonOCRClient {
	if baseURL == "" {
		return nil
	}
	return &PythonOCRClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{Timeout: 120 * time.Second},
		logger:     logger,
	}
}

type ocrRequest struct {
	FileB64  string `json:"file_b64"`
	MimeType string `json:"mime_type"`
	DocHint  string `json:"doc_hint"`
}

type ocrResponse struct {
	Text           string  `json:"text"`
	PageCount      int     `json:"page_count"`
	MeanConfidence float64 `json:"mean_confidence"`
	Lines          []struct {
		Text       string      `json:"text"`
		BBox       [][]float64 `json:"bbox"`
		Confidence float64     `json:"confidence"`
		Page       int         `json:"page"`
	} `json:"lines"`
}

// Recognize implements OCREngine by calling the Python sidecar.
func (c *PythonOCRClient) Recognize(ctx context.Context, data []byte, mimeType string) (*OCRResult, error) {
	reqBody := ocrRequest{
		FileB64:  base64.StdEncoding.EncodeToString(data),
		MimeType: mimeType,
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal ocr request: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, c.baseURL+"/ocr", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create ocr request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ocr call: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ocr service error %d: %s", resp.StatusCode, string(body))
	}

	var parsed ocrResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse ocr response: %w", err)
	}

	lines := make([]Line, 0, len(parsed.Lines))
	for _, l := range parsed.Lines {
		lines = append(lines, Line{Text: l.Text, BBox: l.BBox, Confidence: l.Confidence, Page: l.Page})
	}

	return &OCRResult{
		Text:           parsed.Text,
		PageCount:      parsed.PageCount,
		MeanConfidence: parsed.MeanConfidence,
		Lines:          lines,
		Engine:         "paddleocr",
	}, nil
}
