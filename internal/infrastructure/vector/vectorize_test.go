package vector

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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
	store, err := NewVectorizeStore(&VectorizeConfig{
		AccountID:  "test-account",
		APIToken:   "test-token",
		DefaultDim: 3,
		BaseURL:    srv.URL,
	}, fakeEmbedder{}, zap.NewNop())
	require.NoError(t, err)
	return store
}

func TestVectorizeStore_Store_CreatesIndexOnDemand(t *testing.T) {
	userID := uuid.New()
	var requests []string
	var upsertContentType string
	store := newTestVectorize(t, func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/episodic":
			// Index missing on first check -> create below.
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"success":false,"errors":[{"message":"index not found"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/":
			_, _ = w.Write([]byte(`{"success":true,"result":{"id":"idx-1","name":"episodic"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/episodic/metadata_index/create":
			_, _ = w.Write([]byte(`{"success":true,"result":{"mutationId":"mm1"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/episodic/upsert":
			upsertContentType = r.Header.Get("Content-Type")
			_, _ = w.Write([]byte(`{"success":true,"result":{"mutationId":"m1"}}`))
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
		"POST /episodic/metadata_index/create",
		"POST /episodic/upsert?unparsable-behavior=error",
	}, requests)
	assert.Equal(t, "application/x-ndjson", upsertContentType)
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
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			gotBody = string(body)
			_, _ = w.Write([]byte(`{"success":true,"result":{"mutationId":"m1"}}`))
			return
		}
		w.WriteHeader(http.StatusBadRequest)
	})

	err := store.Store(context.Background(), "episodic", userID, "hello", nil)
	require.NoError(t, err)

	// V2 upserts are NDJSON: exactly one JSON object per line.
	lines := bytes.Split([]byte(gotBody), []byte("\n"))
	require.Len(t, lines, 1)
	var line struct {
		ID       string            `json:"id"`
		Values   []float32         `json:"values"`
		Metadata map[string]string `json:"metadata"`
	}
	require.NoError(t, json.Unmarshal(lines[0], &line))
	assert.NotEmpty(t, line.ID)
	assert.Equal(t, userID.String(), line.Metadata["user_id"])
	assert.Equal(t, "hello", line.Metadata["content"])
	assert.Equal(t, []float32{0.1, 0.2, 0.3}, line.Values)
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
			Vector         []float32         `json:"vector"`
			TopK           int               `json:"topK"`
			ReturnMetadata string            `json:"returnMetadata"`
			Filter         map[string]string `json:"filter"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		gotFilter = body.Filter
		assert.Equal(t, "indexed", body.ReturnMetadata)
		assert.Equal(t, 5, body.TopK)
		assert.Equal(t, []float32{0.1, 0.2, 0.3}, body.Vector)
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

func TestVectorizeStore_Store_InvalidIndexName(t *testing.T) {
	store := newTestVectorize(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("no request should be sent for an invalid index name")
	})
	// Underscores are not allowed in Vectorize index names.
	err := store.Store(context.Background(), "prod_vectors", uuid.New(), "x", nil)
	require.Error(t, err)
}

func TestVectorizeStore_Search_EmbedError(t *testing.T) {
	store, err := NewVectorizeStore(&VectorizeConfig{AccountID: "a", APIToken: "t"}, badEmbedder{}, zap.NewNop())
	require.NoError(t, err)
	_, err = store.Search(context.Background(), "episodic", uuid.New(), "q", 5)
	require.Error(t, err)
}

func TestVectorizeStore_Constructor_RequiresCredentials(t *testing.T) {
	_, err := NewVectorizeStore(&VectorizeConfig{APIToken: "t"}, fakeEmbedder{}, zap.NewNop())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AccountID")
	_, err = NewVectorizeStore(&VectorizeConfig{AccountID: "a"}, fakeEmbedder{}, zap.NewNop())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "APIToken")
	_, err = NewVectorizeStore(nil, fakeEmbedder{}, zap.NewNop())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "VectorizeConfig is required")
}

func TestValidIndexName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"episodic", true},
		{"prod-episodic", true},
		{"prod_episodic", false},
		{"Prod-episodic", false},
		{"1episodic", false},
		{"-episodic", false},
		{"", false},
		{"this-name-is-way-too-long-to-be-a-valid-vectorize-index", false},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, validIndexName(tt.name), "validIndexName(%q)", tt.name)
	}
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
