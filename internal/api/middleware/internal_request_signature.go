package middleware

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// internalSignatureMaxSkewSeconds bounds how far a signed internal request's
// timestamp may drift from server time. Together with the signature this gives
// a short replay window without requiring a shared nonce store.
const internalSignatureMaxSkewSeconds = 300

// internalSignatureMaxBody caps the body we will read for signing to avoid
// unbounded memory use on internal endpoints.
const internalSignatureMaxBody = 5 * 1024 * 1024

// InternalRequestSignature enforces HMAC-SHA256 request signing on internal
// endpoints as defense-in-depth on top of the static internal API key
// (TM-001). A leaked or guessed API key alone is no longer sufficient to call
// money-moving internal routes: the caller must also possess the signing
// secret and produce a fresh, request-bound signature.
//
// When signingSecret is empty the middleware is a no-op so existing internal
// callers (e.g. the Cloudflare cron that triggers /internal/miriam/evaluate)
// keep working until request signing is rolled out to those callers.
//
// When configured, callers must send:
//
//	X-Internal-Timestamp: unix seconds (must be within ±300s of server time)
//	X-Internal-Signature: hex(HMAC_SHA256(secret, timestamp + "." + METHOD + "." + path + "." + body))
func InternalRequestSignature(signingSecret string, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		if signingSecret == "" {
			c.Next()
			return
		}

		tsHeader := c.GetHeader("X-Internal-Timestamp")
		sigHeader := c.GetHeader("X-Internal-Signature")
		if tsHeader == "" || sigHeader == "" {
			abortInternalSignature(c, logger, "missing signature headers")
			return
		}

		ts, err := strconv.ParseInt(tsHeader, 10, 64)
		if err != nil {
			abortInternalSignature(c, logger, "invalid timestamp")
			return
		}
		if skew := time.Now().Unix() - ts; skew > internalSignatureMaxSkewSeconds || skew < -internalSignatureMaxSkewSeconds {
			abortInternalSignature(c, logger, "timestamp out of range")
			return
		}

		// Read and restore the body so downstream handlers still see it.
		var body []byte
		if c.Request.Body != nil {
			body, err = io.ReadAll(io.LimitReader(c.Request.Body, internalSignatureMaxBody))
			if err != nil {
				abortInternalSignature(c, logger, "unable to read body")
				return
			}
			c.Request.Body = io.NopCloser(bytes.NewReader(body))
		}

		payload := fmt.Sprintf("%s.%s.%s.%s", tsHeader, c.Request.Method, c.Request.URL.Path, string(body))
		mac := hmac.New(sha256.New, []byte(signingSecret))
		mac.Write([]byte(payload))
		expected := hex.EncodeToString(mac.Sum(nil))

		if subtle.ConstantTimeCompare([]byte(sigHeader), []byte(expected)) != 1 {
			abortInternalSignature(c, logger, "signature mismatch")
			return
		}
		c.Next()
	}
}

func abortInternalSignature(c *gin.Context, logger *zap.Logger, reason string) {
	if logger != nil {
		logger.Warn("internal request signature rejected",
			zap.String("reason", reason),
			zap.String("path", c.Request.URL.Path),
			zap.String("client_ip", c.ClientIP()),
		)
	}
	c.JSON(http.StatusUnauthorized, gin.H{"error": "internal_signature_invalid"})
	c.Abort()
}
