package idempotency

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"go.uber.org/zap"
)

const (
	// HeaderIdempotencyKey is the HTTP header for idempotency key
	HeaderIdempotencyKey = "Idempotency-Key"
	
	// MaxBodySize is the maximum request body size for idempotency (10MB)
	MaxBodySize = 10 << 20
)

// responseWriter wraps gin.ResponseWriter to capture response
type responseWriter struct {
	gin.ResponseWriter
	body   *bytes.Buffer
	status int
}

func (w *responseWriter) Write(b []byte) (int, error) {
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

func (w *responseWriter) WriteHeader(statusCode int) {
	w.status = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

// Middleware creates an idempotency middleware
func Middleware(repo *repositories.IdempotencyRepository, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Only apply to state-changing methods
		if c.Request.Method != http.MethodPost && 
		   c.Request.Method != http.MethodPut && 
		   c.Request.Method != http.MethodDelete &&
		   c.Request.Method != http.MethodPatch {
			c.Next()
			return
		}

		// Check for idempotency key header
		idempotencyKey := c.GetHeader(HeaderIdempotencyKey)
		if idempotencyKey == "" {
			// Idempotency is optional; if not provided, proceed normally
			c.Next()
			return
		}

		// Validate idempotency key
		if err := ValidateKey(idempotencyKey); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":      "Invalid idempotency key",
				"message":    err.Error(),
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		// Read request body
		bodyBytes, err := ReadBody(c.Request.Body, MaxBodySize)
		if err != nil {
			logger.Error("Failed to read request body",
				zap.String("idempotency_key", idempotencyKey),
				zap.Error(err))
			c.JSON(http.StatusBadRequest, gin.H{
				"error":      "Failed to read request body",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		// Restore body for downstream handlers
		c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		// Calculate request hash
		requestHash := HashRequest(bodyBytes)

		// Check if idempotency key exists
		existing, err := repo.Get(c.Request.Context(), idempotencyKey)
		if err != nil {
			logger.Error("Failed to check idempotency key",
				zap.String("idempotency_key", idempotencyKey),
				zap.Error(err))
			// On error, proceed with request (fail open)
			c.Next()
			return
		}

		// If key exists, validate and return cached response
		if existing != nil {
			shouldCache, reason := ShouldReturnCached(
				&Response{Status: existing.ResponseStatus, Body: existing.ResponseBody},
				requestHash,
				existing.RequestHash,
			)

			if !shouldCache {
				logger.Warn("Idempotency key conflict",
					zap.String("idempotency_key", idempotencyKey),
					zap.String("reason", reason))
				c.JSON(http.StatusConflict, gin.H{
					"error":      "Idempotency key conflict",
					"message":    reason,
					"request_id": c.GetString("request_id"),
				})
				c.Abort()
				return
			}

			// Return cached response
			logger.Info("Returning cached response",
				zap.String("idempotency_key", idempotencyKey),
				zap.Int("status", existing.ResponseStatus))

			var responseBody interface{}
			if err := json.Unmarshal(existing.ResponseBody, &responseBody); err == nil {
				c.JSON(existing.ResponseStatus, responseBody)
			} else {
				c.Data(existing.ResponseStatus, "application/json", existing.ResponseBody)
			}
			c.Abort()
			return
		}

		// Capture response
		writer := &responseWriter{
			ResponseWriter: c.Writer,
			body:           bytes.NewBuffer(nil),
			status:         http.StatusOK,
		}
		c.Writer = writer

		// Process request
		c.Next()

		// Store response for future requests
		var userID *uuid.UUID
		if userIDStr, exists := c.Get("user_id"); exists {
			if uid, ok := userIDStr.(uuid.UUID); ok {
				userID = &uid
			}
		}

		expiresAt := time.Now().Add(DefaultTTL)
		record := &repositories.IdempotencyKey{
			IdempotencyKey: idempotencyKey,
			RequestPath:    c.Request.URL.Path,
			RequestMethod:  c.Request.Method,
			RequestHash:    requestHash,
			UserID:         userID,
			ResponseStatus: writer.status,
			ResponseBody:   writer.body.Bytes(),
			ExpiresAt:      expiresAt,
		}

		if err := repo.Create(c.Request.Context(), record); err != nil {
			// Log error but don't fail the request
			logger.Error("Failed to store idempotency key",
				zap.String("idempotency_key", idempotencyKey),
				zap.Error(err))
		} else {
			logger.Info("Stored idempotency key",
				zap.String("idempotency_key", idempotencyKey),
				zap.Int("status", writer.status))
		}
	}
}

// RequireIdempotency creates middleware that requires idempotency key
func RequireIdempotency() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Only apply to state-changing methods
		if c.Request.Method != http.MethodPost && 
		   c.Request.Method != http.MethodPut && 
		   c.Request.Method != http.MethodDelete &&
		   c.Request.Method != http.MethodPatch {
			c.Next()
			return
		}

		idempotencyKey := c.GetHeader(HeaderIdempotencyKey)
		if idempotencyKey == "" {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":      "Idempotency key required",
				"message":    "This endpoint requires an Idempotency-Key header",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		c.Next()
	}
}
