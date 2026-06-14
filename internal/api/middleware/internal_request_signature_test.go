package middleware

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// sign produces the wire-format HMAC over the canonical payload the middleware
// expects. rawQuery is the raw URL query string (no leading '?'), or "" if none.
func sign(secret, ts, method, path, rawQuery, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(fmt.Appendf(nil, "%s.%s.%s.%s.%s", ts, method, path, rawQuery, body))
	return hex.EncodeToString(mac.Sum(nil))
}

func newSignedRouter(secret string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(InternalRequestSignature(secret, nil))
	r.POST("/internal/test", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	return r
}

func TestInternalRequestSignature_NoOpWhenUnset(t *testing.T) {
	r := newSignedRouter("")
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/internal/test", strings.NewReader(`{"a":1}`))
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 when signing disabled, got %d", w.Code)
	}
}

func TestInternalRequestSignature_ValidSignaturePasses(t *testing.T) {
	secret := "test-internal-signing-secret"
	body := `{"amount":"100"}`
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	r := newSignedRouter(secret)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/internal/test", strings.NewReader(body))
	req.Header.Set("X-Internal-Timestamp", ts)
	req.Header.Set("X-Internal-Signature", sign(secret, ts, http.MethodPost, "/internal/test", "", body))
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for valid signature, got %d (%s)", w.Code, w.Body.String())
	}
}

func TestInternalRequestSignature_RejectsMissingHeaders(t *testing.T) {
	r := newSignedRouter("secret")
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/internal/test", strings.NewReader(`{}`))
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for missing headers, got %d", w.Code)
	}
}

func TestInternalRequestSignature_RejectsTamperedBody(t *testing.T) {
	secret := "secret"
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	r := newSignedRouter(secret)

	w := httptest.NewRecorder()
	// Signature computed over original body, but a different body is sent.
	req := httptest.NewRequest(http.MethodPost, "/internal/test", strings.NewReader(`{"amount":"999"}`))
	req.Header.Set("X-Internal-Timestamp", ts)
	req.Header.Set("X-Internal-Signature", sign(secret, ts, http.MethodPost, "/internal/test", "", `{"amount":"1"}`))
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for tampered body, got %d", w.Code)
	}
}

func TestInternalRequestSignature_RejectsStaleTimestamp(t *testing.T) {
	secret := "secret"
	body := `{}`
	ts := strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10)
	r := newSignedRouter(secret)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/internal/test", strings.NewReader(body))
	req.Header.Set("X-Internal-Timestamp", ts)
	req.Header.Set("X-Internal-Signature", sign(secret, ts, http.MethodPost, "/internal/test", "", body))
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for stale timestamp, got %d", w.Code)
	}
}

func TestInternalRequestSignature_RejectsWrongSecret(t *testing.T) {
	body := `{}`
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	r := newSignedRouter("real-secret")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/internal/test", strings.NewReader(body))
	req.Header.Set("X-Internal-Timestamp", ts)
	req.Header.Set("X-Internal-Signature", sign("attacker-secret", ts, http.MethodPost, "/internal/test", "", body))
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for wrong secret, got %d", w.Code)
	}
}

// TestInternalRequestSignature_RejectsOversizedBody guards against the
// previous LimitReader truncation bug: an attacker who learns the cap could
// otherwise append unsigned bytes past it, and the middleware would happily
// verify the signature over the truncated prefix. After the fix the request
// is rejected outright.
func TestInternalRequestSignature_RejectsOversizedBody(t *testing.T) {
	secret := "secret"
	// One byte past the documented cap (5 MiB).
	body := strings.Repeat("a", 5*1024*1024+1)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	r := newSignedRouter(secret)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/internal/test", strings.NewReader(body))
	req.Header.Set("X-Internal-Timestamp", ts)
	req.Header.Set("X-Internal-Signature", sign(secret, ts, http.MethodPost, "/internal/test", "", body))
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for oversized body, got %d", w.Code)
	}
}

// TestInternalRequestSignature_RejectsTamperedQueryParams confirms the
// canonical payload binds query parameters, so an attacker who captures a
// signed POST cannot mutate ?foo=… to pivot the request semantically.
func TestInternalRequestSignature_RejectsTamperedQueryParams(t *testing.T) {
	secret := "secret"
	body := `{}`
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	r := newSignedRouter(secret)
	r.GET("/internal/test", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	// Signature computed for ?account=A, request sent with ?account=B.
	req := httptest.NewRequest(http.MethodGet, "/internal/test?account=B", strings.NewReader(body))
	req.Header.Set("X-Internal-Timestamp", ts)
	req.Header.Set("X-Internal-Signature", sign(secret, ts, http.MethodGet, "/internal/test", "account=A", body))
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for tampered query string, got %d", w.Code)
	}
}

// TestInternalRequestSignature_AllowsReplayWithinWindow documents — rather
// than fixes — that this middleware does NOT provide replay protection on
// its own. Any captured signed request is replayable until its timestamp
// drifts outside ±internalSignatureMaxSkewSeconds. Money-moving internal
// routes MUST add application-layer idempotency on top. See SECURITY notes
// on internalSignatureMaxSkewSeconds in the middleware file.
func TestInternalRequestSignature_AllowsReplayWithinWindow(t *testing.T) {
	secret := "secret"
	body := `{"amount":"1"}`
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	r := newSignedRouter(secret)

	build := func() *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/internal/test", strings.NewReader(body))
		req.Header.Set("X-Internal-Timestamp", ts)
		req.Header.Set("X-Internal-Signature", sign(secret, ts, http.MethodPost, "/internal/test", "", body))
		return req
	}

	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, build())
		if w.Code != http.StatusOK {
			t.Fatalf("attempt %d: expected 200 (middleware-level replay is permitted by design), got %d", i+1, w.Code)
		}
	}
}
