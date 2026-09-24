package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestRequireRailServiceKey(t *testing.T) {
	// Valid key passes through to the handler.
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/x", nil)
	c.Request.Header.Set("X-Rail-Service-Key", "shared-secret-32-chars-minimum-xxx")
	RequireRailServiceKey("shared-secret-32-chars-minimum-xxx", zap.NewNop())(c)
	require.False(t, c.IsAborted(), "valid key must pass")

	// Wrong key is refused.
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Request = httptest.NewRequest(http.MethodPost, "/x", nil)
	c2.Request.Header.Set("X-Rail-Service-Key", "wrong")
	RequireRailServiceKey("shared-secret-32-chars-minimum-xxx", zap.NewNop())(c2)
	require.True(t, c2.IsAborted())
	require.Equal(t, http.StatusUnauthorized, w2.Code)

	// Missing key is refused.
	w3 := httptest.NewRecorder()
	c3, _ := gin.CreateTestContext(w3)
	c3.Request = httptest.NewRequest(http.MethodPost, "/x", nil)
	RequireRailServiceKey("shared-secret-32-chars-minimum-xxx", zap.NewNop())(c3)
	require.True(t, c3.IsAborted())
	require.Equal(t, http.StatusUnauthorized, w3.Code)

	// Unconfigured endpoint fails closed (unavailable, not open).
	w4 := httptest.NewRecorder()
	c4, _ := gin.CreateTestContext(w4)
	c4.Request = httptest.NewRequest(http.MethodPost, "/x", nil)
	c4.Request.Header.Set("X-Rail-Service-Key", "anything")
	RequireRailServiceKey("", zap.NewNop())(c4)
	require.True(t, c4.IsAborted())
	require.Equal(t, http.StatusServiceUnavailable, w4.Code)
}
