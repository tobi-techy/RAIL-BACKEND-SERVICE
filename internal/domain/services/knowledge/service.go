package knowledge

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

var (
	knowledgeCacheHits = promauto.NewCounter(prometheus.CounterOpts{
		Name: "rail_ai_knowledge_cache_hits_total",
		Help: "Knowledge base cache hits",
	})
	knowledgeCacheMisses = promauto.NewCounter(prometheus.CounterOpts{
		Name: "rail_ai_knowledge_cache_misses_total",
		Help: "Knowledge base cache misses",
	})
)

// EmbeddingProvider generates vector embeddings from text.
type EmbeddingProvider interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
}

// Repository stores and retrieves knowledge chunks.
type Repository interface {
	InsertChunk(ctx context.Context, chunk *entities.KnowledgeChunk) error
	Search(ctx context.Context, queryEmbedding []float32, limit int) ([]entities.KnowledgeSearchResult, error)
	DeleteBySource(ctx context.Context, sourceDoc string) error
}

// Cache provides key-value caching for response deduplication.
type Cache interface {
	Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error
	Get(ctx context.Context, key string, dest interface{}) error
}

// CachedSearchResult is what gets stored in and retrieved from the cache.
type CachedSearchResult struct {
	Results []entities.KnowledgeSearchResult `json:"results"`
}

const cacheTTL = 24 * time.Hour
const cachePrefix = "rag:"

// Service handles knowledge ingestion, search, and response caching.
type Service struct {
	repo      Repository
	embedder  EmbeddingProvider
	cache     Cache
	logger    *zap.Logger
}

// NewService creates a new knowledge service.
func NewService(repo Repository, embedder EmbeddingProvider, cache Cache, logger *zap.Logger) *Service {
	return &Service{repo: repo, embedder: embedder, cache: cache, logger: logger}
}

// Ingest chunks a document and stores embeddings. Replaces any existing
// chunks for the same source document.
func (s *Service) Ingest(ctx context.Context, sourceDoc, text string) (int, error) {
	chunks := chunkText(text, entities.ChunkSize, entities.ChunkOverlap)
	if len(chunks) == 0 {
		return 0, nil
	}

	// Generate embeddings first — if this fails, existing data is preserved
	embeddings, err := s.embedder.EmbedBatch(ctx, chunks)
	if err != nil {
		return 0, fmt.Errorf("generate embeddings: %w", err)
	}

	// Delete existing chunks only after embeddings succeed
	if err := s.repo.DeleteBySource(ctx, sourceDoc); err != nil {
		return 0, fmt.Errorf("delete existing: %w", err)
	}

	for i, chunk := range chunks {
		if err := s.repo.InsertChunk(ctx, &entities.KnowledgeChunk{
			ID:         uuid.New(),
			SourceDoc:  sourceDoc,
			ChunkIndex: i,
			ChunkText:  chunk,
			Embedding:  embeddings[i],
		}); err != nil {
			return i, fmt.Errorf("insert chunk %d: %w", i, err)
		}
	}

	s.logger.Info("knowledge ingested",
		zap.String("source", sourceDoc),
		zap.Int("chunks", len(chunks)),
	)
	return len(chunks), nil
}

// Search finds the most relevant knowledge chunks for a query.
// Results are cached by query hash for 24h.
func (s *Service) Search(ctx context.Context, query string, limit int) ([]entities.KnowledgeSearchResult, error) {
	if limit <= 0 {
		limit = 5
	}

	// Check cache
	cacheKey := cachePrefix + hashQuery(query)
	var cached CachedSearchResult
	if s.cache != nil {
		if err := s.cache.Get(ctx, cacheKey, &cached); err == nil && len(cached.Results) > 0 {
			s.logger.Debug("knowledge cache hit", zap.String("query", query))
			knowledgeCacheHits.Inc()
			return cached.Results, nil
		}
		knowledgeCacheMisses.Inc()
	}

	// Embed the query
	embedding, err := s.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}

	// Search pgvector
	results, err := s.repo.Search(ctx, embedding, limit)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}

	// Cache results
	if s.cache != nil && len(results) > 0 {
		if err := s.cache.Set(ctx, cacheKey, CachedSearchResult{Results: results}, cacheTTL); err != nil {
			s.logger.Warn("failed to cache search results", zap.Error(err))
		}
	}

	return results, nil
}

// hashQuery produces a short deterministic key for cache lookups.
func hashQuery(q string) string {
	h := sha256.Sum256([]byte(q))
	return fmt.Sprintf("%x", h[:16])
}

// chunkText splits text into overlapping chunks.
func chunkText(text string, size, overlap int) []string {
	if len(text) <= size {
		if len(text) == 0 {
			return nil
		}
		return []string{text}
	}

	var chunks []string
	step := size - overlap
	if step <= 0 {
		step = size
	}
	for i := 0; i < len(text); i += step {
		end := i + size
		if end > len(text) {
			end = len(text)
		}
		chunks = append(chunks, text[i:end])
		if end == len(text) {
			break
		}
	}
	return chunks
}
