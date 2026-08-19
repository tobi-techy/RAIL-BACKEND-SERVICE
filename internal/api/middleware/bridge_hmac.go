package middleware

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// BridgeHMAC verifies HMAC-SHA256 signatures on requests from the Spectrum bridge.
// The bridge signs the raw body with RAIL_HMAC_SECRET and sends it as X-HMAC-SHA256.
func BridgeHMAC(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if secret == "" {
			c.Next()
			return
		}

		sigHeader := c.GetHeader("X-HMAC-SHA256")
		if sigHeader == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing signature"})
			return
		}

		body, err := io.ReadAll(io.LimitReader(c.Request.Body, 5*1024*1024))
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(body))

		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		expected := hex.EncodeToString(mac.Sum(nil))

		sig := strings.TrimPrefix(sigHeader, "hmac-sha256=")
		if !hmac.Equal([]byte(expected), []byte(sig)) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
			return
		}

		c.Next()
	}
}
