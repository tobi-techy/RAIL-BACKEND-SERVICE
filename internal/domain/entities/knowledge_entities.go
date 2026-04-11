package entities

import (
	"time"

	"github.com/google/uuid"
)

// KnowledgeChunk represents a text chunk with its embedding stored in pgvector.
type KnowledgeChunk struct {
	ID         uuid.UUID              `json:"id" db:"id"`
	SourceDoc  string                 `json:"source_doc" db:"source_doc"`
	ChunkIndex int                    `json:"chunk_index" db:"chunk_index"`
	ChunkText  string                 `json:"chunk_text" db:"chunk_text"`
	Embedding  []float32              `json:"-" db:"embedding"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt  time.Time              `json:"created_at" db:"created_at"`
}

// KnowledgeSearchResult is a chunk with its similarity score.
type KnowledgeSearchResult struct {
	KnowledgeChunk
	Similarity float64 `json:"similarity"`
}

// EmbeddingDimension is the vector size for OpenAI text-embedding-3-small.
const EmbeddingDimension = 1536

// ChunkSize is the target character count per text chunk for ingestion.
const ChunkSize = 1500

// ChunkOverlap is the character overlap between consecutive chunks.
const ChunkOverlap = 200
