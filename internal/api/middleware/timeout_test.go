package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestTimeoutMiddlewareReturnsGatewayTimeoutOnOwnDeadline(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.Use(TimeoutMiddleware(5 * time.Millisecond))
	router.GET("/slow", func(c *gin.Context) {
		<-c.Request.Context().Done()
	})

	req := httptest.NewRequest(http.MethodGet, "/slow", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusGatewayTimeout, w.Code)
	assert.Contains(t, w.Body.String(), "REQUEST_TIMEOUT")
}

func TestTimeoutMiddlewareDoesNotConvertClientCancelToGatewayTimeout(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.Use(TimeoutMiddleware(time.Second))
	router.GET("/cancelled", func(c *gin.Context) {
		<-c.Request.Context().Done()
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/cancelled", nil).WithContext(ctx)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.NotEqual(t, http.StatusGatewayTimeout, w.Code)
	assert.Empty(t, w.Body.String())
}
