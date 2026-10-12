package middleware

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// signBridge builds the signature the Spectrum bridge sends: HMAC-SHA256 over
// "timestamp.nonce.body", hex, in X-HMAC-SHA256.
func signBridge(secret, ts, nonce string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(fmt.Sprintf("%s.%s.", ts, nonce)))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// bridgeRouter is the inbound route with its real HMAC middleware, and a handler
// that reports exactly how many bytes it was handed.
func bridgeRouter(secret string) (*gin.Engine, *int) {
	gin.SetMode(gin.TestMode)
	seen := 0
	router := gin.New()
	router.Use(BridgeHMAC(secret, nil))
	router.POST("/api/v1/platform/inbound", func(c *gin.Context) {
		body, _ := io.ReadAll(c.Request.Body)
		seen = len(body)
		c.Status(http.StatusOK)
	})
	return router, &seen
}

func postSigned(router *gin.Engine, secret string, body []byte) *httptest.ResponseRecorder {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := fmt.Sprintf("nonce-%d-%d", time.Now().UnixNano(), len(body))
	request := httptest.NewRequest(
		http.MethodPost, "/api/v1/platform/inbound", bytes.NewReader(body),
	)
	request.Header.Set("X-HMAC-Timestamp", ts)
	request.Header.Set("X-HMAC-Nonce", nonce)
	request.Header.Set("X-HMAC-SHA256", signBridge(secret, ts, nonce, body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, request)
	return rec
}

func TestBridgeHMACCarriesAPageLargerThanEightMiBIntact(t *testing.T) {
	// A 12 MiB photo arrives here as roughly 16 MiB of base64. The old 8 MiB cap
	// refused that before the shop path could bound it, so a page the bridge had
	// just been told it may carry died as an authentication failure.
	secret := "bridge-test-secret"
	router, seen := bridgeRouter(secret)
	body := bytes.Repeat([]byte("a"), 9<<20)

	rec := postSigned(router, secret, body)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body must be carried, not refused)", rec.Code, http.StatusOK)
	}
	if *seen != len(body) {
		t.Fatalf("handler saw %d bytes, want %d: the body was truncated", *seen, len(body))
	}
}

func TestBridgeHMACRefusesABodyOverTheCapWithFourThirteen(t *testing.T) {
	secret := "bridge-test-secret"
	router, _ := bridgeRouter(secret)
	body := bytes.Repeat([]byte("a"), MaxInboundBodyBytes+1)

	rec := postSigned(router, secret, body)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d for a body over the cap", rec.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestBridgeHMACRejectsABadSignature(t *testing.T) {
	secret := "bridge-test-secret"
	router, _ := bridgeRouter(secret)
	body := []byte(`{"platform":"imessage","user_id":"+15551234567","text":"hey"}`)
	ts := strconv.FormatInt(time.Now().Unix(), 10)

	request := httptest.NewRequest(
		http.MethodPost, "/api/v1/platform/inbound", bytes.NewReader(body),
	)
	request.Header.Set("X-HMAC-Timestamp", ts)
	request.Header.Set("X-HMAC-Nonce", "nonce-bad-signature")
	request.Header.Set("X-HMAC-SHA256", signBridge("the-wrong-secret", ts, "nonce-bad-signature", body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, request)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}
