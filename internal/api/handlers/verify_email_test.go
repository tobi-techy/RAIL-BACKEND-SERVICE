package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestVerifyEmail_RequiresCodeAndEmail(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name           string
		body           map[string]string
		expectedStatus int
		expectedError  string
	}{
		{
			name:           "Missing email and code",
			body:           map[string]string{},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "INVALID_REQUEST",
		},
		{
			name:           "Missing code",
			body:           map[string]string{"email": "test@example.com"},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "INVALID_REQUEST",
		},
		{
			name:           "Missing email",
			body:           map[string]string{"code": "123456"},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "INVALID_REQUEST",
		},
		{
			name:           "Invalid email format",
			body:           map[string]string{"email": "invalid", "code": "123456"},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "INVALID_REQUEST",
		},
		{
			name:           "Invalid code length",
			body:           map[string]string{"email": "test@example.com", "code": "123"},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "INVALID_REQUEST",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create handler with nil dependencies - will fail on validation before reaching them
			h := &AuthHandlers{}

			router := gin.New()
			router.POST("/verify-email", h.VerifyEmail)

			body, _ := json.Marshal(tt.body)
			req := httptest.NewRequest(http.MethodPost, "/verify-email", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")

			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.expectedError != "" {
				assert.Contains(t, w.Body.String(), tt.expectedError)
			}
		})
	}
}

func TestVerifyEmail_RejectsUserIdQueryParam(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Create handler with nil dependencies
	h := &AuthHandlers{}

	router := gin.New()
	router.POST("/verify-email", h.VerifyEmail)

	// Old insecure way - using user_id query param
	req := httptest.NewRequest(http.MethodPost, "/verify-email?user_id=some-uuid", nil)
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Should reject because no JSON body with email and code
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "INVALID_REQUEST")
}
