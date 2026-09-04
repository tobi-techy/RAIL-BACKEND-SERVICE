package memory

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Service is the unified memory interface. Every memory operation goes through
// this — local facts, Supermemory, Qdrant, episodic — all behind one contract.
// Callers never need to know which backend stores what.
type Service interface {
	// ProcessExchange extracts facts, tone, and episodic memories from a
	// conversation turn. Called async after each exchange.
	ProcessExchange(ctx context.Context, userID uuid.UUID, userMsg, assistantMsg string) error

	// SearchMemory queries all available backends for memories relevant to the
	// given query. Returns merged, deduplicated results sorted by similarity.
	SearchMemory(ctx context.Context, userID uuid.UUID, query string, limit int) ([]Result, error)

	// SearchFacts queries the local fact store for structured facts.
	SearchFacts(ctx context.Context, userID uuid.UUID, query string, limit int) ([]Fact, error)

	// SearchEpisodic queries the episodic memory store (PGVector or Qdrant)
	// for conversation turns similar to the query.
	SearchEpisodic(ctx context.Context, userID uuid.UUID, query string, limit int) ([]Episode, error)

	// IngestConversation stores a conversation turn for later episodic retrieval.
	IngestConversation(ctx context.Context, userID uuid.UUID, userMsg, assistantMsg string) error

	// IngestMemory stores a discrete memory (insight, fact, event) directly.
	IngestMemory(ctx context.Context, userID uuid.UUID, content string, metadata map[string]string) error

	// Tone profile
	GetToneProfile(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error)
	UpsertToneProfile(ctx context.Context, userID uuid.UUID, profile map[string]interface{}) error

	// Personality mode
	GetPersonalityMode(ctx context.Context, userID uuid.UUID) string
	SetPersonalityMode(ctx context.Context, userID uuid.UUID, mode string) error

	// Control level
	GetControlLevel(ctx context.Context, userID uuid.UUID) (string, error)
	SetControlLevel(ctx context.Context, userID uuid.UUID, level string) error

	// Memory summary
	GetMemorySummary(ctx context.Context, userID uuid.UUID) (string, error)
	SummarizeMemory(ctx context.Context, userID uuid.UUID) error

	// Maintenance
	RunDecay(ctx context.Context) error
	SaveTransactionPattern(ctx context.Context, userID uuid.UUID, patternType string, data map[string]interface{}) error

	// User-facing memory management
	ListFacts(ctx context.Context, userID uuid.UUID) ([]Fact, error)
	DeleteFact(ctx context.Context, factID uuid.UUID) error
	DeleteFactsByCategory(ctx context.Context, userID uuid.UUID, category string) error
}

// Result is a unified search result from any memory backend.
type Result struct {
	Content    string
	Similarity float64
	Source     string // "fact", "episodic", "supermemory", "qdrant"
	Timestamp  time.Time
}

// Fact is a structured piece of knowledge about a user.
type Fact struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	Category   string
	Fact       string
	Source     string
	Confidence decimal.Decimal
	FirstSeen  time.Time
	LastSeen   time.Time
}

// Episode is a past conversation turn retrieved by similarity.
type Episode struct {
	Content    string
	Similarity float64
	Timestamp  time.Time
	Role       string // "user" or "assistant"
}

// --- Backend interfaces ---

// FactStore stores structured facts about users (the existing MiriamUserFacts
// table in PostgreSQL).
type FactStore interface {
	SaveFact(ctx context.Context, userID uuid.UUID, category, fact, source string, confidence decimal.Decimal) error
	GetActiveFacts(ctx context.Context, userID uuid.UUID) ([]Fact, error)
	GetActiveFactsByCategory(ctx context.Context, userID uuid.UUID, category string) ([]Fact, error)
	FindSimilarFacts(ctx context.Context, userID uuid.UUID, embedding []byte, threshold float64, limit int) ([]Fact, error)
	ConfirmFact(ctx context.Context, factID uuid.UUID) error
	DeleteFact(ctx context.Context, factID uuid.UUID) error
	DeleteFactsByCategory(ctx context.Context, userID uuid.UUID, category string) error
	SetFactEmbedding(ctx context.Context, factID uuid.UUID, embedding []byte) error
	DecayStaleFacts(ctx context.Context, olderThan time.Duration, decayFactor float64) error
	ExpireLowConfidenceFacts(ctx context.Context, minConfidence float64) error
	GetAllActiveUserIDs(ctx context.Context) ([]uuid.UUID, error)
	GetFactCount(ctx context.Context, userID uuid.UUID) (int, error)
}

// ToneStore stores per-user tone calibration profiles.
type ToneStore interface {
	GetToneProfile(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error)
	UpsertToneProfile(ctx context.Context, userID uuid.UUID, profile map[string]interface{}) error
}

// SummaryStore stores compressed memory summaries.
type SummaryStore interface {
	GetMemorySummary(ctx context.Context, userID uuid.UUID) (string, error)
	UpsertMemorySummary(ctx context.Context, userID uuid.UUID, summary string, factCount int) error
}

// EpisodicStore stores and searches conversation turns by vector similarity.
type EpisodicStore interface {
	Store(ctx context.Context, userID uuid.UUID, role, content string) error
	Search(ctx context.Context, userID uuid.UUID, query string, limit int) ([]Episode, error)
}

// ExternalMemoryStore is the interface for an external memory backend
// (Supermemory, Qdrant, etc.).
type ExternalMemoryStore interface {
	IngestConversation(ctx context.Context, userID string, userMsg, assistantMsg string) error
	SearchMemory(ctx context.Context, userID, query string, limit int) ([]Result, error)
	CreateMemories(ctx context.Context, containerTag string, memories []MemoryEntry) error
}

// MemoryEntry is a single memory to write to Supermemory or Qdrant.
type MemoryEntry struct {
	Content      string
	Metadata     map[string]string
	DocumentDate string
	EventDate    []string
}

// VectorStore is a generic vector similarity search interface (Qdrant, PGVector, etc.).
type VectorStore interface {
	Store(ctx context.Context, collection string, userID uuid.UUID, content string, metadata map[string]string) error
	Search(ctx context.Context, collection string, userID uuid.UUID, query string, limit int) ([]Result, error)
	DeleteCollection(ctx context.Context, collection string) error
}

// Embedder generates vector embeddings from text.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
}
