package vector

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	memory "github.com/rail-service/rail_service/internal/domain/services/ai/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// fakeEmbedder returns a fixed 3-dim vector so tests don't need a real model.
type fakeEmbedder struct{}

func (fakeEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	return []float32{0.1, 0.2, 0.3}, nil
}

func (fakeEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{0.1, 0.2, 0.3}
	}
	return out, nil
}

func newTestVectorize(t *testing.T, handler http.HandlerFunc) *VectorizeStore {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewVectorizeStore(&VectorizeConfig{
		AccountID:  "test-account",
		APIToken:   "test-token",
		DefaultDim: 3,
		BaseURL:    srv.URL,
	}, fakeEmbedder{}, zap.NewNop())
}

func TestVectorizeStore_Store_CreatesIndexOnDemand(t *testing.T) {
	userID := uuid.New()
	var requests []string
	store := newTestVectorize(t, func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/episodic":
			// Index missing on first check -> create below.
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"success":false,"errors":[{"message":"index not found"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/":
			_, _ = w.Write([]byte(`{"success":true,"result":{"id":"idx-1"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/episodic/upsert":
			_, _ = w.Write([]byte(`{"success":true,"result":{"ids":["p1"],"mutationId":"m1"}}`))
		default:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"success":false,"errors":[{"message":"unexpected call"}]}`))
		}
	})

	err := store.Store(context.Background(), "episodic", userID, "bought groceries", map[string]string{"category": "food"})
	require.NoError(t, err)

	assert.Equal(t, []string{
		"GET /episodic",
		"POST /",
		"POST /episodic/upsert",
	}, requests)
}

func TestVectorizeStore_Store_ExistingIndex(t *testing.T) {
	userID := uuid.New()
	var gotBody string
	store := newTestVectorize(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/episodic":
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(`{"success":true,"result":{"name":"episodic"}}`))
				return
			}
		case "/episodic/upsert":
			body := make([]byte, r.ContentLength)
			_, _ = r.Body.Read(body)
			gotBody = string(body)
			_, _ = w.Write([]byte(`{"success":true,"result":{"ids":["p1"],"mutationId":"m1"}}`))
			return
		}
		w.WriteHeader(http.StatusBadRequest)
	})

	err := store.Store(context.Background(), "episodic", userID, "hello", nil)
	require.NoError(t, err)

	var upsert struct {
		IDs      []string            `json:"ids"`
		Vectors  [][]float32         `json:"vectors"`
		Metadata []map[string]string `json:"metadata"`
	}
	require.NoError(t, json.Unmarshal([]byte(gotBody), &upsert))
	require.Len(t, upsert.IDs, 1)
	assert.Equal(t, userID.String(), upsert.Metadata[0]["user_id"])
	assert.Equal(t, "hello", upsert.Metadata[0]["content"])
	assert.Equal(t, []float32{0.1, 0.2, 0.3}, upsert.Vectors[0])
}

func TestVectorizeStore_Search(t *testing.T) {
	userID := uuid.New()
	var gotFilter map[string]string
	store := newTestVectorize(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost || r.URL.Path != "/episodic/query" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var body struct {
			Filter map[string]string `json:"filter"`
			TopK   int               `json:"top_k"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		gotFilter = body.Filter
		_, _ = w.Write([]byte(`{"success":true,"result":{"count":1,"matches":[` +
			`{"id":"p1","score":0.87,"metadata":{"content":"bought groceries","user_id":"` + userID.String() + `"}}]}}`))
	})

	results, err := store.Search(context.Background(), "episodic", userID, "groceries", 5)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "bought groceries", results[0].Content)
	assert.Equal(t, 0.87, results[0].Similarity)
	assert.Equal(t, "vectorize", results[0].Source)
	assert.Equal(t, userID.String(), gotFilter["user_id"])
}

func TestVectorizeStore_DeleteCollection(t *testing.T) {
	store := newTestVectorize(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodDelete && r.URL.Path == "/episodic" {
			_, _ = w.Write([]byte(`{"success":true,"result":{"name":"episodic"}}`))
			return
		}
		w.WriteHeader(http.StatusBadRequest)
	})
	require.NoError(t, store.DeleteCollection(context.Background(), "episodic"))
}

func TestVectorizeStore_DeleteCollection_AlreadyGone(t *testing.T) {
	store := newTestVectorize(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"success":false,"errors":[]}`))
			return
		}
		w.WriteHeader(http.StatusBadRequest)
	})
	require.NoError(t, store.DeleteCollection(context.Background(), "episodic"))
}

func TestVectorizeStore_Search_EmbedError(t *testing.T) {
	store := NewVectorizeStore(&VectorizeConfig{AccountID: "a", APIToken: "t"}, badEmbedder{}, zap.NewNop())
	_, err := store.Search(context.Background(), "episodic", uuid.New(), "q", 5)
	require.Error(t, err)
}

// badEmbedder always fails, for error-path coverage.
type badEmbedder struct{}

func (badEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	return nil, assert.AnError
}
func (badEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	return nil, assert.AnError
}

var _ memory.VectorStore = (*VectorizeStore)(nil)
