package embeddings

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func newTestWorkersAIEmbedder(t *testing.T, handler http.HandlerFunc) *WorkersAIEmbedder {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewWorkersAIEmbedder("test-account", "test-token", "@cf/baai/bge-base-en-v1.5", srv.URL, zap.NewNop())
}

func TestWorkersAIEmbedder_Embed_NativeFormat(t *testing.T) {
	embedder := newTestWorkersAIEmbedder(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/ai/run/@cf/baai/bge-base-en-v1.5", r.URL.Path)
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"success":true,"result":{"data":[[0.1,0.2,0.3]]}}`))
	})

	vec, err := embedder.Embed(context.Background(), "bought groceries")
	require.NoError(t, err)
	assert.Equal(t, []float32{0.1, 0.2, 0.3}, vec)
}

func TestWorkersAIEmbedder_Embed_OpenAIFormat(t *testing.T) {
	embedder := newTestWorkersAIEmbedder(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"result":{"data":[{"embedding":[0.5,0.6,0.7]}]}}`))
	})

	vec, err := embedder.Embed(context.Background(), "hello")
	require.NoError(t, err)
	assert.Equal(t, []float32{0.5, 0.6, 0.7}, vec)
}

func TestWorkersAIEmbedder_Embed_MalformedResponse(t *testing.T) {
	embedder := newTestWorkersAIEmbedder(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"result":{}}`))
	})
	_, err := embedder.Embed(context.Background(), "hello")
	require.Error(t, err)
}

func TestWorkersAIEmbedder_Embed_HTTPError(t *testing.T) {
	embedder := newTestWorkersAIEmbedder(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"success":false,"errors":[{"message":"no access"}]}`))
	})
	_, err := embedder.Embed(context.Background(), "hello")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "403")
}

func TestWorkersAIEmbedder_EmbedBatch(t *testing.T) {
	embedder := newTestWorkersAIEmbedder(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"result":{"data":[[0.1,0.2],[0.3,0.4]]}}`))
	})

	vecs, err := embedder.EmbedBatch(context.Background(), []string{"a", "b"})
	require.NoError(t, err)
	require.Len(t, vecs, 2)
	assert.Equal(t, []float32{0.1, 0.2}, vecs[0])
	assert.Equal(t, []float32{0.3, 0.4}, vecs[1])
}
