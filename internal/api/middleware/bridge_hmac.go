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
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const (
	// bridgeHMACMaxSkewSeconds bounds how far a signed bridge request's timestamp
	// may drift from server time. The effective replay window is 2× this value.
	bridgeHMACMaxSkewSeconds = 300

	// MaxInboundBodyBytes caps the signed inbound body from the bridge. The
	// bridge carries photos and PDFs up to 12 MiB; base64 expands a 12 MiB page
	// to 16 MiB and the JSON envelope adds a little more, so this clears the
	// largest page the bridge will send. The page's own size is bounded further
	// down the shop path (maxShopReadBase64Bytes); this cap only decides whether
	// the page reaches the processor at all.
	MaxInboundBodyBytes = 20 << 20

	// bridgeHMACMaxBody is the body the signature is computed over.
	bridgeHMACMaxBody = MaxInboundBodyBytes

	// maxNoncesInMemory caps the number of recent nonces tracked for replay
	// protection. Emergency eviction removes oldest entries when exceeded.
	maxNoncesInMemory = 10000
)

// nonceStore tracks recent nonces for replay protection. It is per-process, so
// a determined attacker could still replay across replicas; money-moving routes
// must also enforce application-layer idempotency.
type nonceStore struct {
	mu      sync.RWMutex
	nonces  map[string]time.Time
	maxAge  time.Duration
	maxSize int
}

type nonceEntry struct {
	nonce   string
	expires time.Time
}

var bridgeNonceStore = &nonceStore{
	nonces:  make(map[string]time.Time),
	maxAge:  bridgeHMACMaxSkewSeconds * 2 * time.Second,
	maxSize: maxNoncesInMemory,
}

func (s *nonceStore) isUnique(nonce string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, seen := s.nonces[nonce]; seen {
		return false
	}

	if len(s.nonces) >= s.maxSize {
		s.evictOldestLocked()
	}

	s.nonces[nonce] = time.Now().Add(s.maxAge)
	return true
}

// evictOldestLocked removes oldest nonces by expiration time, keeping ~80% of
// maxSize. Caller must hold s.mu.
func (s *nonceStore) evictOldestLocked() {
	entries := make([]nonceEntry, 0, len(s.nonces))
	for nonce, expires := range s.nonces {
		entries = append(entries, nonceEntry{nonce: nonce, expires: expires})
	}
	sortEntriesByTime(entries)

	keepCount := int(float64(s.maxSize) * 0.8)
	if keepCount < 1 {
		keepCount = 1
	}
	dropCount := len(entries) - keepCount
	if dropCount <= 0 {
		return
	}
	for i := 0; i < dropCount; i++ {
		delete(s.nonces, entries[i].nonce)
	}
}

func sortEntriesByTime(entries []nonceEntry) {
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].expires.Before(entries[j].expires)
	})
}

func (s *nonceStore) evict(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for nonce, expires := range s.nonces {
		if expires.Before(now) {
			delete(s.nonces, nonce)
		}
	}
}

func init() {
	// Periodically evict expired nonces.
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			bridgeNonceStore.evict(time.Now())
		}
	}()
}

// BridgeHMAC verifies HMAC-SHA256 signatures on requests from the Spectrum bridge.
// The bridge signs timestamp.nonce.body with RAIL_HMAC_SECRET and sends it as
// X-HMAC-SHA256, along with X-HMAC-Timestamp and X-HMAC-Nonce for replay protection.
//
// When the secret is empty the middleware fails closed (503) rather than
// bypassing authentication.
func BridgeHMAC(secret string, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		if secret == "" {
			if logger != nil {
				logger.Error("bridge HMAC secret not configured, rejecting request")
			}
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "bridge HMAC not configured"})
			return
		}

		timestampHeader := c.GetHeader("X-HMAC-Timestamp")
		nonceHeader := c.GetHeader("X-HMAC-Nonce")
		sigHeader := c.GetHeader("X-HMAC-SHA256")
		if timestampHeader == "" || nonceHeader == "" || sigHeader == "" {
			abortBridgeHMAC(c, logger, "missing signature headers")
			return
		}

		ts, err := strconv.ParseInt(timestampHeader, 10, 64)
		if err != nil {
			abortBridgeHMAC(c, logger, "invalid timestamp")
			return
		}
		if skew := time.Now().Unix() - ts; skew > int64(bridgeHMACMaxSkewSeconds) || skew < -int64(bridgeHMACMaxSkewSeconds) {
			abortBridgeHMAC(c, logger, "timestamp out of range")
			return
		}

		body, err := io.ReadAll(io.LimitReader(c.Request.Body, bridgeHMACMaxBody+1))
		if err != nil {
			abortBridgeHMAC(c, logger, "failed to read body")
			return
		}
		if len(body) > bridgeHMACMaxBody {
			// 413, not the 401 the other rejections use: a page the bridge was
			// told it could carry must not look like a bad signature, or the
			// size problem is hunted in the wrong place.
			if logger != nil {
				logger.Warn("bridge HMAC rejected",
					zap.String("reason", "body exceeds maximum size"),
					zap.Int("body_bytes", len(body)),
					zap.Int("max_bytes", bridgeHMACMaxBody),
					zap.String("path", c.Request.URL.Path),
				)
			}
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"error": "body exceeds maximum size"})
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(body))

		payload := fmt.Sprintf("%s.%s.%s", timestampHeader, nonceHeader, string(body))
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(payload))
		expected := hex.EncodeToString(mac.Sum(nil))

		if subtle.ConstantTimeCompare([]byte(expected), []byte(sigHeader)) != 1 {
			abortBridgeHMAC(c, logger, "signature mismatch")
			return
		}

		// Replay rejection runs only after the HMAC signature is verified so
		// that unauthenticated requests cannot exhaust the nonce store.
		if !bridgeNonceStore.isUnique(nonceHeader) {
			abortBridgeHMAC(c, logger, "nonce reused")
			return
		}

		c.Next()
	}
}

func abortBridgeHMAC(c *gin.Context, logger *zap.Logger, reason string) {
	if logger != nil {
		logger.Warn("bridge HMAC rejected",
			zap.String("reason", reason),
			zap.String("path", c.Request.URL.Path),
			zap.String("client_ip", c.ClientIP()),
		)
	}
	c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or missing HMAC signature"})
}
