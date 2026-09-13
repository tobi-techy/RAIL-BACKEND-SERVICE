package funding

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestPayBillRequiresAuthAndIdempotencyKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewBillPayHandlers(nil, zap.NewNop())

	t.Run("unauthenticated", func(t *testing.T) {
		router := gin.New()
		router.POST("/pay", h.PayBill)
		req := httptest.NewRequest(http.MethodPost, "/pay", bytes.NewBufferString(`{"category":"airtime","recipient":"0801","amount_ngn":100}`))
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		require.Equal(t, http.StatusUnauthorized, res.Code)
	})

	t.Run("missing idempotency key", func(t *testing.T) {
		router := gin.New()
		router.POST("/pay", func(c *gin.Context) {
			c.Set("user_id", uuid.New())
			h.PayBill(c)
		})
		req := httptest.NewRequest(http.MethodPost, "/pay", bytes.NewBufferString(`{"category":"airtime","recipient":"08012345678","amount_ngn":100}`))
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		require.Equal(t, http.StatusBadRequest, res.Code)
		require.Contains(t, res.Body.String(), "Idempotency")
	})
}

func TestListProvidersRequiresCategory(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewBillPayHandlers(nil, zap.NewNop())
	router := gin.New()
	router.GET("/providers", func(c *gin.Context) {
		c.Set("user_id", uuid.New())
		h.ListProviders(c)
	})
	req := httptest.NewRequest(http.MethodGet, "/providers", nil)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	require.Equal(t, http.StatusBadRequest, res.Code)
}
