package funding

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/rail-service/rail_service/internal/domain/entities"
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

func TestHandleSessionErrorMapsDepositLimitExceededToBadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	handler := &PajHandlers{logger: zap.NewNop()}

	handler.handleSessionError(c, entities.ErrDailyDepositExceeded)

	require.Equal(t, http.StatusBadRequest, rec.Code)

	var body map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, "LIMIT_EXCEEDED", body["code"])
	require.Equal(t, "daily deposit limit exceeded", body["message"])
}

func TestHandleSessionErrorMapsWrappedDepositLimitExceededToBadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	handler := &PajHandlers{logger: zap.NewNop()}
	err := fmt.Errorf("paj create onramp order: %w", entities.ErrDailyDepositExceeded)

	handler.handleSessionError(c, err)

	require.Equal(t, http.StatusBadRequest, rec.Code)

	var body map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, "LIMIT_EXCEEDED", body["code"])
	require.Equal(t, "paj create onramp order: daily deposit limit exceeded", body["message"])
}
