package investing

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	knowledgesvc "github.com/rail-service/rail_service/internal/domain/services/knowledge"
	"go.uber.org/zap"
)

// KnowledgeHandlers handles knowledge base admin endpoints.
type KnowledgeHandlers struct {
	service *knowledgesvc.Service
	logger  *zap.Logger
}

// NewKnowledgeHandlers creates new knowledge handlers.
func NewKnowledgeHandlers(service *knowledgesvc.Service, logger *zap.Logger) *KnowledgeHandlers {
	return &KnowledgeHandlers{service: service, logger: logger}
}

// Ingest handles POST /api/v1/admin/knowledge/ingest
// Accepts multipart form with "source" (document name) and "file" (text content).
func (h *KnowledgeHandlers) Ingest(c *gin.Context) {
	source := c.PostForm("source")
	if source == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "source is required"})
		return
	}

	file, _, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file is required"})
		return
	}
	defer file.Close()

	content, err := io.ReadAll(io.LimitReader(file, 5*1024*1024)) // 5MB max
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read file"})
		return
	}

	chunks, err := h.service.Ingest(c.Request.Context(), source, string(content))
	if err != nil {
		h.logger.Error("knowledge ingest failed", zap.Error(err), zap.String("source", source))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "ingestion failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"source": source,
			"chunks": chunks,
		},
	})
}
