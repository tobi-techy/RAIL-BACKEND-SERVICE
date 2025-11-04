package middleware

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/stack-service/stack_service/internal/infrastructure/config"
	"github.com/stack-service/stack_service/pkg/logger"

	"github.com/gin-gonic/gin"
)

// RequestSigning validates HMAC signatures for critical endpoints
func RequestSigning(cfg *config.Config, log *logger.Logger, required bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !required {
			c.Next()
			return
		}

		timestamp := c.GetHeader("X-Timestamp")
		signature := c.GetHeader("X-Signature")
		apiKey := c.GetHeader("X-API-Key")

		if timestamp == "" || signature == "" || apiKey == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":      "Missing required headers for request signing",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		// Validate timestamp (prevent replay attacks - 5 minute window)
		timestampInt, err := strconv.ParseInt(timestamp, 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":      "Invalid timestamp format",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		now := time.Now().Unix()
		if abs(now-timestampInt) > 300 { // 5 minutes
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":      "Request timestamp expired",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		// Validate API key and get secret
		apiKeySecret, exists := cfg.Security.APIKeys[apiKey]
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":      "Invalid API key",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		// Validate signature
		body, _ := c.GetRawData()
		payload := fmt.Sprintf("%s.%s.%s", c.Request.Method, c.Request.URL.Path, string(body))

		expectedSignature := generateHMAC(payload, apiKeySecret)
		if !hmac.Equal([]byte(signature), []byte(expectedSignature)) {
			log.Warnw("Invalid request signature",
				"request_id", c.GetString("request_id"),
				"api_key", apiKey,
				"expected", expectedSignature,
				"received", signature,
			)
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":      "Invalid request signature",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		// Restore body for further processing
		c.Request.Body = io.NopCloser(bytes.NewBuffer(body))

		c.Set("api_key", apiKey)
		c.Next()
	}
}

// generateHMAC generates HMAC-SHA256 signature
func generateHMAC(message, secret string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(message))
	return hex.EncodeToString(h.Sum(nil))
}

// abs returns absolute value of int64
func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// IPWhitelist restricts access to specific IP addresses for sensitive operations
func IPWhitelist(allowedIPs []string, log *logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		clientIP := c.ClientIP()

		// Check if IP is in whitelist
		allowed := false
		for _, allowedIP := range allowedIPs {
			if allowedIP == clientIP || allowedIP == "*" {
				allowed = true
				break
			}
			// Support CIDR notation (basic check)
			if strings.Contains(allowedIP, "/") && strings.HasPrefix(clientIP, strings.Split(allowedIP, "/")[0]) {
				allowed = true
				break
			}
		}

		if !allowed {
			log.Warnw("Access denied from IP",
				"request_id", c.GetString("request_id"),
				"client_ip", clientIP,
				"path", c.Request.URL.Path,
			)
			c.JSON(http.StatusForbidden, gin.H{
				"error":      "Access denied from this IP address",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// RoleBasedAccessControl implements RBAC beyond basic user/admin
func RoleBasedAccessControl(requiredRoles []string, log *logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		userRole := c.GetString("user_role")

		// Check if user has required role
		hasRole := false
		for _, role := range requiredRoles {
			if userRole == role {
				hasRole = true
				break
			}
		}

		if !hasRole {
			log.Warnw("Insufficient permissions",
				"request_id", c.GetString("request_id"),
				"user_id", c.GetString("user_id"),
				"user_role", userRole,
				"required_roles", requiredRoles,
				"path", c.Request.URL.Path,
			)
			c.JSON(http.StatusForbidden, gin.H{
				"error":      "Insufficient permissions",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		c.Next()
	}
}
