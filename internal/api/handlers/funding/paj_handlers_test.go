package funding

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/paj"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestHandleSessionErrorMapsPajUnauthorizedToVerificationRequired(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	handler := &PajHandlers{logger: zap.NewNop()}
	err := fmt.Errorf("paj create onramp order: %w", &paj.APIError{
		StatusCode: http.StatusUnauthorized,
		Body:       `{"message":"Session is invalid or expired","error":"Unauthorized","statusCode":401}`,
		Path:       "/pub/onramp",
	})

	handler.handleSessionError(c, err)

	require.Equal(t, http.StatusForbidden, rec.Code)

	var body map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, "PAJ_VERIFICATION_REQUIRED", body["code"])
}
