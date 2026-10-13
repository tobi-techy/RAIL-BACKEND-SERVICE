package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func serveInternalKeyHeader(apiKey, presented string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/x", InternalKeyHeaderAuth(apiKey), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	if presented != "" {
		req.Header.Set("X-Internal-Key", presented)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestInternalKeyHeaderAuth(t *testing.T) {
	const key = "internal-ops-key-0123456789abcdef0123"

	if w := serveInternalKeyHeader(key, key); w.Code != http.StatusNoContent {
		t.Fatalf("correct key: status %d, want 204", w.Code)
	}
	if w := serveInternalKeyHeader(key, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("missing header: status %d, want 401", w.Code)
	}
	if w := serveInternalKeyHeader(key, "some-other-secret-0123456789abcdef"); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong key: status %d, want 401", w.Code)
	}
}

func TestInternalKeyHeaderAuthFailsClosedWhenUnconfigured(t *testing.T) {
	for _, presented := range []string{"", "anything"} {
		if w := serveInternalKeyHeader("", presented); w.Code != http.StatusServiceUnavailable {
			t.Fatalf("unconfigured key, presented %q: status %d, want 503", presented, w.Code)
		}
	}
}
