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
// timestamp may drift from server time. The skew is symmetric (±skew), so the
// effective replay window is 2× this value.
//
// SECURITY: This middleware does NOT prevent replay attacks within the window.
// Any captured signed request can be replayed verbatim until its timestamp
// falls outside ±internalSignatureMaxSkewSeconds. Endpoints that must be
// strictly once-only (anything money-moving) MUST enforce application-layer
// idempotency / deduplication (e.g. server-side idempotency keys, a request
// nonce store, or single-use tokens) on top of this middleware.
const internalSignatureMaxSkewSeconds = 300

// internalSignatureMaxBody caps the body we will read for signing to avoid
// unbounded memory use on internal endpoints. Requests with a body strictly
// larger than this are rejected — we never truncate, because a truncated read
// would let an attacker append unsigned data past the cap.
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
//	X-Internal-Signature: hex(HMAC_SHA256(secret, timestamp + "." + METHOD + "." + path + "." + rawQuery + "." + body))
//
// Empty query strings are represented as "-" in the signature payload to avoid
// ambiguity from double dots.
//
// The raw query string is included so an attacker who captures a signed
// request cannot mutate ?param=… values without invalidating the signature.
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
		// Read one byte past the cap so we can distinguish "exactly at cap" from
		// "exceeds cap" — if we hit cap+1, the request is oversized and must be
		// rejected (otherwise an attacker can append unsigned bytes past the cap).
		var body []byte
		if c.Request.Body != nil {
			body, err = io.ReadAll(io.LimitReader(c.Request.Body, internalSignatureMaxBody+1))
			if err != nil {
				abortInternalSignature(c, logger, "unable to read body")
				return
			}
			if len(body) > internalSignatureMaxBody {
				abortInternalSignature(c, logger, "body exceeds maximum size")
				return
			}
			c.Request.Body = io.NopCloser(bytes.NewReader(body))
		}

		query := c.Request.URL.RawQuery
		if query == "" {
			query = "-"
		}
		payload := fmt.Sprintf("%s.%s.%s.%s.%s",
			tsHeader, c.Request.Method, c.Request.URL.Path, query, string(body))
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
