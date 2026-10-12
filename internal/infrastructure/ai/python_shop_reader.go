package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/pkg/auth"
	"go.uber.org/zap"
)

// ShopDocumentRead is one page handed to the Python reader.
type ShopDocumentRead struct {
	// FileB64 is the attachment exactly as it arrived. The reader owns
	// preprocessing (EXIF, HEIC, downscale, PDF rasterization).
	FileB64 string
	MIME    string
	// MessageID lets the reader treat a redelivered message as one document.
	MessageID string
	// DocHint is Go's own classification. The reader uses it only to break a
	// tie in its own routing; empty means Go has no opinion.
	DocHint string
	// Caption is what the owner typed with the page.
	Caption string
}

// ShopDocumentAnswer is the reader's reply. ReplyText is shown to the owner
// verbatim: the reader has already checked every figure against the page, so
// nothing here may be rephrased or recomputed downstream.
type ShopDocumentAnswer struct {
	ReplyText  string   `json:"reply_text"`
	DocID      string   `json:"doc_id"`
	DocType    string   `json:"doc_type"`
	Flags      []string `json:"flags"`
	Confidence float64  `json:"confidence"`
	ModelPath  []string `json:"model_path"`
}

// Shop read error copy stays short and free of document content: these strings
// reach the logs, and the page itself must not.
const maxShopReadErrorBody = 200

// ReadShopDocument reads one shop document — a trader's stock book, a
// distributor invoice, or a delivery note — for a linked owner.
//
// It runs on its own HTTP client because a vision read over a whole page takes
// tens of seconds, well past the chat timeout. The token is the same
// short-lived per-user agent JWT the chat path mints, and the body carries the
// same user id: the reader rejects a body that names a different owner than the
// token's subject, so this token can only ever read (and store) its own user's
// pages.
func (c *PythonAgentClient) ReadShopDocument(ctx context.Context, userID uuid.UUID, email, role string, read ShopDocumentRead) (*ShopDocumentAnswer, error) {
	if strings.TrimSpace(c.baseURL) == "" {
		return nil, fmt.Errorf("shop read: python reader base url is not configured")
	}
	if userID == uuid.Nil {
		return nil, fmt.Errorf("shop read: no owner for the document")
	}
	if strings.TrimSpace(read.FileB64) == "" {
		return nil, fmt.Errorf("shop read: empty attachment")
	}

	token, _, err := auth.GenerateAgentToken(userID, email, "", role, c.jwtSecret, int(c.jwtTTL.Seconds()))
	if err != nil {
		return nil, fmt.Errorf("mint shop read jwt: %w", err)
	}

	payload, err := json.Marshal(map[string]string{
		"file_b64":   read.FileB64,
		"mime":       read.MIME,
		"user_id":    userID.String(),
		"message_id": read.MessageID,
		"doc_hint":   read.DocHint,
		"caption":    read.Caption,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal shop read request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/shop/documents/read", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create shop read request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.shopHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("shop read request: %w", err)
	}
	defer func() {
		if _, drainErr := io.Copy(io.Discard, resp.Body); drainErr != nil {
			c.logger.Warn("failed to drain shop read response body", zap.Error(drainErr))
		}
		if closeErr := resp.Body.Close(); closeErr != nil {
			c.logger.Warn("failed to close shop read response body", zap.Error(closeErr))
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxShopReadErrorBody))
		c.logger.Warn("shop read returned non-200",
			zap.Int("status", resp.StatusCode),
			zap.String("body", string(body)),
			zap.String("user_id", userID.String()),
		)
		return nil, fmt.Errorf("shop read: unexpected status %d", resp.StatusCode)
	}

	var out ShopDocumentAnswer
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode shop read response: %w", err)
	}
	return &out, nil
}
