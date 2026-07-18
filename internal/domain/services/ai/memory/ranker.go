package memory

import (
	"encoding/binary"
	"math"
	"sort"
	"time"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

// Ranker scores and ranks memory facts by composite relevance.
// The formula balances four signals:
//
//	score = (similarity × 0.30) + (importance × 0.25) + (recency × 0.25) + (confidence × 0.20)
//
// This ensures the most relevant, important, fresh, and reliable facts
// appear first in the prompt — not just the most recent or most similar.
type Ranker struct{}

// ScoredFact pairs a fact with its composite relevance score.
type ScoredFact struct {
	Fact  *entities.MiriamUserFact
	Score float64
}

// RankFacts scores and ranks facts by composite relevance to the current message.
// queryEmbedding is the embedding of the user's current message (may be nil if
// embeddings are unavailable — in that case similarity defaults to 0.5).
// now is the current time for recency calculations.
// Returns facts sorted highest-score-first, capped at limit.
func (r *Ranker) RankFacts(facts []*entities.MiriamUserFact, queryEmbedding []float32, now time.Time, limit int) []*entities.MiriamUserFact {
	if len(facts) == 0 {
		return nil
	}

	scored := make([]ScoredFact, 0, len(facts))
	for _, f := range facts {
		sf := ScoredFact{
			Fact:  f,
			Score: r.scoreFact(f, queryEmbedding, now),
		}
		scored = append(scored, sf)
	}

	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})

	if limit > 0 && len(scored) > limit {
		scored = scored[:limit]
	}

	out := make([]*entities.MiriamUserFact, len(scored))
	for i, sf := range scored {
		out[i] = sf.Fact
	}
	return out
}

// scoreFact computes the composite score for a single fact.
func (r *Ranker) scoreFact(f *entities.MiriamUserFact, queryEmbedding []float32, now time.Time) float64 {
	similarity := 0.5 // default when no embedding available
	if len(queryEmbedding) > 0 && len(f.Embedding) > 0 {
		// Convert []byte embedding to []float32 for comparison.
		factEmb := bytesToFloat32(f.Embedding)
		similarity = cosineSimilarity(queryEmbedding, factEmb)
	}

	importance := float64(f.Importance) / 10.0
	recency := r.recencyScore(f.LastConfirmedAt, now)
	confidence := f.Confidence.InexactFloat64()

	return (similarity * 0.30) + (importance * 0.25) + (recency * 0.25) + (confidence * 0.20)
}

// recencyScore returns 1.0 for facts confirmed today, decaying linearly to 0.0 at 365 days.
func (r *Ranker) recencyScore(lastConfirmed, now time.Time) float64 {
	ageDays := now.Sub(lastConfirmed).Hours() / 24.0
	if ageDays < 0 {
		ageDays = 0
	}
	if ageDays <= 7 {
		return 1.0
	}
	if ageDays >= 365 {
		return 0.0
	}
	return 1.0 - (ageDays-7)/358.0
}

// cosineSimilarity computes cosine similarity between two embedding vectors.
// Both must be the same dimension. Returns 0 if either is empty.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (sqrt(normA) * sqrt(normB))
}

func sqrt(x float64) float64 {
	if x <= 0 {
		return 0
	}
	z := x / 2
	for i := 0; i < 10; i++ {
		z = (z + x/z) / 2
	}
	return z
}

// bytesToFloat32 converts a byte slice to float32 slice.
func bytesToFloat32(b []byte) []float32 {
	if len(b)%4 != 0 {
		return nil
	}
	result := make([]float32, len(b)/4)
	for i := range result {
		bits := binary.LittleEndian.Uint32(b[i*4 : i*4+4])
		result[i] = math.Float32frombits(bits)
	}
	return result
}
