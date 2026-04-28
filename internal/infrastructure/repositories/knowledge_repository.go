package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// KnowledgeRepository handles persistence for knowledge embeddings.
type KnowledgeRepository struct {
	db     *sql.DB
	logger *zap.Logger
}

// NewKnowledgeRepository creates a new knowledge repository.
func NewKnowledgeRepository(db *sql.DB, logger *zap.Logger) *KnowledgeRepository {
	return &KnowledgeRepository{db: db, logger: logger}
}

// vectorToString formats a float32 slice as a pgvector literal: [0.1,0.2,...]
func vectorToString(v []float32) string {
	parts := make([]string, len(v))
	for i, f := range v {
		parts[i] = fmt.Sprintf("%g", f)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// InsertChunk stores a knowledge chunk with its embedding.
func (r *KnowledgeRepository) InsertChunk(ctx context.Context, chunk *entities.KnowledgeChunk) error {
	metadataJSON, _ := json.Marshal(chunk.Metadata)
	if chunk.ID == uuid.Nil {
		chunk.ID = uuid.New()
	}
	chunk.CreatedAt = time.Now()

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO knowledge_embeddings (id, source_doc, chunk_index, chunk_text, embedding, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5::vector, $6, $7)`,
		chunk.ID, chunk.SourceDoc, chunk.ChunkIndex, chunk.ChunkText,
		vectorToString(chunk.Embedding), metadataJSON, chunk.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert knowledge chunk: %w", err)
	}
	return nil
}

// Search performs cosine similarity search and returns the top-K most similar chunks.
func (r *KnowledgeRepository) Search(ctx context.Context, queryEmbedding []float32, limit int) ([]entities.KnowledgeSearchResult, error) {
	if limit <= 0 || limit > 20 {
		limit = 5
	}

	query := `
		SELECT id, source_doc, chunk_index, chunk_text, metadata, created_at,
		       1 - (embedding <=> $1::vector) AS similarity
		FROM knowledge_embeddings
		ORDER BY embedding <=> $1::vector
		LIMIT $2`

	rows, err := r.db.QueryContext(ctx, query, vectorToString(queryEmbedding), limit)
	if err != nil {
		return nil, fmt.Errorf("knowledge search: %w", err)
	}
	defer rows.Close()

	var results []entities.KnowledgeSearchResult
	for rows.Next() {
		var sr entities.KnowledgeSearchResult
		var metadataJSON []byte
		if err := rows.Scan(
			&sr.ID, &sr.SourceDoc, &sr.ChunkIndex, &sr.ChunkText,
			&metadataJSON, &sr.CreatedAt, &sr.Similarity,
		); err != nil {
			return nil, fmt.Errorf("scan knowledge result: %w", err)
		}
		if len(metadataJSON) > 0 {
			_ = json.Unmarshal(metadataJSON, &sr.Metadata)
		}
		results = append(results, sr)
	}
	return results, rows.Err()
}

// DeleteBySource removes all chunks for a given source document.
func (r *KnowledgeRepository) DeleteBySource(ctx context.Context, sourceDoc string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM knowledge_embeddings WHERE source_doc = $1`, sourceDoc)
	if err != nil {
		return fmt.Errorf("delete knowledge by source: %w", err)
	}
	return nil
}
