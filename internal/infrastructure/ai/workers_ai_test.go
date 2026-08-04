package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func newTestClassifier(t *testing.T, handler http.HandlerFunc) *WorkersAIIntentClassifier {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	client := NewWorkersAIClient(&WorkersAIConfig{
		AccountID: "test-account",
		APIToken:  "test-token",
		Model:     "@cf/meta/llama-3.1-8b-instruct",
		BaseURL:   srv.URL,
	}, zap.NewNop())

	return NewWorkersAIIntentClassifier(&IntentClassifierConfig{
		Client:  client,
		Model:   "@cf/meta/llama-3.1-8b-instruct",
		Timeout: time.Second,
	}, zap.NewNop())
}

// serveJSON returns a handler that records the auth header and request body and
// replies with the given JSON.
func serveJSON(t *testing.T, body string, lastReq *http.Request, lastBody *string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if lastReq != nil {
			*lastReq = *r
		}
		if lastBody != nil {
			b, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read request body: %v", err)
			}
			*lastBody = string(b)
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(body)); err != nil {
			t.Errorf("write response: %v", err)
		}
	}
}

func TestWorkersAIIntentClassifier_Success(t *testing.T) {
	var lastReq http.Request
	var lastBody string
	classifier := newTestClassifier(t, serveJSON(t,
		`{"success":true,"result":{"response":"{\"category\":\"Action\",\"confidence\":0.92}"}}`,
		&lastReq, &lastBody,
	))

	cat, conf, ok := classifier.Classify(context.Background(), "move $50 to my stash")
	assert.True(t, ok)
	assert.Equal(t, IntentAction, cat)
	assert.Equal(t, 0.92, conf)

	// Verify auth header was attached.
	assert.Equal(t, "Bearer test-token", lastReq.Header.Get("Authorization"))

	// Verify the classifier forced JSON output.
	var reqBody struct {
		Messages       []WorkerAIMessage `json:"messages"`
		ResponseFormat struct {
			Type string `json:"type"`
		} `json:"response_format"`
		MaxTokens int `json:"max_tokens"`
	}
	require.NoError(t, json.Unmarshal([]byte(lastBody), &reqBody))
	assert.Equal(t, "json_object", reqBody.ResponseFormat.Type)
	assert.Len(t, reqBody.Messages, 2)
	assert.Equal(t, "user", reqBody.Messages[1].Role)
}

func TestWorkersAIIntentClassifier_NonJSON(t *testing.T) {
	classifier := newTestClassifier(t, serveJSON(t,
		`{"success":true,"result":{"response":"Sorry, I could not classify."}}`,
		nil, nil,
	))
	cat, conf, ok := classifier.Classify(context.Background(), "hello there")
	assert.False(t, ok)
	assert.Equal(t, IntentCategory(""), cat)
	assert.Equal(t, 0.0, conf)
}

func TestWorkersAIIntentClassifier_UnknownCategory(t *testing.T) {
	classifier := newTestClassifier(t, serveJSON(t,
		`{"success":true,"result":{"response":"{\"category\":\"TimeTravel\",\"confidence\":0.99}"}}`,
		nil, nil,
	))
	_, _, ok := classifier.Classify(context.Background(), "take me to 1999")
	assert.False(t, ok)
}

func TestWorkersAIIntentClassifier_LowConfidence(t *testing.T) {
	classifier := newTestClassifier(t, serveJSON(t,
		`{"success":true,"result":{"response":"{\"category\":\"Spending\",\"confidence\":0.3}"}}`,
		nil, nil,
	))
	_, _, ok := classifier.Classify(context.Background(), "hmm money things")
	assert.False(t, ok)
}

func TestWorkersAIIntentClassifier_APIClosed(t *testing.T) {
	classifier := newTestClassifier(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"success":false,"errors":[]}`))
	})
	_, _, ok := classifier.Classify(context.Background(), "move 50 dollars")
	assert.False(t, ok)
}

func TestWorkersAIIntentClassifier_NilClient(t *testing.T) {
	classifier := NewWorkersAIIntentClassifier(&IntentClassifierConfig{}, zap.NewNop())
	_, _, ok := classifier.Classify(context.Background(), "hi")
	assert.False(t, ok)
}

func TestWorkersAIIntentClassifier_VoiceCategory(t *testing.T) {
	classifier := newTestClassifier(t, serveJSON(t,
		`{"success":true,"result":{"response":"{\"category\":\"Voice\",\"confidence\":0.88}"}}`,
		nil, nil,
	))
	cat, conf, ok := classifier.Classify(context.Background(), "use my voice assistant")
	assert.True(t, ok)
	assert.Equal(t, IntentVoice, cat)
	assert.Equal(t, 0.88, conf)
}

func TestWorkersAIClient_Constructor_RequiresCredentials(t *testing.T) {
	assert.Panics(t, func() {
		NewWorkersAIClient(&WorkersAIConfig{APIToken: "tok"}, zap.NewNop())
	})
	assert.Panics(t, func() {
		NewWorkersAIClient(&WorkersAIConfig{AccountID: "acct"}, zap.NewNop())
	})
	assert.Panics(t, func() {
		NewWorkersAIClient(nil, zap.NewNop())
	})
}
